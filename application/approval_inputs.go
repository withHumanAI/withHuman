package application

import (
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/withHumanAI/withHuman/domain"
)

// maximumApprovalAuditEvents bounds the audit events one pipeline segment
// (the request path's initialization, or the worker's resume) commits.
// Every block that continues spends a request budget: at most 1,000
// condition evaluations (branch and delegate conditions share it), five
// judge blocks, and five webhook blocks. One more evaluated event ends the
// run (a block that decides or fails, including the budget check that
// refuses the next block, or a branch's closing event), and at most four
// envelope events surround them (approval.created, the pipeline's started
// and completed or suspended events, and the decision or routing).
const maximumApprovalAuditEvents = 1_000 + 5 + 5 + 1 + 4

type InitializeApprovalInput struct {
	Request     domain.ApprovalRequest
	Decision    *domain.Decision
	AuditEvents []domain.AuditEvent
	// HumanReview is what the pipeline resolved for a request that needs
	// a human; the router turns it into Routing. Never persisted itself.
	HumanReview *HumanReviewRoute
	// Routing pins where a pending request was routed; nil for requests
	// the pipeline decided automatically or suspended before deciding.
	Routing *domain.ApprovalRequestRouting
	// Suspension records a pipeline paused before an asynchronous block.
	// Such a request is pending but not yet routed: the worker resumes the
	// pipeline and routes it only if it still needs a human.
	Suspension *domain.PipelineSuspension
}

// ResumeApprovalInput commits the asynchronous segment of a suspended
// pipeline: its evidence, the automatic decision when one was reached, and
// the routing snapshot when the request still needs a human. The store
// applies it at most once per suspension.
type ResumeApprovalInput struct {
	OrganizationID domain.ID
	RequestID      domain.ID
	EvaluationID   domain.ID
	Decision       *domain.Decision
	AuditEvents    []domain.AuditEvent
	// HumanReview is the pipeline's resolved reviewers when the resumed
	// pipeline still needs a human; the router turns it into Routing.
	HumanReview *HumanReviewRoute
	Routing     *domain.ApprovalRequestRouting
	Now         time.Time
}

func (input ResumeApprovalInput) Validate() error {
	if err := firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("approval request ID", input.RequestID),
		requiredID("pipeline evaluation ID", input.EvaluationID),
		requiredTime("resume time", input.Now),
	}); err != nil {
		return err
	}
	if len(input.AuditEvents) == 0 {
		return invalidField("approval resume audit events", "must not be empty")
	}
	if len(input.AuditEvents) > maximumApprovalAuditEvents {
		return invalidField("approval resume audit events", fmt.Sprintf("must contain at most %d events", maximumApprovalAuditEvents))
	}
	for _, event := range input.AuditEvents {
		if event.OrganizationID != input.OrganizationID || event.SubjectType != "approval_request" || event.SubjectID != input.RequestID {
			return invalidField("approval resume audit event", "must belong to the approval request")
		}
		switch event.EventType {
		case "request_approval_block.evaluated", "request_approval_pipeline.completed", "approval.decided", "routing.evaluated":
			if event.ActorType != domain.ActorSystem || event.ActorID != "" {
				return invalidField("approval resume audit actor", "must be the unidentified system actor")
			}
		default:
			return invalidField("approval resume audit event type", "is not allowed")
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(event.Data, &data); err != nil || data == nil {
			return invalidField("approval resume audit event data", "must be a JSON object")
		}
	}
	if input.Decision != nil {
		decision := input.Decision
		if decision.RequestID != input.RequestID || decision.OrganizationID != input.OrganizationID {
			return invalidField("approval resume decision", "must belong to the approval request")
		}
		if decision.Source != domain.DecisionSourcePipeline {
			return invalidField("approval resume decision source", "must be pipeline")
		}
		if decision.Status != domain.ApprovalApproved && decision.Status != domain.ApprovalDenied {
			return invalidField("approval resume decision status", "must be approved or denied")
		}
		if input.Routing != nil {
			return invalidField("approval resume routing", "cannot accompany an automatic decision")
		}
	}
	if input.Routing != nil && (input.Routing.RequestID != input.RequestID || input.Routing.OrganizationID != input.OrganizationID) {
		return invalidField("approval resume routing", "must belong to the approval request")
	}
	return nil
}

