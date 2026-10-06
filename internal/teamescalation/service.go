// Package teamescalation owns the administrative control plane for team
// escalation policy revisions: the node program a team runs when an
// organization path level hands a request to it. A policy may only name
// people on its own team, so every write and activation checks the
// document against the team's current roster.
package teamescalation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/authorization"
	"github.com/withHumanAI/withHuman/routing"
)

type Service struct {
	policies application.TeamEscalationPolicyRepository
	teams    application.TeamRepository
	clock    application.Clock
}

func NewService(policies application.TeamEscalationPolicyRepository, teams application.TeamRepository, clock application.Clock) *Service {
	return &Service{policies: policies, teams: teams, clock: clock}
}

// PolicyRevision pairs a stored revision with its decoded document.
// Warnings are advisory findings about the document against the team's
// roster and grants. They are computed but never refused on: the roster
// and RBAC change independently of the policy, and the policy editor is
// the place to say so.
type PolicyRevision struct {
	Revision domain.TeamEscalationPolicyRevision
	Document routing.TeamPolicyDocument
	Warnings []Warning
}

const (
	// WarningTeamMembersCannotDecide says Count of the team's MemberCount
	// members hold no request.decide grant, so a level reaches fewer
	// deciders than the roster suggests.
	WarningTeamMembersCannotDecide = "team_members_cannot_decide"
	// WarningTargetNotMember says a user target the policy names is no
	// longer an active member of the team, so the level skips them.
	WarningTargetNotMember = "target_not_member"
)

// Warning is one advisory finding. MembershipID and DisplayName describe
// the target of a target_not_member finding; Count and MemberCount
// quantify a team_members_cannot_decide finding.
type Warning struct {
	Code         string
	MembershipID domain.ID
	DisplayName  string
	Count        int
	MemberCount  int
}

// GetActivePolicy reads the team's active revision with the findings a
// page should show beside it: targets who left the team and members who
// cannot decide.
func (service *Service) GetActivePolicy(ctx context.Context, principal domain.Principal, teamID domain.ID) (PolicyRevision, error) {
	if err := authorize(principal, domain.PermissionTeamRead, teamID); err != nil {
		return PolicyRevision{}, err
	}
	stored, err := service.policies.GetActiveTeamEscalationPolicyRevision(ctx, application.GetActiveTeamEscalationPolicyRevisionInput{
		OrganizationID: principal.OrganizationID,
		TeamID:         teamID,
	})
	if err != nil {
		return PolicyRevision{}, err
	}
	revision, err := decodedPolicyRevision(stored)
	if err != nil {
		return PolicyRevision{}, err
	}
	members, err := service.roster(ctx, principal.OrganizationID, teamID)
	if err != nil {
		return PolicyRevision{}, err
	}
	revision.Warnings = targetWarnings(revision.Document, members)
	deciders, err := service.deciderWarning(ctx, principal.OrganizationID, teamID)
	if err != nil {
		return PolicyRevision{}, err
	}
	revision.Warnings = append(revision.Warnings, deciders...)
	return revision, nil
}

func (service *Service) ListRevisions(ctx context.Context, principal domain.Principal, teamID domain.ID, limit int) ([]domain.TeamEscalationPolicyRevision, error) {
	if err := authorize(principal, domain.PermissionTeamRead, teamID); err != nil {
		return nil, err
	}
	return service.policies.ListTeamEscalationPolicyRevisions(ctx, application.ListTeamEscalationPolicyRevisionsInput{
		OrganizationID: principal.OrganizationID,
		TeamID:         teamID,
		Limit:          limit,
	})
}

// GetRevision reads one revision, active or not, warning about user
// targets who are no longer on the team so a rollback candidate can be
// judged before it is activated.
func (service *Service) GetRevision(ctx context.Context, principal domain.Principal, teamID domain.ID, revisionNumber int64) (PolicyRevision, error) {
	if err := authorize(principal, domain.PermissionTeamRead, teamID); err != nil {
		return PolicyRevision{}, err
	}
	revision, err := service.loadRevision(ctx, principal.OrganizationID, teamID, revisionNumber)
	if err != nil {
		return PolicyRevision{}, err
	}
	members, err := service.roster(ctx, principal.OrganizationID, teamID)
	if err != nil {
		return PolicyRevision{}, err
	}
	revision.Warnings = targetWarnings(revision.Document, members)
	return revision, nil
}

