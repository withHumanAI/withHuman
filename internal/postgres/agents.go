package postgres

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
)

func (s *Store) ResolveAgentCredential(ctx context.Context, input application.ResolveAgentCredentialInput) (domain.Principal, error) {
	if err := input.Validate(); err != nil {
		return domain.Principal{}, err
	}
	credentialID, secretHash, now := input.CredentialID, input.SecretHash, input.Now
	var organizationID, agentID, instanceID, agentSlug, agentStatus, instanceStatus string
	var ownerID, ownerStatus sql.NullString
	var storedHash []byte
	err := s.pool.QueryRow(ctx, `SELECT c.organization_id::text, a.id::text, ai.id::text, a.slug, c.secret_hash,
        a.status, ai.status, ai.membership_id::text, m.status
      FROM agent_credentials c
      JOIN agent_instances ai ON ai.id = c.agent_instance_id AND ai.organization_id = c.organization_id
      JOIN agents a ON a.id = ai.agent_id AND a.organization_id = ai.organization_id
      JOIN organizations o ON o.id = a.organization_id
      LEFT JOIN memberships m ON m.id = ai.membership_id AND m.organization_id = ai.organization_id
     WHERE c.id = $1 AND c.revoked_at IS NULL AND c.expires_at > $2 AND o.status = 'active'`, credentialID, now).Scan(
		&organizationID, &agentID, &instanceID, &agentSlug, &storedHash, &agentStatus, &instanceStatus, &ownerID, &ownerStatus,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	if err != nil {
		return domain.Principal{}, err
	}
	if subtle.ConstantTimeCompare(storedHash, secretHash) != 1 {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	if domain.AgentStatus(agentStatus) != domain.AgentActive || domain.AgentStatus(instanceStatus) != domain.AgentActive {
		return domain.Principal{}, domain.ErrAgentInactive
	}
	// A personal instance acts for its owner and stops with them: a
	// suspended or removed member's instances refuse to resolve, the way
	// their personal API keys do.
	if ownerID.Valid && domain.MembershipStatus(ownerStatus.String) != domain.MembershipActive {
		return domain.Principal{}, domain.ErrAgentInactive
	}
	// An agent's grants are its assignments like any other principal's,
	// typically the built-in agent role at organization scope.
	grants, err := loadGrants(ctx, s.pool, domain.ID(organizationID), domain.PrincipalAgent, domain.ID(agentID))
	if err != nil {
		return domain.Principal{}, err
	}
	_, _ = s.pool.Exec(ctx, `UPDATE agent_credentials SET last_used_at = $2 WHERE id = $1`, credentialID, now)
	_, _ = s.pool.Exec(ctx, `UPDATE agent_instances SET last_seen_at = $2 WHERE id = $1`, instanceID, now)
	return domain.Principal{
		ActorType: domain.ActorAgent, ActorID: domain.ID(agentID), AgentInstanceID: domain.ID(instanceID),
		AgentSlug:      domain.AgentSlug(agentSlug),
		MembershipID:   domain.ID(ownerID.String),
		OrganizationID: domain.ID(organizationID),
		Auth:           domain.AuthContext{Method: "bearer", Assurance: "machine", AuthenticatedAt: now, CredentialID: credentialID},
	}.WithGrants(grants), nil
}

func (s *Store) CreateAgent(ctx context.Context, input application.CreateAgentInput) (domain.Agent, error) {
	if err := input.Validate(); err != nil {
		return domain.Agent{}, err
	}
	organizationID, membershipID := input.OrganizationID, input.ActorID
	name, agentSlug, now := input.Name, input.AgentSlug, input.Now
	agent := domain.Agent{
		ID: domain.ID(uuid.NewString()), OrganizationID: organizationID, CreatedByActorID: membershipID,
		Name: name, AgentSlug: agentSlug, Kind: input.Kind, Status: domain.AgentActive,
		CreatedAt: now, UpdatedAt: now,
	}
	err := retrySerializable(func() error {
		return s.createAgent(ctx, input, agent)
	})
	return agent, err
}

// createAgent writes the agent row and the built-in agent role assignment
// that makes it a principal, inside the allocating transaction.
func (s *Store) createAgent(ctx context.Context, input application.CreateAgentInput, agent domain.Agent) error {
	organizationID, membershipID := input.OrganizationID, input.ActorID
	name, agentSlug, now := input.Name, input.AgentSlug, input.Now
	alloc, err := s.beginAllocation(ctx, application.DimensionAgents, string(organizationID), input.AgentQuota)
	if err != nil {
		return err
	}
	defer alloc.Close(ctx)
	tx := alloc.Tx()
	if err := enforceAgentQuota(ctx, tx, string(organizationID), input.AgentQuota); err != nil {
		return err
	}
	if err := refuseTakenAgentIdentity(ctx, tx, organizationID, agentSlug, name); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO agents
		(id, organization_id, created_by_actor_id, slug, name, kind, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'active', $7, $7)`, agent.ID, organizationID, membershipID,
		agentSlug, name, string(input.Kind), now); err != nil {
		return agentIdentityConflict(err)
	}
	data, _ := json.Marshal(map[string]any{"agent_slug": agentSlug, "name": name, "kind": input.Kind})
	if err := s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{OrganizationID: organizationID, EventType: "agent.created", ActorType: domain.ActorHuman, ActorID: membershipID, SubjectType: "agent", SubjectID: agent.ID, Data: data, OccurredAt: now}); err != nil {
		return err
	}

	if err := s.grantAgentRole(ctx, tx, organizationID, agent.ID, membershipID, now); err != nil {
		return err
	}
	return alloc.Commit(ctx)
}

// grantAgentRole gives a new agent the built-in agent role at organization
// scope, the assignment every agent identity holds and the one its
// credentials resolve their grants from.
func (s *Store) grantAgentRole(ctx context.Context, tx querier, organizationID, agentID, membershipID domain.ID, now time.Time) error {
	role, err := lookupRole(ctx, tx, organizationID, domain.RoleAgentID)
	if err != nil {
		return err
	}
	var actorType domain.ActorType
	var auditID domain.ID
	if err := tx.QueryRow(ctx, `SELECT CASE WHEN a.api_key_id IS NULL THEN 'human' ELSE 'api_key' END, COALESCE(a.api_key_id,m.user_id) FROM management_actors a LEFT JOIN memberships m ON m.id=a.membership_id AND m.organization_id=a.organization_id WHERE a.organization_id=$1 AND a.id=$2`, organizationID, membershipID).Scan(&actorType, &auditID); err != nil {
		return err
	}
	return s.grantAssignment(ctx, tx, organizationID, assignmentWrite{
		Principal: domain.PrincipalAgent, PrincipalID: agentID, Role: role,
		Scope: domain.Scope{Kind: domain.ScopeOrganization}, Source: domain.AssignmentManual,
		GrantedBy: membershipID, ActorType: actorType, ActorID: auditID,
	}, now)
}

func (s *Store) GetAgent(ctx context.Context, input application.GetAgentInput) (domain.Agent, error) {
	if err := input.Validate(); err != nil {
		return domain.Agent{}, err
	}
	return scanAgent(s.pool.QueryRow(ctx, agentSelect+` FROM agents a WHERE a.organization_id = $1 AND a.id = $2`,
		input.OrganizationID, input.AgentID))
}

// GetAgentBySlug looks an agent up by its slug, live or archived: a slug
// names exactly one agent for the organization's lifetime.
func (s *Store) GetAgentBySlug(ctx context.Context, input application.GetAgentBySlugInput) (domain.Agent, error) {
	if err := input.Validate(); err != nil {
		return domain.Agent{}, err
	}
	return scanAgent(s.pool.QueryRow(ctx, agentSelect+` FROM agents a WHERE a.organization_id = $1 AND a.slug = $2`,
		input.OrganizationID, input.AgentSlug))
}

// refuseTakenAgentIdentity answers the specific conflict before an insert
// or rename: the slug is held by any agent of the organization, live or
// archived, or the name by a live one. agentIdentityConflict maps the
// unique violations a concurrent writer can still cause to the same
// errors.
func refuseTakenAgentIdentity(ctx context.Context, tx querier, organizationID domain.ID, agentSlug domain.AgentSlug, name string) error {
	var slugTaken, nameTaken bool
	if err := tx.QueryRow(ctx, `SELECT
		EXISTS (SELECT 1 FROM agents WHERE organization_id = $1 AND slug = $2),
		EXISTS (SELECT 1 FROM agents WHERE organization_id = $1 AND name = $3 AND archived_at IS NULL)`,
		organizationID, agentSlug, name).Scan(&slugTaken, &nameTaken); err != nil {
		return err
	}
	if slugTaken {
		return domain.ErrAgentSlugTaken
	}
	if nameTaken {
		return domain.ErrAgentNameTaken
	}
	return nil
}

func agentIdentityConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
		switch pgErr.ConstraintName {
		case "agents_organization_id_slug_key":
			return domain.ErrAgentSlugTaken
		case "agents_live_name_idx":
			return domain.ErrAgentNameTaken
		case "agent_instances_agent_id_name_key":
			return domain.ErrAgentInstanceNameTaken
		}
	}
	return err
}

// ListAgentSummaries is the inventory: every agent with its instance
// counts, the newest last-seen stamp across its instances, and its
// request counts. Active agents sort first, then disabled, then archived,
// each by name, so a retired agent never hides a working one. A live
// instance is an active instance holding a credential that is neither
// revoked nor expired at Now. Status leaves archived agents out unless
// it says otherwise.
func (s *Store) ListAgentSummaries(ctx context.Context, input application.ListAgentSummariesInput) ([]domain.AgentSummary, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	organizationID, limit, now := input.OrganizationID, input.Limit, input.Now
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	statusFilter := "AND a.archived_at IS NULL"
	switch input.Status {
	case application.AgentListStatusArchived:
		statusFilter = "AND a.archived_at IS NOT NULL"
	case application.AgentListStatusAll:
		statusFilter = ""
	}
	rows, err := s.pool.Query(ctx, agentSelect+`,
		COALESCE(i.instance_count, 0), COALESCE(i.live_instance_count, 0), i.last_seen_at,
		COALESCE(r.recent_count, 0), COALESCE(r.pending_count, 0)
		FROM agents a
		LEFT JOIN (
			SELECT ai.agent_id, count(*) AS instance_count,
				count(*) FILTER (WHERE ai.status = 'active' AND EXISTS (
					SELECT 1 FROM agent_credentials c
					WHERE c.agent_instance_id = ai.id AND c.revoked_at IS NULL AND c.expires_at > $2)) AS live_instance_count,
				max(ai.last_seen_at) AS last_seen_at
			FROM agent_instances ai WHERE ai.organization_id = $1 GROUP BY ai.agent_id
		) i ON i.agent_id = a.id
		LEFT JOIN (`+agentRequestCountsSQL+`) r ON r.agent_id = a.id
		WHERE a.organization_id = $1 AND ($5::boolean OR a.slug = ANY($6::text[])) `+statusFilter+`
		ORDER BY a.status = 'archived', a.status = 'disabled', lower(a.name), a.created_at DESC LIMIT $4`,
		organizationID, now, now.Add(-application.RecentRequestWindow), limit,
		input.Scope.All, input.Scope.IDs(domain.ScopeAgent))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	summaries := make([]domain.AgentSummary, 0)
	for rows.Next() {
		var summary domain.AgentSummary
		var lastSeenAt *time.Time
		agent, err := scanAgentWith(rows, &summary.InstanceCount, &summary.LiveInstanceCount, &lastSeenAt,
			&summary.RecentRequestCount, &summary.PendingRequestCount)
		if err != nil {
			return nil, err
		}
		summary.Agent, summary.LastSeenAt = agent, lastSeenAt
		summary.RequestTrend = make([]int, application.RecentRequestDays)
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	trends := make(map[string][]int, len(summaries))
	for _, summary := range summaries {
		trends[string(summary.ID)] = summary.RequestTrend
	}
	return summaries, s.fillRequestTrends(ctx, trends, `SELECT r.agent_id::text, `+requestDaysAgoSQL+`, count(*)
		FROM approval_requests r
		WHERE r.organization_id = $1 AND r.created_at > $3 AND r.created_at <= $2
		GROUP BY 1, 2`, organizationID, now, now.Add(-application.RecentRequestWindow))
}

// fillRequestTrends buckets recent requests by whole days before Now,
// oldest bucket first, into the trend registered under each key. Every
// registered trend keeps its full length, so a row without requests
// reads as a flat line rather than a missing one. The query yields the
// key, how many whole days ago the request was created, and a count;
// $1 is the organization, $2 Now, and $3 the start of the window.
func (s *Store) fillRequestTrends(ctx context.Context, trends map[string][]int, query string, args ...any) error {
	if len(trends) == 0 {
		return nil
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var daysAgo, count int
		if err := rows.Scan(&key, &daysAgo, &count); err != nil {
			return err
		}
		trend, ok := trends[key]
		if !ok || daysAgo < 0 || daysAgo >= application.RecentRequestDays {
			continue
		}
		trend[application.RecentRequestDays-1-daysAgo] = count
	}
	return rows.Err()
}

// requestDaysAgoSQL is the whole-day bucket of a request's age at $2.
const requestDaysAgoSQL = `floor(extract(epoch FROM ($2::timestamptz - r.created_at)) / 86400)::int`

// agentRequestCountsSQL groups approval requests per agent: how many were
// created inside the recent window ($3) and how many are still pending.
// It is a subquery fragment; the caller binds $1 (organization) and $3.
const agentRequestCountsSQL = `SELECT r.agent_id, count(*) AS total_count,
	count(*) FILTER (WHERE r.created_at > $3) AS recent_count,
	count(*) FILTER (WHERE r.status = 'pending') AS pending_count
	FROM approval_requests r WHERE r.organization_id = $1 GROUP BY r.agent_id`

// GetAgentDetail loads the agent page: the identity, who created it, the
// instances with every credential each has held (newest first), the
// provisioners, and the request counts.
func (s *Store) GetAgentDetail(ctx context.Context, input application.GetAgentDetailInput) (domain.AgentDetail, error) {
	if err := input.Validate(); err != nil {
		return domain.AgentDetail{}, err
	}
	organizationID, agentID, now := input.OrganizationID, input.AgentID, input.Now
	since := now.Add(-application.RecentRequestWindow)
	var detail domain.AgentDetail
	agent, err := scanAgentWith(s.pool.QueryRow(ctx, agentSelect+`,
		COALESCE(u.display_name, k.name, ''), CASE WHEN k.id IS NULL THEN 'human' ELSE 'api_key' END, COALESCE(r.recent_count, 0), COALESCE(r.pending_count, 0), COALESCE(r.total_count, 0)
		FROM agents a
		LEFT JOIN memberships m ON m.id = a.created_by_actor_id AND m.organization_id = a.organization_id
		LEFT JOIN users u ON u.id = m.user_id
 LEFT JOIN api_keys k ON k.id=a.created_by_actor_id AND k.organization_id=a.organization_id
		LEFT JOIN (`+agentRequestCountsSQL+`) r ON r.agent_id = a.id
		WHERE a.organization_id = $1 AND a.id = $2`, organizationID, agentID, since),
		&detail.CreatedByDisplayName, &detail.CreatedByActorType, &detail.RecentRequestCount, &detail.PendingRequestCount, &detail.RequestCount)
	if err != nil {
		return domain.AgentDetail{}, err
	}
	detail.Agent = agent
	limit := input.InstanceLimit
	if limit <= 0 || limit > 200 {
		limit = 25
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*),
		count(*) FILTER (WHERE ai.status = 'active' AND EXISTS (
			SELECT 1 FROM agent_credentials c WHERE c.organization_id = ai.organization_id
			AND c.agent_instance_id = ai.id AND c.revoked_at IS NULL AND c.expires_at > $3)),
		count(*) FILTER (WHERE ($4::text = '' OR strpos(lower(ai.name), lower($4)) > 0)
			AND ($5::text = '' OR ai.status = $5))
		FROM agent_instances ai WHERE ai.organization_id = $1 AND ai.agent_id = $2`,
		organizationID, agentID, now, input.InstanceQuery, input.InstanceStatus).Scan(
		&detail.InstanceCount, &detail.LiveInstanceCount, &detail.MatchingInstanceCount); err != nil {
		return domain.AgentDetail{}, err
	}

	rows, err := s.pool.Query(ctx, `SELECT ai.id::text, ai.organization_id::text, ai.agent_id::text, ai.name, ai.status,
		ai.metadata, ai.created_at, ai.last_seen_at, ai.membership_id::text, ou.display_name,
		(SELECT count(*) FROM approval_requests r WHERE r.organization_id = ai.organization_id AND r.agent_instance_id = ai.id AND r.created_at > $3)
		FROM agent_instances ai
		LEFT JOIN memberships om ON om.id = ai.membership_id AND om.organization_id = ai.organization_id
		LEFT JOIN users ou ON ou.id = om.user_id
		WHERE ai.organization_id = $1 AND ai.agent_id = $2
		AND ($4::text = '' OR strpos(lower(ai.name), lower($4)) > 0)
		AND ($5::text = '' OR ai.status = $5)
		ORDER BY ai.last_seen_at DESC, ai.created_at DESC, ai.id DESC LIMIT $6 OFFSET $7`,
		organizationID, agentID, since, input.InstanceQuery, input.InstanceStatus, limit, input.InstanceOffset)
	if err != nil {
		return domain.AgentDetail{}, err
	}
	defer rows.Close()
	detail.Instances = make([]domain.AgentInstanceDetail, 0)
	byInstance := make(map[domain.ID]int)
	for rows.Next() {
		var instance domain.AgentInstanceDetail
		var id, orgID, agentRef, status string
		var ownerID, ownerName sql.NullString
		if err := rows.Scan(&id, &orgID, &agentRef, &instance.Name, &status, &instance.Metadata,
			&instance.CreatedAt, &instance.LastSeenAt, &ownerID, &ownerName, &instance.RecentRequestCount); err != nil {
			return domain.AgentDetail{}, err
		}
		instance.ID, instance.OrganizationID, instance.AgentID = domain.ID(id), domain.ID(orgID), domain.ID(agentRef)
		instance.MembershipID, instance.OwnerDisplayName = domain.ID(ownerID.String), ownerName.String
		instance.AgentSlug, instance.Status = agent.AgentSlug, domain.AgentStatus(status)
		instance.Credentials = make([]domain.AgentCredentialState, 0)
		instance.RequestTrend = make([]int, application.RecentRequestDays)
		byInstance[instance.ID] = len(detail.Instances)
		detail.Instances = append(detail.Instances, instance)
	}
	if err := rows.Err(); err != nil {
		return domain.AgentDetail{}, err
	}
	rows.Close()
	instanceIDs := make([]string, 0, len(detail.Instances))
	trends := make(map[string][]int, len(detail.Instances))
	for _, instance := range detail.Instances {
		trends[string(instance.ID)] = instance.RequestTrend
		instanceIDs = append(instanceIDs, string(instance.ID))
	}
	if err := s.fillRequestTrends(ctx, trends, `SELECT r.agent_instance_id::text, `+requestDaysAgoSQL+`, count(*)
		FROM approval_requests r
		WHERE r.organization_id = $1 AND r.created_at > $3 AND r.created_at <= $2
			AND r.agent_id = $4 AND r.agent_instance_id = ANY($5::uuid[])
		GROUP BY 1, 2`, organizationID, now, since, agentID, instanceIDs); err != nil {
		return domain.AgentDetail{}, err
	}

	credentialRows, err := s.pool.Query(ctx, `SELECT c.id::text, c.agent_instance_id::text, c.issued_at, c.expires_at,
		c.last_used_at, c.revoked_at
		FROM agent_credentials c
		JOIN agent_instances ai ON ai.id = c.agent_instance_id AND ai.organization_id = c.organization_id
		WHERE c.organization_id = $1 AND ai.agent_id = $2 AND ai.id = ANY($3::uuid[])
		ORDER BY c.issued_at DESC`, organizationID, agentID, instanceIDs)
	if err != nil {
		return domain.AgentDetail{}, err
	}
	defer credentialRows.Close()
	for credentialRows.Next() {
		var credential domain.AgentCredentialState
		var id, instanceID string
		if err := credentialRows.Scan(&id, &instanceID, &credential.IssuedAt, &credential.ExpiresAt,
			&credential.LastUsedAt, &credential.RevokedAt); err != nil {
			return domain.AgentDetail{}, err
		}
		credential.ID = domain.ID(id)
		if index, ok := byInstance[domain.ID(instanceID)]; ok {
			detail.Instances[index].Credentials = append(detail.Instances[index].Credentials, credential)
		}
	}
	if err := credentialRows.Err(); err != nil {
		return domain.AgentDetail{}, err
	}

	provisionerLimit := input.ProvisionerLimit
	if provisionerLimit <= 0 || provisionerLimit > 200 {
		provisionerLimit = 25
	}
	const provisionerMatch = `($4::text = '' OR strpos(lower(id::text), lower($4)) > 0
		OR strpos(lower('whp_…' || right(id::text, 4)), lower($4)) > 0)
		AND ($5::text = ''
			OR ($5 = 'active' AND revoked_at IS NULL AND expires_at > $3)
			OR ($5 = 'expired' AND revoked_at IS NULL AND expires_at <= $3)
			OR ($5 = 'revoked' AND revoked_at IS NOT NULL))`
	if err := s.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE `+provisionerMatch+`)
		FROM agent_provisioners WHERE organization_id = $1 AND agent_id = $2`,
		organizationID, agentID, now, input.ProvisionerQuery, input.ProvisionerStatus).Scan(
		&detail.ProvisionerCount, &detail.MatchingProvisionerCount); err != nil {
		return domain.AgentDetail{}, err
	}
	provisionerRows, err := s.pool.Query(ctx, `SELECT id::text, organization_id::text, agent_id::text,
		created_by_actor_id::text, issued_at, expires_at, last_used_at, revoked_at
		FROM agent_provisioners WHERE organization_id = $1 AND agent_id = $2 AND `+provisionerMatch+`
		ORDER BY issued_at DESC, id DESC LIMIT $6 OFFSET $7`,
		organizationID, agentID, now, input.ProvisionerQuery, input.ProvisionerStatus, provisionerLimit, input.ProvisionerOffset)
	if err != nil {
		return domain.AgentDetail{}, err
	}
	defer provisionerRows.Close()
	detail.Provisioners = make([]domain.AgentProvisioner, 0)
	for provisionerRows.Next() {
		var provisioner domain.AgentProvisioner
		var id, orgID, ownerID, membershipID string
		if err := provisionerRows.Scan(&id, &orgID, &ownerID, &membershipID, &provisioner.IssuedAt,
			&provisioner.ExpiresAt, &provisioner.LastUsedAt, &provisioner.RevokedAt); err != nil {
			return domain.AgentDetail{}, err
		}
		provisioner.ID, provisioner.OrganizationID = domain.ID(id), domain.ID(orgID)
		provisioner.AgentID, provisioner.AgentSlug = domain.ID(ownerID), agent.AgentSlug
		provisioner.CreatedByActorID = domain.ID(membershipID)
		detail.Provisioners = append(detail.Provisioners, provisioner)
	}
	return detail, provisionerRows.Err()
}

// SetAgentStatus disables or re-enables the agent. Credentials stay in
// place: resolution refuses them while the agent is disabled and accepts
// them again once it is enabled. An archived agent is past the switch.
func (s *Store) SetAgentStatus(ctx context.Context, input application.SetAgentStatusInput) (domain.Agent, error) {
	if err := input.Validate(); err != nil {
		return domain.Agent{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Agent{}, err
	}
	defer tx.Rollback(ctx)
	previous, agentSlug, err := lockAgentForWrite(ctx, tx, input.OrganizationID, input.AgentID)
	if err != nil {
		return domain.Agent{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE agents SET status = $3, updated_at = $4
		WHERE organization_id = $1 AND id = $2`,
		input.OrganizationID, input.AgentID, string(input.Status), input.Now); err != nil {
		return domain.Agent{}, err
	}
	if previous != input.Status {
		if err := s.appendActorAuditEvent(ctx, tx, input.OrganizationID, agentStatusEventType("agent", input.Status),
			"agent", input.AgentID, input.ActorID, input.Now,
			map[string]any{"agent_slug": agentSlug, "previous_status": previous, "status": input.Status}); err != nil {
			return domain.Agent{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Agent{}, err
	}
	return s.GetAgent(ctx, application.GetAgentInput{OrganizationID: input.OrganizationID, AgentID: input.AgentID})
}

// SetAgentInstanceStatus disables or re-enables one runtime instance.
// The instances of an archived agent are frozen with it.
func (s *Store) SetAgentInstanceStatus(ctx context.Context, input application.SetAgentInstanceStatusInput) (domain.AgentInstance, error) {
	if err := input.Validate(); err != nil {
		return domain.AgentInstance{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AgentInstance{}, err
	}
	defer tx.Rollback(ctx)
	// An instance a provisioner deleted stays disabled: its credentials
	// are revoked and its pending requests were cancelled.
	var deleted bool
	if err := tx.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM agent_instances WHERE organization_id = $1 AND id = $2 FOR UPDATE`,
		input.OrganizationID, input.InstanceID).Scan(&deleted); errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentInstance{}, domain.ErrNotFound
	} else if err != nil {
		return domain.AgentInstance{}, err
	}
	if deleted && input.Status != domain.AgentDisabled {
		return domain.AgentInstance{}, domain.ErrInstanceDeleted
	}
	var instance domain.AgentInstance
	var id, orgID, agentID, previous, agentStatus string
	var ownerID sql.NullString
	err = tx.QueryRow(ctx, `UPDATE agent_instances ai SET status = $3
		FROM (SELECT status FROM agent_instances WHERE organization_id = $1 AND id = $2 FOR UPDATE) before,
		     agents a
		WHERE ai.organization_id = $1 AND ai.id = $2 AND a.id = ai.agent_id AND a.organization_id = ai.organization_id
		RETURNING ai.id::text, ai.organization_id::text, ai.agent_id::text, a.slug, ai.name, ai.metadata, ai.created_at, ai.last_seen_at, before.status, a.status, ai.membership_id::text`,
		input.OrganizationID, input.InstanceID, string(input.Status)).Scan(
		&id, &orgID, &agentID, &instance.AgentSlug, &instance.Name, &instance.Metadata, &instance.CreatedAt, &instance.LastSeenAt, &previous, &agentStatus, &ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentInstance{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.AgentInstance{}, err
	}
	if domain.AgentStatus(agentStatus) == domain.AgentArchived {
		return domain.AgentInstance{}, domain.ErrAgentArchived
	}
	instance.ID, instance.OrganizationID, instance.AgentID = domain.ID(id), domain.ID(orgID), domain.ID(agentID)
	instance.MembershipID, instance.Status = domain.ID(ownerID.String), input.Status
	if domain.AgentStatus(previous) != input.Status {
		if err := s.appendActorAuditEvent(ctx, tx, input.OrganizationID, agentStatusEventType("agent_instance", input.Status),
			"agent_instance", input.InstanceID, input.ActorID, input.Now,
			map[string]any{"agent_slug": instance.AgentSlug, "previous_status": previous, "status": input.Status}); err != nil {
			return domain.AgentInstance{}, err
		}
	}
	return instance, tx.Commit(ctx)
}

// RenameAgentInstance changes one instance's display name. Nothing
// addresses an instance by name after enrollment, so the running agent is
// unaffected; requests already made keep the name they were created with.
// The instances of an archived agent are frozen with it.
func (s *Store) RenameAgentInstance(ctx context.Context, input application.RenameAgentInstanceInput) (domain.AgentInstance, error) {
	if err := input.Validate(); err != nil {
		return domain.AgentInstance{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AgentInstance{}, err
	}
	defer tx.Rollback(ctx)
	var instance domain.AgentInstance
	var id, orgID, agentID, previous, status, agentStatus string
	var ownerID sql.NullString
	err = tx.QueryRow(ctx, `SELECT ai.id::text, ai.organization_id::text, ai.agent_id::text, a.slug, ai.name, ai.status, ai.metadata, ai.created_at, ai.last_seen_at, a.status, ai.membership_id::text
		FROM agent_instances ai JOIN agents a ON a.id = ai.agent_id AND a.organization_id = ai.organization_id
		WHERE ai.organization_id = $1 AND ai.id = $2 FOR UPDATE OF ai`,
		input.OrganizationID, input.InstanceID).Scan(
		&id, &orgID, &agentID, &instance.AgentSlug, &previous, &status, &instance.Metadata, &instance.CreatedAt, &instance.LastSeenAt, &agentStatus, &ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentInstance{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.AgentInstance{}, err
	}
	if domain.AgentStatus(agentStatus) == domain.AgentArchived {
		return domain.AgentInstance{}, domain.ErrAgentArchived
	}
	instance.ID, instance.OrganizationID, instance.AgentID = domain.ID(id), domain.ID(orgID), domain.ID(agentID)
	instance.MembershipID, instance.Name, instance.Status = domain.ID(ownerID.String), input.Name, domain.AgentStatus(status)
	if previous != input.Name {
		if _, err := tx.Exec(ctx, `UPDATE agent_instances SET name = $3 WHERE organization_id = $1 AND id = $2`,
			input.OrganizationID, input.InstanceID, input.Name); err != nil {
			return domain.AgentInstance{}, agentIdentityConflict(err)
		}
		if err := s.appendActorAuditEvent(ctx, tx, input.OrganizationID, "agent_instance.renamed",
			"agent_instance", input.InstanceID, input.ActorID, input.Now,
			map[string]any{"agent_slug": instance.AgentSlug, "previous_name": previous, "name": input.Name}); err != nil {
			return domain.AgentInstance{}, err
		}
	}
	return instance, tx.Commit(ctx)
}

// RenameAgent changes the display name. The slug is the identity and
// never moves; the name only has to be free among the live agents.
func (s *Store) RenameAgent(ctx context.Context, input application.RenameAgentInput) (domain.Agent, error) {
	if err := input.Validate(); err != nil {
		return domain.Agent{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Agent{}, err
	}
	defer tx.Rollback(ctx)
	_, agentSlug, err := lockAgentForWrite(ctx, tx, input.OrganizationID, input.AgentID)
	if err != nil {
		return domain.Agent{}, err
	}
	var previous string
	if err := tx.QueryRow(ctx, `SELECT name FROM agents WHERE organization_id = $1 AND id = $2`,
		input.OrganizationID, input.AgentID).Scan(&previous); err != nil {
		return domain.Agent{}, err
	}
	if previous != input.Name {
		if _, err := tx.Exec(ctx, `UPDATE agents SET name = $3, updated_at = $4
			WHERE organization_id = $1 AND id = $2`,
			input.OrganizationID, input.AgentID, input.Name, input.Now); err != nil {
			return domain.Agent{}, agentIdentityConflict(err)
		}
		if err := s.appendActorAuditEvent(ctx, tx, input.OrganizationID, "agent.renamed",
			"agent", input.AgentID, input.ActorID, input.Now,
			map[string]any{"agent_slug": agentSlug, "previous_name": previous, "name": input.Name}); err != nil {
			return domain.Agent{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Agent{}, err
	}
	return s.GetAgent(ctx, application.GetAgentInput{OrganizationID: input.OrganizationID, AgentID: input.AgentID})
}

// ArchiveAgent retires the agent: a status beside the active/disabled
// switch that only restore reverses. Every live credential and
// provisioner token is revoked, since nothing of the agent resolves while
// it is archived, and the instances and history stay for the record.
// Organization keys scoped to the agent lose their assignments with it.
func (s *Store) ArchiveAgent(ctx context.Context, input application.ArchiveAgentInput) (domain.Agent, error) {
	if err := input.Validate(); err != nil {
		return domain.Agent{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Agent{}, err
	}
	defer tx.Rollback(ctx)
	previous, agentSlug, err := lockAgentForWrite(ctx, tx, input.OrganizationID, input.AgentID)
	if err != nil {
		return domain.Agent{}, err
	}
	var name string
	if err := tx.QueryRow(ctx, `UPDATE agents SET status = 'archived', archived_at = $3, updated_at = $3
		WHERE organization_id = $1 AND id = $2 RETURNING name`,
		input.OrganizationID, input.AgentID, input.Now).Scan(&name); err != nil {
		return domain.Agent{}, err
	}
	revokedCredentials, err := tx.Exec(ctx, `UPDATE agent_credentials c SET revoked_at = $3
		FROM agent_instances ai
		WHERE c.organization_id = $1 AND ai.id = c.agent_instance_id AND ai.organization_id = c.organization_id
			AND ai.agent_id = $2 AND c.revoked_at IS NULL`, input.OrganizationID, input.AgentID, input.Now)
	if err != nil {
		return domain.Agent{}, err
	}
	revokedProvisioners, err := tx.Exec(ctx, `UPDATE agent_provisioners SET revoked_at = $3
		WHERE organization_id = $1 AND agent_id = $2 AND revoked_at IS NULL`, input.OrganizationID, input.AgentID, input.Now)
	if err != nil {
		return domain.Agent{}, err
	}
	actorID, err := managementActorID(ctx, tx, input.OrganizationID, input.ActorID)
	if err != nil {
		return domain.Agent{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM role_assignments WHERE organization_id=$1 AND principal_kind='api_key' AND scope_kind='agent' AND scope_id=$2`, input.OrganizationID, string(agentSlug)); err != nil {
		return domain.Agent{}, err
	}
	data, _ := json.Marshal(map[string]any{
		"agent_slug": agentSlug, "name": name, "previous_status": previous,
		"credentials_revoked": revokedCredentials.RowsAffected(), "provisioners_revoked": revokedProvisioners.RowsAffected(),
	})
	if err := s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "agent.archived",
		ActorType: domain.ActorHuman, ActorID: actorID,
		SubjectType: "agent", SubjectID: input.AgentID,
		Data: data, OccurredAt: input.Now,
	}); err != nil {
		return domain.Agent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Agent{}, err
	}
	return s.GetAgent(ctx, application.GetAgentInput{OrganizationID: input.OrganizationID, AgentID: input.AgentID})
}

