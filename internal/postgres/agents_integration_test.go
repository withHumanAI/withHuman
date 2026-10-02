package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/db"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/embeddedpg/embeddedpgtest"
)

// Exercises the agent inventory: summaries derive liveness from
// credentials and count requests inside the recent window, the detail
// page carries every credential and provisioner, and the status switches
// are reversible, audited, and enforced at credential resolution.
func TestAgentInventoryAgainstRealPostgres(t *testing.T) {
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
	orgID, userID, membershipID := domain.ID(uuid.NewString()), domain.ID(uuid.NewString()), domain.ID(uuid.NewString())
	if _, err := store.ActivateOrganization(ctx, application.ActivateOrganizationInput{
		OrganizationID: orgID, OrganizationSlug: "acme", OrganizationName: "Acme",
		UserID: userID, MembershipID: membershipID, DisplayName: "Ece Kayan",
		Email: "owner@example.com", PasswordHash: "argon2id$test",
		AdmissionSource: domain.AdmissionManual, Now: now,
	}); err != nil {
		t.Fatalf("ActivateOrganization: %v", err)
	}

	createAgent := func(name string) domain.Agent {
		agentSlug := domain.AgentSlug(strings.ReplaceAll(name, " ", "-"))
		agent, err := store.CreateAgent(ctx, application.CreateAgentInput{Kind: domain.AgentService,
			OrganizationID: orgID, ActorID: membershipID, Name: name, AgentSlug: agentSlug, AgentQuota: application.UnlimitedQuota(), Now: now,
		})
		if err != nil {
			t.Fatalf("CreateAgent %s: %v", name, err)
		}
		return agent
	}
	type issued struct {
		instance domain.AgentInstance
		id       domain.ID
		secret   []byte
	}
	createInstance := func(agent domain.Agent, name string, expiresAt time.Time) issued {
		secret := sha256.Sum256([]byte(name))
		credentialID := domain.ID(uuid.NewString())
		instance, err := store.CreateAgentInstanceWithCredential(ctx, application.CreateAgentInstanceWithCredentialInput{ActorType: domain.ActorHuman,
			OrganizationID: orgID, AgentID: agent.ID, ActorID: userID, Name: name,
			Metadata: json.RawMessage(`{"host":"` + name + `"}`),
			Credential: application.AgentCredentialRecord{
				ID: credentialID, SecretHash: secret[:], IssuedAt: now.Add(-time.Hour), ExpiresAt: expiresAt,
			},
			Now: now,
		})
		if err != nil {
			t.Fatalf("CreateAgentInstanceWithCredential %s: %v", name, err)
		}
		return issued{instance: instance, id: credentialID, secret: secret[:]}
	}
	createRequest := func(agent domain.Agent, instance domain.AgentInstance, key string, createdAt time.Time) domain.ApprovalRequest {
		request := domain.ApprovalRequest{
			Timeout: "1h",
			ID:      domain.ID(uuid.NewString()), OrganizationID: orgID,
			AgentID: agent.ID, AgentInstanceID: instance.ID, AgentSlug: agent.AgentSlug,
			Tool: "deploy", Arguments: json.RawMessage(`{"env":"prod"}`),
			IdempotencyKey: key, SemanticHash: "hash-" + key,
			Status: domain.ApprovalPending, DeadlineAt: createdAt.Add(time.Hour),
			CreatedAt: createdAt, UpdatedAt: createdAt,
		}
		if _, err := store.InitializeApproval(ctx, pendingApprovalInitialization(request)); err != nil {
			t.Fatalf("InitializeApproval %s: %v", key, err)
		}
		return request
	}

	// "release automation": one live instance, one whose credential
	// expired, one revoked. Two recent requests (one pending, one decided
	// by the pipeline) and one older than the window.
	release := createAgent("release automation")
	live := createInstance(release, "production runner", now.Add(24*time.Hour))
	expired := createInstance(release, "canary runner", now.Add(-time.Minute))
	revoked := createInstance(release, "old laptop", now.Add(24*time.Hour))
	if err := store.RevokeAgentCredential(ctx, application.RevokeAgentCredentialInput{ActorID: membershipID, Now: now, OrganizationID: orgID, CredentialID: revoked.id}); err != nil {
		t.Fatalf("RevokeAgentCredential: %v", err)
	}
	createRequest(release, live.instance, "release-pending", now)
	oldRequest := createRequest(release, live.instance, "release-old", now.Add(-application.RecentRequestWindow-time.Hour))
	decidedRequest := createRequest(release, expired.instance, "release-decided", now.Add(-time.Hour))
	for _, request := range []domain.ApprovalRequest{oldRequest, decidedRequest} {
		if _, err := store.ExpireApproval(ctx, application.ExpireApprovalInput{
			OrganizationID: orgID, RequestID: request.ID, Now: now,
		}); err != nil {
			t.Fatalf("ExpireApproval: %v", err)
		}
	}
	// A revoked credential tells its holder so; an expired one does not.
	var refused domain.CredentialRefusedError
	if _, err := store.ResolveAgentCredential(ctx, application.ResolveAgentCredentialInput{
		CredentialID: revoked.id, SecretHash: revoked.secret, Now: now,
	}); !errors.As(err, &refused) || refused.Reason != domain.CredentialRefusedRevoked || !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("a revoked credential must be refused as revoked, got %v", err)
	}
	if _, err := store.ResolveAgentCredential(ctx, application.ResolveAgentCredentialInput{
		CredentialID: expired.id, SecretHash: expired.secret, Now: now,
	}); !errors.Is(err, domain.ErrUnauthenticated) || errors.As(err, &refused) {
		t.Fatalf("an expired credential must stay plainly unauthenticated, got %v", err)
	}
	// Resolving the live credential stamps last_seen_at on its instance.
	if _, err := store.ResolveAgentCredential(ctx, application.ResolveAgentCredentialInput{
		CredentialID: live.id, SecretHash: live.secret, Now: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("ResolveAgentCredential: %v", err)
	}
	provisionerID := domain.ID(uuid.NewString())
	provisionerSecret := sha256.Sum256([]byte("provisioner"))
	if _, err := store.CreateAgentProvisioner(ctx, application.CreateAgentProvisionerInput{
		OrganizationID: orgID, AgentID: release.ID, ActorID: membershipID,
		Provisioner: application.AgentProvisionerRecord{
			ID: provisionerID, SecretHash: provisionerSecret[:], IssuedAt: now, ExpiresAt: now.Add(24 * time.Hour),
		},
		Now: now,
	}); err != nil {
		t.Fatalf("CreateAgentProvisioner: %v", err)
	}
	// "crm hygiene": no instances at all. "zebra" sorts after it by name
	// while active, and first-by-status once disabled agents sort last.
	crm := createAgent("crm hygiene")
	zebra := createAgent("zebra")

	summaries, err := store.ListAgentSummaries(ctx, application.ListAgentSummariesInput{OrganizationID: orgID, Scope: domain.ScopeSet{All: true}, Limit: 50, Now: now})
	if err != nil {
		t.Fatalf("ListAgentSummaries: %v", err)
	}
	if len(summaries) != 3 || summaries[0].ID != crm.ID || summaries[1].ID != release.ID || summaries[2].ID != zebra.ID {
		t.Fatalf("summaries are not active-first by name: %#v", summaries)
	}
	got := summaries[1]
	if got.InstanceCount != 3 || got.LiveInstanceCount != 1 {
		t.Fatalf("release instance counts = %d/%d, want 3/1", got.InstanceCount, got.LiveInstanceCount)
	}
	if got.LastSeenAt == nil || !got.LastSeenAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("release last seen = %v, want the credential resolution time", got.LastSeenAt)
	}
	if got.RecentRequestCount != 2 || got.PendingRequestCount != 1 {
		t.Fatalf("release request counts = %d recent, %d pending; want 2 and 1", got.RecentRequestCount, got.PendingRequestCount)
	}
	// Both recent requests fall inside the newest day; the expired one is
	// outside the window and does not appear at all.
	if want := []int{0, 0, 0, 0, 0, 0, 2}; !slices.Equal(got.RequestTrend, want) {
		t.Fatalf("release request trend = %v, want %v", got.RequestTrend, want)
	}
	if want := make([]int, application.RecentRequestDays); !slices.Equal(summaries[0].RequestTrend, want) {
		t.Fatalf("an agent without requests must have a flat trend, got %v", summaries[0].RequestTrend)
	}
	if summaries[0].InstanceCount != 0 || summaries[0].LastSeenAt != nil || summaries[0].RecentRequestCount != 0 {
		t.Fatalf("an agent without instances must be empty, got %#v", summaries[0])
	}

	detail, err := store.GetAgentDetail(ctx, application.GetAgentDetailInput{OrganizationID: orgID, AgentID: release.ID, Now: now})
	if err != nil {
		t.Fatalf("GetAgentDetail: %v", err)
	}
	if detail.Name != "release automation" || detail.CreatedByDisplayName != "Ece Kayan" {
		t.Fatalf("detail identity = %q by %q", detail.Name, detail.CreatedByDisplayName)
	}
	if detail.RecentRequestCount != 2 || detail.PendingRequestCount != 1 {
		t.Fatalf("detail request counts = %d/%d", detail.RecentRequestCount, detail.PendingRequestCount)
	}
	if len(detail.Instances) != 3 || detail.Instances[0].ID != live.instance.ID {
		t.Fatalf("instances must sort by last seen, newest first: %#v", detail.Instances)
	}
	t.Run("paged instance detail keeps global counts and liveness", func(t *testing.T) {
		input := application.GetAgentDetailInput{OrganizationID: orgID, AgentID: release.ID, Now: now, InstanceLimit: 1}
		first, err := store.GetAgentDetail(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if len(first.Instances) != 1 || first.InstanceCount != 3 || first.MatchingInstanceCount != 3 || first.LiveInstanceCount != 1 || first.RequestCount != 3 {
			t.Fatalf("unexpected first page: %#v", first)
		}
		input.InstanceOffset = 1
		second, err := store.GetAgentDetail(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if len(second.Instances) != 1 || second.Instances[0].ID == first.Instances[0].ID || second.LiveInstanceCount != 1 {
			t.Fatalf("page overlaps or loses liveness: %#v", second)
		}
		input.InstanceOffset, input.InstanceQuery = 0, "CANARY"
		filtered, err := store.GetAgentDetail(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if len(filtered.Instances) != 1 || filtered.Instances[0].ID != expired.instance.ID || filtered.MatchingInstanceCount != 1 || filtered.LiveInstanceCount != 1 || len(filtered.Instances[0].Credentials) != 1 {
			t.Fatalf("wrong filtered page: %#v", filtered)
		}
		input.InstanceQuery = "%"
		empty, err := store.GetAgentDetail(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if len(empty.Instances) != 0 || empty.MatchingInstanceCount != 0 || empty.InstanceCount != 3 || empty.LiveInstanceCount != 1 {
			t.Fatalf("literal query or empty-page counts failed: %#v", empty)
		}
		input.InstanceQuery, input.InstanceStatus = "", domain.AgentDisabled
		disabledPage, err := store.GetAgentDetail(ctx, input)
		if err != nil || disabledPage.MatchingInstanceCount != 0 {
			t.Fatalf("disabled filter: %#v, %v", disabledPage, err)
		}
		input.OrganizationID = domain.ID(uuid.NewString())
		if _, err := store.GetAgentDetail(ctx, input); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("cross-organization detail: %v", err)
		}
	})
	t.Run("agent history pages and filters before applying the limit", func(t *testing.T) {
		input := application.ListApprovalsInput{OrganizationID: orgID, AgentSlug: release.AgentSlug, Limit: 1}
		first, err := store.ListApprovals(ctx, input)
		if err != nil || len(first) != 1 || first[0].Status != domain.ApprovalPending {
			t.Fatalf("first page: %#v, %v", first, err)
		}
		input.Offset = 1
		second, err := store.ListApprovals(ctx, input)
		if err != nil || len(second) != 1 || second[0].ID != decidedRequest.ID {
			t.Fatalf("second page: %#v, %v", second, err)
		}
		input.Offset, input.Status, input.Query = 0, domain.ApprovalExpired, "CANARY"
		filtered, err := store.ListApprovals(ctx, input)
		if err != nil || len(filtered) != 1 || filtered[0].ID != decidedRequest.ID {
			t.Fatalf("filtered history: %#v, %v", filtered, err)
		}
		input.Query = "%"
		empty, err := store.ListApprovals(ctx, input)
		if err != nil || len(empty) != 0 {
			t.Fatalf("literal query: %#v, %v", empty, err)
		}
		input.Query, input.Status, input.AgentSlug = "deploy", "", crm.AgentSlug
		empty, err = store.ListApprovals(ctx, input)
		if err != nil || len(empty) != 0 {
			t.Fatalf("other agent leaked history: %#v, %v", empty, err)
		}
		input.AgentSlug, input.OrganizationID = release.AgentSlug, domain.ID(uuid.NewString())
		empty, err = store.ListApprovals(ctx, input)
		if err != nil || len(empty) != 0 {
			t.Fatalf("other organization leaked history: %#v, %v", empty, err)
		}
	})
	byName := make(map[string]domain.AgentInstanceDetail)
	for _, instance := range detail.Instances {
		byName[instance.Name] = instance
	}
	if credentials := byName["production runner"].Credentials; len(credentials) != 1 || !credentials[0].Live(now) || credentials[0].LastUsedAt == nil {
		t.Fatalf("live instance credentials = %#v", credentials)
	}
	if credentials := byName["canary runner"].Credentials; len(credentials) != 1 || credentials[0].Live(now) || credentials[0].RevokedAt != nil {
		t.Fatalf("expired instance credentials = %#v", credentials)
	}
	if credentials := byName["old laptop"].Credentials; len(credentials) != 1 || credentials[0].RevokedAt == nil {
		t.Fatalf("revoked instance credentials = %#v", credentials)
	}
	if byName["production runner"].RecentRequestCount != 1 || byName["canary runner"].RecentRequestCount != 1 {
		t.Fatalf("per-instance request counts = %#v", byName)
	}
	for name, instance := range byName {
		total := 0
		for _, count := range instance.RequestTrend {
			total += count
		}
		if len(instance.RequestTrend) != application.RecentRequestDays || total != instance.RecentRequestCount {
			t.Fatalf("%s request trend = %v; want %d buckets summing to %d", name, instance.RequestTrend, application.RecentRequestDays, instance.RecentRequestCount)
		}
	}
	if len(detail.Provisioners) != 1 || detail.Provisioners[0].ID != provisionerID || detail.Provisioners[0].RevokedAt != nil {
		t.Fatalf("provisioners = %#v", detail.Provisioners)
	}
	if _, err := store.GetAgentDetail(ctx, application.GetAgentDetailInput{OrganizationID: orgID, AgentID: domain.ID(uuid.NewString()), Now: now}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown agent must be not found, got %v", err)
	}

	// One agent's history is scoped to that agent.
	history, err := store.ListApprovals(ctx, application.ListApprovalsInput{OrganizationID: orgID, Limit: 10, AgentSlug: release.AgentSlug})
	if err != nil || len(history) != 3 {
		t.Fatalf("agent history = %d requests, %v; want 3", len(history), err)
	}
	other, err := store.ListApprovals(ctx, application.ListApprovalsInput{OrganizationID: orgID, Limit: 10, AgentSlug: crm.AgentSlug})
	if err != nil || len(other) != 0 {
		t.Fatalf("another agent's history = %d requests, %v; want 0", len(other), err)
	}
	// The agents' recent history keeps each agent's newest requests.
	recent, err := store.ListApprovals(ctx, application.ListApprovalsInput{OrganizationID: orgID, Limit: 10, PerAgent: 2})
	if err != nil || len(recent) != 2 || recent[0].ID != history[0].ID || recent[1].ID != history[1].ID {
		t.Fatalf("recent history = %#v, %v; want release's two newest", recent, err)
	}
	if _, err := store.ListApprovals(ctx, application.ListApprovalsInput{OrganizationID: orgID, PerAgent: 51}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("more than 50 per agent must be refused, got %v", err)
	}

	// Disabling the agent refuses its live credential and sorts it last;
	// enabling it restores both.
	disabled, err := store.SetAgentStatus(ctx, application.SetAgentStatusInput{
		OrganizationID: orgID, AgentID: zebra.ID, Status: domain.AgentDisabled, ActorID: membershipID, Now: now.Add(2 * time.Minute),
	})
	if err != nil || disabled.Status != domain.AgentDisabled {
		t.Fatalf("SetAgentStatus disabled = %#v, %v", disabled, err)
	}
	if _, err := store.SetAgentStatus(ctx, application.SetAgentStatusInput{
		OrganizationID: orgID, AgentID: release.ID, Status: domain.AgentDisabled, ActorID: membershipID, Now: now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("SetAgentStatus release: %v", err)
	}
	if _, err := store.ResolveAgentCredential(ctx, application.ResolveAgentCredentialInput{
		CredentialID: live.id, SecretHash: live.secret, Now: now.Add(3 * time.Minute),
	}); !errors.Is(err, domain.ErrAgentInactive) {
		t.Fatalf("disabled agent credential must be refused, got %v", err)
	}
	summaries, err = store.ListAgentSummaries(ctx, application.ListAgentSummariesInput{OrganizationID: orgID, Scope: domain.ScopeSet{All: true}, Limit: 50, Now: now})
	if err != nil || len(summaries) != 3 || summaries[0].ID != crm.ID || summaries[1].ID != release.ID || summaries[2].ID != zebra.ID {
		t.Fatalf("disabled agents must sort last: %#v, %v", summaries, err)
	}
	if summaries[1].LiveInstanceCount != 1 {
		t.Fatalf("disabling the agent must not rewrite instance liveness: %#v", summaries[1])
	}
	if _, err := store.SetAgentStatus(ctx, application.SetAgentStatusInput{
		OrganizationID: orgID, AgentID: release.ID, Status: domain.AgentActive, ActorID: membershipID, Now: now.Add(4 * time.Minute),
	}); err != nil {
		t.Fatalf("SetAgentStatus enabled: %v", err)
	}
	if _, err := store.ResolveAgentCredential(ctx, application.ResolveAgentCredentialInput{
		CredentialID: live.id, SecretHash: live.secret, Now: now.Add(5 * time.Minute),
	}); err != nil {
		t.Fatalf("re-enabled agent credential must resolve: %v", err)
	}
	if _, err := store.SetAgentStatus(ctx, application.SetAgentStatusInput{
		OrganizationID: orgID, AgentID: domain.ID(uuid.NewString()), Status: domain.AgentDisabled, ActorID: membershipID, Now: now,
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown agent status change must be not found, got %v", err)
	}

	// Disabling one instance leaves the agent and its siblings alone.
	instance, err := store.SetAgentInstanceStatus(ctx, application.SetAgentInstanceStatusInput{
		OrganizationID: orgID, InstanceID: live.instance.ID, Status: domain.AgentDisabled, ActorID: membershipID, Now: now.Add(6 * time.Minute),
	})
	if err != nil || instance.Status != domain.AgentDisabled || instance.Name != "production runner" {
		t.Fatalf("SetAgentInstanceStatus = %#v, %v", instance, err)
	}
	if _, err := store.ResolveAgentCredential(ctx, application.ResolveAgentCredentialInput{
		CredentialID: live.id, SecretHash: live.secret, Now: now.Add(7 * time.Minute),
	}); !errors.Is(err, domain.ErrAgentInactive) {
		t.Fatalf("disabled instance credential must be refused, got %v", err)
	}
	detail, err = store.GetAgentDetail(ctx, application.GetAgentDetailInput{OrganizationID: orgID, AgentID: release.ID, Now: now})
	if err != nil || detail.Status != domain.AgentActive {
		t.Fatalf("agent must stay active after an instance is disabled: %#v, %v", detail.Agent, err)
	}
	summaries, err = store.ListAgentSummaries(ctx, application.ListAgentSummariesInput{OrganizationID: orgID, Scope: domain.ScopeSet{All: true}, Limit: 50, Now: now})
	if err != nil || summaries[1].LiveInstanceCount != 0 {
		t.Fatalf("a disabled instance is not live: %#v, %v", summaries[1], err)
	}
	if _, err := store.SetAgentInstanceStatus(ctx, application.SetAgentInstanceStatusInput{
		OrganizationID: orgID, InstanceID: live.instance.ID, Status: domain.AgentActive, ActorID: membershipID, Now: now.Add(8 * time.Minute),
	}); err != nil {
		t.Fatalf("SetAgentInstanceStatus enabled: %v", err)
	}

	// Renaming an instance changes its display name alone: the name must
	// be free among the agent's instances, the requests already made keep
	// the name they were created with, and only a real change is audited.
	renamedAt := now.Add(9 * time.Minute)
	renamedInstance, err := store.RenameAgentInstance(ctx, application.RenameAgentInstanceInput{
		OrganizationID: orgID, InstanceID: live.instance.ID, Name: "chris laptop", ActorID: membershipID, Now: renamedAt,
	})
	if err != nil || renamedInstance.ID != live.instance.ID || renamedInstance.Name != "chris laptop" || renamedInstance.Status != domain.AgentActive || renamedInstance.AgentSlug != release.AgentSlug {
		t.Fatalf("RenameAgentInstance = %#v, %v", renamedInstance, err)
	}
	if _, err := store.RenameAgentInstance(ctx, application.RenameAgentInstanceInput{
		OrganizationID: orgID, InstanceID: live.instance.ID, Name: "canary runner", ActorID: membershipID, Now: renamedAt,
	}); !errors.Is(err, domain.ErrAgentInstanceNameTaken) || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("renaming onto a sibling's name must be refused, got %v", err)
	}
	if _, err := store.RenameAgentInstance(ctx, application.RenameAgentInstanceInput{
		OrganizationID: orgID, InstanceID: domain.ID(uuid.NewString()), Name: "nobody", ActorID: membershipID, Now: renamedAt,
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("renaming an unknown instance must be not found, got %v", err)
	}
	if _, err := store.RenameAgentInstance(ctx, application.RenameAgentInstanceInput{
		OrganizationID: orgID, InstanceID: live.instance.ID, Name: "chris laptop", ActorID: membershipID, Now: renamedAt.Add(time.Minute),
	}); err != nil {
		t.Fatalf("renaming to the current name must be a no-op, got %v", err)
	}
	detail, err = store.GetAgentDetail(ctx, application.GetAgentDetailInput{OrganizationID: orgID, AgentID: release.ID, Now: now, InstanceLimit: 25})
	if err != nil {
		t.Fatalf("GetAgentDetail after rename: %v", err)
	}
	if !slices.ContainsFunc(detail.Instances, func(instance domain.AgentInstanceDetail) bool {
		return instance.ID == live.instance.ID && instance.Name == "chris laptop"
	}) {
		t.Fatalf("the renamed instance must list under its new name: %#v", detail.Instances)
	}
	var requestInstanceName string
	if err := store.Pool().QueryRow(ctx, `SELECT agent_instance_name FROM approval_requests WHERE id = $1`, oldRequest.ID).Scan(&requestInstanceName); err != nil || requestInstanceName != "production runner" {
		t.Fatalf("an earlier request must keep the name it was created with, got %q, %v", requestInstanceName, err)
	}
	var renameEvents int
	var renamePayload map[string]any
	if err := store.Pool().QueryRow(ctx, `SELECT count(*), min(data::text)::jsonb FROM test_audit_events
		WHERE organization_id = $1 AND actor_id = $2 AND event_type = 'agent_instance.renamed' AND subject_id = $3`, orgID, userID, live.instance.ID).Scan(&renameEvents, &renamePayload); err != nil {
		t.Fatalf("query rename audit events: %v", err)
	}
	if renameEvents != 1 || renamePayload["agent_slug"] != string(release.AgentSlug) || renamePayload["previous_name"] != "production runner" || renamePayload["name"] != "chris laptop" {
		t.Fatalf("instance rename audit = %d events, payload %v", renameEvents, renamePayload)
	}
	if _, err := store.RenameAgentInstance(ctx, application.RenameAgentInstanceInput{
		OrganizationID: orgID, InstanceID: live.instance.ID, Name: "production runner", ActorID: membershipID, Now: renamedAt.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("the freed name must be usable again: %v", err)
	}

	// Every switch that changed something is audited and attributed to
	// the acting member; the no-op re-enable of an active instance is not.
	var events []string
	rows, err := store.Pool().Query(ctx, `SELECT event_type FROM test_audit_events
		WHERE organization_id = $1 AND actor_id = $2 AND event_type LIKE 'agent%abled' ORDER BY occurred_at, event_type`, orgID, userID)
	if err != nil {
		t.Fatalf("query audit events: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var eventType string
		if err := rows.Scan(&eventType); err != nil {
			t.Fatalf("scan audit event: %v", err)
		}
		events = append(events, eventType)
	}
	want := []string{"agent.disabled", "agent.disabled", "agent.enabled", "agent_instance.disabled", "agent_instance.enabled"}
	if len(events) != len(want) {
		t.Fatalf("audit events = %v, want %v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("audit events = %v, want %v", events, want)
		}
	}
	t.Run("provisioner pages search and status stay scoped", func(t *testing.T) {
		tokenAgent := createAgent("token pagination")
		for i := 0; i < 30; i++ {
			id := domain.ID(uuid.NewString())
			expires := now.Add(time.Hour)
			if i >= 20 {
				expires = now
			}
			if _, err := store.CreateAgentProvisioner(ctx, application.CreateAgentProvisionerInput{
				OrganizationID: orgID, AgentID: tokenAgent.ID, ActorID: membershipID,
				Provisioner: application.AgentProvisionerRecord{ID: id, SecretHash: provisionerSecret[:], IssuedAt: now.Add(-time.Hour), ExpiresAt: expires}, Now: now,
			}); err != nil {
				t.Fatal(err)
			}
			if i >= 25 {
				if _, err := store.Pool().Exec(ctx, `UPDATE agent_provisioners SET revoked_at = $2 WHERE id = $1`, id, now); err != nil {
					t.Fatal(err)
				}
			}
		}
		input := application.GetAgentDetailInput{OrganizationID: orgID, AgentID: tokenAgent.ID, Now: now, ProvisionerLimit: 25}
		first, err := store.GetAgentDetail(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if len(first.Provisioners) != 25 || first.ProvisionerCount != 30 || first.MatchingProvisionerCount != 30 {
			t.Fatalf("first page: %#v", first)
		}
		input.ProvisionerOffset = 25
		second, err := store.GetAgentDetail(ctx, input)
		if err != nil || len(second.Provisioners) != 5 {
			t.Fatalf("second page: %#v, %v", second, err)
		}
		seen := map[domain.ID]bool{}
		for _, token := range append(first.Provisioners, second.Provisioners...) {
			if seen[token.ID] || token.AgentID != tokenAgent.ID {
				t.Fatalf("overlapping or foreign token: %s", token.ID)
			}
			seen[token.ID] = true
		}
		input.ProvisionerOffset = 0
		for status, count := range map[string]int{"active": 20, "expired": 5, "revoked": 5} {
			input.ProvisionerStatus = status
			page, err := store.GetAgentDetail(ctx, input)
			if err != nil || len(page.Provisioners) != count || page.MatchingProvisionerCount != count || page.ProvisionerCount != 30 {
				t.Fatalf("%s filter: %#v, %v", status, page, err)
			}
		}
		input.ProvisionerStatus = ""
		input.ProvisionerQuery = strings.ToUpper(string(first.Provisioners[0].ID))
		matched, err := store.GetAgentDetail(ctx, input)
		if err != nil || len(matched.Provisioners) != 1 || matched.Provisioners[0].ID != first.Provisioners[0].ID {
			t.Fatalf("identifier search: %#v, %v", matched, err)
		}
		input.ProvisionerQuery = "WHP_…" + strings.ToUpper(string(first.Provisioners[0].ID)[32:])
		matched, err = store.GetAgentDetail(ctx, input)
		if err != nil || len(matched.Provisioners) == 0 {
			t.Fatalf("displayed identifier search: %#v, %v", matched, err)
		}
		input.ProvisionerQuery = "%"
		empty, err := store.GetAgentDetail(ctx, input)
		if err != nil || len(empty.Provisioners) != 0 || empty.MatchingProvisionerCount != 0 || empty.ProvisionerCount != 30 {
			t.Fatalf("literal search: %#v, %v", empty, err)
		}
		input.ProvisionerQuery = ""
		input.ProvisionerOffset = 100
		empty, err = store.GetAgentDetail(ctx, input)
		if err != nil || len(empty.Provisioners) != 0 || empty.MatchingProvisionerCount != 30 {
			t.Fatalf("out of range page: %#v, %v", empty, err)
		}
		input.ProvisionerStatus = "disabled"
		if _, err := store.GetAgentDetail(ctx, input); err == nil {
			t.Fatal("invalid token status accepted")
		}
		input.ProvisionerStatus = ""
		input.ProvisionerOffset = -1
		if _, err := store.GetAgentDetail(ctx, input); err == nil {
			t.Fatal("negative token offset accepted")
		}
		input.ProvisionerOffset = 0
		input.OrganizationID = domain.ID(uuid.NewString())
		if _, err := store.GetAgentDetail(ctx, input); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("cross-organization tokens: %v", err)
		}
	})

}

// Archiving is reversible: the agent keeps its instances and history and
// resolves nothing, its name is freed while its slug stays taken for good.
// Restoring brings the agent back; credentials and provisioner tokens
// revoked at archive stay revoked. Renaming changes the name alone. Agents
// own no pipeline: the entry pipeline is the organization's.
func TestArchiveAgentAgainstRealPostgres(t *testing.T) {
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
	orgID, userID, membershipID := domain.ID(uuid.NewString()), domain.ID(uuid.NewString()), domain.ID(uuid.NewString())
	if _, err := store.ActivateOrganization(ctx, application.ActivateOrganizationInput{
		OrganizationID: orgID, OrganizationSlug: "acme", OrganizationName: "Acme",
		UserID: userID, MembershipID: membershipID, DisplayName: "Ece Kayan",
		Email: "owner@example.com", PasswordHash: "argon2id$test",
		AdmissionSource: domain.AdmissionManual, Now: now,
	}); err != nil {
		t.Fatalf("ActivateOrganization: %v", err)
	}
	createAgent := func(name string, agentSlug domain.AgentSlug) domain.Agent {
		agent, err := store.CreateAgent(ctx, application.CreateAgentInput{Kind: domain.AgentService,
			OrganizationID: orgID, ActorID: membershipID, Name: name, AgentSlug: agentSlug, AgentQuota: application.UnlimitedQuota(), Now: now,
		})
		if err != nil {
			t.Fatalf("CreateAgent %s: %v", name, err)
		}
		return agent
	}
	list := func(status application.AgentListStatus) []domain.AgentSummary {
		summaries, err := store.ListAgentSummaries(ctx, application.ListAgentSummariesInput{OrganizationID: orgID, Scope: domain.ScopeSet{All: true}, Status: status, Limit: 50, Now: now})
		if err != nil {
			t.Fatalf("ListAgentSummaries %q: %v", status, err)
		}
		return summaries
	}
	detailOf := func(agentID domain.ID) domain.AgentDetail {
		detail, err := store.GetAgentDetail(ctx, application.GetAgentDetailInput{OrganizationID: orgID, AgentID: agentID, Now: now})
		if err != nil {
			t.Fatalf("GetAgentDetail: %v", err)
		}
		return detail
	}

	first := createAgent("release automation", "release")
	createAgent("crm hygiene", "crm")
	if _, err := store.GetActiveRequestApprovalPipelineRevision(ctx, application.GetActiveRequestApprovalPipelineRevisionInput{OrganizationID: orgID, PipelineKey: "release"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an agent must not seed a pipeline of its own, got %v", err)
	}
	secret := sha256.Sum256([]byte("runner"))
	credentialID := domain.ID(uuid.NewString())
	instance, err := store.CreateAgentInstanceWithCredential(ctx, application.CreateAgentInstanceWithCredentialInput{ActorType: domain.ActorHuman,
		OrganizationID: orgID, AgentID: first.ID, ActorID: userID, Name: "production runner", Metadata: json.RawMessage(`{}`),
		Credential: application.AgentCredentialRecord{ID: credentialID, SecretHash: secret[:], IssuedAt: now, ExpiresAt: now.Add(24 * time.Hour)},
		Now:        now,
	})
	if err != nil {
		t.Fatalf("CreateAgentInstanceWithCredential: %v", err)
	}
	provisionerSecret := sha256.Sum256([]byte("provisioner"))
	if _, err := store.CreateAgentProvisioner(ctx, application.CreateAgentProvisionerInput{
		OrganizationID: orgID, AgentID: first.ID, ActorID: membershipID,
		Provisioner: application.AgentProvisionerRecord{ID: domain.ID(uuid.NewString()), SecretHash: provisionerSecret[:], IssuedAt: now, ExpiresAt: now.Add(24 * time.Hour)},
		Now:         now,
	}); err != nil {
		t.Fatalf("CreateAgentProvisioner: %v", err)
	}

	// Archiving the agent leaves the organization's pipelines alone.
	archivedAt := now.Add(time.Minute)
	archived, err := store.ArchiveAgent(ctx, application.ArchiveAgentInput{OrganizationID: orgID, AgentID: first.ID, ActorID: membershipID, Now: archivedAt})
	if err != nil || archived.Status != domain.AgentArchived || archived.ArchivedAt == nil || !archived.ArchivedAt.Equal(archivedAt) || archived.AgentSlug != "release" {
		t.Fatalf("ArchiveAgent = %#v, %v", archived, err)
	}
	if entry, err := store.GetActiveRequestApprovalPipelineRevision(ctx, application.GetActiveRequestApprovalPipelineRevisionInput{OrganizationID: orgID, PipelineKey: domain.EntryPipelineKey}); err != nil || !entry.IsActive {
		t.Fatalf("archiving an agent must leave the entry pipeline active: %#v, %v", entry, err)
	}

	// Nothing of the agent resolves while it is archived. The credential
	// was revoked with the agent, so it is unauthenticated rather than
	// inactive, and its holder is told the agent was archived so the hook
	// on their machine can say what to run. A wrong secret learns nothing.
	var refused domain.CredentialRefusedError
	if _, err := store.ResolveAgentCredential(ctx, application.ResolveAgentCredentialInput{CredentialID: credentialID, SecretHash: secret[:], Now: now.Add(2 * time.Minute)}); !errors.Is(err, domain.ErrUnauthenticated) || !errors.As(err, &refused) || refused.Reason != domain.CredentialRefusedAgentArchived || refused.AgentSlug != "release" {
		t.Fatalf("an archived agent's credential must be refused as archived, got %v", err)
	}
	wrongSecret := sha256.Sum256([]byte("not the secret"))
	if _, err := store.ResolveAgentCredential(ctx, application.ResolveAgentCredentialInput{CredentialID: credentialID, SecretHash: wrongSecret[:], Now: now.Add(2 * time.Minute)}); !errors.Is(err, domain.ErrUnauthenticated) || errors.As(err, &refused) {
		t.Fatalf("a wrong secret must stay plainly unauthenticated, got %v", err)
	}
	if _, err := store.SetAgentStatus(ctx, application.SetAgentStatusInput{OrganizationID: orgID, AgentID: first.ID, Status: domain.AgentActive, ActorID: membershipID, Now: now}); !errors.Is(err, domain.ErrAgentArchived) {
		t.Fatalf("the status switch must refuse an archived agent, got %v", err)
	}
	if _, err := store.SetAgentInstanceStatus(ctx, application.SetAgentInstanceStatusInput{OrganizationID: orgID, InstanceID: instance.ID, Status: domain.AgentDisabled, ActorID: membershipID, Now: now}); !errors.Is(err, domain.ErrAgentArchived) {
		t.Fatalf("an archived agent's instances must be frozen, got %v", err)
	}
	lateSecret := sha256.Sum256([]byte("late runner"))
	if _, err := store.CreateAgentInstanceWithCredential(ctx, application.CreateAgentInstanceWithCredentialInput{ActorType: domain.ActorHuman,
		OrganizationID: orgID, AgentID: first.ID, ActorID: userID, Name: "late runner", Metadata: json.RawMessage(`{}`),
		Credential: application.AgentCredentialRecord{ID: domain.ID(uuid.NewString()), SecretHash: lateSecret[:], IssuedAt: now, ExpiresAt: now.Add(time.Hour)},
		Now:        now,
	}); !errors.Is(err, domain.ErrAgentInactive) {
		t.Fatalf("nothing can be enrolled under an archived agent, got %v", err)
	}
	if _, err := store.ArchiveAgent(ctx, application.ArchiveAgentInput{OrganizationID: orgID, AgentID: first.ID, ActorID: membershipID, Now: now}); !errors.Is(err, domain.ErrAgentArchived) {
		t.Fatalf("archiving twice must be refused, got %v", err)
	}
	if _, err := store.ArchiveAgent(ctx, application.ArchiveAgentInput{OrganizationID: orgID, AgentID: domain.ID(uuid.NewString()), ActorID: membershipID, Now: now}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("archiving an unknown agent must be not found, got %v", err)
	}
	detail := detailOf(first.ID)
	if detail.Status != domain.AgentArchived || len(detail.Instances) != 1 || len(detail.Instances[0].Credentials) != 1 {
		t.Fatalf("an archived agent keeps its instances and credentials for the record: %#v", detail)
	}
	if revokedAt := detail.Instances[0].Credentials[0].RevokedAt; revokedAt == nil || !revokedAt.Equal(archivedAt) {
		t.Fatalf("archiving must revoke every live credential, got %v", revokedAt)
	}
	if len(detail.Provisioners) != 1 || detail.Provisioners[0].RevokedAt == nil || !detail.Provisioners[0].RevokedAt.Equal(archivedAt) {
		t.Fatalf("archiving must revoke every live provisioner token, got %#v", detail.Provisioners)
	}

	// The name is freed while the slug stays taken: the archived agent is
	// still reachable by its slug, a new agent may take its name under
	// another slug, and no agent may ever take the slug again, so the
	// archived slug can never change hands.
	if found, err := store.GetAgentBySlug(ctx, application.GetAgentBySlugInput{OrganizationID: orgID, AgentSlug: "release"}); err != nil || found.ID != first.ID || found.Status != domain.AgentArchived {
		t.Fatalf("an archived agent must stay reachable by its slug, got %#v, %v", found, err)
	}
	reused := createAgent(first.Name, "release-two")
	if _, err := store.CreateAgent(ctx, application.CreateAgentInput{Kind: domain.AgentService,
		OrganizationID: orgID, ActorID: membershipID, Name: "release again", AgentSlug: "release", AgentQuota: application.UnlimitedQuota(), Now: now,
	}); !errors.Is(err, domain.ErrAgentSlugTaken) || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("an archived agent's slug must never be reused, got %v", err)
	}
	if live := list(""); len(live) != 2 || slices.ContainsFunc(live, func(summary domain.AgentSummary) bool { return summary.ID == first.ID }) {
		t.Fatalf("the default list must leave archived agents out: %#v", live)
	}
	if only := list(application.AgentListStatusArchived); len(only) != 1 || only[0].ID != first.ID {
		t.Fatalf("the archived list must hold the archived agent alone: %#v", only)
	}
	if all := list(application.AgentListStatusAll); len(all) != 3 || all[2].ID != first.ID || all[2].ArchivedAt == nil {
		t.Fatalf("archived agents must sort last of all: %#v", all)
	}
	assertSQLRejected(t, ctx, store, `UPDATE agents SET status = 'archived' WHERE id = $1`, "agents_archived_status_check", reused.ID)

	// Restore takes an archived agent whose name is free among the live
	// ones; the live agent that took the name blocks it until renamed.
	if _, err := store.RestoreAgent(ctx, application.RestoreAgentInput{OrganizationID: orgID, AgentID: reused.ID, ActorID: membershipID, Now: now}); !errors.Is(err, domain.ErrAgentNotArchived) {
		t.Fatalf("restoring a live agent must be refused, got %v", err)
	}
	if _, err := store.RestoreAgent(ctx, application.RestoreAgentInput{OrganizationID: orgID, AgentID: first.ID, ActorID: membershipID, Now: now}); !errors.Is(err, domain.ErrAgentNameTaken) || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("restoring while a live agent holds the name must be refused, got %v", err)
	}
	if _, err := store.RestoreAgent(ctx, application.RestoreAgentInput{OrganizationID: orgID, AgentID: domain.ID(uuid.NewString()), ActorID: membershipID, Now: now}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("restoring an unknown agent must be not found, got %v", err)
	}
	if found, err := store.GetAgentBySlug(ctx, application.GetAgentBySlugInput{OrganizationID: orgID, AgentSlug: "release"}); err != nil || found.Status != domain.AgentArchived {
		t.Fatalf("a refused restore must leave the agent archived, got %#v, %v", found, err)
	}

	// Renaming changes the display name alone: the slug never moves, the
	// new name must be free among the live agents, and an archived agent
	// refuses every write but restore.
	renamedAt := now.Add(2 * time.Minute)
	renamed, err := store.RenameAgent(ctx, application.RenameAgentInput{OrganizationID: orgID, AgentID: reused.ID, Name: "release canary", ActorID: membershipID, Now: renamedAt})
	if err != nil || renamed.ID != reused.ID || renamed.Name != "release canary" || renamed.AgentSlug != "release-two" || !renamed.UpdatedAt.Equal(renamedAt) {
		t.Fatalf("RenameAgent = %#v, %v", renamed, err)
	}
	if _, err := store.RenameAgent(ctx, application.RenameAgentInput{OrganizationID: orgID, AgentID: reused.ID, Name: "crm hygiene", ActorID: membershipID, Now: renamedAt}); !errors.Is(err, domain.ErrAgentNameTaken) || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("renaming onto a live agent's name must be refused, got %v", err)
	}
	if _, err := store.RenameAgent(ctx, application.RenameAgentInput{OrganizationID: orgID, AgentID: first.ID, Name: "release archived", ActorID: membershipID, Now: renamedAt}); !errors.Is(err, domain.ErrAgentArchived) {
		t.Fatalf("renaming an archived agent must be refused, got %v", err)
	}
	if found, err := store.GetAgentBySlug(ctx, application.GetAgentBySlugInput{OrganizationID: orgID, AgentSlug: "release-two"}); err != nil || found.Name != "release canary" || found.ID != reused.ID {
		t.Fatalf("the renamed agent must keep its slug, got %#v, %v", found, err)
	}
	if _, err := store.RenameAgentInstance(ctx, application.RenameAgentInstanceInput{OrganizationID: orgID, InstanceID: instance.ID, Name: "archived runner", ActorID: membershipID, Now: renamedAt}); !errors.Is(err, domain.ErrAgentArchived) {
		t.Fatalf("renaming an archived agent's instance must be refused, got %v", err)
	}

	// Restoring puts the agent back to active.
	restoredAt := now.Add(3 * time.Minute)
	restored, err := store.RestoreAgent(ctx, application.RestoreAgentInput{OrganizationID: orgID, AgentID: first.ID, ActorID: membershipID, Now: restoredAt})
	if err != nil || restored.ID != first.ID || restored.Status != domain.AgentActive || restored.ArchivedAt != nil || restored.AgentSlug != "release" || restored.Name != first.Name || !restored.UpdatedAt.Equal(restoredAt) {
		t.Fatalf("RestoreAgent = %#v, %v", restored, err)
	}
	if _, err := store.RestoreAgent(ctx, application.RestoreAgentInput{OrganizationID: orgID, AgentID: first.ID, ActorID: membershipID, Now: restoredAt}); !errors.Is(err, domain.ErrAgentNotArchived) {
		t.Fatalf("restoring twice must be refused, got %v", err)
	}
	// Credentials and provisioner tokens revoked at archive stay revoked;
	// the instances stay and new ones enroll the ordinary way.
	if _, err := store.ResolveAgentCredential(ctx, application.ResolveAgentCredentialInput{CredentialID: credentialID, SecretHash: secret[:], Now: restoredAt}); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("a credential revoked at archive must stay revoked after restore, got %v", err)
	}
	detail = detailOf(first.ID)
	if detail.Status != domain.AgentActive || len(detail.Instances) != 1 || len(detail.Instances[0].Credentials) != 1 {
		t.Fatalf("a restored agent keeps its instances: %#v", detail)
	}
	if revokedAt := detail.Instances[0].Credentials[0].RevokedAt; revokedAt == nil || !revokedAt.Equal(archivedAt) {
		t.Fatalf("restore must not revive revoked credentials, got %v", revokedAt)
	}
	if len(detail.Provisioners) != 1 || detail.Provisioners[0].RevokedAt == nil {
		t.Fatalf("restore must not revive revoked provisioner tokens, got %#v", detail.Provisioners)
	}
	if _, err := store.CreateAgentInstanceWithCredential(ctx, application.CreateAgentInstanceWithCredentialInput{ActorType: domain.ActorHuman,
		OrganizationID: orgID, AgentID: first.ID, ActorID: userID, Name: "late runner", Metadata: json.RawMessage(`{}`),
		Credential: application.AgentCredentialRecord{ID: domain.ID(uuid.NewString()), SecretHash: lateSecret[:], IssuedAt: restoredAt, ExpiresAt: restoredAt.Add(time.Hour)},
		Now:        restoredAt,
	}); err != nil {
		t.Fatalf("a restored agent must enroll instances again: %v", err)
	}
	if live := list(""); len(live) != 3 || !slices.ContainsFunc(live, func(summary domain.AgentSummary) bool { return summary.ID == first.ID }) {
		t.Fatalf("the default list must hold the restored agent: %#v", live)
	}
	if only := list(application.AgentListStatusArchived); len(only) != 0 {
		t.Fatalf("the archived list must be empty after restore: %#v", only)
	}

	// The trail: archive and restore each audit the agent, the rename
	// audits the name change with the stable slug.
	type auditRow struct {
		eventType string
		data      struct {
			AgentSlug           string `json:"agent_slug"`
			Name                string `json:"name"`
			PreviousName        string `json:"previous_name"`
			CredentialsRevoked  int    `json:"credentials_revoked"`
			ProvisionersRevoked int    `json:"provisioners_revoked"`
		}
	}
	var trail []auditRow
	rows, err := store.Pool().Query(ctx, `SELECT event_type, data FROM test_audit_events
		WHERE organization_id = $1 AND event_type IN ('agent.archived', 'agent.renamed', 'agent.restored')
		ORDER BY occurred_at, event_type`, orgID)
	if err != nil {
		t.Fatalf("query audit events: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row auditRow
		var data []byte
		if err := rows.Scan(&row.eventType, &data); err != nil {
			t.Fatalf("scan audit event: %v", err)
		}
		if err := json.Unmarshal(data, &row.data); err != nil {
			t.Fatalf("decode audit data: %v", err)
		}
		trail = append(trail, row)
	}
	var events []string
	for _, row := range trail {
		events = append(events, row.eventType)
	}
	if want := []string{"agent.archived", "agent.renamed", "agent.restored"}; !slices.Equal(events, want) {
		t.Fatalf("audit events = %v, want %v", events, want)
	}
	if archivedEvent := trail[0].data; archivedEvent.AgentSlug != "release" || archivedEvent.Name != first.Name || archivedEvent.CredentialsRevoked != 1 || archivedEvent.ProvisionersRevoked != 1 {
		t.Fatalf("agent.archived data = %+v", archivedEvent)
	}
	if renamedEvent := trail[1].data; renamedEvent.AgentSlug != "release-two" || renamedEvent.PreviousName != first.Name || renamedEvent.Name != "release canary" {
		t.Fatalf("agent.renamed data = %+v", renamedEvent)
	}
	if restoredEvent := trail[2].data; restoredEvent.AgentSlug != "release" || restoredEvent.Name != first.Name {
		t.Fatalf("agent.restored data = %+v", restoredEvent)
	}
}
