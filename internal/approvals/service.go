package approvals

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/authorization"
)

// MaximumTimeout bounds how long a request may wait for a human. A week
// covers a change that sits over a weekend or a reviewer on leave; beyond
// that a request is stale rather than pending.
const MaximumTimeout = 7 * 24 * time.Hour

type Service struct {
	approvals    application.ApprovalRepository
	handoffs     application.ApprovalWorkflowOutboxRepository
	policies     organizationPolicyReader
	evaluator    approvalEvaluator
	router       approvalRouter
	orchestrator application.LifecycleOrchestrator
	entitlements application.Entitlements
	clock        application.Clock
}

type approvalEvaluator interface {
	Evaluate(context.Context, domain.ApprovalRequest) (application.InitializeApprovalInput, error)
}

// approvalRouter pins the routing outcome onto a pending initialization.
// It is fail-safe by contract and never blocks request creation; nil
// disables routing entirely.
type approvalRouter interface {
	Decorate(context.Context, application.InitializeApprovalInput) application.InitializeApprovalInput
}

// organizationPolicyReader reads the organization policy the queue
// applies: request visibility on every human read, and who may decide an
// unrouted request when the review view computes the decide affordance.
type organizationPolicyReader interface {
	GetOrganizationPolicy(context.Context, application.GetOrganizationPolicyInput) (domain.OrganizationPolicy, error)
}

func NewService(approvals application.ApprovalRepository, handoffs application.ApprovalWorkflowOutboxRepository, policies organizationPolicyReader, evaluator approvalEvaluator, router approvalRouter, orchestrator application.LifecycleOrchestrator, entitlements application.Entitlements, clock application.Clock) *Service {
	return &Service{approvals: approvals, handoffs: handoffs, policies: policies, evaluator: evaluator, router: router, orchestrator: orchestrator, entitlements: entitlements, clock: clock}
}

type CreateInput struct {
	Tool string `json:"tool" binding:"required"`
	// Server is the MCP server that defines the tool, as the adapter
	// observed it; absent for built-in tools.
	Server string `json:"server,omitempty"`
	// Arguments are the ground truth the reviewer decides on; the provider
	// renders the review UI from them. AgentReasoning is free text from the
	// agent, displayed as an unverified claim and never as a description of
	// the action.
	Arguments      json.RawMessage `json:"arguments" binding:"required"`
	AgentReasoning string          `json:"agent_reasoning,omitempty"`
	Context        json.RawMessage `json:"context,omitempty"`
	Timeout        time.Duration   `json:"-"`
	TimeoutText    string          `json:"timeout"`
	IdempotencyKey string          `json:"-"`
}

