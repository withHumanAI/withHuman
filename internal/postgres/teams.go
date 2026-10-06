package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/routing"
)

// recipientAddressesSQL gathers every address a person can be reached at:
// the account's own email, which sign-in already proved, plus each
// contact method they added and confirmed. Unverified methods are absent
// by construction, so an address nobody proved can never be delivered to.
const recipientAddressesSQL = `ARRAY(
    SELECT DISTINCT address FROM (
        SELECT u.primary_email AS address
        UNION
        SELECT cm.value FROM contact_methods cm
        WHERE cm.user_id = u.id AND cm.kind = 'email' AND cm.verified_at IS NOT NULL
    ) addresses
    WHERE address IS NOT NULL AND address <> ''
)`

// recipientSlackSQL is the person's Slack identity in this organization's
// workspace, if it has been linked.
const recipientSlackSQL = `COALESCE((SELECT cm.value FROM contact_methods cm
    WHERE cm.user_id = u.id AND cm.kind = 'slack' AND cm.organization_id = m.organization_id
      AND cm.verified_at IS NOT NULL AND EXISTS (SELECT 1 FROM slack_connections sc WHERE sc.organization_id = cm.organization_id AND sc.status = 'connected' AND sc.installation_id = cm.slack_installation_id) LIMIT 1), '')`

// recipientDiscordSQL is the person's Discord identity in this
// organization's server, if they have linked it.
const recipientDiscordSQL = `COALESCE((SELECT cm.value FROM contact_methods cm
    WHERE cm.user_id = u.id AND cm.kind = 'discord' AND cm.organization_id = m.organization_id
      AND cm.verified_at IS NOT NULL LIMIT 1), '')`

// teamDirectoryManagedSQL derives whether a team is directory managed: the
// flag is never stored, so removing the group link instantly returns the
// team to manual control.
const teamDirectoryManagedSQL = `EXISTS (SELECT 1 FROM directory_object_links l
    WHERE l.team_id = t.id AND l.provider_object_type = 'group')`

// teamEscalationPolicyStatusSQL summarizes the team's policy lineage as
// two nullable revisions: the active one and the latest one. Both are
// NULL for a team that never had a policy.
const teamEscalationPolicyStatusSQL = `(SELECT max(p.revision) FILTER (WHERE p.is_active) FROM team_escalation_policies p
    WHERE p.organization_id = t.organization_id AND p.team_id = t.id),
    (SELECT max(p.revision) FROM team_escalation_policies p
    WHERE p.organization_id = t.organization_id AND p.team_id = t.id)`