type GetPipelineSuspensionInput struct {
	OrganizationID domain.ID
	RequestID      domain.ID
}

func (input GetPipelineSuspensionInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("approval request ID", input.RequestID),
	})
}

type ApprovalWorkflowStart struct {
	EventID        domain.ID
	OrganizationID domain.ID
	RequestID      domain.ID
	CreatedAt      time.Time
}

type ListApprovalWorkflowStartsInput struct {
	Limit int
}

func (input ListApprovalWorkflowStartsInput) Validate() error {
	return nonNegative("limit", int64(input.Limit))
}

type MarkApprovalWorkflowStartedInput struct {
	OrganizationID domain.ID
	RequestID      domain.ID
	PublishedAt    time.Time
}

func (input MarkApprovalWorkflowStartedInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("approval request ID", input.RequestID),
		requiredTime("workflow published time", input.PublishedAt),
	})
}

func (input InitializeApprovalInput) Validate() error {
	if err := validateApprovalRequest(input.Request); err != nil {
		return err
	}
	if err := validateApprovalInitializationDecision(input.Request, input.Decision); err != nil {
		return err
	}
	if len(input.AuditEvents) == 0 {
		return invalidField("approval initialization audit events", "must not be empty")
	}
	if len(input.AuditEvents) > maximumApprovalAuditEvents {
		return invalidField("approval initialization audit events", fmt.Sprintf("must contain at most %d events", maximumApprovalAuditEvents))
	}
	for _, event := range input.AuditEvents {
		if err := validateApprovalInitializationAuditEvent(input.Request, event); err != nil {
			return err
		}
	}
	if err := validateApprovalInitializationRouting(input.Request, input.Routing); err != nil {
		return err
	}
	if err := validateApprovalInitializationSuspension(input); err != nil {
		return err
	}
	return validateApprovalInitializationAuditOrder(input.Decision, input.Suspension != nil, input.AuditEvents)
}

func validateApprovalInitializationSuspension(input InitializeApprovalInput) error {
	suspension := input.Suspension
	if suspension == nil {
		return nil
	}
	if input.Request.Status != domain.ApprovalPending || input.Decision != nil {
		return invalidField("approval suspension", "only an undecided pending request can be suspended")
	}
	if input.Routing != nil {
		return invalidField("approval suspension", "a suspended request is not routed until the pipeline resumes")
	}
	if suspension.RequestID != input.Request.ID || suspension.OrganizationID != input.Request.OrganizationID {
		return invalidField("approval suspension", "must belong to the approval request")
	}
	if err := firstError([]error{
		requiredID("pipeline evaluation ID", suspension.EvaluationID),
		requiredTime("suspension start time", suspension.StartedAt),
		requiredTime("suspension time", suspension.SuspendedAt),
	}); err != nil {
		return err
	}
	if suspension.EntryPipelineID() == "" {
		return invalidField("approval suspension", "must pin the entry pipeline revision")
	}
	for key, id := range suspension.PinnedRevisions {
		if !domain.ValidPipelineKey(key) || id == "" {
			return invalidField("approval suspension", "must pin revisions under valid pipeline keys")
		}
	}
	if suspension.ResumePosition < 0 || suspension.ResumedAt != nil {
		return invalidField("approval suspension", "must be open at a non-negative position")
	}
	for key, value := range suspension.PreviousMetadata {
		if key == "" || !json.Valid(value) {
			return invalidField("approval suspension metadata", "must be valid JSON under non-empty keys")
		}
	}
	return nil
}

