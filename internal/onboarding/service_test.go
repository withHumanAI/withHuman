package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/agentruntime"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/approvalblock"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/approvalpipeline"
	"github.com/withHumanAI/withHuman/internal/authorization"
	"github.com/withHumanAI/withHuman/internal/reviewrouting"
	"github.com/withHumanAI/withHuman/routing"
)

// memorySessions is an in-memory OnboardingRepository with the same
// precondition semantics as the PostgreSQL adapter: status checks, the
// version bump, expiry, and one open session per member.
type memorySessions struct {
	mu       sync.Mutex
	sessions map[domain.ID]*domain.OnboardingSession
	codes    map[string]domain.ID // hex of setup code hash -> session
	secrets  map[domain.ID]string // session -> hex of credential hash
	complete []application.CompleteOnboardingInput
}

func newMemorySessions() *memorySessions {
	return &memorySessions{sessions: map[domain.ID]*domain.OnboardingSession{}, codes: map[string]domain.ID{}, secrets: map[domain.ID]string{}}
}

func (m *memorySessions) CreateOnboardingSession(_ context.Context, input application.CreateOnboardingSessionInput) (domain.OnboardingSession, error) {
	if err := input.Validate(); err != nil {
		return domain.OnboardingSession{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.sessions {
		if existing.MembershipID == input.Session.MembershipID && !existing.Status.Terminal() {
			existing.Status = domain.OnboardingSkipped
			now := input.Session.CreatedAt
			existing.CompletedAt = &now
			existing.Version++
		}
	}
	session := input.Session
	session.Version = 1
	m.sessions[session.ID] = &session
	m.codes[fmt.Sprintf("%x", input.SetupCodeHash)] = session.ID
	return session, nil
}

func (m *memorySessions) GetOnboardingSession(_ context.Context, input application.GetOnboardingSessionInput) (domain.OnboardingSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[input.SessionID]
	if !ok || session.OrganizationID != input.OrganizationID {
		return domain.OnboardingSession{}, domain.ErrNotFound
	}
	return *session, nil
}

func (m *memorySessions) GetOpenOnboardingSession(_ context.Context, input application.GetOpenOnboardingSessionInput) (domain.OnboardingSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, session := range m.sessions {
		if session.OrganizationID == input.OrganizationID && session.MembershipID == input.MembershipID && !session.Status.Terminal() {
			return *session, nil
		}
	}
	return domain.OnboardingSession{}, domain.ErrNotFound
}

func (m *memorySessions) ExchangeOnboardingSetupCode(_ context.Context, input application.ExchangeOnboardingSetupCodeInput) (domain.OnboardingSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%x", input.SetupCodeHash)
	id, ok := m.codes[key]
	if !ok {
		return domain.OnboardingSession{}, domain.ErrUnauthenticated
	}
	session := m.sessions[id]
	if session.Status != domain.OnboardingAwaitingCLI || !input.Now.Before(session.ExpiresAt) {
		return domain.OnboardingSession{}, domain.ErrUnauthenticated
	}
	delete(m.codes, key)
	m.secrets[id] = fmt.Sprintf("%x", input.CredentialSecretHash)
	session.Status, session.Machine, session.Version = domain.OnboardingConnected, input.Machine, session.Version+1
	return *session, nil
}

func (m *memorySessions) ResolveOnboardingCredential(_ context.Context, input application.ResolveOnboardingCredentialInput) (domain.OnboardingSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[input.SessionID]
	if !ok || m.secrets[input.SessionID] != fmt.Sprintf("%x", input.SecretHash) || session.Status.Terminal() || !input.Now.Before(session.ExpiresAt) {
		return domain.OnboardingSession{}, domain.ErrUnauthenticated
	}
	return *session, nil
}

func (m *memorySessions) UpdateOnboardingSession(_ context.Context, input application.UpdateOnboardingSessionInput) (domain.OnboardingSession, error) {
	if err := input.Validate(); err != nil {
		return domain.OnboardingSession{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[input.SessionID]
	if !ok || session.OrganizationID != input.OrganizationID {
		return domain.OnboardingSession{}, domain.ErrNotFound
	}
	if session.Expired(input.Now) {
		return domain.OnboardingSession{}, domain.ErrOnboardingExpired
	}
	if session.Status.Terminal() {
		return domain.OnboardingSession{}, fmt.Errorf("session is %s: %w", session.Status, domain.ErrOnboardingState)
	}
	if len(input.ExpectedStatuses) > 0 {
		allowed := false
		for _, status := range input.ExpectedStatuses {
			allowed = allowed || status == session.Status
		}
		if !allowed {
			return domain.OnboardingSession{}, fmt.Errorf("session is %s: %w", session.Status, domain.ErrOnboardingState)
		}
	}
	patch := input.Patch
	if patch.Detections != nil {
		session.Detections = *patch.Detections
	}
	if patch.SelectedRuntimes != nil {
		session.SelectedRuntimes = *patch.SelectedRuntimes
	}
	if patch.Tools != nil {
		session.Tools = *patch.Tools
	}
	if patch.DiscoveryErrors != nil {
		session.DiscoveryErrors = *patch.DiscoveryErrors
	}
	if patch.Generated != nil {
		session.Generated = *patch.Generated
	}
	if patch.Enrollments != nil {
		session.Enrollments = *patch.Enrollments
	}
	if patch.TestRequestID != nil {
		session.TestRequestID = patch.TestRequestID
	}
	if patch.TestedAt != nil {
		session.TestedAt = patch.TestedAt
	}
	if patch.ChannelsSetAt != nil {
		session.ChannelsSetAt = patch.ChannelsSetAt
	}
	if patch.RulesSetAt != nil {
		session.RulesSetAt = patch.RulesSetAt
	}
	if patch.Assistant != nil {
		session.Assistant = *patch.Assistant
	}
	if patch.AssistantContext != nil {
		session.AssistantContext = *patch.AssistantContext
	}
	if patch.InstallResults != nil {
		session.InstallResults = *patch.InstallResults
	}
	if patch.TestCommands != nil {
		session.TestCommands = *patch.TestCommands
	}
	if patch.LastError != nil {
		session.LastError = patch.LastError
	} else if patch.ClearLastError {
		session.LastError = nil
	}
	if input.NextStatus != "" {
		session.Status = input.NextStatus
	}
	session.Version++
	session.UpdatedAt = input.Now
	return *session, nil
}

func (m *memorySessions) WaitOnboardingSession(ctx context.Context, input application.WaitOnboardingSessionInput) (domain.OnboardingSession, error) {
	return m.GetOnboardingSession(ctx, application.GetOnboardingSessionInput{OrganizationID: input.OrganizationID, SessionID: input.SessionID})
}

func (m *memorySessions) CompleteOnboarding(_ context.Context, input application.CompleteOnboardingInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.complete = append(m.complete, input)
	for _, session := range m.sessions {
		if session.MembershipID == input.MembershipID && !session.Status.Terminal() {
			if input.Outcome == domain.OnboardingFinished && session.Status != domain.OnboardingTested {
				return fmt.Errorf("session is %s: %w", session.Status, domain.ErrOnboardingState)
			}
			session.Status = domain.OnboardingCompleted
			if input.Outcome == domain.OnboardingSkip {
				session.Status = domain.OnboardingSkipped
			}
			now := input.Now
			session.CompletedAt = &now
			session.Version++
		}
	}
	return nil
}

type identityStub struct {
	application.IdentityRepository
	membership domain.Membership
	user       domain.User
}

// rolesStub answers the delegated principal's grant load with whatever
// the test says the membership holds right now.
type rolesStub struct {
	application.RoleRepository
	grants []domain.Grant
}

func (s *rolesStub) LoadGrants(context.Context, application.LoadGrantsInput) ([]domain.Grant, error) {
	return s.grants, nil
}

func (s *identityStub) GetOrganization(_ context.Context, input application.GetOrganizationInput) (domain.Organization, error) {
	return domain.Organization{ID: input.OrganizationID, Status: domain.OrganizationActive}, nil
}

func (s *identityStub) GetUser(context.Context, application.GetUserInput) (domain.User, error) {
	return s.user, nil
}

func (s *identityStub) GetMembership(context.Context, application.GetMembershipInput) (domain.Membership, error) {
	return s.membership, nil
}

// agentsStub holds the organization's agents by slug, archived ones
// included, as the real lookup does.
type agentsStub struct {
	application.AgentRepository
	existing map[domain.AgentSlug]domain.Agent
}

func (s *agentsStub) GetAgentBySlug(_ context.Context, input application.GetAgentBySlugInput) (domain.Agent, error) {
	if agent, ok := s.existing[input.AgentSlug]; ok {
		return agent, nil
	}
	return domain.Agent{}, domain.ErrNotFound
}

type minterStub struct {
	agents    *agentsStub
	created   []string
	instances []string
	conflicts int
}

func (m *minterStub) CreateAgent(_ context.Context, principal domain.Principal, name string, agentSlug domain.AgentSlug) (domain.Agent, error) {
	if err := authorization.Check(principal, domain.PermissionAgentWrite, domain.OrganizationResource()); err != nil {
		return domain.Agent{}, err
	}
	m.created = append(m.created, string(agentSlug)+"="+name)
	agent := domain.Agent{ID: domain.ID(uuid.NewString()), Name: name, AgentSlug: agentSlug, Status: domain.AgentActive}
	m.agents.existing[agentSlug] = agent
	return agent, nil
}

func (m *minterStub) CreateAgentInstanceCredential(_ context.Context, _ domain.Principal, agentID domain.ID, name string, _ json.RawMessage) (domain.AgentInstanceCredential, error) {
	if m.conflicts > 0 {
		m.conflicts--
		return domain.AgentInstanceCredential{}, fmt.Errorf("taken: %w", domain.ErrConflict)
	}
	m.instances = append(m.instances, name)
	return domain.AgentInstanceCredential{
		AgentInstance: domain.AgentInstance{ID: domain.ID(uuid.NewString()), AgentID: agentID, Name: name},
		Credential:    domain.IssuedCredential{ID: domain.ID(uuid.NewString()), Token: "whc_" + name, ExpiresAt: time.Now().Add(time.Hour)},
	}, nil
}

type pipelinesStub struct {
	active    map[domain.AgentSlug]approvalpipeline.PipelineRevision
	created   map[domain.AgentSlug][]approvalblock.Definition
	activated map[domain.AgentSlug]int64
	failOn    domain.AgentSlug
	// previews records every sample the assistant checked, and
	// previewOutcome is what each answers.
	previews       []approvalpipeline.PreviewSample
	previewOutcome approvalpipeline.BlockOutcome
}

func (p *pipelinesStub) Preview(_ context.Context, _ domain.Principal, _ domain.AgentSlug, _ []approvalblock.Definition, _ string, sample approvalpipeline.PreviewSample) (approvalpipeline.PreviewResult, error) {
	p.previews = append(p.previews, sample)
	outcome := p.previewOutcome
	if outcome == "" {
		outcome = approvalpipeline.BlockHuman
	}
	return approvalpipeline.PreviewResult{Outcome: outcome, TerminalBlockKey: "stub"}, nil
}

// GetActive answers like the real service: an agent whose pipeline
// nobody has edited has the empty revision 1 it was born with.
func (p *pipelinesStub) GetActive(_ context.Context, _ domain.Principal, agentSlug domain.AgentSlug) (approvalpipeline.PipelineRevision, error) {
	if revision, ok := p.active[agentSlug]; ok {
		return revision, nil
	}
	return approvalpipeline.PipelineRevision{Revision: domain.RequestApprovalPipelineRevision{AgentSlug: agentSlug, Revision: 1, IsActive: true}}, nil
}

func (p *pipelinesStub) CreateRevision(_ context.Context, _ domain.Principal, agentSlug domain.AgentSlug, definitions []approvalblock.Definition, defaultPath string) (approvalpipeline.PipelineRevision, error) {
	if agentSlug == p.failOn {
		return approvalpipeline.PipelineRevision{}, fmt.Errorf("boom: %w", domain.ErrInvalid)
	}
	if p.created == nil {
		p.created = map[domain.AgentSlug][]approvalblock.Definition{}
	}
	p.created[agentSlug] = definitions
	next := int64(2)
	if active, ok := p.active[agentSlug]; ok {
		next = active.Revision.Revision + 1
	}
	return approvalpipeline.PipelineRevision{Revision: domain.RequestApprovalPipelineRevision{Revision: next, DefaultEscalationPath: defaultPath}}, nil
}

func (p *pipelinesStub) Activate(_ context.Context, _ domain.Principal, agentSlug domain.AgentSlug, revision int64, expected int64) (approvalpipeline.PipelineRevision, error) {
	if p.activated == nil {
		p.activated = map[domain.AgentSlug]int64{}
	}
	p.activated[agentSlug] = expected
	return approvalpipeline.PipelineRevision{Revision: domain.RequestApprovalPipelineRevision{Revision: revision, IsActive: true}}, nil
}

type pathsStub struct {
	active    map[string]reviewrouting.PathRevision
	created   map[string]routing.PathDocument
	activated []string
}

func (p *pathsStub) GetActivePath(_ context.Context, _ domain.Principal, key string) (reviewrouting.PathRevision, error) {
	if revision, ok := p.active[key]; ok {
		return revision, nil
	}
	return reviewrouting.PathRevision{}, domain.ErrNotFound
}

func (p *pathsStub) CreatePath(ctx context.Context, principal domain.Principal, key, name string, document routing.PathDocument) (reviewrouting.PathRevision, error) {
	if _, exists := p.created[key]; exists {
		return reviewrouting.PathRevision{}, domain.ErrEscalationPathExists
	}
	return p.CreatePathRevision(ctx, principal, key, name, document)
}

func (p *pathsStub) CreatePathRevision(_ context.Context, _ domain.Principal, key, _ string, document routing.PathDocument) (reviewrouting.PathRevision, error) {
	if p.created == nil {
		p.created = map[string]routing.PathDocument{}
	}
	p.created[key] = document
	return reviewrouting.PathRevision{Revision: domain.EscalationPathRevision{PathKey: key, Revision: 1}, Document: document}, nil
}

func (p *pathsStub) ActivatePathRevision(_ context.Context, _ domain.Principal, key string, revision int64, _ *int64) (reviewrouting.PathRevision, error) {
	p.activated = append(p.activated, key)
	return reviewrouting.PathRevision{Revision: domain.EscalationPathRevision{PathKey: key, Revision: revision, IsActive: true}}, nil
}

type clockStub struct{ now time.Time }

func (c clockStub) Now() time.Time { return c.now }

// approvalsStub is the organization's requests by id, for the test the
// person records.
type approvalsStub struct {
	requests map[domain.ID]domain.ApprovalRequest
}

func (s *approvalsStub) GetApproval(_ context.Context, input application.GetApprovalInput) (domain.ApprovalRequest, error) {
	request, ok := s.requests[input.RequestID]
	if !ok || request.OrganizationID != input.OrganizationID {
		return domain.ApprovalRequest{}, domain.ErrNotFound
	}
	return request, nil
}

type harness struct {
	service    *Service
	sessions   *memorySessions
	identities *identityStub
	roles      *rolesStub
	agents     *agentsStub
	approvals  *approvalsStub
	minter     *minterStub
	pipelines  *pipelinesStub
	paths      *pathsStub
	principal  domain.Principal
}

func newHarness() *harness {
	orgID, membershipID, userID := domain.ID(uuid.NewString()), domain.ID(uuid.NewString()), domain.ID(uuid.NewString())
	principal := domain.Principal{
		ActorType: domain.ActorHuman, ActorID: userID, OrganizationID: orgID, MembershipID: membershipID,
	}.WithGrants(domain.GrantsForRoles([]domain.Role{domain.RoleOwner}))
	agents := &agentsStub{existing: map[domain.AgentSlug]domain.Agent{}}
	h := &harness{
		sessions: newMemorySessions(),
		identities: &identityStub{
			membership: domain.Membership{ID: membershipID, UserID: userID, OrganizationID: orgID, Status: domain.MembershipActive},
			user:       domain.User{ID: userID, DisplayName: "Chris Battarbee"},
		},
		roles:     &rolesStub{grants: principal.Grants},
		agents:    agents,
		approvals: &approvalsStub{requests: map[domain.ID]domain.ApprovalRequest{}},
		minter:    &minterStub{agents: agents},
		pipelines: &pipelinesStub{active: map[domain.AgentSlug]approvalpipeline.PipelineRevision{}},
		paths:     &pathsStub{active: map[string]reviewrouting.PathRevision{}},
		principal: principal,
	}
	h.service = NewService(h.sessions, h.identities, h.agents, h.roles, h.approvals, h.minter, h.pipelines, h.paths, clockStub{time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)},
		slog.New(slog.NewTextHandler(io.Discard, nil)), Config{PublicOrigin: "https://app.example.test/", CLIDownloadURL: "https://downloads.example.test"})
	return h
}

func detections(installed ...string) []domain.OnboardingDetection {
	var out []domain.OnboardingDetection
	for _, key := range agentruntime.Keys() {
		detection := domain.OnboardingDetection{Runtime: string(key)}
		for _, name := range installed {
			if name == string(key) {
				detection.Installed = true
				detection.Version = "1.0"
			}
		}
		out = append(out, detection)
	}
	return out
}

func mustStatus(t *testing.T, session domain.OnboardingSession, want domain.OnboardingStatus) {
	t.Helper()
	if session.Status != want {
		t.Fatalf("status %s, want %s", session.Status, want)
	}
}

func TestGuidedSetupHappyPath(t *testing.T) {
	ctx := context.Background()
	h := newHarness()

	begun, err := h.service.Begin(ctx, h.principal)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	mustStatus(t, begun.Session, domain.OnboardingAwaitingCLI)
	if begun.SetupCommand != "curl -fsSL https://downloads.example.test/install.sh | WITHHUMAN_CLI_BASE=https://downloads.example.test sh -s -- onboard --url https://app.example.test --code "+begun.SetupCode {
		t.Fatalf("setup command: %s", begun.SetupCommand)
	}
	if len(begun.SetupCode) != 14 {
		t.Fatalf("setup code shape: %s", begun.SetupCode)
	}

	// The CLI exchanges the code, in a lower-case, space-separated spelling.
	exchanged, err := h.service.Exchange(ctx, strings.ToLower(strings.ReplaceAll(begun.SetupCode, "-", " ")), domain.OnboardingMachine{Hostname: "chris-mbp", OS: "darwin", Arch: "arm64", CLIVersion: "0.1.0"})
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	mustStatus(t, exchanged.Session, domain.OnboardingConnected)
	if !strings.HasPrefix(exchanged.Token, "whb_"+string(begun.Session.ID)+"_") || exchanged.ProviderURL != "https://app.example.test" {
		t.Fatalf("exchange result: %+v", exchanged)
	}
	if _, err := h.service.Exchange(ctx, begun.SetupCode, domain.OnboardingMachine{}); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("replayed code should be refused, got %v", err)
	}
	session := exchanged.Session

	// The person cannot pick agents before the CLI reported them.
	if _, err := h.service.SelectAgents(ctx, h.principal, []string{"claude-code"}); err == nil {
		t.Fatal("SelectAgents before detection should fail")
	}

	if _, err := h.service.ReportDetections(ctx, session, detections("claude-code")[:5]); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("partial detections should be invalid, got %v", err)
	}
	session, err = h.service.ReportDetections(ctx, session, detections("claude-code", "codex"))
	if err != nil {
		t.Fatalf("ReportDetections: %v", err)
	}
	mustStatus(t, session, domain.OnboardingDetected)

	var step *StepError
	if _, err := h.service.SelectAgents(ctx, h.principal, []string{"hermes"}); !errors.As(err, &step) || step.Code != "runtime_not_detected" {
		t.Fatalf("selecting an absent runtime: %v", err)
	}
	session, err = h.service.SelectAgents(ctx, h.principal, []string{"claude-code", "codex", "claude-code"})
	if err != nil {
		t.Fatalf("SelectAgents: %v", err)
	}
	mustStatus(t, session, domain.OnboardingAgentsSelected)
	if len(session.SelectedRuntimes) != 2 {
		t.Fatalf("selected runtimes: %v", session.SelectedRuntimes)
	}

	destructive := true
	readOnly := true
	reported := map[string]domain.OnboardingReportedTools{
		"claude-code": {
			Builtin: []domain.OnboardingReportedTool{{Name: "Bash"}, {Name: "Edit"}, {Name: "Read"}},
			MCP: map[string][]domain.OnboardingReportedTool{
				"stripe": {
					{Name: "create_refund", Description: "Refund a charge"},
					{Name: "get_customer", Annotations: &domain.OnboardingToolAnnotations{ReadOnlyHint: &readOnly}},
					{Name: "customer", Annotations: &domain.OnboardingToolAnnotations{DestructiveHint: &destructive}},
				},
			},
			Errors: []domain.OnboardingDiscoveryError{{Server: "github", Message: "spawn failed"}},
		},
		"codex": {Builtin: []domain.OnboardingReportedTool{{Name: "Bash"}, {Name: "apply_patch"}}},
	}
	if _, err := h.service.ReportTools(ctx, session, map[string]domain.OnboardingReportedTools{"claude-code": reported["claude-code"]}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("tools missing a selected runtime should be invalid, got %v", err)
	}
	session, err = h.service.ReportTools(ctx, session, reported)
	if err != nil {
		t.Fatalf("ReportTools: %v", err)
	}
	mustStatus(t, session, domain.OnboardingToolsDiscovered)
	suggested := map[string]bool{}
	for _, tool := range session.Tools["claude-code"] {
		suggested[tool.Name] = tool.Suggested
	}
	for name, want := range map[string]bool{"Bash": true, "Edit": false, "Read": false, "create_refund": true, "get_customer": false, "customer": true} {
		if suggested[name] != want {
			t.Fatalf("%s suggested=%v, want %v", name, suggested[name], want)
		}
	}
	if got := SuggestedToolNames(session, "claude-code"); strings.Join(got, ",") != "Bash,create_refund,customer" {
		t.Fatalf("suggested tools: %v", got)
	}
	if len(session.DiscoveryErrors["claude-code"]) != 1 {
		t.Fatalf("discovery errors lost: %+v", session.DiscoveryErrors)
	}

	// Enrolling before provisioning is a status conflict, and provisioning
	// waits for the person to have passed the notification steps, because
	// the path it writes notifies them.
	if _, err := h.service.Enroll(ctx, session); !errors.Is(err, domain.ErrOnboardingState) {
		t.Fatalf("Enroll before provisioning: %v", err)
	}
	if _, err := h.service.Provision(ctx, h.principal); !errors.As(err, &step) || step.Code != "channels_not_set" {
		t.Fatalf("Provision before the channels: %v", err)
	}
	session, err = h.service.ConfirmChannels(ctx, h.principal)
	if err != nil {
		t.Fatalf("ConfirmChannels: %v", err)
	}
	mustStatus(t, session, domain.OnboardingToolsDiscovered)
	if session.ChannelsSetAt == nil {
		t.Fatalf("channels step should be stamped: %+v", session)
	}
	if _, err := h.service.RecordRules(ctx, h.principal); !errors.Is(err, domain.ErrOnboardingState) {
		t.Fatalf("RecordRules before the hooks are installed: %v", err)
	}

	session, err = h.service.Provision(ctx, h.principal)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	mustStatus(t, session, domain.OnboardingProvisioning)
	if session.Generated.EscalationPathKey != "notify-chris-battarbee" || session.Generated.EscalationPathName != "Notify Chris Battarbee" {
		t.Fatalf("generated path: %+v", session.Generated)
	}
	if len(h.paths.activated) != 1 || !PathTargetsMembership(h.paths.created["notify-chris-battarbee"], h.principal.MembershipID) {
		t.Fatalf("path not created for the member: %+v", h.paths.created)
	}
	if got := GatedToolsOf(h.pipelines.created["claude-code"]); strings.Join(got, ",") != "Bash,create_refund,customer" {
		t.Fatalf("claude-code gated tools: %v", got)
	}
	if got := GatedToolsOf(h.pipelines.created["codex"]); strings.Join(got, ",") != "Bash" {
		t.Fatalf("codex gated tools: %v", got)
	}
	if h.pipelines.created["codex"][0].CEL.EscalationPath != "notify-chris-battarbee" || h.pipelines.created["codex"][1].Always == nil || h.pipelines.created["codex"][1].Always.Outcome != approvalblock.Approve {
		t.Fatalf("pipeline blocks: %+v", h.pipelines.created["codex"])
	}
	if session.Generated.Pipelines["codex"].Revision != 2 || session.Generated.Pipelines["codex"].ReplacedActiveRevision != 1 {
		t.Fatalf("generated pipelines: %+v", session.Generated.Pipelines)
	}

	// The CLI's turn: enrollment mints one agent and instance per runtime,
	// the runtime key as the slug and its display name as the name.
	h.minter.conflicts = 1
	enrolled, err := h.service.Enroll(ctx, session)
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	mustStatus(t, enrolled.Session, domain.OnboardingEnrolled)
	if strings.Join(h.minter.created, ",") != "claude-code=Claude Code,codex=Codex" {
		t.Fatalf("agents created: %v", h.minter.created)
	}
	if codex := enrolled.Session.Enrollments[1]; codex.AgentSlug != "codex" || codex.AgentName != "Codex" {
		t.Fatalf("enrollment record lost the agent identity: %+v", codex)
	}
	if strings.Join(h.minter.instances, ",") != "chris-mbp-2,chris-mbp" {
		t.Fatalf("instance names should suffix on conflict: %v", h.minter.instances)
	}
	if len(enrolled.Enrollments) != 2 || enrolled.Enrollments[0].Credential.Token == "" {
		t.Fatalf("enrollments: %+v", enrolled.Enrollments)
	}
	if enrolled.Hooks["claude-code"].Matcher != ".*" || enrolled.Hooks["codex"].Matcher != ".*" {
		t.Fatalf("hook plans: %+v", enrolled.Hooks)
	}
	if len(enrolled.Session.Enrollments) != 2 || enrolled.Session.Enrollments[0].CredentialID == "" {
		t.Fatalf("session enrollments: %+v", enrolled.Session.Enrollments)
	}
	if _, err := h.service.Enroll(ctx, enrolled.Session); !errors.Is(err, domain.ErrOnboardingState) {
		t.Fatalf("second Enroll: %v", err)
	}

	session, err = h.service.ReportInstall(ctx, enrolled.Session, []domain.OnboardingInstallResult{
		{Runtime: "claude-code", OK: true, Changes: []domain.OnboardingFileChange{{Path: "~/.claude/settings.json", Action: "updated"}}},
		{Runtime: "codex", OK: false, Manual: true, Message: "trust the hook", Snippet: "[hooks]"},
	})
	if err != nil {
		t.Fatalf("ReportInstall: %v", err)
	}
	mustStatus(t, session, domain.OnboardingInstalled)
	if command := session.TestCommands["claude-code"]; command.Tool != "Bash" || !strings.Contains(command.Command, "claude -p") {
		t.Fatalf("claude-code test command: %+v", command)
	}
	if command := session.TestCommands["codex"]; command.Tool != "Bash" || !strings.HasPrefix(command.Command, "codex exec --skip-git-repo-check ") {
		t.Fatalf("codex test command: %+v", command)
	}

	// Leaving the rules step is stamped once the hooks are in; the test
	// counts what arrives after it.
	session, err = h.service.RecordRules(ctx, h.principal)
	if err != nil {
		t.Fatalf("RecordRules: %v", err)
	}
	mustStatus(t, session, domain.OnboardingInstalled)
	if session.RulesSetAt == nil {
		t.Fatalf("rules step should be stamped: %+v", session)
	}

	// The test: only a decided request from an enrolled agent counts.
	if err := h.service.Complete(ctx, h.principal, domain.OnboardingFinished); !errors.Is(err, domain.ErrOnboardingState) {
		t.Fatalf("Complete before the test should refuse, got %v", err)
	}
	enrolledAgent := enrolled.Session.Enrollments[0].AgentSlug
	pending := domain.ApprovalRequest{ID: domain.ID(uuid.NewString()), OrganizationID: h.principal.OrganizationID, AgentSlug: enrolledAgent, Status: domain.ApprovalPending}
	stranger := domain.ApprovalRequest{ID: domain.ID(uuid.NewString()), OrganizationID: h.principal.OrganizationID, AgentSlug: "support-bot", Status: domain.ApprovalApproved}
	decided := domain.ApprovalRequest{ID: domain.ID(uuid.NewString()), OrganizationID: h.principal.OrganizationID, AgentSlug: enrolledAgent, Status: domain.ApprovalDenied}
	for _, request := range []domain.ApprovalRequest{pending, stranger, decided} {
		h.approvals.requests[request.ID] = request
	}
	var stepErr *StepError
	if _, err := h.service.RecordTest(ctx, h.principal, domain.ID(uuid.NewString())); !errors.As(err, &stepErr) || stepErr.Code != "test_request_unknown" {
		t.Fatalf("RecordTest with an unknown request: %v", err)
	}
	if _, err := h.service.RecordTest(ctx, h.principal, stranger.ID); !errors.As(err, &stepErr) || stepErr.Code != "test_request_not_enrolled" {
		t.Fatalf("RecordTest with another agent's request: %v", err)
	}
	if _, err := h.service.RecordTest(ctx, h.principal, pending.ID); !errors.As(err, &stepErr) || stepErr.Code != "test_request_pending" {
		t.Fatalf("RecordTest with a pending request: %v", err)
	}
	session, err = h.service.RecordTest(ctx, h.principal, decided.ID)
	if err != nil {
		t.Fatalf("RecordTest: %v", err)
	}
	mustStatus(t, session, domain.OnboardingTested)
	if session.TestRequestID == nil || *session.TestRequestID != decided.ID || session.TestedAt == nil {
		t.Fatalf("tested session should carry the request and time: %+v", session)
	}
	// The person's own steps can be revisited from the test.
	if _, err := h.service.ConfirmChannels(ctx, h.principal); err != nil {
		t.Fatalf("ConfirmChannels from the test: %v", err)
	}

	if err := h.service.Complete(ctx, h.principal, domain.OnboardingFinished); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, err := h.service.Get(ctx, h.principal); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("session should be closed, got %v", err)
	}
}

