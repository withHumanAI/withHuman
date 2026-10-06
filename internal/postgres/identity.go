package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
)

func (s *Store) HasOwner(ctx context.Context) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (
        SELECT 1 FROM role_assignments ra
        JOIN memberships m ON m.id = ra.principal_id AND ra.principal_kind = 'membership'
        JOIN organizations o ON o.id = m.organization_id
        WHERE ra.role_id = $1 AND ra.scope_kind = 'organization'
          AND m.status = 'active' AND o.status = 'active'
    )`, domain.RoleOwnerID).Scan(&exists)
	return exists, err
}

func (s *Store) GetUser(ctx context.Context, input application.GetUserInput) (domain.User, error) {
	if err := input.Validate(); err != nil {
		return domain.User{}, err
	}
	var user domain.User
	var id, status string
	err := s.pool.QueryRow(ctx, `SELECT id::text, display_name, primary_email, status, created_at, updated_at
        FROM users WHERE id = $1`, input.UserID).Scan(&id, &user.DisplayName, &user.PrimaryEmail, &status, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	user.ID, user.Status = domain.ID(id), domain.UserStatus(status)
	return user, err
}

func (s *Store) GetMembership(ctx context.Context, input application.GetMembershipInput) (domain.Membership, error) {
	if err := input.Validate(); err != nil {
		return domain.Membership{}, err
	}
	var membership domain.Membership
	var id, userID, status, source string
	var teams []string
	err := s.pool.QueryRow(ctx, `SELECT m.id::text, m.user_id::text, m.status, m.admission_source,
        m.directory_managed, m.created_at, m.updated_at,
        COALESCE((SELECT array_agg(DISTINCT team_id::text) FROM active_membership_teams WHERE membership_id = m.id), '{}')
      FROM memberships m WHERE m.organization_id = $1 AND m.id = $2`, input.OrganizationID, input.MembershipID).Scan(
		&id, &userID, &status, &source, &membership.DirectoryManaged,
		&membership.CreatedAt, &membership.UpdatedAt, &teams,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Membership{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Membership{}, err
	}
	membership.ID, membership.UserID, membership.OrganizationID = domain.ID(id), domain.ID(userID), input.OrganizationID
	membership.Status, membership.AdmissionSource = domain.MembershipStatus(status), domain.AdmissionSource(source)
	for _, team := range teams {
		membership.TeamIDs = append(membership.TeamIDs, domain.ID(team))
	}
	assignments, err := listAssignments(ctx, s.pool, input.OrganizationID, domain.PrincipalMembership, membership.ID)
	if err != nil {
		return domain.Membership{}, err
	}
	membership.Assignments = assignments
	return membership, nil
}

func (s *Store) GetOrganization(ctx context.Context, input application.GetOrganizationInput) (domain.Organization, error) {
	if err := input.Validate(); err != nil {
		return domain.Organization{}, err
	}
	return s.scanOrganization(s.pool.QueryRow(ctx, `SELECT id::text, slug, name, status, status_reason,
        suspended_at, deletion_scheduled_for, onboarding_completed_at, created_at, updated_at
        FROM organizations WHERE id = $1`, input.OrganizationID))
}

func (s *Store) FindOrganizationBySlug(ctx context.Context, input application.FindOrganizationBySlugInput) (domain.Organization, error) {
	if err := input.Validate(); err != nil {
		return domain.Organization{}, err
	}
	return s.scanOrganization(s.pool.QueryRow(ctx, `SELECT id::text, slug, name, status, status_reason,
        suspended_at, deletion_scheduled_for, onboarding_completed_at, created_at, updated_at
        FROM organizations WHERE slug = $1`, input.OrganizationSlug))
}

func (s *Store) scanOrganization(row rowScanner) (domain.Organization, error) {
	var organization domain.Organization
	var id, status string
	err := row.Scan(&id, &organization.Slug, &organization.Name, &status, &organization.StatusReason,
		&organization.SuspendedAt, &organization.DeletionScheduledFor, &organization.OnboardingCompletedAt,
		&organization.CreatedAt, &organization.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Organization{}, domain.ErrNotFound
	}
	organization.ID, organization.Status = domain.ID(id), domain.OrganizationStatus(status)
	return organization, err
}

func (s *Store) ResolveExternalIdentity(ctx context.Context, input application.ResolveExternalIdentityInput) (domain.User, error) {
	if err := input.Validate(); err != nil {
		return domain.User{}, err
	}
	var user domain.User
	var id, status string
	err := s.pool.QueryRow(ctx, `UPDATE external_identities e
        SET last_authenticated_at = $4,
            last_observed_email = CASE WHEN $5 <> '' THEN $5 ELSE e.last_observed_email END
        FROM users u
        WHERE e.provider_key = $1 AND e.issuer = $2 AND e.subject = $3
          AND u.id = e.user_id AND u.status = 'active'
        RETURNING u.id::text, u.display_name, u.primary_email, u.status, u.created_at, u.updated_at`,
		input.ProviderKey, input.Issuer, input.Subject, input.Now, input.ObservedEmail).Scan(
		&id, &user.DisplayName, &user.PrimaryEmail, &status, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	user.ID, user.Status = domain.ID(id), domain.UserStatus(status)
	return user, err
}

// LinkVerifiedMemberIdentity binds a provider identity to the existing
// member whose email the organization's own IdP asserted. Identity linking
// only: it never creates a membership, and only an active membership gets
// the binding, so a suspended member's SSO attempt surfaces as inactive
// rather than minting a fresh identity link.
func (s *Store) LinkVerifiedMemberIdentity(ctx context.Context, input application.LinkVerifiedMemberIdentityInput) (domain.Membership, error) {
	if err := input.Validate(); err != nil {
		return domain.Membership{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return domain.Membership{}, err
	}
	defer tx.Rollback(ctx)
	var membershipID, userID, status string
	err = tx.QueryRow(ctx, `SELECT m.id::text, u.id::text, m.status
        FROM users u JOIN memberships m ON m.user_id = u.id
        WHERE lower(u.primary_email) = lower($1) AND u.status = 'active'
          AND m.organization_id = $2`, input.Email, input.OrganizationID).Scan(&membershipID, &userID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Membership{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Membership{}, err
	}
	membership := domain.Membership{
		ID: domain.ID(membershipID), UserID: domain.ID(userID),
		OrganizationID: input.OrganizationID, Status: domain.MembershipStatus(status),
	}
	if membership.Status != domain.MembershipActive {
		return membership, nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO external_identities
        (user_id, provider_key, issuer, subject, last_observed_email, email_verified, linked_at, last_authenticated_at)
        VALUES ($1, $2, $3, $4, $5, true, $6, $6)
        ON CONFLICT (provider_key, issuer, subject) DO UPDATE
        SET last_observed_email = EXCLUDED.last_observed_email,
            last_authenticated_at = EXCLUDED.last_authenticated_at`,
		userID, input.ProviderKey, input.Issuer, input.Subject, strings.ToLower(input.Email), input.Now); err != nil {
		return domain.Membership{}, err
	}
	data, _ := json.Marshal(map[string]string{
		"provider_key": input.ProviderKey, "issuer": input.Issuer, "email": strings.ToLower(input.Email),
	})
	if err := s.appendAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "membership.identity_linked",
		ActorType: domain.ActorHuman, ActorID: domain.ID(userID), SubjectType: "membership",
		SubjectID: membership.ID, Data: data, OccurredAt: input.Now,
	}); err != nil {
		return domain.Membership{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Membership{}, err
	}
	return membership, nil
}

