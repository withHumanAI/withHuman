package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
)

func membershipActorID(ctx context.Context, tx pgx.Tx, organizationID, membershipID domain.ID) (domain.ID, error) {
	var actorID string
	err := tx.QueryRow(ctx, `SELECT user_id::text FROM memberships
		WHERE organization_id = $1 AND id = $2`, organizationID, membershipID).Scan(&actorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	return domain.ID(actorID), err
}

func isLastOwnerViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "P0001" &&
		strings.Contains(pgErr.Message, "must retain at least one active owner")
}

func (s *Store) ListMembers(ctx context.Context, input application.ListMembersInput) ([]application.MemberRecord, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `SELECT m.id::text, m.user_id::text, m.status, m.admission_source,
        m.directory_managed, m.created_at, m.updated_at,
        COALESCE((SELECT array_agg(DISTINCT team_id::text ORDER BY team_id::text) FROM active_membership_teams WHERE membership_id = m.id), '{}'),
        u.display_name, u.primary_email
        FROM memberships m JOIN users u ON u.id = m.user_id
        WHERE m.organization_id = $1
        ORDER BY u.display_name, u.primary_email LIMIT $2`, input.OrganizationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := make([]application.MemberRecord, 0)
	for rows.Next() {
		var record application.MemberRecord
		var id, userID, status, source string
		var teamIDs []string
		if err := rows.Scan(&id, &userID, &status, &source, &record.Membership.DirectoryManaged,
			&record.Membership.CreatedAt, &record.Membership.UpdatedAt,
			&teamIDs, &record.DisplayName, &record.Email); err != nil {
			return nil, err
		}
		record.Membership.ID, record.Membership.UserID = domain.ID(id), domain.ID(userID)
		record.Membership.OrganizationID = input.OrganizationID
		record.Membership.Status = domain.MembershipStatus(status)
		record.Membership.AdmissionSource = domain.AdmissionSource(source)
		record.Membership.TeamIDs = make([]domain.ID, len(teamIDs))
		for index, teamID := range teamIDs {
			record.Membership.TeamIDs[index] = domain.ID(teamID)
		}
		members = append(members, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// One query resolves every member's assignments rather than one per
	// row.
	membershipIDs := make([]domain.ID, len(members))
	for index, member := range members {
		membershipIDs[index] = member.Membership.ID
	}
	assignments, err := membershipAssignments(ctx, s.pool, input.OrganizationID, membershipIDs)
	if err != nil {
		return nil, err
	}
	for index := range members {
		members[index].Membership.Assignments = assignments[members[index].Membership.ID]
	}
	return members, nil
}

// SetMembershipStatus suspends, reactivates, or deprovisions a membership.
// Directory-managed memberships are refused: the directory pipeline owns
// their lifecycle. Deprovisioning never deletes the row because decisions
// and audit events reference it.
func (s *Store) SetMembershipStatus(ctx context.Context, input application.SetMembershipStatusInput) (domain.Membership, error) {
	if err := input.Validate(); err != nil {
		return domain.Membership{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return domain.Membership{}, err
	}
	defer tx.Rollback(ctx)
	var userID, currentStatus string
	var directoryManaged bool
	err = tx.QueryRow(ctx, `SELECT user_id::text, status, directory_managed FROM memberships
        WHERE organization_id = $1 AND id = $2 FOR UPDATE`,
		input.OrganizationID, input.MembershipID).Scan(&userID, &currentStatus, &directoryManaged)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Membership{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Membership{}, err
	}
	if directoryManaged {
		return domain.Membership{}, fmt.Errorf("this member is managed by your directory: %w", domain.ErrConflict)
	}
	if domain.MembershipStatus(currentStatus) == domain.MembershipDeprovisioned {
		return domain.Membership{}, fmt.Errorf("this member was removed: %w", domain.ErrConflict)
	}
	// Taking a member out of service leaves every active path that targets
	// them pointing at nobody. Refuse unless the actor forces it, and then
	// record which paths they accepted leaving dangling.
	auditData := map[string]any{"previous_status": currentStatus}
	if input.Status != domain.MembershipActive {
		paths, err := activePathsTargetingMembership(ctx, tx, input.OrganizationID, input.MembershipID)
		if err != nil {
			return domain.Membership{}, err
		}
		teamPolicies, err := activeTeamPoliciesTargetingMembership(ctx, tx, input.OrganizationID, input.MembershipID)
		if err != nil {
			return domain.Membership{}, err
		}
		if len(paths) > 0 || len(teamPolicies) > 0 {
			if !input.Force {
				return domain.Membership{}, domain.MembershipInEscalationPathError{
					MembershipID: input.MembershipID, Paths: paths, TeamPolicies: teamPolicies,
				}
			}
			keys := make([]string, 0, len(paths))
			for _, path := range paths {
				keys = append(keys, path.PathKey)
			}
			teamIDs := make([]domain.ID, 0, len(teamPolicies))
			for _, policy := range teamPolicies {
				teamIDs = append(teamIDs, policy.TeamID)
			}
			auditData["forced"] = true
			auditData["escalation_paths"] = keys
			auditData["team_escalation_policies"] = teamIDs
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE memberships SET status = $3, updated_at = $4
        WHERE organization_id = $1 AND id = $2`,
		input.OrganizationID, input.MembershipID, string(input.Status), input.Now); err != nil {
		if isLastOwnerViolation(err) {
			return domain.Membership{}, fmt.Errorf("the organization needs at least one active owner: %w", domain.ErrConflict)
		}
		return domain.Membership{}, err
	}
	if input.RevokeSessions {
		if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at = $3
            WHERE organization_id = $1 AND user_id = $2 AND revoked_at IS NULL`,
			input.OrganizationID, userID, input.Now); err != nil {
			return domain.Membership{}, err
		}
	}
	if input.Status != domain.MembershipActive {
		actorID, err := membershipActorID(ctx, tx, input.OrganizationID, input.ActorMembershipID)
		if err != nil {
			return domain.Membership{}, err
		}
		// A member who can no longer decide hands the requests routed to
		// them as their agent's owner to each request's fallback path.
		if err := s.handOffAgentOwnerRequests(ctx, tx, input.OrganizationID, input.MembershipID, domain.ActorHuman, actorID, input.Now); err != nil {
			return domain.Membership{}, err
		}
		// Removal takes the member's personal API keys with them: keys are
		// part of "signed out everywhere". Suspension leaves them, because
		// resolution refuses an inactive membership and reactivation brings
		// them back.
		if input.Status == domain.MembershipDeprovisioned {
			if err := s.revokeMembershipAPIKeys(ctx, tx, input.OrganizationID, input.MembershipID, domain.ActorHuman, actorID, input.Now); err != nil {
				return domain.Membership{}, err
			}
			if err := s.disableMembershipAgentInstances(ctx, tx, input.OrganizationID, input.MembershipID, domain.ActorHuman, actorID, input.Now); err != nil {
				return domain.Membership{}, err
			}
		}
	}
	eventType := map[domain.MembershipStatus]string{
		domain.MembershipActive:        "membership.reactivated",
		domain.MembershipSuspended:     "membership.suspended",
		domain.MembershipDeprovisioned: "membership.deprovisioned",
	}[input.Status]
	if err := s.insertMembershipAudit(ctx, tx, input.OrganizationID, eventType,
		input.ActorMembershipID, input.MembershipID, auditData, input.Now); err != nil {
		return domain.Membership{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		if isLastOwnerViolation(err) {
			return domain.Membership{}, fmt.Errorf("the organization needs at least one active owner: %w", domain.ErrConflict)
		}
		return domain.Membership{}, err
	}
	return s.GetMembership(ctx, application.GetMembershipInput{
		OrganizationID: input.OrganizationID, MembershipID: input.MembershipID,
	})
}

// activePathsTargetingMembership lists the active escalation path
// revisions that name the membership as a user target, in path key order.
// Targets carry no foreign key, so the check walks the documents, exactly
// as the team archive guard does.
func activePathsTargetingMembership(ctx context.Context, tx pgx.Tx, organizationID, membershipID domain.ID) ([]domain.EscalationPathReference, error) {
	rows, err := tx.Query(ctx, `SELECT path_key, name, revision FROM escalation_paths
        WHERE organization_id = $1 AND is_active
        AND jsonb_path_exists(document, '$.** ? (@.type == "user" && @.id == $mid)',
            jsonb_build_object('mid', $2::text))
        ORDER BY path_key`, organizationID, membershipID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paths := make([]domain.EscalationPathReference, 0)
	for rows.Next() {
		var path domain.EscalationPathReference
		if err := rows.Scan(&path.PathKey, &path.Name, &path.Revision); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, rows.Err()
}

// activeTeamPoliciesTargetingMembership lists the active team escalation
// policy revisions that name the membership as a user target, in team
// name order, walking the documents as the path guard does.
func activeTeamPoliciesTargetingMembership(ctx context.Context, tx pgx.Tx, organizationID, membershipID domain.ID) ([]domain.TeamEscalationPolicyReference, error) {
	rows, err := tx.Query(ctx, `SELECT p.team_id::text, COALESCE(t.name, ''), p.revision FROM team_escalation_policies p
        LEFT JOIN teams t ON t.id = p.team_id AND t.organization_id = p.organization_id
        WHERE p.organization_id = $1 AND p.is_active
        AND jsonb_path_exists(p.document, '$.** ? (@.type == "user" && @.id == $mid)',
            jsonb_build_object('mid', $2::text))
        ORDER BY t.name, p.team_id`, organizationID, membershipID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	policies := make([]domain.TeamEscalationPolicyReference, 0)
	for rows.Next() {
		var policy domain.TeamEscalationPolicyReference
		var teamID string
		if err := rows.Scan(&teamID, &policy.TeamName, &policy.Revision); err != nil {
			return nil, err
		}
		policy.TeamID = domain.ID(teamID)
		policies = append(policies, policy)
	}
	return policies, rows.Err()
}
