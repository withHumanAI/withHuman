// Package onboarding runs the guided setup:
// the session both the browser and the CLI advance, the objects
// provisioning writes, and the agent enrollment the CLI collects. Human
// writes carry the person's own principal. CLI writes carry the session
// the whb_ bearer resolved to; only Enroll turns that into a principal,
// rebuilt from the membership at that moment, because agent credentials
// must be minted with a person's authority and handed over once.
package onboarding

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/agentruntime"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/approvalblock"
	"github.com/withHumanAI/withHuman/authn"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/approvalpipeline"
	"github.com/withHumanAI/withHuman/internal/authorization"
	"github.com/withHumanAI/withHuman/internal/reviewrouting"
	"github.com/withHumanAI/withHuman/internal/toolsuggest"
	"github.com/withHumanAI/withHuman/routing"
)

// ErrProvisionFailed wraps a failure while writing the generated objects.
// The session stays where it was with last_error set, so the person can
// retry from the page.
var ErrProvisionFailed = errors.New("onboarding provisioning failed")

// Config is the service's deployment configuration.
type Config struct {
	PublicOrigin   string
	CLIDownloadURL string
	SessionTTL     time.Duration
	SetupCodeTTL   time.Duration
}

// AgentMinter is the slice of authn.Service enrollment needs.
type AgentMinter interface {
	CreateAgent(ctx context.Context, principal domain.Principal, name string, agentSlug domain.AgentSlug, kind domain.AgentKind) (domain.Agent, error)
	CreateAgentInstanceCredential(ctx context.Context, principal domain.Principal, agentID domain.ID, name string, metadata json.RawMessage) (domain.AgentInstanceCredential, error)
}

// PipelineManager is the slice of the pipeline management service the
// setup needs, keyed by pipeline: the setup writes the entry pipeline
// alone.
type PipelineManager interface {
	GetActive(ctx context.Context, principal domain.Principal, pipelineKey string) (approvalpipeline.PipelineRevision, error)
	CreateRevision(ctx context.Context, principal domain.Principal, pipelineKey, name string, definitions []approvalblock.Definition, defaultRoute approvalpipeline.Route) (approvalpipeline.PipelineRevision, error)
	Activate(ctx context.Context, principal domain.Principal, pipelineKey string, revision int64, expectedActiveRevision int64) (approvalpipeline.PipelineRevision, error)
	Preview(ctx context.Context, principal domain.Principal, pipelineKey string, definitions []approvalblock.Definition, defaultRoute approvalpipeline.Route, sample approvalpipeline.PreviewSample) (approvalpipeline.PreviewResult, error)
}

// agentStatuses are the statuses in which the agents exist, so their
// rules can be written.
var agentStatuses = map[domain.OnboardingStatus]bool{
	domain.OnboardingProvisioning: true, domain.OnboardingEnrolled: true,
	domain.OnboardingInstalled: true, domain.OnboardingTested: true,
}

// ErrEntryPipelineChanged means the entry pipeline changed between the
// setup's read and its write; the page retries from a fresh read.
var ErrEntryPipelineChanged = errors.New("the entry pipeline changed while the rules were being written")

// PathManager is the slice of the escalation path management service
// provisioning needs.
type PathManager interface {
	GetActivePath(ctx context.Context, principal domain.Principal, pathKey string) (reviewrouting.PathRevision, error)
	CreatePath(ctx context.Context, principal domain.Principal, pathKey, name string, document routing.PathDocument) (reviewrouting.PathRevision, error)
	CreatePathRevision(ctx context.Context, principal domain.Principal, pathKey, name string, document routing.PathDocument) (reviewrouting.PathRevision, error)
	ActivatePathRevision(ctx context.Context, principal domain.Principal, pathKey string, revision int64, expectedActiveRevision *int64) (reviewrouting.PathRevision, error)
}

// ApprovalReader is the one approvals read the flow needs: checking that
// the request the person says they decided is real, theirs, and decided.
type ApprovalReader interface {
	GetApproval(ctx context.Context, input application.GetApprovalInput) (domain.ApprovalRequest, error)
}

type Service struct {
	sessions   application.OnboardingRepository
	identities application.IdentityRepository
	agents     application.AgentRepository
	roles      application.RoleRepository
	approvals  ApprovalReader
	minter     AgentMinter
	pipelines  PipelineManager
	paths      PathManager
	clock      application.Clock
	logger     *slog.Logger
	config     Config
}

func NewService(sessions application.OnboardingRepository, identities application.IdentityRepository, agents application.AgentRepository, roles application.RoleRepository,
	approvals ApprovalReader, minter AgentMinter, pipelines PipelineManager, paths PathManager, clock application.Clock, logger *slog.Logger, config Config) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if config.SessionTTL <= 0 {
		config.SessionTTL = time.Hour
	}
	if config.SetupCodeTTL <= 0 {
		config.SetupCodeTTL = 10 * time.Minute
	}
	return &Service{sessions: sessions, identities: identities, agents: agents, roles: roles, approvals: approvals, minter: minter,
		pipelines: pipelines, paths: paths, clock: clock, logger: logger, config: config}
}

// Authorized reports whether a principal may run the guided setup.
func Authorized(principal domain.Principal) bool {
	if principal.ActorType != domain.ActorHuman || principal.MembershipID == "" {
		return false
	}
	for _, permission := range domain.OnboardingPermissions {
		if !authorization.HoldsAnywhere(principal, permission) {
			return false
		}
	}
	// The setup writes the entry pipeline, an organization-wide act: a
	// grant scoped to one pipeline does not reach it.
	for _, permission := range []domain.Permission{domain.PermissionPipelineWrite, domain.PermissionPipelineActivate} {
		if authorization.Check(principal, permission, domain.OrganizationResource()) != nil {
			return false
		}
	}
	return true
}

