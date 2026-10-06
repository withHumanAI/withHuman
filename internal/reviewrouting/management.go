// Package reviewrouting owns reviewer routing: the administrative control
// plane for escalation path revisions, and the handoff step that pins
// which path a request that needs a human goes to. Documents are
// validated before every write that could make them durable or active.
package reviewrouting

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/authorization"
	"github.com/withHumanAI/withHuman/routing"
)

// TeamRosterReader is the slice of the team repository the path editor
// needs to warn about a team target's roster.
type TeamRosterReader interface {
	GetTeam(ctx context.Context, input application.GetTeamInput) (domain.Team, []application.TeamMemberRecord, error)
	CountTeamMembersLacking(ctx context.Context, input application.CountTeamMembersLackingInput) (application.TeamMemberPermissionCount, error)
}

type ManagementService struct {
	paths application.EscalationPathRepository
	teams TeamRosterReader
	clock application.Clock
}

func NewManagementService(paths application.EscalationPathRepository, teams TeamRosterReader, clock application.Clock) *ManagementService {
	return &ManagementService{paths: paths, teams: teams, clock: clock}
}

// PathRevision pairs a stored revision with its decoded document.
// Warnings are advisory findings about the document, computed when a
// revision is created or activated and never refused on: the path
// narrows who decides, it never grants, so a team target whose members
// cannot decide, or a branch layout that leaves some request with nobody
// to decide it, is something the editor should say out loud.
type PathRevision struct {
	Revision domain.EscalationPathRevision
	Document routing.PathDocument
	Warnings []PathWarning
}

// PathWarningTeamMembersCannotDecide says Count of the team's MemberCount
// members hold no request.decide grant, so routing to the team reaches
// fewer people than its roster suggests.
const PathWarningTeamMembersCannotDecide = "team_members_cannot_decide"

// PathWarningMayReachNobody says some request's conditions could leave
// no level to run: such a request reaches nobody and only a break-glass
// reviewer can decide it. It carries no team fields.
const PathWarningMayReachNobody = "path_may_reach_nobody"

// PathWarning is one advisory finding about the document. The team
// fields are set only for findings about a team target.
type PathWarning struct {
	Code        string
	TeamID      domain.ID
	TeamName    string
	Count       int
	MemberCount int
}

// ListPaths lists the organization's paths: the live ones by default,
// the archived ones, or both.
func (service *ManagementService) ListPaths(ctx context.Context, principal domain.Principal, status application.EscalationPathStatus, limit int) ([]domain.EscalationPathSummary, error) {
	if err := authorizePathManagement(principal, domain.PermissionEscalationPathRead); err != nil {
		return nil, err
	}
	return service.paths.ListEscalationPathSummaries(ctx, application.ListEscalationPathSummariesInput{
		OrganizationID: principal.OrganizationID,
		Status:         status,
		Limit:          limit,
	})
}

func (service *ManagementService) GetActivePath(ctx context.Context, principal domain.Principal, pathKey string) (PathRevision, error) {
	if err := authorizePathManagement(principal, domain.PermissionEscalationPathRead); err != nil {
		return PathRevision{}, err
	}
	if err := validatePathKey(pathKey); err != nil {
		return PathRevision{}, err
	}
	stored, err := service.paths.GetActiveEscalationPathRevision(ctx, application.GetActiveEscalationPathRevisionInput{
		OrganizationID: principal.OrganizationID,
		PathKey:        pathKey,
	})
	if err != nil {
		return PathRevision{}, err
	}
	return decodedPathRevision(stored)
}

// ListPathRevisions returns one page of the key's history, newest first;
// cursor is the previous page's NextCursor, empty for the first page.
func (service *ManagementService) ListPathRevisions(ctx context.Context, principal domain.Principal, pathKey string, limit int, cursor string) (application.RevisionPage[domain.EscalationPathRevision], error) {
	var page application.RevisionPage[domain.EscalationPathRevision]
	if err := authorizePathManagement(principal, domain.PermissionEscalationPathRead); err != nil {
		return page, err
	}
	if err := validatePathKey(pathKey); err != nil {
		return page, err
	}
	return service.paths.ListEscalationPathRevisions(ctx, application.ListEscalationPathRevisionsInput{
		OrganizationID: principal.OrganizationID,
		PathKey:        pathKey,
		Limit:          limit,
		Cursor:         cursor,
	})
}

func (service *ManagementService) GetPathRevision(ctx context.Context, principal domain.Principal, pathKey string, revision int64) (PathRevision, error) {
	if err := authorizePathManagement(principal, domain.PermissionEscalationPathRead); err != nil {
		return PathRevision{}, err
	}
	if err := validatePathKey(pathKey); err != nil {
		return PathRevision{}, err
	}
	stored, err := service.paths.GetEscalationPathRevision(ctx, application.GetEscalationPathRevisionInput{
		OrganizationID: principal.OrganizationID,
		PathKey:        pathKey,
		Revision:       revision,
	})
	if err != nil {
		return PathRevision{}, err
	}
	return decodedPathRevision(stored)
}