// RestoreAgent brings an archived agent back to active. Credentials and
// provisioner tokens revoked at archive stay revoked; instances are
// re-enrolled the ordinary way. The name must still be free among the
// live agents, since a live agent may have taken it meanwhile.
func (s *Store) RestoreAgent(ctx context.Context, input application.RestoreAgentInput) (domain.Agent, error) {
	if err := input.Validate(); err != nil {
		return domain.Agent{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Agent{}, err
	}
	defer tx.Rollback(ctx)
	status, agentSlug, err := lockAgent(ctx, tx, input.OrganizationID, input.AgentID)
	if err != nil {
		return domain.Agent{}, err
	}
	if status != domain.AgentArchived {
		return domain.Agent{}, domain.ErrAgentNotArchived
	}
	var name string
	if err := tx.QueryRow(ctx, `UPDATE agents SET status = 'active', archived_at = NULL, updated_at = $3
		WHERE organization_id = $1 AND id = $2 RETURNING name`,
		input.OrganizationID, input.AgentID, input.Now).Scan(&name); err != nil {
		return domain.Agent{}, agentIdentityConflict(err)
	}
	actorID, err := managementActorID(ctx, tx, input.OrganizationID, input.ActorID)
	if err != nil {
		return domain.Agent{}, err
	}
	data, _ := json.Marshal(map[string]any{"agent_slug": agentSlug, "name": name})
	if err := s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "agent.restored",
		ActorType: domain.ActorHuman, ActorID: actorID,
		SubjectType: "agent", SubjectID: input.AgentID,
		Data: data, OccurredAt: input.Now,
	}); err != nil {
		return domain.Agent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Agent{}, err
	}
	return s.GetAgent(ctx, application.GetAgentInput{OrganizationID: input.OrganizationID, AgentID: input.AgentID})
}

