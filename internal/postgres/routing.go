package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/authz"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/escalation"
	"github.com/withHumanAI/withHuman/routing"
)

const escalationPathSelect = `SELECT id::text, organization_id::text, path_key, revision, is_active,
	name, document, document_version, created_by_actor_id::text, created_at, archived_at
	FROM escalation_paths `

// routingQuerier is what the snapshot reads need from a pool or a
// transaction.
type routingQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// CreateEscalationPath creates the path with its first revision. A key
// that was ever used, archived included, refuses with
// domain.ErrEscalationPathExists.
func (s *Store) CreateEscalationPath(ctx context.Context, input application.CreateEscalationPathInput) (domain.EscalationPathRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.EscalationPathRevision{}, err
	}
	return s.insertEscalationPathRevision(ctx, application.CreateEscalationPathRevisionInput(input), false, "escalation_path.created")
}

// CreateEscalationPathRevision appends the next revision to a live path;
// an unknown key is domain.ErrNotFound and an archived one
// domain.ErrEscalationPathArchived.
func (s *Store) CreateEscalationPathRevision(ctx context.Context, input application.CreateEscalationPathRevisionInput) (domain.EscalationPathRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.EscalationPathRevision{}, err
	}
	return s.insertEscalationPathRevision(ctx, input, true, "escalation_path.revision_created")
}

