// Package webhookendpoints owns the management surface for organization
// webhook endpoints: revisioned destinations (a stable key with
// append-only name and URL revisions, at most one active) that pipeline
// webhook blocks reference by key and event subscriptions will later. The
// signing secret belongs to the key: it is minted with the key's first
// revision, shown once then and at rotation, and never read back through
// this surface.
package webhookendpoints

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/approvalblock"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/authorization"
	"github.com/withHumanAI/withHuman/internal/secrets"
	"github.com/withHumanAI/withHuman/internal/webhookdelivery"
	"github.com/withHumanAI/withHuman/telemetry"
)

const (
	secretPrefix        = "whsec_"
	secretBytes         = 32
	testDeliveryTimeout = 10 * time.Second
)

// Deliverer posts one signed delivery under the egress policy.
type Deliverer interface {
	Deliver(context.Context, webhookdelivery.Delivery) (webhookdelivery.Response, error)
}

type Service struct {
	endpoints application.WebhookEndpointRepository
	sealer    *secrets.Sealer
	policy    webhookdelivery.Policy
	deliverer Deliverer
	clock     application.Clock
	logger    *slog.Logger
}

// NewService composes the surface. A nil sealer means the deployment has no
// secrets key: the service still exists so the page can say the surface
// is absent, and every write answers ErrWebhookUnconfigured.
func NewService(endpoints application.WebhookEndpointRepository, sealer *secrets.Sealer, policy webhookdelivery.Policy, deliverer Deliverer, clock application.Clock, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{endpoints: endpoints, sealer: sealer, policy: policy, deliverer: deliverer, clock: clock, logger: logger}
}

// Configured reports whether endpoints can be stored and delivered to.
func (s *Service) Configured() bool { return s.sealer != nil && s.deliverer != nil }

// CreatedRevision carries the one-time secret next to the stored revision.
// SigningSecret is set only when the revision is the key's first, which
// is when the secret is minted.
type CreatedRevision struct {
	Revision      domain.WebhookEndpointRevision
	SigningSecret string
}

// RotatedSecret is the new secret, shown once, with when it was minted.
type RotatedSecret struct {
	EndpointKey   string
	SigningSecret string
	RotatedAt     time.Time
}

// TestResult reports one synthetic delivery. Problem is a stable code
// (empty when the endpoint answered a valid document); Message is safe
// text for the administrator, never a response body.
type TestResult struct {
	StatusCode int
	DurationMS int64
	Outcome    approvalblock.Outcome
	Reason     string
	Problem    string
	Message    string
}

func requireManagement(principal domain.Principal) error {
	if !domain.ManagementPrincipal(principal) {
		return domain.ErrForbidden
	}
	return nil
}

func (s *Service) requireManager(principal domain.Principal) error {
	return s.require(principal, domain.PermissionWebhookWrite, domain.OrganizationResource())
}

// requireReader gates every read, history included. A pipeline author
// picking an endpoint for a block needs the keys and names, so the roles
// that hold pipeline.write must hold webhook.read as well.
func (s *Service) requireReader(principal domain.Principal) error {
	return s.require(principal, domain.PermissionWebhookRead, domain.OrganizationResource())
}

func (s *Service) require(principal domain.Principal, permission domain.Permission, resource domain.Resource) error {
	if err := requireManagement(principal); err != nil {
		return err
	}
	return authorization.Check(principal, permission, resource)
}

func (s *Service) requireConfigured() error {
	if !s.Configured() {
		return domain.ErrWebhookUnconfigured
	}
	return nil
}

func validateEndpointKey(endpointKey string) error {
	if !approvalblock.ValidWebhookEndpointKey(endpointKey) {
		return fmt.Errorf("endpoint_key must match ^[a-z0-9][a-z0-9._-]{0,62}$: %w", domain.ErrInvalid)
	}
	return nil
}

func (s *Service) validateURL(raw string) error {
	if _, err := s.policy.ValidateURL(raw); err != nil {
		return fmt.Errorf("%s: %w", err.Error(), domain.ErrInvalid)
	}
	return nil
}

