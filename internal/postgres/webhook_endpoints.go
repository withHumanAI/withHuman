package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
)

const webhookEndpointSelect = `SELECT id::text, organization_id::text, endpoint_key, revision, is_active,
	name, url, created_by_actor_id::text, created_at
	FROM webhook_endpoint_revisions `

func scanWebhookEndpointRevision(row rowScanner) (domain.WebhookEndpointRevision, error) {
	var revision domain.WebhookEndpointRevision
	var id, organizationID, membershipID string
	err := row.Scan(&id, &organizationID, &revision.EndpointKey, &revision.Revision, &revision.IsActive,
		&revision.Name, &revision.URL, &membershipID, &revision.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.WebhookEndpointRevision{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	revision.ID = domain.ID(id)
	revision.OrganizationID = domain.ID(organizationID)
	revision.CreatedByActorID = domain.ID(membershipID)
	return revision, nil
}

// webhookURLHost is the only part of an endpoint URL that reaches audit
// data: a path may carry a token the organization considers secret.
func webhookURLHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Host
}

// CreateWebhookEndpointRevision appends the next revision of the key. The
// first revision of a key also files the key's signing secret; later ones
// leave the secret alone and report SecretCreated false.
func (s *Store) CreateWebhookEndpointRevision(ctx context.Context, input application.CreateWebhookEndpointRevisionInput) (application.CreatedWebhookEndpointRevision, error) {
	if err := input.Validate(); err != nil {
		return application.CreatedWebhookEndpointRevision{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return application.CreatedWebhookEndpointRevision{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockWebhookEndpoint(ctx, tx, input.OrganizationID, input.EndpointKey); err != nil {
		return application.CreatedWebhookEndpointRevision{}, err
	}
	actorID, err := managementActorID(ctx, tx, input.OrganizationID, input.CreatedByActorID)
	if err != nil {
		return application.CreatedWebhookEndpointRevision{}, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO webhook_endpoint_secrets
		(organization_id, endpoint_key, sealed_signing_secret, rotated_at, rotated_by_actor_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (organization_id, endpoint_key) DO NOTHING`,
		input.OrganizationID, input.EndpointKey, input.SealedSigningSecret, input.Now, input.CreatedByActorID)
	if err != nil {
		return application.CreatedWebhookEndpointRevision{}, err
	}
	secretCreated := tag.RowsAffected() == 1
	var revisionNumber int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(revision), 0) + 1
		FROM webhook_endpoint_revisions WHERE organization_id = $1 AND endpoint_key = $2`,
		input.OrganizationID, input.EndpointKey).Scan(&revisionNumber); err != nil {
		return application.CreatedWebhookEndpointRevision{}, err
	}
	revision := domain.WebhookEndpointRevision{
		ID: input.ID, OrganizationID: input.OrganizationID, EndpointKey: input.EndpointKey,
		Revision: revisionNumber, IsActive: false, Name: input.Name, URL: input.URL,
		CreatedByActorID: input.CreatedByActorID, CreatedAt: input.Now,
	}
	if _, err := tx.Exec(ctx, `INSERT INTO webhook_endpoint_revisions
		(id, organization_id, endpoint_key, revision, is_active, name, url, created_by_actor_id, created_at)
		VALUES ($1, $2, $3, $4, false, $5, $6, $7, $8)`, revision.ID, revision.OrganizationID,
		revision.EndpointKey, revision.Revision, revision.Name, revision.URL,
		revision.CreatedByActorID, revision.CreatedAt); err != nil {
		return application.CreatedWebhookEndpointRevision{}, err
	}
	data, _ := json.Marshal(map[string]any{
		"endpoint_key": revision.EndpointKey, "revision": revision.Revision,
		"name": revision.Name, "url_host": webhookURLHost(revision.URL), "secret_created": secretCreated,
	})
	if err := s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "webhook_endpoint.revision_created",
		ActorType: domain.ActorHuman, ActorID: actorID,
		SubjectType: "webhook_endpoint", SubjectID: revision.ID,
		Data: data, OccurredAt: input.Now,
	}); err != nil {
		return application.CreatedWebhookEndpointRevision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return application.CreatedWebhookEndpointRevision{}, err
	}
	return application.CreatedWebhookEndpointRevision{Revision: revision, SecretCreated: secretCreated}, nil
}

func (s *Store) ListWebhookEndpointSummaries(ctx context.Context, input application.ListWebhookEndpointSummariesInput) ([]domain.WebhookEndpointSummary, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `SELECT r.endpoint_key,
		(array_agg(r.name ORDER BY r.revision DESC))[1],
		(array_agg(r.url ORDER BY r.revision DESC))[1],
		max(r.revision) FILTER (WHERE r.is_active), s.archived_at, max(r.revision), count(*), max(r.created_at), s.rotated_at
		FROM webhook_endpoint_revisions r
		JOIN webhook_endpoint_secrets s ON s.organization_id = r.organization_id AND s.endpoint_key = r.endpoint_key
		WHERE r.organization_id = $1
		GROUP BY r.endpoint_key, s.archived_at, s.rotated_at
		ORDER BY r.endpoint_key
		LIMIT $2`, input.OrganizationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	summaries := make([]domain.WebhookEndpointSummary, 0)
	for rows.Next() {
		var summary domain.WebhookEndpointSummary
		if err := rows.Scan(&summary.EndpointKey, &summary.Name, &summary.URL, &summary.ActiveRevision, &summary.ArchivedAt,
			&summary.LatestRevision, &summary.RevisionCount, &summary.LatestCreatedAt, &summary.SecretRotatedAt); err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

func (s *Store) GetWebhookEndpointRevision(ctx context.Context, input application.GetWebhookEndpointRevisionInput) (domain.WebhookEndpointRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	return scanWebhookEndpointRevision(s.pool.QueryRow(ctx, webhookEndpointSelect+
		`WHERE organization_id = $1 AND endpoint_key = $2 AND revision = $3`,
		input.OrganizationID, input.EndpointKey, input.Revision))
}

func (s *Store) ListWebhookEndpointRevisions(ctx context.Context, input application.ListWebhookEndpointRevisionsInput) (application.RevisionPage[domain.WebhookEndpointRevision], error) {
	var page application.RevisionPage[domain.WebhookEndpointRevision]
	if err := input.Validate(); err != nil {
		return page, err
	}
	limit := revisionPageLimit(input.Limit)
	before, _ := application.DecodeRevisionCursor(input.Cursor)
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM webhook_endpoint_revisions WHERE organization_id = $1 AND endpoint_key = $2`,
		input.OrganizationID, input.EndpointKey).Scan(&page.TotalCount); err != nil {
		return page, err
	}
	rows, err := s.pool.Query(ctx, webhookEndpointSelect+`WHERE organization_id = $1 AND endpoint_key = $2
		AND ($3 = 0 OR revision < $3)
		ORDER BY revision DESC LIMIT $4`, input.OrganizationID, input.EndpointKey, before, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	revisions := make([]domain.WebhookEndpointRevision, 0)
	for rows.Next() {
		revision, err := scanWebhookEndpointRevision(rows)
		if err != nil {
			return page, err
		}
		revisions = append(revisions, revision)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	page.Revisions, page.NextCursor = trimRevisionPage(revisions, limit, func(revision domain.WebhookEndpointRevision) int64 { return revision.Revision })
	return page, nil
}

func (s *Store) GetActiveWebhookEndpointRevision(ctx context.Context, input application.GetActiveWebhookEndpointRevisionInput) (domain.WebhookEndpointRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	return scanWebhookEndpointRevision(s.pool.QueryRow(ctx, webhookEndpointSelect+
		`WHERE organization_id = $1 AND endpoint_key = $2 AND is_active`, input.OrganizationID, input.EndpointKey))
}

func (s *Store) ActivateWebhookEndpointRevision(ctx context.Context, input application.ActivateWebhookEndpointRevisionInput) (domain.WebhookEndpointRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockWebhookEndpoint(ctx, tx, input.OrganizationID, input.EndpointKey); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	current, hasCurrent, err := activeWebhookEndpointRevision(ctx, tx, input.OrganizationID, input.EndpointKey)
	if err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	if !expectedRevisionMatches(current.Revision, hasCurrent, input.ExpectedActiveRevision) {
		return domain.WebhookEndpointRevision{}, fmt.Errorf("this webhook endpoint changed. Reload and try again: %w", domain.ErrPreconditionFailed)
	}
	target, err := scanWebhookEndpointRevision(tx.QueryRow(ctx, webhookEndpointSelect+
		`WHERE organization_id = $1 AND endpoint_key = $2 AND revision = $3`, input.OrganizationID,
		input.EndpointKey, input.Revision))
	if err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	if hasCurrent && current.ID == target.ID {
		if err := tx.Commit(ctx); err != nil {
			return domain.WebhookEndpointRevision{}, err
		}
		return target, nil
	}
	actorID, err := managementActorID(ctx, tx, input.OrganizationID, input.ActorID)
	if err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	if hasCurrent {
		if _, err := tx.Exec(ctx, `UPDATE webhook_endpoint_revisions SET is_active = false
			WHERE organization_id = $1 AND id = $2`, input.OrganizationID, current.ID); err != nil {
			return domain.WebhookEndpointRevision{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE webhook_endpoint_revisions SET is_active = true
		WHERE organization_id = $1 AND id = $2`, input.OrganizationID, target.ID); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	// An active revision ends any archive of the key.
	if _, err := tx.Exec(ctx, `UPDATE webhook_endpoint_secrets SET archived_at = NULL
		WHERE organization_id = $1 AND endpoint_key = $2 AND archived_at IS NOT NULL`, input.OrganizationID, target.EndpointKey); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	data, _ := json.Marshal(map[string]any{
		"endpoint_key":             target.EndpointKey,
		"revision":                 target.Revision,
		"url_host":                 webhookURLHost(target.URL),
		"previous_active_revision": revisionValue(current.Revision, hasCurrent),
	})
	if err := s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "webhook_endpoint.activated",
		ActorType: domain.ActorHuman, ActorID: actorID,
		SubjectType: "webhook_endpoint", SubjectID: target.ID,
		Data: data, OccurredAt: input.Now,
	}); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	target.IsActive = true
	return target, nil
}

// DeactivateWebhookEndpoint archives the key: its active revision is
// deactivated and the key stamped, so a key saved as a draft and never
// activated is not mistaken for an archived one. It is refused while any
// active pipeline revision's webhook block posts to it, so a live
// pipeline never loses its destination underneath it.
func (s *Store) DeactivateWebhookEndpoint(ctx context.Context, input application.DeactivateWebhookEndpointInput) (domain.WebhookEndpointRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockWebhookEndpoint(ctx, tx, input.OrganizationID, input.EndpointKey); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	current, hasCurrent, err := activeWebhookEndpointRevision(ctx, tx, input.OrganizationID, input.EndpointKey)
	if err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	if !hasCurrent || current.Revision != input.ExpectedActiveRevision {
		return domain.WebhookEndpointRevision{}, fmt.Errorf("this webhook endpoint changed. Reload and try again: %w", domain.ErrPreconditionFailed)
	}
	// Pipeline activation takes this key's lock before it verifies the
	// endpoint is active, so the uses read here cannot be stale.
	uses, err := listWebhookEndpointUses(ctx, tx, input.OrganizationID, input.EndpointKey)
	if err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	if len(uses) > 0 {
		return domain.WebhookEndpointRevision{}, domain.WebhookEndpointInUseError{EndpointKey: input.EndpointKey, Uses: uses}
	}
	actorID, err := managementActorID(ctx, tx, input.OrganizationID, input.ActorID)
	if err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE webhook_endpoint_revisions SET is_active = false
		WHERE organization_id = $1 AND id = $2`, input.OrganizationID, current.ID); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE webhook_endpoint_secrets SET archived_at = $3
		WHERE organization_id = $1 AND endpoint_key = $2`, input.OrganizationID, current.EndpointKey, input.Now); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	data, _ := json.Marshal(map[string]any{"endpoint_key": current.EndpointKey, "revision": current.Revision})
	if err := s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "webhook_endpoint.deactivated",
		ActorType: domain.ActorHuman, ActorID: actorID,
		SubjectType: "webhook_endpoint", SubjectID: current.ID,
		Data: data, OccurredAt: input.Now,
	}); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.WebhookEndpointRevision{}, err
	}
	current.IsActive = false
	return current, nil
}

