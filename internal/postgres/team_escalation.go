package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
)

const teamEscalationPolicySelect = `SELECT id::text, organization_id::text, team_id::text, revision, is_active,
	document, document_version, created_by_actor_id::text, created_at
	FROM team_escalation_policies `

func (s *Store) CreateTeamEscalationPolicyRevision(ctx context.Context, input application.CreateTeamEscalationPolicyRevisionInput) (domain.TeamEscalationPolicyRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireLiveTeam(ctx, tx, input.OrganizationID, input.TeamID); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	if err := lockTeamEscalationPolicy(ctx, tx, input.OrganizationID, input.TeamID); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	actorID, err := managementActorID(ctx, tx, input.OrganizationID, input.CreatedByActorID)
	if err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	var revisionNumber int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(revision), 0) + 1
		FROM team_escalation_policies WHERE organization_id = $1 AND team_id = $2`,
		input.OrganizationID, input.TeamID).Scan(&revisionNumber); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	revision := domain.TeamEscalationPolicyRevision{
		ID: input.ID, OrganizationID: input.OrganizationID, TeamID: input.TeamID,
		Revision: revisionNumber, IsActive: false,
		Document: input.Document, DocumentVersion: input.DocumentVersion,
		CreatedByActorID: input.CreatedByActorID, CreatedAt: input.Now,
	}
	if _, err := tx.Exec(ctx, `INSERT INTO team_escalation_policies
		(id, organization_id, team_id, revision, is_active, document, document_version,
		 created_by_actor_id, created_at)
		VALUES ($1, $2, $3, $4, false, $5, $6, $7, $8)`, revision.ID, revision.OrganizationID,
		revision.TeamID, revision.Revision, revision.Document, revision.DocumentVersion,
		revision.CreatedByActorID, revision.CreatedAt); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	data, _ := json.Marshal(map[string]any{"team_id": revision.TeamID, "revision": revision.Revision})
	if err := s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "team_escalation_policy.revision_created",
		ActorType: domain.ActorHuman, ActorID: actorID,
		SubjectType: "team_escalation_policy", SubjectID: revision.ID,
		Data: data, OccurredAt: input.Now,
	}); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	return revision, nil
}

func (s *Store) ListTeamEscalationPolicyRevisions(ctx context.Context, input application.ListTeamEscalationPolicyRevisionsInput) ([]domain.TeamEscalationPolicyRevision, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, teamEscalationPolicySelect+`WHERE organization_id = $1 AND team_id = $2
		ORDER BY revision DESC LIMIT $3`, input.OrganizationID, input.TeamID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	revisions := make([]domain.TeamEscalationPolicyRevision, 0)
	for rows.Next() {
		revision, err := scanTeamEscalationPolicyRevision(rows)
		if err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	return revisions, rows.Err()
}

func (s *Store) GetTeamEscalationPolicyRevision(ctx context.Context, input application.GetTeamEscalationPolicyRevisionInput) (domain.TeamEscalationPolicyRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	return scanTeamEscalationPolicyRevision(s.pool.QueryRow(ctx, teamEscalationPolicySelect+
		`WHERE organization_id = $1 AND team_id = $2 AND revision = $3`,
		input.OrganizationID, input.TeamID, input.Revision))
}

func (s *Store) GetActiveTeamEscalationPolicyRevision(ctx context.Context, input application.GetActiveTeamEscalationPolicyRevisionInput) (domain.TeamEscalationPolicyRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	return scanTeamEscalationPolicyRevision(s.pool.QueryRow(ctx, teamEscalationPolicySelect+
		`WHERE organization_id = $1 AND team_id = $2 AND is_active`, input.OrganizationID, input.TeamID))
}

// GetActiveTeamEscalationPolicies answers for every named team at once so
// the router pins a whole path's team targets with one round trip.
func (s *Store) GetActiveTeamEscalationPolicies(ctx context.Context, input application.GetActiveTeamEscalationPoliciesInput) ([]domain.TeamEscalationPolicyRevision, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	revisions := make([]domain.TeamEscalationPolicyRevision, 0)
	if len(input.TeamIDs) == 0 {
		return revisions, nil
	}
	rows, err := s.pool.Query(ctx, teamEscalationPolicySelect+`WHERE organization_id = $1 AND is_active
		AND team_id = ANY($2::uuid[]) ORDER BY team_id`, input.OrganizationID, idStrings(input.TeamIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		revision, err := scanTeamEscalationPolicyRevision(rows)
		if err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	return revisions, rows.Err()
}

func (s *Store) ActivateTeamEscalationPolicyRevision(ctx context.Context, input application.ActivateTeamEscalationPolicyRevisionInput) (domain.TeamEscalationPolicyRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireLiveTeam(ctx, tx, input.OrganizationID, input.TeamID); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	if err := lockTeamEscalationPolicy(ctx, tx, input.OrganizationID, input.TeamID); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	current, hasCurrent, err := activeTeamEscalationPolicyRevision(ctx, tx, input.OrganizationID, input.TeamID)
	if err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	if !expectedRevisionMatches(current.Revision, hasCurrent, input.ExpectedActiveRevision) {
		return domain.TeamEscalationPolicyRevision{}, fmt.Errorf("this escalation policy changed. Reload and try again: %w", domain.ErrPreconditionFailed)
	}
	target, err := scanTeamEscalationPolicyRevision(tx.QueryRow(ctx, teamEscalationPolicySelect+
		`WHERE organization_id = $1 AND team_id = $2 AND revision = $3`, input.OrganizationID,
		input.TeamID, input.Revision))
	if err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	if hasCurrent && current.ID == target.ID {
		if err := tx.Commit(ctx); err != nil {
			return domain.TeamEscalationPolicyRevision{}, err
		}
		return target, nil
	}
	actorID, err := managementActorID(ctx, tx, input.OrganizationID, input.ActorID)
	if err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	if hasCurrent {
		if _, err := tx.Exec(ctx, `UPDATE team_escalation_policies SET is_active = false
			WHERE organization_id = $1 AND id = $2`, input.OrganizationID, current.ID); err != nil {
			return domain.TeamEscalationPolicyRevision{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE team_escalation_policies SET is_active = true
		WHERE organization_id = $1 AND id = $2`, input.OrganizationID, target.ID); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	data, _ := json.Marshal(map[string]any{
		"team_id":                  target.TeamID,
		"revision":                 target.Revision,
		"previous_active_revision": revisionValue(current.Revision, hasCurrent),
	})
	if err := s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "team_escalation_policy.activated",
		ActorType: domain.ActorHuman, ActorID: actorID,
		SubjectType: "team_escalation_policy", SubjectID: target.ID,
		Data: data, OccurredAt: input.Now,
	}); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	target.IsActive = true
	return target, nil
}

