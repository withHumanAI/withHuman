package application

import (
	"context"
	"time"

	"github.com/withHumanAI/withHuman/domain"
)

// IdentityRepository owns canonical users, organizations, memberships, roles,
// and human sessions. Implementations must enforce tenant
// scoping in every query rather than relying on callers to filter results.
type IdentityRepository interface {
	HasOwner(ctx context.Context) (bool, error)
	CreateSession(ctx context.Context, input CreateSessionInput) (domain.Principal, error)
	ResolveSession(ctx context.Context, input ResolveSessionInput) (domain.Principal, error)
	RevokeSession(ctx context.Context, input RevokeSessionInput) error
	GetLocalCredential(ctx context.Context, input GetLocalCredentialInput) (LocalCredentialRecord, error)
	SetLocalCredential(ctx context.Context, input SetLocalCredentialInput) error
	RevokeUserSessions(ctx context.Context, input RevokeUserSessionsInput) error
	CreatePasswordReset(ctx context.Context, input CreatePasswordResetInput) error
	GetPasswordReset(ctx context.Context, input GetPasswordResetInput) (PasswordResetRecord, error)
	ConsumePasswordReset(ctx context.Context, input ConsumePasswordResetInput) (PasswordResetRecord, error)
	FindMembershipForLogin(ctx context.Context, input FindMembershipForLoginInput) (domain.Membership, error)
	ListLoginOrganizations(ctx context.Context, input ListLoginOrganizationsInput) (LoginOrganizations, error)
	CreateLoginTicket(ctx context.Context, input CreateLoginTicketInput) error
	ConsumeLoginTicket(ctx context.Context, input ConsumeLoginTicketInput) (LoginTicketRecord, error)
	// AdmitExternalMember is an edition's unattended admission of an
	// asserted person with a default role (the hosted just-in-time
	// admission under a verified domain).
	AdmitExternalMember(ctx context.Context, input AdmitExternalMemberInput) (domain.Principal, error)
	GetUser(ctx context.Context, input GetUserInput) (domain.User, error)
	GetMembership(ctx context.Context, input GetMembershipInput) (domain.Membership, error)
	GetOrganizationPolicy(ctx context.Context, input GetOrganizationPolicyInput) (domain.OrganizationPolicy, error)
	UpdateOrganizationPolicy(ctx context.Context, input UpdateOrganizationPolicyInput) (domain.OrganizationPolicy, error)
	GetOrganization(ctx context.Context, input GetOrganizationInput) (domain.Organization, error)
	FindOrganizationBySlug(ctx context.Context, input FindOrganizationBySlugInput) (domain.Organization, error)
	ResolveExternalIdentity(ctx context.Context, input ResolveExternalIdentityInput) (domain.User, error)
	LinkVerifiedMemberIdentity(ctx context.Context, input LinkVerifiedMemberIdentityInput) (domain.Membership, error)
	LinkVerifiedUserIdentity(ctx context.Context, input LinkVerifiedUserIdentityInput) (domain.User, error)
	Health(ctx context.Context) error
}

// MembershipRepository owns invitations and the membership management
// surface. Implementations must enforce tenant scoping in every query.
type MembershipRepository interface {
	CreateInvitation(ctx context.Context, input CreateInvitationInput) (domain.Invitation, error)
	ListInvitations(ctx context.Context, input ListInvitationsInput) ([]domain.Invitation, error)
	RotateInvitationToken(ctx context.Context, input RotateInvitationTokenInput) (domain.Invitation, error)
	GetInvitation(ctx context.Context, input GetInvitationInput) (domain.Invitation, error)
	RevokeInvitation(ctx context.Context, input RevokeInvitationInput) error
	GetInvitationByToken(ctx context.Context, input GetInvitationByTokenInput) (InvitationPreview, error)
	FindPendingInvitationByEmail(ctx context.Context, input FindPendingInvitationByEmailInput) (domain.Invitation, error)
	AcceptInvitation(ctx context.Context, input AcceptInvitationInput) (domain.Principal, error)
	ListMembers(ctx context.Context, input ListMembersInput) ([]MemberRecord, error)
	SetMembershipStatus(ctx context.Context, input SetMembershipStatusInput) (domain.Membership, error)
}