// CreatePath creates the path with the document as its first, inactive
// revision. A key that already has a live revision refuses with
// domain.ErrEscalationPathExists.
func (service *ManagementService) CreatePath(ctx context.Context, principal domain.Principal, pathKey, name string, document routing.PathDocument) (PathRevision, error) {
	input, warnings, err := service.revisionInput(ctx, principal, pathKey, name, document)
	if err != nil {
		return PathRevision{}, err
	}
	stored, err := service.paths.CreateEscalationPath(ctx, application.CreateEscalationPathInput(input))
	if err != nil {
		return PathRevision{}, err
	}
	return PathRevision{Revision: stored, Document: document, Warnings: warnings}, nil
}

// CreatePathRevision appends the next inactive revision to a live path;
// an unknown key is domain.ErrNotFound and an archived one
// domain.ErrEscalationPathArchived.
func (service *ManagementService) CreatePathRevision(ctx context.Context, principal domain.Principal, pathKey, name string, document routing.PathDocument) (PathRevision, error) {
	input, warnings, err := service.revisionInput(ctx, principal, pathKey, name, document)
	if err != nil {
		return PathRevision{}, err
	}
	stored, err := service.paths.CreateEscalationPathRevision(ctx, input)
	if err != nil {
		return PathRevision{}, err
	}
	return PathRevision{Revision: stored, Document: document, Warnings: warnings}, nil
}

// revisionInput authorizes and validates one document, targets included,
// canonicalizes it for storage, and computes its advisory warnings.
func (service *ManagementService) revisionInput(ctx context.Context, principal domain.Principal, pathKey, name string, document routing.PathDocument) (application.CreateEscalationPathRevisionInput, []PathWarning, error) {
	if err := authorizePathManagement(principal, domain.PermissionEscalationPathWrite); err != nil {
		return application.CreateEscalationPathRevisionInput{}, nil, err
	}
	if err := validatePathKey(pathKey); err != nil {
		return application.CreateEscalationPathRevisionInput{}, nil, err
	}
	if err := document.Validate(); err != nil {
		return application.CreateEscalationPathRevisionInput{}, nil, invalidRoutingDocument("escalation path document", err)
	}
	if err := service.requireResolvableTargets(ctx, principal.OrganizationID, document); err != nil {
		return application.CreateEscalationPathRevisionInput{}, nil, err
	}
	warnings, err := service.teamWarnings(ctx, principal.OrganizationID, document)
	if err != nil {
		return application.CreateEscalationPathRevisionInput{}, nil, err
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return application.CreateEscalationPathRevisionInput{}, nil, err
	}
	return application.CreateEscalationPathRevisionInput{
		ID:               domain.ID(uuid.NewString()),
		OrganizationID:   principal.OrganizationID,
		PathKey:          pathKey,
		Name:             name,
		Document:         canonical,
		DocumentVersion:  routing.DocumentVersion,
		CreatedByActorID: principal.ManagementActorID(),
		Now:              service.clock.Now(),
	}, warnings, nil
}

func (service *ManagementService) ActivatePathRevision(ctx context.Context, principal domain.Principal, pathKey string, revision int64, expectedActiveRevision *int64) (PathRevision, error) {
	if err := authorizePathManagement(principal, domain.PermissionEscalationPathActivate); err != nil {
		return PathRevision{}, err
	}
	target, err := service.GetPathRevision(ctx, principal, pathKey, revision)
	if err != nil {
		return PathRevision{}, err
	}
	if err := service.requireResolvableTargets(ctx, principal.OrganizationID, target.Document); err != nil {
		return PathRevision{}, err
	}
	if target.Warnings, err = service.teamWarnings(ctx, principal.OrganizationID, target.Document); err != nil {
		return PathRevision{}, err
	}
	activated, err := service.paths.ActivateEscalationPathRevision(ctx, application.ActivateEscalationPathRevisionInput{
		OrganizationID:         principal.OrganizationID,
		PathKey:                pathKey,
		Revision:               revision,
		ExpectedActiveRevision: expectedActiveRevision,
		ActorID:                principal.ManagementActorID(),
		Now:                    service.clock.Now(),
	})
	if err != nil {
		return PathRevision{}, err
	}
	target.Revision = activated
	return target, nil
}

// ListPathUses reports the active pipeline revisions that name the path.
// The same set refuses ArchivePath, so the page can explain a refusal
// before the click.
func (service *ManagementService) ListPathUses(ctx context.Context, principal domain.Principal, pathKey string) ([]domain.PipelineUse, error) {
	if err := authorizePathManagement(principal, domain.PermissionEscalationPathRead); err != nil {
		return nil, err
	}
	if err := validatePathKey(pathKey); err != nil {
		return nil, err
	}
	return service.paths.ListEscalationPathUses(ctx, application.ListEscalationPathUsesInput{
		OrganizationID: principal.OrganizationID,
		PathKey:        pathKey,
	})
}