func (s *Service) Create(ctx context.Context, principal domain.Principal, input CreateInput) (domain.ApprovalRequest, error) {
	if principal.ActorType != domain.ActorAgent || principal.AgentInstanceID == "" {
		return domain.ApprovalRequest{}, domain.ErrForbidden
	}
	if !principal.AgentSlug.Valid() {
		return domain.ApprovalRequest{}, fmt.Errorf("authenticated agent slug is invalid: %w", domain.ErrInvalid)
	}
	if err := authorization.Check(principal, domain.PermissionRequestCreate, domain.OrganizationResource()); err != nil {
		return domain.ApprovalRequest{}, err
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return domain.ApprovalRequest{}, fmt.Errorf("idempotency key is required: %w", domain.ErrInvalid)
	}
	if input.Timeout == 0 {
		var err error
		input.Timeout, err = time.ParseDuration(input.TimeoutText)
		if err != nil {
			return domain.ApprovalRequest{}, fmt.Errorf("invalid timeout: %w", domain.ErrInvalid)
		}
	}
	if input.Timeout < time.Second || input.Timeout > MaximumTimeout {
		return domain.ApprovalRequest{}, fmt.Errorf("timeout must be between 1s and 168h: %w", domain.ErrInvalid)
	}
	if len(input.Arguments) == 0 || !json.Valid(input.Arguments) || strings.TrimSpace(string(input.Arguments))[0] != '{' {
		return domain.ApprovalRequest{}, fmt.Errorf("arguments must be a JSON object: %w", domain.ErrInvalid)
	}

	if input.TimeoutText == "" {
		input.TimeoutText = input.Timeout.String()
	}
	if len(input.Context) > 0 && (!json.Valid(input.Context) || strings.TrimSpace(string(input.Context))[0] != '{') {
		return domain.ApprovalRequest{}, fmt.Errorf("context must be a JSON object: %w", domain.ErrInvalid)
	}
	if existing, err := s.approvals.GetApprovalByIdempotencyKey(ctx, application.GetApprovalByIdempotencyKeyInput{
		OrganizationID:  principal.OrganizationID,
		AgentInstanceID: principal.AgentInstanceID,
		IdempotencyKey:  input.IdempotencyKey,
	}); err == nil {
		if approvalFingerprint(existing.AgentID, existing.AgentSlug, existing.Tool, existing.Server, existing.Arguments, existing.AgentReasoning,
			existing.Context, existing.Timeout) !=
			approvalFingerprint(principal.ActorID, principal.AgentSlug, input.Tool, input.Server, input.Arguments, input.AgentReasoning,
				input.Context, input.TimeoutText) {
			return domain.ApprovalRequest{}, errors.Join(domain.ErrIdempotencyConflict, domain.ErrConflict)
		}
		s.startHumanReview(ctx, existing)
		return existing, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.ApprovalRequest{}, err
	}

	now := s.clock.Now()
	requestID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("withhuman:approval:"+string(principal.OrganizationID)+":"+string(principal.AgentInstanceID)+":"+input.IdempotencyKey))
	request := domain.ApprovalRequest{
		ID: domain.ID(requestID.String()), OrganizationID: principal.OrganizationID,
		AgentID: principal.ActorID, AgentInstanceID: principal.AgentInstanceID,
		AgentSlug: principal.AgentSlug,
		// A personal instance's principal carries its owner, so routing
		// can ask the owner before the request is stored; the insert
		// copies the same owner from the instance row.
		RequesterMembershipID: principal.MembershipID,
		Tool:                  input.Tool, Server: input.Server, Arguments: input.Arguments, Timeout: input.TimeoutText,
		AgentReasoning: input.AgentReasoning, Context: input.Context,
		IdempotencyKey: input.IdempotencyKey, SemanticHash: semanticHash(input.Tool, input.Server, input.Arguments),
		Status: domain.ApprovalPending, DeadlineAt: now.Add(input.Timeout), CreatedAt: now, UpdatedAt: now,
	}
	initialization, err := s.evaluator.Evaluate(ctx, request)
	if err != nil {
		return domain.ApprovalRequest{}, err
	}
	if s.router != nil {
		initialization = s.router.Decorate(ctx, initialization)
	}
	if initialization.Request.Status == domain.ApprovalPending {
		if err := s.checkPendingLimit(ctx, principal.OrganizationID); err != nil {
			return domain.ApprovalRequest{}, err
		}
	}
	initialized, err := s.approvals.InitializeApproval(ctx, initialization)
	if err != nil {
		return domain.ApprovalRequest{}, err
	}
	if approvalFingerprint(initialized.AgentID, initialized.AgentSlug, initialized.Tool, initialized.Server, initialized.Arguments, initialized.AgentReasoning,
		initialized.Context, initialized.Timeout) !=
		approvalFingerprint(principal.ActorID, principal.AgentSlug, input.Tool, input.Server, input.Arguments, input.AgentReasoning,
			input.Context, input.TimeoutText) {
		return domain.ApprovalRequest{}, errors.Join(domain.ErrIdempotencyConflict, domain.ErrConflict)
	}
	s.startHumanReview(ctx, initialized)
	// Metering is lossy-tolerant: recording failures must never fail the
	// approval request they record.
	if err := s.entitlements.RecordUsage(ctx, application.RecordUsageInput{
		OrganizationID: principal.OrganizationID,
		Dimension:      application.DimensionApprovalRequests,
		Amount:         1,
		IdempotencyKey: "approval_request:" + string(initialized.ID),
		Now:            now,
	}); err != nil {
		slog.WarnContext(ctx, "approval request usage could not be recorded", "dimension", application.DimensionApprovalRequests, "error", err)
	}
	return initialized, nil
}