func TestProvisionFailureKeepsSessionAndRecordsError(t *testing.T) {
	ctx := context.Background()
	h := newHarness()
	h.pipelines.failOn = "codex"
	begun, _ := h.service.Begin(ctx, h.principal)
	exchanged, _ := h.service.Exchange(ctx, begun.SetupCode, domain.OnboardingMachine{Hostname: "box"})
	session, _ := h.service.ReportDetections(ctx, exchanged.Session, detections("codex"))
	session, _ = h.service.SelectAgents(ctx, h.principal, []string{"codex"})
	session, err := h.service.ReportTools(ctx, session, map[string]domain.OnboardingReportedTools{"codex": {Builtin: []domain.OnboardingReportedTool{{Name: "Bash"}}}})
	if err != nil {
		t.Fatalf("ReportTools: %v", err)
	}
	if _, err := h.service.ConfirmChannels(ctx, h.principal); err != nil {
		t.Fatalf("ConfirmChannels: %v", err)
	}
	if _, err := h.service.Provision(ctx, h.principal); !errors.Is(err, ErrProvisionFailed) {
		t.Fatalf("Provision should report the failure, got %v", err)
	}
	after, _ := h.service.Get(ctx, h.principal)
	mustStatus(t, after, domain.OnboardingToolsDiscovered)
	if after.LastError == nil || after.LastError.Code != "provision_failed" || after.Version <= session.Version {
		t.Fatalf("last error not recorded: %+v", after.LastError)
	}
	// The path was already activated; a retry reuses it rather than
	// creating a second one.
	h.paths.active["notify-chris-battarbee"] = reviewrouting.PathRevision{
		Revision: domain.EscalationPathRevision{PathKey: "notify-chris-battarbee", Revision: 1, IsActive: true},
		Document: h.paths.created["notify-chris-battarbee"],
	}
	h.pipelines.failOn = ""
	provisioned, err := h.service.Provision(ctx, h.principal)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(h.paths.activated) != 1 || provisioned.LastError != nil {
		t.Fatalf("retry should reuse the path and clear the error: activated=%v err=%+v", h.paths.activated, provisioned.LastError)
	}
}