func validateApprovalInitializationRouting(request domain.ApprovalRequest, routing *domain.ApprovalRequestRouting) error {
	if routing == nil {
		return nil
	}
	if request.Status != domain.ApprovalPending {
		return invalidField("approval routing", "only pending requests are routed")
	}
	if routing.RequestID != request.ID || routing.OrganizationID != request.OrganizationID {
		return invalidField("approval routing", "must belong to the approval request")
	}
	if routing.Urgency != "standard" && routing.Urgency != "interrupt" {
		return invalidField("approval routing urgency", "must be standard or interrupt")
	}
	switch routing.Outcome {
	case domain.RoutingEscalated, domain.RoutingDefaultQueue:
		escalated := routing.Outcome == domain.RoutingEscalated
		if escalated != (routing.EscalationPathKey != "") || escalated != (routing.EscalationPathRevisionID != "") {
			return invalidField("approval routing", "escalated outcomes carry a path key and revision; fallbacks carry neither")
		}
		if routing.AgentOwnerMembershipID != "" {
			return invalidField("approval routing", "only an agent_owner outcome names the agent's owner")
		}
	case domain.RoutingAgentOwner:
		// The fallback path, when the route had one, is pinned beside the
		// owner: its key always, its revision when the path was active.
		if routing.AgentOwnerMembershipID == "" || routing.AgentOwnerFallback != "" ||
			(routing.EscalationPathRevisionID != "" && routing.EscalationPathKey == "") {
			return invalidField("approval routing", "an agent_owner outcome names the owner, no fallback reason, and a pinned revision only with its path key")
		}
	default:
		return invalidField("approval routing outcome", "must be agent_owner, escalated or default_queue")
	}
	switch routing.AgentOwnerFallback {
	case "", domain.AgentOwnerFallbackNoOwner, domain.AgentOwnerFallbackInactive, domain.AgentOwnerFallbackCannotDecide:
	default:
		return invalidField("approval routing", "agent owner fallback reason is not known")
	}
	if err := validOptionalEscalationPathKey(routing.EscalationPathKey); err != nil {
		return err
	}
	if routing.BlockID != "" && routing.Reason != domain.RoutingReasonBlockEscalation {
		return invalidField("approval routing", "only a block escalation carries a block id")
	}
	return firstError([]error{
		requiredString("approval routing reason", routing.Reason),
		requiredTime("approval routing time", routing.CreatedAt),
	})
}

func validateApprovalInitializationDecision(request domain.ApprovalRequest, decision *domain.Decision) error {
	if decision == nil {
		if request.Status != domain.ApprovalPending {
			return invalidField("approval status", "must be pending without an initialization decision")
		}
		return nil
	}
	if err := firstError([]error{
		requiredID("decision ID", decision.ID),
		requiredID("decision request ID", decision.RequestID),
		requiredID("decision organization ID", decision.OrganizationID),
		requiredString("decision channel", decision.Channel),
		requiredString("decision idempotency key", decision.IdempotencyKey),
		requiredTime("decision time", decision.DecidedAt),
		requiredString("decision authentication method", decision.Auth.Method),
		requiredString("decision authentication assurance", decision.Auth.Assurance),
		requiredTime("decision authentication time", decision.Auth.AuthenticatedAt),
	}); err != nil {
		return err
	}
	if decision.RequestID != request.ID || decision.OrganizationID != request.OrganizationID {
		return invalidField("initialization decision", "must belong to the approval request")
	}
	if decision.Source != domain.DecisionSourcePipeline {
		return invalidField("initialization decision source", "must be pipeline")
	}
	if decision.Status != domain.ApprovalApproved && decision.Status != domain.ApprovalDenied {
		return invalidField("initialization decision status", "must be approved or denied")
	}
	if request.Status != decision.Status {
		return invalidField("approval status", "must match the initialization decision")
	}
	if decision.MembershipID != "" || decision.ReviewerDisplayName != "" || decision.ReviewerEmail != "" {
		return invalidField("initialization decision reviewer", "must be empty for a pipeline decision")
	}
	if decision.Auth.Method != "pipeline" || decision.Auth.Assurance != "automatic" || decision.Auth.SessionID != "" || decision.Auth.CredentialID != "" {
		return invalidField("initialization decision authentication", "must identify automatic pipeline evaluation")
	}
	return nil
}