func (s *Store) CreateTeam(ctx context.Context, input application.CreateTeamInput) (domain.Team, error) {
	if err := input.Validate(); err != nil {
		return domain.Team{}, err
	}
	team := domain.Team{
		ID: domain.ID(uuid.NewString()), OrganizationID: input.OrganizationID,
		Name: input.Name, CreatedAt: input.Now,
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return domain.Team{}, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO teams (id, organization_id, name, created_at)
        VALUES ($1, $2, $3, $4)`, team.ID, team.OrganizationID, team.Name, input.Now)
	if isUniqueViolation(err) {
		return domain.Team{}, fmt.Errorf("team name already exists: %w", domain.ErrConflict)
	}
	if err != nil {
		return domain.Team{}, err
	}
	if err := s.insertTeamAudit(ctx, tx, input.OrganizationID, "team.created",
		input.ActorMembershipID, team.ID, map[string]any{"name": team.Name}, input.Now); err != nil {
		return domain.Team{}, err
	}
	return team, tx.Commit(ctx)
}

func (s *Store) ListTeams(ctx context.Context, input application.ListTeamsInput) ([]domain.Team, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `SELECT t.id::text, t.name, t.created_at, t.archived_at, `+teamDirectoryManagedSQL+`,
        (SELECT count(DISTINCT membership_id) FROM membership_teams WHERE team_id = t.id),
        `+teamEscalationPolicyStatusSQL+`
        FROM teams t WHERE t.organization_id = $1 AND ($3::boolean OR t.id = ANY($4::uuid[]))
        AND ($5 = 'all' OR ($5 = 'archived' AND t.archived_at IS NOT NULL) OR ($5 IN ('', 'live') AND t.archived_at IS NULL))
        ORDER BY t.name LIMIT $2`,
		input.OrganizationID, limit, input.Scope.All, input.Scope.IDs(domain.ScopeTeam), string(input.Status))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	teams := make([]domain.Team, 0)
	var teamIDs []domain.ID
	for rows.Next() {
		team := domain.Team{OrganizationID: input.OrganizationID}
		var id string
		if err := rows.Scan(&id, &team.Name, &team.CreatedAt, &team.ArchivedAt, &team.DirectoryManaged, &team.MemberCount,
			&team.EscalationPolicy.ActiveRevision, &team.EscalationPolicy.LatestRevision); err != nil {
			return nil, err
		}
		team.ID = domain.ID(id)
		teams = append(teams, team)
		teamIDs = append(teamIDs, team.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	assignments, err := teamAssignments(ctx, s.pool, input.OrganizationID, teamIDs)
	if err != nil {
		return nil, err
	}
	for index := range teams {
		teams[index].Assignments = assignments[teams[index].ID]
	}
	return teams, nil
}

func (s *Store) GetTeam(ctx context.Context, input application.GetTeamInput) (domain.Team, []application.TeamMemberRecord, error) {
	if err := input.Validate(); err != nil {
		return domain.Team{}, nil, err
	}
	team := domain.Team{ID: input.TeamID, OrganizationID: input.OrganizationID}
	var id string
	err := s.pool.QueryRow(ctx, `SELECT t.id::text, t.name, t.created_at, t.archived_at, `+teamDirectoryManagedSQL+`,
        (SELECT count(DISTINCT membership_id) FROM membership_teams WHERE team_id = t.id),
        `+teamEscalationPolicyStatusSQL+`
        FROM teams t WHERE t.organization_id = $1 AND t.id = $2`,
		input.OrganizationID, input.TeamID).Scan(&id, &team.Name, &team.CreatedAt, &team.ArchivedAt, &team.DirectoryManaged, &team.MemberCount,
		&team.EscalationPolicy.ActiveRevision, &team.EscalationPolicy.LatestRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Team{}, nil, domain.ErrNotFound
	}
	if err != nil {
		return domain.Team{}, nil, err
	}
	if team.Assignments, err = listAssignments(ctx, s.pool, input.OrganizationID, domain.PrincipalTeam, input.TeamID); err != nil {
		return domain.Team{}, nil, err
	}
	members, err := s.listTeamMembers(ctx, s.pool, input.OrganizationID, input.TeamID)
	if err != nil {
		return domain.Team{}, nil, err
	}
	return team, members, nil
}

// CountTeamMembersLacking counts the team's active members and those
// among them whose grants (their own assignments plus every team's they
// belong to) never carry the permission at organization scope. A grant
// pinned to a team or agent type does not count: an escalation target
// decides at organization scope or not at all.
func (s *Store) CountTeamMembersLacking(ctx context.Context, input application.CountTeamMembersLackingInput) (application.TeamMemberPermissionCount, error) {
	if err := input.Validate(); err != nil {
		return application.TeamMemberPermissionCount{}, err
	}
	var count application.TeamMemberPermissionCount
	err := s.pool.QueryRow(ctx, `SELECT count(DISTINCT m.id),
        count(DISTINCT m.id) FILTER (WHERE NOT EXISTS (
            SELECT 1 FROM role_assignments ra JOIN roles r ON r.id = ra.role_id
            WHERE ra.organization_id = t.organization_id AND ra.scope_kind = 'organization'
              AND $3 = ANY(r.permissions) AND `+membershipPrincipalSQL("m.id")+`))
        FROM teams t
        LEFT JOIN membership_teams mt ON mt.team_id = t.id
        LEFT JOIN memberships m ON m.id = mt.membership_id AND m.status = 'active'
        WHERE t.organization_id = $1 AND t.id = $2
        GROUP BY t.id`, input.OrganizationID, input.TeamID, string(input.Permission)).Scan(&count.MemberCount, &count.LackingCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.TeamMemberPermissionCount{}, domain.ErrNotFound
	}
	return count, err
}

func (s *Store) listTeamMembers(ctx context.Context, q querier, organizationID, teamID domain.ID) ([]application.TeamMemberRecord, error) {
	rows, err := q.Query(ctx, `SELECT m.id::text, u.display_name, u.primary_email, m.status,
        array_agg(DISTINCT mt.source ORDER BY mt.source)
        FROM membership_teams mt
        JOIN memberships m ON m.id = mt.membership_id
        JOIN users u ON u.id = m.user_id
        WHERE mt.organization_id = $1 AND mt.team_id = $2
        GROUP BY m.id, u.display_name, u.primary_email, m.status
        ORDER BY u.display_name, u.primary_email`, organizationID, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := make([]application.TeamMemberRecord, 0)
	for rows.Next() {
		var record application.TeamMemberRecord
		var membershipID, status string
		if err := rows.Scan(&membershipID, &record.DisplayName, &record.Email, &status, &record.Sources); err != nil {
			return nil, err
		}
		record.MembershipID = domain.ID(membershipID)
		record.Status = domain.MembershipStatus(status)
		members = append(members, record)
	}
	return members, rows.Err()
}

func (s *Store) RenameTeam(ctx context.Context, input application.RenameTeamInput) (domain.Team, error) {
	if err := input.Validate(); err != nil {
		return domain.Team{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return domain.Team{}, err
	}
	defer tx.Rollback(ctx)
	if err := requireLiveTeam(ctx, tx, input.OrganizationID, input.TeamID); err != nil {
		return domain.Team{}, err
	}
	var previousName string
	var directoryManaged bool
	err = tx.QueryRow(ctx, `SELECT t.name, `+teamDirectoryManagedSQL+`
        FROM teams t WHERE t.organization_id = $1 AND t.id = $2 FOR UPDATE OF t`,
		input.OrganizationID, input.TeamID).Scan(&previousName, &directoryManaged)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Team{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Team{}, err
	}
	if directoryManaged {
		return domain.Team{}, fmt.Errorf("this team is managed by your directory: %w", domain.ErrConflict)
	}
	_, err = tx.Exec(ctx, `UPDATE teams SET name = $3 WHERE organization_id = $1 AND id = $2`,
		input.OrganizationID, input.TeamID, input.Name)
	if isUniqueViolation(err) {
		return domain.Team{}, fmt.Errorf("team name already exists: %w", domain.ErrConflict)
	}
	if err != nil {
		return domain.Team{}, err
	}
	if err := s.insertTeamAudit(ctx, tx, input.OrganizationID, "team.renamed",
		input.ActorMembershipID, input.TeamID,
		map[string]any{"before": previousName, "after": input.Name}, input.Now); err != nil {
		return domain.Team{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Team{}, err
	}
	team, _, err := s.GetTeam(ctx, application.GetTeamInput{OrganizationID: input.OrganizationID, TeamID: input.TeamID})
	return team, err
}

// teamTargetedByActivePath reports whether any active escalation path
// revision references the team as a routing target. Targets carry no
// foreign key, so the check walks the documents.
func teamTargetedByActivePath(ctx context.Context, tx pgx.Tx, organizationID, teamID domain.ID) (bool, error) {
	var targeted bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM escalation_paths
        WHERE organization_id = $1 AND is_active
        AND jsonb_path_exists(document, '$.** ? (@.type == "team" && @.id == $tid)',
            jsonb_build_object('tid', $2::text)))`, organizationID, teamID).Scan(&targeted)
	return targeted, err
}