// insertEscalationPathRevision writes the next revision of the key under
// its advisory lock. requireExisting says whether the key must already
// exist and be live (a new revision) or must never have been used (a new
// path).
func (s *Store) insertEscalationPathRevision(ctx context.Context, input application.CreateEscalationPathRevisionInput, requireExisting bool, eventType string) (domain.EscalationPathRevision, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.EscalationPathRevision{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockEscalationPath(ctx, tx, input.OrganizationID, input.PathKey); err != nil {
		return domain.EscalationPathRevision{}, err
	}
	actorID, err := managementActorID(ctx, tx, input.OrganizationID, input.CreatedByActorID)
	if err != nil {
		return domain.EscalationPathRevision{}, err
	}
	var revisionNumber int64
	var exists, archived bool
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(revision), 0) + 1, count(*) > 0,
		COALESCE(bool_or(archived_at IS NOT NULL), false)
		FROM escalation_paths WHERE organization_id = $1 AND path_key = $2`,
		input.OrganizationID, input.PathKey).Scan(&revisionNumber, &exists, &archived); err != nil {
		return domain.EscalationPathRevision{}, err
	}
	switch {
	case requireExisting && !exists:
		return domain.EscalationPathRevision{}, fmt.Errorf("escalation path %q: %w", input.PathKey, domain.ErrNotFound)
	case requireExisting && archived:
		return domain.EscalationPathRevision{}, domain.ErrEscalationPathArchived
	case !requireExisting && exists:
		return domain.EscalationPathRevision{}, domain.ErrEscalationPathExists
	}
	revision := domain.EscalationPathRevision{
		ID: input.ID, OrganizationID: input.OrganizationID, PathKey: input.PathKey,
		Revision: revisionNumber, IsActive: false, Name: input.Name,
		Document: input.Document, DocumentVersion: input.DocumentVersion,
		CreatedByActorID: input.CreatedByActorID, CreatedAt: input.Now,
	}
	if _, err := tx.Exec(ctx, `INSERT INTO escalation_paths
		(id, organization_id, path_key, revision, is_active, name, document, document_version,
		 created_by_actor_id, created_at)
		VALUES ($1, $2, $3, $4, false, $5, $6, $7, $8, $9)`, revision.ID, revision.OrganizationID,
		revision.PathKey, revision.Revision, revision.Name, revision.Document,
		revision.DocumentVersion, revision.CreatedByActorID, revision.CreatedAt); err != nil {
		return domain.EscalationPathRevision{}, err
	}
	data, _ := json.Marshal(map[string]any{"path_key": revision.PathKey, "revision": revision.Revision})
	if err := s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: eventType,
		ActorType: domain.ActorHuman, ActorID: actorID,
		SubjectType: "escalation_path", SubjectID: revision.ID,
		Data: data, OccurredAt: input.Now,
	}); err != nil {
		return domain.EscalationPathRevision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.EscalationPathRevision{}, err
	}
	return revision, nil
}

func (s *Store) ListEscalationPathSummaries(ctx context.Context, input application.ListEscalationPathSummariesInput) ([]domain.EscalationPathSummary, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// Archiving stamps every revision of a key, so any row's archived_at
	// speaks for the path.
	statusFilter := "AND archived_at IS NULL"
	switch input.Status {
	case application.EscalationPathStatusArchived:
		statusFilter = "AND archived_at IS NOT NULL"
	case application.EscalationPathStatusAll:
		statusFilter = ""
	}
	rows, err := s.pool.Query(ctx, `SELECT path_key,
		(array_agg(name ORDER BY revision DESC))[1],
		max(revision) FILTER (WHERE is_active), max(revision), count(*), max(created_at), max(archived_at)
		FROM escalation_paths
		WHERE organization_id = $1 `+statusFilter+`
		GROUP BY path_key
		ORDER BY path_key
		LIMIT $2`, input.OrganizationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	summaries := make([]domain.EscalationPathSummary, 0)
	for rows.Next() {
		var summary domain.EscalationPathSummary
		if err := rows.Scan(&summary.PathKey, &summary.Name, &summary.ActiveRevision,
			&summary.LatestRevision, &summary.RevisionCount, &summary.LatestCreatedAt, &summary.ArchivedAt); err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

func (s *Store) GetEscalationPathRevision(ctx context.Context, input application.GetEscalationPathRevisionInput) (domain.EscalationPathRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.EscalationPathRevision{}, err
	}
	return scanEscalationPathRevision(s.pool.QueryRow(ctx, escalationPathSelect+
		`WHERE organization_id = $1 AND path_key = $2 AND revision = $3`,
		input.OrganizationID, input.PathKey, input.Revision))
}

func (s *Store) ListEscalationPathRevisions(ctx context.Context, input application.ListEscalationPathRevisionsInput) (application.RevisionPage[domain.EscalationPathRevision], error) {
	var page application.RevisionPage[domain.EscalationPathRevision]
	if err := input.Validate(); err != nil {
		return page, err
	}
	limit := revisionPageLimit(input.Limit)
	before, _ := application.DecodeRevisionCursor(input.Cursor)
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM escalation_paths WHERE organization_id = $1 AND path_key = $2`,
		input.OrganizationID, input.PathKey).Scan(&page.TotalCount); err != nil {
		return page, err
	}
	rows, err := s.pool.Query(ctx, escalationPathSelect+`WHERE organization_id = $1 AND path_key = $2
		AND ($3 = 0 OR revision < $3)
		ORDER BY revision DESC LIMIT $4`, input.OrganizationID, input.PathKey, before, limit+1)
	if err != nil {
		return page, err
	}
	revisions, err := scanEscalationPathRevisions(rows)
	if err != nil {
		return page, err
	}
	page.Revisions, page.NextCursor = trimRevisionPage(revisions, limit, func(revision domain.EscalationPathRevision) int64 { return revision.Revision })
	return page, nil
}

func (s *Store) GetActiveEscalationPathRevision(ctx context.Context, input application.GetActiveEscalationPathRevisionInput) (domain.EscalationPathRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.EscalationPathRevision{}, err
	}
	return scanEscalationPathRevision(s.pool.QueryRow(ctx, escalationPathSelect+
		`WHERE organization_id = $1 AND path_key = $2 AND is_active`, input.OrganizationID, input.PathKey))
}