func (s *Service) checkPendingLimit(ctx context.Context, organizationID domain.ID) error {
	limit, err := s.entitlements.Limit(ctx, application.LimitInput{
		OrganizationID: organizationID,
		Dimension:      application.DimensionPendingApprovals,
	})
	if err != nil {
		return err
	}
	if limit.Unlimited {
		return nil
	}
	pending, err := s.approvals.CountPendingApprovals(ctx, application.CountPendingApprovalsInput{OrganizationID: organizationID})
	if err != nil {
		return err
	}
	if pending >= limit.Value {
		return fmt.Errorf("the organization has reached its limit of %d pending requests: %w", limit.Value, domain.ErrLimitExceeded)
	}
	return nil
}

func (s *Service) startHumanReview(ctx context.Context, request domain.ApprovalRequest) {
	if request.Status != domain.ApprovalPending {
		return
	}
	if err := s.orchestrator.StartApproval(ctx, application.StartApprovalInput{Request: request}); err != nil {
		slog.WarnContext(ctx, "human review workflow start failed; outbox will retry", "approval_request_id", request.ID, "error", err)
		return
	}
	if err := s.handoffs.MarkApprovalWorkflowStarted(ctx, application.MarkApprovalWorkflowStartedInput{
		OrganizationID: request.OrganizationID,
		RequestID:      request.ID,
		PublishedAt:    s.clock.Now(),
	}); err != nil {
		slog.WarnContext(ctx, "human review workflow started but outbox acknowledgment failed; dispatch may repeat", "approval_request_id", request.ID, "error", err)
	}
}

type DecideInput struct {
	Status  domain.ApprovalStatus `json:"status" binding:"required"`
	Note    string                `json:"note,omitempty"`
	Channel string                `json:"channel"`
	// OutsideRouting is the reviewer's explicit acknowledgement that they
	// are deciding a request routing did not hand them (break glass). It
	// is part of the decision's intent, so a retry that changes it is a
	// different decision.
	OutsideRouting bool   `json:"outside_routing,omitempty"`
	IdempotencyKey string `json:"-"`
}

func (s *Service) Decide(ctx context.Context, principal domain.Principal, requestID domain.ID, input DecideInput) (domain.DecisionOperation, error) {
	if err := requireHuman(principal, domain.PermissionRequestDecide, domain.RequestResource(requestID)); err != nil {
		return domain.DecisionOperation{}, err
	}
	if input.Status != domain.ApprovalApproved && input.Status != domain.ApprovalDenied {
		return domain.DecisionOperation{}, fmt.Errorf("decision must be approved or denied: %w", domain.ErrInvalid)
	}
	if input.IdempotencyKey == "" {
		return domain.DecisionOperation{}, fmt.Errorf("idempotency key is required: %w", domain.ErrInvalid)
	}
	if input.Channel == "" {
		input.Channel = "web"
	}
	operation := domain.DecisionOperation{
		ID: domain.ID(uuid.NewString()), OrganizationID: principal.OrganizationID,
		RequestID: requestID, Status: domain.OperationPending, CreatedAt: s.clock.Now(),
	}
	intentHash := decisionFingerprint(input.Status, input.Note, input.Channel, input.OutsideRouting)
	operation, err := s.approvals.CreateDecisionOperation(ctx, application.CreateDecisionOperationInput{
		Operation:      operation,
		IdempotencyKey: input.IdempotencyKey,
		IntentHash:     intentHash,
	})
	if err != nil {
		return domain.DecisionOperation{}, err
	}
	if operation.Status != domain.OperationPending {
		return operation, operationError(operation)
	}
	command := domain.DecisionCommand{
		OperationID: operation.ID, RequestID: requestID, OrganizationID: principal.OrganizationID,
		Principal: principal, Status: input.Status, Note: input.Note, Channel: input.Channel,
		IdempotencyKey: input.IdempotencyKey, OutsideRouting: input.OutsideRouting,
	}
	err = s.orchestrator.SubmitDecision(ctx, application.SubmitDecisionInput{Command: command})
	if errors.Is(err, application.ErrWorkflowClosed) {
		// No workflow is left to record the decision, so record it here:
		// the store rejects it against the terminal request and closes the
		// operation, which answers retries under the same key.
		operation, err = s.approvals.ApplyDecision(ctx, application.ApplyDecisionInput{Command: command, Now: s.clock.Now()})
		if err != nil {
			return domain.DecisionOperation{}, err
		}
		return operation, operationError(operation)
	}
	if err != nil {
		return domain.DecisionOperation{}, err
	}
	return s.waitForOperation(ctx, principal.OrganizationID, operation.ID)
}