func validateApprovalInitializationAuditEvent(request domain.ApprovalRequest, event domain.AuditEvent) error {
	if err := firstError([]error{
		requiredID("audit event ID", event.ID),
		requiredID("audit event organization ID", event.OrganizationID),
		requiredString("audit event type", event.EventType),
		requiredString("audit event subject type", event.SubjectType),
		requiredID("audit event subject ID", event.SubjectID),
		validJSON("audit event data", event.Data),
		requiredTime("audit event occurrence time", event.OccurredAt),
	}); err != nil {
		return err
	}
	if event.OrganizationID != request.OrganizationID || event.SubjectType != "approval_request" || event.SubjectID != request.ID {
		return invalidField("approval initialization audit event", "must belong to the approval request")
	}
	if len(event.Data) > 64*1024 {
		return invalidField("approval initialization audit event data", "must not exceed 65536 bytes")
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(event.Data, &data); err != nil || data == nil {
		return invalidField("approval initialization audit event data", "must be a JSON object")
	}
	switch event.EventType {
	case "approval.created":
		if event.ActorType != domain.ActorAgent {
			return invalidField("approval initialization audit actor type", "must be agent for approval.created")
		}
		if event.ActorID != request.AgentID {
			return invalidField("approval initialization audit actor", "must be the requesting agent")
		}
	case "request_approval_pipeline.started", "request_approval_block.evaluated", "request_approval_pipeline.completed", "request_approval_pipeline.suspended", "approval.decided", "routing.evaluated":
		if event.ActorType != domain.ActorSystem || event.ActorID != "" {
			return invalidField("approval initialization audit actor", "must be the unidentified system actor")
		}
	default:
		return invalidField("approval initialization audit event type", "is not allowed")
	}
	return nil
}

func validateApprovalInitializationAuditOrder(decision *domain.Decision, suspended bool, events []domain.AuditEvent) error {
	if events[0].EventType != "approval.created" {
		return invalidField("approval initialization audit events", "must start with approval.created")
	}
	index := 1
	started := false
	if index < len(events) && events[index].EventType == "request_approval_pipeline.started" {
		started = true
		index++
		for index < len(events) && events[index].EventType == "request_approval_block.evaluated" {
			index++
		}
	}
	// A suspended pipeline ends its synchronous segment with the
	// suspension event instead of a completion; the worker writes the
	// completion when it resumes.
	if suspended {
		if !started || index != len(events)-1 || events[index].EventType != "request_approval_pipeline.suspended" {
			return invalidField("approval initialization audit events", "must end with one pipeline suspension after its start evidence")
		}
		return nil
	}
	if index >= len(events) || events[index].EventType != "request_approval_pipeline.completed" {
		return invalidField("approval initialization audit events", "must contain one ordered pipeline completion")
	}
	index++
	if decision != nil {
		if !started {
			return invalidField("approval initialization audit events", "must include pipeline start evidence for a pipeline decision")
		}
		if index >= len(events) || events[index].EventType != "approval.decided" {
			return invalidField("approval initialization audit events", "must end with approval.decided for a pipeline decision")
		}
		index++
	}
	if decision == nil && index < len(events) && events[index].EventType == "routing.evaluated" {
		index++
	}
	if index != len(events) {
		return invalidField("approval initialization audit events", "contain an invalid event order")
	}
	return nil
}

type GetApprovalInput struct {
	OrganizationID domain.ID
	RequestID      domain.ID
	// VisibleTo has the meaning it has on ListApprovalsInput; a request
	// outside the membership's view reads as not found.
	VisibleTo domain.ID
}

func (input GetApprovalInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("approval request ID", input.RequestID),
	})
}

type GetApprovalByIdempotencyKeyInput struct {
	AgentInstanceID domain.ID
	OrganizationID  domain.ID
	IdempotencyKey  string
}

func (input GetApprovalByIdempotencyKeyInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("agent instance ID", input.AgentInstanceID),
		requiredString("idempotency key", input.IdempotencyKey),
	})
}