// TenantRepository owns how organizations come into and leave existence:
// the activation transaction shared by bootstrap and hosted signup, the
// lifecycle status machine, and the terminal purge. Canonical users,
// organizations, and memberships stay in this database in every edition;
// editions differ only in which callers exist.
type TenantRepository interface {
	ActivateOrganization(ctx context.Context, input ActivateOrganizationInput) (ActivationResult, error)
	SetOrganizationStatus(ctx context.Context, input SetOrganizationStatusInput) (domain.Organization, error)
	PurgeOrganization(ctx context.Context, input PurgeOrganizationInput) (bool, error)
	GetOrganization(ctx context.Context, input GetOrganizationInput) (domain.Organization, error)
}

// TenantOrchestrator owns the durable deletion timer: scheduling starts a
// workflow that waits out the grace period and purges, cancellation stops
// it.
type TenantOrchestrator interface {
	StartOrganizationDeletion(ctx context.Context, input StartOrganizationDeletionInput) error
	CancelOrganizationDeletion(ctx context.Context, input CancelOrganizationDeletionInput) error
}

// AgentRepository owns stable agent identities, their enrolled runtime
// instances, bootstrap enrollments, and instance-bound credentials.
type AgentRepository interface {
	ResolveAgentCredential(ctx context.Context, input ResolveAgentCredentialInput) (domain.Principal, error)
	CreateAgent(ctx context.Context, input CreateAgentInput) (domain.Agent, error)
	GetAgent(ctx context.Context, input GetAgentInput) (domain.Agent, error)
	GetAgentBySlug(ctx context.Context, input GetAgentBySlugInput) (domain.Agent, error)
	ListAgentSummaries(ctx context.Context, input ListAgentSummariesInput) ([]domain.AgentSummary, error)
	GetAgentDetail(ctx context.Context, input GetAgentDetailInput) (domain.AgentDetail, error)
	SetAgentStatus(ctx context.Context, input SetAgentStatusInput) (domain.Agent, error)
	RenameAgent(ctx context.Context, input RenameAgentInput) (domain.Agent, error)
	// ArchiveAgent retires the agent and archives its approval pipeline in
	// the same transaction; RestoreAgent reverses both.
	ArchiveAgent(ctx context.Context, input ArchiveAgentInput) (domain.Agent, error)
	RestoreAgent(ctx context.Context, input RestoreAgentInput) (domain.Agent, error)
	SetAgentInstanceStatus(ctx context.Context, input SetAgentInstanceStatusInput) (domain.AgentInstance, error)
	CreateAgentInstanceWithCredential(ctx context.Context, input CreateAgentInstanceWithCredentialInput) (domain.AgentInstance, error)
	RevokeAgentCredential(ctx context.Context, input RevokeAgentCredentialInput) error
	CreateAgentProvisioner(ctx context.Context, input CreateAgentProvisionerInput) (domain.AgentProvisioner, error)
	MintAgentInstance(ctx context.Context, input MintAgentInstanceInput) (domain.AgentInstance, error)
	RevokeAgentProvisioner(ctx context.Context, input RevokeAgentProvisionerInput) error
	CreateAgentEnrollment(ctx context.Context, input CreateAgentEnrollmentInput) error
	GetAgentEnrollment(ctx context.Context, input GetAgentEnrollmentInput) (domain.AgentEnrollment, error)
	ListEnrollmentAgents(ctx context.Context, input ListEnrollmentAgentsInput) (domain.EnrollmentAgents, error)
	AuthorizeAgentEnrollment(ctx context.Context, input AuthorizeAgentEnrollmentInput) (domain.AgentInstance, error)
	ExchangeAgentEnrollment(ctx context.Context, input ExchangeAgentEnrollmentInput) (domain.AgentEnrollmentIdentity, error)
	// LookupAgentSlug resolves the slug of the agent an instance,
	// credential, or provisioner belongs to, so authorization can check a
	// management permission against the agent before touching the row.
	LookupAgentSlug(ctx context.Context, input LookupAgentSlugInput) (domain.AgentSlug, error)
	Health(ctx context.Context) error
}

