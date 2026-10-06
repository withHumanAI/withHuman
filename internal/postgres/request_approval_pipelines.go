package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
)

const requestApprovalPipelineSelect = `SELECT id::text, organization_id::text, pipeline_key, name, revision,
	is_active, block_ids::text[], COALESCE(default_escalation_path, ''), default_ask_agent_owner,
	created_by_actor_id::text, created_at, archived_at
	FROM request_approval_pipelines `

const requestApprovalBlockSelect = `SELECT b.id::text, b.organization_id::text, b.block_key, b.name, b.block_type,
	b.config_version, b.config, b.timeout_ms, b.max_attempts, b.content_hash,
	b.created_by_actor_id::text, b.created_at `

// entryPipelineName is the display name every organization's entry
// pipeline is seeded with.
const entryPipelineName = "Entry pipeline"

// CreateRequestApprovalPipeline brings a pipeline into being with its
// first revision, active at once, so a pipeline never exists without an
// active revision. A key that was ever used, archived keys included, is
// refused: keys are never reused. The entry pipeline is seeded by the
// organization's activation and never created here.
func (s *Store) CreateRequestApprovalPipeline(ctx context.Context, input application.CreateRequestApprovalPipelineInput) (domain.RequestApprovalPipelineRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	if input.PipelineKey == domain.EntryPipelineKey {
		return domain.RequestApprovalPipelineRevision{}, domain.ErrRequestApprovalPipelineEntry
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockPipelineGraph(ctx, tx, input.OrganizationID); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	if err := lockRequestApprovalPipeline(ctx, tx, input.OrganizationID, input.PipelineKey); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM request_approval_pipelines WHERE organization_id = $1 AND pipeline_key = $2)`,
		input.OrganizationID, input.PipelineKey).Scan(&exists); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	if exists {
		return domain.RequestApprovalPipelineRevision{}, domain.ErrRequestApprovalPipelineExists
	}
	revision, err := s.insertRequestApprovalPipelineRevision(ctx, tx, application.CreateRequestApprovalPipelineRevisionInput(input), 1, true)
	if err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	// The first revision needs no reference or graph check beyond what
	// the pipeline it delegates to already passed: a new key is reached
	// by nobody yet, so it cannot close a cycle or deepen a chain that a
	// request would walk.
	if err := requirePipelineReferences(ctx, tx, revision); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	return revision, nil
}

func (s *Store) CreateRequestApprovalPipelineRevision(ctx context.Context, input application.CreateRequestApprovalPipelineRevisionInput) (domain.RequestApprovalPipelineRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockRequestApprovalPipeline(ctx, tx, input.OrganizationID, input.PipelineKey); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	// A pipeline comes into being through its create (the entry pipeline
	// with the organization), never through a revision: an unknown key
	// has no pipeline, and an archived one takes no revisions either.
	if _, hasActive, err := activeRequestApprovalPipelineRevision(ctx, tx, input.OrganizationID, input.PipelineKey); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	} else if !hasActive {
		return domain.RequestApprovalPipelineRevision{}, missingRequestApprovalPipeline(ctx, tx, input.OrganizationID, input.PipelineKey)
	}
	var revisionNumber int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(revision), 0) + 1
		FROM request_approval_pipelines WHERE organization_id = $1 AND pipeline_key = $2`,
		input.OrganizationID, input.PipelineKey).Scan(&revisionNumber); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	revision, err := s.insertRequestApprovalPipelineRevision(ctx, tx, input, revisionNumber, false)
	if err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	return revision, nil
}

