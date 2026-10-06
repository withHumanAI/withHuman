package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/authorization"
)

var _ application.PermissionPolicyRepository = (*Store)(nil)

func policyTarget(ctx context.Context, q querier, input application.ReadPermissionPoliciesInput) (application.PermissionPoliciesPage, error) {
	page := application.PermissionPoliciesPage{Policies: []domain.PermissionPolicy{}, Resources: []domain.PermissionResource{}}
	var locked bool
	var status string
	var err error
	if input.PrincipalKind == domain.PrincipalMembership {
		err = q.QueryRow(ctx, `SELECT COALESCE(NULLIF(u.display_name,''),u.primary_email), m.directory_managed, m.status
            FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.organization_id=$1 AND m.id=$2`, input.Actor.OrganizationID, input.PrincipalID).Scan(&page.Name, &locked, &status)
		if locked {
			page.ReadOnlyReason = "This membership is managed by your directory."
		}
		if status != "active" {
			page.ReadOnlyReason = "Reactivate this member before editing permissions."
		}
		if input.Actor.MembershipID == input.PrincipalID {
			page.ReadOnlyReason = "You cannot edit your own permissions."
		}
	} else if input.PrincipalKind == domain.PrincipalAPIKey {
		err = q.QueryRow(ctx, `SELECT name, revoked_at IS NOT NULL OR (expires_at IS NOT NULL AND expires_at<=now()) FROM api_keys WHERE organization_id=$1 AND id=$2 AND kind='organization'`, input.Actor.OrganizationID, input.PrincipalID).Scan(&page.Name, &locked)
		if locked {
			page.ReadOnlyReason = "Expired and revoked keys are read-only."
		}
	} else {
		err = q.QueryRow(ctx, `SELECT name, archived_at IS NOT NULL FROM teams WHERE organization_id=$1 AND id=$2`, input.Actor.OrganizationID, input.PrincipalID).Scan(&page.Name, &locked)
		if locked {
			page.ReadOnlyReason = "Archived teams are read-only."
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return page, domain.ErrNotFound
	}
	return page, err
}

// policyState reads the saved grouping, including inherited provenance. It
// never infers grouping from a role name or from equal permission arrays.
func policyState(ctx context.Context, q querier, input application.ReadPermissionPoliciesInput) (application.PermissionPoliciesPage, error) {
	page, err := policyTarget(ctx, q, input)
	if err != nil {
		return page, err
	}
	predicate := `ra.principal_kind=$2 AND ra.principal_id=$3`
	if input.PrincipalKind == domain.PrincipalMembership {
		predicate = `$2='membership' AND ` + membershipPrincipalSQL("$3")
	}
	rows, err := q.Query(ctx, `SELECT COALESCE(ra.permission_policy_id,ra.id)::text, COALESCE(pp.revision,1),
        ra.scope_kind,ra.scope_id,r.permissions,ra.source,
        CASE WHEN ra.principal_kind='team' AND $2='membership' THEN ra.principal_id::text ELSE '' END,
        COALESCE(via.name,''),COALESCE(target.name,agent.name,pipeline.name,ra.scope_id),r.key
        FROM role_assignments ra JOIN roles r ON r.id=ra.role_id
        LEFT JOIN permission_policies pp ON pp.id=ra.permission_policy_id
        LEFT JOIN teams via ON via.id=ra.principal_id AND $2='membership' AND ra.principal_kind='team'
        LEFT JOIN teams target ON ra.scope_kind='team' AND target.id::text=ra.scope_id AND target.organization_id=ra.organization_id
        LEFT JOIN agents agent ON ra.scope_kind='agent' AND agent.slug=ra.scope_id AND agent.organization_id=ra.organization_id
        LEFT JOIN request_approval_pipelines pipeline ON ra.scope_kind='pipeline' AND pipeline.pipeline_key=ra.scope_id AND pipeline.organization_id=ra.organization_id AND pipeline.is_active
        WHERE ra.organization_id=$1 AND (`+predicate+`) ORDER BY ra.created_at,ra.id`, input.Actor.OrganizationID, string(input.PrincipalKind), input.PrincipalID)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	indices := map[domain.ID]int{}
	for rows.Next() {
		var id, kind, scopeID, source, viaID, viaName, name, key string
		var revision int64
		var permissions []string
		if err := rows.Scan(&id, &revision, &kind, &scopeID, &permissions, &source, &viaID, &viaName, &name, &key); err != nil {
			return page, err
		}
		if key == string(domain.RoleOwner) {
			page.IsOwner = true
			continue
		}
		policyID := domain.ID(id)
		index, exists := indices[policyID]
		if !exists {
			policy := domain.PermissionPolicy{ID: policyID, Revision: revision, Source: domain.AssignmentSource(source), ViaTeamID: domain.ID(viaID), ViaTeamName: viaName, Resources: []domain.PermissionResource{}, PermissionPolicySpec: domain.PermissionPolicySpec{ScopeKind: domain.ScopeKind(kind), ResourceIDs: []string{}, Permissions: []domain.Permission{}}}
			for _, key := range permissions {
				d, ok := domain.LookupPermission(domain.Permission(key))
				if ok && d.HeldBy(policyHolder(input.PrincipalKind)) && d.AppliesAt(policy.ScopeKind) {
					policy.Permissions = append(policy.Permissions, d.Key)
				}
			}
			index = len(page.Policies)
			indices[policyID] = index
			page.Policies = append(page.Policies, policy)
		}
		if scopeID != "" {
			page.Policies[index].ResourceIDs = append(page.Policies[index].ResourceIDs, scopeID)
			page.Policies[index].Resources = append(page.Policies[index].Resources, domain.PermissionResource{ID: scopeID, Name: name, Kind: domain.ScopeKind(kind)})
		}
	}
	return page, rows.Err()
}

func policyScopeAllowed(actor domain.Principal, permission domain.Permission, spec domain.PermissionPolicySpec) bool {
	for _, scope := range spec.Scopes() {
		if authorization.Check(actor, permission, scope.Resource()) != nil {
			return false
		}
	}
	return true
}

func (s *Store) ReadPermissionPolicies(ctx context.Context, input application.ReadPermissionPoliciesInput) (application.PermissionPoliciesPage, error) {
	if err := input.Validate(); err != nil {
		return application.PermissionPoliciesPage{}, err
	}
	if input.PrincipalKind == domain.PrincipalAPIKey && authorization.Check(input.Actor, domain.PermissionAPIKeyRead, domain.OrganizationResource()) != nil {
		return application.PermissionPoliciesPage{}, domain.ErrForbidden
	}
	self := input.PrincipalKind == domain.PrincipalMembership && input.PrincipalID == input.Actor.MembershipID
	if !self && !authorization.HoldsAnywhere(input.Actor, domain.PermissionPolicyRead) && !authorization.HoldsAnywhere(input.Actor, domain.PermissionPolicyWrite) {
		return application.PermissionPoliciesPage{}, domain.ErrForbidden
	}
	page, err := policyState(ctx, s.pool, input)
	if err != nil {
		return page, err
	}
	actorAssignments, err := listAssignments(ctx, s.pool, input.Actor.OrganizationID, domain.PrincipalMembership, input.Actor.MembershipID)
	if err != nil {
		return page, err
	}
	if page.IsOwner && !domain.HoldsRoleEverywhere(actorAssignments, domain.RoleOwner) {
		page.ReadOnlyReason = "Only an organization owner can change this member's permissions."
	}
	visible := make([]domain.PermissionPolicy, 0, len(page.Policies))
	for _, p := range page.Policies {
		write := policyScopeAllowed(input.Actor, domain.PermissionPolicyWrite, p.PermissionPolicySpec)
		if !self && !write && !policyScopeAllowed(input.Actor, domain.PermissionPolicyRead, p.PermissionPolicySpec) {
			continue
		}
		p.ReadOnlyReason = page.ReadOnlyReason
		if p.ViaTeamID != "" {
			p.ReadOnlyReason = "Inherited permissions must be edited on the source team."
		}
		if p.Source == domain.AssignmentDirectory {
			p.ReadOnlyReason = "This policy is managed by your directory."
		}
		p.CanEdit = write && p.ReadOnlyReason == ""
		if !p.CanEdit && p.ReadOnlyReason == "" {
			p.ReadOnlyReason = "You cannot manage permissions at this scope."
		}
		visible = append(visible, p)
	}
	page.Policies = visible
	if input.PrincipalKind == domain.PrincipalAPIKey && authorization.Check(input.Actor, domain.PermissionAPIKeyOrganizationIssue, domain.OrganizationResource()) != nil {
		for i := range page.Policies {
			page.Policies[i].CanEdit = false
			page.Policies[i].ReadOnlyReason = "You cannot delegate access to organization keys."
		}
		page.ReadOnlyReason = "You cannot delegate access to organization keys."
	}
	page.CanCreate = page.ReadOnlyReason == "" && authorization.HoldsAnywhere(input.Actor, domain.PermissionPolicyWrite)
	rows, err := s.pool.Query(ctx, `SELECT 'team',id::text,name FROM teams WHERE organization_id=$1 AND archived_at IS NULL
        UNION ALL SELECT DISTINCT 'agent',slug,name FROM agents WHERE organization_id=$1 AND archived_at IS NULL
        UNION ALL SELECT 'pipeline',pipeline_key,name FROM request_approval_pipelines WHERE organization_id=$1 AND is_active ORDER BY 1,3`, input.Actor.OrganizationID)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var resource domain.PermissionResource
		if err := rows.Scan(&resource.Kind, &resource.ID, &resource.Name); err != nil {
			return page, err
		}
		if authorization.Check(input.Actor, domain.PermissionPolicyWrite, (domain.Scope{Kind: resource.Kind, ID: resource.ID}).Resource()) == nil {
			page.Resources = append(page.Resources, resource)
		}
	}
	return page, rows.Err()
}

