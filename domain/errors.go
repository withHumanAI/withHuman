package domain

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNotFound             = errors.New("not found")
	ErrUnauthenticated      = errors.New("unauthenticated")
	ErrForbidden            = errors.New("forbidden")
	ErrIdempotencyConflict  = errors.New("idempotency key was already used for a different request")
	ErrConflict             = errors.New("conflict")
	ErrPreconditionFailed   = errors.New("precondition failed")
	ErrPreconditionRequired = errors.New("precondition required")
	ErrInvalid              = errors.New("invalid")
	ErrMembershipInactive   = errors.New("membership is not active")
	ErrAgentInactive        = errors.New("agent or agent instance is not active")
	// ErrAgentOwnerRequired refuses registering an instance of a personal
	// agent without the member it belongs to: only a signed-in person can
	// enroll one, for themselves.
	ErrAgentOwnerRequired = errors.New("sign in to add an instance of a personal agent")
	// ErrPersonalAgent refuses an operation a personal agent has no use
	// for, such as a provisioner token: its instances are enrolled by
	// the people they belong to.
	ErrPersonalAgent = errors.New("not available for personal agents")
	// ErrAgentArchived refuses every write to an archived agent: the
	// status switch, new instances and provisioners, and archiving it
	// again. Archived agents are read only.
	ErrAgentArchived = errors.New("agent is archived")
	// ErrAgentNotArchived refuses restoring an agent that is live.
	ErrAgentNotArchived = errors.New("agent is not archived")
	// ErrAgentSlugTaken refuses a slug another agent of the organization
	// holds, live or archived: slugs are never reused.
	ErrAgentSlugTaken error = conflictError{"agent slug is already taken"}
	// ErrAgentNameTaken refuses a display name a live agent of the
	// organization already carries.
	ErrAgentNameTaken error = conflictError{"agent name is already taken"}
	// ErrAgentInstanceNameTaken refuses an instance name another instance
	// of the same agent already carries.
	ErrAgentInstanceNameTaken error = conflictError{"agent instance name is already taken"}
	// ErrRequestApprovalPipelineArchived refuses appending to, activating
	// within, or archiving again a pipeline that was archived. Archived
	// pipelines are read only.
	ErrRequestApprovalPipelineArchived = errors.New("this pipeline is archived")
	// ErrRequestApprovalPipelineExists refuses creating a pipeline whose
	// key was ever used, archived keys included.
	ErrRequestApprovalPipelineExists = errors.New("a pipeline with this key already exists")
	// ErrRequestApprovalPipelineEntry refuses what the entry pipeline
	// never allows: creating it again or archiving it.
	ErrRequestApprovalPipelineEntry = errors.New("the entry pipeline cannot be created or archived")
	// ErrRequestApprovalPipelineInUse refuses archiving a pipeline a
	// delegate block in an active pipeline revision still names.
	ErrRequestApprovalPipelineInUse = errors.New("another active pipeline delegates to this pipeline")
	// ErrRequestApprovalPipelineNotFound rejects a delegate block that
	// names a pipeline key with no live revision in the organization.
	ErrRequestApprovalPipelineNotFound = errors.New("pipeline not found")
	// ErrRequestApprovalPipelineInactive refuses activating a pipeline
	// revision that delegates to a pipeline with no active revision.
	ErrRequestApprovalPipelineInactive = errors.New("delegated pipeline has no active revision")
	// ErrPipelineDelegationCycle refuses an activation that would let a
	// request reach a pipeline it already passed through.
	ErrPipelineDelegationCycle = errors.New("delegate blocks would form a loop")
	// ErrPipelineDelegationTooDeep refuses an activation whose delegation
	// chain exceeds MaximumDelegationDepth.
	ErrPipelineDelegationTooDeep = errors.New("pipelines delegate too many levels deep")
	// ErrEffectivePipelineTooLarge refuses an activation whose flattened
	// effective pipeline exceeds MaximumEffectivePipelineBlocks.
	ErrEffectivePipelineTooLarge = errors.New("pipeline has too many blocks")
	ErrAuthenticationStale       = errors.New("sign in again to continue")
	ErrAuthenticationWeak        = errors.New("sign in with multi-factor authentication to continue")
	ErrEmailMismatch             = errors.New("this invitation is for a different email address")
	// ErrUserExists refuses an unauthenticated admission that would attach
	// an existing global account by email alone: the person must sign in
	// to that account and accept from there.
	ErrUserExists      = errors.New("an account with this email already exists. Sign in to accept")
	ErrNotTargeted     = errors.New("this request was sent to other reviewers")
	ErrAlreadyTerminal = errors.New("this request is no longer pending")
	ErrLimitExceeded   = errors.New("limit reached")
	// ErrWebhookUnconfigured means the deployment has no secrets key, so
	// webhook endpoints cannot be stored and webhook blocks cannot compile.
	ErrWebhookUnconfigured = errors.New("webhooks are not configured on this server")
	// ErrUnsupportedDeliveryType rejects an AAP instance delivery whose
	// type this provider does not offer; webhook is the only one.
	ErrUnsupportedDeliveryType = errors.New("delivery type is not supported")
	// ErrWebhookVerificationFailed means a proposed AAP receiver did not
	// echo the verification challenge, so no instance was created.
	ErrWebhookVerificationFailed = errors.New("webhook receiver verification failed")
	// ErrInvalidWebhookURL rejects a malformed AAP receiver URL or one the
	// egress policy forbids.
	ErrInvalidWebhookURL = errors.New("webhook URL is not allowed")
	// ErrInstanceDeleted answers a replayed AAP provisioning whose instance
	// has since been deleted, and a product attempt to re-enable one.
	ErrInstanceDeleted = errors.New("agent instance was deleted")
	// ErrIdempotencyInProgress means another attempt with the same
	// idempotency key is still running; retry after it finishes.
	ErrIdempotencyInProgress = errors.New("an attempt with this idempotency key is in progress")
	// ErrWebhookEndpointInUse refuses archiving an endpoint a webhook block
	// in an active pipeline revision still references.
	ErrWebhookEndpointInUse = errors.New("an active pipeline posts to this webhook endpoint")
	// ErrWebhookEndpointNotFound rejects a block that references an
	// endpoint key with no revision in the organization.
	ErrWebhookEndpointNotFound = errors.New("webhook endpoint not found")
	// ErrWebhookEndpointInactive refuses activating a pipeline revision
	// that posts to an endpoint with no active revision.
	ErrWebhookEndpointInactive = errors.New("webhook endpoint has no active revision")
	// ErrEscalationPathInUse refuses archiving a path an active
	// pipeline revision still names, as a block's path or as its default.
	ErrEscalationPathInUse = errors.New("an active pipeline uses this escalation path")
	// ErrEscalationPathExists refuses creating a path whose key was ever
	// used, archived keys included; post a revision to a live one instead.
	ErrEscalationPathExists = errors.New("an escalation path with this key already exists")
	// ErrEscalationPathArchived refuses appending to, activating, or
	// archiving again a path that was archived. Archived paths are read
	// only.
	ErrEscalationPathArchived = errors.New("this escalation path is archived")
	// ErrEscalationPathInactive refuses activating a pipeline revision that
	// names a path with no active revision.
	ErrEscalationPathInactive = errors.New("escalation path has no active revision")
	// ErrGatewayServerExists refuses registering a gateway server whose
	// slug was ever used, archived servers included; post a revision to a
	// live one instead.
	ErrGatewayServerExists = errors.New("gateway server already exists")
	// ErrGatewayServerArchived refuses revising, activating, connecting,
	// or archiving again a gateway server that was archived. Archived
	// servers are read only.
	ErrGatewayServerArchived = errors.New("gateway server is archived")
	// ErrGatewayDownstreamUnreachable reports that a downstream MCP server
	// could not be dialled or refused to list its tools with the stored
	// credential; the wrapped message says why.
	ErrGatewayDownstreamUnreachable = errors.New("downstream server unreachable")
	// ErrMembershipInEscalationPath refuses suspending or deprovisioning a
	// member an active escalation path revision still targets, unless the
	// caller forces it and accepts the dangling target.
	ErrMembershipInEscalationPath = errors.New("this member is still in an active escalation path")
	// ErrTeamMemberInEscalationPolicy refuses removing a member from a team
	// while the team's active escalation policy still names them, unless
	// the caller forces it and accepts that the level skips them.
	ErrTeamMemberInEscalationPolicy = errors.New("the team's escalation policy still includes this member")
	// ErrOnboardingState refuses an onboarding write the session's status
	// does not allow: the other side has not taken its turn yet, or the
	// session already moved on.
	ErrOnboardingState = errors.New("setup is not ready for this step")
	// ErrOnboardingExpired refuses every write to a session past its
	// expiry; the person starts a new one from the page.
	ErrOnboardingExpired = errors.New("this setup has expired. Start again")
	// ErrRoleBuiltIn refuses editing or deleting a built-in role row;
	// those are reconciled from code.
	// ErrPlanRequired refuses a capability the organization's plan does
	// not include; the wrapping message names the capability and plan.
	ErrPlanRequired = errors.New("not included in your plan")
	ErrRoleBuiltIn  = errors.New("built-in roles cannot be changed")
	// ErrAPIKeysDisabled refuses minting or resolving a personal API key
	// while the organization's policy switch is off. Existing keys are
	// kept and come back when the switch is turned on.
	ErrAPIKeysDisabled = errors.New("personal API keys are disabled for this organization")
	// ErrAPIKeyScopingUnavailable refuses a permission list on a key in
	// the open edition, whose keys inherit the member's role unchanged.
	ErrAPIKeyScopingUnavailable = errors.New("this edition cannot scope API keys")
	// ErrCustomPermissionsUnavailable refuses a permission policy write
	// (a member's or team's own policies, or an invitation carrying them)
	// on an edition that offers predefined roles alone.
	ErrCustomPermissionsUnavailable = errors.New("custom permissions are not available in this edition. Choose a role")
	// ErrAPIKeyNotAllowed refuses a browser-only operation (issuing keys,
	// changing a password, browser hand-offs) presented with an API key.
	ErrAPIKeyNotAllowed = errors.New("API keys cannot be used for this")
	// ErrRoleAssigned refuses deleting a role while assignments still
	// reference it.
	ErrRoleAssigned = errors.New("role is still assigned")
)