// Claim marks a pending request as being reviewed by the principal. The
// claim write commits synchronously; arming the retrigger timer on the
// workflow is best-effort, because read-time expiry and the stale-claim
// sweep keep a lost signal harmless.
func (s *Service) Claim(ctx context.Context, principal domain.Principal, requestID domain.ID) (domain.Claim, error) {
	if err := requireHuman(principal, domain.PermissionRequestClaim, domain.RequestResource(requestID)); err != nil {
		return domain.Claim{}, err
	}
	claim, err := s.approvals.ClaimApproval(ctx, application.ClaimApprovalInput{
		OrganizationID: principal.OrganizationID,
		RequestID:      requestID,
		Principal:      principal,
		Now:            s.clock.Now(),
	})
	if err != nil {
		return domain.Claim{}, err
	}
	if err := s.orchestrator.SubmitClaim(ctx, application.SubmitClaimInput{
		OrganizationID: principal.OrganizationID,
		RequestID:      requestID,
		ClaimID:        claim.ID,
		ExpiresAt:      claim.ExpiresAt,
	}); err != nil {
		slog.WarnContext(ctx, "claim retrigger timer not armed", "approval_request_id", requestID, "claim_id", claim.ID, "error", err)
	}
	return claim, nil
}

// ReleaseClaim releases the principal's own claim. The release commits
// synchronously; telling the workflow is best-effort, because a lost
// signal only means escalation resumes when the retrigger timer lapses
// against the already released claim, a clean no-op.
func (s *Service) ReleaseClaim(ctx context.Context, principal domain.Principal, requestID domain.ID) error {
	if err := requireHuman(principal, domain.PermissionRequestClaim, domain.RequestResource(requestID)); err != nil {
		return err
	}
	claimID, err := s.approvals.ReleaseClaim(ctx, application.ReleaseClaimInput{
		OrganizationID: principal.OrganizationID,
		RequestID:      requestID,
		MembershipID:   principal.MembershipID,
		ActorID:        principal.ActorID,
		Now:            s.clock.Now(),
	})
	if err != nil {
		return err
	}
	if err := s.orchestrator.SubmitClaimRelease(ctx, application.SubmitClaimReleaseInput{
		OrganizationID: principal.OrganizationID,
		RequestID:      requestID,
		ClaimID:        claimID,
	}); err != nil {
		slog.WarnContext(ctx, "claim release not signalled to the request workflow", "approval_request_id", requestID, "claim_id", claimID, "error", err)
	}
	return nil
}