// DeactivateTeamEscalationPolicy archives the team's active revision. Unlike
// an organization path, nothing names a team policy by key: a path level
// targeting the team simply falls back to notifying every member, so
// there is no "in use" refusal.
func (s *Store) DeactivateTeamEscalationPolicy(ctx context.Context, input application.DeactivateTeamEscalationPolicyInput) (domain.TeamEscalationPolicyRevision, error) {
	if err := input.Validate(); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireLiveTeam(ctx, tx, input.OrganizationID, input.TeamID); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	if err := lockTeamEscalationPolicy(ctx, tx, input.OrganizationID, input.TeamID); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	current, hasCurrent, err := activeTeamEscalationPolicyRevision(ctx, tx, input.OrganizationID, input.TeamID)
	if err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	if !hasCurrent || current.Revision != input.ExpectedActiveRevision {
		return domain.TeamEscalationPolicyRevision{}, fmt.Errorf("this escalation policy changed. Reload and try again: %w", domain.ErrPreconditionFailed)
	}
	actorID, err := managementActorID(ctx, tx, input.OrganizationID, input.ActorID)
	if err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE team_escalation_policies SET is_active = false
		WHERE organization_id = $1 AND id = $2`, input.OrganizationID, current.ID); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	data, _ := json.Marshal(map[string]any{"team_id": current.TeamID, "revision": current.Revision})
	if err := s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "team_escalation_policy.deactivated",
		ActorType: domain.ActorHuman, ActorID: actorID,
		SubjectType: "team_escalation_policy", SubjectID: current.ID,
		Data: data, OccurredAt: input.Now,
	}); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	current.IsActive = false
	return current, nil
}

func scanTeamEscalationPolicyRevision(row rowScanner) (domain.TeamEscalationPolicyRevision, error) {
	var revision domain.TeamEscalationPolicyRevision
	var id, organizationID, teamID, membershipID string
	err := row.Scan(&id, &organizationID, &teamID, &revision.Revision, &revision.IsActive,
		&revision.Document, &revision.DocumentVersion, &membershipID, &revision.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.TeamEscalationPolicyRevision{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.TeamEscalationPolicyRevision{}, err
	}
	revision.ID = domain.ID(id)
	revision.OrganizationID = domain.ID(organizationID)
	revision.TeamID = domain.ID(teamID)
	revision.CreatedByActorID = domain.ID(membershipID)
	return revision, nil
}

func activeTeamEscalationPolicyRevision(ctx context.Context, q routingQuerier, organizationID, teamID domain.ID) (domain.TeamEscalationPolicyRevision, bool, error) {
	revision, err := scanTeamEscalationPolicyRevision(q.QueryRow(ctx, teamEscalationPolicySelect+
		`WHERE organization_id = $1 AND team_id = $2 AND is_active`, organizationID, teamID))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.TeamEscalationPolicyRevision{}, false, nil
	}
	return revision, err == nil, err
}

// lockTeamEscalationPolicy must derive the same advisory lock key as the
// monotonic-revision trigger in 0029_team_escalation_policies.sql.
func lockTeamEscalationPolicy(ctx context.Context, tx pgx.Tx, organizationID, teamID domain.ID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('team_escalation_policy:' || $1::text || ':' || $2::text, 0))`,
		organizationID, teamID)
	return err
}