// teamTargetedByPendingRequest reports whether the pinned path revision of
// any still-pending approval request references the team, or whether such
// a request pinned one of the team's policy revisions. Decide authorization
// and the escalation executor read pinned documents, so those teams must
// outlive the requests.
func teamTargetedByPendingRequest(ctx context.Context, tx pgx.Tx, organizationID, teamID domain.ID) (bool, error) {
	var targeted bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM approval_requests r
        JOIN approval_request_routing arr ON arr.organization_id = r.organization_id AND arr.request_id = r.id
        JOIN escalation_paths ep ON ep.id = arr.escalation_path_revision_id AND ep.organization_id = arr.organization_id
        WHERE r.organization_id = $1 AND r.status = 'pending'
        AND jsonb_path_exists(ep.document, '$.** ? (@.type == "team" && @.id == $tid)',
            jsonb_build_object('tid', $2::text)))
        OR EXISTS (SELECT 1 FROM approval_requests r
        JOIN approval_request_routing_team_policies tp ON tp.organization_id = r.organization_id AND tp.request_id = r.id
        WHERE r.organization_id = $1 AND r.status = 'pending' AND tp.team_id = $2::uuid)`, organizationID, teamID).Scan(&targeted)
	return targeted, err
}

func (s *Store) ArchiveTeam(ctx context.Context, input application.ArchiveTeamInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var name string
	var archivedAt *time.Time
	var directoryManaged bool
	err = tx.QueryRow(ctx, `SELECT t.name, t.archived_at, `+teamDirectoryManagedSQL+`
        FROM teams t WHERE t.organization_id = $1 AND t.id = $2 FOR UPDATE OF t`,
		input.OrganizationID, input.TeamID).Scan(&name, &archivedAt, &directoryManaged)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if archivedAt != nil {
		return tx.Commit(ctx)
	}
	if directoryManaged {
		return fmt.Errorf("this team is managed by your directory: %w", domain.ErrConflict)
	}
	if targeted, err := teamTargetedByActivePath(ctx, tx, input.OrganizationID, input.TeamID); err != nil {
		return err
	} else if targeted {
		return fmt.Errorf("this team is in an active escalation path: %w", domain.ErrConflict)
	}
	if targeted, err := teamTargetedByPendingRequest(ctx, tx, input.OrganizationID, input.TeamID); err != nil {
		return err
	} else if targeted {
		return fmt.Errorf("this team has pending requests: %w", domain.ErrConflict)
	}
	// The team's own grants and every grant scoped to it go with it.
	if err := s.revokeAssignmentsWhere(ctx, tx, input.OrganizationID, domain.ActorHuman, input.ActorMembershipID, input.Now,
		`((ra.principal_kind = 'team' AND ra.principal_id = $2) OR (ra.scope_kind = 'team' AND ra.scope_id = $2::text))`,
		input.TeamID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE teams SET archived_at = $3 WHERE organization_id = $1 AND id = $2`,
		input.OrganizationID, input.TeamID, input.Now); err != nil {
		return err
	}
	// The team's policy revisions stay as history (pinned requests may
	// still read them), but nothing may keep running for a team that no
	// longer operates, so the active one is archived with the team.
	if err := s.deactivateTeamEscalationPolicyForArchive(ctx, tx, input.OrganizationID, input.TeamID, domain.ActorHuman, input.ActorMembershipID, input.Now); err != nil {
		return err
	}
	if err := s.insertTeamAudit(ctx, tx, input.OrganizationID, "team.archived",
		input.ActorMembershipID, input.TeamID, map[string]any{"name": name}, input.Now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// deactivateTeamEscalationPolicyForArchive archives the team's active policy
// revision, if any, and audits the archiving with the reason so the
// trail explains a deactivation nobody asked for by hand.
func (s *Store) deactivateTeamEscalationPolicyForArchive(ctx context.Context, tx pgx.Tx, organizationID, teamID domain.ID, actorType domain.ActorType, actorMembershipID domain.ID, now time.Time) error {
	if err := lockTeamEscalationPolicy(ctx, tx, organizationID, teamID); err != nil {
		return err
	}
	var revisionID string
	var revision int64
	err := tx.QueryRow(ctx, `UPDATE team_escalation_policies SET is_active = false
        WHERE organization_id = $1 AND team_id = $2 AND is_active
        RETURNING id::text, revision`, organizationID, teamID).Scan(&revisionID, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]any{"team_id": teamID, "revision": revision, "reason": "team_archived"})
	return s.appendAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: organizationID, EventType: "team_escalation_policy.deactivated",
		ActorType: actorType, ActorID: actorMembershipID,
		SubjectType: "team_escalation_policy", SubjectID: domain.ID(revisionID),
		Data: data, OccurredAt: now,
	})
}