// lockAgent takes the agent's row lock and answers its status and slug.
func lockAgent(ctx context.Context, tx pgx.Tx, organizationID, agentID domain.ID) (domain.AgentStatus, domain.AgentSlug, error) {
	var status, agentSlug string
	err := tx.QueryRow(ctx, `SELECT status, slug FROM agents WHERE organization_id = $1 AND id = $2 FOR UPDATE`,
		organizationID, agentID).Scan(&status, &agentSlug)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", domain.ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	return domain.AgentStatus(status), domain.AgentSlug(agentSlug), nil
}

// lockAgentForWrite takes the agent's row lock for a change to a live
// agent and answers its current status and slug; an archived agent
// refuses every write but restore.
func lockAgentForWrite(ctx context.Context, tx pgx.Tx, organizationID, agentID domain.ID) (domain.AgentStatus, domain.AgentSlug, error) {
	status, agentSlug, err := lockAgent(ctx, tx, organizationID, agentID)
	if err != nil {
		return "", "", err
	}
	if status == domain.AgentArchived {
		return "", "", domain.ErrAgentArchived
	}
	return status, agentSlug, nil
}

func agentStatusEventType(subject string, status domain.AgentStatus) string {
	if status == domain.AgentDisabled {
		return subject + ".disabled"
	}
	return subject + ".enabled"
}