// OnboardingRepository owns the guided setup's shared session record
// (design_doc/09-onboarding.md): the single-use setup code, the who_
// bearer, the versioned state both the browser and the CLI advance, and
// the organization's completion mark. Implementations must enforce tenant
// scoping in every query.
type OnboardingRepository interface {
	CreateOnboardingSession(ctx context.Context, input CreateOnboardingSessionInput) (domain.OnboardingSession, error)
	GetOnboardingSession(ctx context.Context, input GetOnboardingSessionInput) (domain.OnboardingSession, error)
	GetOpenOnboardingSession(ctx context.Context, input GetOpenOnboardingSessionInput) (domain.OnboardingSession, error)
	ExchangeOnboardingSetupCode(ctx context.Context, input ExchangeOnboardingSetupCodeInput) (domain.OnboardingSession, error)
	ResolveOnboardingCredential(ctx context.Context, input ResolveOnboardingCredentialInput) (domain.OnboardingSession, error)
	UpdateOnboardingSession(ctx context.Context, input UpdateOnboardingSessionInput) (domain.OnboardingSession, error)
	WaitOnboardingSession(ctx context.Context, input WaitOnboardingSessionInput) (domain.OnboardingSession, error)
	CompleteOnboarding(ctx context.Context, input CompleteOnboardingInput) error
}

// ApprovalRepository is the PostgreSQL product read model and atomic approval
// write target. Writes are idempotent across HTTP retries and Temporal
// activities.
type ApprovalRepository interface {
	InitializeApproval(ctx context.Context, input InitializeApprovalInput) (domain.ApprovalRequest, error)
	GetApproval(ctx context.Context, input GetApprovalInput) (domain.ApprovalRequest, error)
	GetApprovalByIdempotencyKey(ctx context.Context, input GetApprovalByIdempotencyKeyInput) (domain.ApprovalRequest, error)
	ListApprovals(ctx context.Context, input ListApprovalsInput) ([]domain.ApprovalRequest, error)
	CreateDecisionOperation(ctx context.Context, input CreateDecisionOperationInput) (domain.DecisionOperation, error)
	GetDecisionOperation(ctx context.Context, input GetDecisionOperationInput) (domain.DecisionOperation, error)
	ApplyDecision(ctx context.Context, input ApplyDecisionInput) (domain.DecisionOperation, error)
	ExpireApproval(ctx context.Context, input ExpireApprovalInput) (domain.ApprovalRequest, error)
	// CancelApproval withdraws a pending request for the instance that
	// opened it. Only that instance may cancel; any other terminal state
	// is ErrAlreadyTerminal, and a repeated cancel returns the request.
	CancelApproval(ctx context.Context, input CancelApprovalInput) (domain.ApprovalRequest, error)
	// GetPipelineSuspension returns the request's pipeline suspension, open
	// or resumed; ErrNotFound when the pipeline never suspended.
	GetPipelineSuspension(ctx context.Context, input GetPipelineSuspensionInput) (domain.PipelineSuspension, error)
	// ResumeApproval commits a suspended pipeline's asynchronous segment at
	// most once. A later call, or one for a request a human already decided,
	// changes nothing and returns the current request.
	ResumeApproval(ctx context.Context, input ResumeApprovalInput) (domain.ApprovalRequest, error)
	ClaimApproval(ctx context.Context, input ClaimApprovalInput) (domain.Claim, error)
	// ReleaseClaim ends the caller's own live claim and reports its id, so
	// the caller can tell the request's workflow which claim ended.
	ReleaseClaim(ctx context.Context, input ReleaseClaimInput) (domain.ID, error)
	LapseClaim(ctx context.Context, input LapseClaimInput) error
	CountPendingApprovals(ctx context.Context, input CountPendingApprovalsInput) (int64, error)
	AddApprovalReviewer(ctx context.Context, input AddApprovalReviewerInput) (domain.ApprovalReviewer, error)
	// ListReviewerCandidates lists the active members who could be added
	// to the request: they hold request.decide and are not yet reviewers.
	ListReviewerCandidates(ctx context.Context, input ListReviewerCandidatesInput) ([]ReviewerCandidate, error)
	GetApprovalRoutingState(ctx context.Context, input GetApprovalRoutingStateInput) (ApprovalRoutingState, error)
	OperationalMetrics(ctx context.Context) (ApprovalMetrics, error)
	Health(ctx context.Context) error
}