// EscalationPathInUseError carries the uses that refused an archive.
type EscalationPathInUseError struct {
	PathKey string
	Uses    []PipelineUse
}

func (err EscalationPathInUseError) Error() string {
	return ErrEscalationPathInUse.Error()
}

func (err EscalationPathInUseError) Unwrap() error { return ErrEscalationPathInUse }

// EscalationPathsInactiveError names the paths that refused a pipeline
// activation: each exists without an active revision or not at all.
type EscalationPathsInactiveError struct {
	PathKeys []string
}

func (err EscalationPathsInactiveError) Error() string {
	return fmt.Sprintf("%v: %s", ErrEscalationPathInactive, strings.Join(err.PathKeys, ", "))
}

func (err EscalationPathsInactiveError) Unwrap() error { return ErrEscalationPathInactive }

// RequestApprovalPipelineInUseError carries the uses that refused
// archiving a pipeline.
type RequestApprovalPipelineInUseError struct {
	PipelineKey string
	Uses        []PipelineUse
}

func (err RequestApprovalPipelineInUseError) Error() string {
	return ErrRequestApprovalPipelineInUse.Error()
}

func (err RequestApprovalPipelineInUseError) Unwrap() error { return ErrRequestApprovalPipelineInUse }

// PipelinesInactiveError names the delegated pipelines that refused an
// activation: each exists without an active revision or not at all.
type PipelinesInactiveError struct {
	PipelineKeys []string
}