// appendActorAuditEvent records a human action attributed through
// the acting membership's user, the same attribution the creation events
// use.
func (s *Store) appendActorAuditEvent(ctx context.Context, tx pgx.Tx, organizationID domain.ID, eventType, subjectType string, subjectID, membershipID domain.ID, now time.Time, payload map[string]any) error {
	data, _ := json.Marshal(payload)
	return s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{OrganizationID: organizationID, EventType: eventType, ActorType: domain.ActorHuman, ActorID: membershipID, SubjectType: subjectType, SubjectID: subjectID, Data: data, OccurredAt: now})
}

// agentSelect is the agent column list every agent query starts from;
// scanAgentWith reads it and then any trailing columns the caller adds.
const agentSelect = `SELECT a.id::text, a.organization_id::text, a.created_by_actor_id::text,
	a.name, a.slug, a.kind, a.status, a.created_at, a.updated_at, a.archived_at`

func scanAgent(row rowScanner) (domain.Agent, error) {
	return scanAgentWith(row)
}

func scanAgentWith(row rowScanner, extra ...any) (domain.Agent, error) {
	var agent domain.Agent
	var id, organizationID, membershipID, kind, status string
	targets := append([]any{&id, &organizationID, &membershipID, &agent.Name, &agent.AgentSlug, &kind, &status, &agent.CreatedAt, &agent.UpdatedAt, &agent.ArchivedAt}, extra...)
	err := row.Scan(targets...)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Agent{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Agent{}, err
	}
	agent.ID, agent.OrganizationID = domain.ID(id), domain.ID(organizationID)
	agent.CreatedByActorID, agent.Kind, agent.Status = domain.ID(membershipID), domain.AgentKind(kind), domain.AgentStatus(status)
	return agent, nil
}