type ListApprovalsInput struct {
	Offset         int
	Query          string
	Status         domain.ApprovalStatus
	OrganizationID domain.ID
	Limit          int
	// HumanReviewOnly restricts the listing to the review queue: requests
	// that reached human review, whether still pending, decided by a
	// human, or expired while waiting. Requests the pipeline decided
	// automatically are excluded; they stay reachable through the audit
	// log and by direct fetch.
	HumanReviewOnly bool
	// AgentSlug restricts the listing to one agent's requests; empty lists
	// every agent.
	AgentSlug domain.AgentSlug
	// PerAgent, when positive, keeps at most that many of each agent's
	// newest requests, automatic decisions included: the agents' recent
	// history in one listing rather than one per agent.
	PerAgent int
	// VisibleTo, when set, narrows the listing to what that membership was
	// routed to (directly, through a team, or added as a reviewer) plus
	// unrouted requests, for organizations whose request visibility policy
	// is routed_only and for the queue's decidable layer. Empty means
	// every request.
	VisibleTo domain.ID
	// Scope is the queue layer the reader asked for; the service turns it
	// into the narrowing fields below. Empty lists everything the reader
	// may see.
	Scope QueueScope
	// NotifiedTo, when set, keeps only the requests that membership was
	// paged about, or handed by a colleague: the queue's default layer.
	NotifiedTo domain.ID
	// HideUnrouted drops the requests no path scoped when VisibleTo is
	// set, for a reader who may not decide unrouted requests under the
	// break_glass_only policy.
	HideUnrouted bool
}

// QueueScope names one layer of the review queue from the reader's
// standpoint. Each layer contains the one before it.
type QueueScope string

const (
	// QueueScopeNotified is what the reader was told about: the requests a
	// notification plan addressed to them, or a colleague handed them.
	QueueScopeNotified QueueScope = "notified"
	// QueueScopeDecidable is what the reader may decide outright: routed
	// to them, or unrouted where any reviewer may take it.
	QueueScopeDecidable QueueScope = "decidable"
	// QueueScopeVisible is everything the reader may see, whoever decides.
	QueueScopeVisible QueueScope = "visible"
)

func (scope QueueScope) Valid() bool {
	switch scope {
	case "", QueueScopeNotified, QueueScopeDecidable, QueueScopeVisible:
		return true
	}
	return false
}

func (input ListApprovalsInput) Validate() error {
	if input.Status != "" && input.Status != domain.ApprovalPending && !input.Status.Terminal() {
		return invalidField("status", "Choose a valid request status.")
	}
	if !input.Scope.Valid() {
		return invalidField("scope", "Choose notified, decidable, or visible.")
	}
	if input.PerAgent < 0 || input.PerAgent > 50 {
		return invalidField("per_agent", "Choose between 1 and 50 requests per agent.")
	}
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		nonNegative("limit", int64(input.Limit)),
		nonNegative("offset", int64(input.Offset)),
	})
}

// RecordApprovalNotificationsInput remembers that a request was brought to
// these people's attention: the executor entered a level naming them, the
// default queue plan addressed every reviewer, or a colleague handed them
// the request. Recorded when the people are chosen, before any channel
// delivers, so someone with no channel set up still finds the request in
// their queue. A membership already recorded keeps its first time.
type RecordApprovalNotificationsInput struct {
	OrganizationID domain.ID
	RequestID      domain.ID
	MembershipIDs  []domain.ID
	Now            time.Time
}

func (input RecordApprovalNotificationsInput) Validate() error {
	if err := firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("approval request ID", input.RequestID),
		requiredTime("notification time", input.Now),
	}); err != nil {
		return err
	}
	for _, membershipID := range input.MembershipIDs {
		if err := requiredID("membership ID", membershipID); err != nil {
			return err
		}
	}
	return nil
}

type CreateDecisionOperationInput struct {
	Operation      domain.DecisionOperation
	IdempotencyKey string
	IntentHash     string
}

func (input CreateDecisionOperationInput) Validate() error {
	operation := input.Operation
	if err := firstError([]error{
		requiredID("decision operation ID", operation.ID),
		requiredID("organization ID", operation.OrganizationID),
		requiredID("approval request ID", operation.RequestID),
		requiredTime("decision operation creation time", operation.CreatedAt),
		requiredString("idempotency key", input.IdempotencyKey),
		requiredString("decision intent hash", input.IntentHash),
	}); err != nil {
		return err
	}
	if operation.Status != domain.OperationPending {
		return invalidField("decision operation status", "must be pending when created")
	}
	return nil
}