func (err PipelinesInactiveError) Error() string {
	return fmt.Sprintf("%v: %s", ErrRequestApprovalPipelineInactive, strings.Join(err.PipelineKeys, ", "))
}

func (err PipelinesInactiveError) Unwrap() error { return ErrRequestApprovalPipelineInactive }

// PipelineDelegationCycleError names the keys on the cycle an activation
// would close, in delegation order.
type PipelineDelegationCycleError struct {
	PipelineKeys []string
}

func (err PipelineDelegationCycleError) Error() string {
	return fmt.Sprintf("%v: %s", ErrPipelineDelegationCycle, strings.Join(err.PipelineKeys, ", "))
}

func (err PipelineDelegationCycleError) Unwrap() error { return ErrPipelineDelegationCycle }

// PipelineDelegationTooDeepError carries the depth an activation would
// reach.
type PipelineDelegationTooDeepError struct {
	Depth int
}

func (err PipelineDelegationTooDeepError) Error() string {
	return fmt.Sprintf("%v: %d levels, the limit is %d", ErrPipelineDelegationTooDeep, err.Depth, MaximumDelegationDepth)
}

func (err PipelineDelegationTooDeepError) Unwrap() error { return ErrPipelineDelegationTooDeep }

// MembershipInEscalationPathError carries the active path revisions that
// refused a suspension or deprovision, so the caller can name them and
// decide whether to force the change.
type MembershipInEscalationPathError struct {
	MembershipID ID
	Paths        []EscalationPathReference
	// TeamPolicies are the active team escalation policies that name the
	// membership as a user target.
	TeamPolicies []TeamEscalationPolicyReference
}