// MutatePermissionPolicy owns validation, live authorization, bindings, audit,
// and the retry receipt in one serializable transaction.
func (s *Store) MutatePermissionPolicy(ctx context.Context, input application.MutatePermissionPolicyInput) (domain.PermissionPolicy, error) {
	if err := input.Validate(); err != nil {
		return domain.PermissionPolicy{}, err
	}

	for attempt := 0; attempt < 3; attempt++ {
		result, err := s.mutatePermissionPolicyAttempt(ctx, input)
		var pgErr *pgconn.PgError
		retry := errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01" || (pgErr.Code == "23505" && pgErr.ConstraintName == "permission_policy_commands_pkey"))
		if !retry {
			return result, err
		}
	}
	return domain.PermissionPolicy{}, fmt.Errorf("permissions changed. Reload and try again: %w", domain.ErrConflict)
}

func (s *Store) mutatePermissionPolicyAttempt(ctx context.Context, input application.MutatePermissionPolicyInput) (domain.PermissionPolicy, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return domain.PermissionPolicy{}, err
	}
	defer tx.Rollback(ctx)
	result, err := s.mutatePermissionPolicy(ctx, tx, input)
	if err != nil {
		return domain.PermissionPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.PermissionPolicy{}, err
	}
	return result, nil
}