func (service *Service) CreateRevision(ctx context.Context, principal domain.Principal, teamID domain.ID, document routing.TeamPolicyDocument) (PolicyRevision, error) {
	if err := authorize(principal, domain.PermissionTeamEscalationWrite, teamID); err != nil {
		return PolicyRevision{}, err
	}
	if err := document.Validate(); err != nil {
		return PolicyRevision{}, invalidPolicyDocument(err)
	}
	if err := service.requireTeamTargets(ctx, principal.OrganizationID, teamID, document); err != nil {
		return PolicyRevision{}, err
	}
	warnings, err := service.deciderWarning(ctx, principal.OrganizationID, teamID)
	if err != nil {
		return PolicyRevision{}, err
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return PolicyRevision{}, err
	}
	stored, err := service.policies.CreateTeamEscalationPolicyRevision(ctx, application.CreateTeamEscalationPolicyRevisionInput{
		ID:               domain.ID(uuid.NewString()),
		OrganizationID:   principal.OrganizationID,
		TeamID:           teamID,
		Document:         canonical,
		DocumentVersion:  routing.DocumentVersion,
		CreatedByActorID: principal.ManagementActorID(),
		Now:              service.clock.Now(),
	})
	if err != nil {
		return PolicyRevision{}, err
	}
	return PolicyRevision{Revision: stored, Document: document, Warnings: warnings}, nil
}

// ActivateRevision re-checks the stored document against the roster as
// it stands now: a rollback to a revision naming someone who has since
// left the team is refused the same way a new revision would be.
func (service *Service) ActivateRevision(ctx context.Context, principal domain.Principal, teamID domain.ID, revisionNumber int64, expectedActiveRevision *int64) (PolicyRevision, error) {
	if err := authorize(principal, domain.PermissionTeamEscalationActivate, teamID); err != nil {
		return PolicyRevision{}, err
	}
	target, err := service.loadRevision(ctx, principal.OrganizationID, teamID, revisionNumber)
	if err != nil {
		return PolicyRevision{}, err
	}
	if err := service.requireTeamTargets(ctx, principal.OrganizationID, teamID, target.Document); err != nil {
		return PolicyRevision{}, err
	}
	if target.Warnings, err = service.deciderWarning(ctx, principal.OrganizationID, teamID); err != nil {
		return PolicyRevision{}, err
	}
	activated, err := service.policies.ActivateTeamEscalationPolicyRevision(ctx, application.ActivateTeamEscalationPolicyRevisionInput{
		OrganizationID:         principal.OrganizationID,
		TeamID:                 teamID,
		Revision:               revisionNumber,
		ExpectedActiveRevision: expectedActiveRevision,
		ActorID:                principal.ManagementActorID(),
		Now:                    service.clock.Now(),
	})
	if err != nil {
		return PolicyRevision{}, err
	}
	target.Revision = activated
	return target, nil
}

// Deactivate archives the active revision. Nothing refuses it: a path
// level that targets the team simply falls back to notifying every
// member at once, which is what the team had before any policy.
func (service *Service) Deactivate(ctx context.Context, principal domain.Principal, teamID domain.ID, expectedActiveRevision int64) error {
	if err := authorize(principal, domain.PermissionTeamEscalationActivate, teamID); err != nil {
		return err
	}
	_, err := service.policies.DeactivateTeamEscalationPolicy(ctx, application.DeactivateTeamEscalationPolicyInput{
		OrganizationID:         principal.OrganizationID,
		TeamID:                 teamID,
		ExpectedActiveRevision: expectedActiveRevision,
		ActorID:                principal.ManagementActorID(),
		Now:                    service.clock.Now(),
	})
	return err
}

func (service *Service) loadRevision(ctx context.Context, organizationID, teamID domain.ID, revisionNumber int64) (PolicyRevision, error) {
	stored, err := service.policies.GetTeamEscalationPolicyRevision(ctx, application.GetTeamEscalationPolicyRevisionInput{
		OrganizationID: organizationID,
		TeamID:         teamID,
		Revision:       revisionNumber,
	})
	if err != nil {
		return PolicyRevision{}, err
	}
	return decodedPolicyRevision(stored)
}