// Cancel withdraws a pending request for the agent instance that opened
// it: the runtime died, was interrupted, or stopped waiting, and nobody
// should approve into the void. The cancellation commits synchronously as
// the request's terminal decision; standing the workflow down is
// best-effort, because the deadline activity is already harmless against
// a terminal request and the queue no longer lists it.
func (s *Service) Cancel(ctx context.Context, principal domain.Principal, requestID domain.ID) (domain.ApprovalRequest, error) {
	if principal.ActorType != domain.ActorAgent || principal.AgentInstanceID == "" {
		return domain.ApprovalRequest{}, domain.ErrForbidden
	}
	if err := authorization.Check(principal, domain.PermissionRequestCreate, domain.OrganizationResource()); err != nil {
		return domain.ApprovalRequest{}, err
	}
	request, err := s.approvals.CancelApproval(ctx, application.CancelApprovalInput{
		OrganizationID:  principal.OrganizationID,
		RequestID:       requestID,
		AgentInstanceID: principal.AgentInstanceID,
		Auth:            principal.Auth,
		Now:             s.clock.Now(),
	})
	if err != nil {
		return domain.ApprovalRequest{}, err
	}
	if err := s.orchestrator.SubmitCancellation(ctx, application.SubmitCancellationInput{
		OrganizationID: principal.OrganizationID,
		RequestID:      requestID,
	}); err != nil {
		slog.WarnContext(ctx, "cancellation not signalled to the request workflow", "approval_request_id", requestID, "error", err)
	}
	return request, nil
}

// SignalCancelled stands down the workflows of requests another path
// already cancelled, such as an AAP instance deletion. Like Cancel's, the
// signal is best-effort.
func (s *Service) SignalCancelled(ctx context.Context, organizationID domain.ID, requestIDs []domain.ID) {
	for _, requestID := range requestIDs {
		if err := s.orchestrator.SubmitCancellation(ctx, application.SubmitCancellationInput{
			OrganizationID: organizationID, RequestID: requestID,
		}); err != nil {
			slog.WarnContext(ctx, "cancellation not signalled to the request workflow", "approval_request_id", requestID, "error", err)
		}
	}
}

// AddReviewer widens a pending request's routing to one more person. The
// actor must be a reviewer; the repository enforces the rest inside its
// transaction (the actor is targeted or holds break glass, the request is
// pending and routed, the added member is active and may decide) and
// audits the widening as request.reviewer_added.
// ReviewerCandidates lists who a reviewer may hand the request to. Gated
// like deciding, because it exists for the handoff and nothing else.
func (s *Service) ReviewerCandidates(ctx context.Context, principal domain.Principal, requestID domain.ID) ([]application.ReviewerCandidate, error) {
	if err := requireHuman(principal, domain.PermissionRequestDecide, domain.RequestResource(requestID)); err != nil {
		return nil, err
	}
	if _, err := s.Get(ctx, principal, requestID); err != nil {
		return nil, err
	}
	return s.approvals.ListReviewerCandidates(ctx, application.ListReviewerCandidatesInput{
		OrganizationID: principal.OrganizationID, RequestID: requestID,
	})
}

func (s *Service) AddReviewer(ctx context.Context, principal domain.Principal, requestID, membershipID domain.ID) (domain.ApprovalReviewer, error) {
	if err := requireHuman(principal, domain.PermissionRequestDecide, domain.RequestResource(requestID)); err != nil {
		return domain.ApprovalReviewer{}, err
	}
	if strings.TrimSpace(string(membershipID)) == "" {
		return domain.ApprovalReviewer{}, fmt.Errorf("membership ID is required: %w", domain.ErrInvalid)
	}
	return s.approvals.AddApprovalReviewer(ctx, application.AddApprovalReviewerInput{
		OrganizationID: principal.OrganizationID,
		RequestID:      requestID,
		MembershipID:   membershipID,
		Actor:          principal,
		Now:            s.clock.Now(),
	})
}

func (s *Service) Get(ctx context.Context, principal domain.Principal, requestID domain.ID) (domain.ApprovalRequest, error) {
	policy, err := s.policyFor(ctx, principal)
	if err != nil {
		return domain.ApprovalRequest{}, err
	}
	return s.get(ctx, principal, requestID, visibleTo(principal, policy))
}