func (err MembershipInEscalationPathError) Error() string {
	return ErrMembershipInEscalationPath.Error()
}

func (err MembershipInEscalationPathError) Unwrap() error { return ErrMembershipInEscalationPath }

// TeamMemberInEscalationPolicyError carries which members of a team its
// active policy still names, so the caller can decide whether to force
// the removal.
type TeamMemberInEscalationPolicyError struct {
	TeamID        ID
	Revision      int64
	MembershipIDs []ID
}

func (err TeamMemberInEscalationPolicyError) Error() string {
	if len(err.MembershipIDs) > 1 {
		return "the team's escalation policy still includes these members"
	}
	return ErrTeamMemberInEscalationPolicy.Error()
}

func (err TeamMemberInEscalationPolicyError) Unwrap() error { return ErrTeamMemberInEscalationPolicy }

// WebhookEndpointInUseError carries the uses that refused archiving an
// endpoint.
type WebhookEndpointInUseError struct {
	EndpointKey string
	Uses        []PipelineUse
}

func (err WebhookEndpointInUseError) Error() string {
	return ErrWebhookEndpointInUse.Error()
}

func (err WebhookEndpointInUseError) Unwrap() error { return ErrWebhookEndpointInUse }

// WebhookEndpointsInactiveError names the endpoints that refused a
// pipeline activation: each exists without an active revision or not at
// all.
type WebhookEndpointsInactiveError struct {
	EndpointKeys []string
}

func (err WebhookEndpointsInactiveError) Error() string {
	return fmt.Sprintf("%v: %s", ErrWebhookEndpointInactive, strings.Join(err.EndpointKeys, ", "))
}

func (err WebhookEndpointsInactiveError) Unwrap() error { return ErrWebhookEndpointInactive }

// conflictError is a specific conflict that still satisfies
// errors.Is(err, ErrConflict), so generic conflict handling keeps working
// while callers that care can tell the cases apart.
type conflictError struct{ message string }

func (e conflictError) Error() string { return e.message }

func (conflictError) Is(target error) bool { return target == ErrConflict }