// roster reads the team's members; a missing team surfaces as not found
// so every policy route answers 404 for a team that does not exist.
func (service *Service) roster(ctx context.Context, organizationID, teamID domain.ID) ([]application.TeamMemberRecord, error) {
	_, members, err := service.teams.GetTeam(ctx, application.GetTeamInput{OrganizationID: organizationID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	return members, nil
}

// requireTeamTargets refuses a document naming anyone who is not an
// active member of the team. Nobody outside a team is ever reached
// through its policy, so accepting the target would only create a level
// that silently skips them.
func (service *Service) requireTeamTargets(ctx context.Context, organizationID, teamID domain.ID, document routing.TeamPolicyDocument) error {
	members, err := service.roster(ctx, organizationID, teamID)
	if err != nil {
		return err
	}
	active := activeMembers(members)
	var missing []string
	for _, membershipID := range document.UserTargetIDs() {
		if _, member := active[domain.ID(membershipID)]; !member {
			missing = append(missing, membershipID)
		}
	}
	if len(missing) > 0 {
		return invalidPolicyDocument(errors.New("everyone in the policy must be on the team"))
	}
	return nil
}

// deciderWarning asks how many of the team's members hold no
// request.decide grant. The policy narrows who is asked within the team,
// it never grants, so this is reported rather than refused.
func (service *Service) deciderWarning(ctx context.Context, organizationID, teamID domain.ID) ([]Warning, error) {
	count, err := service.teams.CountTeamMembersLacking(ctx, application.CountTeamMembersLackingInput{
		OrganizationID: organizationID, TeamID: teamID, Permission: domain.PermissionRequestDecide,
	})
	if err != nil {
		return nil, err
	}
	if count.LackingCount == 0 {
		return nil, nil
	}
	return []Warning{{Code: WarningTeamMembersCannotDecide, Count: count.LackingCount, MemberCount: count.MemberCount}}, nil
}

// targetWarnings names every user target who is no longer an active
// member. The display name is known only while the roster still lists
// the person under another status; a membership gone from the roster is
// reported by id alone.
func targetWarnings(document routing.TeamPolicyDocument, members []application.TeamMemberRecord) []Warning {
	active := activeMembers(members)
	names := make(map[domain.ID]string, len(members))
	for _, member := range members {
		names[member.MembershipID] = member.DisplayName
	}
	var warnings []Warning
	for _, membershipID := range document.UserTargetIDs() {
		id := domain.ID(membershipID)
		if _, member := active[id]; member {
			continue
		}
		warnings = append(warnings, Warning{Code: WarningTargetNotMember, MembershipID: id, DisplayName: names[id]})
	}
	return warnings
}

func activeMembers(members []application.TeamMemberRecord) map[domain.ID]struct{} {
	active := make(map[domain.ID]struct{}, len(members))
	for _, member := range members {
		if member.Status == domain.MembershipActive {
			active[member.MembershipID] = struct{}{}
		}
	}
	return active
}

// authorize admits a member holding the permission at the team: policies
// are team resources, so a team-scoped grant covers its own team only.
func authorize(principal domain.Principal, permission domain.Permission, teamID domain.ID) error {
	if !domain.ManagementPrincipal(principal) {
		return domain.ErrForbidden
	}
	return authorization.Check(principal, permission, domain.TeamResource(teamID))
}

func invalidPolicyDocument(cause error) error {
	return fmt.Errorf("%v: %w", cause, domain.ErrInvalid)
}

func decodedPolicyRevision(stored domain.TeamEscalationPolicyRevision) (PolicyRevision, error) {
	if stored.DocumentVersion != routing.DocumentVersion {
		return PolicyRevision{}, fmt.Errorf("stored team escalation policy document version %d is unsupported", stored.DocumentVersion)
	}
	var document routing.TeamPolicyDocument
	if err := routing.Decode(stored.Document, &document); err != nil {
		return PolicyRevision{}, fmt.Errorf("stored team escalation policy document is unreadable: %w", err)
	}
	return PolicyRevision{Revision: stored, Document: document}, nil
}