type GetDecisionOperationInput struct {
	OrganizationID domain.ID
	OperationID    domain.ID
}

func (input GetDecisionOperationInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("decision operation ID", input.OperationID),
	})
}

type ApplyDecisionInput struct {
	Command domain.DecisionCommand
	Now     time.Time
}

func (input ApplyDecisionInput) Validate() error {
	return firstError([]error{
		validateDecisionCommand(input.Command),
		requiredTime("current time", input.Now),
	})
}

type ExpireApprovalInput struct {
	OrganizationID domain.ID
	RequestID      domain.ID
	Now            time.Time
}

func (input ExpireApprovalInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("approval request ID", input.RequestID),
		requiredTime("current time", input.Now),
	})
}

// CancelApprovalInput withdraws a pending request on behalf of the agent
// instance that opened it. The store refuses any other instance and any
// request already terminal; a request this instance already cancelled is
// returned unchanged, so a retried cancel is harmless.
type CancelApprovalInput struct {
	OrganizationID  domain.ID
	RequestID       domain.ID
	AgentInstanceID domain.ID
	// Auth is the credential the instance presented, recorded on the
	// cancellation decision the same way a reviewer's session is on
	// theirs.
	Auth domain.AuthContext
	Now  time.Time
}

func (input CancelApprovalInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("approval request ID", input.RequestID),
		requiredID("agent instance ID", input.AgentInstanceID),
		requiredString("authentication method", input.Auth.Method),
		requiredTime("current time", input.Now),
	})
}

// ConsumeApprovalInput marks an approved request used by the instance
// that opened it, under IdempotencyKey. The store reads the current time
// from the database, so the expiry check and consumed_at share one clock.
type ConsumeApprovalInput struct {
	OrganizationID  domain.ID
	RequestID       domain.ID
	AgentInstanceID domain.ID
	IdempotencyKey  string
}

func (input ConsumeApprovalInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("approval request ID", input.RequestID),
		requiredID("agent instance ID", input.AgentInstanceID),
		requiredString("idempotency key", input.IdempotencyKey),
	})
}

// ReportApprovalOutcomeInput records how a consumed approval's call went.
// An empty Note means none.
type ReportApprovalOutcomeInput struct {
	OrganizationID  domain.ID
	RequestID       domain.ID
	AgentInstanceID domain.ID
	Status          domain.ConsumptionOutcomeStatus
	Note            string
}

func (input ReportApprovalOutcomeInput) Validate() error {
	var status error
	if !input.Status.Valid() {
		status = invalidField("status", "must be succeeded or failed")
	}
	var note error
	if utf8.RuneCountInString(input.Note) > domain.MaximumConsumptionNoteLength {
		note = invalidField("note", fmt.Sprintf("must be at most %d characters", domain.MaximumConsumptionNoteLength))
	}
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("approval request ID", input.RequestID),
		requiredID("agent instance ID", input.AgentInstanceID),
		status,
		note,
	})
}

// ClaimApprovalInput claims a pending request for the principal. The store
// evaluates claim authorization atomically with the request's terminal
// state and computes the claim expiry from the organization's retrigger
// window, capped at the request deadline.
type ClaimApprovalInput struct {
	OrganizationID domain.ID
	RequestID      domain.ID
	Principal      domain.Principal
	Now            time.Time
}

func (input ClaimApprovalInput) Validate() error {
	if err := firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("approval request ID", input.RequestID),
		requiredID("principal actor ID", input.Principal.ActorID),
		requiredID("principal membership ID", input.Principal.MembershipID),
		requiredTime("current time", input.Now),
	}); err != nil {
		return err
	}
	if input.Principal.OrganizationID != input.OrganizationID {
		return invalidField("principal organization ID", "must match the claim organization ID")
	}
	if input.Principal.ActorType != domain.ActorHuman {
		return invalidField("principal actor type", "must be human")
	}
	return nil
}