// SetTeamManualMembers replaces the team's manually assigned member set.
// Directory-sourced rows are untouched; effective membership is the union.
func (s *Store) SetTeamManualMembers(ctx context.Context, input application.SetTeamManualMembersInput) ([]application.TeamMemberRecord, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := requireLiveTeam(ctx, tx, input.OrganizationID, input.TeamID); err != nil {
		return nil, err
	}
	requested := make([]string, len(input.MembershipIDs))
	for index, membershipID := range input.MembershipIDs {
		requested[index] = string(membershipID)
	}
	var known int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM memberships
        WHERE organization_id = $1 AND id = ANY($2::uuid[])`,
		input.OrganizationID, requested).Scan(&known); err != nil {
		return nil, err
	}
	if known != len(requested) {
		return nil, fmt.Errorf("membership IDs must belong to this organization: %w", domain.ErrInvalid)
	}
	var previous []string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(array_agg(membership_id::text ORDER BY membership_id::text), '{}')
        FROM membership_teams WHERE organization_id = $1 AND team_id = $2 AND source = 'manual'`,
		input.OrganizationID, input.TeamID).Scan(&previous); err != nil {
		return nil, err
	}
	added, removed := diffIDSets(previous, requested)
	auditData := map[string]any{"added": added, "removed": removed, "source": "manual"}
	// Removing someone the team's active policy still names leaves that
	// level pointing at nobody on the team. Refuse unless forced, and then
	// record which revision the actor accepted leaving dangling.
	if len(removed) > 0 {
		policy, hasPolicy, err := activeTeamEscalationPolicyRevision(ctx, tx, input.OrganizationID, input.TeamID)
		if err != nil {
			return nil, err
		}
		if hasPolicy {
			stranded, err := policyTargetsLeavingTeam(ctx, tx, policy, input.OrganizationID, input.TeamID, removed)
			if err != nil {
				return nil, err
			}
			if len(stranded) > 0 {
				if !input.Force {
					return nil, domain.TeamMemberInEscalationPolicyError{
						TeamID: input.TeamID, Revision: policy.Revision, MembershipIDs: stranded,
					}
				}
				auditData["forced"] = true
				auditData["escalation_policy_revision"] = policy.Revision
			}
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM membership_teams
        WHERE organization_id = $1 AND team_id = $2 AND source = 'manual' AND NOT (membership_id = ANY($3::uuid[]))`,
		input.OrganizationID, input.TeamID, requested); err != nil {
		return nil, err
	}
	for _, membershipID := range input.MembershipIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO membership_teams (membership_id, team_id, organization_id, source, source_key)
            VALUES ($1, $2, $3, 'manual', '') ON CONFLICT DO NOTHING`,
			membershipID, input.TeamID, input.OrganizationID); err != nil {
			return nil, err
		}
	}
	if err := s.insertTeamAudit(ctx, tx, input.OrganizationID, "team.members_changed",
		input.ActorMembershipID, input.TeamID, auditData, input.Now); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.listTeamMembers(ctx, s.pool, input.OrganizationID, input.TeamID)
}