func (s *Store) CreateAgentInstanceWithCredential(ctx context.Context, input application.CreateAgentInstanceWithCredentialInput) (domain.AgentInstance, error) {
	if err := input.Validate(); err != nil {
		return domain.AgentInstance{}, err
	}
	organizationID, agentID, actorID := input.OrganizationID, input.AgentID, input.ActorID
	name, metadata, credential, now := input.Name, input.Metadata, input.Credential, input.Now
	instance := domain.AgentInstance{
		ID: domain.ID(uuid.NewString()), OrganizationID: organizationID, AgentID: agentID, MembershipID: input.MembershipID, Name: name,
		Status: domain.AgentActive, Metadata: metadata, CreatedAt: now, LastSeenAt: now,
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AgentInstance{}, err
	}
	defer tx.Rollback(ctx)
	var status, kind string
	if err := tx.QueryRow(ctx, `SELECT status, slug, kind FROM agents WHERE organization_id = $1 AND id = $2 FOR SHARE`, organizationID, agentID).Scan(&status, &instance.AgentSlug, &kind); errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentInstance{}, domain.ErrNotFound
	} else if err != nil {
		return domain.AgentInstance{}, err
	} else if domain.AgentStatus(status) != domain.AgentActive {
		return domain.AgentInstance{}, domain.ErrAgentInactive
	}
	if err := checkInstanceOwner(domain.AgentKind(kind), input.MembershipID); err != nil {
		return domain.AgentInstance{}, err
	}
	command, err := tx.Exec(ctx, `INSERT INTO agent_instances
        (id, organization_id, agent_id, membership_id, name, status, metadata, created_at, last_seen_at)
		VALUES ($1, $2, $3, NULLIF($4::text, '')::uuid, $5, 'active', $6, $7, $7)
		ON CONFLICT (agent_id, name) DO NOTHING`, instance.ID, organizationID, agentID, input.MembershipID, name, metadata, now)
	if err != nil {
		return domain.AgentInstance{}, err
	}
	if command.RowsAffected() == 0 {
		return domain.AgentInstance{}, fmt.Errorf("agent instance name already exists: %w", domain.ErrConflict)
	}
	if err := insertAgentCredential(ctx, tx, organizationID, instance.ID, credential); err != nil {
		return domain.AgentInstance{}, err
	}
	data, _ := json.Marshal(map[string]any{"agent_slug": instance.AgentSlug, "name": name, "membership_id": input.MembershipID})
	if err := s.appendAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: organizationID, EventType: "agent_instance.registered",
		ActorType: input.ActorType, ActorID: actorID,
		SubjectType: "agent_instance", SubjectID: instance.ID,
		Data: data, OccurredAt: now,
	}); err != nil {
		return domain.AgentInstance{}, err
	}
	return instance, tx.Commit(ctx)
}