func TestSecondRunUnionsPipelineAndReusesAgent(t *testing.T) {
	ctx := context.Background()
	h := newHarness()
	h.pipelines.active["claude-code"] = approvalpipeline.PipelineRevision{
		Revision: domain.RequestApprovalPipelineRevision{Revision: 3, IsActive: true},
		Blocks:   []approvalpipeline.PipelineBlock{{Definition: PipelineBlocks([]string{"Write"}, "notify-x")[0]}, {Definition: PipelineBlocks(nil, "notify-x")[1]}},
	}
	h.agents.existing["claude-code"] = domain.Agent{ID: domain.ID(uuid.NewString()), Name: "Claude Code", AgentSlug: "claude-code", Status: domain.AgentActive}
	begun, _ := h.service.Begin(ctx, h.principal)
	exchanged, _ := h.service.Exchange(ctx, begun.SetupCode, domain.OnboardingMachine{Hostname: "second"})
	session, _ := h.service.ReportDetections(ctx, exchanged.Session, detections("claude-code"))
	session, _ = h.service.SelectAgents(ctx, h.principal, []string{"claude-code"})
	session, _ = h.service.ReportTools(ctx, session, map[string]domain.OnboardingReportedTools{"claude-code": {Builtin: []domain.OnboardingReportedTool{{Name: "Bash"}}}})
	session, _ = h.service.ConfirmChannels(ctx, h.principal)
	session, err := h.service.Provision(ctx, h.principal)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if got := GatedToolsOf(h.pipelines.created["claude-code"]); strings.Join(got, ",") != "Bash,Write" {
		t.Fatalf("second run should union: %v", got)
	}
	if expected := h.pipelines.activated["claude-code"]; expected != 3 {
		t.Fatalf("activation should carry the replaced revision, got %v", expected)
	}
	if replaced := session.Generated.Pipelines["claude-code"].ReplacedActiveRevision; replaced != 3 {
		t.Fatalf("generated should record the replaced revision: %+v", session.Generated.Pipelines)
	}
	enrolled, err := h.service.Enroll(ctx, session)
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if len(h.minter.created) != 0 || enrolled.Enrollments[0].Agent.ID != h.agents.existing["claude-code"].ID {
		t.Fatalf("existing agent should be reused: created=%v", h.minter.created)
	}
}