// insertRequestApprovalPipelineRevision stores the blocks and the revision
// row and audits the creation, plus the activation when the revision is
// born active (a pipeline's first).
func (s *Store) insertRequestApprovalPipelineRevision(ctx context.Context, tx pgx.Tx, input application.CreateRequestApprovalPipelineRevisionInput, revisionNumber int64, active bool) (domain.RequestApprovalPipelineRevision, error) {
	actorID, err := managementActorID(ctx, tx, input.OrganizationID, input.CreatedByActorID)
	if err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	blockIDs := make([]domain.ID, 0, len(input.Blocks))
	for _, blockInput := range input.Blocks {
		block, err := insertOrReuseRequestApprovalBlock(ctx, tx, input, blockInput)
		if err != nil {
			return domain.RequestApprovalPipelineRevision{}, err
		}
		blockIDs = append(blockIDs, block.ID)
	}
	revision := domain.RequestApprovalPipelineRevision{
		ID: input.ID, OrganizationID: input.OrganizationID, PipelineKey: input.PipelineKey, Name: input.Name,
		Revision: revisionNumber, IsActive: active, BlockIDs: blockIDs, DefaultEscalationPath: input.DefaultEscalationPath,
		DefaultAskAgentOwner: input.DefaultAskAgentOwner, CreatedByActorID: input.CreatedByActorID, CreatedAt: input.Now,
	}
	if _, err := tx.Exec(ctx, `INSERT INTO request_approval_pipelines
		(id, organization_id, pipeline_key, name, revision, is_active, block_ids, default_escalation_path,
		 default_ask_agent_owner, created_by_actor_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9, $10, $11)`, revision.ID, revision.OrganizationID,
		revision.PipelineKey, revision.Name, revision.Revision, active, idStrings(revision.BlockIDs), revision.DefaultEscalationPath,
		revision.DefaultAskAgentOwner, revision.CreatedByActorID, revision.CreatedAt); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	created, _ := json.Marshal(map[string]any{
		"pipeline_key": revision.PipelineKey, "name": revision.Name,
		"revision": revision.Revision, "block_count": len(revision.BlockIDs),
	})
	if err := s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "request_approval_pipeline.revision_created",
		ActorType: domain.ActorHuman, ActorID: actorID,
		SubjectType: "request_approval_pipeline", SubjectID: revision.ID,
		Data: created, OccurredAt: input.Now,
	}); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	if !active {
		return revision, nil
	}
	activated, _ := json.Marshal(map[string]any{
		"pipeline_key": revision.PipelineKey, "name": revision.Name,
		"revision": revision.Revision, "previous_active_revision": nil,
	})
	if err := s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "request_approval_pipeline.activated",
		ActorType: domain.ActorHuman, ActorID: actorID,
		SubjectType: "request_approval_pipeline", SubjectID: revision.ID,
		Data: activated, OccurredAt: input.Now,
	}); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	return revision, nil
}