// ApprovalWorkflowOutboxRepository owns the durable handoff from a committed
// pending request to its human-review workflow. Starting a workflow is
// idempotent, so dispatchers may safely read the same unpublished item.
type ApprovalWorkflowOutboxRepository interface {
	ListApprovalWorkflowStarts(ctx context.Context, input ListApprovalWorkflowStartsInput) ([]ApprovalWorkflowStart, error)
	MarkApprovalWorkflowStarted(ctx context.Context, input MarkApprovalWorkflowStartedInput) error
}

// RequestApprovalPipelineRepository owns immutable block snapshots and
// monotonically revisioned pipelines. Every lookup and mutation is scoped by
// organization. Active switches use the expected active revision as their
// optimistic concurrency precondition.
type RequestApprovalPipelineRepository interface {
	CreateRequestApprovalPipelineRevision(context.Context, CreateRequestApprovalPipelineRevisionInput) (domain.RequestApprovalPipelineRevision, error)
	ListRequestApprovalPipelineSummaries(context.Context, ListRequestApprovalPipelineSummariesInput) ([]domain.RequestApprovalPipelineSummary, error)
	GetRequestApprovalPipelineRevision(context.Context, GetRequestApprovalPipelineRevisionInput) (domain.RequestApprovalPipelineRevision, error)
	ListRequestApprovalPipelineRevisions(context.Context, ListRequestApprovalPipelineRevisionsInput) (RevisionPage[domain.RequestApprovalPipelineRevision], error)
	GetActiveRequestApprovalPipelineRevision(context.Context, GetActiveRequestApprovalPipelineRevisionInput) (domain.RequestApprovalPipelineRevision, error)
	// GetRequestApprovalPipelineRevisionByID loads one immutable revision,
	// active or not, so a suspended pipeline resumes on its pinned content.
	GetRequestApprovalPipelineRevisionByID(context.Context, GetRequestApprovalPipelineRevisionByIDInput) (domain.RequestApprovalPipelineRevision, error)
	GetRequestApprovalBlocks(context.Context, GetRequestApprovalBlocksInput) ([]domain.RequestApprovalBlock, error)
	ActivateRequestApprovalPipelineRevision(context.Context, ActivateRequestApprovalPipelineRevisionInput) (domain.RequestApprovalPipelineRevision, error)
}

// RoutingTargetResolver reports which path targets exist: user targets
// must be active memberships of the organization and team targets
// existing teams. Escalation path revisions consult it before a document
// becomes durable or active.
type RoutingTargetResolver interface {
	ResolveRoutingTargets(context.Context, ResolveRoutingTargetsInput) (ResolvedRoutingTargets, error)
}

// EscalationPathResolver reports which escalation path keys exist and
// which are active. Pipeline revisions consult it before a document that
// names a path becomes durable or active.
type EscalationPathResolver interface {
	ResolveEscalationPaths(context.Context, ResolveEscalationPathsInput) (ResolvedEscalationPaths, error)
}