func (s *Store) mutatePermissionPolicy(ctx context.Context, tx pgx.Tx, input application.MutatePermissionPolicyInput) (domain.PermissionPolicy, error) {
	empty := domain.PermissionPolicy{}
	org := input.Actor.OrganizationID
	var active bool
	if err := tx.QueryRow(ctx, `SELECT m.status='active' AND o.status='active' FROM memberships m JOIN organizations o ON o.id=m.organization_id WHERE m.organization_id=$1 AND m.id=$2 FOR SHARE OF m,o`, org, input.Actor.MembershipID).Scan(&active); err != nil || !active {
		return empty, domain.ErrForbidden
	}
	grants, err := loadGrants(ctx, tx, org, domain.PrincipalMembership, input.Actor.MembershipID)
	if err != nil {
		return empty, err
	}
	input.Actor = input.Actor.WithGrants(grants)
	if input.PrincipalKind == domain.PrincipalAPIKey {
		// Permission edits delegate access just as issuance does. Revalidate the
		// current session, including expiry, revocation and assurance, in this transaction.
		if err := tx.QueryRow(ctx, `SELECT s.auth_method,s.assurance,s.authenticated_at FROM sessions s JOIN memberships m ON m.user_id=s.user_id AND m.organization_id=s.organization_id WHERE s.id=$1 AND s.organization_id=$2 AND m.id=$3 AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp()`, input.Actor.Auth.SessionID, org, input.Actor.MembershipID).Scan(&input.Actor.Auth.Method, &input.Actor.Auth.Assurance, &input.Actor.Auth.AuthenticatedAt); err != nil {
			return empty, domain.ErrUnauthenticated
		}
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&input.Now); err != nil {
			return empty, err
		}
		if err := authorization.Check(input.Actor, domain.PermissionAPIKeyOrganizationIssue, domain.OrganizationResource()); err != nil {
			return empty, err
		}
	}

	if !authorization.HoldsAnywhere(input.Actor, domain.PermissionPolicyWrite) {
		return empty, domain.ErrForbidden
	}
	intent, _ := json.Marshal(struct {
		Kind              domain.PrincipalKind
		Principal, Policy domain.ID
		Spec              domain.PermissionPolicySpec
		Revision          int64
		Delete            bool
	}{input.PrincipalKind, input.PrincipalID, input.PolicyID, input.Spec, input.Revision, input.Delete})
	sum := sha256.Sum256(intent)
	hash := hex.EncodeToString(sum[:])
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, string(org)+string(input.Actor.MembershipID)+input.IdempotencyKey); err != nil {
		return empty, err
	}
	var previousHash string
	var receipt []byte
	err = tx.QueryRow(ctx, `SELECT intent_hash,result FROM permission_policy_commands WHERE organization_id=$1 AND membership_id=$2 AND idempotency_key=$3`, org, input.Actor.MembershipID, input.IdempotencyKey).Scan(&previousHash, &receipt)
	if err == nil {
		if previousHash != hash {
			return empty, fmt.Errorf("idempotency key was used for another change: %w", domain.ErrConflict)
		}
		err = json.Unmarshal(receipt, &empty)
		return empty, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return empty, err
	}
	if input.PrincipalKind == domain.PrincipalTeam {
		if err := requireLiveTeam(ctx, tx, org, input.PrincipalID); err != nil {
			return empty, err
		}
	}
	page, err := policyState(ctx, tx, input.ReadPermissionPoliciesInput)
	if err != nil {
		return empty, err
	}
	if page.ReadOnlyReason != "" {
		return empty, fmt.Errorf("%s: %w", page.ReadOnlyReason, domain.ErrForbidden)
	}
	actorAssignments, err := listAssignments(ctx, tx, org, domain.PrincipalMembership, input.Actor.MembershipID)
	if err != nil {
		return empty, err
	}
	if page.IsOwner && !domain.HoldsRoleEverywhere(actorAssignments, domain.RoleOwner) {
		return empty, domain.ErrForbidden
	}
	var before *domain.PermissionPolicy
	if input.PolicyID != "" {
		for _, p := range page.Policies {
			if p.ID == input.PolicyID {
				copy := p
				before = &copy
				break
			}
		}
		if before == nil {
			return empty, domain.ErrNotFound
		}
		if before.ViaTeamID != "" || before.Source != domain.AssignmentManual {
			return empty, domain.ErrForbidden
		}
		if before.Revision != input.Revision {
			return empty, fmt.Errorf("this policy changed. Reload and try again: %w", domain.ErrConflict)
		}
		if !policyScopeAllowed(input.Actor, domain.PermissionPolicyWrite, before.PermissionPolicySpec) {
			return empty, domain.ErrForbidden
		}
	}
	sensitive := input.PrincipalKind == domain.PrincipalAPIKey
	if before != nil {
		for _, key := range before.Permissions {
			sensitive = sensitive || domain.PermissionIsDangerous(key)
		}
	}
	if !input.Delete {
		for _, scope := range input.Spec.Scopes() {
			if err := authorization.Check(input.Actor, domain.PermissionPolicyWrite, scope.Resource()); err != nil {
				return empty, err
			}
			for _, key := range input.Spec.Permissions {
				if err := authorization.Check(input.Actor, key, scope.Resource()); err != nil {
					return empty, err
				}
				sensitive = sensitive || domain.PermissionIsDangerous(key)
			}
			if err := validatePolicyResource(ctx, tx, org, scope); err != nil {
				return empty, err
			}
		}
	}
	if sensitive {
		policy, err := readOrganizationPolicy(ctx, tx, org, "")
		if err != nil {
			return empty, err
		}
		if rejection := authorization.RequireFresh(input.Actor.Auth, policy.ApprovalReauthWindow, policy.MFAMode, input.Now); rejection != nil {
			return empty, rejection.Err
		}
	}
	id := input.PolicyID
	revision := int64(1)
	if id == "" {
		id = domain.ID(uuid.NewString())
		if _, err := tx.Exec(ctx, `INSERT INTO permission_policies(id,organization_id) VALUES($1,$2)`, id, org); err != nil {
			return empty, err
		}
	} else {
		revision = before.Revision + 1
		tag, err := tx.Exec(ctx, `UPDATE permission_policies SET revision=revision+1 WHERE id=$1 AND organization_id=$2 AND revision=$3`, id, org, input.Revision)
		if err != nil {
			return empty, err
		}
		if tag.RowsAffected() != 1 {
			return empty, domain.ErrConflict
		}
		if _, err := tx.Exec(ctx, `DELETE FROM role_assignments WHERE organization_id=$1 AND permission_policy_id=$2`, org, id); err != nil {
			return empty, err
		}
	}
	result := domain.PermissionPolicy{ID: id, Revision: revision, Source: domain.AssignmentManual, CanEdit: true, PermissionPolicySpec: input.Spec, Resources: []domain.PermissionResource{}}
	event := "permission_policy.created"
	if before != nil {
		event = "permission_policy.updated"
	}
	if input.Delete {
		event = "permission_policy.deleted"
		result = *before
		result.Revision = revision
		result.CanEdit = false
	} else {
		if err := insertPolicyBindings(ctx, tx, input.ReadPermissionPoliciesInput, id, input.Spec, input.Now); err != nil {
			return empty, err
		}
		saved, err := policyState(ctx, tx, input.ReadPermissionPoliciesInput)
		if err != nil {
			return empty, err
		}
		for _, p := range saved.Policies {
			if p.ID == id {
				result = p
				result.CanEdit = true
				break
			}
		}
	}
	actorID, err := membershipActorID(ctx, tx, org, input.Actor.MembershipID)
	if err != nil {
		return empty, err
	}
	data, _ := json.Marshal(map[string]any{"before": before, "after": result, "principal_kind": input.PrincipalKind, "principal_id": input.PrincipalID})
	if err := s.appendAuditEvent(ctx, tx, domain.AuditEvent{OrganizationID: org, EventType: event, ActorType: domain.ActorHuman, ActorID: actorID, SubjectType: "permission_policy", SubjectID: id, Data: data, OccurredAt: input.Now}); err != nil {
		return empty, err
	}
	encoded, _ := json.Marshal(result)
	if _, err := tx.Exec(ctx, `INSERT INTO permission_policy_commands(organization_id,membership_id,idempotency_key,intent_hash,result,created_at) VALUES($1,$2,$3,$4,$5,$6)`, org, input.Actor.MembershipID, input.IdempotencyKey, hash, encoded, input.Now); err != nil {
		return empty, err
	}
	return result, nil
}