// LinkVerifiedUserIdentity binds a provider identity to the active user
// whose primary email the provider verified. Global identity linking for
// an existing account, never account creation: no user and no membership
// is created. A subject already bound to a different user is a conflict,
// not a silent re-link. Each of the user's active organizations records
// the link, because a new way into the account is a new way into every
// organization it belongs to.
func (s *Store) LinkVerifiedUserIdentity(ctx context.Context, input application.LinkVerifiedUserIdentityInput) (domain.User, error) {
	if err := input.Validate(); err != nil {
		return domain.User{}, err
	}
	email := strings.ToLower(strings.TrimSpace(input.Email))
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return domain.User{}, err
	}
	defer tx.Rollback(ctx)
	var user domain.User
	var id, status string
	err = tx.QueryRow(ctx, `SELECT id::text, display_name, primary_email, status, created_at, updated_at
        FROM users WHERE lower(primary_email) = $1 AND status = 'active'`, email).Scan(
		&id, &user.DisplayName, &user.PrimaryEmail, &status, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, err
	}
	user.ID, user.Status = domain.ID(id), domain.UserStatus(status)
	if err := linkExternalIdentity(ctx, tx, user.ID, input.ProviderKey, input.Issuer, input.Subject, email, input.Now); err != nil {
		return domain.User{}, err
	}
	rows, err := tx.Query(ctx, `SELECT m.id::text, m.organization_id::text FROM memberships m
        JOIN organizations o ON o.id = m.organization_id
        WHERE m.user_id = $1 AND m.status = 'active' AND o.status = 'active'`, id)
	if err != nil {
		return domain.User{}, err
	}
	type activeMembership struct{ id, organizationID string }
	var memberships []activeMembership
	for rows.Next() {
		var membership activeMembership
		if err := rows.Scan(&membership.id, &membership.organizationID); err != nil {
			rows.Close()
			return domain.User{}, err
		}
		memberships = append(memberships, membership)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return domain.User{}, err
	}
	data, _ := json.Marshal(map[string]string{
		"provider_key": input.ProviderKey, "issuer": input.Issuer, "email": email,
	})
	for _, membership := range memberships {
		if err := s.appendAuditEvent(ctx, tx, domain.AuditEvent{
			OrganizationID: domain.ID(membership.organizationID), EventType: "membership.identity_linked",
			ActorType: domain.ActorHuman, ActorID: user.ID, SubjectType: "membership",
			SubjectID: domain.ID(membership.id), Data: data, OccurredAt: input.Now,
		}); err != nil {
			return domain.User{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

// linkExternalIdentity upserts one provider identity for a user. The
// identity key is the (provider, issuer, subject) triple: re-observing a
// linked identity refreshes its email and timestamp, while a triple that
// already belongs to another user is refused as a conflict.
func linkExternalIdentity(ctx context.Context, tx pgx.Tx, userID domain.ID, providerKey, issuer, subject, email string, now time.Time) error {
	var boundUserID string
	err := tx.QueryRow(ctx, `INSERT INTO external_identities
        (user_id, provider_key, issuer, subject, last_observed_email, email_verified, linked_at, last_authenticated_at)
        VALUES ($1, $2, $3, $4, $5, true, $6, $6)
        ON CONFLICT (provider_key, issuer, subject) DO UPDATE
        SET last_observed_email = EXCLUDED.last_observed_email,
            last_authenticated_at = EXCLUDED.last_authenticated_at
        RETURNING user_id::text`,
		string(userID), providerKey, issuer, subject, strings.ToLower(email), now).Scan(&boundUserID)
	if err != nil {
		return err
	}
	if boundUserID != string(userID) {
		return fmt.Errorf("this sign-in is linked to another account: %w", domain.ErrConflict)
	}
	return nil
}

func (s *Store) GetOrganizationPolicy(ctx context.Context, input application.GetOrganizationPolicyInput) (domain.OrganizationPolicy, error) {
	if err := input.Validate(); err != nil {
		return domain.OrganizationPolicy{}, err
	}
	return readOrganizationPolicy(ctx, s.pool, input.OrganizationID, "")
}

// readOrganizationPolicy reads one organization's policy row; lock is
// appended to the query so a writer can take the row FOR UPDATE.
func readOrganizationPolicy(ctx context.Context, q routingQuerier, organizationID domain.ID, lock string) (domain.OrganizationPolicy, error) {
	var policy domain.OrganizationPolicy
	var admissionMode, mfaMode, unrouted, visibility string
	var reauthSeconds, sessionSeconds, claimRetriggerSeconds int64
	err := q.QueryRow(ctx, `SELECT admission_mode, mfa_mode,
        approval_reauth_window_seconds, session_max_age_seconds, claim_retrigger_seconds,
        unrouted_requests, request_visibility, personal_api_keys_allowed, organization_api_keys_allowed
      FROM organization_policies WHERE organization_id = $1 `+lock, organizationID).Scan(
		&admissionMode, &mfaMode, &reauthSeconds, &sessionSeconds, &claimRetriggerSeconds,
		&unrouted, &visibility, &policy.PersonalAPIKeysAllowed, &policy.OrganizationAPIKeysAllowed,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OrganizationPolicy{}, domain.ErrNotFound
	}
	policy.OrganizationID = organizationID
	policy.AdmissionMode, policy.MFAMode = domain.AdmissionMode(admissionMode), domain.MFAMode(mfaMode)
	policy.ApprovalReauthWindow, policy.SessionMaxAge = time.Duration(reauthSeconds)*time.Second, time.Duration(sessionSeconds)*time.Second
	policy.ClaimRetriggerWindow = time.Duration(claimRetriggerSeconds) * time.Second
	policy.UnroutedRequests, policy.RequestVisibility = domain.UnroutedRequestsPolicy(unrouted), domain.RequestVisibilityPolicy(visibility)
	return policy, err
}

// policyAuditFields is the approval-policy subset an administrator edits,
// recorded before and after every update so the audit trail shows the
// whole change rather than only the fields that moved.
func policyAuditFields(policy domain.OrganizationPolicy) map[string]any {
	return map[string]any{
		"mfa_mode":                      policy.MFAMode,
		"approval_reauth_window":        policy.ApprovalReauthWindow.String(),
		"claim_retrigger_window":        policy.ClaimRetriggerWindow.String(),
		"unrouted_requests":             policy.UnroutedRequests,
		"request_visibility":            policy.RequestVisibility,
		"personal_api_keys_allowed":     policy.PersonalAPIKeysAllowed,
		"organization_api_keys_allowed": policy.OrganizationAPIKeysAllowed,
	}
}

// UpdateOrganizationPolicy replaces the approval policy knobs in one
// transaction and audits the change as organization.policy_updated with
// the before and after values.
func (s *Store) UpdateOrganizationPolicy(ctx context.Context, input application.UpdateOrganizationPolicyInput) (domain.OrganizationPolicy, error) {
	if err := input.Validate(); err != nil {
		return domain.OrganizationPolicy{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.OrganizationPolicy{}, err
	}
	defer tx.Rollback(ctx)
	before, err := readOrganizationPolicy(ctx, tx, input.OrganizationID, "FOR UPDATE")
	if err != nil {
		return domain.OrganizationPolicy{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE organization_policies
        SET mfa_mode = $2, approval_reauth_window_seconds = $3,
            claim_retrigger_seconds = $4, unrouted_requests = $5, request_visibility = $6,
            personal_api_keys_allowed = $7, organization_api_keys_allowed = $8
        WHERE organization_id = $1`,
		input.OrganizationID, string(input.MFAMode),
		int64(input.ApprovalReauthWindow/time.Second), int64(input.ClaimRetriggerWindow/time.Second),
		string(input.UnroutedRequests), string(input.RequestVisibility), input.PersonalAPIKeysAllowed, input.OrganizationAPIKeysAllowed); err != nil {
		return domain.OrganizationPolicy{}, err
	}
	after, err := readOrganizationPolicy(ctx, tx, input.OrganizationID, "")
	if err != nil {
		return domain.OrganizationPolicy{}, err
	}
	data, _ := json.Marshal(map[string]any{"before": policyAuditFields(before), "after": policyAuditFields(after)})
	if err := s.appendAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "organization.policy_updated",
		ActorType: domain.ActorHuman, ActorID: input.ActorMembershipID,
		SubjectType: "organization", SubjectID: input.OrganizationID,
		Data: data, OccurredAt: input.Now,
	}); err != nil {
		return domain.OrganizationPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.OrganizationPolicy{}, err
	}
	return after, nil
}