// get is the shared read: the permission check, the organization-scoped
// fetch narrowed to what the principal may see, and the rule that an
// agent reads only its own requests.
func (s *Service) get(ctx context.Context, principal domain.Principal, requestID, visibleTo domain.ID) (domain.ApprovalRequest, error) {
	if err := authorization.Check(principal, domain.PermissionRequestRead, domain.RequestResource(requestID)); err != nil {
		return domain.ApprovalRequest{}, err
	}
	request, err := s.approvals.GetApproval(ctx, application.GetApprovalInput{
		OrganizationID: principal.OrganizationID,
		RequestID:      requestID,
		VisibleTo:      visibleTo,
	})
	if err != nil {
		return domain.ApprovalRequest{}, err
	}
	if principal.ActorType != domain.ActorHuman && principal.ActorType != domain.ActorAPIKey && (request.AgentID != principal.ActorID || request.AgentInstanceID != principal.AgentInstanceID) {
		return domain.ApprovalRequest{}, domain.ErrForbidden
	}
	return request, nil
}

// RoutingState reports how the request's routing relates to the
// principal: whether it carries targets, whether the principal is one, and
// who was added as a reviewer. The read authorization is the request's.
func (s *Service) RoutingState(ctx context.Context, principal domain.Principal, requestID domain.ID) (application.ApprovalRoutingState, error) {
	if _, err := s.Get(ctx, principal, requestID); err != nil {
		return application.ApprovalRoutingState{}, err
	}
	if !domain.ManagementPrincipal(principal) {
		return application.ApprovalRoutingState{}, domain.ErrForbidden
	}
	return s.routingState(ctx, principal, requestID)
}

// routingState tolerates a request without routing rows, which is how a
// request the pipeline decided automatically reads: no targets, nobody
// added.
func (s *Service) routingState(ctx context.Context, principal domain.Principal, requestID domain.ID) (application.ApprovalRoutingState, error) {
	state, err := s.approvals.GetApprovalRoutingState(ctx, application.GetApprovalRoutingStateInput{
		OrganizationID: principal.OrganizationID,
		RequestID:      requestID,
		MembershipID:   principal.MembershipID,
	})
	if errors.Is(err, domain.ErrNotFound) {
		state, err = application.ApprovalRoutingState{}, nil
	}
	if err != nil {
		return application.ApprovalRoutingState{}, err
	}
	if state.AddedReviewers == nil {
		state.AddedReviewers = []domain.ApprovalReviewer{}
	}
	return state, nil
}

// policyFor reads the organization policy for a human principal. Agents
// never read under the visibility policy (they read their own requests
// only), so no policy is loaded for them.
func (s *Service) policyFor(ctx context.Context, principal domain.Principal) (domain.OrganizationPolicy, error) {
	if principal.ActorType != domain.ActorHuman {
		return domain.OrganizationPolicy{}, nil
	}
	return s.policies.GetOrganizationPolicy(ctx, application.GetOrganizationPolicyInput{OrganizationID: principal.OrganizationID})
}

// visibleTo answers which membership a read is narrowed to. Routing
// selects deciders, not readers, so visibility is an organization policy:
// under routed_only a reader sees what was routed to them, and anyone
// holding request.read.unrouted (owners and auditors) sees everything
// either way.
func visibleTo(principal domain.Principal, policy domain.OrganizationPolicy) domain.ID {
	if principal.ActorType != domain.ActorHuman || policy.RequestVisibility != domain.VisibilityRoutedOnly {
		return ""
	}
	if authorization.HoldsAnywhere(principal, domain.PermissionRequestReadUnrouted) {
		return ""
	}
	return principal.MembershipID
}