func (s *Store) ListWebhookEndpointUses(ctx context.Context, input application.ListWebhookEndpointUsesInput) (map[string][]domain.PipelineUse, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	uses := make(map[string][]domain.PipelineUse)
	if len(input.EndpointKeys) == 0 {
		return uses, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT b.config->>'endpoint_key', p.id::text, p.pipeline_key, p.revision, b.block_key
		FROM request_approval_pipelines p
		JOIN request_approval_blocks b ON b.organization_id = p.organization_id AND b.id = ANY(p.block_ids)
		WHERE p.organization_id = $1 AND p.is_active
		  AND b.block_type = 'webhook' AND b.config->>'endpoint_key' = ANY($2)
		ORDER BY (p.pipeline_key <> $3), p.pipeline_key, p.revision, b.block_key`, input.OrganizationID, input.EndpointKeys, domain.EntryPipelineKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var endpointKey, pipelineID string
		var use domain.PipelineUse
		if err := rows.Scan(&endpointKey, &pipelineID, &use.PipelineKey, &use.Revision, &use.BlockKey); err != nil {
			return nil, err
		}
		use.PipelineID = domain.ID(pipelineID)
		uses[endpointKey] = append(uses[endpointKey], use)
	}
	return uses, rows.Err()
}

// listWebhookEndpointUses discovers references rather than storing them:
// a webhook block in an active pipeline revision names the endpoint key
// in its immutable config. Later uses (event subscriptions) add their
// own query here.
func listWebhookEndpointUses(ctx context.Context, q querier, organizationID domain.ID, endpointKey string) ([]domain.PipelineUse, error) {
	return scanPipelineUses(q.Query(ctx, `SELECT p.id::text, p.pipeline_key, p.revision, b.block_key
		FROM request_approval_pipelines p
		JOIN request_approval_blocks b ON b.organization_id = p.organization_id AND b.id = ANY(p.block_ids)
		WHERE p.organization_id = $1 AND p.is_active
		  AND b.block_type = 'webhook' AND b.config->>'endpoint_key' = $2
		ORDER BY (p.pipeline_key <> $3), p.pipeline_key, b.block_key`, organizationID, endpointKey, domain.EntryPipelineKey))
}

// scanPipelineUses reads (pipeline id, pipeline key, revision, block key)
// rows; an empty block key is a pipeline-level reference.
func scanPipelineUses(rows pgx.Rows, err error) ([]domain.PipelineUse, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	uses := make([]domain.PipelineUse, 0)
	for rows.Next() {
		var use domain.PipelineUse
		var pipelineID string
		if err := rows.Scan(&pipelineID, &use.PipelineKey, &use.Revision, &use.BlockKey); err != nil {
			return nil, err
		}
		use.PipelineID = domain.ID(pipelineID)
		uses = append(uses, use)
	}
	return uses, rows.Err()
}

// ResolveWebhookEndpoints reports which of the named keys have no
// revision at all and which exist without an active one.
func (s *Store) ResolveWebhookEndpoints(ctx context.Context, input application.ResolveWebhookEndpointsInput) (application.ResolvedWebhookEndpoints, error) {
	if err := input.Validate(); err != nil {
		return application.ResolvedWebhookEndpoints{}, err
	}
	result := application.ResolvedWebhookEndpoints{Missing: make([]string, 0), Inactive: make([]string, 0)}
	if len(input.EndpointKeys) == 0 {
		return result, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT endpoint_key, bool_or(is_active) FROM webhook_endpoint_revisions
		WHERE organization_id = $1 AND endpoint_key = ANY($2)
		GROUP BY endpoint_key`, input.OrganizationID, input.EndpointKeys)
	if err != nil {
		return application.ResolvedWebhookEndpoints{}, err
	}
	defer rows.Close()
	active := make(map[string]bool, len(input.EndpointKeys))
	for rows.Next() {
		var key string
		var isActive bool
		if err := rows.Scan(&key, &isActive); err != nil {
			return application.ResolvedWebhookEndpoints{}, err
		}
		active[key] = isActive
	}
	if err := rows.Err(); err != nil {
		return application.ResolvedWebhookEndpoints{}, err
	}
	for _, key := range input.EndpointKeys {
		isActive, exists := active[key]
		if !exists {
			result.Missing = append(result.Missing, key)
		} else if !isActive {
			result.Inactive = append(result.Inactive, key)
		}
	}
	return result, nil
}