func authorize(principal domain.Principal) error {
	if !Authorized(principal) {
		return domain.ErrForbidden
	}
	return nil
}

// requireHuman admits a person holding the permission on the resource.
func requireHuman(principal domain.Principal, permission domain.Permission, resource domain.Resource) error {
	if principal.ActorType != domain.ActorHuman {
		return domain.ErrForbidden
	}
	return authorization.Check(principal, permission, resource)
}

// requireMember additionally needs a membership, because the write is
// attributed to it.
func requireMember(principal domain.Principal, permission domain.Permission, resource domain.Resource) error {
	if principal.MembershipID == "" {
		return domain.ErrForbidden
	}
	return requireHuman(principal, permission, resource)
}

// BeginResult is what the page shows: the session plus the one-time code
// inside the single command to paste, which installs the CLI and runs
// its setup in one go.
type BeginResult struct {
	Session      domain.OnboardingSession
	SetupCode    string
	SetupCommand string
}

const setupCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newSetupCode() (string, error) {
	var groups []string
	for group := 0; group < 3; group++ {
		var builder strings.Builder
		for index := 0; index < 4; index++ {
			position, err := rand.Int(rand.Reader, big.NewInt(int64(len(setupCodeAlphabet))))
			if err != nil {
				return "", err
			}
			builder.WriteByte(setupCodeAlphabet[position.Int64()])
		}
		groups = append(groups, builder.String())
	}
	return strings.Join(groups, "-"), nil
}

// NormalizeSetupCode makes a typed or pasted code canonical: upper case,
// separators dropped, then regrouped as three groups of four when the
// twelve characters are all there.
func NormalizeSetupCode(code string) string {
	cleaned := strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "_", "").Replace(strings.TrimSpace(code)))
	if len(cleaned) != 12 {
		return cleaned
	}
	return cleaned[0:4] + "-" + cleaned[4:8] + "-" + cleaned[8:12]
}

// Begin opens a session for the person, superseding any open one.
func (s *Service) Begin(ctx context.Context, principal domain.Principal) (BeginResult, error) {
	if err := authorize(principal); err != nil {
		return BeginResult{}, err
	}
	code, err := newSetupCode()
	if err != nil {
		return BeginResult{}, err
	}
	now := s.clock.Now()
	session, err := s.sessions.CreateOnboardingSession(ctx, application.CreateOnboardingSessionInput{
		Session: domain.OnboardingSession{
			ID: domain.ID(uuid.NewString()), OrganizationID: principal.OrganizationID, MembershipID: principal.MembershipID,
			Status: domain.OnboardingAwaitingCLI, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(s.config.SessionTTL),
		},
		SetupCodeHash:      authn.HashToken(code),
		SetupCodeExpiresAt: now.Add(s.config.SetupCodeTTL),
	})
	if err != nil {
		return BeginResult{}, err
	}
	return BeginResult{
		Session:      session,
		SetupCode:    code,
		SetupCommand: setupCommand(s.config.CLIDownloadURL, s.config.PublicOrigin, code),
	}, nil
}

func setupCommand(downloadURL, origin, code string) string {
	downloadURL = strings.TrimRight(downloadURL, "/")
	mirror := ""
	if downloadURL != "https://downloads.withhuman.ai" {
		mirror = "WITHHUMAN_CLI_BASE=" + shellQuote(downloadURL) + " "
	}
	return fmt.Sprintf("curl -fsSL %s | %ssh -s -- onboard --url %s --code %s",
		shellQuote(downloadURL+"/install.sh"), mirror, shellQuote(strings.TrimRight(origin, "/")), shellQuote(code))
}