// policyTargetsLeavingTeam lists the user targets of the team's policy
// among the manual members being removed that will have no
// membership_teams row for the team afterwards: a directory-sourced row
// keeps such a person a member, so only the manual rows count as leaving.
// A target the policy names that is already off the team is not this
// change's doing and never blocks it.
func policyTargetsLeavingTeam(ctx context.Context, tx pgx.Tx, policy domain.TeamEscalationPolicyRevision, organizationID, teamID domain.ID, removed []string) ([]domain.ID, error) {
	var document routing.TeamPolicyDocument
	if err := routing.Decode(policy.Document, &document); err != nil {
		return nil, fmt.Errorf("active team escalation policy document is unreadable: %w", err)
	}
	targets := document.UserTargetIDs()
	if len(targets) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `SELECT target::text FROM unnest($3::uuid[]) AS target
        WHERE target = ANY($4::uuid[])
        AND NOT EXISTS (SELECT 1 FROM membership_teams mt
            WHERE mt.organization_id = $1 AND mt.team_id = $2 AND mt.membership_id = target AND mt.source <> 'manual')
        ORDER BY target::text`, organizationID, teamID, targets, removed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stranded := make([]domain.ID, 0)
	for rows.Next() {
		var membershipID string
		if err := rows.Scan(&membershipID); err != nil {
			return nil, err
		}
		stranded = append(stranded, domain.ID(membershipID))
	}
	return stranded, rows.Err()
}