func TestProvisionRefusesArchivedAgentAndEnrollRefusesSuspendedMember(t *testing.T) {
	ctx := context.Background()
	h := newHarness()
	archivedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	h.agents.existing["codex"] = domain.Agent{ID: domain.ID(uuid.NewString()), Name: "Old Codex", AgentSlug: "codex", Status: domain.AgentArchived, ArchivedAt: &archivedAt}
	begun, _ := h.service.Begin(ctx, h.principal)
	exchanged, _ := h.service.Exchange(ctx, begun.SetupCode, domain.OnboardingMachine{Hostname: "box"})
	session, _ := h.service.ReportDetections(ctx, exchanged.Session, detections("codex"))
	session, _ = h.service.SelectAgents(ctx, h.principal, []string{"codex"})
	session, _ = h.service.ReportTools(ctx, session, map[string]domain.OnboardingReportedTools{"codex": {Builtin: []domain.OnboardingReportedTool{{Name: "Bash"}}}})
	session, _ = h.service.ConfirmChannels(ctx, h.principal)
	// Provisioning finds or creates the runtime's agent before its
	// pipeline. An archived agent still holds the runtime's slug, which
	// the guided setup can neither reuse nor restore, so the step fails
	// with the code the page keys on and nothing is created.
	var step *StepError
	if _, err := h.service.Provision(ctx, h.principal); !errors.Is(err, ErrProvisionFailed) || !errors.As(err, &step) || step.Code != "agent_archived" || step.Param != "codex" || !errors.Is(err, domain.ErrAgentArchived) {
		t.Fatalf("archived agent holding the slug: %v", err)
	}
	if len(h.minter.created) != 0 {
		t.Fatalf("archived slug must not be recreated: %v", h.minter.created)
	}
	delete(h.agents.existing, "codex")
	session, err := h.service.Provision(ctx, h.principal)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	h.identities.membership.Status = domain.MembershipSuspended
	if _, err := h.service.Enroll(ctx, session); !errors.Is(err, domain.ErrMembershipInactive) {
		t.Fatalf("suspended member: %v", err)
	}
	h.identities.membership.Status = domain.MembershipActive
	h.roles.grants = domain.GrantsForRoles([]domain.Role{domain.RoleAdmin})
	if _, err := h.service.Enroll(ctx, session); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member who lost the reviewer role: %v", err)
	}
}