func newSigningSecret() (string, error) {
	raw := make([]byte, secretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return secretPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// Listed pairs one key's summary with the active pipeline revisions
// posting to it, so the list can say which endpoints can be archived.
type Listed struct {
	Summary domain.WebhookEndpointSummary
	Uses    []domain.PipelineUse
}

func (s *Service) ListEndpoints(ctx context.Context, principal domain.Principal, limit int) ([]Listed, error) {
	if err := s.requireReader(principal); err != nil {
		return nil, err
	}
	summaries, err := s.endpoints.ListWebhookEndpointSummaries(ctx, application.ListWebhookEndpointSummariesInput{OrganizationID: principal.OrganizationID, Limit: limit})
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(summaries))
	for index, summary := range summaries {
		keys[index] = summary.EndpointKey
	}
	uses, err := s.endpoints.ListWebhookEndpointUses(ctx, application.ListWebhookEndpointUsesInput{OrganizationID: principal.OrganizationID, EndpointKeys: keys})
	if err != nil {
		return nil, err
	}
	listed := make([]Listed, len(summaries))
	for index, summary := range summaries {
		listed[index] = Listed{Summary: summary, Uses: uses[summary.EndpointKey]}
	}
	return listed, nil
}

func (s *Service) GetActiveEndpoint(ctx context.Context, principal domain.Principal, endpointKey string) (domain.WebhookEndpointRevision, error) {
	if err := s.requireReader(principal); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	if err := validateEndpointKey(endpointKey); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	return s.endpoints.GetActiveWebhookEndpointRevision(ctx, application.GetActiveWebhookEndpointRevisionInput{OrganizationID: principal.OrganizationID, EndpointKey: endpointKey})
}

// ListRevisions returns one page of the key's history, newest first;
// cursor is the previous page's NextCursor, empty for the first page.
func (s *Service) ListRevisions(ctx context.Context, principal domain.Principal, endpointKey string, limit int, cursor string) (application.RevisionPage[domain.WebhookEndpointRevision], error) {
	var page application.RevisionPage[domain.WebhookEndpointRevision]
	if err := s.requireReader(principal); err != nil {
		return page, err
	}
	if err := validateEndpointKey(endpointKey); err != nil {
		return page, err
	}
	return s.endpoints.ListWebhookEndpointRevisions(ctx, application.ListWebhookEndpointRevisionsInput{OrganizationID: principal.OrganizationID, EndpointKey: endpointKey, Limit: limit, Cursor: cursor})
}

func (s *Service) GetRevision(ctx context.Context, principal domain.Principal, endpointKey string, revision int64) (domain.WebhookEndpointRevision, error) {
	if err := s.requireReader(principal); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	if err := validateEndpointKey(endpointKey); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	return s.endpoints.GetWebhookEndpointRevision(ctx, application.GetWebhookEndpointRevisionInput{OrganizationID: principal.OrganizationID, EndpointKey: endpointKey, Revision: revision})
}

// CreateRevision appends the next revision of the key, a draft. A fresh
// secret is sealed for every call; the store keeps it only when the key
// has none yet, and that is the one time the secret is shown.
func (s *Service) CreateRevision(ctx context.Context, principal domain.Principal, endpointKey, name, url string) (CreatedRevision, error) {
	if err := s.requireManager(principal); err != nil {
		return CreatedRevision{}, err
	}
	if err := s.requireConfigured(); err != nil {
		return CreatedRevision{}, err
	}
	if err := validateEndpointKey(endpointKey); err != nil {
		return CreatedRevision{}, err
	}
	if err := s.validateURL(url); err != nil {
		return CreatedRevision{}, err
	}
	secret, err := newSigningSecret()
	if err != nil {
		return CreatedRevision{}, err
	}
	sealed, err := s.sealer.Seal(application.WebhookSigningSecretLabel(principal.OrganizationID, endpointKey), secret)
	if err != nil {
		return CreatedRevision{}, err
	}
	created, err := s.endpoints.CreateWebhookEndpointRevision(ctx, application.CreateWebhookEndpointRevisionInput{
		ID: domain.ID(uuid.NewString()), OrganizationID: principal.OrganizationID, EndpointKey: endpointKey,
		Name: strings.TrimSpace(name), URL: strings.TrimSpace(url), SealedSigningSecret: sealed,
		CreatedByActorID: principal.ManagementActorID(), Now: s.clock.Now(),
	})
	if err != nil {
		return CreatedRevision{}, err
	}
	result := CreatedRevision{Revision: created.Revision}
	if created.SecretCreated {
		result.SigningSecret = secret
	}
	return result, nil
}

// UpdateRevision replaces a draft revision's name and URL in place; the
// revision keeps its number and the key keeps its secret. A revision that
// was ever active refuses with domain.ErrRevisionNotDraft.
func (s *Service) UpdateRevision(ctx context.Context, principal domain.Principal, endpointKey string, revision int64, name, url string) (domain.WebhookEndpointRevision, error) {
	if err := s.requireManager(principal); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	if err := s.requireConfigured(); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	if err := validateEndpointKey(endpointKey); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	if err := s.validateURL(url); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	return s.endpoints.UpdateWebhookEndpointRevision(ctx, application.UpdateWebhookEndpointRevisionInput{
		OrganizationID: principal.OrganizationID, EndpointKey: endpointKey, Revision: revision,
		Name: strings.TrimSpace(name), URL: strings.TrimSpace(url),
		ActorID: principal.ManagementActorID(), Now: s.clock.Now(),
	})
}

// ActivateRevision switches the key's active revision, or rolls back to
// an earlier one. Deliveries from the next request on use it.
func (s *Service) ActivateRevision(ctx context.Context, principal domain.Principal, endpointKey string, revision int64, expectedActiveRevision *int64) (domain.WebhookEndpointRevision, error) {
	if err := s.requireManager(principal); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	if err := s.requireConfigured(); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	if err := validateEndpointKey(endpointKey); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	return s.endpoints.ActivateWebhookEndpointRevision(ctx, application.ActivateWebhookEndpointRevisionInput{
		OrganizationID: principal.OrganizationID, EndpointKey: endpointKey, Revision: revision,
		ExpectedActiveRevision: expectedActiveRevision, ActorID: principal.ManagementActorID(), Now: s.clock.Now(),
	})
}

// ListUses reports the active pipeline revisions whose webhook blocks
// post to the key. The same set refuses Deactivate, so the page can
// explain a refusal before the click.
func (s *Service) ListUses(ctx context.Context, principal domain.Principal, endpointKey string) ([]domain.PipelineUse, error) {
	if err := s.requireReader(principal); err != nil {
		return nil, err
	}
	if err := validateEndpointKey(endpointKey); err != nil {
		return nil, err
	}
	uses, err := s.endpoints.ListWebhookEndpointUses(ctx, application.ListWebhookEndpointUsesInput{OrganizationID: principal.OrganizationID, EndpointKeys: []string{endpointKey}})
	if err != nil {
		return nil, err
	}
	return uses[endpointKey], nil
}

// Deactivate archives the key by deactivating its current revision.
func (s *Service) Deactivate(ctx context.Context, principal domain.Principal, endpointKey string, expectedActiveRevision int64) error {
	if err := s.requireManager(principal); err != nil {
		return err
	}
	if err := validateEndpointKey(endpointKey); err != nil {
		return err
	}
	_, err := s.endpoints.DeactivateWebhookEndpoint(ctx, application.DeactivateWebhookEndpointInput{
		OrganizationID: principal.OrganizationID, EndpointKey: endpointKey, ExpectedActiveRevision: expectedActiveRevision,
		ActorID: principal.ManagementActorID(), Now: s.clock.Now(),
	})
	return err
}

// RotateSecret replaces the key's secret immediately. Every revision and
// every use of the key switches at once; overlapping secrets are deferred.
func (s *Service) RotateSecret(ctx context.Context, principal domain.Principal, endpointKey string) (RotatedSecret, error) {
	if err := s.requireManager(principal); err != nil {
		return RotatedSecret{}, err
	}
	if err := s.requireConfigured(); err != nil {
		return RotatedSecret{}, err
	}
	if err := validateEndpointKey(endpointKey); err != nil {
		return RotatedSecret{}, err
	}
	secret, err := newSigningSecret()
	if err != nil {
		return RotatedSecret{}, err
	}
	sealed, err := s.sealer.Seal(application.WebhookSigningSecretLabel(principal.OrganizationID, endpointKey), secret)
	if err != nil {
		return RotatedSecret{}, err
	}
	state, err := s.endpoints.RotateWebhookSigningSecret(ctx, application.RotateWebhookSigningSecretInput{
		OrganizationID: principal.OrganizationID, EndpointKey: endpointKey, SealedSigningSecret: sealed,
		ActorID: principal.ManagementActorID(), Now: s.clock.Now(),
	})
	if err != nil {
		return RotatedSecret{}, err
	}
	return RotatedSecret{EndpointKey: state.EndpointKey, SigningSecret: secret, RotatedAt: state.RotatedAt}, nil
}

// Test sends one signed synthetic delivery to one revision's URL now, so
// an administrator can verify connectivity and signature handling before
// activating it and without a real request. The payload is flagged as a
// preview and carries no request state.
func (s *Service) Test(ctx context.Context, principal domain.Principal, endpointKey string, revision int64) (TestResult, error) {
	if err := s.requireManager(principal); err != nil {
		return TestResult{}, err
	}
	if err := s.requireConfigured(); err != nil {
		return TestResult{}, err
	}
	if err := validateEndpointKey(endpointKey); err != nil {
		return TestResult{}, err
	}
	target, err := s.endpoints.GetWebhookEndpointRevision(ctx, application.GetWebhookEndpointRevisionInput{OrganizationID: principal.OrganizationID, EndpointKey: endpointKey, Revision: revision})
	if err != nil {
		return TestResult{}, err
	}
	secret, err := s.endpoints.OpenWebhookSigningSecret(ctx, application.OpenWebhookSigningSecretInput{OrganizationID: principal.OrganizationID, EndpointKey: endpointKey})
	if err != nil {
		return TestResult{}, err
	}
	deliveryID := uuid.NewString()
	body, err := json.Marshal(webhookdelivery.Payload{
		Version: webhookdelivery.PayloadVersion, Type: webhookdelivery.TypeEndpointTest, Preview: true,
		DeliveryID: deliveryID, IdempotencyKey: "test:" + deliveryID, OrganizationID: string(principal.OrganizationID),
		Request: webhookdelivery.PayloadRequest{Tool: "withhuman.webhook_test", Arguments: json.RawMessage(`{"test":true}`)},
		Agent:   webhookdelivery.PayloadAgent{Slug: "test-agent", Name: "Test agent"},
	})
	if err != nil {
		return TestResult{}, err
	}
	response, deliverErr := s.deliverer.Deliver(ctx, webhookdelivery.Delivery{
		Purpose: telemetry.WebhookEndpointTest,
		URL:     target.URL, Secret: secret, Body: body, Timeout: testDeliveryTimeout,
		DeliveryID: deliveryID, IdempotencyKey: "test:" + deliveryID,
	})
	result := classifyTest(response, deliverErr)
	if deliverErr != nil {
		s.logger.WarnContext(ctx, "webhook endpoint test failed - "+result.Problem, "organization_id", string(principal.OrganizationID), "endpoint_key", endpointKey, "endpoint_revision", revision, "error", deliverErr)
	}
	record, _ := json.Marshal(map[string]any{
		"endpoint_key": endpointKey, "revision": revision,
		"http_status": result.StatusCode, "duration_ms": result.DurationMS, "problem": result.Problem,
	})
	if err := s.endpoints.RecordWebhookEndpointTest(ctx, application.RecordWebhookEndpointTestInput{
		OrganizationID: principal.OrganizationID, RevisionID: target.ID,
		ActorID: principal.ManagementActorID(), Data: record, Now: s.clock.Now(),
	}); err != nil {
		s.logger.WarnContext(ctx, "webhook endpoint test audit failed", "endpoint_key", endpointKey, "error", err)
	}
	return result, nil
}

func classifyTest(response webhookdelivery.Response, err error) TestResult {
	result := TestResult{StatusCode: response.StatusCode, DurationMS: response.Duration.Milliseconds()}
	switch {
	case errors.Is(err, webhookdelivery.ErrDestinationBlocked):
		result.Problem, result.Message = "blocked", "The endpoint address is outside the egress policy."
	case errors.Is(err, webhookdelivery.ErrResponseTooLarge):
		result.Problem, result.Message = "response_too_large", "The endpoint answered with more than 64 KiB."
	case webhookdelivery.IsTimeout(err):
		result.Problem, result.Message = "timeout", "The endpoint did not answer within 10 seconds."
	case err != nil:
		result.Problem, result.Message = "unreachable", "The endpoint could not be reached."
	case response.StatusCode < 200 || response.StatusCode >= 300:
		result.Problem, result.Message = "status", fmt.Sprintf("The endpoint answered %d %s.", response.StatusCode, http.StatusText(response.StatusCode))
	default:
		answer, parseErr := webhookdelivery.ParseAnswer(response.Body)
		if parseErr != nil {
			result.Problem, result.Message = "invalid_answer", "The endpoint answered, but not with a valid outcome document."
			return result
		}
		result.Outcome, result.Reason = answer.Outcome, answer.Reason
	}
	return result
}