func diffIDSets(previous, requested []string) (added, removed []string) {
	previousSet := make(map[string]struct{}, len(previous))
	for _, id := range previous {
		previousSet[id] = struct{}{}
	}
	requestedSet := make(map[string]struct{}, len(requested))
	for _, id := range requested {
		requestedSet[id] = struct{}{}
	}
	added, removed = make([]string, 0), make([]string, 0)
	for _, id := range requested {
		if _, existed := previousSet[id]; !existed {
			added = append(added, id)
		}
	}
	for _, id := range previous {
		if _, kept := requestedSet[id]; !kept {
			removed = append(removed, id)
		}
	}
	return added, removed
}

func (s *Store) insertTeamAudit(ctx context.Context, tx execer, organizationID domain.ID, eventType string, actorMembershipID, teamID domain.ID, data map[string]any, now time.Time) error {
	payload, _ := json.Marshal(data)
	return s.appendAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: organizationID, EventType: eventType,
		ActorType: domain.ActorHuman, ActorID: actorMembershipID,
		SubjectType: "team", SubjectID: teamID,
		Data: payload, OccurredAt: now,
	})
}

// ResolveRoutingTargets reports which of the given routing targets do not
// resolve: user targets must be active memberships of the organization and
// team targets must be live teams.
func (s *Store) ResolveRoutingTargets(ctx context.Context, input application.ResolveRoutingTargetsInput) (application.ResolvedRoutingTargets, error) {
	if err := input.Validate(); err != nil {
		return application.ResolvedRoutingTargets{}, err
	}
	result := application.ResolvedRoutingTargets{
		MissingUsers: make([]domain.ID, 0), MissingTeams: make([]domain.ID, 0),
	}
	if len(input.UserMembershipIDs) > 0 {
		var found []string
		if err := s.pool.QueryRow(ctx, `SELECT COALESCE(array_agg(id::text), '{}') FROM memberships
            WHERE organization_id = $1 AND status = 'active' AND id = ANY($2::uuid[])`,
			input.OrganizationID, idStrings(input.UserMembershipIDs)).Scan(&found); err != nil {
			return application.ResolvedRoutingTargets{}, err
		}
		result.MissingUsers = missingIDs(input.UserMembershipIDs, found)
	}
	if len(input.TeamIDs) > 0 {
		var found []string
		if err := s.pool.QueryRow(ctx, `SELECT COALESCE(array_agg(id::text), '{}') FROM teams
            WHERE organization_id = $1 AND archived_at IS NULL AND id = ANY($2::uuid[])`,
			input.OrganizationID, idStrings(input.TeamIDs)).Scan(&found); err != nil {
			return application.ResolvedRoutingTargets{}, err
		}
		result.MissingTeams = missingIDs(input.TeamIDs, found)
	}
	return result, nil
}

func missingIDs(requested []domain.ID, found []string) []domain.ID {
	foundSet := make(map[string]struct{}, len(found))
	for _, id := range found {
		foundSet[id] = struct{}{}
	}
	missing := make([]domain.ID, 0)
	for _, id := range requested {
		if _, ok := foundSet[string(id)]; !ok {
			missing = append(missing, id)
		}
	}
	return missing
}

