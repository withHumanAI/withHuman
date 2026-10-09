package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/db"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/embeddedpg/embeddedpgtest"
	"github.com/withHumanAI/withHuman/routing"
)

// Exercises the agent-owner route against real PostgreSQL: an agent_owner
// snapshot admits the owner alone for deciding, notification, visibility
// and the routing view; suspending the owner hands the request to the
// fallback pinned beside it in the same transaction, with an outbox event
// for the workflow; and a pipeline revision stores its default owner route.
func TestAgentOwnerRoutingAgainstRealPostgres(t *testing.T) {
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
	orgID, ownerUserID, ownerMembershipID := domain.ID(uuid.NewString()), domain.ID(uuid.NewString()), domain.ID(uuid.NewString())
	if _, err := store.ActivateOrganization(ctx, application.ActivateOrganizationInput{
		OrganizationID: orgID, OrganizationSlug: "acme", OrganizationName: "Acme",
		UserID: ownerUserID, MembershipID: ownerMembershipID, DisplayName: "Jordan Lee",
		Email: "owner@example.com", PasswordHash: "argon2id$test",
		AdmissionSource: domain.AdmissionManual, Now: now,
	}); err != nil {
		t.Fatalf("ActivateOrganization: %v", err)
	}
	_, chrisID := apiKeyTestMember(t, ctx, store, orgID, ownerMembershipID, domain.RoleReviewerID, "chris", now)
	_, platformID := apiKeyTestMember(t, ctx, store, orgID, ownerMembershipID, domain.RoleReviewerID, "platform", now)
	_, auditorID := apiKeyTestMember(t, ctx, store, orgID, ownerMembershipID, domain.RoleAuditorID, "auditor", now)

	agent, err := store.CreateAgent(ctx, application.CreateAgentInput{Kind: domain.AgentPersonal,
		OrganizationID: orgID, ActorID: ownerMembershipID, Name: "Claude Code", AgentSlug: "claude-code",
		AgentQuota: application.UnlimitedQuota(), Now: now,
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	secret := sha256.Sum256([]byte("laptop"))
	laptop, err := store.CreateAgentInstanceWithCredential(ctx, application.CreateAgentInstanceWithCredentialInput{ActorType: domain.ActorHuman,
		OrganizationID: orgID, AgentID: agent.ID, ActorID: ownerUserID, MembershipID: chrisID, Name: "laptop",
		Metadata:   json.RawMessage(`{}`),
		Credential: application.AgentCredentialRecord{ID: domain.ID(uuid.NewString()), SecretHash: secret[:], IssuedAt: now, ExpiresAt: now.Add(time.Hour)},
		Now:        now,
	})
	if err != nil {
		t.Fatalf("CreateAgentInstanceWithCredential: %v", err)
	}

	document, err := json.Marshal(routing.PathDocument{WorkingHours: []routing.WorkingHoursSet{}, Nodes: []routing.Node{{
		ID: uuid.NewString(), Type: routing.NodeLevel, EscalateAfter: "15m",
		Targets: []routing.Target{{Type: routing.TargetUser, ID: string(platformID)}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	fallback := createRoutingTestPathRevision(t, ctx, store, orgID, ownerMembershipID, "platform", document, now)
	if _, err := store.ActivateEscalationPathRevision(ctx, application.ActivateEscalationPathRevisionInput{
		OrganizationID: orgID, PathKey: "platform", Revision: fallback.Revision, ActorID: ownerMembershipID, Now: now,
	}); err != nil {
		t.Fatalf("activate the fallback path: %v", err)
	}

	// The owner standing routing reads before sending a request anywhere.
	standing := func(membershipID domain.ID) application.AgentOwnerStanding {
		t.Helper()
		result, err := store.GetAgentOwnerStanding(ctx, application.GetAgentOwnerStandingInput{
			OrganizationID: orgID, MembershipID: membershipID, RequestID: domain.ID(uuid.NewString()),
		})
		if err != nil {
			t.Fatalf("GetAgentOwnerStanding: %v", err)
		}
		return result
	}
	if got := standing(chrisID); !got.Active || !got.CanDecide {
		t.Fatalf("a reviewer owner = %+v", got)
	}
	if got := standing(auditorID); !got.Active || got.CanDecide {
		t.Fatalf("an auditor owner = %+v", got)
	}
	if got := standing(domain.ID(uuid.NewString())); got.Active {
		t.Fatalf("an unknown owner = %+v", got)
	}

	// pinToOwner routes a request to chris, with the platform path pinned
	// as the fallback unless withoutPath.
	pinToOwnerWith := func(key string, withoutPath bool) domain.ApprovalRequest {
		t.Helper()
		request := domain.ApprovalRequest{
			Timeout: "1h", ID: domain.ID(uuid.NewString()), OrganizationID: orgID,
			AgentID: agent.ID, AgentInstanceID: laptop.ID, AgentSlug: agent.AgentSlug, RequesterMembershipID: chrisID,
			Tool: "Bash", Arguments: json.RawMessage(`{"command":"git push"}`),
			IdempotencyKey: key, SemanticHash: "hash-" + key,
			Status: domain.ApprovalPending, DeadlineAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
		}
		initialization := pendingApprovalInitialization(request)
		initialization.Routing = &domain.ApprovalRequestRouting{
			RequestID: request.ID, OrganizationID: orgID,
			Outcome: domain.RoutingAgentOwner, Reason: domain.RoutingReasonPipelineDefault, AgentOwnerMembershipID: chrisID,
			EscalationPathKey: "platform", EscalationPathRevisionID: fallback.ID,
			Urgency: "standard", Facts: map[string]bool{}, CreatedAt: now,
		}
		if withoutPath {
			initialization.Routing.EscalationPathKey, initialization.Routing.EscalationPathRevisionID = "", ""
		}
		stored, err := store.InitializeApproval(ctx, initialization)
		if err != nil {
			t.Fatalf("InitializeApproval: %v", err)
		}
		if stored.RequesterMembershipID != chrisID {
			t.Fatalf("requester = %q", stored.RequesterMembershipID)
		}
		return stored
	}
	pinToOwner := func(key string) domain.ApprovalRequest { return pinToOwnerWith(key, false) }
	routingState := func(membershipID, requestID domain.ID) application.ApprovalRoutingState {
		t.Helper()
		state, err := store.GetApprovalRoutingState(ctx, application.GetApprovalRoutingStateInput{
			OrganizationID: orgID, RequestID: requestID, MembershipID: membershipID,
		})
		if err != nil {
			t.Fatalf("GetApprovalRoutingState: %v", err)
		}
		return state
	}
	recipients := func(requestID domain.ID) application.ApprovalRecipients {
		t.Helper()
		result, err := store.ResolveApprovalRecipients(ctx, application.ResolveApprovalRecipientsInput{OrganizationID: orgID, RequestID: requestID})
		if err != nil {
			t.Fatalf("ResolveApprovalRecipients: %v", err)
		}
		return result
	}
	visibleTo := func(membershipID domain.ID, request domain.ApprovalRequest) bool {
		t.Helper()
		visible, err := store.visibleApprovals(ctx, orgID, membershipID, true, []domain.ApprovalRequest{request})
		if err != nil {
			t.Fatalf("visibleApprovals: %v", err)
		}
		return len(visible) == 1
	}
	view := func(requestID domain.ID) application.ApprovalRoutingView {
		t.Helper()
		result, err := store.GetApprovalRouting(ctx, application.GetApprovalRoutingInput{OrganizationID: orgID, RequestID: requestID})
		if err != nil {
			t.Fatalf("GetApprovalRouting: %v", err)
		}
		return result
	}
	escalated := func(requestID domain.ID) bool {
		t.Helper()
		program, err := store.LoadEscalationProgram(ctx, application.LoadEscalationProgramInput{OrganizationID: orgID, RequestID: requestID})
		if err != nil {
			t.Fatalf("LoadEscalationProgram: %v", err)
		}
		return program.Escalated
	}

	t.Run("the owner alone is routed to", func(t *testing.T) {
		request := pinToOwner("owner-routed")
		if state := routingState(chrisID, request.ID); !state.HasTargets || !state.Targeted {
			t.Fatalf("owner state = %+v", state)
		}
		if state := routingState(platformID, request.ID); !state.HasTargets || state.Targeted {
			t.Fatalf("fallback member state = %+v", state)
		}
		if got := recipients(request.ID); got.Outcome != domain.RoutingAgentOwner || !slices.Equal(got.MembershipIDs(), []domain.ID{chrisID}) {
			t.Fatalf("recipients = %v %v", got.Outcome, got.MembershipIDs())
		}
		if !visibleTo(chrisID, request) || visibleTo(platformID, request) {
			t.Fatal("routed-only visibility must show the request to its owner and nobody on the fallback")
		}
		if escalated(request.ID) {
			t.Fatal("the fallback must not be walked before a handoff")
		}
		routed := view(request.ID)
		if routed.Outcome != domain.RoutingAgentOwner || routed.AgentOwnerMembershipID != chrisID || routed.AgentOwnerName != "chris" ||
			routed.AgentOwnerHandedOffAt != nil || routed.EscalationPathKey != "platform" ||
			len(routed.Targets) != 1 || routed.Targets[0].ID != chrisID {
			t.Fatalf("routing view = %+v", routed)
		}
		if rejected := teamTestDecision(t, ctx, store, orgID, request.ID, platformID, "owner-routed-platform", now); rejected.Status != domain.OperationRejected || rejected.ErrorCode != "not_targeted" {
			t.Fatalf("the fallback's reviewer must not decide before a handoff: %#v", rejected)
		}
		if applied := teamTestDecision(t, ctx, store, orgID, request.ID, chrisID, "owner-routed-chris", now); applied.Status != domain.OperationApplied {
			t.Fatalf("the owner must decide their own agent's request: %#v", applied)
		}
	})

	t.Run("suspending the owner hands the request to the fallback", func(t *testing.T) {
		request := pinToOwner("owner-handoff")
		// An owner route without a fallback path hands off to the queue.
		unpathed := pinToOwnerWith("owner-handoff-unpathed", true)
		if got := recipients(unpathed.ID); got.Outcome != domain.RoutingAgentOwner || !slices.Equal(got.MembershipIDs(), []domain.ID{chrisID}) {
			t.Fatalf("recipients before the handoff = %v %v", got.Outcome, got.MembershipIDs())
		}
		if _, err := store.SetMembershipStatus(ctx, application.SetMembershipStatusInput{
			OrganizationID: orgID, MembershipID: chrisID, Status: domain.MembershipSuspended,
			ActorMembershipID: ownerMembershipID, Now: now,
		}); err != nil {
			t.Fatalf("suspend the owner: %v", err)
		}
		signals, err := store.ListAgentOwnerHandoffSignals(ctx, application.ListAgentOwnerHandoffSignalsInput{Limit: 100})
		if err != nil || !slices.ContainsFunc(signals, func(signal application.AgentOwnerHandoffSignal) bool { return signal.RequestID == request.ID }) {
			t.Fatalf("handoff signals = %+v, %v", signals, err)
		}
		if events := auditEvents(t, ctx, store, `organization_id = $1 AND event_type = 'routing.agent_owner_handoff' AND subject_id = $2`, orgID, request.ID); len(events) != 1 {
			t.Fatalf("handoff audit = %+v", events)
		}
		if got := recipients(request.ID); got.Outcome != domain.RoutingEscalated || !slices.Equal(got.MembershipIDs(), []domain.ID{platformID}) {
			t.Fatalf("recipients after the handoff = %v %v", got.Outcome, got.MembershipIDs())
		}
		if !visibleTo(platformID, request) {
			t.Fatal("the fallback's reviewer must see the handed-off request")
		}
		if !escalated(request.ID) {
			t.Fatal("the fallback must be walked after a handoff")
		}
		if got := recipients(unpathed.ID); got.Outcome != domain.RoutingDefaultQueue || !slices.Contains(got.MembershipIDs(), platformID) ||
			slices.Contains(got.MembershipIDs(), chrisID) {
			t.Fatalf("an unpathed handoff must reach every reviewer who may decide: %v %v", got.Outcome, got.MembershipIDs())
		}
		if escalated(unpathed.ID) {
			t.Fatal("an unpathed handoff has no path to walk")
		}
		if unpathedView := view(unpathed.ID); unpathedView.AgentOwnerHandedOffAt == nil || len(unpathedView.Targets) != 0 {
			t.Fatalf("routing view of an unpathed handoff = %+v", unpathedView)
		}
		handedOff := view(request.ID)
		if handedOff.AgentOwnerHandedOffAt == nil || len(handedOff.Targets) != 1 || handedOff.Targets[0].ID != platformID {
			t.Fatalf("routing view after the handoff = %+v", handedOff)
		}

		// A second deactivation writes nothing new, and the signal marks once.
		if _, err := store.SetMembershipStatus(ctx, application.SetMembershipStatusInput{
			OrganizationID: orgID, MembershipID: chrisID, Status: domain.MembershipDeprovisioned,
			ActorMembershipID: ownerMembershipID, Now: now,
		}); err != nil {
			t.Fatalf("deprovision the owner: %v", err)
		}
		if count := countRows(t, ctx, store, `SELECT count(*) FROM approval_request_agent_owner_handoffs WHERE request_id = $1`, request.ID); count != 1 {
			t.Fatalf("handoff rows = %d", count)
		}
		for range 2 {
			if err := store.MarkAgentOwnerHandoffSignaled(ctx, application.MarkAgentOwnerHandoffSignaledInput{
				OrganizationID: orgID, RequestID: request.ID, SignaledAt: now,
			}); err != nil {
				t.Fatalf("MarkAgentOwnerHandoffSignaled: %v", err)
			}
		}
		signals, err = store.ListAgentOwnerHandoffSignals(ctx, application.ListAgentOwnerHandoffSignalsInput{Limit: 100})
		if err != nil || slices.ContainsFunc(signals, func(signal application.AgentOwnerHandoffSignal) bool { return signal.RequestID == request.ID }) {
			t.Fatalf("a signaled handoff is listed again: %+v, %v", signals, err)
		}
		if applied := teamTestDecision(t, ctx, store, orgID, request.ID, platformID, "owner-handoff-platform", now); applied.Status != domain.OperationApplied {
			t.Fatalf("the fallback's reviewer must decide after the handoff: %#v", applied)
		}
	})

	t.Run("a revision stores its default owner route", func(t *testing.T) {
		created, err := store.CreateRequestApprovalPipelineRevision(ctx, application.CreateRequestApprovalPipelineRevisionInput{
			ID: domain.ID(uuid.NewString()), OrganizationID: orgID, PipelineKey: domain.EntryPipelineKey, Name: "Entry pipeline",
			DefaultEscalationPath: "platform", DefaultAskAgentOwner: true,
			CreatedByActorID: ownerMembershipID, Now: now,
		})
		if err != nil {
			t.Fatalf("CreateRequestApprovalPipelineRevision: %v", err)
		}
		read, err := store.GetRequestApprovalPipelineRevision(ctx, application.GetRequestApprovalPipelineRevisionInput{
			OrganizationID: orgID, PipelineKey: domain.EntryPipelineKey, Revision: created.Revision,
		})
		if err != nil || !read.DefaultAskAgentOwner || read.DefaultEscalationPath != "platform" {
			t.Fatalf("revision = %+v, %v", read, err)
		}
		unpathed, err := store.CreateRequestApprovalPipelineRevision(ctx, application.CreateRequestApprovalPipelineRevisionInput{
			ID: domain.ID(uuid.NewString()), OrganizationID: orgID, PipelineKey: domain.EntryPipelineKey, Name: "Entry pipeline",
			DefaultAskAgentOwner: true, CreatedByActorID: ownerMembershipID, Now: now,
		})
		if err != nil || !unpathed.DefaultAskAgentOwner || unpathed.DefaultEscalationPath != "" {
			t.Fatalf("an owner route without a fallback path is a valid default: %+v, %v", unpathed, err)
		}
	})
}