// RotateWebhookSigningSecret replaces the key's secret in place. Every
// revision of the key, active or not, signs with it from now on.
func (s *Store) RotateWebhookSigningSecret(ctx context.Context, input application.RotateWebhookSigningSecretInput) (application.WebhookSigningSecretState, error) {
	if err := input.Validate(); err != nil {
		return application.WebhookSigningSecretState{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return application.WebhookSigningSecretState{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockWebhookEndpoint(ctx, tx, input.OrganizationID, input.EndpointKey); err != nil {
		return application.WebhookSigningSecretState{}, err
	}
	actorID, err := managementActorID(ctx, tx, input.OrganizationID, input.ActorID)
	if err != nil {
		return application.WebhookSigningSecretState{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE webhook_endpoint_secrets
		SET sealed_signing_secret = $3, rotated_at = $4, rotated_by_actor_id = $5
		WHERE organization_id = $1 AND endpoint_key = $2`,
		input.OrganizationID, input.EndpointKey, input.SealedSigningSecret, input.Now, input.ActorID)
	if err != nil {
		return application.WebhookSigningSecretState{}, err
	}
	if tag.RowsAffected() == 0 {
		return application.WebhookSigningSecretState{}, domain.ErrNotFound
	}
	// The audit subject is the key's latest revision: the secret has no
	// row identity of its own, and a key always has at least one revision.
	latest, err := scanWebhookEndpointRevision(tx.QueryRow(ctx, webhookEndpointSelect+
		`WHERE organization_id = $1 AND endpoint_key = $2 ORDER BY revision DESC LIMIT 1`,
		input.OrganizationID, input.EndpointKey))
	if err != nil {
		return application.WebhookSigningSecretState{}, err
	}
	data, _ := json.Marshal(map[string]any{"endpoint_key": input.EndpointKey, "name": latest.Name})
	if err := s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "webhook_endpoint.secret_rotated",
		ActorType: domain.ActorHuman, ActorID: actorID,
		SubjectType: "webhook_endpoint", SubjectID: latest.ID,
		Data: data, OccurredAt: input.Now,
	}); err != nil {
		return application.WebhookSigningSecretState{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return application.WebhookSigningSecretState{}, err
	}
	return application.WebhookSigningSecretState{EndpointKey: input.EndpointKey, RotatedAt: input.Now}, nil
}

// OpenWebhookSigningSecret hands back the unsealed secret. It is the one
// read of the sealed column, kept separate from the revision reads so
// the product surfaces never see it.
func (s *Store) OpenWebhookSigningSecret(ctx context.Context, input application.OpenWebhookSigningSecretInput) (string, error) {
	if err := input.Validate(); err != nil {
		return "", err
	}
	if s.sealer == nil {
		return "", domain.ErrWebhookUnconfigured
	}
	var sealed []byte
	err := s.pool.QueryRow(ctx, `SELECT sealed_signing_secret FROM webhook_endpoint_secrets WHERE organization_id = $1 AND endpoint_key = $2`,
		input.OrganizationID, input.EndpointKey).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return s.sealer.Open(application.WebhookSigningSecretLabel(input.OrganizationID, input.EndpointKey), sealed)
}

func (s *Store) RecordWebhookEndpointTest(ctx context.Context, input application.RecordWebhookEndpointTestInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	return s.appendManagementAuditEvent(ctx, s.pool, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "webhook_endpoint.tested",
		ActorType: domain.ActorHuman, ActorID: input.ActorID,
		SubjectType: "webhook_endpoint", SubjectID: input.RevisionID,
		Data: input.Data, OccurredAt: input.Now,
	})
}

func activeWebhookEndpointRevision(ctx context.Context, tx pgx.Tx, organizationID domain.ID, endpointKey string) (domain.WebhookEndpointRevision, bool, error) {
	revision, err := scanWebhookEndpointRevision(tx.QueryRow(ctx, webhookEndpointSelect+
		`WHERE organization_id = $1 AND endpoint_key = $2 AND is_active`, organizationID, endpointKey))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.WebhookEndpointRevision{}, false, nil
	}
	return revision, err == nil, err
}

// lockWebhookEndpoint must derive the same advisory lock key as the
// monotonic-revision trigger in 0026_webhook_endpoint_revisions.sql.
func lockWebhookEndpoint(ctx context.Context, tx pgx.Tx, organizationID domain.ID, endpointKey string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('webhook_endpoint:' || $1::text || ':' || $2::text, 0))`,
		organizationID, endpointKey)
	return err
}