// disableMembershipAgentInstances is the cascade a membership deprovision
// runs beside revoking personal API keys: every instance the member owns
// is disabled and its live credentials revoked in the same transaction,
// one audit event per instance, so a personal agent leaves with its
// person. actorID may be empty for a directory-driven removal, which
// the audit row then attributes to the system.
func (s *Store) disableMembershipAgentInstances(ctx context.Context, tx pgx.Tx, organizationID, membershipID domain.ID, actorType domain.ActorType, actorID domain.ID, now time.Time) error {
	rows, err := tx.Query(ctx, `UPDATE agent_instances ai SET status = 'disabled'
		FROM agents a
		WHERE ai.organization_id = $1 AND ai.membership_id = $2 AND ai.status = 'active'
			AND a.id = ai.agent_id AND a.organization_id = ai.organization_id
		RETURNING ai.id::text, ai.name, a.slug`, organizationID, membershipID)
	if err != nil {
		return err
	}
	type disabled struct{ id, name, agentSlug string }
	var instances []disabled
	for rows.Next() {
		var instance disabled
		if err := rows.Scan(&instance.id, &instance.name, &instance.agentSlug); err != nil {
			rows.Close()
			return err
		}
		instances = append(instances, instance)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_credentials c SET revoked_at = $3
		FROM agent_instances ai
		WHERE c.organization_id = $1 AND ai.id = c.agent_instance_id AND ai.organization_id = c.organization_id
			AND ai.membership_id = $2 AND c.revoked_at IS NULL`, organizationID, membershipID, now); err != nil {
		return err
	}
	for _, instance := range instances {
		payload, _ := json.Marshal(map[string]any{
			"agent_slug": instance.agentSlug, "name": instance.name, "membership_id": membershipID,
			"previous_status": domain.AgentActive, "status": domain.AgentDisabled, "reason": "membership_deprovisioned",
		})
		if err := s.appendAuditEvent(ctx, tx, domain.AuditEvent{
			OrganizationID: organizationID, EventType: "agent_instance.disabled",
			ActorType: actorType, ActorID: actorID,
			SubjectType: "agent_instance", SubjectID: domain.ID(instance.id),
			Data: payload, OccurredAt: now,
		}); err != nil {
			return err
		}
	}
	return nil
}

// checkInstanceOwner is the owner rule the database trigger also holds:
// an instance of a personal agent belongs to a member, an instance of a
// service agent to nobody. Answering it here names the mistake instead
// of surfacing a constraint failure.
func checkInstanceOwner(kind domain.AgentKind, membershipID domain.ID) error {
	switch {
	case kind == domain.AgentPersonal && membershipID == "":
		return domain.ErrAgentOwnerRequired
	case kind != domain.AgentPersonal && membershipID != "":
		return fmt.Errorf("a service agent's instances have no owner: %w", domain.ErrInvalid)
	}
	return nil
}

func insertAgentCredential(ctx context.Context, tx pgx.Tx, organizationID, instanceID domain.ID, record application.AgentCredentialRecord) error {
	_, err := tx.Exec(ctx, `INSERT INTO agent_credentials
        (id, organization_id, agent_instance_id, secret_hash, issued_at, expires_at, rotated_from_id)
        VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7::text, '')::uuid)`, record.ID, organizationID,
		instanceID, record.SecretHash, record.IssuedAt, record.ExpiresAt, record.RotatedFromID)
	return err
}

func (s *Store) RevokeAgentCredential(ctx context.Context, input application.RevokeAgentCredentialInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var revoked bool
	err = tx.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM agent_credentials WHERE organization_id=$1 AND id=$2 FOR UPDATE`, input.OrganizationID, input.CredentialID).Scan(&revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if revoked {
		return nil
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_credentials SET revoked_at=$3 WHERE organization_id=$1 AND id=$2`, input.OrganizationID, input.CredentialID, input.Now); err != nil {
		return err
	}
	if err = s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{OrganizationID: input.OrganizationID, EventType: "agent_credential.revoked", ActorType: domain.ActorHuman, ActorID: input.ActorID, SubjectType: "agent_credential", SubjectID: input.CredentialID, Data: json.RawMessage(`{}`), OccurredAt: input.Now}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func permissionStrings(permissions []domain.Permission) []string {
	values := make([]string, len(permissions))
	for index, permission := range permissions {
		values[index] = string(permission)
	}
	return values
}

// LookupAgentSlug resolves the slug of the agent behind an agent id,
// instance, credential, or provisioner within the organization, so a
// management permission can be checked against the agent before the row
// is touched.
func (s *Store) LookupAgentSlug(ctx context.Context, input application.LookupAgentSlugInput) (domain.AgentSlug, error) {
	if err := input.Validate(); err != nil {
		return "", err
	}
	var query string
	var id domain.ID
	switch {
	case input.AgentID != "":
		query, id = `SELECT a.slug FROM agents a WHERE a.id = $1 AND a.organization_id = $2`, input.AgentID
	case input.AgentInstanceID != "":
		query, id = `SELECT a.slug FROM agent_instances ai JOIN agents a ON a.id = ai.agent_id
			WHERE ai.id = $1 AND ai.organization_id = $2`, input.AgentInstanceID
	case input.CredentialID != "":
		query, id = `SELECT a.slug FROM agent_credentials c
			JOIN agent_instances ai ON ai.id = c.agent_instance_id JOIN agents a ON a.id = ai.agent_id
			WHERE c.id = $1 AND c.organization_id = $2`, input.CredentialID
	default:
		query, id = `SELECT a.slug FROM agent_provisioners p JOIN agents a ON a.id = p.agent_id
			WHERE p.id = $1 AND p.organization_id = $2`, input.ProvisionerID
	}
	var agentSlug string
	err := s.pool.QueryRow(ctx, query, id, input.OrganizationID).Scan(&agentSlug)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return domain.AgentSlug(agentSlug), nil
}