func (s *Store) ActivateEscalationPathRevision(ctx context.Context, input application.ActivateEscalationPathRevisionInput) (domain.EscalationPathRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.EscalationPathRevision{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.EscalationPathRevision{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockEscalationPath(ctx, tx, input.OrganizationID, input.PathKey); err != nil {
		return domain.EscalationPathRevision{}, err
	}
	current, hasCurrent, err := activeEscalationPathRevision(ctx, tx, input.OrganizationID, input.PathKey)
	if err != nil {
		return domain.EscalationPathRevision{}, err
	}
	if !expectedRevisionMatches(current.Revision, hasCurrent, input.ExpectedActiveRevision) {
		return domain.EscalationPathRevision{}, fmt.Errorf("this escalation path changed. Reload and try again: %w", domain.ErrPreconditionFailed)
	}
	target, err := scanEscalationPathRevision(tx.QueryRow(ctx, escalationPathSelect+
		`WHERE organization_id = $1 AND path_key = $2 AND revision = $3`,
		input.OrganizationID, input.PathKey, input.Revision))
	if err != nil {
		return domain.EscalationPathRevision{}, err
	}
	if target.ArchivedAt != nil {
		return domain.EscalationPathRevision{}, domain.ErrEscalationPathArchived
	}
	if hasCurrent && current.ID == target.ID {
		if err := tx.Commit(ctx); err != nil {
			return domain.EscalationPathRevision{}, err
		}
		return target, nil
	}
	actorID, err := managementActorID(ctx, tx, input.OrganizationID, input.ActorID)
	if err != nil {
		return domain.EscalationPathRevision{}, err
	}
	if hasCurrent {
		if _, err := tx.Exec(ctx, `UPDATE escalation_paths SET is_active = false
			WHERE organization_id = $1 AND id = $2`, input.OrganizationID, current.ID); err != nil {
			return domain.EscalationPathRevision{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE escalation_paths SET is_active = true
		WHERE organization_id = $1 AND id = $2`, input.OrganizationID, target.ID); err != nil {
		return domain.EscalationPathRevision{}, err
	}
	data, _ := json.Marshal(map[string]any{
		"path_key":                 target.PathKey,
		"revision":                 target.Revision,
		"previous_active_revision": revisionValue(current.Revision, hasCurrent),
	})
	if err := s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "escalation_path.activated",
		ActorType: domain.ActorHuman, ActorID: actorID,
		SubjectType: "escalation_path", SubjectID: target.ID,
		Data: data, OccurredAt: input.Now,
	}); err != nil {
		return domain.EscalationPathRevision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.EscalationPathRevision{}, err
	}
	target.IsActive = true
	return target, nil
}

// ArchiveEscalationPath archives every revision of the key in one update,
// deactivating the active one with it. The in-use guard runs under the
// path's lock, the same lock pipeline activation takes before it verifies
// the path is active, so neither write can slip past the other.
func (s *Store) ArchiveEscalationPath(ctx context.Context, input application.ArchiveEscalationPathInput) ([]domain.EscalationPathRevision, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := lockEscalationPath(ctx, tx, input.OrganizationID, input.PathKey); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, escalationPathSelect+`WHERE organization_id = $1 AND path_key = $2
		ORDER BY revision DESC`, input.OrganizationID, input.PathKey)
	if err != nil {
		return nil, err
	}
	revisions, err := scanEscalationPathRevisions(rows)
	if err != nil {
		return nil, err
	}
	if len(revisions) == 0 {
		return nil, fmt.Errorf("escalation path %q: %w", input.PathKey, domain.ErrNotFound)
	}
	if revisions[0].ArchivedAt != nil {
		return nil, domain.ErrEscalationPathArchived
	}
	uses, err := listEscalationPathUses(ctx, tx, input.OrganizationID, input.PathKey)
	if err != nil {
		return nil, err
	}
	if len(uses) > 0 {
		return nil, domain.EscalationPathInUseError{PathKey: input.PathKey, Uses: uses}
	}
	actorID, err := managementActorID(ctx, tx, input.OrganizationID, input.ActorID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE escalation_paths SET is_active = false, archived_at = $3
		WHERE organization_id = $1 AND path_key = $2 AND archived_at IS NULL`,
		input.OrganizationID, input.PathKey, input.Now); err != nil {
		return nil, err
	}
	var activeRevision any
	for _, revision := range revisions {
		if revision.IsActive {
			activeRevision = revision.Revision
		}
	}
	data, _ := json.Marshal(map[string]any{
		"path_key":        input.PathKey,
		"revision_count":  len(revisions),
		"active_revision": activeRevision,
	})
	if err := s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "escalation_path.archived",
		ActorType: domain.ActorHuman, ActorID: actorID,
		SubjectType: "escalation_path", SubjectID: revisions[0].ID,
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

func (s *Store) ListEscalationPathUses(ctx context.Context, input application.ListEscalationPathUsesInput) ([]domain.PipelineUse, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	return listEscalationPathUses(ctx, s.pool, input.OrganizationID, input.PathKey)
}

// listEscalationPathUses discovers the active pipeline revisions that
// name the path: once per block whose config names it, and once with an
// empty block key when the revision names it as its default.
func listEscalationPathUses(ctx context.Context, q querier, organizationID domain.ID, pathKey string) ([]domain.PipelineUse, error) {
	return scanPipelineUses(q.Query(ctx, `SELECT id::text, pipeline_key, revision, block_key FROM (
		SELECT p.id, p.pipeline_key, p.revision, '' AS block_key
		FROM request_approval_pipelines p
		WHERE p.organization_id = $1 AND p.is_active AND p.default_escalation_path = $2
		UNION ALL
		SELECT p.id, p.pipeline_key, p.revision, b.block_key
		FROM request_approval_pipelines p
		JOIN request_approval_blocks b ON b.organization_id = p.organization_id AND b.id = ANY(p.block_ids)
		WHERE p.organization_id = $1 AND p.is_active AND b.config->>'escalation_path' = $2
	) uses ORDER BY (pipeline_key <> $3), pipeline_key, revision, block_key`, organizationID, pathKey, domain.EntryPipelineKey))
}

// ResolveEscalationPaths reports which of the named keys are missing
// (never created, or archived: an archived path can never be named) and
// which exist without an active one.
func (s *Store) ResolveEscalationPaths(ctx context.Context, input application.ResolveEscalationPathsInput) (application.ResolvedEscalationPaths, error) {
	if err := input.Validate(); err != nil {
		return application.ResolvedEscalationPaths{}, err
	}
	result := application.ResolvedEscalationPaths{Missing: make([]string, 0), Inactive: make([]string, 0)}
	if len(input.PathKeys) == 0 {
		return result, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT path_key, bool_or(is_active) FROM escalation_paths
		WHERE organization_id = $1 AND path_key = ANY($2) AND archived_at IS NULL
		GROUP BY path_key`, input.OrganizationID, input.PathKeys)
	if err != nil {
		return application.ResolvedEscalationPaths{}, err
	}
	defer rows.Close()
	active := make(map[string]bool, len(input.PathKeys))
	for rows.Next() {
		var key string
		var isActive bool
		if err := rows.Scan(&key, &isActive); err != nil {
			return application.ResolvedEscalationPaths{}, err
		}
		active[key] = isActive
	}
	if err := rows.Err(); err != nil {
		return application.ResolvedEscalationPaths{}, err
	}
	for _, key := range input.PathKeys {
		isActive, exists := active[key]
		if !exists {
			result.Missing = append(result.Missing, key)
		} else if !isActive {
			result.Inactive = append(result.Inactive, key)
		}
	}
	return result, nil
}

// GetApprovalRouting reads the routing snapshot pinned when the request
// entered human review, with the pinned path revision's name. Requests
// that never routed have no row.
func (s *Store) GetApprovalRouting(ctx context.Context, input application.GetApprovalRoutingInput) (application.ApprovalRoutingView, error) {
	if err := input.Validate(); err != nil {
		return application.ApprovalRoutingView{}, err
	}
	var view application.ApprovalRoutingView
	var requestID, organizationID string
	var pathKey, pathRevisionID, pipelineRevisionID, blockID *string
	var ownerID, ownerFallback string
	var document, facts []byte
	err := s.pool.QueryRow(ctx, `SELECT arr.request_id::text, arr.organization_id::text, arr.outcome, arr.reason,
		arr.escalation_path_key, arr.escalation_path_revision_id::text, arr.pipeline_revision_id::text,
		arr.block_id::text, arr.urgency, arr.created_at, COALESCE(ep.name, ''),
		COALESCE(ep.document, 'null'::jsonb), arr.facts,
		COALESCE(arr.agent_owner_membership_id::text, ''), COALESCE(arr.agent_owner_fallback, ''),
		COALESCE(ou.display_name, ''), h.created_at
		FROM approval_request_routing arr
		LEFT JOIN escalation_paths ep ON ep.id = arr.escalation_path_revision_id AND ep.organization_id = arr.organization_id
		LEFT JOIN memberships om ON om.id = arr.agent_owner_membership_id AND om.organization_id = arr.organization_id
		LEFT JOIN users ou ON ou.id = om.user_id
		LEFT JOIN approval_request_agent_owner_handoffs h ON h.request_id = arr.request_id AND h.organization_id = arr.organization_id
		WHERE arr.organization_id = $1 AND arr.request_id = $2`,
		input.OrganizationID, input.RequestID).Scan(&requestID, &organizationID, &view.Outcome,
		&view.Reason, &pathKey, &pathRevisionID, &pipelineRevisionID, &blockID,
		&view.Urgency, &view.CreatedAt, &view.EscalationPathName, &document, &facts,
		&ownerID, &ownerFallback, &view.AgentOwnerName, &view.AgentOwnerHandedOffAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ApprovalRoutingView{}, domain.ErrNotFound
	}
	if err != nil {
		return application.ApprovalRoutingView{}, err
	}
	view.RequestID = domain.ID(requestID)
	view.OrganizationID = domain.ID(organizationID)
	if view.Facts, err = decodeRoutingFacts(facts); err != nil {
		return application.ApprovalRoutingView{}, err
	}
	view.AgentOwnerMembershipID, view.AgentOwnerFallback = domain.ID(ownerID), ownerFallback
	view.Targets = make([]application.ApprovalRoutingTarget, 0)
	owner, escalated := effectiveRouting(string(view.Outcome), ownerID, view.AgentOwnerHandedOffAt != nil, document)
	switch {
	case owner != "":
		view.Targets = append(view.Targets, application.ApprovalRoutingTarget{
			Type: string(routing.TargetUser), ID: owner, Name: view.AgentOwnerName,
		})
	case escalated:
		if view.Targets, err = resolveRoutingTargets(ctx, s.pool, input.OrganizationID, document, facts); err != nil {
			return application.ApprovalRoutingView{}, err
		}
	}
	if pathKey != nil {
		view.EscalationPathKey = *pathKey
	}
	if pathRevisionID != nil {
		view.EscalationPathRevisionID = domain.ID(*pathRevisionID)
	}
	if pipelineRevisionID != nil {
		view.PipelineRevisionID = domain.ID(*pipelineRevisionID)
	}
	if blockID != nil {
		view.BlockID = domain.ID(*blockID)
	}
	if view.PinnedTeamPolicies, err = readPinnedTeamPolicies(ctx, s.pool, input.OrganizationID, input.RequestID); err != nil {
		return application.ApprovalRoutingView{}, err
	}
	view.TeamPolicies = make([]domain.ApprovalRoutingTeamPolicy, 0, len(view.PinnedTeamPolicies))
	for _, pinned := range view.PinnedTeamPolicies {
		view.TeamPolicies = append(view.TeamPolicies, domain.ApprovalRoutingTeamPolicy{
			TeamID: pinned.TeamID, PolicyRevisionID: pinned.PolicyRevisionID, Revision: pinned.Revision,
		})
	}
	if view.Escalation, err = readEscalationProgress(ctx, s.pool, input.OrganizationID, input.RequestID); err != nil {
		return application.ApprovalRoutingView{}, err
	}
	return view, nil
}

// readPinnedTeamPolicies lists the request's pinned team policy revisions
// with each team's current name, in team name order for the product page.
func readPinnedTeamPolicies(ctx context.Context, q querier, organizationID, requestID domain.ID) ([]application.PinnedTeamPolicy, error) {
	rows, err := q.Query(ctx, `SELECT tp.team_id::text, COALESCE(t.name, ''), p.id::text, p.revision
		FROM approval_request_routing_team_policies tp
		JOIN team_escalation_policies p ON p.id = tp.policy_revision_id AND p.organization_id = tp.organization_id
		LEFT JOIN teams t ON t.id = tp.team_id AND t.organization_id = tp.organization_id
		WHERE tp.organization_id = $1 AND tp.request_id = $2
		ORDER BY t.name, tp.team_id`, organizationID, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pinned := make([]application.PinnedTeamPolicy, 0)
	for rows.Next() {
		var policy application.PinnedTeamPolicy
		var teamID, revisionID string
		if err := rows.Scan(&teamID, &policy.TeamName, &revisionID, &policy.Revision); err != nil {
			return nil, err
		}
		policy.TeamID = domain.ID(teamID)
		policy.PolicyRevisionID = domain.ID(revisionID)
		pinned = append(pinned, policy)
	}
	return pinned, rows.Err()
}

// insertApprovalRouting writes the pinned snapshot inside the transaction
// that commits the request or its resume.
func insertApprovalRouting(ctx context.Context, tx pgx.Tx, routing domain.ApprovalRequestRouting) error {
	// A nil map would encode as SQL NULL; the column is an object always.
	facts := []byte("{}")
	if len(routing.Facts) > 0 {
		encoded, err := json.Marshal(routing.Facts)
		if err != nil {
			return err
		}
		facts = encoded
	}
	_, err := tx.Exec(ctx, `INSERT INTO approval_request_routing
		(request_id, organization_id, outcome, reason, escalation_path_key, escalation_path_revision_id,
		 pipeline_revision_id, block_id, urgency, created_at, facts, agent_owner_membership_id, agent_owner_fallback)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6::text, '')::uuid, NULLIF($7::text, '')::uuid,
		        NULLIF($8::text, '')::uuid, $9, $10, $11, NULLIF($12::text, '')::uuid, NULLIF($13, ''))`,
		routing.RequestID, routing.OrganizationID, routing.Outcome, routing.Reason, routing.EscalationPathKey,
		routing.EscalationPathRevisionID, routing.PipelineRevisionID, routing.BlockID, routing.Urgency, routing.CreatedAt, facts,
		routing.AgentOwnerMembershipID, routing.AgentOwnerFallback)
	if err != nil {
		return err
	}
	// The team policy pins ride the same transaction so a request can
	// never be observed with its path pinned but its team programs not.
	for _, policy := range routing.TeamPolicies {
		if _, err := tx.Exec(ctx, `INSERT INTO approval_request_routing_team_policies
			(request_id, organization_id, team_id, policy_revision_id, created_at)
			VALUES ($1, $2, $3, $4, $5)`,
			routing.RequestID, routing.OrganizationID, policy.TeamID, policy.PolicyRevisionID, routing.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

// pinnedRouting is an escalated snapshot's pinned path document, the
// grammar version it was written in, the request-condition facts pinned
// with it, and the request's urgency. agentOwnerID is set instead of the
// document while an agent_owner snapshot still reaches its owner.
type pinnedRouting struct {
	document        []byte
	documentVersion int
	facts           []byte
	urgency         string
	agentOwnerID    domain.ID
}

// readPinnedRouting reads the request's current routing; escalated
// reports whether a path is walked. An agent_owner snapshot reports its
// owner in agentOwnerID until a handoff, and afterwards reads as the
// escalated snapshot of its pinned fallback. A missing row reads as the
// default queue.
func readPinnedRouting(ctx context.Context, q routingQuerier, organizationID, requestID domain.ID) (pinnedRouting, bool, error) {
	var outcome, urgency, ownerID string
	var document, facts []byte
	var documentVersion int
	var handedOff bool
	err := q.QueryRow(ctx, `SELECT arr.outcome, arr.urgency, arr.facts, COALESCE(ep.document, 'null'::jsonb),
		COALESCE(ep.document_version, 0), COALESCE(arr.agent_owner_membership_id::text, ''), h.request_id IS NOT NULL
		FROM approval_request_routing arr
		LEFT JOIN escalation_paths ep
		  ON ep.id = arr.escalation_path_revision_id AND ep.organization_id = arr.organization_id
		LEFT JOIN approval_request_agent_owner_handoffs h
		  ON h.request_id = arr.request_id AND h.organization_id = arr.organization_id
		WHERE arr.organization_id = $1 AND arr.request_id = $2`,
		organizationID, requestID).Scan(&outcome, &urgency, &facts, &document, &documentVersion, &ownerID, &handedOff)
	if errors.Is(err, pgx.ErrNoRows) {
		return pinnedRouting{}, false, nil
	}
	if err != nil {
		return pinnedRouting{}, false, err
	}
	owner, escalated := effectiveRouting(outcome, ownerID, handedOff, document)
	if !escalated {
		return pinnedRouting{urgency: urgency, agentOwnerID: owner}, false, nil
	}
	return pinnedRouting{document: document, documentVersion: documentVersion, facts: facts, urgency: urgency}, true, nil
}

// effectiveRouting reads a snapshot row the way every reader must: an
// agent_owner snapshot admits its owner until a handoff and then runs its
// pinned fallback like an escalated snapshot, and a snapshot whose path
// document is missing reads as the default queue.
func effectiveRouting(outcome, ownerID string, handedOff bool, document []byte) (domain.ID, bool) {
	switch domain.RoutingOutcome(outcome) {
	case domain.RoutingAgentOwner:
		if !handedOff {
			return domain.ID(ownerID), false
		}
	case domain.RoutingEscalated:
	default:
		return "", false
	}
	return "", len(document) > 0 && string(document) != "null"
}

// decodeRoutingFacts reads a snapshot's facts column. An empty column
// reads as no facts, so every request condition is false.
func decodeRoutingFacts(facts []byte) (map[string]bool, error) {
	decoded := make(map[string]bool)
	if len(facts) == 0 {
		return decoded, nil
	}
	if err := json.Unmarshal(facts, &decoded); err != nil {
		return nil, fmt.Errorf("pinned routing facts are unreadable: %w", err)
	}
	return decoded, nil
}

// pinnedTargets splits the targets current routing admits by type: the
// owner of an agent_owner snapshot, or what the pinned path reaches.
func pinnedTargets(pinned pinnedRouting) (userIDs, teamIDs []domain.ID, err error) {
	if pinned.agentOwnerID != "" {
		return []domain.ID{pinned.agentOwnerID}, make([]domain.ID, 0), nil
	}
	return reachableDocumentTargets(pinned.document, pinned.facts)
}

// reachableDocumentTargets parses a pinned escalation path document and
// splits by type the targets it reaches under the pinned request facts:
// the set that may claim and decide. A corrupt document or fact map is an
// error, never a silent unscope.
func reachableDocumentTargets(document, facts []byte) (userIDs, teamIDs []domain.ID, err error) {
	var parsed routing.PathDocument
	if err := json.Unmarshal(document, &parsed); err != nil {
		return nil, nil, fmt.Errorf("pinned escalation path document is unreadable: %w", err)
	}
	factMap, err := decodeRoutingFacts(facts)
	if err != nil {
		return nil, nil, err
	}
	userIDs, teamIDs = make([]domain.ID, 0), make([]domain.ID, 0)
	for _, target := range reachableTargets(parsed, factMap) {
		switch target.Type {
		case routing.TargetUser:
			userIDs = append(userIDs, domain.ID(target.ID))
		case routing.TargetTeam:
			teamIDs = append(teamIDs, domain.ID(target.ID))
		}
	}
	return userIDs, teamIDs, nil
}

func reachableTargets(document routing.PathDocument, facts map[string]bool) []routing.Target {
	return document.ReachableTargets(func(conditionID string) bool {
		return facts[escalation.FactKey("", conditionID)]
	})
}

// resolveRoutingTargets names the targets a pinned document reaches for
// the product page, in walk order. A membership or team that vanished
// keeps its id with an empty name rather than dropping out.
func resolveRoutingTargets(ctx context.Context, q querier, organizationID domain.ID, document, facts []byte) ([]application.ApprovalRoutingTarget, error) {
	var parsed routing.PathDocument
	if err := json.Unmarshal(document, &parsed); err != nil {
		return nil, fmt.Errorf("pinned escalation path document is unreadable: %w", err)
	}
	factMap, err := decodeRoutingFacts(facts)
	if err != nil {
		return nil, err
	}
	reachable := reachableTargets(parsed, factMap)
	userIDs, teamIDs := make([]domain.ID, 0), make([]domain.ID, 0)
	for _, target := range reachable {
		switch target.Type {
		case routing.TargetUser:
			userIDs = append(userIDs, domain.ID(target.ID))
		case routing.TargetTeam:
			teamIDs = append(teamIDs, domain.ID(target.ID))
		}
	}
	names := make(map[string]string, len(userIDs)+len(teamIDs))
	if len(userIDs) > 0 {
		rows, err := q.Query(ctx, `SELECT m.id::text, u.display_name FROM memberships m JOIN users u ON u.id = m.user_id
            WHERE m.organization_id = $1 AND m.id = ANY($2::uuid[])`, organizationID, idStrings(userIDs))
		if err != nil {
			return nil, err
		}
		if err := collectNames(rows, string(routing.TargetUser), names); err != nil {
			return nil, err
		}
	}
	if len(teamIDs) > 0 {
		rows, err := q.Query(ctx, `SELECT id::text, name FROM teams WHERE organization_id = $1 AND id = ANY($2::uuid[])`,
			organizationID, idStrings(teamIDs))
		if err != nil {
			return nil, err
		}
		if err := collectNames(rows, string(routing.TargetTeam), names); err != nil {
			return nil, err
		}
	}
	targets := make([]application.ApprovalRoutingTarget, 0, len(reachable))
	for _, target := range reachable {
		if target.Type != routing.TargetUser && target.Type != routing.TargetTeam {
			continue
		}
		targets = append(targets, application.ApprovalRoutingTarget{
			Type: string(target.Type), ID: domain.ID(target.ID), Name: names[string(target.Type)+":"+target.ID],
		})
	}
	return targets, nil
}

func collectNames(rows pgx.Rows, kind string, into map[string]string) error {
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		into[kind+":"+id] = name
	}
	return rows.Err()
}

func scanEscalationPathRevision(row rowScanner) (domain.EscalationPathRevision, error) {
	var revision domain.EscalationPathRevision
	var id, organizationID, membershipID string
	err := row.Scan(&id, &organizationID, &revision.PathKey, &revision.Revision, &revision.IsActive,
		&revision.Name, &revision.Document, &revision.DocumentVersion, &membershipID, &revision.CreatedAt,
		&revision.ArchivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.EscalationPathRevision{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.EscalationPathRevision{}, err
	}
	revision.ID = domain.ID(id)
	revision.OrganizationID = domain.ID(organizationID)
	revision.CreatedByActorID = domain.ID(membershipID)
	return revision, nil
}

func scanEscalationPathRevisions(rows pgx.Rows) ([]domain.EscalationPathRevision, error) {
	defer rows.Close()
	revisions := make([]domain.EscalationPathRevision, 0)
	for rows.Next() {
		revision, err := scanEscalationPathRevision(rows)
		if err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	return revisions, rows.Err()
}

func activeEscalationPathRevision(ctx context.Context, tx pgx.Tx, organizationID domain.ID, pathKey string) (domain.EscalationPathRevision, bool, error) {
	revision, err := scanEscalationPathRevision(tx.QueryRow(ctx, escalationPathSelect+
		`WHERE organization_id = $1 AND path_key = $2 AND is_active`, organizationID, pathKey))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.EscalationPathRevision{}, false, nil
	}
	return revision, err == nil, err
}

func expectedRevisionMatches(currentRevision int64, hasCurrent bool, expected *int64) bool {
	if expected == nil {
		return !hasCurrent
	}
	return hasCurrent && currentRevision == *expected
}

func revisionValue(revision int64, hasCurrent bool) any {
	if !hasCurrent {
		return nil
	}
	return revision
}

// lockEscalationPath must derive the same advisory lock key as the
// monotonic-revision trigger in 0007_routing.sql.
func lockEscalationPath(ctx context.Context, tx pgx.Tx, organizationID domain.ID, pathKey string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('escalation_path:' || $1::text || ':' || $2::text, 0))`,
		organizationID, pathKey)
	return err
}

// GetAgentOwnerStanding reads whether a personal agent's owner can take a
// request: an active membership on an active account, holding
// request.decide for the request under their current grants. A membership
// that does not exist reads as inactive.
func (s *Store) GetAgentOwnerStanding(ctx context.Context, input application.GetAgentOwnerStandingInput) (application.AgentOwnerStanding, error) {
	if err := input.Validate(); err != nil {
		return application.AgentOwnerStanding{}, err
	}
	var active bool
	err := s.pool.QueryRow(ctx, `SELECT m.status = 'active' AND u.status = 'active'
		FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.organization_id = $1 AND m.id = $2`, input.OrganizationID, input.MembershipID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
		return application.AgentOwnerStanding{}, nil
	}
	if err != nil {
		return application.AgentOwnerStanding{}, err
	}
	grants, err := loadGrants(ctx, s.pool, input.OrganizationID, domain.PrincipalMembership, input.MembershipID)
	if err != nil {
		return application.AgentOwnerStanding{}, err
	}
	return application.AgentOwnerStanding{
		Active:    true,
		CanDecide: authz.Holds(grants, domain.PermissionRequestDecide, domain.RequestResource(input.RequestID)),
	}, nil
}
