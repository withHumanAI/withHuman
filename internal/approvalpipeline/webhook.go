package approvalpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/approvalblock"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/webhookdelivery"
)

// WebhookRuntimeFingerprint covers the payload shape, the signature scheme,
// the answer contract, and the limits. Any change to those changes the
// compiled cache key.
const WebhookRuntimeFingerprint = "withhuman-webhook/v1;delivery/v1;limits/v1"

const webhookConfigVersion = 1

// WebhookDeliverer posts one signed delivery under the egress policy.
type WebhookDeliverer interface {
	Deliver(context.Context, webhookdelivery.Delivery) (webhookdelivery.Response, error)
}

// webhookEndpointReader is the slice of the endpoint repository blocks
// need: the key's active revision and secret at evaluation time, and
// which keys exist or are active at management time.
type webhookEndpointReader interface {
	application.WebhookEndpointResolver
	GetActiveWebhookEndpointRevision(context.Context, application.GetActiveWebhookEndpointRevisionInput) (domain.WebhookEndpointRevision, error)
	OpenWebhookSigningSecret(context.Context, application.OpenWebhookSigningSecretInput) (string, error)
}

// WebhookRuntime is what the composition root hands the registry so
// webhook blocks can execute: the endpoint reader (which opens sealed
// secrets), the delivery client, and the policy the client enforces.
// Backoff is overridable for tests; nil means the exponential default.
type WebhookRuntime struct {
	Endpoints webhookEndpointReader
	Deliverer WebhookDeliverer
	Backoff   func(attempt int) time.Duration
}

// WithWebhooks enables webhook block execution. Without it the type is
// still known (stored revisions decode and list) but cannot compile, so a
// revision containing one can neither be created nor activated.
func WithWebhooks(runtime WebhookRuntime) BlockRegistryOption {
	return func(registry *BlockRegistry) { registry.webhooks = &runtime }
}

// webhookBackoff waits 1s, 2s, 4s between attempts, capped at 10s.
func webhookBackoff(attempt int) time.Duration {
	wait := time.Second << (attempt - 1)
	if wait > 10*time.Second {
		wait = 10 * time.Second
	}
	return wait
}

// CompiledWebhookBlock executes one webhook block through the delivery
// client. It resolves the endpoint key's active revision at evaluation
// time, so activating a new endpoint revision applies to the next request
// without a pipeline revision.
type CompiledWebhookBlock struct {
	definition   approvalblock.Definition
	config       approvalblock.WebhookConfig
	timeout      time.Duration
	runtime      WebhookRuntime
	entitlements application.Entitlements
	clock        application.Clock
}

// CompileWebhookBlock validates the definition and binds it to the
// runtime. A nil runtime means the deployment has no secrets key.
func CompileWebhookBlock(definition approvalblock.Definition, runtime *WebhookRuntime, entitlements application.Entitlements, clock application.Clock) (*CompiledWebhookBlock, error) {
	if err := definition.Validate(); err != nil {
		return nil, fmt.Errorf("validate webhook block: %w", err)
	}
	if definition.Type != approvalblock.TypeWebhook || definition.Webhook == nil {
		return nil, errors.New("validate webhook block: definition is not a webhook block")
	}
	if runtime == nil || runtime.Endpoints == nil || runtime.Deliverer == nil {
		return nil, domain.ErrWebhookUnconfigured
	}
	cloned, err := cloneDefinition(definition)
	if err != nil {
		return nil, fmt.Errorf("clone webhook block definition: %w", err)
	}
	timeout, _ := time.ParseDuration(definition.Timeout)
	if clock == nil {
		clock = application.RealClock{}
	}
	bound := *runtime
	if bound.Backoff == nil {
		bound.Backoff = webhookBackoff
	}
	return &CompiledWebhookBlock{
		definition: cloned, config: *cloned.Webhook, timeout: timeout,
		runtime: bound, entitlements: entitlements, clock: clock,
	}, nil
}

func (block *CompiledWebhookBlock) Definition() approvalblock.Definition {
	definition, err := cloneDefinition(block.definition)
	if err != nil {
		panic("validated webhook block definition could not be cloned")
	}
	return definition
}

// webhookAttempt is the classified result of one delivery attempt.
type webhookAttempt struct {
	answer    webhookdelivery.Answer
	status    int
	duration  time.Duration
	code      BlockErrorCode
	retryable bool
	err       error
}

