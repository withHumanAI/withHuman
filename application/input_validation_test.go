package application

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/routing"
)

type validatable interface {
	Validate() error
}

func TestPortInputsAcceptCompleteValues(t *testing.T) {
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	hash := bytes.Repeat([]byte{1}, sha256.Size)
	auth := domain.AuthContext{
		Method:          "local_password",
		Assurance:       "single_factor",
		AuthenticatedAt: now,
		SessionID:       "session-1",
	}
	credential := AgentCredentialRecord{
		ID:         "credential-1",
		SecretHash: hash,
		IssuedAt:   now,
		ExpiresAt:  now.Add(time.Hour),
	}
	enrollment := domain.AgentEnrollment{
		ID:                 "enrollment-1",
		EnrollmentCodeHash: hash,
		UserCode:           "ABC-12",
		Runtime:            "claude-code",
		InstanceName:       "runner-1",
		InstanceMetadata:   []byte(`{"region":"eu-west-1"}`),
		Status:             "pending",
		CreatedAt:          now,
		ExpiresAt:          now.Add(10 * time.Minute),
	}
	request := domain.ApprovalRequest{
		Timeout:         "1h",
		ID:              "request-1",
		OrganizationID:  "organization-1",
		AgentID:         "agent-1",
		AgentInstanceID: "instance-1",
		AgentSlug:       "release",
		Tool:            "deploy",
		Arguments:       []byte(`{"version":"1"}`),
		AgentReasoning:  "Deploying version 1 as requested",
		IdempotencyKey:  "request-key-1",
		SemanticHash:    "semantic-hash-1",
		Status:          domain.ApprovalPending,
		DeadlineAt:      now.Add(time.Minute),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	initializationAudit := domain.AuditEvent{
		ID: "audit-1", OrganizationID: request.OrganizationID, EventType: "approval.created",
		ActorType: domain.ActorAgent, ActorID: request.AgentID, SubjectType: "approval_request",
		SubjectID: request.ID, Data: []byte(`{}`), OccurredAt: now,
	}
	initializationCompleted := domain.AuditEvent{
		ID: "audit-2", OrganizationID: request.OrganizationID, EventType: "request_approval_pipeline.completed",
		ActorType: domain.ActorSystem, SubjectType: "approval_request", SubjectID: request.ID,
		Data: []byte(`{"outcome":"human","reason_code":"no_active_revision"}`), OccurredAt: now,
	}
	principal := domain.Principal{
		ActorType:      domain.ActorHuman,
		ActorID:        "user-1",
		OrganizationID: "organization-1",
		MembershipID:   "membership-1",
		Auth:           auth,
	}
	command := domain.DecisionCommand{
		OperationID:    "operation-1",
		RequestID:      request.ID,
		OrganizationID: request.OrganizationID,
		Principal:      principal,
		Status:         domain.ApprovalApproved,
		Channel:        "web",
		IdempotencyKey: "decision-key-1",
	}
	operation := domain.DecisionOperation{
		ID:             command.OperationID,
		OrganizationID: command.OrganizationID,
		RequestID:      command.RequestID,
		Status:         domain.OperationPending,
		CreatedAt:      now,
	}
	block := RequestApprovalBlockSnapshotInput{
		ID: "block-1", BlockKey: "small-refund", Name: "Small refund", BlockType: "cel",
		ConfigVersion: 1, Config: json.RawMessage(`{"on_match":"approve"}`), TimeoutMS: 50, MaxAttempts: 1,
	}
	expectedRevision := int64(1)

	tests := []struct {
		name  string
		input validatable
	}{
		{"activate organization", ActivateOrganizationInput{OrganizationID: "organization-1", OrganizationSlug: "acme", OrganizationName: "Acme", UserID: "user-1", MembershipID: "membership-1", DisplayName: "Ada", Email: "ada@example.com", AdmissionSource: domain.AdmissionManual, Now: now}},
		{"create session", CreateSessionInput{UserID: "user-1", OrganizationID: "organization-1", SecretHash: hash, IssuedAt: now, ExpiresAt: now.Add(time.Hour), Auth: auth}},
		{"resolve session", ResolveSessionInput{SecretHash: hash, Now: now}},
		{"revoke session", RevokeSessionInput{SessionID: "session-1"}},
		{"get local credential", GetLocalCredentialInput{Email: "ada@example.com"}},
		{"local credential record", LocalCredentialRecord{UserID: "user-1", PasswordHash: "encoded-password-hash"}},
		{"find membership for login", FindMembershipForLoginInput{UserID: "user-1", OrganizationID: "organization-1"}},
		{"list login organizations", ListLoginOrganizationsInput{Email: "ada@example.com"}},
		{"create login ticket", CreateLoginTicketInput{TicketID: "ticket-1", UserID: "user-1", SecretHash: hash, AuthMethod: "local_password", Assurance: "single_factor", IssuedAt: now, ExpiresAt: now.Add(time.Hour)}},
		{"link verified user identity", LinkVerifiedUserIdentityInput{Email: "ada@example.com", ProviderKey: "google", Issuer: "https://accounts.google.com", Subject: "sub-1", Now: now}},
		{"consume login ticket", ConsumeLoginTicketInput{TicketID: "ticket-1", SecretHash: hash, Now: now}},
		{"get user", GetUserInput{UserID: "user-1"}},
		{"get membership", GetMembershipInput{OrganizationID: "organization-1", MembershipID: "membership-1"}},
		{"get organization policy", GetOrganizationPolicyInput{OrganizationID: "organization-1"}},
		{"credential record", credential},
		{"resolve agent credential", ResolveAgentCredentialInput{CredentialID: credential.ID, SecretHash: hash, Now: now}},
		{"create agent", CreateAgentInput{OrganizationID: "organization-1", ActorID: "membership-1", Name: "release-agent", AgentSlug: "release", AgentQuota: UnlimitedQuota(), Now: now}},
		{"get agent", GetAgentInput{OrganizationID: "organization-1", AgentID: "agent-1"}},
		{"list agent summaries", ListAgentSummariesInput{OrganizationID: "organization-1", Scope: domain.ScopeSet{All: true}, Limit: 50, Now: now}},
		{"get agent detail", GetAgentDetailInput{OrganizationID: "organization-1", AgentID: "agent-1", Now: now}},
		{"set agent status", SetAgentStatusInput{OrganizationID: "organization-1", AgentID: "agent-1", Status: domain.AgentDisabled, ActorID: "membership-1", Now: now}},
		{"set agent instance status", SetAgentInstanceStatusInput{OrganizationID: "organization-1", InstanceID: "instance-1", Status: domain.AgentActive, ActorID: "membership-1", Now: now}},
		{"create agent instance", CreateAgentInstanceWithCredentialInput{ActorType: domain.ActorHuman, OrganizationID: "organization-1", AgentID: "agent-1", ActorID: "user-1", Name: "runner-1", Metadata: []byte(`{}`), Credential: credential, Now: now}},
		{"revoke agent credential", RevokeAgentCredentialInput{ActorID: "member-1", Now: now, OrganizationID: "organization-1", CredentialID: credential.ID}},
		{"create agent enrollment", CreateAgentEnrollmentInput{Enrollment: enrollment}},
		{"get agent enrollment", GetAgentEnrollmentInput{OrganizationID: "organization-1", UserCode: enrollment.UserCode, Now: now}},
		{"list enrollment agents", ListEnrollmentAgentsInput{OrganizationID: "organization-1", Scope: domain.ScopeSet{All: true}, Limit: 25}},
		{"authorize agent enrollment", AuthorizeAgentEnrollmentInput{OrganizationID: "organization-1", UserCode: enrollment.UserCode, MembershipID: "membership-1", AgentID: "agent-1", Now: now}},
		{"exchange agent enrollment", ExchangeAgentEnrollmentInput{EnrollmentCodeHash: hash, Credential: credential, Now: now}},
		{"initialize approval", InitializeApprovalInput{Request: request, AuditEvents: []domain.AuditEvent{initializationAudit, initializationCompleted}}},
		{"list approval workflow starts", ListApprovalWorkflowStartsInput{Limit: 100}},
		{"mark approval workflow started", MarkApprovalWorkflowStartedInput{OrganizationID: request.OrganizationID, RequestID: request.ID, PublishedAt: now}},
		{"get approval", GetApprovalInput{OrganizationID: "organization-1", RequestID: request.ID}},
		{"get approval by idempotency key", GetApprovalByIdempotencyKeyInput{OrganizationID: "organization-1", AgentInstanceID: request.AgentInstanceID, IdempotencyKey: request.IdempotencyKey}},
		{"list approvals", ListApprovalsInput{OrganizationID: "organization-1", Limit: 50}},
		{"list approvals scoped", ListApprovalsInput{OrganizationID: "organization-1", Limit: 50, Scope: QueueScopeNotified, NotifiedTo: "membership-1"}},
		{"record approval notifications", RecordApprovalNotificationsInput{OrganizationID: "organization-1", RequestID: request.ID, MembershipIDs: []domain.ID{"membership-1"}, Now: now}},
		{"create decision operation", CreateDecisionOperationInput{Operation: operation, IdempotencyKey: command.IdempotencyKey, IntentHash: "intent-hash-1"}},
		{"get decision operation", GetDecisionOperationInput{OrganizationID: "organization-1", OperationID: operation.ID}},
		{"apply decision", ApplyDecisionInput{Command: command, Now: now}},
		{"expire approval", ExpireApprovalInput{OrganizationID: "organization-1", RequestID: request.ID, Now: now}},
		{"cancel approval", CancelApprovalInput{OrganizationID: "organization-1", RequestID: request.ID, AgentInstanceID: "instance-1", Auth: domain.AuthContext{Method: "bearer", Assurance: "machine", AuthenticatedAt: now, CredentialID: "credential-1"}, Now: now}},
		{"submit cancellation", SubmitCancellationInput{OrganizationID: "organization-1", RequestID: request.ID}},
		{"start approval", StartApprovalInput{Request: request}},
		{"submit decision", SubmitDecisionInput{Command: command}},
		{"send approval prompt", SendApprovalPromptInput{Request: request, Recipients: []NotificationRecipient{{MembershipID: "membership-1", DisplayName: "Ana", Emails: []string{"ana@example.com"}}}}},
		{"check capability", CheckCapabilityInput{OrganizationID: "organization-1", Capability: CapabilityEnterpriseSSO}},
		{"limit", LimitInput{OrganizationID: "organization-1", Dimension: DimensionAgents}},
		{"record usage", RecordUsageInput{OrganizationID: "organization-1", Dimension: DimensionApprovalRequests, Amount: 1, IdempotencyKey: "approval_request:request-1", Now: now}},
		{"count pending approvals", CountPendingApprovalsInput{OrganizationID: "organization-1"}},
		{"create invitation", CreateInvitationInput{OrganizationID: "organization-1", InvitationID: "invitation-1", Email: "person@example.com", Grants: []domain.AssignmentSpec{{RoleID: domain.RoleReviewerID, ScopeKind: domain.ScopeOrganization}}, InvitedByMembershipID: "membership-1", TokenHash: hash, ExpiresAt: now.Add(time.Hour), SeatQuota: UnlimitedQuota(), Now: now}},
		{"list invitations", ListInvitationsInput{OrganizationID: "organization-1", Limit: 50}},
		{"rotate invitation token", RotateInvitationTokenInput{OrganizationID: "organization-1", InvitationID: "invitation-1", TokenHash: hash, ExpiresAt: now.Add(time.Hour), SeatQuota: UnlimitedQuota(), Now: now}},
		{"revoke invitation", RevokeInvitationInput{OrganizationID: "organization-1", InvitationID: "invitation-1", Now: now}},
		{"get invitation by token", GetInvitationByTokenInput{InvitationID: "invitation-1", TokenHash: hash, Now: now}},
		{"find pending invitation by email", FindPendingInvitationByEmailInput{OrganizationID: "organization-1", Email: "person@example.com", Now: now}},
		{"accept invitation", AcceptInvitationInput{InvitationID: "invitation-1", TokenHash: hash, AssertedEmail: "person@example.com", NewUserID: "user-1", DisplayName: "Person", ProviderKey: "local", Issuer: "withhuman:self-hosted", MembershipID: "membership-1", SeatQuota: UnlimitedQuota(), Now: now}},
		{"list members", ListMembersInput{OrganizationID: "organization-1", Limit: 50}},
		{"set membership status", SetMembershipStatusInput{OrganizationID: "organization-1", MembershipID: "membership-1", Status: domain.MembershipSuspended, ActorMembershipID: "membership-2", Now: now}},
		{"create team", CreateTeamInput{OrganizationID: "organization-1", Name: "payments", ActorMembershipID: "membership-1", Now: now}},
		{"list teams", ListTeamsInput{OrganizationID: "organization-1", Scope: domain.ScopeSet{Scopes: []domain.Scope{{Kind: domain.ScopeTeam, ID: "team-1"}}}, Limit: 50}},
		{"get team", GetTeamInput{OrganizationID: "organization-1", TeamID: "team-1"}},
		{"rename team", RenameTeamInput{OrganizationID: "organization-1", TeamID: "team-1", Name: "payments-eu", ActorMembershipID: "membership-1", Now: now}},
		{"delete team", ArchiveTeamInput{OrganizationID: "organization-1", TeamID: "team-1", ActorMembershipID: "membership-1", Now: now}},
		{"set team manual members", SetTeamManualMembersInput{OrganizationID: "organization-1", TeamID: "team-1", MembershipIDs: []domain.ID{"membership-1"}, ActorMembershipID: "membership-2", Now: now}},
		{"create team escalation policy revision", CreateTeamEscalationPolicyRevisionInput{ID: "policy-revision-1", OrganizationID: "organization-1", TeamID: "team-1", Document: []byte(`{"nodes":[]}`), DocumentVersion: 2, CreatedByActorID: "membership-1", Now: now}},
		{"list team escalation policy revisions", ListTeamEscalationPolicyRevisionsInput{OrganizationID: "organization-1", TeamID: "team-1", Limit: 50}},
		{"get team escalation policy revision", GetTeamEscalationPolicyRevisionInput{OrganizationID: "organization-1", TeamID: "team-1", Revision: 1}},
		{"get active team escalation policy revision", GetActiveTeamEscalationPolicyRevisionInput{OrganizationID: "organization-1", TeamID: "team-1"}},
		{"activate team escalation policy revision", ActivateTeamEscalationPolicyRevisionInput{OrganizationID: "organization-1", TeamID: "team-1", Revision: 2, ExpectedActiveRevision: &expectedRevision, ActorID: "membership-1", Now: now}},
		{"deactivate team escalation policy", DeactivateTeamEscalationPolicyInput{OrganizationID: "organization-1", TeamID: "team-1", ExpectedActiveRevision: 1, ActorID: "membership-1", Now: now}},
		{"get active team escalation policies", GetActiveTeamEscalationPoliciesInput{OrganizationID: "organization-1", TeamIDs: []domain.ID{"team-1"}}},
		{"resolve level recipients", ResolveLevelRecipientsInput{OrganizationID: "organization-1", RequestID: request.ID, Urgency: domain.NotificationStandard, Entries: []LevelEntry{{Targets: []routing.Target{{Type: routing.TargetUser, ID: "membership-1"}}}, {TeamID: "team-1", Targets: []routing.Target{{Type: routing.TargetBroadcast}}}}}},
		{"record escalation progress", RecordEscalationProgressInput{OrganizationID: "organization-1", RequestID: request.ID, Progress: EscalationProgress{Sequence: 1, Urgency: routing.UrgencyStandard, Organization: EscalationWalkerProgress{Phase: EscalationPhaseLevel}, Teams: []EscalationWalkerProgress{{TeamID: "team-1", Phase: EscalationPhaseExhausted}}, UpdatedAt: now}, Now: now}},
		{"load escalation program", LoadEscalationProgramInput{OrganizationID: "organization-1", RequestID: request.ID}},
		{"submit claim release", SubmitClaimReleaseInput{OrganizationID: "organization-1", RequestID: request.ID, ClaimID: "claim-1"}},
		{"resolve routing targets", ResolveRoutingTargetsInput{OrganizationID: "organization-1", UserMembershipIDs: []domain.ID{"membership-1"}, TeamIDs: []domain.ID{"team-1"}}},
		{"resolve approval recipients", ResolveApprovalRecipientsInput{OrganizationID: "organization-1", RequestID: request.ID}},
		{"get organization", GetOrganizationInput{OrganizationID: "organization-1"}},
		{"find organization by slug", FindOrganizationBySlugInput{OrganizationSlug: "acme"}},
		{"resolve external identity", ResolveExternalIdentityInput{ProviderKey: "polis", Issuer: "issuer", Subject: "subject", Now: now}},
		{"send transactional", SendTransactionalInput{Message: TransactionalMessage{Kind: TransactionalInvitation, OrganizationID: "organization-1", OrganizationName: "Acme", InviteURL: "http://localhost/invite/x", ExpiresAt: now.Add(time.Hour)}, Recipient: NotificationRecipient{Emails: []string{"person@example.com"}}, Address: "person@example.com"}},
		{"send signup verification", SendTransactionalInput{Message: TransactionalMessage{Kind: TransactionalSignupVerification, OrganizationName: "Acme", VerifyURL: "http://localhost/signup/x", ExpiresAt: now.Add(time.Hour)}, Recipient: NotificationRecipient{Emails: []string{"person@example.com"}}, Address: "person@example.com"}},
		{"upsert directory connection", UpsertDirectoryConnectionInput{OrganizationID: "organization-1", AdapterKey: "polis", ProviderResourceID: "dir-1", State: "active", Now: now}},
		{"admit external member", AdmitExternalMemberInput{OrganizationID: "organization-1", MembershipID: "membership-1", NewUserID: "user-1", Email: "ada@example.com", DisplayName: "Ada", RoleID: domain.RoleReviewerID, AdmissionSource: domain.AdmissionSSOJIT, ProviderKey: "polis", Issuer: "issuer", Subject: "subject", Now: now}},
		{"apply directory commands", ApplyDirectoryCommandsInput{OrganizationID: "organization-1", AdapterKey: "polis", SeatQuota: UnlimitedQuota(), Now: now}},
		{"update directory reconciliation", UpdateDirectoryReconciliationInput{OrganizationID: "organization-1", AdapterKey: "polis", Status: "healthy", Now: now}},
		{"request approval block snapshot", block},
		{"create request approval pipeline revision", CreateRequestApprovalPipelineRevisionInput{ID: "pipeline-1", OrganizationID: "organization-1", AgentSlug: "billing", Blocks: []RequestApprovalBlockSnapshotInput{block}, CreatedByActorID: "membership-1", Now: now}},
		{"list request approval pipeline summaries", ListRequestApprovalPipelineSummariesInput{OrganizationID: "organization-1", Scope: domain.ScopeSet{Scopes: []domain.Scope{{Kind: domain.ScopeAgent, ID: "refund-bot"}}}, Limit: 50}},
		{"get request approval pipeline revision", GetRequestApprovalPipelineRevisionInput{OrganizationID: "organization-1", AgentSlug: "billing", Revision: 1}},
		{"list request approval pipeline revisions", ListRequestApprovalPipelineRevisionsInput{OrganizationID: "organization-1", AgentSlug: "billing", Limit: 50}},
		{"list active request approval pipeline revisions", ListActiveRequestApprovalPipelineRevisionsInput{Limit: 200}},
		{"get active request approval pipeline revision", GetActiveRequestApprovalPipelineRevisionInput{OrganizationID: "organization-1", AgentSlug: "billing"}},
		{"get request approval blocks", GetRequestApprovalBlocksInput{OrganizationID: "organization-1", BlockIDs: []domain.ID{"block-1"}}},
		{"activate request approval pipeline revision", ActivateRequestApprovalPipelineRevisionInput{OrganizationID: "organization-1", AgentSlug: "billing", Revision: 2, ExpectedActiveRevision: expectedRevision, ActorID: "membership-1", Now: now}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.input.Validate(); err != nil {
				t.Fatalf("complete input was rejected: %v", err)
			}
		})
	}
}