// EscalationPathRepository owns monotonically revisioned escalation path
// documents addressed by path key. Every lookup and mutation is scoped by
// organization. Active switches use the expected active revision as their
// optimistic concurrency precondition. A path comes into being through
// CreateEscalationPath, which writes its first revision, and ends through
// ArchiveEscalationPath, which stamps every revision at once. An archived
// path stays readable (its revisions carry ArchivedAt and the routing
// snapshots that pin them keep working) but is never active, resolves as
// missing, refuses new revisions, and keeps its key: the key cannot be
// created again.
type EscalationPathRepository interface {
	RoutingTargetResolver
	EscalationPathResolver
	// CreateEscalationPath creates the path and its first revision. A key
	// that was ever used refuses with domain.ErrEscalationPathExists.
	CreateEscalationPath(context.Context, CreateEscalationPathInput) (domain.EscalationPathRevision, error)
	// CreateEscalationPathRevision appends the next revision to a live
	// path; an unknown key is domain.ErrNotFound and an archived one
	// domain.ErrEscalationPathArchived.
	CreateEscalationPathRevision(context.Context, CreateEscalationPathRevisionInput) (domain.EscalationPathRevision, error)
	ListEscalationPathSummaries(context.Context, ListEscalationPathSummariesInput) ([]domain.EscalationPathSummary, error)
	GetEscalationPathRevision(context.Context, GetEscalationPathRevisionInput) (domain.EscalationPathRevision, error)
	ListEscalationPathRevisions(context.Context, ListEscalationPathRevisionsInput) (RevisionPage[domain.EscalationPathRevision], error)
	GetActiveEscalationPathRevision(context.Context, GetActiveEscalationPathRevisionInput) (domain.EscalationPathRevision, error)
	ActivateEscalationPathRevision(context.Context, ActivateEscalationPathRevisionInput) (domain.EscalationPathRevision, error)
	// ArchiveEscalationPath archives the path, revisions included, and
	// answers the archived revisions. It is refused with
	// domain.EscalationPathInUseError while any active pipeline revision
	// names the path, and with domain.ErrEscalationPathArchived when the
	// path already is.
	ArchiveEscalationPath(context.Context, ArchiveEscalationPathInput) ([]domain.EscalationPathRevision, error)
	// ListEscalationPathUses discovers the active pipeline revisions that
	// name the path, as a block's path or as the pipeline default. The
	// same set refuses ArchiveEscalationPath.
	ListEscalationPathUses(context.Context, ListEscalationPathUsesInput) ([]domain.PipelineUse, error)
}

// ApprovalMetrics is a deliberately tenant-neutral operational view. It is
// safe to expose to infrastructure monitoring without leaking organization
// names, request payloads, or reviewer identity.
type ApprovalMetrics struct {
	Pending                    int64
	Approved                   int64
	Denied                     int64
	Expired                    int64
	Cancelled                  int64
	AverageDecisionLatencySecs float64
	DatabaseAcquiredConns      int32
	DatabaseIdleConns          int32
	DatabaseMaxConns           int32
}

// LifecycleOrchestrator owns only durable asynchronous approval work. Automatic
// pipeline decisions commit synchronously without entering this port.
type LifecycleOrchestrator interface {
	StartApproval(ctx context.Context, input StartApprovalInput) error
	SubmitDecision(ctx context.Context, input SubmitDecisionInput) error
	SubmitClaim(ctx context.Context, input SubmitClaimInput) error
	// SubmitCancellation stands the request's workflow down after the
	// cancellation committed: notifications stop, the claim timer and the
	// deadline timer are dropped.
	SubmitCancellation(ctx context.Context, input SubmitCancellationInput) error
	// SubmitClaimRelease tells the workflow a claim was released early so
	// escalation resumes now rather than at the claim's retrigger time.
	SubmitClaimRelease(ctx context.Context, input SubmitClaimReleaseInput) error
	Health(ctx context.Context) error
}

// RoutingRecipientResolver expands a request's pinned routing snapshot into
// the memberships its escalation path targets, resolving team membership
// and contact details at call time. Notification delivery consumes this
// before dispatching to channels.
type RoutingRecipientResolver interface {
	ResolveApprovalRecipients(ctx context.Context, input ResolveApprovalRecipientsInput) (ApprovalRecipients, error)
	// ResolveLevelRecipients expands the levels one executor tick entered
	// into people, honouring the team boundary of team policy levels.
	ResolveLevelRecipients(ctx context.Context, input ResolveLevelRecipientsInput) (ApprovalRecipients, error)
}