func (s *Store) ListRequestApprovalPipelineSummaries(ctx context.Context, input application.ListRequestApprovalPipelineSummariesInput) ([]domain.RequestApprovalPipelineSummary, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	statusFilter := "HAVING bool_or(archived_at IS NULL)"
	switch input.Status {
	case application.PipelineStatusArchived:
		statusFilter = "HAVING bool_and(archived_at IS NOT NULL)"
	case application.PipelineStatusAll:
		statusFilter = ""
	}
	// The name is the active revision's, or the latest revision's once a
	// pipeline is archived. The entry pipeline lists first.
	rows, err := s.pool.Query(ctx, `SELECT pipeline_key,
		(array_agg(name ORDER BY is_active DESC, revision DESC))[1],
		max(revision) FILTER (WHERE is_active), max(revision), count(*), max(created_at),
		CASE WHEN bool_and(archived_at IS NOT NULL) THEN max(archived_at) END
		FROM request_approval_pipelines
		WHERE organization_id = $1 AND ($3::boolean OR pipeline_key = ANY($4::text[]))
		GROUP BY pipeline_key `+statusFilter+`
		ORDER BY (pipeline_key <> $5), pipeline_key
		LIMIT $2`, input.OrganizationID, limit, input.Scope.All, input.Scope.IDs(domain.ScopePipeline), domain.EntryPipelineKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	summaries := make([]domain.RequestApprovalPipelineSummary, 0)
	for rows.Next() {
		var summary domain.RequestApprovalPipelineSummary
		if err := rows.Scan(&summary.PipelineKey, &summary.Name, &summary.ActiveRevision, &summary.LatestRevision,
			&summary.RevisionCount, &summary.LatestCreatedAt, &summary.ArchivedAt); err != nil {
			return nil, err
		}
		summary.ReferencedBy = make([]domain.PipelineDelegation, 0)
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := attachPipelineDelegations(ctx, s.pool, input.OrganizationID, summaries); err != nil {
		return nil, err
	}
	return summaries, nil
}

// attachPipelineDelegations fills each summary's ReferencedBy with the
// delegate blocks in active revisions that name its key, in the order a
// request meets them: the entry pipeline first, then by key, and within
// a pipeline by the block's position.
func attachPipelineDelegations(ctx context.Context, q querier, organizationID domain.ID, summaries []domain.RequestApprovalPipelineSummary) error {
	if len(summaries) == 0 {
		return nil
	}
	index := make(map[string]int, len(summaries))
	keys := make([]string, len(summaries))
	for position, summary := range summaries {
		index[summary.PipelineKey] = position
		keys[position] = summary.PipelineKey
	}
	rows, err := q.Query(ctx, `SELECT b.config->>'pipeline_key', p.pipeline_key, p.name, p.revision, b.block_key, b.name
		FROM request_approval_pipelines p
		JOIN request_approval_blocks b ON b.organization_id = p.organization_id AND b.id = ANY(p.block_ids)
		WHERE p.organization_id = $1 AND p.is_active
		  AND b.block_type = 'delegate' AND b.config->>'pipeline_key' = ANY($2::text[])
		ORDER BY (p.pipeline_key <> $3), p.pipeline_key, array_position(p.block_ids, b.id)`,
		organizationID, keys, domain.EntryPipelineKey)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var target string
		var delegation domain.PipelineDelegation
		if err := rows.Scan(&target, &delegation.PipelineKey, &delegation.PipelineName, &delegation.Revision,
			&delegation.BlockKey, &delegation.BlockName); err != nil {
			return err
		}
		if position, ok := index[target]; ok {
			summaries[position].ReferencedBy = append(summaries[position].ReferencedBy, delegation)
		}
	}
	return rows.Err()
}

func insertOrReuseRequestApprovalBlock(ctx context.Context, tx pgx.Tx, input application.CreateRequestApprovalPipelineRevisionInput, blockInput application.RequestApprovalBlockSnapshotInput) (domain.RequestApprovalBlock, error) {
	query := requestApprovalBlockSelect + `FROM request_approval_blocks b WHERE b.organization_id = $1
		AND b.content_hash = request_approval_block_content_hash($2, $3, $4, $5, $6, $7, $8)`
	row := tx.QueryRow(ctx, `INSERT INTO request_approval_blocks
		(id, organization_id, block_key, name, block_type, config_version, config, timeout_ms,
		 max_attempts, created_by_actor_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (organization_id, content_hash) DO NOTHING
		RETURNING id::text, organization_id::text, block_key, name, block_type, config_version,
		config, timeout_ms, max_attempts, content_hash, created_by_actor_id::text, created_at`,
		blockInput.ID, input.OrganizationID, blockInput.BlockKey, blockInput.Name, blockInput.BlockType,
		blockInput.ConfigVersion, blockInput.Config, blockInput.TimeoutMS, blockInput.MaxAttempts,
		input.CreatedByActorID, input.Now)
	block, err := scanRequestApprovalBlock(row)
	if !errors.Is(err, domain.ErrNotFound) {
		return block, err
	}
	return scanRequestApprovalBlock(tx.QueryRow(ctx, query, input.OrganizationID, blockInput.BlockKey,
		blockInput.Name, blockInput.BlockType, blockInput.ConfigVersion, blockInput.Config,
		blockInput.TimeoutMS, blockInput.MaxAttempts))
}

func (s *Store) GetRequestApprovalPipelineRevision(ctx context.Context, input application.GetRequestApprovalPipelineRevisionInput) (domain.RequestApprovalPipelineRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	return scanRequestApprovalPipelineRevision(s.pool.QueryRow(ctx, requestApprovalPipelineSelect+
		`WHERE organization_id = $1 AND pipeline_key = $2 AND revision = $3`,
		input.OrganizationID, input.PipelineKey, input.Revision))
}

func (s *Store) GetRequestApprovalPipelineRevisionByID(ctx context.Context, input application.GetRequestApprovalPipelineRevisionByIDInput) (domain.RequestApprovalPipelineRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	return scanRequestApprovalPipelineRevision(s.pool.QueryRow(ctx, requestApprovalPipelineSelect+
		`WHERE organization_id = $1 AND id = $2`, input.OrganizationID, input.RevisionID))
}

func (s *Store) ListRequestApprovalPipelineRevisions(ctx context.Context, input application.ListRequestApprovalPipelineRevisionsInput) (application.RevisionPage[domain.RequestApprovalPipelineRevision], error) {
	var page application.RevisionPage[domain.RequestApprovalPipelineRevision]
	if err := input.Validate(); err != nil {
		return page, err
	}
	limit := revisionPageLimit(input.Limit)
	before, _ := application.DecodeRevisionCursor(input.Cursor)
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM request_approval_pipelines
		WHERE organization_id = $1 AND pipeline_key = $2`,
		input.OrganizationID, input.PipelineKey).Scan(&page.TotalCount); err != nil {
		return page, err
	}
	rows, err := s.pool.Query(ctx, requestApprovalPipelineSelect+`WHERE organization_id = $1 AND pipeline_key = $2
		AND ($3 = 0 OR revision < $3)
		ORDER BY revision DESC LIMIT $4`, input.OrganizationID, input.PipelineKey, before, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	revisions := make([]domain.RequestApprovalPipelineRevision, 0)
	for rows.Next() {
		revision, err := scanRequestApprovalPipelineRevision(rows)
		if err != nil {
			return page, err
		}
		revisions = append(revisions, revision)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	page.Revisions, page.NextCursor = trimRevisionPage(revisions, limit, func(revision domain.RequestApprovalPipelineRevision) int64 { return revision.Revision })
	return page, nil
}

func (s *Store) ListActiveRequestApprovalPipelineRevisions(ctx context.Context, input application.ListActiveRequestApprovalPipelineRevisionsInput) ([]domain.RequestApprovalPipelineRevision, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, requestApprovalPipelineSelect+`WHERE is_active
		AND (NULLIF($1, '')::uuid IS NULL OR id > NULLIF($1, '')::uuid)
		ORDER BY id LIMIT $2`, input.AfterID, input.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	revisions := make([]domain.RequestApprovalPipelineRevision, 0, input.Limit)
	for rows.Next() {
		revision, err := scanRequestApprovalPipelineRevision(rows)
		if err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	return revisions, rows.Err()
}

func (s *Store) GetActiveRequestApprovalPipelineRevision(ctx context.Context, input application.GetActiveRequestApprovalPipelineRevisionInput) (domain.RequestApprovalPipelineRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	return scanRequestApprovalPipelineRevision(s.pool.QueryRow(ctx, requestApprovalPipelineSelect+
		`WHERE organization_id = $1 AND pipeline_key = $2 AND is_active`, input.OrganizationID, input.PipelineKey))
}

func (s *Store) GetRequestApprovalBlocks(ctx context.Context, input application.GetRequestApprovalBlocksInput) ([]domain.RequestApprovalBlock, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	if len(input.BlockIDs) == 0 {
		return []domain.RequestApprovalBlock{}, nil
	}
	rows, err := s.pool.Query(ctx, requestApprovalBlockSelect+`FROM unnest($2::uuid[]) WITH ORDINALITY requested(id, position)
		JOIN request_approval_blocks b ON b.id = requested.id AND b.organization_id = $1
		ORDER BY requested.position`, input.OrganizationID, idStrings(input.BlockIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	blocks := make([]domain.RequestApprovalBlock, 0, len(input.BlockIDs))
	for rows.Next() {
		block, err := scanRequestApprovalBlock(rows)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(blocks) != len(input.BlockIDs) {
		return nil, domain.ErrNotFound
	}
	return blocks, nil
}

// ActivateRequestApprovalPipelineRevision switches the pipeline's active
// revision. It takes the organization's pipeline graph lock first, then
// the pipeline's own lock, then the locks of everything the target
// references, so an archive of any of them either sees this activation or
// waits for it, and the delegation graph the target joins is checked as a
// whole under the same lock.
func (s *Store) ActivateRequestApprovalPipelineRevision(ctx context.Context, input application.ActivateRequestApprovalPipelineRevisionInput) (domain.RequestApprovalPipelineRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockPipelineGraph(ctx, tx, input.OrganizationID); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	if err := lockRequestApprovalPipeline(ctx, tx, input.OrganizationID, input.PipelineKey); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	current, hasCurrent, err := activeRequestApprovalPipelineRevision(ctx, tx, input.OrganizationID, input.PipelineKey)
	if err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	if !hasCurrent {
		return domain.RequestApprovalPipelineRevision{}, missingRequestApprovalPipeline(ctx, tx, input.OrganizationID, input.PipelineKey)
	}
	if current.Revision != input.ExpectedActiveRevision {
		return domain.RequestApprovalPipelineRevision{}, fmt.Errorf("this pipeline changed. Reload and try again: %w", domain.ErrPreconditionFailed)
	}
	target, err := scanRequestApprovalPipelineRevision(tx.QueryRow(ctx, requestApprovalPipelineSelect+
		`WHERE organization_id = $1 AND pipeline_key = $2 AND revision = $3`, input.OrganizationID,
		input.PipelineKey, input.Revision))
	if err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	if target.ArchivedAt != nil {
		return domain.RequestApprovalPipelineRevision{}, domain.ErrRequestApprovalPipelineArchived
	}
	if current.ID == target.ID {
		if err := tx.Commit(ctx); err != nil {
			return domain.RequestApprovalPipelineRevision{}, err
		}
		return target, nil
	}
	if err := requirePipelineReferences(ctx, tx, target); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	if err := requirePipelineGraph(ctx, tx, target); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	actorID, err := managementActorID(ctx, tx, input.OrganizationID, input.ActorID)
	if err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE request_approval_pipelines SET is_active = false
		WHERE organization_id = $1 AND id = $2`, input.OrganizationID, current.ID); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE request_approval_pipelines SET is_active = true
		WHERE organization_id = $1 AND id = $2`, input.OrganizationID, target.ID); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	data, _ := json.Marshal(map[string]any{
		"pipeline_key":             target.PipelineKey,
		"name":                     target.Name,
		"revision":                 target.Revision,
		"previous_active_revision": current.Revision,
	})
	if err := s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "request_approval_pipeline.activated",
		ActorType: domain.ActorHuman, ActorID: actorID,
		SubjectType: "request_approval_pipeline", SubjectID: target.ID,
		Data: data, OccurredAt: input.Now,
	}); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	target.IsActive = true
	return target, nil
}

// ArchiveRequestApprovalPipeline stamps every revision of the pipeline in
// one update, deactivating the active one with it: the one way a pipeline
// loses its active revision. The in-use guard runs under the graph lock
// and the pipeline's lock, the same locks an activation takes before it
// verifies its delegate targets are active, so neither write can slip
// past the other. Organization keys scoped to the pipeline lose their
// assignments with it.
func (s *Store) ArchiveRequestApprovalPipeline(ctx context.Context, input application.ArchiveRequestApprovalPipelineInput) ([]domain.RequestApprovalPipelineRevision, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	if input.PipelineKey == domain.EntryPipelineKey {
		return nil, domain.ErrRequestApprovalPipelineEntry
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := lockPipelineGraph(ctx, tx, input.OrganizationID); err != nil {
		return nil, err
	}
	if err := lockRequestApprovalPipeline(ctx, tx, input.OrganizationID, input.PipelineKey); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, requestApprovalPipelineSelect+`WHERE organization_id = $1 AND pipeline_key = $2
		ORDER BY revision DESC`, input.OrganizationID, input.PipelineKey)
	if err != nil {
		return nil, err
	}
	revisions, err := scanRequestApprovalPipelineRevisions(rows)
	if err != nil {
		return nil, err
	}
	if len(revisions) == 0 {
		return nil, fmt.Errorf("pipeline %q: %w", input.PipelineKey, domain.ErrNotFound)
	}
	if revisions[0].ArchivedAt != nil {
		return nil, domain.ErrRequestApprovalPipelineArchived
	}
	uses, err := listRequestApprovalPipelineUses(ctx, tx, input.OrganizationID, input.PipelineKey)
	if err != nil {
		return nil, err
	}
	if len(uses) > 0 {
		return nil, domain.RequestApprovalPipelineInUseError{PipelineKey: input.PipelineKey, Uses: uses}
	}
	actorID, err := managementActorID(ctx, tx, input.OrganizationID, input.ActorID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE request_approval_pipelines SET is_active = false, archived_at = $3
		WHERE organization_id = $1 AND pipeline_key = $2 AND archived_at IS NULL`,
		input.OrganizationID, input.PipelineKey, input.Now); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM role_assignments WHERE organization_id=$1 AND principal_kind='api_key' AND scope_kind='pipeline' AND scope_id=$2`,
		input.OrganizationID, input.PipelineKey); err != nil {
		return nil, err
	}
	var activeRevision any
	subjectID := revisions[0].ID
	for _, revision := range revisions {
		if revision.IsActive {
			activeRevision, subjectID = revision.Revision, revision.ID
		}
	}
	data, _ := json.Marshal(map[string]any{
		"pipeline_key":    input.PipelineKey,
		"name":            revisions[0].Name,
		"revision_count":  len(revisions),
		"active_revision": activeRevision,
	})
	if err := s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "request_approval_pipeline.archived",
		ActorType: domain.ActorHuman, ActorID: actorID,
		SubjectType: "request_approval_pipeline", SubjectID: subjectID,
		Data: data, OccurredAt: input.Now,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	archivedAt := input.Now
	for index := range revisions {
		revisions[index].IsActive = false
		revisions[index].ArchivedAt = &archivedAt
	}
	return revisions, nil
}

func (s *Store) ListRequestApprovalPipelineUses(ctx context.Context, input application.ListRequestApprovalPipelineUsesInput) ([]domain.PipelineUse, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	return listRequestApprovalPipelineUses(ctx, s.pool, input.OrganizationID, input.PipelineKey)
}

// listRequestApprovalPipelineUses discovers the active pipeline revisions
// whose delegate blocks name the pipeline.
func listRequestApprovalPipelineUses(ctx context.Context, q querier, organizationID domain.ID, pipelineKey string) ([]domain.PipelineUse, error) {
	return scanPipelineUses(q.Query(ctx, `SELECT p.id::text, p.pipeline_key, p.revision, b.block_key
		FROM request_approval_pipelines p
		JOIN request_approval_blocks b ON b.organization_id = p.organization_id AND b.id = ANY(p.block_ids)
		WHERE p.organization_id = $1 AND p.is_active
		  AND b.block_type = 'delegate' AND b.config->>'pipeline_key' = $2
		ORDER BY (p.pipeline_key <> $3), p.pipeline_key, b.block_key`, organizationID, pipelineKey, domain.EntryPipelineKey))
}

// ResolvePipelineKeys reports which of the named keys are missing (never
// created, or archived: an archived pipeline can never be a target) and
// which exist without an active revision.
func (s *Store) ResolvePipelineKeys(ctx context.Context, input application.ResolvePipelineKeysInput) (application.ResolvedPipelineKeys, error) {
	if err := input.Validate(); err != nil {
		return application.ResolvedPipelineKeys{}, err
	}
	result := application.ResolvedPipelineKeys{Missing: make([]string, 0), Inactive: make([]string, 0)}
	if len(input.PipelineKeys) == 0 {
		return result, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT pipeline_key, bool_or(is_active) FROM request_approval_pipelines
		WHERE organization_id = $1 AND pipeline_key = ANY($2) AND archived_at IS NULL
		GROUP BY pipeline_key`, input.OrganizationID, input.PipelineKeys)
	if err != nil {
		return application.ResolvedPipelineKeys{}, err
	}
	defer rows.Close()
	found := make(map[string]bool, len(input.PipelineKeys))
	for rows.Next() {
		var key string
		var active bool
		if err := rows.Scan(&key, &active); err != nil {
			return application.ResolvedPipelineKeys{}, err
		}
		found[key] = active
	}
	if err := rows.Err(); err != nil {
		return application.ResolvedPipelineKeys{}, err
	}
	for _, key := range input.PipelineKeys {
		active, exists := found[key]
		switch {
		case !exists:
			result.Missing = append(result.Missing, key)
		case !active:
			result.Inactive = append(result.Inactive, key)
		}
	}
	return result, nil
}