// ResolveApprovalRecipients expands a request into the people to notify
// all at once, with the contact details delivery needs. It is the plan
// for requests the per-level executor does not walk.
//
// An agent_owner snapshot resolves to the owner plus the added reviewers
// until a handoff. An escalated routing snapshot resolves to the targets
// the pinned path reaches under the pinned facts plus the reviewers added
// to the request since, with team membership resolved at call time; a path that reaches
// nobody resolves to the added reviewers alone. A request that fell to
// the default queue, or that carries no snapshot at all, resolves to
// every active member who may decide: the queue's promise is that the
// request waits visibly rather than silently, which is only true if
// somebody is actually told.
func (s *Store) ResolveApprovalRecipients(ctx context.Context, input application.ResolveApprovalRecipientsInput) (application.ApprovalRecipients, error) {
	if err := input.Validate(); err != nil {
		return application.ApprovalRecipients{}, err
	}
	pinned, escalated, err := readPinnedRouting(ctx, s.pool, input.OrganizationID, input.RequestID)
	if err != nil {
		return application.ApprovalRecipients{}, err
	}
	urgency := pinned.urgency
	if urgency == "" {
		urgency = string(domain.NotificationStandard)
	}
	recipients := application.ApprovalRecipients{
		Outcome:    domain.RoutingDefaultQueue,
		Urgency:    domain.NotificationUrgency(urgency),
		Recipients: make([]application.NotificationRecipient, 0),
	}
	if !escalated && pinned.agentOwnerID == "" {
		fallback, err := s.eligibleApprovalRecipients(ctx, input.OrganizationID)
		if err != nil {
			return application.ApprovalRecipients{}, err
		}
		recipients.Recipients = fallback
		return recipients, s.attachRecipientMethods(ctx, input.OrganizationID, recipients.Recipients)
	}
	recipients.Outcome = domain.RoutingEscalated
	if pinned.agentOwnerID != "" {
		recipients.Outcome = domain.RoutingAgentOwner
	}
	userIDs, teamIDs, err := pinnedTargets(pinned)
	if err != nil {
		return application.ApprovalRecipients{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT m.id::text, u.id::text, u.display_name, `+recipientAddressesSQL+`, `+recipientSlackSQL+`, `+recipientDiscordSQL+`
        FROM memberships m JOIN users u ON u.id = m.user_id
        WHERE m.organization_id = $1 AND m.status = 'active' AND u.status = 'active'
        AND (m.id = ANY($2::uuid[]) OR m.id IN (
            SELECT mt.membership_id FROM active_membership_teams mt
            WHERE mt.organization_id = $1 AND mt.team_id = ANY($3::uuid[]))
          OR m.id IN (
            SELECT arr.membership_id FROM approval_request_reviewers arr
            WHERE arr.organization_id = $1 AND arr.request_id = $4))
        ORDER BY u.display_name`,
		input.OrganizationID, idStrings(userIDs), idStrings(teamIDs), input.RequestID)
	if err != nil {
		return application.ApprovalRecipients{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var membershipID, userID, displayName, slackUserID, discordUserID string
		var addresses []string
		if err := rows.Scan(&membershipID, &userID, &displayName, &addresses, &slackUserID, &discordUserID); err != nil {
			return application.ApprovalRecipients{}, err
		}
		recipients.Recipients = append(recipients.Recipients, application.NotificationRecipient{
			MembershipID: domain.ID(membershipID), UserID: domain.ID(userID), DisplayName: displayName,
			Emails: addresses, SlackUserID: slackUserID, DiscordUserID: discordUserID,
		})
	}
	if err := rows.Err(); err != nil {
		return application.ApprovalRecipients{}, err
	}
	return recipients, s.attachRecipientMethods(ctx, input.OrganizationID, recipients.Recipients)
}

// attachRecipientMethods fills each recipient's keyed method set: the
// account email under an empty id, then every confirmed method that is
// person-wide or scoped to this organization. Rules name these ids.
func (s *Store) attachRecipientMethods(ctx context.Context, organizationID domain.ID, recipients []application.NotificationRecipient) error {
	if len(recipients) == 0 {
		return nil
	}
	byUser := make(map[domain.ID]int, len(recipients))
	userIDs := make([]domain.ID, 0, len(recipients))
	for index, recipient := range recipients {
		byUser[recipient.UserID] = index
		userIDs = append(userIDs, recipient.UserID)
	}
	rows, err := s.pool.Query(ctx, `SELECT u.id::text, '' AS id, 'email', u.primary_email, '' AS slack_installation_id, false AS unavailable FROM users u WHERE u.id = ANY($1::uuid[])
        UNION ALL
        SELECT cm.user_id::text, cm.id::text, cm.kind, cm.value, COALESCE(cm.slack_installation_id::text, ''),
        cm.kind = 'slack' AND NOT EXISTS (SELECT 1 FROM slack_connections sc WHERE sc.organization_id = cm.organization_id AND sc.status = 'connected' AND sc.installation_id = cm.slack_installation_id) FROM contact_methods cm
        WHERE cm.user_id = ANY($1::uuid[]) AND cm.verified_at IS NOT NULL
          AND (cm.organization_id IS NULL OR cm.organization_id = $2)`, idStrings(userIDs), organizationID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var userID, id, kind, address, installationID string
		var unavailable bool
		if err := rows.Scan(&userID, &id, &kind, &address, &installationID, &unavailable); err != nil {
			return err
		}
		index, known := byUser[domain.ID(userID)]
		if !known {
			continue
		}
		recipients[index].Methods = append(recipients[index].Methods, application.RecipientMethod{
			ID: domain.ID(id), Kind: domain.ContactMethodKind(kind), Address: address, SlackInstallationID: domain.ID(installationID), Unavailable: unavailable,
		})
	}
	return rows.Err()
}

// eligibleApprovalRecipients lists every active member whose role
// assignments, direct or through a team, grant request.decide at any
// scope. The permission is read from the role rows, so a role gaining or
// losing it changes who gets notified without a code change.
func (s *Store) eligibleApprovalRecipients(ctx context.Context, organizationID domain.ID) ([]application.NotificationRecipient, error) {
	rows, err := s.pool.Query(ctx, `SELECT m.id::text, u.id::text, u.display_name, `+recipientAddressesSQL+`, `+recipientSlackSQL+`, `+recipientDiscordSQL+`
        FROM memberships m JOIN users u ON u.id = m.user_id
        WHERE m.organization_id = $1 AND m.status = 'active' AND u.status = 'active'
          AND EXISTS (SELECT 1 FROM role_assignments ra JOIN roles r ON r.id = ra.role_id
              WHERE ra.organization_id = m.organization_id AND $2 = ANY(r.permissions)
                AND `+membershipPrincipalSQL("m.id")+`)
        ORDER BY u.display_name`, organizationID, string(domain.PermissionRequestDecide))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	recipients := make([]application.NotificationRecipient, 0)
	for rows.Next() {
		var membershipID, userID, displayName, slackUserID, discordUserID string
		var addresses []string
		if err := rows.Scan(&membershipID, &userID, &displayName, &addresses, &slackUserID, &discordUserID); err != nil {
			return nil, err
		}
		recipients = append(recipients, application.NotificationRecipient{
			MembershipID: domain.ID(membershipID), UserID: domain.ID(userID), DisplayName: displayName,
			Emails: addresses, SlackUserID: slackUserID, DiscordUserID: discordUserID,
		})
	}
	return recipients, rows.Err()
}

// requireLiveTeam serializes writes with archiving while keeping archived
// records readable through GetTeam.
func requireLiveTeam(ctx context.Context, tx pgx.Tx, organizationID, teamID domain.ID) error {
	var archivedAt *time.Time
	err := tx.QueryRow(ctx, `SELECT archived_at FROM teams
        WHERE organization_id = $1 AND id = $2 FOR UPDATE`, organizationID, teamID).Scan(&archivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if archivedAt != nil {
		return fmt.Errorf("this team is archived: %w", domain.ErrConflict)
	}
	return nil
}
