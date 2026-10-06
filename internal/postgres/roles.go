package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
)

// querier is what the pool and a transaction have in common, so the
// grant loader and the assignment writers run inside a caller's
// transaction as readily as on their own.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// reconcileLockKey serialises boot reconcilers across replicas so two
// pods starting together cannot interleave their upserts.
const reconcileLockKey = 7300001

// builtInOrderSQL sorts the seeded roles in their canonical order ahead
// of custom rows, which follow by name.
const builtInOrderSQL = `r.built_in DESC,
    CASE WHEN r.built_in THEN array_position(ARRAY['owner', 'admin', 'reviewer', 'auditor', 'agent'], r.key) END`

// membershipPrincipalSQL matches the assignments a membership holds:
// its own rows and the rows of every team it belongs to. The membership
// id parameter is spliced in by name so the same fragment serves a
// single lookup and the per-list batch.
func membershipPrincipalSQL(membershipRef string) string {
	return `((ra.principal_kind = 'membership' AND ra.principal_id = ` + membershipRef + `)
        OR (ra.principal_kind = 'team' AND ra.principal_id IN
            (SELECT team_id FROM active_membership_teams WHERE membership_id = ` + membershipRef + `)))`
}

// loadGrants flattens a principal's assignments into grants: every
// permission of every assigned role, pinned at the assignment's scope.
// A membership inherits its teams' assignments; a team or agent holds
// only its direct rows.
func loadGrants(ctx context.Context, q querier, organizationID domain.ID, kind domain.PrincipalKind, principalID domain.ID) ([]domain.Grant, error) {
	principal := `(ra.principal_kind = $2 AND ra.principal_id = $3)`
	if kind == domain.PrincipalMembership {
		principal = `($2 = 'membership' AND ` + membershipPrincipalSQL("$3") + `)`
	}
	rows, err := q.Query(ctx, `SELECT DISTINCT p.permission, ra.scope_kind, ra.scope_id
        FROM role_assignments ra
        JOIN roles r ON r.id = ra.role_id
        CROSS JOIN LATERAL unnest(r.permissions) AS p(permission)
        WHERE ra.organization_id = $1 AND `+principal+`
        ORDER BY 1, 2, 3`, organizationID, string(kind), principalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := make([]domain.Grant, 0)
	for rows.Next() {
		var permission, scopeKind, scopeID string
		if err := rows.Scan(&permission, &scopeKind, &scopeID); err != nil {
			return nil, err
		}
		grants = append(grants, domain.Grant{
			Permission: domain.Permission(permission), ScopeKind: domain.ScopeKind(scopeKind), ScopeID: scopeID,
		})
	}
	return grants, rows.Err()
}

func (s *Store) LoadGrants(ctx context.Context, input application.LoadGrantsInput) ([]domain.Grant, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	return loadGrants(ctx, s.pool, input.OrganizationID, input.PrincipalKind, input.PrincipalID)
}

// roleColumns reads one role row; $1 is always the reading organization
// so the assignment count is that tenant's alone.
const roleColumns = `r.id::text, COALESCE(r.organization_id::text, ''), r.key, r.name, r.description,
    r.permissions, r.bindable_scope_kinds, r.built_in,
    (SELECT count(*) FROM role_assignments ra WHERE ra.role_id = r.id AND ra.organization_id = $1),
    r.created_at, r.updated_at`

func scanRole(row rowScanner) (domain.RoleRow, error) {
	var role domain.RoleRow
	var id, organizationID, key string
	var permissions, kinds []string
	err := row.Scan(&id, &organizationID, &key, &role.Name, &role.Description,
		&permissions, &kinds, &role.BuiltIn, &role.AssignmentCount, &role.CreatedAt, &role.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RoleRow{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.RoleRow{}, err
	}
	role.ID, role.OrganizationID, role.Key = domain.ID(id), domain.ID(organizationID), domain.Role(key)
	role.Permissions = make([]domain.Permission, len(permissions))
	for index, permission := range permissions {
		role.Permissions[index] = domain.Permission(permission)
	}
	role.BindableScopeKinds = make([]domain.ScopeKind, len(kinds))
	for index, kind := range kinds {
		role.BindableScopeKinds[index] = domain.ScopeKind(kind)
	}
	return role, nil
}

func scopeKindStrings(kinds []domain.ScopeKind) []string {
	values := make([]string, len(kinds))
	for index, kind := range kinds {
		values[index] = string(kind)
	}
	return values
}

// getRole reads a role the organization may see: a built-in row or its
// own custom row.
func getRole(ctx context.Context, q querier, organizationID, roleID domain.ID) (domain.RoleRow, error) {
	return scanRole(q.QueryRow(ctx, `SELECT `+roleColumns+` FROM roles r
        WHERE r.id = $2 AND (r.organization_id IS NULL OR r.organization_id = $1)`, organizationID, roleID))
}

func (s *Store) ListRoles(ctx context.Context, input application.ListRolesInput) ([]domain.RoleRow, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+roleColumns+` FROM roles r
        WHERE r.organization_id IS NULL OR r.organization_id = $1
        ORDER BY `+builtInOrderSQL+`, r.name, r.key`, input.OrganizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roles := make([]domain.RoleRow, 0)
	for rows.Next() {
		role, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func (s *Store) GetRole(ctx context.Context, input application.GetRoleInput) (domain.RoleRow, error) {
	if err := input.Validate(); err != nil {
		return domain.RoleRow{}, err
	}
	return getRole(ctx, s.pool, input.OrganizationID, input.RoleID)
}

// roleAuditData is what every role event carries, so a reader of the
// log sees the role's whole shape at that moment.
func roleAuditData(role domain.RoleRow, extra map[string]any) []byte {
	data := map[string]any{
		"key": role.Key, "name": role.Name, "permissions": role.Permissions,
		"bindable_scope_kinds": role.BindableScopeKinds, "dangerous": role.Dangerous(),
	}
	for key, value := range extra {
		data[key] = value
	}
	payload, _ := json.Marshal(data)
	return payload
}

func (s *Store) appendRoleAudit(ctx context.Context, tx execer, organizationID domain.ID, eventType string, actorType domain.ActorType, actorID domain.ID, role domain.RoleRow, extra map[string]any, now time.Time) error {
	return s.appendAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: organizationID, EventType: eventType,
		ActorType: actorType, ActorID: actorID,
		SubjectType: "role", SubjectID: role.ID,
		Data: roleAuditData(role, extra), OccurredAt: now,
	})
}

func (s *Store) CreateRole(ctx context.Context, input application.CreateRoleInput) (domain.RoleRow, error) {
	if err := input.Validate(); err != nil {
		return domain.RoleRow{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RoleRow{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO roles
        (id, organization_id, key, name, description, permissions, bindable_scope_kinds, built_in, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, false, $8, $8)`,
		input.RoleID, input.OrganizationID, string(input.Key), input.Name, input.Description,
		permissionStrings(input.Permissions), scopeKindStrings(input.BindableScopeKinds), input.Now); err != nil {
		if isUniqueViolation(err) {
			return domain.RoleRow{}, fmt.Errorf("a role with this key already exists: %w", domain.ErrConflict)
		}
		return domain.RoleRow{}, err
	}
	role, err := getRole(ctx, tx, input.OrganizationID, input.RoleID)
	if err != nil {
		return domain.RoleRow{}, err
	}
	if err := s.appendRoleAudit(ctx, tx, input.OrganizationID, "role.created",
		domain.ActorHuman, input.ActorMembershipID, role, nil, input.Now); err != nil {
		return domain.RoleRow{}, err
	}
	return role, tx.Commit(ctx)
}

// lockCustomRole locks one of the organization's own role rows for a
// write. A built-in row is visible but refused; another tenant's row is
// not found.
func lockCustomRole(ctx context.Context, tx pgx.Tx, organizationID, roleID domain.ID) (domain.RoleRow, error) {
	role, err := scanRole(tx.QueryRow(ctx, `SELECT `+roleColumns+` FROM roles r
        WHERE r.id = $2 AND (r.organization_id IS NULL OR r.organization_id = $1) FOR UPDATE OF r`, organizationID, roleID))
	if err != nil {
		return domain.RoleRow{}, err
	}
	if role.BuiltIn {
		return domain.RoleRow{}, domain.ErrRoleBuiltIn
	}
	return role, nil
}

func (s *Store) UpdateRole(ctx context.Context, input application.UpdateRoleInput) (domain.RoleRow, error) {
	if err := input.Validate(); err != nil {
		return domain.RoleRow{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return domain.RoleRow{}, err
	}
	defer tx.Rollback(ctx)
	before, err := lockCustomRole(ctx, tx, input.OrganizationID, input.RoleID)
	if err != nil {
		return domain.RoleRow{}, err
	}
	// The trigger checks bindability only when a row is written, so an
	// assignment at a kind the role would stop allowing has to be
	// refused here.
	var boundElsewhere bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM role_assignments
        WHERE role_id = $1 AND NOT (scope_kind = ANY($2::text[])))`,
		input.RoleID, scopeKindStrings(input.BindableScopeKinds)).Scan(&boundElsewhere); err != nil {
		return domain.RoleRow{}, err
	}
	if boundElsewhere {
		return domain.RoleRow{}, fmt.Errorf("this role is still assigned where the change would not allow it: %w", domain.ErrConflict)
	}
	if _, err := tx.Exec(ctx, `UPDATE roles SET name = $2, description = $3, permissions = $4,
        bindable_scope_kinds = $5, updated_at = $6 WHERE id = $1`,
		input.RoleID, input.Name, input.Description, permissionStrings(input.Permissions),
		scopeKindStrings(input.BindableScopeKinds), input.Now); err != nil {
		return domain.RoleRow{}, err
	}
	role, err := getRole(ctx, tx, input.OrganizationID, input.RoleID)
	if err != nil {
		return domain.RoleRow{}, err
	}
	if err := s.appendRoleAudit(ctx, tx, input.OrganizationID, "role.updated",
		domain.ActorHuman, input.ActorMembershipID, role,
		map[string]any{"before_permissions": before.Permissions, "after_permissions": role.Permissions}, input.Now); err != nil {
		return domain.RoleRow{}, err
	}
	return role, tx.Commit(ctx)
}

func (s *Store) DeleteRole(ctx context.Context, input application.DeleteRoleInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	role, err := lockCustomRole(ctx, tx, input.OrganizationID, input.RoleID)
	if err != nil {
		return err
	}
	if role.AssignmentCount > 0 {
		return fmt.Errorf("%w %d times. Remove those assignments first", domain.ErrRoleAssigned, role.AssignmentCount)
	}
	// A pending invitation would grant the role on acceptance; deleting
	// it out from under the invite is refused rather than silently
	// dropping the grant.
	var invited bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM invitations
        WHERE organization_id = $1 AND status = 'pending'
          AND grants @> jsonb_build_array(jsonb_build_object('role_id', $2::text)))`,
		input.OrganizationID, input.RoleID).Scan(&invited); err != nil {
		return err
	}
	if invited {
		return fmt.Errorf("%w in a pending invitation", domain.ErrRoleAssigned)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM roles WHERE id = $1 AND organization_id = $2`,
		input.RoleID, input.OrganizationID); err != nil {
		if isForeignKeyViolation(err) {
			return fmt.Errorf("%w by a domain default or directory group", domain.ErrRoleAssigned)
		}
		return err
	}
	if err := s.appendRoleAudit(ctx, tx, input.OrganizationID, "role.deleted",
		domain.ActorHuman, input.ActorMembershipID, role, nil, input.Now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReconcileBuiltInRoles upserts the seeded rows from their code
// definitions and strips every role row of keys the registry no longer
// knows, auditing the custom rows it changed. The whole pass runs under
// an advisory lock so concurrent boots serialise.
func (s *Store) ReconcileBuiltInRoles(ctx context.Context, input application.ReconcileBuiltInRolesInput) (application.ReconcileReport, error) {
	if err := input.Validate(); err != nil {
		return application.ReconcileReport{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return application.ReconcileReport{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, reconcileLockKey); err != nil {
		return application.ReconcileReport{}, err
	}
	var report application.ReconcileReport
	for _, def := range input.Roles {
		// The update fires only when something differs, so an unchanged
		// row returns nothing and counts as neither seeded nor updated.
		var inserted bool
		err := tx.QueryRow(ctx, `INSERT INTO roles
            (id, organization_id, key, name, description, permissions, bindable_scope_kinds, built_in, created_at, updated_at)
            VALUES ($1, NULL, $2, $3, $4, $5, $6, true, $7, $7)
            ON CONFLICT (id) DO UPDATE SET key = EXCLUDED.key, name = EXCLUDED.name,
                description = EXCLUDED.description, permissions = EXCLUDED.permissions,
                bindable_scope_kinds = EXCLUDED.bindable_scope_kinds, updated_at = EXCLUDED.updated_at
            WHERE roles.key IS DISTINCT FROM EXCLUDED.key OR roles.name IS DISTINCT FROM EXCLUDED.name
               OR roles.description IS DISTINCT FROM EXCLUDED.description
               OR roles.permissions IS DISTINCT FROM EXCLUDED.permissions
               OR roles.bindable_scope_kinds IS DISTINCT FROM EXCLUDED.bindable_scope_kinds
            RETURNING xmax = 0`,
			def.ID, string(def.Key), def.Name, def.Description,
			permissionStrings(def.Permissions), scopeKindStrings(def.BindableScopeKinds), input.Now).Scan(&inserted)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return application.ReconcileReport{}, err
		}
		if inserted {
			report.Seeded++
		} else {
			report.Updated++
		}
	}

	registered := permissionStrings(input.Permissions)
	// No organization reads here, so the assignment count is unused and
	// its parameter is null.
	rows, err := tx.Query(ctx, `SELECT `+roleColumns+` FROM roles r
        WHERE NOT (r.permissions <@ $2::text[]) ORDER BY r.id`, nil, registered)
	if err != nil {
		return application.ReconcileReport{}, err
	}
	var stale []domain.RoleRow
	for rows.Next() {
		role, err := scanRole(rows)
		if err != nil {
			rows.Close()
			return application.ReconcileReport{}, err
		}
		stale = append(stale, role)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return application.ReconcileReport{}, err
	}
	stripped := make(map[domain.Permission]struct{})
	for _, before := range stale {
		// The surviving keys keep their stored order.
		if _, err := tx.Exec(ctx, `UPDATE roles SET permissions = ARRAY(
                SELECT u.permission FROM unnest(permissions) WITH ORDINALITY AS u(permission, ordinal)
                WHERE u.permission = ANY($2::text[]) ORDER BY u.ordinal),
            updated_at = $3 WHERE id = $1`, before.ID, registered, input.Now); err != nil {
			return application.ReconcileReport{}, err
		}
		report.Updated++
		after := before
		after.Permissions = nil
		for _, key := range before.Permissions {
			if slices.Contains(input.Permissions, key) {
				after.Permissions = append(after.Permissions, key)
			} else {
				stripped[key] = struct{}{}
			}
		}
		after.UpdatedAt = input.Now
		if before.OrganizationID == "" {
			continue
		}
		if err := s.appendRoleAudit(ctx, tx, before.OrganizationID, "role.updated", domain.ActorSystem, "", after,
			map[string]any{"before_permissions": before.Permissions, "after_permissions": after.Permissions}, input.Now); err != nil {
			return application.ReconcileReport{}, err
		}
	}
	for key := range stripped {
		report.StrippedKeys = append(report.StrippedKeys, key)
	}
	slices.Sort(report.StrippedKeys)
	return report, tx.Commit(ctx)
}

// assignmentColumns reads one assignment with its role resolved and,
// for a team scope, the team's name. The via columns are appended by
// each query because only a membership listing inherits through teams.
const assignmentColumns = `ra.id::text, ra.organization_id::text, ra.principal_kind, ra.principal_id::text,
    ra.role_id::text, r.key, r.name, ra.scope_kind, ra.scope_id,
    COALESCE((SELECT t.name FROM teams t WHERE ra.scope_kind = 'team'
        AND t.organization_id = ra.organization_id AND t.id::text = ra.scope_id), ''),
    ra.source, ra.source_key, COALESCE(ra.granted_by_actor_id::text, ''), ra.created_at, r.permissions, COALESCE(ra.permission_policy_id::text, '')`

const assignmentOrderSQL = `ORDER BY ` + builtInOrderSQL + `, r.name, ra.scope_kind, ra.scope_id, ra.created_at, ra.id`

func scanAssignment(row rowScanner, holder *string) (domain.RoleAssignment, error) {
	var assignment domain.RoleAssignment
	var id, organizationID, principalKind, principalID, roleID, roleKey, scopeKind, source, grantedBy, viaID, policyID string
	var permissionKeys []string
	targets := []any{&id, &organizationID, &principalKind, &principalID, &roleID, &roleKey, &assignment.RoleName,
		&scopeKind, &assignment.ScopeID, &assignment.ScopeName, &source, &assignment.SourceKey, &grantedBy,
		&assignment.CreatedAt, &permissionKeys, &policyID, &viaID, &assignment.ViaTeamName}
	if holder != nil {
		targets = append([]any{holder}, targets...)
	}
	if err := row.Scan(targets...); err != nil {
		return domain.RoleAssignment{}, err
	}
	assignment.PermissionPolicyID = domain.ID(policyID)
	assignment.IsOwner = roleKey == string(domain.RoleOwner) && scopeKind == string(domain.ScopeOrganization)
	assignment.Permissions = make([]domain.Permission, 0, len(permissionKeys))
	for _, key := range permissionKeys {
		d, ok := domain.LookupPermission(domain.Permission(key))
		if ok && d.AppliesAt(domain.ScopeKind(scopeKind)) {
			assignment.Permissions = append(assignment.Permissions, d.Key)
		}
	}
	assignment.ID, assignment.OrganizationID = domain.ID(id), domain.ID(organizationID)
	assignment.PrincipalKind, assignment.PrincipalID = domain.PrincipalKind(principalKind), domain.ID(principalID)
	assignment.RoleID, assignment.RoleKey = domain.ID(roleID), domain.Role(roleKey)
	if def, ok := domain.BuiltInRoleByID(assignment.RoleID); ok {
		assignment.BuiltInRole = def.Key
	}
	assignment.ScopeKind, assignment.Source = domain.ScopeKind(scopeKind), domain.AssignmentSource(source)
	assignment.GrantedByActorID, assignment.ViaTeamID = domain.ID(grantedBy), domain.ID(viaID)
	return assignment, nil
}

// membershipAssignments lists the assignments of several memberships in
// one query, direct and inherited, keyed by membership. Every requested
// membership gets an entry, empty when it holds nothing.
func membershipAssignments(ctx context.Context, q querier, organizationID domain.ID, membershipIDs []domain.ID) (map[domain.ID][]domain.RoleAssignment, error) {
	byMembership := make(map[domain.ID][]domain.RoleAssignment, len(membershipIDs))
	for _, id := range membershipIDs {
		byMembership[id] = []domain.RoleAssignment{}
	}
	if len(membershipIDs) == 0 {
		return byMembership, nil
	}
	rows, err := q.Query(ctx, `SELECT m.id::text, `+assignmentColumns+`,
        CASE WHEN ra.principal_kind = 'team' THEN ra.principal_id::text ELSE '' END,
        COALESCE(via.name, '')
        FROM memberships m
        JOIN role_assignments ra ON ra.organization_id = m.organization_id AND `+membershipPrincipalSQL("m.id")+`
        JOIN roles r ON r.id = ra.role_id
        LEFT JOIN teams via ON ra.principal_kind = 'team' AND via.id = ra.principal_id
        WHERE m.organization_id = $1 AND m.id = ANY($2::uuid[])
        `+assignmentOrderSQL, organizationID, idStrings(membershipIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var holder string
		assignment, err := scanAssignment(rows, &holder)
		if err != nil {
			return nil, err
		}
		byMembership[domain.ID(holder)] = append(byMembership[domain.ID(holder)], assignment)
	}
	return byMembership, rows.Err()
}

// teamAssignments lists the direct assignments of several teams in one
// query, keyed by team. Every requested team gets an entry, empty when
// it holds nothing.
func teamAssignments(ctx context.Context, q querier, organizationID domain.ID, teamIDs []domain.ID) (map[domain.ID][]domain.RoleAssignment, error) {
	byTeam := make(map[domain.ID][]domain.RoleAssignment, len(teamIDs))
	for _, id := range teamIDs {
		byTeam[id] = []domain.RoleAssignment{}
	}
	if len(teamIDs) == 0 {
		return byTeam, nil
	}
	rows, err := q.Query(ctx, `SELECT ra.principal_id::text, `+assignmentColumns+`, '', ''
        FROM role_assignments ra JOIN roles r ON r.id = ra.role_id
        WHERE ra.organization_id = $1 AND ra.principal_kind = 'team' AND ra.principal_id = ANY($2::uuid[])
        `+assignmentOrderSQL, organizationID, idStrings(teamIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var holder string
		assignment, err := scanAssignment(rows, &holder)
		if err != nil {
			return nil, err
		}
		byTeam[domain.ID(holder)] = append(byTeam[domain.ID(holder)], assignment)
	}
	return byTeam, rows.Err()
}

// listAssignments is ListAssignments without validation, for callers
// already inside a transaction.
func listAssignments(ctx context.Context, q querier, organizationID domain.ID, kind domain.PrincipalKind, principalID domain.ID) ([]domain.RoleAssignment, error) {
	if kind == domain.PrincipalMembership {
		byMembership, err := membershipAssignments(ctx, q, organizationID, []domain.ID{principalID})
		if err != nil {
			return nil, err
		}
		return byMembership[principalID], nil
	}
	rows, err := q.Query(ctx, `SELECT `+assignmentColumns+`, '', ''
        FROM role_assignments ra JOIN roles r ON r.id = ra.role_id
        WHERE ra.organization_id = $1 AND ra.principal_kind = $2 AND ra.principal_id = $3
        `+assignmentOrderSQL, organizationID, string(kind), principalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assignments := make([]domain.RoleAssignment, 0)
	for rows.Next() {
		assignment, err := scanAssignment(rows, nil)
		if err != nil {
			return nil, err
		}
		assignments = append(assignments, assignment)
	}
	return assignments, rows.Err()
}

func (s *Store) ListAssignments(ctx context.Context, input application.ListAssignmentsInput) ([]domain.RoleAssignment, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	return listAssignments(ctx, s.pool, input.OrganizationID, input.PrincipalKind, input.PrincipalID)
}

// roleRef is the little a writer needs to know about a role it grants:
// the key for the audit row and whether the grant deserves ceremony.
type roleRef struct {
	ID        domain.ID
	Key       domain.Role
	Dangerous bool
}

// lookupRole resolves a role the organization may grant: a built-in
// row or its own. Anything else is invalid input, never a foreign key
// error surfacing from the insert.
func lookupRole(ctx context.Context, q querier, organizationID, roleID domain.ID) (roleRef, error) {
	role, err := getRole(ctx, q, organizationID, roleID)
	if errors.Is(err, domain.ErrNotFound) {
		return roleRef{}, fmt.Errorf("role %s no longer exists: %w", roleID, domain.ErrInvalid)
	}
	if err != nil {
		return roleRef{}, err
	}
	return roleRef{ID: role.ID, Key: role.Key, Dangerous: role.Dangerous()}, nil
}

// assignmentWrite names one assignment row being granted or revoked.
type assignmentWrite struct {
	PolicyID    domain.ID
	Principal   domain.PrincipalKind
	PrincipalID domain.ID
	Role        roleRef
	Scope       domain.Scope
	Source      domain.AssignmentSource
	SourceKey   string
	// GrantedBy is the membership that granted the row, empty for a
	// system or bootstrap grant.
	GrantedBy domain.ID
	// Actor is who the audit row names.
	ActorType domain.ActorType
	ActorID   domain.ID
}

// assignmentCheckViolation recognises the check trigger's refusals (a
// principal that is not in the organization, a team scope that is not,
// a role from another tenant, or a scope the role cannot bind at) and
// returns the trigger's message.
func assignmentCheckViolation(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "P0001" {
		return "", false
	}
	for _, fragment := range []string{"does not exist", "not bindable", "belongs to another organization"} {
		if strings.Contains(pgErr.Message, fragment) {
			return pgErr.Message, true
		}
	}
	return "", false
}

func assignmentAuditData(write assignmentWrite) []byte {
	payload, _ := json.Marshal(map[string]any{
		"principal_kind": write.Principal, "principal_id": write.PrincipalID,
		"role_id": write.Role.ID, "role_key": write.Role.Key,
		"scope_kind": write.Scope.Kind, "scope_id": write.Scope.ID,
		"source": write.Source, "dangerous": write.Role.Dangerous,
	})
	return payload
}

func (s *Store) appendAssignmentAudit(ctx context.Context, tx execer, organizationID domain.ID, eventType string, write assignmentWrite, now time.Time) error {
	return s.appendAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: organizationID, EventType: eventType,
		ActorType: write.ActorType, ActorID: write.ActorID,
		SubjectType: string(write.Principal), SubjectID: write.PrincipalID,
		Data: assignmentAuditData(write), OccurredAt: now,
	})
}

// grantAssignment inserts one assignment row and audits it. The check
// trigger's refusals come back as invalid input; a duplicate as a
// conflict.
func (s *Store) grantAssignment(ctx context.Context, tx querier, organizationID domain.ID, write assignmentWrite, now time.Time) error {
	if write.Source == "" {
		write.Source = domain.AssignmentManual
	}
	_, err := tx.Exec(ctx, `INSERT INTO role_assignments
        (id, organization_id, principal_kind, principal_id, role_id, scope_kind, scope_id,
         source, source_key, granted_by_actor_id, created_at, permission_policy_id)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10::text, '')::uuid, $11, NULLIF($12::text, '')::uuid)`,
		uuid.NewString(), organizationID, string(write.Principal), write.PrincipalID, write.Role.ID,
		string(write.Scope.Kind), write.Scope.ID, string(write.Source), write.SourceKey, string(write.GrantedBy), now, string(write.PolicyID))
	if message, refused := assignmentCheckViolation(err); refused {
		return fmt.Errorf("%s: %w", message, domain.ErrInvalid)
	}
	if isUniqueViolation(err) {
		return fmt.Errorf("this role is already assigned here: %w", domain.ErrConflict)
	}
	if err != nil {
		return err
	}
	return s.appendAssignmentAudit(ctx, tx, organizationID, "role_assignment.granted", write, now)
}

// grantSpecs resolves and grants each spec to one principal, the shape
// invitation acceptance and admission share.
func (s *Store) grantSpecs(ctx context.Context, tx querier, organizationID domain.ID, base assignmentWrite, specs []domain.AssignmentSpec, now time.Time) error {
	groups := map[string]domain.ID{}
	for _, spec := range specs {
		role, err := lookupRole(ctx, tx, organizationID, spec.RoleID)
		if err != nil {
			return err
		}
		write := base
		write.Role, write.Scope = role, spec.Scope()
		if base.Principal != domain.PrincipalAgent && role.ID != domain.RoleOwnerID {
			key := string(role.ID) + ":" + string(spec.ScopeKind)
			policyID, exists := groups[key]
			if !exists {
				policyID = domain.ID(uuid.NewString())
				groups[key] = policyID
				if _, err := tx.Exec(ctx, `INSERT INTO permission_policies(id,organization_id) VALUES($1,$2)`, policyID, organizationID); err != nil {
					return err
				}
			}
			write.PolicyID = policyID
		}
		if err := s.grantAssignment(ctx, tx, organizationID, write, now); err != nil {
			return err
		}
	}
	return nil
}

// storedAssignment is one existing row a writer may revoke.
type storedAssignment struct {
	ID    domain.ID
	Write assignmentWrite
}

// selectAssignments reads the rows a predicate matches with enough of
// their role to audit a revocation. $1 is the organization.
func selectAssignments(ctx context.Context, q querier, organizationID domain.ID, predicate string, args ...any) ([]storedAssignment, error) {
	rows, err := q.Query(ctx, `SELECT ra.id::text, ra.principal_kind, ra.principal_id::text, ra.role_id::text, r.key, r.permissions,
        ra.scope_kind, ra.scope_id, ra.source, ra.source_key, COALESCE(ra.granted_by_actor_id::text, '')
        FROM role_assignments ra JOIN roles r ON r.id = ra.role_id
        WHERE ra.organization_id = $1 AND `+predicate+` ORDER BY ra.created_at, ra.id`, append([]any{organizationID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stored []storedAssignment
	for rows.Next() {
		var id, principalKind, principalID, roleID, roleKey, scopeKind, scopeID, source, sourceKey, grantedBy string
		var permissions []string
		if err := rows.Scan(&id, &principalKind, &principalID, &roleID, &roleKey, &permissions,
			&scopeKind, &scopeID, &source, &sourceKey, &grantedBy); err != nil {
			return nil, err
		}
		role := domain.RoleRow{Permissions: make([]domain.Permission, len(permissions))}
		for index, permission := range permissions {
			role.Permissions[index] = domain.Permission(permission)
		}
		stored = append(stored, storedAssignment{ID: domain.ID(id), Write: assignmentWrite{
			Principal: domain.PrincipalKind(principalKind), PrincipalID: domain.ID(principalID),
			Role:   roleRef{ID: domain.ID(roleID), Key: domain.Role(roleKey), Dangerous: role.Dangerous()},
			Scope:  domain.Scope{Kind: domain.ScopeKind(scopeKind), ID: scopeID},
			Source: domain.AssignmentSource(source), SourceKey: sourceKey, GrantedBy: domain.ID(grantedBy),
		}})
	}
	return stored, rows.Err()
}

// revokeAssignment deletes one row and audits it. The last-owner
// trigger's refusal is a conflict.
func (s *Store) revokeAssignment(ctx context.Context, tx querier, organizationID domain.ID, stored storedAssignment, actorType domain.ActorType, actorID domain.ID, now time.Time) error {
	if _, err := tx.Exec(ctx, `DELETE FROM role_assignments WHERE organization_id = $1 AND id = $2`,
		organizationID, stored.ID); err != nil {
		if isLastOwnerViolation(err) {
			return fmt.Errorf("the organization needs at least one active owner: %w", domain.ErrConflict)
		}
		return err
	}
	write := stored.Write
	write.ActorType, write.ActorID = actorType, actorID
	return s.appendAssignmentAudit(ctx, tx, organizationID, "role_assignment.revoked", write, now)
}

// revokeAssignmentsWhere deletes and audits every row a predicate
// matches, for the writers that clear a source wholesale.
func (s *Store) revokeAssignmentsWhere(ctx context.Context, tx querier, organizationID domain.ID, actorType domain.ActorType, actorID domain.ID, now time.Time, predicate string, args ...any) error {
	stored, err := selectAssignments(ctx, tx, organizationID, predicate, args...)
	if err != nil {
		return err
	}
	for _, row := range stored {
		if err := s.revokeAssignment(ctx, tx, organizationID, row, actorType, actorID, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ReplaceAssignments(ctx context.Context, input application.ReplaceAssignmentsInput) ([]domain.RoleAssignment, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	switch input.PrincipalKind {
	case domain.PrincipalMembership:
		var directoryManaged bool
		err = tx.QueryRow(ctx, `SELECT directory_managed FROM memberships
            WHERE organization_id = $1 AND id = $2 FOR UPDATE`, input.OrganizationID, input.PrincipalID).Scan(&directoryManaged)
		if err == nil && directoryManaged {
			return nil, fmt.Errorf("this member is managed by your directory: %w", domain.ErrConflict)
		}
	case domain.PrincipalTeam:
		err = requireLiveTeam(ctx, tx, input.OrganizationID, input.PrincipalID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	current, err := selectAssignments(ctx, tx, input.OrganizationID,
		`ra.principal_kind = $2 AND ra.principal_id = $3 AND ra.source = 'manual'`,
		string(input.PrincipalKind), input.PrincipalID)
	if err != nil {
		return nil, err
	}
	desired := make(map[domain.AssignmentSpec]struct{}, len(input.Assignments))
	for _, spec := range input.Assignments {
		desired[spec] = struct{}{}
	}
	held := make(map[domain.AssignmentSpec]struct{}, len(current))
	for _, row := range current {
		spec := domain.AssignmentSpec{RoleID: row.Write.Role.ID, ScopeKind: row.Write.Scope.Kind, ScopeID: row.Write.Scope.ID}
		held[spec] = struct{}{}
		if _, keep := desired[spec]; keep {
			continue
		}
		if err := s.revokeAssignment(ctx, tx, input.OrganizationID, row, domain.ActorHuman, input.ActorMembershipID, input.Now); err != nil {
			return nil, err
		}
	}
	var missing []domain.AssignmentSpec
	for _, spec := range input.Assignments {
		if _, exists := held[spec]; !exists {
			missing = append(missing, spec)
		}
	}
	if err := s.grantSpecs(ctx, tx, input.OrganizationID, assignmentWrite{
		Principal: input.PrincipalKind, PrincipalID: input.PrincipalID,
		Source: domain.AssignmentManual, GrantedBy: input.ActorMembershipID,
		ActorType: domain.ActorHuman, ActorID: input.ActorMembershipID,
	}, missing, input.Now); err != nil {
		return nil, err
	}
	if input.PrincipalKind == domain.PrincipalMembership {
		if _, err := tx.Exec(ctx, `UPDATE memberships SET updated_at = $3
            WHERE organization_id = $1 AND id = $2`, input.OrganizationID, input.PrincipalID, input.Now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		if isLastOwnerViolation(err) {
			return nil, fmt.Errorf("the organization needs at least one active owner: %w", domain.ErrConflict)
		}
		return nil, err
	}
	return listAssignments(ctx, s.pool, input.OrganizationID, input.PrincipalKind, input.PrincipalID)
}

// SetDirectoryGroupGrant maps a provider group to a grant its linked
// team holds, or clears it. The team's directory-sourced assignment for
// that group is replaced in the same transaction, so the link and the
// row it explains never disagree.
func (s *Store) SetDirectoryGroupGrant(ctx context.Context, input application.SetDirectoryGroupGrantInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var teamID string
	err = tx.QueryRow(ctx, `SELECT team_id::text FROM directory_object_links
        WHERE organization_id = $1 AND provider_object_type = 'group' AND provider_object_id = $2
          AND team_id IS NOT NULL FOR UPDATE`, input.OrganizationID, input.ProviderObjectID).Scan(&teamID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	var role roleRef
	if input.Grant != nil {
		if role, err = lookupRole(ctx, tx, input.OrganizationID, input.Grant.RoleID); err != nil {
			return err
		}
	}
	var roleID, scopeKind, scopeID *string
	if input.Grant != nil {
		id, kind, scope := string(input.Grant.RoleID), string(input.Grant.ScopeKind), input.Grant.ScopeID
		roleID, scopeKind, scopeID = &id, &kind, &scope
	}
	if _, err := tx.Exec(ctx, `UPDATE directory_object_links
        SET role_id = $3::uuid, scope_kind = $4, scope_id = $5, updated_at = $6
        WHERE organization_id = $1 AND provider_object_type = 'group' AND provider_object_id = $2`,
		input.OrganizationID, input.ProviderObjectID, roleID, scopeKind, scopeID, input.Now); err != nil {
		return err
	}
	if err := s.revokeAssignmentsWhere(ctx, tx, input.OrganizationID, domain.ActorHuman, input.ActorMembershipID, input.Now,
		`ra.principal_kind = 'team' AND ra.principal_id = $2 AND ra.source = 'directory' AND ra.source_key = $3`,
		teamID, input.ProviderObjectID); err != nil {
		return err
	}
	if input.Grant != nil {
		if err := s.grantAssignment(ctx, tx, input.OrganizationID, assignmentWrite{
			Principal: domain.PrincipalTeam, PrincipalID: domain.ID(teamID), Role: role, Scope: input.Grant.Scope(),
			Source: domain.AssignmentDirectory, SourceKey: input.ProviderObjectID, GrantedBy: input.ActorMembershipID,
			ActorType: domain.ActorHuman, ActorID: input.ActorMembershipID,
		}, input.Now); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) ListDirectoryGroupLinks(ctx context.Context, input application.ListDirectoryGroupLinksInput) ([]domain.DirectoryGroupLink, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT l.provider_object_id, t.id::text, t.name,
        COALESCE(l.role_id::text, ''), COALESCE(l.scope_kind, ''), COALESCE(l.scope_id, ''),
        COALESCE(r.key, ''), COALESCE(r.name, ''), l.updated_at
        FROM directory_object_links l
        JOIN teams t ON t.id = l.team_id
        LEFT JOIN roles r ON r.id = l.role_id
        WHERE l.organization_id = $1 AND l.provider_object_type = 'group'
        ORDER BY t.name, l.provider_object_id`, input.OrganizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	links := make([]domain.DirectoryGroupLink, 0)
	for rows.Next() {
		var link domain.DirectoryGroupLink
		var teamID, roleID, scopeKind, scopeID, roleKey string
		if err := rows.Scan(&link.ProviderObjectID, &teamID, &link.TeamName, &roleID, &scopeKind, &scopeID,
			&roleKey, &link.RoleName, &link.UpdatedAt); err != nil {
			return nil, err
		}
		link.TeamID, link.RoleKey = domain.ID(teamID), domain.Role(roleKey)
		if roleID != "" {
			link.Grant = &domain.AssignmentSpec{RoleID: domain.ID(roleID), ScopeKind: domain.ScopeKind(scopeKind), ScopeID: scopeID}
		}
		links = append(links, link)
	}
	return links, rows.Err()
}