func (block *CompiledWebhookBlock) Evaluate(ctx context.Context, input RequestApprovalBlockInput) (RequestApprovalBlockResult, error) {
	if !block.definition.Enabled {
		return RequestApprovalBlockResult{Outcome: BlockNext}, nil
	}
	if err := input.Budget.Consume(approvalblock.TypeWebhook, approvalblock.MaximumWebhookBlocksPerRequest); err != nil {
		return RequestApprovalBlockResult{}, executionError(ErrorResourceLimit, err)
	}
	if ctx == nil {
		return RequestApprovalBlockResult{}, executionError(ErrorInvalidInput, errors.New("context is required"))
	}
	if input.Request.Tool == "" || input.Agent.Slug == "" || input.Agent.Name == "" {
		return RequestApprovalBlockResult{}, executionError(ErrorInvalidInput, errors.New("request tool and canonical agent fields are required"))
	}
	if size := block.inputSize(input); size > approvalblock.MaximumRequestInputBytes {
		return RequestApprovalBlockResult{}, executionError(ErrorInvalidInput, fmt.Errorf("request input exceeds %d bytes", approvalblock.MaximumRequestInputBytes))
	}

	// A key with no active revision (archived, or never activated) is an
	// unavailable endpoint: the block falls closed to human.
	endpoint, err := block.runtime.Endpoints.GetActiveWebhookEndpointRevision(ctx, application.GetActiveWebhookEndpointRevisionInput{
		OrganizationID: input.OrganizationID, EndpointKey: block.config.EndpointKey,
	})
	if err != nil {
		evidence := BlockEvidence{Delivery: &DeliveryEvidence{EndpointKey: block.config.EndpointKey}}
		return RequestApprovalBlockResult{Evidence: evidence}, executionError(ErrorEndpointUnavailable, err)
	}
	evidence := BlockEvidence{Delivery: &DeliveryEvidence{EndpointKey: endpoint.EndpointKey, EndpointRevision: endpoint.Revision, EndpointHost: hostOf(endpoint.URL)}}
	secret, err := block.runtime.Endpoints.OpenWebhookSigningSecret(ctx, application.OpenWebhookSigningSecretInput{
		OrganizationID: input.OrganizationID, EndpointKey: endpoint.EndpointKey,
	})
	if err != nil {
		return RequestApprovalBlockResult{Evidence: evidence}, executionError(ErrorEndpointUnavailable, err)
	}

	idempotencyKey := "preview:" + uuid.NewString()
	if !input.Preview && input.Request.ID != "" {
		idempotencyKey = string(input.Request.ID) + ":" + input.PipelineKey + ":" + block.definition.ID
	}
	var last webhookAttempt
	attempts := 0
	for attempts < block.definition.MaxAttempts {
		attempts++
		body, err := block.payload(input, idempotencyKey, attempts)
		if err != nil {
			return RequestApprovalBlockResult{Evidence: evidence, Attempts: attempts}, executionError(ErrorInvalidInput, err)
		}
		last = block.deliver(ctx, endpoint.URL, secret, body, idempotencyKey)
		block.recordUsage(ctx, input.OrganizationID, deliveryIDOf(body))
		evidence.Delivery.HTTPStatus = last.status
		evidence.Delivery.DurationMS = last.duration.Milliseconds()
		if last.err == nil || !last.retryable || attempts == block.definition.MaxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return RequestApprovalBlockResult{Evidence: evidence, Attempts: attempts}, executionError(ErrorTimeout, ctx.Err())
		case <-time.After(block.runtime.Backoff(attempts)):
		}
	}
	if last.err != nil {
		// The audit trail keeps only the safe error code; the transport or
		// status detail goes to the operator log. It never contains request
		// values or the secret.
		slog.WarnContext(ctx, fmt.Sprintf("approval webhook delivery failed - %s after %d attempts", endpoint.EndpointKey, attempts),
			"block_key", block.definition.ID, "endpoint_key", endpoint.EndpointKey, "endpoint_revision", endpoint.Revision, "endpoint_host", evidence.Delivery.EndpointHost,
			"attempts", attempts, "http_status", last.status, "error", last.err)
		return RequestApprovalBlockResult{Evidence: evidence, Attempts: attempts}, executionError(last.code, last.err)
	}
	evidence.Delivery.Reason = last.answer.Reason
	if !block.config.Allows(last.answer.Outcome) {
		return RequestApprovalBlockResult{Evidence: evidence, Attempts: attempts},
			executionError(ErrorOutcomeNotAllowed, fmt.Errorf("endpoint answered %q, which the block does not allow", last.answer.Outcome))
	}
	return RequestApprovalBlockResult{
		Outcome:  blockOutcome(last.answer.Outcome),
		Reason:   block.config.Reason,
		Metadata: last.answer.Metadata,
		Evidence: evidence,
		Attempts: attempts,
	}, nil
}

func (block *CompiledWebhookBlock) inputSize(input RequestApprovalBlockInput) int {
	size := len(input.Request.Tool) + len(input.Request.Arguments) + len(input.Agent.Slug) + len(input.Agent.Name)
	if block.config.Include.AgentReasoning {
		size += len(input.Request.AgentReasoning)
	}
	if block.config.Include.Context {
		size += len(input.Request.Context)
	}
	return size
}