func TestAuthorization(t *testing.T) {
	ctx := context.Background()
	h := newHarness()
	admin := h.principal
	admin = admin.WithGrants(domain.GrantsForRoles([]domain.Role{domain.RoleAdmin}))
	if _, err := h.service.Begin(ctx, admin); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin without decide should be refused, got %v", err)
	}
	if err := h.service.Complete(ctx, admin, domain.OnboardingSkip); err != nil {
		t.Fatalf("admin may skip: %v", err)
	}
	reviewer := h.principal
	reviewer = reviewer.WithGrants(domain.GrantsForRoles([]domain.Role{domain.RoleReviewer}))
	if err := h.service.Complete(ctx, reviewer, domain.OnboardingSkip); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("reviewer may not skip: %v", err)
	}
	agent := domain.Principal{ActorType: domain.ActorAgent, OrganizationID: h.principal.OrganizationID}.WithGrants(h.principal.Grants)
	if _, err := h.service.Get(ctx, agent); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("agent principal: %v", err)
	}
	required, _, err := h.service.Required(ctx, h.principal)
	if err == nil && !required {
		t.Fatal("owner of an unfinished organization should need onboarding")
	}
}

func TestExpiredSessionRefusesWrites(t *testing.T) {
	ctx := context.Background()
	h := newHarness()
	begun, _ := h.service.Begin(ctx, h.principal)
	exchanged, _ := h.service.Exchange(ctx, begun.SetupCode, domain.OnboardingMachine{})
	h.sessions.sessions[begun.Session.ID].ExpiresAt = time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC)
	if _, err := h.service.ReportDetections(ctx, exchanged.Session, detections()); !errors.Is(err, domain.ErrOnboardingExpired) {
		t.Fatalf("expired session: %v", err)
	}
}