func TestPortInputsRejectZeroValues(t *testing.T) {
	now := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		input validatable
	}{
		{"activate organization", ActivateOrganizationInput{}},
		{"create session", CreateSessionInput{}},
		{"resolve session", ResolveSessionInput{}},
		{"revoke session", RevokeSessionInput{}},
		{"get local credential", GetLocalCredentialInput{}},
		{"local credential record", LocalCredentialRecord{}},
		{"find membership for login", FindMembershipForLoginInput{}},
		{"get user", GetUserInput{}},
		{"get membership", GetMembershipInput{}},
		{"get organization policy", GetOrganizationPolicyInput{}},
		{"credential record", AgentCredentialRecord{}},
		{"resolve agent credential", ResolveAgentCredentialInput{}},
		{"create agent", CreateAgentInput{}},
		{"get agent", GetAgentInput{}},
		{"list agent summaries", ListAgentSummariesInput{}},
		{"get agent detail", GetAgentDetailInput{}},
		{"set agent status", SetAgentStatusInput{OrganizationID: "organization-1", AgentID: "agent-1", Status: "paused", ActorID: "membership-1", Now: now}},
		{"set agent instance status", SetAgentInstanceStatusInput{}},
		{"create agent instance", CreateAgentInstanceWithCredentialInput{}},
		{"revoke agent credential", RevokeAgentCredentialInput{}},
		{"create agent enrollment", CreateAgentEnrollmentInput{}},
		{"get agent enrollment", GetAgentEnrollmentInput{}},
		{"list enrollment agents", ListEnrollmentAgentsInput{}},
		{"authorize agent enrollment", AuthorizeAgentEnrollmentInput{}},
		{"exchange agent enrollment", ExchangeAgentEnrollmentInput{}},
		{"initialize approval", InitializeApprovalInput{}},
		{"mark approval workflow started", MarkApprovalWorkflowStartedInput{}},
		{"get approval", GetApprovalInput{}},
		{"get approval by idempotency key", GetApprovalByIdempotencyKeyInput{}},
		{"list approvals", ListApprovalsInput{}},
		{"list approvals unknown scope", ListApprovalsInput{OrganizationID: "organization-1", Limit: 50, Scope: "mine"}},
		{"record approval notifications", RecordApprovalNotificationsInput{}},
		{"record approval notifications blank member", RecordApprovalNotificationsInput{OrganizationID: "organization-1", RequestID: "request-1", MembershipIDs: []domain.ID{""}, Now: time.Unix(1, 0)}},
		{"create decision operation", CreateDecisionOperationInput{}},
		{"get decision operation", GetDecisionOperationInput{}},
		{"apply decision", ApplyDecisionInput{}},
		{"expire approval", ExpireApprovalInput{}},
		{"cancel approval", CancelApprovalInput{}},
		{"cancel approval without the instance", CancelApprovalInput{OrganizationID: "organization-1", RequestID: "request-1", Auth: domain.AuthContext{Method: "bearer"}, Now: time.Unix(1, 0)}},
		{"submit cancellation", SubmitCancellationInput{}},
		{"start approval", StartApprovalInput{}},
		{"submit decision", SubmitDecisionInput{}},
		{"send approval prompt", SendApprovalPromptInput{}},
		{"check capability", CheckCapabilityInput{}},
		{"limit", LimitInput{}},
		{"record usage", RecordUsageInput{}},
		{"count pending approvals", CountPendingApprovalsInput{}},
		{"create invitation", CreateInvitationInput{}},
		{"list invitations", ListInvitationsInput{}},
		{"rotate invitation token", RotateInvitationTokenInput{}},
		{"revoke invitation", RevokeInvitationInput{}},
		{"get invitation by token", GetInvitationByTokenInput{}},
		{"find pending invitation by email", FindPendingInvitationByEmailInput{}},
		{"accept invitation", AcceptInvitationInput{}},
		{"list members", ListMembersInput{}},
		{"set membership status", SetMembershipStatusInput{}},
		{"create team", CreateTeamInput{}},
		{"list teams", ListTeamsInput{}},
		{"get team", GetTeamInput{}},
		{"rename team", RenameTeamInput{}},
		{"delete team", ArchiveTeamInput{}},
		{"set team manual members", SetTeamManualMembersInput{}},
		{"create team escalation policy revision", CreateTeamEscalationPolicyRevisionInput{}},
		{"list team escalation policy revisions", ListTeamEscalationPolicyRevisionsInput{}},
		{"get team escalation policy revision", GetTeamEscalationPolicyRevisionInput{}},
		{"get active team escalation policy revision", GetActiveTeamEscalationPolicyRevisionInput{}},
		{"activate team escalation policy revision", ActivateTeamEscalationPolicyRevisionInput{}},
		{"deactivate team escalation policy", DeactivateTeamEscalationPolicyInput{}},
		{"get active team escalation policies", GetActiveTeamEscalationPoliciesInput{}},
		{"resolve level recipients", ResolveLevelRecipientsInput{}},
		{"record escalation progress", RecordEscalationProgressInput{}},
		{"load escalation program", LoadEscalationProgramInput{}},
		{"submit claim release", SubmitClaimReleaseInput{}},
		{"resolve routing targets", ResolveRoutingTargetsInput{}},
		{"resolve approval recipients", ResolveApprovalRecipientsInput{}},
		{"get organization", GetOrganizationInput{}},
		{"find organization by slug", FindOrganizationBySlugInput{}},
		{"resolve external identity", ResolveExternalIdentityInput{}},
		{"send transactional", SendTransactionalInput{}},
		{"upsert directory connection", UpsertDirectoryConnectionInput{}},
		{"admit external member", AdmitExternalMemberInput{}},
		{"apply directory commands", ApplyDirectoryCommandsInput{}},
		{"update directory reconciliation", UpdateDirectoryReconciliationInput{}},
		{"request approval block snapshot", RequestApprovalBlockSnapshotInput{}},
		{"create request approval pipeline revision", CreateRequestApprovalPipelineRevisionInput{}},
		{"list request approval pipeline summaries", ListRequestApprovalPipelineSummariesInput{}},
		{"get request approval pipeline revision", GetRequestApprovalPipelineRevisionInput{}},
		{"list request approval pipeline revisions", ListRequestApprovalPipelineRevisionsInput{}},
		{"list active request approval pipeline revisions", ListActiveRequestApprovalPipelineRevisionsInput{}},
		{"get active request approval pipeline revision", GetActiveRequestApprovalPipelineRevisionInput{}},
		{"get request approval blocks", GetRequestApprovalBlocksInput{}},
		{"activate request approval pipeline revision", ActivateRequestApprovalPipelineRevisionInput{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.input.Validate(); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("zero input returned %v, want domain.ErrInvalid", err)
			}
		})
	}
}