// CompilePermissionPolicies validates the complete admission intent before
// creating any bundles. Bundle creation is private; the admission itself owns
// the user-visible audit event. No assignment is granted here.
func (s *Store) CompilePermissionPolicies(ctx context.Context, input application.CompilePermissionPoliciesInput) ([]domain.AssignmentSpec, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	org := input.Actor.OrganizationID
	var active bool
	if err := tx.QueryRow(ctx, `SELECT m.status='active' AND o.status='active' FROM memberships m JOIN organizations o ON o.id=m.organization_id WHERE m.organization_id=$1 AND m.id=$2 FOR SHARE OF m,o`, org, input.Actor.MembershipID).Scan(&active); err != nil {
		return nil, err
	}
	if !active {
		return nil, domain.ErrForbidden
	}
	grants, err := loadGrants(ctx, tx, org, domain.PrincipalMembership, input.Actor.MembershipID)
	if err != nil {
		return nil, err
	}
	actor := input.Actor.WithGrants(grants)
	sensitive := false
	for _, policy := range input.Policies {
		for _, scope := range policy.Scopes() {
			if err := authorization.Check(actor, domain.PermissionPolicyWrite, scope.Resource()); err != nil {
				return nil, err
			}
			for _, key := range policy.Permissions {
				if err := authorization.Check(actor, key, scope.Resource()); err != nil {
					return nil, err
				}
				if domain.PermissionIsDangerous(key) {
					if input.Unattended {
						return nil, fmt.Errorf("sensitive permissions cannot be granted automatically: %w", domain.ErrInvalid)
					}
					sensitive = true
				}
			}
			if err := validatePolicyResource(ctx, tx, org, scope); err != nil {
				return nil, err
			}
		}
	}
	if sensitive {
		policy, err := readOrganizationPolicy(ctx, tx, org, "")
		if err != nil {
			return nil, err
		}
		if rejection := authorization.RequireFresh(actor.Auth, policy.ApprovalReauthWindow, policy.MFAMode, input.Now); rejection != nil {
			return nil, rejection.Err
		}
	}
	assignments := []domain.AssignmentSpec{}
	for _, policy := range input.Policies {
		roleID := domain.ID(uuid.NewString())
		if _, err := tx.Exec(ctx, `INSERT INTO roles(id,organization_id,key,name,permissions,bindable_scope_kinds,built_in,created_at,updated_at) VALUES($1,$2,$3,'Permission policy',$4,$5,false,$6,$6)`, roleID, org, "policy."+string(roleID), permissionStrings(policy.Permissions), []string{"organization", "team", "agent", "pipeline"}, input.Now); err != nil {
			return nil, err
		}
		for _, scope := range policy.Scopes() {
			assignments = append(assignments, domain.AssignmentSpec{RoleID: roleID, ScopeKind: scope.Kind, ScopeID: scope.ID})
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return assignments, nil
}

func validatePolicyResource(ctx context.Context, q querier, org domain.ID, scope domain.Scope) error {
	if scope.Kind == domain.ScopeOrganization {
		return nil
	}
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM teams WHERE organization_id=$1 AND id::text=$2 AND archived_at IS NULL)`
	switch scope.Kind {
	case domain.ScopeAgent:
		query = `SELECT EXISTS(SELECT 1 FROM agents WHERE organization_id=$1 AND slug=$2 AND archived_at IS NULL)`
	case domain.ScopePipeline:
		query = `SELECT EXISTS(SELECT 1 FROM request_approval_pipelines WHERE organization_id=$1 AND pipeline_key=$2 AND archived_at IS NULL)`
	}
	if err := q.QueryRow(ctx, query, org, scope.ID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("selected resource is no longer available: %w", domain.ErrConflict)
	}
	return nil
}

func policyHolder(kind domain.PrincipalKind) domain.PermissionHolder {
	if kind == domain.PrincipalAPIKey {
		return domain.HolderOrganizationAPIKey
	}
	return domain.HolderHuman
}

// insertPolicyBindings is shared by key issuance and the policy editor. Every
// policy owns a private permission bundle and one assignment per resource.
func insertPolicyBindings(ctx context.Context, tx pgx.Tx, target application.ReadPermissionPoliciesInput, policyID domain.ID, spec domain.PermissionPolicySpec, now time.Time) error {
	roleID := uuid.NewString()
	permissions := slices.Clone(spec.Permissions)
	slices.Sort(permissions)
	if _, err := tx.Exec(ctx, `INSERT INTO roles(id,organization_id,key,name,permissions,bindable_scope_kinds,built_in,created_at,updated_at) VALUES($1,$2,$3,'Permission policy',$4,$5,false,$6,$6)`, roleID, target.Actor.OrganizationID, "policy."+roleID, permissionStrings(permissions), []string{"organization", "team", "agent", "pipeline"}, now); err != nil {
		return err
	}
	for _, scope := range spec.Scopes() {
		if _, err := tx.Exec(ctx, `INSERT INTO role_assignments(id,organization_id,principal_kind,principal_id,role_id,scope_kind,scope_id,source,source_key,granted_by_actor_id,created_at,permission_policy_id) VALUES($1,$2,$3,$4,$5,$6,$7,'manual','',$8,$9,$10)`, uuid.NewString(), target.Actor.OrganizationID, target.PrincipalKind, target.PrincipalID, roleID, scope.Kind, scope.ID, target.Actor.ManagementActorID(), now, policyID); err != nil {
			return err
		}
	}
	return nil
}