type ReleaseClaimInput struct {
	OrganizationID domain.ID
	RequestID      domain.ID
	MembershipID   domain.ID
	ActorID        domain.ID
	Now            time.Time
}

func (input ReleaseClaimInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("approval request ID", input.RequestID),
		requiredID("membership ID", input.MembershipID),
		requiredID("actor ID", input.ActorID),
		requiredTime("current time", input.Now),
	})
}

// LapseClaimInput lapses one specific claim by id. A claim already released
// for any reason, or superseded by a newer claim, is a clean no-op, which
// makes duplicate workflow timers and activity retries safe.
type LapseClaimInput struct {
	OrganizationID domain.ID
	RequestID      domain.ID
	ClaimID        domain.ID
	Now            time.Time
}

func (input LapseClaimInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("approval request ID", input.RequestID),
		requiredID("claim ID", input.ClaimID),
		requiredTime("current time", input.Now),
	})
}

// SubmitClaimInput arms the claim's retrigger timer on the request
// workflow. ExpiresAt is the store-computed expiry, so workflow code never
// reads configuration.
// SubmitCancellationInput tells the request's workflow that the request
// was withdrawn, so it stands down notifications and timers. The
// cancellation itself committed before this is sent.
type SubmitCancellationInput struct {
	OrganizationID domain.ID
	RequestID      domain.ID
}

func (input SubmitCancellationInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("approval request ID", input.RequestID),
	})
}

type SubmitClaimInput struct {
	OrganizationID domain.ID
	RequestID      domain.ID
	ClaimID        domain.ID
	ExpiresAt      time.Time
}

func (input SubmitClaimInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("approval request ID", input.RequestID),
		requiredID("claim ID", input.ClaimID),
		requiredTime("claim expiry time", input.ExpiresAt),
	})
}

type StartApprovalInput struct {
	Request domain.ApprovalRequest
}

func (input StartApprovalInput) Validate() error {
	if err := validateApprovalRequest(input.Request); err != nil {
		return err
	}
	if input.Request.Status != domain.ApprovalPending {
		return invalidField("approval status", "must be pending when started")
	}
	return nil
}

type SubmitDecisionInput struct {
	Command domain.DecisionCommand
}

func (input SubmitDecisionInput) Validate() error {
	return validateDecisionCommand(input.Command)
}

func validateApprovalRequest(request domain.ApprovalRequest) error {
	if err := firstError([]error{
		requiredID("approval request ID", request.ID),
		requiredID("organization ID", request.OrganizationID),
		requiredID("agent ID", request.AgentID),
		requiredID("agent instance ID", request.AgentInstanceID),
		validAgentSlug(request.AgentSlug),
		requiredString("tool", request.Tool),
		validJSON("arguments", request.Arguments),
		optionalJSON("context", request.Context),
		requiredString("idempotency key", request.IdempotencyKey),
		requiredString("semantic hash", request.SemanticHash),
		requiredTime("approval deadline", request.DeadlineAt),
		requiredTime("approval creation time", request.CreatedAt),
		requiredTime("approval update time", request.UpdatedAt),
	}); err != nil {
		return err
	}
	switch request.Status {
	case domain.ApprovalPending, domain.ApprovalApproved, domain.ApprovalDenied, domain.ApprovalExpired, domain.ApprovalCancelled:
	default:
		return invalidField("approval status", "is invalid")
	}
	if !request.DeadlineAt.After(request.CreatedAt) {
		return invalidField("approval deadline", "must be after the creation time")
	}
	return nil
}