func (s *Service) Wait(ctx context.Context, principal domain.Principal, requestID domain.ID, wait time.Duration) (domain.ApprovalRequest, error) {
	deadline := s.clock.Now().Add(wait)
	for {
		request, err := s.Get(ctx, principal, requestID)
		if err != nil || request.Status.Terminal() || !s.clock.Now().Before(deadline) {
			return request, err
		}
		if err := waitContext(ctx, 150*time.Millisecond); err != nil {
			return domain.ApprovalRequest{}, err
		}
	}
}

// List is the review queue, or one agent's history when agentID is set.
// The queue exists for humans: automatic pipeline decisions never appear
// in it and are visible through the audit log instead. An agent's own
// history is the exception: the agent page shows everything the agent
// asked for, automatic decisions included, because that is what the
// agent has been doing.
func (s *Service) List(ctx context.Context, principal domain.Principal, input application.ListApprovalsInput) ([]domain.ApprovalRequest, error) {
	if !domain.ManagementPrincipal(principal) || !authorization.HoldsAnywhere(principal, domain.PermissionRequestRead) {
		return nil, domain.ErrForbidden
	}
	policy, err := s.policyFor(ctx, principal)
	if err != nil {
		return nil, err
	}
	input.OrganizationID = principal.OrganizationID
	input.HumanReviewOnly = principal.ActorType == domain.ActorHuman && input.AgentSlug == "" && input.PerAgent == 0
	input.VisibleTo = visibleTo(principal, policy)
	if !scopeQueue(&input, principal, policy) {
		return []domain.ApprovalRequest{}, nil
	}
	return s.approvals.ListApprovals(ctx, input)
}

// scopeQueue turns the layer a human reader asked for into the listing's
// narrowing fields, on top of the visibility policy. It reports false when
// the layer is empty by construction: someone who may not decide requests
// has nothing in the decidable layer. The notified layer is the person's
// own notification records; the decidable layer is the routing rule the
// decision kernel applies, minus the checks only the transaction can make.
func scopeQueue(input *application.ListApprovalsInput, principal domain.Principal, policy domain.OrganizationPolicy) bool {
	if principal.ActorType != domain.ActorHuman || principal.MembershipID == "" {
		return true
	}
	switch input.Scope {
	case application.QueueScopeNotified:
		input.NotifiedTo = principal.MembershipID
	case application.QueueScopeDecidable:
		if !authorization.HoldsAnywhere(principal, domain.PermissionRequestDecide) {
			return false
		}
		input.VisibleTo = principal.MembershipID
		input.HideUnrouted = policy.UnroutedRequests == domain.UnroutedBreakGlassOnly &&
			!authorization.HoldsAnywhere(principal, domain.PermissionRequestDecideUnrouted)
	}
	return true
}

// requireHuman is the reviewer-side gate: the actor must be a person and
// hold the permission on the request being reviewed.
func requireHuman(principal domain.Principal, permission domain.Permission, resource domain.Resource) error {
	if principal.ActorType != domain.ActorHuman {
		return domain.ErrForbidden
	}
	return authorization.Check(principal, permission, resource)
}

func (s *Service) waitForOperation(ctx context.Context, organizationID, operationID domain.ID) (domain.DecisionOperation, error) {
	for {
		operation, err := s.approvals.GetDecisionOperation(ctx, application.GetDecisionOperationInput{
			OrganizationID: organizationID,
			OperationID:    operationID,
		})
		if err != nil {
			return domain.DecisionOperation{}, err
		}
		if operation.Status != domain.OperationPending {
			return operation, operationError(operation)
		}
		if err := waitContext(ctx, 25*time.Millisecond); err != nil {
			return domain.DecisionOperation{}, err
		}
	}
}

// RoutingRejection is a decision refused by the routing rule, carrying the
// kernel's code so the client can say which rule: not_targeted (the
// request was routed elsewhere), break_glass_only (the organization sends
// unrouted requests to break-glass holders), or outside_routing_required
// (a break-glass holder must acknowledge deciding outside routing).
type RoutingRejection struct {
	Code string
}