// ArchivePath archives the path, revisions included. The path stays
// readable and requests already routed keep the revision they pinned,
// but it is never active again and pipelines can no longer name it.
func (service *ManagementService) ArchivePath(ctx context.Context, principal domain.Principal, pathKey string) error {
	if err := authorizePathManagement(principal, domain.PermissionEscalationPathActivate); err != nil {
		return err
	}
	if err := validatePathKey(pathKey); err != nil {
		return err
	}
	_, err := service.paths.ArchiveEscalationPath(ctx, application.ArchiveEscalationPathInput{
		OrganizationID: principal.OrganizationID,
		PathKey:        pathKey,
		ActorID:        principal.ManagementActorID(),
		Now:            service.clock.Now(),
	})
	return err
}

// requireResolvableTargets rejects documents whose targets do not exist:
// user targets must be active memberships and team targets existing teams.
// The check is advisory (targets carry no foreign key); later deletion is
// blocked by the team archive guard, and deactivated members are filtered
// at notification and decide time. It deliberately walks every branch
// (document.Targets, the union): the editor has no request in hand, so
// which branches a request reaches is unknown here.
func (service *ManagementService) requireResolvableTargets(ctx context.Context, organizationID domain.ID, document routing.PathDocument) error {
	input := application.ResolveRoutingTargetsInput{OrganizationID: organizationID}
	for _, target := range document.Targets() {
		switch target.Type {
		case routing.TargetUser:
			input.UserMembershipIDs = append(input.UserMembershipIDs, domain.ID(target.ID))
		case routing.TargetTeam:
			input.TeamIDs = append(input.TeamIDs, domain.ID(target.ID))
		}
	}
	if len(input.UserMembershipIDs) == 0 && len(input.TeamIDs) == 0 {
		return nil
	}
	resolved, err := service.paths.ResolveRoutingTargets(ctx, input)
	if err != nil {
		return err
	}
	if len(resolved.MissingUsers) > 0 {
		return invalidRoutingDocument("some people in this path are no longer active members", nil)
	}
	if len(resolved.MissingTeams) > 0 {
		return invalidRoutingDocument("some teams in this path no longer exist", nil)
	}
	return nil
}

// teamWarnings checks every distinct team target's roster against RBAC:
// a member the path names who holds no request.decide grant can be
// notified but never decide. The finding is a warning, never a refusal,
// because grants change independently of the path and the path editor
// is the place to say it. Like requireResolvableTargets it walks the
// union of every branch, since no request is in hand.
func (service *ManagementService) teamWarnings(ctx context.Context, organizationID domain.ID, document routing.PathDocument) ([]PathWarning, error) {
	var warnings []PathWarning
	seen := make(map[domain.ID]struct{})
	for _, target := range document.Targets() {
		teamID := domain.ID(target.ID)
		if target.Type != routing.TargetTeam {
			continue
		}
		if _, done := seen[teamID]; done {
			continue
		}
		seen[teamID] = struct{}{}
		count, err := service.teams.CountTeamMembersLacking(ctx, application.CountTeamMembersLackingInput{
			OrganizationID: organizationID, TeamID: teamID, Permission: domain.PermissionRequestDecide,
		})
		if err != nil {
			return nil, err
		}
		if count.LackingCount == 0 {
			continue
		}
		team, _, err := service.teams.GetTeam(ctx, application.GetTeamInput{OrganizationID: organizationID, TeamID: teamID})
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, PathWarning{
			Code: PathWarningTeamMembersCannotDecide, TeamID: teamID, TeamName: team.Name,
			Count: count.LackingCount, MemberCount: count.MemberCount,
		})
	}
	return append(warnings, reachabilityWarnings(document)...), nil
}

// reachabilityWarnings says when some request's conditions could leave
// no level to run: such a request is routed to nobody, and only a
// break-glass reviewer can decide it. The one request-aware finding the
// editor makes, computed over the document alone.
func reachabilityWarnings(document routing.PathDocument) []PathWarning {
	if !document.MayReachNobody() {
		return nil
	}
	return []PathWarning{{Code: PathWarningMayReachNobody}}
}

// authorizePathManagement admits a member holding the permission on the
// organization: escalation paths are organization resources.
func authorizePathManagement(principal domain.Principal, permission domain.Permission) error {
	if !domain.ManagementPrincipal(principal) {
		return domain.ErrForbidden
	}
	return authorization.Check(principal, permission, domain.OrganizationResource())
}

func validatePathKey(pathKey string) error {
	if !routing.ValidPathKey(pathKey) {
		return invalidRoutingDocument("path_key must match ^[a-z0-9][a-z0-9._-]{0,62}$", nil)
	}
	return nil
}

func invalidRoutingDocument(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%s: %w", message, domain.ErrInvalid)
	}
	return fmt.Errorf("%s: %v: %w", message, cause, domain.ErrInvalid)
}

func decodedPathRevision(stored domain.EscalationPathRevision) (PathRevision, error) {
	if stored.DocumentVersion != routing.DocumentVersion {
		return PathRevision{}, fmt.Errorf("stored escalation path document version %d is unsupported", stored.DocumentVersion)
	}
	var document routing.PathDocument
	if err := routing.Decode(stored.Document, &document); err != nil {
		return PathRevision{}, fmt.Errorf("stored escalation path document is unreadable: %w", err)
	}
	return PathRevision{Revision: stored, Document: document}, nil
}