// ApprovalNotificationRecorder keeps who each request was brought to the
// attention of, which the review queue's default layer lists. Recording
// happens when a plan chooses its people, never when a channel delivers.
type ApprovalNotificationRecorder interface {
	RecordApprovalNotifications(ctx context.Context, input RecordApprovalNotificationsInput) error
}

// Entitlements answers what an organization's plan allows. The open-source
// distribution wires the unlimited adapter; the hosted product supplies a
// billing-backed implementation. Capability and limit checks sit on the
// request path and fail closed on error. Usage recording is metering only
// and must never fail the action it records.
type Entitlements interface {
	CheckCapability(ctx context.Context, input CheckCapabilityInput) (CapabilityDecision, error)
	Limit(ctx context.Context, input LimitInput) (LimitValue, error)
	// Usage sums what was recorded for the dimension inside the window.
	// Consumption quotas read it before spending a unit; a small overrun
	// under concurrency is accepted, since the quota exists to stop a
	// runaway, not to bill exactly.
	Usage(ctx context.Context, input UsageInput) (int64, error)
	RecordUsage(ctx context.Context, input RecordUsageInput) error
}

type CapabilityDecision struct {
	Allowed bool
	// Reason explains a denial in plain language safe to show the caller.
	Reason string
}

type LimitValue struct {
	Unlimited bool
	Value     int64
}

type Clock interface {
	Now() time.Time
}

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }

// WebhookEndpointResolver reports which webhook endpoint keys exist and
// which have an active revision, for pipeline revision creation,
// activation, and preview.
type WebhookEndpointResolver interface {
	ResolveWebhookEndpoints(context.Context, ResolveWebhookEndpointsInput) (ResolvedWebhookEndpoints, error)
}

// WebhookEndpointRepository owns monotonically revisioned webhook
// endpoints, the same shape as escalation paths: append-only numbered
// revisions per endpoint key with at most one active, plus one signing
// secret per key, stored sealed and opened only for delivery. Every
// lookup and mutation is organization scoped.
type WebhookEndpointRepository interface {
	WebhookEndpointResolver
	CreateWebhookEndpointRevision(context.Context, CreateWebhookEndpointRevisionInput) (CreatedWebhookEndpointRevision, error)
	ListWebhookEndpointSummaries(context.Context, ListWebhookEndpointSummariesInput) ([]domain.WebhookEndpointSummary, error)
	GetWebhookEndpointRevision(context.Context, GetWebhookEndpointRevisionInput) (domain.WebhookEndpointRevision, error)
	ListWebhookEndpointRevisions(context.Context, ListWebhookEndpointRevisionsInput) (RevisionPage[domain.WebhookEndpointRevision], error)
	GetActiveWebhookEndpointRevision(context.Context, GetActiveWebhookEndpointRevisionInput) (domain.WebhookEndpointRevision, error)
	ActivateWebhookEndpointRevision(context.Context, ActivateWebhookEndpointRevisionInput) (domain.WebhookEndpointRevision, error)
	DeactivateWebhookEndpoint(context.Context, DeactivateWebhookEndpointInput) (domain.WebhookEndpointRevision, error)
	// ListWebhookEndpointUses discovers the active pipeline revisions whose
	// webhook blocks post to each named key; a key's set is what refuses
	// DeactivateWebhookEndpoint.
	ListWebhookEndpointUses(context.Context, ListWebhookEndpointUsesInput) (map[string][]domain.PipelineUse, error)
	RotateWebhookSigningSecret(context.Context, RotateWebhookSigningSecretInput) (WebhookSigningSecretState, error)
	OpenWebhookSigningSecret(context.Context, OpenWebhookSigningSecretInput) (string, error)
	RecordWebhookEndpointTest(context.Context, RecordWebhookEndpointTestInput) error
}