func shellQuote(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_./:@%-", r))
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// Get returns the person's open session.
func (s *Service) Get(ctx context.Context, principal domain.Principal) (domain.OnboardingSession, error) {
	if err := authorize(principal); err != nil {
		return domain.OnboardingSession{}, err
	}
	return s.sessions.GetOpenOnboardingSession(ctx, application.GetOpenOnboardingSessionInput{
		OrganizationID: principal.OrganizationID, MembershipID: principal.MembershipID,
	})
}

// Status reports whether the organization still needs onboarding from
// this person's point of view: nobody has finished or skipped it, and
// they hold the authority to run it.
func (s *Service) Required(ctx context.Context, principal domain.Principal) (required bool, completedAt *time.Time, err error) {
	organization, err := s.identities.GetOrganization(ctx, application.GetOrganizationInput{OrganizationID: principal.OrganizationID})
	if err != nil {
		return false, nil, err
	}
	return organization.OnboardingCompletedAt == nil && Authorized(principal), organization.OnboardingCompletedAt, nil
}

// SelectAgents records which detected runtimes the person wants
// protected. Re-selecting while tools are already discovered sends the
// CLI back to discovery.
func (s *Service) SelectAgents(ctx context.Context, principal domain.Principal, runtimes []string) (domain.OnboardingSession, error) {
	if err := authorize(principal); err != nil {
		return domain.OnboardingSession{}, err
	}
	session, err := s.Get(ctx, principal)
	if err != nil {
		return domain.OnboardingSession{}, err
	}
	selected := Union(runtimes)
	if len(selected) == 0 {
		return domain.OnboardingSession{}, fmt.Errorf("select at least one agent: %w", domain.ErrInvalid)
	}
	for _, runtime := range selected {
		detection, ok := session.Detection(runtime)
		if !agentruntime.Valid(agentruntime.Key(runtime)) || !ok || !detection.Installed {
			return domain.OnboardingSession{}, &StepError{Code: "runtime_not_detected", Param: runtime,
				Err: fmt.Errorf("%s was not detected on this machine: %w", runtime, domain.ErrInvalid)}
		}
	}
	tools := map[string][]domain.OnboardingTool{}
	for _, runtime := range selected {
		if discovered, ok := session.Tools[runtime]; ok {
			tools[runtime] = discovered
		}
	}
	return s.sessions.UpdateOnboardingSession(ctx, application.UpdateOnboardingSessionInput{
		OrganizationID: session.OrganizationID, SessionID: session.ID,
		ExpectedStatuses: []domain.OnboardingStatus{domain.OnboardingDetected, domain.OnboardingAgentsSelected, domain.OnboardingToolsDiscovered},
		Patch:            domain.OnboardingSessionPatch{SelectedRuntimes: &selected, Tools: &tools, ClearLastError: true},
		NextStatus:       domain.OnboardingAgentsSelected,
		Now:              s.clock.Now(),
	})
}

// StepError carries the code the page keys its messages on and the
// parameter it concerns.
type StepError struct {
	Code  string
	Param string
	Err   error
}

func (e *StepError) Error() string { return e.Err.Error() }
func (e *StepError) Unwrap() error { return e.Err }

// Provision writes the generated objects under the person's own
// authority: the path, one agent per selected runtime, and one branch per
// runtime in the entry pipeline holding its starting rules. It needs the person to have passed
// the notification steps, because the path it writes notifies them. On
// failure the session keeps its status and records the error.
func (s *Service) Provision(ctx context.Context, principal domain.Principal) (domain.OnboardingSession, error) {
	if err := authorize(principal); err != nil {
		return domain.OnboardingSession{}, err
	}
	session, err := s.Get(ctx, principal)
	if err != nil {
		return domain.OnboardingSession{}, err
	}
	if session.Status != domain.OnboardingToolsDiscovered {
		return domain.OnboardingSession{}, domain.ErrOnboardingState
	}
	if session.ChannelsSetAt == nil {
		return domain.OnboardingSession{}, &StepError{Code: "channels_not_set",
			Err: fmt.Errorf("choose how you want to be notified first: %w", domain.ErrInvalid)}
	}
	generated, err := s.provision(ctx, principal, session)
	if err != nil {
		s.logger.WarnContext(ctx, "onboarding provisioning failed", "session_id", session.ID, "error", err)
		recorded := domain.OnboardingError{Source: "server", Code: "provision_failed", Message: err.Error()}
		if _, updateErr := s.sessions.UpdateOnboardingSession(ctx, application.UpdateOnboardingSessionInput{
			OrganizationID: session.OrganizationID, SessionID: session.ID,
			ExpectedStatuses: []domain.OnboardingStatus{domain.OnboardingToolsDiscovered},
			Patch:            domain.OnboardingSessionPatch{LastError: &recorded},
			Now:              s.clock.Now(),
		}); updateErr != nil {
			s.logger.WarnContext(ctx, "recording onboarding error failed", "session_id", session.ID, "error", updateErr)
		}
		return domain.OnboardingSession{}, fmt.Errorf("%w: %w", ErrProvisionFailed, err)
	}
	return s.sessions.UpdateOnboardingSession(ctx, application.UpdateOnboardingSessionInput{
		OrganizationID: session.OrganizationID, SessionID: session.ID,
		ExpectedStatuses: []domain.OnboardingStatus{domain.OnboardingToolsDiscovered},
		Patch:            domain.OnboardingSessionPatch{Generated: &generated, ClearLastError: true},
		NextStatus:       domain.OnboardingProvisioning,
		Now:              s.clock.Now(),
	})
}

func (s *Service) provision(ctx context.Context, principal domain.Principal, session domain.OnboardingSession) (domain.OnboardingGenerated, error) {
	displayName := ""
	if user, err := s.identities.GetUser(ctx, application.GetUserInput{UserID: principal.ActorID}); err == nil {
		displayName = user.DisplayName
	}
	pathKey, pathRevision, err := s.ensurePath(ctx, principal, displayName)
	if err != nil {
		return domain.OnboardingGenerated{}, fmt.Errorf("escalation path: %w", err)
	}
	generated := domain.OnboardingGenerated{
		EscalationPathKey: pathKey, EscalationPathName: PathName(displayName), EscalationPathRevision: pathRevision,
		Pipelines: map[string]domain.OnboardingGeneratedPipeline{},
	}
	// The agent exists before its branch names it, so a request from it
	// can enter the branch; enrollment later reuses the agent.
	for _, runtime := range session.SelectedRuntimes {
		if _, err := s.ensureAgent(ctx, principal, runtime); err != nil {
			return domain.OnboardingGenerated{}, fmt.Errorf("agent for %s: %w", runtime, err)
		}
	}
	current, err := s.readEntry(ctx, principal)
	if err != nil {
		return domain.OnboardingGenerated{}, fmt.Errorf("entry pipeline: %w", err)
	}
	// The entry's default is organization-wide; the setup fills it only
	// when nobody has.
	if current.defaultPath == "" {
		current.defaultPath = pathKey
	}
	branches := make([]approvalblock.Definition, 0, len(session.SelectedRuntimes))
	for _, runtime := range session.SelectedRuntimes {
		if existing, ok := RuntimeBranchOf(current.blocks, runtime); ok && !StartingShape(runtime, existing) {
			// The person or their coding agent already wrote this runtime's
			// rules; a later setup on another machine leaves them alone.
			continue
		}
		selected := Union(GatedToolsOf(current.blocks, runtime), SuggestedToolNames(session, runtime))
		branches = append(branches, RuntimeBranch(runtime, StartingBlocks(runtime, selected, pathKey), pathKey))
	}
	activated, err := s.writeEntry(ctx, principal, current, EntryBlocks(current.blocks, branches...))
	if err != nil {
		return domain.OnboardingGenerated{}, fmt.Errorf("entry pipeline: %w", err)
	}
	for _, runtime := range session.SelectedRuntimes {
		generated.Pipelines[runtime] = domain.OnboardingGeneratedPipeline{
			PipelineKey: EntryPipelineKey, BranchKey: RuntimeBranchKey(runtime),
			Revision: activated, ReplacedActiveRevision: current.revision,
		}
	}
	return generated, nil
}

// entryRevision is the active entry document with the parts a write
// needs: the revision the write is guarded by, the default the write
// keeps, and the blocks it upserts into.
type entryRevision struct {
	revision    int64
	name        string
	defaultPath string
	askOwner    bool
	blocks      []approvalblock.Definition
}

// route is the entry pipeline's default as a route.
func (current entryRevision) route() approvalpipeline.Route {
	return approvalpipeline.Route{EscalationPath: current.defaultPath, AskAgentOwner: current.askOwner}
}

func (s *Service) readEntry(ctx context.Context, principal domain.Principal) (entryRevision, error) {
	active, err := s.pipelines.GetActive(ctx, principal, EntryPipelineKey)
	if err != nil {
		return entryRevision{}, err
	}
	blocks := make([]approvalblock.Definition, len(active.Blocks))
	for index, block := range active.Blocks {
		blocks[index] = block.Definition
	}
	return entryRevision{
		revision: active.Revision.Revision, name: active.Revision.Name,
		defaultPath: active.Revision.DefaultEscalationPath, askOwner: active.Revision.DefaultAskAgentOwner, blocks: blocks,
	}, nil
}

// writeEntry creates and activates an entry revision holding blocks,
// guarded by the revision the read saw, and answers the active revision.
// A document identical to the current one writes nothing.
func (s *Service) writeEntry(ctx context.Context, principal domain.Principal, current entryRevision, blocks []approvalblock.Definition) (int64, error) {
	active, err := s.pipelines.GetActive(ctx, principal, EntryPipelineKey)
	if err != nil {
		return 0, err
	}
	if active.Revision.DefaultEscalationPath == current.defaultPath && active.Revision.DefaultAskAgentOwner == current.askOwner && SameDocument(current.blocks, blocks) && active.Revision.Revision == current.revision {
		return current.revision, nil
	}
	created, err := s.pipelines.CreateRevision(ctx, principal, EntryPipelineKey, current.name, blocks, current.route())
	if err != nil {
		return 0, err
	}
	activated, err := s.pipelines.Activate(ctx, principal, EntryPipelineKey, created.Revision.Revision, current.revision)
	if errors.Is(err, domain.ErrPreconditionFailed) {
		return 0, fmt.Errorf("%w: %w", ErrEntryPipelineChanged, err)
	}
	if err != nil {
		return 0, err
	}
	return activated.Revision.Revision, nil
}

// ensurePath reuses a path that already notifies the person, else
// creates and activates one under a free key.
func (s *Service) ensurePath(ctx context.Context, principal domain.Principal, displayName string) (string, int64, error) {
	base := PathSlug(displayName, principal.MembershipID)
	for attempt := 1; attempt <= 20; attempt++ {
		key := base
		if attempt > 1 {
			key = fmt.Sprintf("%s-%d", base, attempt)
		}
		active, err := s.paths.GetActivePath(ctx, principal, key)
		switch {
		case err == nil:
			if PathTargetsMembership(active.Document, principal.MembershipID) {
				return key, active.Revision.Revision, nil
			}
			continue
		case errors.Is(err, domain.ErrNotFound):
			name, document := PathName(displayName), PathDocument(principal.MembershipID)
			created, err := s.paths.CreatePath(ctx, principal, key, name, document)
			if errors.Is(err, domain.ErrEscalationPathExists) {
				// The key exists with older, inactive revisions; append the
				// next one. Activation only needs the current active
				// revision, which is none either way.
				created, err = s.paths.CreatePathRevision(ctx, principal, key, name, document)
			}
			if errors.Is(err, domain.ErrEscalationPathArchived) {
				// An archived key is taken for good; try the next one.
				continue
			}
			if err != nil {
				return "", 0, err
			}
			activated, err := s.paths.ActivatePathRevision(ctx, principal, key, created.Revision.Revision, nil)
			if err != nil {
				return "", 0, err
			}
			return key, activated.Revision.Revision, nil
		default:
			return "", 0, err
		}
	}
	return "", 0, fmt.Errorf("no free escalation path key near %s: %w", base, domain.ErrConflict)
}

// Complete closes the flow. Skipping needs only the authority to connect
// agents; finishing additionally needs the open session, when there is
// one, to have reached tested.
func (s *Service) Complete(ctx context.Context, principal domain.Principal, outcome domain.OnboardingOutcome) error {
	if err := requireMember(principal, domain.PermissionAgentWrite, domain.OrganizationResource()); err != nil {
		return err
	}
	if outcome != domain.OnboardingFinished && outcome != domain.OnboardingSkip {
		return fmt.Errorf("outcome must be finished or skipped: %w", domain.ErrInvalid)
	}
	return s.sessions.CompleteOnboarding(ctx, application.CompleteOnboardingInput{
		OrganizationID: principal.OrganizationID, MembershipID: principal.MembershipID,
		ActorMembershipID: principal.MembershipID, Outcome: outcome, Now: s.clock.Now(),
	})
}

// RecordTest records the request the person decided in the test and
// moves the session to tested. The request must be the organization's,
// come from an agent this session enrolled, and be decided: the step is
// "decide it here", not "watch it arrive".
func (s *Service) RecordTest(ctx context.Context, principal domain.Principal, requestID domain.ID) (domain.OnboardingSession, error) {
	if err := authorize(principal); err != nil {
		return domain.OnboardingSession{}, err
	}
	session, err := s.Get(ctx, principal)
	if err != nil {
		return domain.OnboardingSession{}, err
	}
	if session.Status != domain.OnboardingInstalled {
		return domain.OnboardingSession{}, domain.ErrOnboardingState
	}
	if requestID == "" {
		return domain.OnboardingSession{}, fmt.Errorf("request_id is required: %w", domain.ErrInvalid)
	}
	request, err := s.approvals.GetApproval(ctx, application.GetApprovalInput{OrganizationID: session.OrganizationID, RequestID: requestID})
	if errors.Is(err, domain.ErrNotFound) {
		return domain.OnboardingSession{}, &StepError{Code: "test_request_unknown", Param: string(requestID),
			Err: fmt.Errorf("request %s is not in this organization: %w", requestID, domain.ErrInvalid)}
	}
	if err != nil {
		return domain.OnboardingSession{}, err
	}
	enrolled := false
	for _, enrollment := range session.Enrollments {
		enrolled = enrolled || enrollment.AgentSlug == request.AgentSlug
	}
	if !enrolled {
		return domain.OnboardingSession{}, &StepError{Code: "test_request_not_enrolled", Param: string(requestID),
			Err: fmt.Errorf("request %s did not come from an agent this setup enrolled: %w", requestID, domain.ErrInvalid)}
	}
	if !request.Status.Terminal() {
		return domain.OnboardingSession{}, &StepError{Code: "test_request_pending", Param: string(requestID),
			Err: fmt.Errorf("request %s has not been decided yet: %w", requestID, domain.ErrInvalid)}
	}
	now := s.clock.Now()
	return s.sessions.UpdateOnboardingSession(ctx, application.UpdateOnboardingSessionInput{
		OrganizationID: session.OrganizationID, SessionID: session.ID,
		ExpectedStatuses: []domain.OnboardingStatus{domain.OnboardingInstalled},
		Patch:            domain.OnboardingSessionPatch{TestRequestID: &requestID, TestedAt: &now, ClearLastError: true},
		NextStatus:       domain.OnboardingTested,
		Now:              now,
	})
}

// personalStepStatuses are the statuses in which the person's own steps
// (channels, rules) may be stamped: once agents are chosen, until the
// session ends. The status never moves: these steps gate the person, not
// the CLI.
var personalStepStatuses = []domain.OnboardingStatus{
	domain.OnboardingAgentsSelected, domain.OnboardingToolsDiscovered, domain.OnboardingProvisioning,
	domain.OnboardingEnrolled, domain.OnboardingInstalled, domain.OnboardingTested,
}

// ConfirmChannels records that the person chose how withHuman reaches
// them; provisioning waits for it. The channels themselves are the
// person's contact methods and notification rules, written through their
// own endpoints; this is the step, not the data. Passing the steps again
// re-stamps them.
func (s *Service) ConfirmChannels(ctx context.Context, principal domain.Principal) (domain.OnboardingSession, error) {
	if err := authorize(principal); err != nil {
		return domain.OnboardingSession{}, err
	}
	session, err := s.Get(ctx, principal)
	if err != nil {
		return domain.OnboardingSession{}, err
	}
	now := s.clock.Now()
	return s.sessions.UpdateOnboardingSession(ctx, application.UpdateOnboardingSessionInput{
		OrganizationID: session.OrganizationID, SessionID: session.ID,
		ExpectedStatuses: personalStepStatuses,
		Patch:            domain.OnboardingSessionPatch{ChannelsSetAt: &now, ClearLastError: true},
		Now:              now,
	})
}

// SetGatedTools replaces one runtime's branch of the entry pipeline with
// the starting shape over the tools the person picked by hand: the named
// tools wait for them and everything else runs. It is the quick-try
// alternative to writing the rules with a coding agent, so it needs the
// agents to exist; an empty list makes every call run.
func (s *Service) SetGatedTools(ctx context.Context, principal domain.Principal, runtime string, tools []string) (domain.OnboardingSession, error) {
	if err := authorize(principal); err != nil {
		return domain.OnboardingSession{}, err
	}
	session, err := s.Get(ctx, principal)
	if err != nil {
		return domain.OnboardingSession{}, err
	}
	if !agentStatuses[session.Status] {
		return domain.OnboardingSession{}, fmt.Errorf("%w: connect the agents first", domain.ErrOnboardingState)
	}
	if !session.Selected(runtime) {
		return domain.OnboardingSession{}, &StepError{Code: "runtime_not_selected", Param: runtime,
			Err: fmt.Errorf("%s is not one of the selected agents: %w", runtime, domain.ErrInvalid)}
	}
	var gated []domain.OnboardingTool
	for _, name := range Union(tools) {
		found := false
		for _, tool := range session.Tools[runtime] {
			if tool.Name == name {
				gated = append(gated, tool)
				found = true
				break
			}
		}
		if !found {
			return domain.OnboardingSession{}, &StepError{Code: "tool_not_discovered", Param: name,
				Err: fmt.Errorf("%s was not discovered for %s: %w", name, runtime, domain.ErrInvalid)}
		}
	}
	names := make([]string, 0, len(gated))
	for _, tool := range gated {
		names = append(names, tool.Name)
	}
	pathKey := session.Generated.EscalationPathKey
	current, err := s.readEntry(ctx, principal)
	if err != nil {
		return domain.OnboardingSession{}, err
	}
	branch := RuntimeBranch(runtime, StartingBlocks(runtime, names, pathKey), pathKey)
	if _, err := s.writeEntry(ctx, principal, current, EntryBlocks(current.blocks, branch)); err != nil {
		return domain.OnboardingSession{}, err
	}
	return s.sessions.UpdateOnboardingSession(ctx, application.UpdateOnboardingSessionInput{
		OrganizationID: session.OrganizationID, SessionID: session.ID,
		Patch: domain.OnboardingSessionPatch{ClearLastError: true},
		Now:   s.clock.Now(),
	})
}

// RecordRules records that the person left the rules step for the test,
// once the CLI has installed the hooks, and chooses the test commands
// against the rules as they now stand, since whoever wrote them (a coding
// agent, the person) may have let the starting test call through. The
// first time stamps rules_set_at, and the test counts the requests that
// arrive after it; coming back to change the rules keeps that stamp, so a
// request already decided still counts.
func (s *Service) RecordRules(ctx context.Context, principal domain.Principal) (domain.OnboardingSession, error) {
	if err := authorize(principal); err != nil {
		return domain.OnboardingSession{}, err
	}
	session, err := s.Get(ctx, principal)
	if err != nil {
		return domain.OnboardingSession{}, err
	}
	if session.Status != domain.OnboardingInstalled && session.Status != domain.OnboardingTested {
		return domain.OnboardingSession{}, domain.ErrOnboardingState
	}
	commands, err := s.chooseTestCommands(ctx, principal, session)
	if err != nil {
		return domain.OnboardingSession{}, err
	}
	now := s.clock.Now()
	patch := domain.OnboardingSessionPatch{TestCommands: &commands, ClearLastError: true}
	if session.RulesSetAt == nil {
		patch.RulesSetAt = &now
	}
	return s.sessions.UpdateOnboardingSession(ctx, application.UpdateOnboardingSessionInput{
		OrganizationID: session.OrganizationID, SessionID: session.ID,
		ExpectedStatuses: []domain.OnboardingStatus{domain.OnboardingInstalled, domain.OnboardingTested},
		Patch:            patch,
		Now:              now,
	})
}

// ExchangeResult is what the CLI receives for a setup code.
type ExchangeResult struct {
	Token       string
	Session     domain.OnboardingSession
	ProviderURL string
}

// Exchange redeems a setup code for the CLI's bearer.
func (s *Service) Exchange(ctx context.Context, code string, machine domain.OnboardingMachine) (ExchangeResult, error) {
	code = NormalizeSetupCode(code)
	if code == "" {
		return ExchangeResult{}, fmt.Errorf("setup code is required: %w", domain.ErrInvalid)
	}
	secret, secretHash, err := authn.NewOnboardingSecret()
	if err != nil {
		return ExchangeResult{}, err
	}
	session, err := s.sessions.ExchangeOnboardingSetupCode(ctx, application.ExchangeOnboardingSetupCodeInput{
		SetupCodeHash: authn.HashToken(code), CredentialSecretHash: secretHash, Machine: machine, Now: s.clock.Now(),
	})
	if err != nil {
		return ExchangeResult{}, err
	}
	return ExchangeResult{
		Token: authn.FormatOnboardingCredential(session.ID, secret), Session: session,
		ProviderURL: strings.TrimRight(s.config.PublicOrigin, "/"),
	}, nil
}

// Wait long-polls the session on the CLI's behalf.
func (s *Service) Wait(ctx context.Context, session domain.OnboardingSession, since int64, timeout time.Duration) (domain.OnboardingSession, error) {
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	return s.sessions.WaitOnboardingSession(ctx, application.WaitOnboardingSessionInput{
		OrganizationID: session.OrganizationID, SessionID: session.ID, SinceVersion: since, Timeout: timeout,
	})
}

// ReportDetections records the CLI's runtime survey; every catalogued
// runtime must be present so the page never guesses about an absence.
func (s *Service) ReportDetections(ctx context.Context, session domain.OnboardingSession, detections []domain.OnboardingDetection) (domain.OnboardingSession, error) {
	seen := map[string]bool{}
	for index := range detections {
		key := agentruntime.Key(detections[index].Runtime)
		if !agentruntime.Valid(key) {
			return domain.OnboardingSession{}, fmt.Errorf("unknown runtime %q: %w", key, domain.ErrInvalid)
		}
		if seen[string(key)] {
			return domain.OnboardingSession{}, fmt.Errorf("runtime %s reported twice: %w", key, domain.ErrInvalid)
		}
		seen[string(key)] = true
		if detections[index].MCPServers == nil {
			detections[index].MCPServers = []domain.OnboardingMCPServer{}
		}
	}
	for _, key := range agentruntime.Keys() {
		if !seen[string(key)] {
			return domain.OnboardingSession{}, fmt.Errorf("runtime %s missing from the report: %w", key, domain.ErrInvalid)
		}
	}
	return s.sessions.UpdateOnboardingSession(ctx, application.UpdateOnboardingSessionInput{
		OrganizationID: session.OrganizationID, SessionID: session.ID,
		ExpectedStatuses: []domain.OnboardingStatus{domain.OnboardingConnected, domain.OnboardingDetected},
		Patch:            domain.OnboardingSessionPatch{Detections: &detections, ClearLastError: true},
		NextStatus:       domain.OnboardingDetected,
		Now:              s.clock.Now(),
	})
}

// ReportTools records discovery for every selected runtime and judges
// each tool; the suggested ones are what the starting pipeline gates.
func (s *Service) ReportTools(ctx context.Context, session domain.OnboardingSession, reported map[string]domain.OnboardingReportedTools) (domain.OnboardingSession, error) {
	tools := map[string][]domain.OnboardingTool{}
	discoveryErrors := map[string][]domain.OnboardingDiscoveryError{}
	for runtime := range reported {
		if !session.Selected(runtime) {
			return domain.OnboardingSession{}, fmt.Errorf("%s is not a selected agent: %w", runtime, domain.ErrInvalid)
		}
	}
	for _, runtime := range session.SelectedRuntimes {
		report, ok := reported[runtime]
		if !ok {
			return domain.OnboardingSession{}, fmt.Errorf("no tools reported for %s: %w", runtime, domain.ErrInvalid)
		}
		tools[runtime] = judgeTools(agentruntime.Key(runtime), report)
		discoveryErrors[runtime] = append([]domain.OnboardingDiscoveryError{}, report.Errors...)
	}
	return s.sessions.UpdateOnboardingSession(ctx, application.UpdateOnboardingSessionInput{
		OrganizationID: session.OrganizationID, SessionID: session.ID,
		ExpectedStatuses: []domain.OnboardingStatus{domain.OnboardingAgentsSelected, domain.OnboardingToolsDiscovered},
		Patch:            domain.OnboardingSessionPatch{Tools: &tools, DiscoveryErrors: &discoveryErrors, ClearLastError: true},
		NextStatus:       domain.OnboardingToolsDiscovered,
		Now:              s.clock.Now(),
	})
}

func judgeTools(runtime agentruntime.Key, report domain.OnboardingReportedTools) []domain.OnboardingTool {
	catalog, _ := agentruntime.Lookup(runtime)
	var judged []domain.OnboardingTool
	seen := map[string]bool{}
	for _, tool := range report.Builtin {
		if tool.Name == "" || seen[tool.Name] {
			continue
		}
		seen[tool.Name] = true
		kind := agentruntime.ToolOther
		description := tool.Description
		if known, ok := catalog.Builtin(tool.Name); ok {
			kind = known.Kind
			if description == "" {
				description = known.Description
			}
		}
		verdict := toolsuggest.Suggest(toolsuggest.Tool{Runtime: runtime, Name: tool.Name, Description: description, Source: agentruntime.SourceBuiltin, Kind: kind})
		judged = append(judged, domain.OnboardingTool{
			Name: tool.Name, Source: string(agentruntime.SourceBuiltin), Description: description, Kind: string(kind), InputSchema: tool.InputSchema, DisplayName: tool.DisplayName,
			Suggested: verdict.Suggested, Reason: verdict.Reason,
		})
	}
	servers := make([]string, 0, len(report.MCP))
	for server := range report.MCP {
		servers = append(servers, server)
	}
	sortStrings(servers)
	for _, server := range servers {
		for _, tool := range report.MCP[server] {
			if tool.Name == "" || seen["mcp:"+server+":"+tool.Name] {
				continue
			}
			seen["mcp:"+server+":"+tool.Name] = true
			var annotations *toolsuggest.Annotations
			if tool.Annotations != nil {
				annotations = &toolsuggest.Annotations{
					ReadOnlyHint: tool.Annotations.ReadOnlyHint, DestructiveHint: tool.Annotations.DestructiveHint,
					IdempotentHint: tool.Annotations.IdempotentHint, OpenWorldHint: tool.Annotations.OpenWorldHint,
				}
			}
			verdict := toolsuggest.Suggest(toolsuggest.Tool{Runtime: runtime, Name: tool.Name, Description: tool.Description,
				Source: agentruntime.SourceMCP, Server: server, Annotations: annotations})
			judged = append(judged, domain.OnboardingTool{
				Name: tool.Name, Source: string(agentruntime.SourceMCP), Server: server, Description: tool.Description, InputSchema: tool.InputSchema, DisplayName: tool.DisplayName,
				Annotations: tool.Annotations, Suggested: verdict.Suggested, Reason: verdict.Reason,
			})
		}
	}
	if judged == nil {
		judged = []domain.OnboardingTool{}
	}
	return judged
}

// ReportError records a CLI-side failure without moving the session.
func (s *Service) ReportError(ctx context.Context, session domain.OnboardingSession, code, message string) (domain.OnboardingSession, error) {
	recorded := domain.OnboardingError{Source: "cli", Code: strings.TrimSpace(code), Message: strings.TrimSpace(message)}
	if recorded.Code == "" {
		recorded.Code = "cli_error"
	}
	return s.sessions.UpdateOnboardingSession(ctx, application.UpdateOnboardingSessionInput{
		OrganizationID: session.OrganizationID, SessionID: session.ID,
		Patch: domain.OnboardingSessionPatch{LastError: &recorded},
		Now:   s.clock.Now(),
	})
}

// EnrollResult hands the CLI everything it needs to finish locally. The
// credentials in it exist nowhere else.
type EnrollResult struct {
	ProviderURL string
	Enrollments []Enrollment
	Hooks       map[string]HookPlan
	Session     domain.OnboardingSession
}

type Enrollment struct {
	Runtime       string
	Agent         domain.Agent
	AgentInstance domain.AgentInstance
	Credential    domain.IssuedCredential
}

// Enroll mints one agent, instance, and credential per selected runtime
// under a principal rebuilt from the session's membership.
func (s *Service) Enroll(ctx context.Context, session domain.OnboardingSession) (EnrollResult, error) {
	if session.Status != domain.OnboardingProvisioning {
		return EnrollResult{}, domain.ErrOnboardingState
	}
	principal, err := s.delegatedPrincipal(ctx, session)
	if err != nil {
		return EnrollResult{}, err
	}
	instanceName := strings.TrimSpace(session.Machine.Hostname)
	if instanceName == "" {
		instanceName = "default"
	}
	metadata, _ := json.Marshal(map[string]any{
		"hostname": session.Machine.Hostname, "os": session.Machine.OS, "arch": session.Machine.Arch,
		"cli_version": session.Machine.CLIVersion, "onboarding_session_id": session.ID,
	})
	result := EnrollResult{ProviderURL: strings.TrimRight(s.config.PublicOrigin, "/"), Hooks: PlanHooks(session)}
	var records []domain.OnboardingEnrollment
	for _, runtime := range session.SelectedRuntimes {
		agent, err := s.ensureAgent(ctx, principal, runtime)
		if err != nil {
			return EnrollResult{}, fmt.Errorf("agent %s: %w", runtime, err)
		}
		issued, err := s.mintInstance(ctx, principal, agent.ID, instanceName, metadata)
		if err != nil {
			return EnrollResult{}, fmt.Errorf("instance for %s: %w", runtime, err)
		}
		result.Enrollments = append(result.Enrollments, Enrollment{Runtime: runtime, Agent: agent, AgentInstance: issued.AgentInstance, Credential: issued.Credential})
		records = append(records, domain.OnboardingEnrollment{
			Runtime: runtime, AgentSlug: agent.AgentSlug, AgentName: agent.Name,
			AgentInstanceID: issued.AgentInstance.ID, AgentInstanceName: issued.AgentInstance.Name, CredentialID: issued.Credential.ID,
		})
	}
	updated, err := s.sessions.UpdateOnboardingSession(ctx, application.UpdateOnboardingSessionInput{
		OrganizationID: session.OrganizationID, SessionID: session.ID,
		ExpectedStatuses: []domain.OnboardingStatus{domain.OnboardingProvisioning},
		Patch:            domain.OnboardingSessionPatch{Enrollments: &records, ClearLastError: true},
		NextStatus:       domain.OnboardingEnrolled,
		Now:              s.clock.Now(),
	})
	if err != nil {
		return EnrollResult{}, err
	}
	result.Session = updated
	return result, nil
}

// ensureAgent finds or creates the runtime's agent. The runtime key is
// the slug (codex, claude-code) and its display name the agent's name, so
// every machine that connects the same runtime lands under one agent. An
// archived agent still holds the slug: the person restores it in the app
// or connects under another slug, which the guided setup cannot do for
// them.
func (s *Service) ensureAgent(ctx context.Context, principal domain.Principal, runtime string) (domain.Agent, error) {
	agent, err := s.agents.GetAgentBySlug(ctx, application.GetAgentBySlugInput{OrganizationID: principal.OrganizationID, AgentSlug: domain.AgentSlug(runtime)})
	switch {
	case err == nil:
		if agent.Status == domain.AgentArchived {
			return domain.Agent{}, &StepError{Code: "agent_archived", Param: runtime,
				Err: fmt.Errorf("%w: restore %s first", domain.ErrAgentArchived, runtime)}
		}
		if agent.Status != domain.AgentActive {
			return domain.Agent{}, fmt.Errorf("agent %s is disabled: %w", runtime, domain.ErrAgentInactive)
		}
		return agent, nil
	case errors.Is(err, domain.ErrNotFound):
		name := runtime
		if known, ok := agentruntime.Lookup(agentruntime.Key(runtime)); ok {
			name = known.DisplayName
		}
		// A runtime the guided setup connects is a coding assistant the
		// person runs for themselves, so its agent is personal: every
		// instance belongs to whoever enrolls it.
		return s.minter.CreateAgent(ctx, principal, name, domain.AgentSlug(runtime), domain.AgentPersonal)
	default:
		return domain.Agent{}, err
	}
}

func (s *Service) mintInstance(ctx context.Context, principal domain.Principal, agentID domain.ID, name string, metadata json.RawMessage) (domain.AgentInstanceCredential, error) {
	for attempt := 1; attempt <= 10; attempt++ {
		candidate := name
		if attempt > 1 {
			candidate = fmt.Sprintf("%s-%d", name, attempt)
		}
		issued, err := s.minter.CreateAgentInstanceCredential(ctx, principal, agentID, candidate, metadata)
		if errors.Is(err, domain.ErrConflict) {
			continue
		}
		return issued, err
	}
	return domain.AgentInstanceCredential{}, fmt.Errorf("no free instance name near %s: %w", name, domain.ErrConflict)
}

// delegatedPrincipal rebuilds the session starter's principal from their
// current membership. A suspended member or one who lost a role since
// starting the setup cannot mint anything through the CLI.
func (s *Service) delegatedPrincipal(ctx context.Context, session domain.OnboardingSession) (domain.Principal, error) {
	membership, err := s.identities.GetMembership(ctx, application.GetMembershipInput{
		OrganizationID: session.OrganizationID, MembershipID: session.MembershipID,
	})
	if err != nil {
		return domain.Principal{}, err
	}
	if membership.Status != domain.MembershipActive {
		return domain.Principal{}, domain.ErrMembershipInactive
	}
	grants, err := s.roles.LoadGrants(ctx, application.LoadGrantsInput{
		OrganizationID: session.OrganizationID, PrincipalKind: domain.PrincipalMembership, PrincipalID: session.MembershipID,
	})
	if err != nil {
		return domain.Principal{}, err
	}
	principal := domain.Principal{
		ActorType: domain.ActorHuman, ActorID: membership.UserID, OrganizationID: session.OrganizationID,
		MembershipID: session.MembershipID, TeamIDs: membership.TeamIDs,
		Auth: domain.AuthContext{Method: "onboarding", Assurance: "delegated", AuthenticatedAt: s.clock.Now()},
	}.WithGrants(grants)
	if !Authorized(principal) {
		return domain.Principal{}, domain.ErrForbidden
	}
	return principal, nil
}

// ReportInstall records the CLI's hook installation. The test commands
// wait for the rules: RecordRules chooses them.
func (s *Service) ReportInstall(ctx context.Context, session domain.OnboardingSession, results []domain.OnboardingInstallResult) (domain.OnboardingSession, error) {
	seen := map[string]bool{}
	for _, result := range results {
		if !session.Selected(result.Runtime) {
			return domain.OnboardingSession{}, fmt.Errorf("%s is not a selected agent: %w", result.Runtime, domain.ErrInvalid)
		}
		seen[result.Runtime] = true
	}
	for _, runtime := range session.SelectedRuntimes {
		if !seen[runtime] {
			return domain.OnboardingSession{}, fmt.Errorf("no install result for %s: %w", runtime, domain.ErrInvalid)
		}
	}
	return s.sessions.UpdateOnboardingSession(ctx, application.UpdateOnboardingSessionInput{
		OrganizationID: session.OrganizationID, SessionID: session.ID,
		ExpectedStatuses: []domain.OnboardingStatus{domain.OnboardingEnrolled},
		Patch:            domain.OnboardingSessionPatch{InstallResults: &results, ClearLastError: true},
		NextStatus:       domain.OnboardingInstalled,
		Now:              s.clock.Now(),
	})
}
