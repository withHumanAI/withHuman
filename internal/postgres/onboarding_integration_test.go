package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/db"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/embeddedpg/embeddedpgtest"
)

// Exercises the onboarding repository against real PostgreSQL: one open
// session per member with supersession, the single-use setup code, the
// who_ resolution joins, the optimistic status-checked write, expiry,
// and completion marking the organization.
func TestOnboardingRepositoryAgainstRealPostgres(t *testing.T) {
	server := embeddedpgtest.Start(t)
	ctx := context.Background()
	store, err := Open(ctx, server.URL("postgres"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := db.Apply(ctx, store.Pool(), db.Migrations()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	recordAudit(t, store)

	now := time.Now().UTC().Truncate(time.Microsecond)
	orgID := domain.ID(uuid.NewString())
	membershipID := domain.ID(uuid.NewString())
	if _, err := store.ActivateOrganization(ctx, application.ActivateOrganizationInput{
		OrganizationID: orgID, OrganizationSlug: "acme", OrganizationName: "Acme",
		UserID: domain.ID(uuid.NewString()), MembershipID: membershipID, DisplayName: "Owner",
		Email: "owner@example.com", PasswordHash: "argon2id$test",
		AdmissionSource: domain.AdmissionManual, Now: now,
	}); err != nil {
		t.Fatalf("ActivateOrganization: %v", err)
	}
	organization, err := store.GetOrganization(ctx, application.GetOrganizationInput{OrganizationID: orgID})
	if err != nil || organization.OnboardingCompletedAt != nil {
		t.Fatalf("fresh organization should not be onboarded: %v %+v", err, organization.OnboardingCompletedAt)
	}

	newSession := func(id domain.ID, codeHash []byte) application.CreateOnboardingSessionInput {
		return application.CreateOnboardingSessionInput{
			Session: domain.OnboardingSession{
				ID: id, OrganizationID: orgID, MembershipID: membershipID, Status: domain.OnboardingAwaitingCLI,
				CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
			},
			SetupCodeHash: codeHash, SetupCodeExpiresAt: now.Add(10 * time.Minute),
		}
	}
	first := domain.ID(uuid.NewString())
	if _, err := store.CreateOnboardingSession(ctx, newSession(first, []byte("code-1"))); err != nil {
		t.Fatalf("CreateOnboardingSession: %v", err)
	}
	second := domain.ID(uuid.NewString())
	created, err := store.CreateOnboardingSession(ctx, newSession(second, []byte("code-2")))
	if err != nil {
		t.Fatalf("second CreateOnboardingSession: %v", err)
	}
	if created.Version != 1 || created.Status != domain.OnboardingAwaitingCLI || len(created.Detections) != 0 {
		t.Fatalf("created session: %+v", created)
	}
	superseded, err := store.GetOnboardingSession(ctx, application.GetOnboardingSessionInput{OrganizationID: orgID, SessionID: first})
	if err != nil || superseded.Status != domain.OnboardingSkipped || superseded.CompletedAt == nil || superseded.LastError == nil || superseded.LastError.Code != "superseded" {
		t.Fatalf("first session should be superseded: %v %+v", err, superseded)
	}
	open, err := store.GetOpenOnboardingSession(ctx, application.GetOpenOnboardingSessionInput{OrganizationID: orgID, MembershipID: membershipID})
	if err != nil || open.ID != second {
		t.Fatalf("open session: %v %+v", err, open)
	}

	// A superseded code no longer exchanges; the live one does, once.
	if _, err := store.ExchangeOnboardingSetupCode(ctx, application.ExchangeOnboardingSetupCodeInput{
		SetupCodeHash: []byte("code-1"), CredentialSecretHash: []byte("secret"), Now: now,
	}); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("superseded code: %v", err)
	}
	machine := domain.OnboardingMachine{Hostname: "laptop", OS: "darwin", Arch: "arm64", CLIVersion: "0.1.0"}
	exchanged, err := store.ExchangeOnboardingSetupCode(ctx, application.ExchangeOnboardingSetupCodeInput{
		SetupCodeHash: []byte("code-2"), CredentialSecretHash: []byte("secret"), Machine: machine, Now: now.Add(time.Second),
	})
	if err != nil || exchanged.Status != domain.OnboardingConnected || exchanged.Version != 2 || exchanged.Machine != machine {
		t.Fatalf("exchange: %v %+v", err, exchanged)
	}
	if _, err := store.ExchangeOnboardingSetupCode(ctx, application.ExchangeOnboardingSetupCodeInput{
		SetupCodeHash: []byte("code-2"), CredentialSecretHash: []byte("other"), Now: now.Add(time.Second),
	}); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("replayed code: %v", err)
	}

	resolved, err := store.ResolveOnboardingCredential(ctx, application.ResolveOnboardingCredentialInput{SessionID: second, SecretHash: []byte("secret"), Now: now.Add(time.Second)})
	if err != nil || resolved.ID != second {
		t.Fatalf("resolve: %v %+v", err, resolved)
	}
	if _, err := store.ResolveOnboardingCredential(ctx, application.ResolveOnboardingCredentialInput{SessionID: second, SecretHash: []byte("wrong"), Now: now}); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("wrong secret: %v", err)
	}
	if _, err := store.ResolveOnboardingCredential(ctx, application.ResolveOnboardingCredentialInput{SessionID: second, SecretHash: []byte("secret"), Now: now.Add(2 * time.Hour)}); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("expired session should not resolve: %v", err)
	}

	// The status-checked write: wrong expected status conflicts, the
	// right one applies the patch and bumps the version.
	detections := []domain.OnboardingDetection{{Runtime: "claude-code", Installed: true, Version: "2.0", MCPServers: []domain.OnboardingMCPServer{{Name: "stripe", Transport: "stdio", Source: "~/.claude.json"}}}}
	if _, err := store.UpdateOnboardingSession(ctx, application.UpdateOnboardingSessionInput{
		OrganizationID: orgID, SessionID: second, ExpectedStatuses: []domain.OnboardingStatus{domain.OnboardingDetected},
		Patch: domain.OnboardingSessionPatch{Detections: &detections}, NextStatus: domain.OnboardingAgentsSelected, Now: now.Add(2 * time.Second),
	}); !errors.Is(err, domain.ErrOnboardingState) {
		t.Fatalf("wrong expected status: %v", err)
	}
	updated, err := store.UpdateOnboardingSession(ctx, application.UpdateOnboardingSessionInput{
		OrganizationID: orgID, SessionID: second, ExpectedStatuses: []domain.OnboardingStatus{domain.OnboardingConnected, domain.OnboardingDetected},
		Patch: domain.OnboardingSessionPatch{Detections: &detections}, NextStatus: domain.OnboardingDetected, Now: now.Add(2 * time.Second),
	})
	if err != nil || updated.Status != domain.OnboardingDetected || updated.Version != 3 || len(updated.Detections) != 1 || updated.Detections[0].MCPServers[0].Name != "stripe" {
		t.Fatalf("update: %v %+v", err, updated)
	}
	selected := []string{"claude-code"}
	tools := map[string][]domain.OnboardingTool{"claude-code": {{Name: "Bash", Source: "builtin", Kind: "shell", Suggested: true, Reason: "shell"}}}
	lastError := domain.OnboardingError{Source: "cli", Code: "x", Message: "y"}
	updated, err = store.UpdateOnboardingSession(ctx, application.UpdateOnboardingSessionInput{
		OrganizationID: orgID, SessionID: second,
		Patch: domain.OnboardingSessionPatch{SelectedRuntimes: &selected, Tools: &tools, LastError: &lastError}, Now: now.Add(3 * time.Second),
	})
	if err != nil || updated.Version != 4 || updated.SelectedRuntimes[0] != "claude-code" || updated.Tools["claude-code"][0].Reason != "shell" || updated.LastError == nil {
		t.Fatalf("patch: %v %+v", err, updated)
	}
	updated, err = store.UpdateOnboardingSession(ctx, application.UpdateOnboardingSessionInput{
		OrganizationID: orgID, SessionID: second, Patch: domain.OnboardingSessionPatch{ClearLastError: true}, Now: now.Add(4 * time.Second),
	})
	if err != nil || updated.LastError != nil {
		t.Fatalf("clear error: %v %+v", err, updated.LastError)
	}
	// Tenant scoping: another organization cannot touch the session.
	if _, err := store.UpdateOnboardingSession(ctx, application.UpdateOnboardingSessionInput{
		OrganizationID: domain.ID(uuid.NewString()), SessionID: second, Patch: domain.OnboardingSessionPatch{ClearLastError: true}, Now: now,
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant update: %v", err)
	}

	// Wait returns at once when the version already moved.
	waited, err := store.WaitOnboardingSession(ctx, application.WaitOnboardingSessionInput{OrganizationID: orgID, SessionID: second, SinceVersion: 1, Timeout: 5 * time.Second})
	if err != nil || waited.Version != 5 {
		t.Fatalf("wait: %v %+v", err, waited.Version)
	}

	// Finishing needs an installed session; skipping does not, and both
	// mark the organization.
	if err := store.CompleteOnboarding(ctx, application.CompleteOnboardingInput{OrganizationID: orgID, MembershipID: membershipID, Outcome: domain.OnboardingFinished, Now: now.Add(5 * time.Second)}); !errors.Is(err, domain.ErrOnboardingState) {
		t.Fatalf("finish before installed: %v", err)
	}
	if err := store.CompleteOnboarding(ctx, application.CompleteOnboardingInput{OrganizationID: orgID, MembershipID: membershipID, Outcome: domain.OnboardingSkip, Now: now.Add(5 * time.Second)}); err != nil {
		t.Fatalf("skip: %v", err)
	}
	closed, err := store.GetOnboardingSession(ctx, application.GetOnboardingSessionInput{OrganizationID: orgID, SessionID: second})
	if err != nil || closed.Status != domain.OnboardingSkipped || closed.CompletedAt == nil {
		t.Fatalf("closed session: %v %+v", err, closed)
	}
	organization, err = store.GetOrganization(ctx, application.GetOrganizationInput{OrganizationID: orgID})
	if err != nil || organization.OnboardingCompletedAt == nil {
		t.Fatalf("organization should be marked onboarded: %v %+v", err, organization)
	}
	if _, err := store.UpdateOnboardingSession(ctx, application.UpdateOnboardingSessionInput{
		OrganizationID: orgID, SessionID: second, Patch: domain.OnboardingSessionPatch{ClearLastError: true}, Now: now.Add(6 * time.Second),
	}); !errors.Is(err, domain.ErrOnboardingState) {
		t.Fatalf("terminal session should refuse writes: %v", err)
	}

	// An expired open session refuses writes with the expiry error.
	third := domain.ID(uuid.NewString())
	input := newSession(third, []byte("code-3"))
	input.Session.ExpiresAt = now.Add(time.Minute)
	if _, err := store.CreateOnboardingSession(ctx, input); err != nil {
		t.Fatalf("third session: %v", err)
	}
	if _, err := store.UpdateOnboardingSession(ctx, application.UpdateOnboardingSessionInput{
		OrganizationID: orgID, SessionID: third, Patch: domain.OnboardingSessionPatch{ClearLastError: true}, Now: now.Add(2 * time.Minute),
	}); !errors.Is(err, domain.ErrOnboardingExpired) {
		t.Fatalf("expired session: %v", err)
	}

	// The agent lookup by slug onboarding uses to reuse agents.
	agent, err := store.CreateAgent(ctx, application.CreateAgentInput{
		OrganizationID: orgID, ActorID: membershipID, Name: "claude-code", AgentSlug: "claude-code", AgentQuota: application.UnlimitedQuota(), Now: now,
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	found, err := store.GetAgentBySlug(ctx, application.GetAgentBySlugInput{OrganizationID: orgID, AgentSlug: "claude-code"})
	if err != nil || found.ID != agent.ID {
		t.Fatalf("GetAgentBySlug: %v %+v", err, found)
	}
	if _, err := store.GetAgentBySlug(ctx, application.GetAgentBySlugInput{OrganizationID: orgID, AgentSlug: "codex"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing agent: %v", err)
	}
}