// ensureEntryPipeline gives an organization its entry pipeline: an empty,
// active revision 1 that passes every request through. It runs in the
// organization's activation transaction so the entry pipeline never
// exists without an active revision, and is a no-op for an organization
// that already has one. The membership is recorded as the creator and
// audited as the actor, exactly as a hand-written revision would be.
func (s *Store) ensureEntryPipeline(ctx context.Context, tx pgx.Tx, organizationID domain.ID, membershipID domain.ID, now time.Time) error {
	if err := lockRequestApprovalPipeline(ctx, tx, organizationID, domain.EntryPipelineKey); err != nil {
		return err
	}
	var live int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE archived_at IS NULL)
		FROM request_approval_pipelines WHERE organization_id = $1 AND pipeline_key = $2`,
		organizationID, domain.EntryPipelineKey).Scan(&live); err != nil {
		return err
	}
	if live > 0 {
		return nil
	}
	_, err := s.insertRequestApprovalPipelineRevision(ctx, tx, application.CreateRequestApprovalPipelineRevisionInput{
		ID: domain.ID(uuid.NewString()), OrganizationID: organizationID, PipelineKey: domain.EntryPipelineKey, Name: entryPipelineName,
		CreatedByActorID: membershipID, Now: now,
	}, 1, true)
	return err
}

// missingRequestApprovalPipeline explains a pipeline without an active
// revision: archived, or never created.
func missingRequestApprovalPipeline(ctx context.Context, tx pgx.Tx, organizationID domain.ID, pipelineKey string) error {
	var archived bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM request_approval_pipelines
		WHERE organization_id = $1 AND pipeline_key = $2)`,
		organizationID, pipelineKey).Scan(&archived); err != nil {
		return err
	}
	if archived {
		return domain.ErrRequestApprovalPipelineArchived
	}
	return fmt.Errorf("request approval pipeline %q does not exist: %w", pipelineKey, domain.ErrNotFound)
}