func TestGenerators(t *testing.T) {
	if got := PathSlug("  Dana O'Neil!! ", "m"); got != "notify-dana-o-neil" {
		t.Fatalf("PathSlug: %s", got)
	}
	if got := PathSlug("日本", "0123456789abcdef-1"); got != "notify-01234567" {
		t.Fatalf("PathSlug fallback: %s", got)
	}
	if got := PathSlug(strings.Repeat("a", 80), "m"); len(got) != len(pathKeyPrefix)+maxPathSlug || !approvalblock.ValidEscalationPathKey(got) {
		t.Fatalf("PathSlug length: %s", got)
	}
	if PathName("") != "Notify me" || PathName("Chris") != "Notify Chris" {
		t.Fatal("PathName")
	}
	document := PathDocument("11111111-1111-1111-1111-111111111111")
	if err := document.Validate(); err != nil {
		t.Fatalf("generated path document is invalid: %v", err)
	}
	if !PathTargetsMembership(document, "11111111-1111-1111-1111-111111111111") || PathTargetsMembership(document, "other") {
		t.Fatal("PathTargetsMembership")
	}
	blocks := PipelineBlocks([]string{"Write", "Bash"}, "notify-x")
	for _, block := range blocks {
		if err := block.Validate(); err != nil {
			t.Fatalf("generated block %s is invalid: %v", block.ID, err)
		}
	}
	if got := GatedToolsOf(blocks); strings.Join(got, ",") != "Bash,Write" {
		t.Fatalf("GatedToolsOf: %v", got)
	}
	// The product quotes block names inside sentences, so they stay
	// verb-first labels rather than sentences of their own.
	if blocks[0].Name != "Review tools chosen in setup" || blocks[1].Name != "Approve every other tool" {
		t.Fatalf("block names: %q, %q", blocks[0].Name, blocks[1].Name)
	}
	if GatedToolsOf(blocks[1:]) != nil || GatedToolsOf(nil) != nil {
		t.Fatal("GatedToolsOf should ignore foreign first blocks")
	}
	if got := Union([]string{"b", "a"}, nil, []string{"a", "", "c"}); strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("Union: %v", got)
	}
	if got := Union(); got == nil || len(got) != 0 {
		t.Fatal("Union of nothing should be an empty, non-nil list")
	}
	session := domain.OnboardingSession{
		SelectedRuntimes: []string{"claude-code", "pi"},
		Tools: map[string][]domain.OnboardingTool{
			"claude-code": {{Name: "Edit", Source: "builtin", Kind: "edit", Suggested: true}, {Name: "create_refund", Source: "mcp", Server: "stripe", Suggested: true}, {Name: "Bash", Source: "builtin", Kind: "shell", Suggested: true}, {Name: "Read", Source: "builtin", Kind: "read"}},
			"pi":          {{Name: "write", Source: "builtin", Kind: "edit", Suggested: true}},
		},
	}
	commands := TestCommands(session)
	if commands["claude-code"].Tool != "Bash" || commands["pi"].Tool != "write" {
		t.Fatalf("TestCommands should prefer shell, then edit: %+v", commands)
	}
	plans := PlanHooks(session)
	if plans["claude-code"].Matcher != ".*" || plans["pi"].Matcher != "" || strings.Join(plans["pi"].Tools, ",") != "write" || strings.Join(plans["claude-code"].Tools, ",") != "Bash,Edit,create_refund" {
		t.Fatalf("PlanHooks: %+v", plans)
	}
	if NormalizeSetupCode(" abcd-efgh_jklm ") != "ABCD-EFGH-JKLM" || NormalizeSetupCode("abcd efgh jklm") != "ABCD-EFGH-JKLM" || NormalizeSetupCode("abc") != "ABC" {
		t.Fatalf("NormalizeSetupCode: %q %q", NormalizeSetupCode(" abcd-efgh_jklm "), NormalizeSetupCode("abcd efgh jklm"))
	}
}