func validateDecisionCommand(command domain.DecisionCommand) error {
	// A signed-in reviewer decides through a session. A personal API key
	// never signs in: it is identified by its key id instead.
	authID := requiredID("session ID", command.Principal.Auth.SessionID)
	if command.Principal.Auth.Method == domain.AuthMethodAPIKey {
		authID = requiredID("API key ID", command.Principal.Auth.CredentialID)
	}
	if err := firstError([]error{
		requiredID("decision operation ID", command.OperationID),
		requiredID("approval request ID", command.RequestID),
		requiredID("organization ID", command.OrganizationID),
		requiredID("principal actor ID", command.Principal.ActorID),
		requiredID("principal organization ID", command.Principal.OrganizationID),
		requiredID("principal membership ID", command.Principal.MembershipID),
		requiredString("authentication method", command.Principal.Auth.Method),
		requiredString("authentication assurance", command.Principal.Auth.Assurance),
		requiredTime("authentication time", command.Principal.Auth.AuthenticatedAt),
		authID,
		requiredString("decision channel", command.Channel),
		requiredString("idempotency key", command.IdempotencyKey),
	}); err != nil {
		return err
	}
	if command.Principal.OrganizationID != command.OrganizationID {
		return invalidField("principal organization ID", "must match the command organization ID")
	}
	if command.Principal.ActorType != domain.ActorHuman {
		return invalidField("principal actor type", "must be human")
	}
	if command.Status != domain.ApprovalApproved && command.Status != domain.ApprovalDenied {
		return invalidField("decision status", "must be approved or denied")
	}
	return nil
}

// AddApprovalReviewerInput widens a pending, routed request to one more
// person. The repository checks inside its transaction that the actor is
// targeted (or holds request.decide.unrouted), that the request is pending
// and escalated, and that the added member is active and may decide.
type AddApprovalReviewerInput struct {
	OrganizationID domain.ID
	RequestID      domain.ID
	MembershipID   domain.ID
	Actor          domain.Principal
	Now            time.Time
}

func (input AddApprovalReviewerInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("approval request ID", input.RequestID),
		requiredID("membership ID", input.MembershipID),
		requiredID("acting membership ID", input.Actor.MembershipID),
		requiredTime("current time", input.Now),
	})
}

// GetApprovalRoutingStateInput asks how a request's routing relates to one
// membership, for the review view and the decide affordance.
// MembershipID is optional for organization-key readers, who are never targeted.
type GetApprovalRoutingStateInput struct {
	OrganizationID domain.ID
	RequestID      domain.ID
	MembershipID   domain.ID
}

func (input GetApprovalRoutingStateInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("approval request ID", input.RequestID),
	})
}

// ApprovalRoutingState is the answer: whether the request carries routing
// targets, whether the membership is among them (directly, through a team,
// or as an added reviewer), and who was added.
type ApprovalRoutingState struct {
	HasTargets     bool
	Targeted       bool
	AddedReviewers []domain.ApprovalReviewer
}

type ListReviewerCandidatesInput struct {
	OrganizationID domain.ID
	RequestID      domain.ID
}

func (input ListReviewerCandidatesInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("approval request ID", input.RequestID),
	})
}

// ReviewerCandidate is one person who may be added to a request.
type ReviewerCandidate struct {
	MembershipID domain.ID
	DisplayName  string
	Email        string
}

// AgentOwnerHandoffSignal is one handoff whose workflow signal is due: the
// request moved from its agent's deactivated owner to the pinned fallback.
type AgentOwnerHandoffSignal struct {
	OrganizationID domain.ID
	RequestID      domain.ID
}

type ListAgentOwnerHandoffSignalsInput struct {
	Limit int
}

func (input ListAgentOwnerHandoffSignalsInput) Validate() error {
	return nonNegative("limit", int64(input.Limit))
}

type MarkAgentOwnerHandoffSignaledInput struct {
	OrganizationID domain.ID
	RequestID      domain.ID
	SignaledAt     time.Time
}

func (input MarkAgentOwnerHandoffSignaledInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("approval request ID", input.RequestID),
		requiredTime("handoff signaled time", input.SignaledAt),
	})
}

// SubmitAgentOwnerHandoffInput tells a request's workflow that its agent's
// owner was deactivated and the pinned fallback takes over.
type SubmitAgentOwnerHandoffInput struct {
	OrganizationID domain.ID
	RequestID      domain.ID
}

func (input SubmitAgentOwnerHandoffInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("approval request ID", input.RequestID),
	})
}