// pipelineReferences is what a revision's immutable content points at
// outside itself: escalation path keys (block paths and the default),
// webhook endpoint keys, and the pipelines its delegate blocks name, each
// sorted and deduplicated.
type pipelineReferences struct {
	pathKeys     []string
	endpointKeys []string
	pipelineKeys []string
}

func readPipelineReferences(ctx context.Context, tx pgx.Tx, revision domain.RequestApprovalPipelineRevision) (pipelineReferences, error) {
	rows, err := tx.Query(ctx, `SELECT block_type, COALESCE(config->>'escalation_path', ''), COALESCE(config->>'endpoint_key', ''), COALESCE(config->>'pipeline_key', '')
		FROM request_approval_blocks WHERE organization_id = $1 AND id = ANY($2::uuid[])`,
		revision.OrganizationID, revision.BlockIDs)
	if err != nil {
		return pipelineReferences{}, err
	}
	defer rows.Close()
	paths, endpoints, pipelines := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	if revision.DefaultEscalationPath != "" {
		paths[revision.DefaultEscalationPath] = struct{}{}
	}
	for rows.Next() {
		var blockType, pathKey, endpointKey, pipelineKey string
		if err := rows.Scan(&blockType, &pathKey, &endpointKey, &pipelineKey); err != nil {
			return pipelineReferences{}, err
		}
		if pathKey != "" {
			paths[pathKey] = struct{}{}
		}
		if blockType == "webhook" && endpointKey != "" {
			endpoints[endpointKey] = struct{}{}
		}
		if blockType == "delegate" && pipelineKey != "" {
			pipelines[pipelineKey] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return pipelineReferences{}, err
	}
	return pipelineReferences{
		pathKeys: slices.Sorted(maps.Keys(paths)), endpointKeys: slices.Sorted(maps.Keys(endpoints)),
		pipelineKeys: slices.Sorted(maps.Keys(pipelines)),
	}, nil
}

// requirePipelineReferences is the activation guard against a revision
// whose references are gone: every escalation path it names, every
// webhook endpoint it posts to, and every pipeline it delegates to must
// have an active revision. It runs inside the activating transaction and
// takes each referenced object's advisory lock (after the pipeline's, in
// sorted key order) so a concurrent archive either sees this activation
// or waits for it.
func requirePipelineReferences(ctx context.Context, tx pgx.Tx, revision domain.RequestApprovalPipelineRevision) error {
	references, err := readPipelineReferences(ctx, tx, revision)
	if err != nil {
		return err
	}
	for _, pathKey := range references.pathKeys {
		if err := lockEscalationPath(ctx, tx, revision.OrganizationID, pathKey); err != nil {
			return err
		}
	}
	if len(references.pathKeys) > 0 {
		active, err := queryStrings(ctx, tx, `SELECT path_key FROM escalation_paths
			WHERE organization_id = $1 AND path_key = ANY($2) AND is_active`, revision.OrganizationID, references.pathKeys)
		if err != nil {
			return err
		}
		if inactive := missingStrings(references.pathKeys, active); len(inactive) > 0 {
			return domain.EscalationPathsInactiveError{PathKeys: inactive}
		}
	}
	for _, endpointKey := range references.endpointKeys {
		if err := lockWebhookEndpoint(ctx, tx, revision.OrganizationID, endpointKey); err != nil {
			return err
		}
	}
	if len(references.endpointKeys) > 0 {
		active, err := queryStrings(ctx, tx, `SELECT endpoint_key FROM webhook_endpoint_revisions
			WHERE organization_id = $1 AND endpoint_key = ANY($2) AND is_active`, revision.OrganizationID, references.endpointKeys)
		if err != nil {
			return err
		}
		if inactive := missingStrings(references.endpointKeys, active); len(inactive) > 0 {
			return domain.WebhookEndpointsInactiveError{EndpointKeys: inactive}
		}
	}
	for _, pipelineKey := range references.pipelineKeys {
		if pipelineKey == revision.PipelineKey {
			return domain.PipelineDelegationCycleError{PipelineKeys: []string{pipelineKey, pipelineKey}}
		}
		if err := lockRequestApprovalPipeline(ctx, tx, revision.OrganizationID, pipelineKey); err != nil {
			return err
		}
	}
	if len(references.pipelineKeys) > 0 {
		active, err := queryStrings(ctx, tx, `SELECT pipeline_key FROM request_approval_pipelines
			WHERE organization_id = $1 AND pipeline_key = ANY($2) AND is_active`, revision.OrganizationID, references.pipelineKeys)
		if err != nil {
			return err
		}
		if inactive := missingStrings(references.pipelineKeys, active); len(inactive) > 0 {
			return domain.PipelinesInactiveError{PipelineKeys: inactive}
		}
	}
	return nil
}

// requirePipelineGraph checks the organization's delegation graph as it
// would stand with the candidate active in place of its key's current
// revision: every delegate target is a live node, no cycle, no chain
// deeper than the limit, and the flattened effective pipeline from the
// entry within the cap. The whole active graph is checked, not only what
// the entry reaches, so a later activation is never surprised by a cycle
// that was already waiting. It runs under the organization's graph lock.
func requirePipelineGraph(ctx context.Context, tx pgx.Tx, candidate domain.RequestApprovalPipelineRevision) error {
	rows, err := tx.Query(ctx, `SELECT p.pipeline_key, cardinality(p.block_ids), b.config->>'pipeline_key'
		FROM request_approval_pipelines p
		LEFT JOIN request_approval_blocks b ON b.organization_id = p.organization_id AND b.id = ANY(p.block_ids) AND b.block_type = 'delegate'
		WHERE p.organization_id = $1 AND ((p.is_active AND p.pipeline_key <> $2) OR p.id = $3)`,
		candidate.OrganizationID, candidate.PipelineKey, candidate.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	sizes := make(map[string]int)
	edges := make(map[string][]string)
	for rows.Next() {
		var key string
		var size int
		var target *string
		if err := rows.Scan(&key, &size, &target); err != nil {
			return err
		}
		sizes[key] = size
		if target != nil {
			edges[key] = append(edges[key], *target)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return checkPipelineGraph(sizes, edges)
}

// checkPipelineGraph is the pure graph check: nodes are live pipelines with
// their block counts, edges are delegate blocks in order. Every edge
// target must be a node; a depth-first walk from every node refuses a
// cycle (naming it) and a chain deeper than the limit; and the flattened
// size from the entry, counting a target once per delegate that names
// it, must fit the cap.
func checkPipelineGraph(sizes map[string]int, edges map[string][]string) error {
	for key, targets := range edges {
		for _, target := range targets {
			if _, live := sizes[target]; !live {
				return domain.PipelinesInactiveError{PipelineKeys: []string{target}}
			}
			_ = key
		}
	}
	const (
		unvisited = iota
		visiting
		done
	)
	state := make(map[string]int, len(sizes))
	depthBelow := make(map[string]int, len(sizes))
	flattened := make(map[string]int, len(sizes))
	var walk func(key string, stack []string) error
	walk = func(key string, stack []string) error {
		switch state[key] {
		case done:
			return nil
		case visiting:
			start := slices.Index(stack, key)
			return domain.PipelineDelegationCycleError{PipelineKeys: append(slices.Clone(stack[start:]), key)}
		}
		state[key] = visiting
		stack = append(stack, key)
		deepest, size := 0, sizes[key]
		for _, target := range edges[key] {
			if err := walk(target, stack); err != nil {
				return err
			}
			deepest = max(deepest, depthBelow[target]+1)
			size += flattened[target]
			if size > domain.MaximumEffectivePipelineBlocks {
				return fmt.Errorf("%w, the limit is %d", domain.ErrEffectivePipelineTooLarge, domain.MaximumEffectivePipelineBlocks)
			}
		}
		if deepest > domain.MaximumDelegationDepth {
			return domain.PipelineDelegationTooDeepError{Depth: deepest}
		}
		depthBelow[key], flattened[key] = deepest, size
		state[key] = done
		return nil
	}
	for _, key := range slices.Sorted(maps.Keys(sizes)) {
		if err := walk(key, nil); err != nil {
			return err
		}
	}
	return nil
}

func queryStrings(ctx context.Context, q querier, sql string, args ...any) ([]string, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]string, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// missingStrings returns the wanted values absent from found, in order.
func missingStrings(wanted, found []string) []string {
	present := make(map[string]struct{}, len(found))
	for _, value := range found {
		present[value] = struct{}{}
	}
	missing := make([]string, 0)
	for _, value := range wanted {
		if _, ok := present[value]; !ok {
			missing = append(missing, value)
		}
	}
	return missing
}

func scanRequestApprovalPipelineRevision(row rowScanner) (domain.RequestApprovalPipelineRevision, error) {
	var revision domain.RequestApprovalPipelineRevision
	var id, organizationID, membershipID string
	var blockIDs []string
	err := row.Scan(&id, &organizationID, &revision.PipelineKey, &revision.Name, &revision.Revision, &revision.IsActive,
		&blockIDs, &revision.DefaultEscalationPath, &revision.DefaultAskAgentOwner, &membershipID, &revision.CreatedAt, &revision.ArchivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RequestApprovalPipelineRevision{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.RequestApprovalPipelineRevision{}, err
	}
	revision.ID = domain.ID(id)
	revision.OrganizationID = domain.ID(organizationID)
	revision.CreatedByActorID = domain.ID(membershipID)
	revision.BlockIDs = make([]domain.ID, len(blockIDs))
	for index, blockID := range blockIDs {
		revision.BlockIDs[index] = domain.ID(blockID)
	}
	return revision, nil
}

func scanRequestApprovalPipelineRevisions(rows pgx.Rows) ([]domain.RequestApprovalPipelineRevision, error) {
	defer rows.Close()
	revisions := make([]domain.RequestApprovalPipelineRevision, 0)
	for rows.Next() {
		revision, err := scanRequestApprovalPipelineRevision(rows)
		if err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	return revisions, rows.Err()
}

func scanRequestApprovalBlock(row rowScanner) (domain.RequestApprovalBlock, error) {
	var block domain.RequestApprovalBlock
	var id, organizationID, membershipID string
	err := row.Scan(&id, &organizationID, &block.BlockKey, &block.Name, &block.BlockType,
		&block.ConfigVersion, &block.Config, &block.TimeoutMS, &block.MaxAttempts, &block.ContentHash,
		&membershipID, &block.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RequestApprovalBlock{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.RequestApprovalBlock{}, err
	}
	block.ID = domain.ID(id)
	block.OrganizationID = domain.ID(organizationID)
	block.CreatedByActorID = domain.ID(membershipID)
	return block, nil
}

func activeRequestApprovalPipelineRevision(ctx context.Context, tx pgx.Tx, organizationID domain.ID, pipelineKey string) (domain.RequestApprovalPipelineRevision, bool, error) {
	revision, err := scanRequestApprovalPipelineRevision(tx.QueryRow(ctx, requestApprovalPipelineSelect+
		`WHERE organization_id = $1 AND pipeline_key = $2 AND is_active`, organizationID, pipelineKey))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.RequestApprovalPipelineRevision{}, false, nil
	}
	return revision, err == nil, err
}

// lockRequestApprovalPipeline serializes one pipeline's mutations. The key
// must match the enforce_request_approval_pipeline_revision trigger.
func lockRequestApprovalPipeline(ctx context.Context, tx pgx.Tx, organizationID domain.ID, pipelineKey string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text || ':' || $2::text, 0))`, organizationID, pipelineKey)
	return err
}

// lockPipelineGraph serializes changes to the organization's delegation
// graph: activations and archives take it before any pipeline's own lock,
// so the graph check sees every concurrent switch settled.
func lockPipelineGraph(ctx context.Context, tx pgx.Tx, organizationID domain.ID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text || ':pipeline-graph', 0))`, organizationID)
	return err
}

func idStrings(ids []domain.ID) []string {
	values := make([]string, len(ids))
	for index, id := range ids {
		values[index] = string(id)
	}
	return values
}