// payload renders the delivery body for one attempt. Arguments are sent
// verbatim; the include flags gate the untrusted and unmodeled fields.
func (block *CompiledWebhookBlock) payload(input RequestApprovalBlockInput, idempotencyKey string, attempt int) ([]byte, error) {
	if len(input.Request.Arguments) == 0 || !json.Valid(input.Request.Arguments) {
		return nil, errors.New("request arguments must be valid JSON")
	}
	payload := webhookdelivery.Payload{
		Version: webhookdelivery.PayloadVersion, Type: webhookdelivery.TypeBlockDelivery, Preview: input.Preview,
		DeliveryID: uuid.NewString(), IdempotencyKey: idempotencyKey, OrganizationID: string(input.OrganizationID),
		Block:    &webhookdelivery.PayloadBlock{ID: block.definition.ID, Name: block.definition.Name, Attempt: attempt, MaxAttempts: block.definition.MaxAttempts},
		Pipeline: &webhookdelivery.PayloadPipeline{EvaluationID: string(input.EvaluationID)},
		Request:  webhookdelivery.PayloadRequest{ID: string(input.Request.ID), Tool: input.Request.Tool, Server: input.Request.Server, Arguments: input.Request.Arguments},
		Agent:    webhookdelivery.PayloadAgent{Slug: input.Agent.Slug, Name: input.Agent.Name},
	}
	if input.Preview {
		payload.Pipeline.EvaluationID = "preview"
	}
	if !input.Request.CreatedAt.IsZero() {
		created := input.Request.CreatedAt.UTC()
		payload.Request.CreatedAt = &created
	}
	if !input.Request.DeadlineAt.IsZero() {
		deadline := input.Request.DeadlineAt.UTC()
		payload.Request.DeadlineAt = &deadline
	}
	if block.config.Include.AgentReasoning {
		reasoning := input.Request.AgentReasoning
		payload.Request.AgentReasoning = &reasoning
	}
	if block.config.Include.Context && len(input.Request.Context) > 0 {
		if !json.Valid(input.Request.Context) {
			return nil, errors.New("request context must be valid JSON")
		}
		payload.Request.Context = input.Request.Context
	}
	if block.config.Include.PreviousMetadata && len(input.PreviousMetadata) > 0 {
		payload.PreviousMetadata = webhookdelivery.NestMetadata(input.PreviousMetadata)
	}
	return json.Marshal(payload)
}

// deliver runs one attempt and classifies it against the status and error
// mapping: transport failures, timeouts, 408, 425, 429, and 5xx retry;
// every other non-2xx answer and every malformed answer does not.
func (block *CompiledWebhookBlock) deliver(ctx context.Context, endpointURL, secret string, body []byte, idempotencyKey string) webhookAttempt {
	response, err := block.runtime.Deliverer.Deliver(ctx, webhookdelivery.Delivery{
		URL: endpointURL, Secret: secret, Body: body, Timeout: block.timeout,
		DeliveryID: deliveryIDOf(body), IdempotencyKey: idempotencyKey,
	})
	attempt := webhookAttempt{status: response.StatusCode, duration: response.Duration}
	switch {
	case errors.Is(err, webhookdelivery.ErrDestinationBlocked):
		attempt.code, attempt.err = ErrorEndpointUnavailable, err
	case errors.Is(err, webhookdelivery.ErrResponseTooLarge):
		attempt.code, attempt.err = ErrorInvalidBlockOutput, err
	case webhookdelivery.IsTimeout(err):
		attempt.code, attempt.err, attempt.retryable = ErrorTimeout, err, true
	case err != nil:
		attempt.code, attempt.err, attempt.retryable = ErrorEndpointUnreachable, err, true
	case response.StatusCode == http.StatusRequestTimeout, response.StatusCode == http.StatusTooEarly,
		response.StatusCode == http.StatusTooManyRequests, response.StatusCode >= 500:
		attempt.code, attempt.err, attempt.retryable = ErrorEndpointError, fmt.Errorf("endpoint answered %d", response.StatusCode), true
	case response.StatusCode < 200 || response.StatusCode >= 300:
		attempt.code, attempt.err = ErrorEndpointRejected, fmt.Errorf("endpoint answered %d", response.StatusCode)
	default:
		answer, parseErr := webhookdelivery.ParseAnswer(response.Body)
		if parseErr != nil {
			attempt.code, attempt.err = ErrorInvalidBlockOutput, parseErr
			return attempt
		}
		attempt.answer = answer
	}
	return attempt
}

// recordUsage meters every delivery attempt, keyed by the attempt's
// delivery ID so a retried resume never double counts. Metering is
// lossy-tolerant and never changes the block's result.
func (block *CompiledWebhookBlock) recordUsage(ctx context.Context, organizationID domain.ID, deliveryID string) {
	if block.entitlements == nil || organizationID == "" {
		return
	}
	if err := block.entitlements.RecordUsage(ctx, application.RecordUsageInput{
		OrganizationID: organizationID, Dimension: application.DimensionWebhookDeliveries, Amount: 1,
		IdempotencyKey: "webhook_delivery:" + deliveryID, Now: block.clock.Now(),
	}); err != nil {
		slog.WarnContext(ctx, "approval webhook delivery usage could not be recorded", "dimension", application.DimensionWebhookDeliveries, "error", err)
	}
}

func hostOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Host
}

// deliveryIDOf reads the delivery ID back out of the encoded payload so
// the header and the body agree without threading a second value.
func deliveryIDOf(body []byte) string {
	var envelope struct {
		DeliveryID string `json:"delivery_id"`
	}
	_ = json.Unmarshal(body, &envelope)
	return envelope.DeliveryID
}