func (r RoutingRejection) Error() string {
	switch r.Code {
	case "break_glass_only":
		return "only break-glass reviewers can decide requests sent to no one"
	case "outside_routing_required":
		return "confirm you want to decide a request not sent to you"
	default:
		return domain.ErrNotTargeted.Error()
	}
}

func (r RoutingRejection) Unwrap() error { return domain.ErrNotTargeted }

func operationError(operation domain.DecisionOperation) error {
	if operation.Status != domain.OperationRejected {
		return nil
	}
	switch operation.ErrorCode {
	case "membership_inactive":
		return domain.ErrMembershipInactive
	case "authentication_stale":
		return domain.ErrAuthenticationStale
	case "authentication_weak":
		return domain.ErrAuthenticationWeak
	case "not_targeted", "break_glass_only", "outside_routing_required":
		return RoutingRejection{Code: operation.ErrorCode}
	case "already_terminal":
		return domain.ErrAlreadyTerminal
	case "forbidden":
		return domain.ErrForbidden
	default:
		return fmt.Errorf("decision rejected: %s", operation.ErrorMessage)
	}
}

func semanticHash(tool, server string, arguments json.RawMessage) string {
	normalized := normalizedJSON(arguments)
	canonical, _ := json.Marshal(normalized)
	hash := sha256.Sum256(append([]byte(strings.TrimSpace(server)+"\n"+strings.TrimSpace(tool)+"\n"), canonical...))
	return hex.EncodeToString(hash[:])
}

func approvalFingerprint(agentID domain.ID, agentSlug domain.AgentSlug, tool, server string, arguments json.RawMessage, agentReasoning string, requestContext json.RawMessage, timeout string) string {
	canonical, _ := json.Marshal(map[string]any{
		"agent_id":        strings.TrimSpace(string(agentID)),
		"agent_slug":      strings.TrimSpace(string(agentSlug)),
		"tool":            tool,
		"server":          server,
		"arguments":       normalizedJSON(arguments),
		"agent_reasoning": agentReasoning,
		"context":         normalizedJSON(requestContext),
		"timeout":         timeout,
	})
	hash := sha256.Sum256(canonical)
	return hex.EncodeToString(hash[:])
}

func normalizedJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	var normalized any
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(&normalized); err != nil {
		return string(value)
	}
	return canonicalJSON(normalized)
}

// JSONB changes number spelling (1e3 becomes 1000). Canonicalize exact decimal
// values without float64 rounding or expanding arbitrarily large exponents.
// Containers are tagged so user objects cannot impersonate canonical numbers.
func canonicalJSON(value any) any {
	switch v := value.(type) {
	case json.Number:
		mantissa, exponentText, _ := strings.Cut(strings.ToLower(string(v)), "e")
		exponent := new(big.Int)
		if exponentText != "" {
			exponent.SetString(exponentText, 10)
		}
		sign := ""
		if strings.HasPrefix(mantissa, "-") {
			sign, mantissa = "-", mantissa[1:]
		}
		whole, fraction, _ := strings.Cut(mantissa, ".")
		exponent.Sub(exponent, big.NewInt(int64(len(fraction))))
		digits := strings.TrimLeft(whole+fraction, "0")
		if digits == "" {
			return []any{"number", "0", "0"}
		}
		trimmed := strings.TrimRight(digits, "0")
		exponent.Add(exponent, big.NewInt(int64(len(digits)-len(trimmed))))
		return []any{"number", sign + trimmed, exponent.String()}
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			out[key] = canonicalJSON(child)
		}
		return []any{"object", out}
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = canonicalJSON(child)
		}
		return []any{"array", out}
	default:
		return value
	}
}

func decisionFingerprint(status domain.ApprovalStatus, note, channel string, outsideRouting bool) string {
	canonical, _ := json.Marshal(map[string]any{
		"status":          string(status),
		"note":            strings.TrimSpace(note),
		"channel":         strings.TrimSpace(channel),
		"outside_routing": outsideRouting,
	})
	hash := sha256.Sum256(canonical)
	return hex.EncodeToString(hash[:])
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
