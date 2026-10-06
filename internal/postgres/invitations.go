package postgres

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
)

const uniqueViolationCode = "23505"

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

func (s *Store) CreateInvitation(ctx context.Context, input application.CreateInvitationInput) (domain.Invitation, error) {
	if err := input.Validate(); err != nil {
		return domain.Invitation{}, err
	}
	var invitation domain.Invitation
	err := retrySerializable(func() error {
		var err error
		invitation, err = s.createInvitation(ctx, input)
		return err
	})
	return invitation, err
}

func (s *Store) createInvitation(ctx context.Context, input application.CreateInvitationInput) (domain.Invitation, error) {
	alloc, err := s.beginAllocation(ctx, application.DimensionSeats, string(input.OrganizationID), input.SeatQuota)
	if err != nil {
		return domain.Invitation{}, err
	}
	defer alloc.Close(ctx)
	tx := alloc.Tx()
	// A pending invitation reserves a seat, so the quota is checked here,
	// under the seat lock, before the reservation is written.
	if err := enforceSeatQuota(ctx, tx, string(input.OrganizationID), input.SeatQuota, input.Now, ""); err != nil {
		return domain.Invitation{}, err
	}
	var alreadyMember bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
        SELECT 1 FROM memberships m JOIN users u ON u.id = m.user_id
        WHERE m.organization_id = $1 AND lower(u.primary_email) = lower($2)
          AND m.status <> 'deprovisioned'
    )`, input.OrganizationID, input.Email).Scan(&alreadyMember); err != nil {
		return domain.Invitation{}, err
	}
	if alreadyMember {
		return domain.Invitation{}, fmt.Errorf("a member with this email already exists: %w", domain.ErrConflict)
	}
	// Every granted role must be one the organization may grant now;
	// acceptance checks again because a custom role can be deleted in
	// between.
	for _, spec := range input.Grants {
		if _, err := lookupRole(ctx, tx, input.OrganizationID, spec.RoleID); err != nil {
			return domain.Invitation{}, err
		}
	}
	grants, err := json.Marshal(input.Grants)
	if err != nil {
		return domain.Invitation{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO invitations
        (id, organization_id, email, grants, invited_by_membership_id, token_hash, status, expires_at, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, $8, $8)`,
		input.InvitationID, input.OrganizationID, input.Email, grants,
		input.InvitedByMembershipID, input.TokenHash, input.ExpiresAt, input.Now); err != nil {
		if isUniqueViolation(err) {
			return domain.Invitation{}, fmt.Errorf("a pending invitation for this email already exists: %w", domain.ErrConflict)
		}
		return domain.Invitation{}, err
	}
	if err := s.insertInvitationAudit(ctx, tx, input.OrganizationID, "invitation.created",
		input.InvitedByMembershipID, input.InvitationID,
		map[string]any{"email": input.Email, "grants": input.Grants}, input.Now); err != nil {
		return domain.Invitation{}, err
	}
	if err := alloc.Commit(ctx); err != nil {
		if isUniqueViolation(err) {
			return domain.Invitation{}, fmt.Errorf("a pending invitation for this email already exists: %w", domain.ErrConflict)
		}
		return domain.Invitation{}, err
	}
	return domain.Invitation{
		ID: input.InvitationID, OrganizationID: input.OrganizationID, Email: input.Email,
		Grants: input.Grants, InvitedByMembershipID: input.InvitedByMembershipID,
		Status: domain.InvitationPending, ExpiresAt: input.ExpiresAt,
		CreatedAt: input.Now, UpdatedAt: input.Now,
	}, nil
}

func (s *Store) ListInvitations(ctx context.Context, input application.ListInvitationsInput) ([]domain.Invitation, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text, email, grants, invited_by_membership_id::text,
        status, expires_at, COALESCE(accepted_membership_id::text, ''), created_at, updated_at
        FROM invitations WHERE organization_id = $1
        ORDER BY created_at DESC LIMIT $2`, input.OrganizationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	invitations := make([]domain.Invitation, 0)
	for rows.Next() {
		invitation, err := scanInvitation(rows, input.OrganizationID)
		if err != nil {
			return nil, err
		}
		invitations = append(invitations, invitation)
	}
	return invitations, rows.Err()
}

func (s *Store) GetInvitation(ctx context.Context, input application.GetInvitationInput) (domain.Invitation, error) {
	if err := input.Validate(); err != nil {
		return domain.Invitation{}, err
	}
	row := s.pool.QueryRow(ctx, `SELECT id::text, email, grants, invited_by_membership_id::text,
        status, expires_at, COALESCE(accepted_membership_id::text, ''), created_at, updated_at
        FROM invitations WHERE organization_id = $1 AND id = $2`, input.OrganizationID, input.InvitationID)
	invitation, err := scanInvitation(row, input.OrganizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invitation{}, domain.ErrNotFound
	}
	return invitation, err
}

func (s *Store) RotateInvitationToken(ctx context.Context, input application.RotateInvitationTokenInput) (domain.Invitation, error) {
	if err := input.Validate(); err != nil {
		return domain.Invitation{}, err
	}
	var invitation domain.Invitation
	err := retrySerializable(func() error {
		var err error
		invitation, err = s.rotateInvitationToken(ctx, input)
		return err
	})
	return invitation, err
}

func (s *Store) rotateInvitationToken(ctx context.Context, input application.RotateInvitationTokenInput) (domain.Invitation, error) {
	alloc, err := s.beginAllocation(ctx, application.DimensionSeats, string(input.OrganizationID), input.SeatQuota)
	if err != nil {
		return domain.Invitation{}, err
	}
	defer alloc.Close(ctx)
	tx := alloc.Tx()
	var expired bool
	err = tx.QueryRow(ctx, `SELECT expires_at <= $3 FROM invitations
        WHERE organization_id = $1 AND id = $2 AND status = 'pending' FOR UPDATE`,
		input.OrganizationID, input.InvitationID, input.Now).Scan(&expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invitation{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Invitation{}, err
	}
	// A live invitation already holds its seat; reviving an expired one
	// reserves a seat again and must fit the quota.
	if expired {
		if err := enforceSeatQuota(ctx, tx, string(input.OrganizationID), input.SeatQuota, input.Now, string(input.InvitationID)); err != nil {
			return domain.Invitation{}, err
		}
	}
	row := tx.QueryRow(ctx, `UPDATE invitations
        SET token_hash = $3, expires_at = $4, updated_at = $5
        WHERE organization_id = $1 AND id = $2 AND status = 'pending'
        RETURNING id::text, email, grants, invited_by_membership_id::text, status, expires_at,
            COALESCE(accepted_membership_id::text, ''), created_at, updated_at`,
		input.OrganizationID, input.InvitationID, input.TokenHash, input.ExpiresAt, input.Now)
	invitation, err := scanInvitation(row, input.OrganizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invitation{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Invitation{}, err
	}
	if err := s.insertInvitationAudit(ctx, tx, input.OrganizationID, "invitation.resent",
		invitation.InvitedByMembershipID, invitation.ID,
		map[string]any{"email": invitation.Email}, input.Now); err != nil {
		return domain.Invitation{}, err
	}
	return invitation, alloc.Commit(ctx)
}

func (s *Store) RevokeInvitation(ctx context.Context, input application.RevokeInvitationInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	var email string
	err := s.pool.QueryRow(ctx, `UPDATE invitations
        SET status = 'revoked', updated_at = $3
        WHERE organization_id = $1 AND id = $2 AND status = 'pending'
        RETURNING email`, input.OrganizationID, input.InvitationID, input.Now).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	return s.appendInvitationAudit(ctx, input.OrganizationID, "invitation.revoked",
		"", input.InvitationID, map[string]any{"email": email}, input.Now)
}

func (s *Store) GetInvitationByToken(ctx context.Context, input application.GetInvitationByTokenInput) (application.InvitationPreview, error) {
	if err := input.Validate(); err != nil {
		return application.InvitationPreview{}, err
	}
	var preview application.InvitationPreview
	var organizationID string
	var tokenHash []byte
	row := s.pool.QueryRow(ctx, `SELECT i.id::text, i.organization_id::text, i.email, i.grants,
        i.invited_by_membership_id::text, i.status, i.expires_at,
        COALESCE(i.accepted_membership_id::text, ''), i.created_at, i.updated_at,
        i.token_hash, o.name, o.slug,
        COALESCE((SELECT u.display_name FROM memberships m JOIN users u ON u.id = m.user_id
            WHERE m.id = i.invited_by_membership_id), ''),
        EXISTS (SELECT 1 FROM users u WHERE lower(u.primary_email) = lower(i.email) AND u.status = 'active')
        FROM invitations i
        JOIN organizations o ON o.id = i.organization_id AND o.status = 'active'
        WHERE i.id = $1`, input.InvitationID)
	var invitation domain.Invitation
	var id, invitedBy, status, accepted string
	var grants []byte
	err := row.Scan(&id, &organizationID, &invitation.Email, &grants, &invitedBy, &status,
		&invitation.ExpiresAt, &accepted, &invitation.CreatedAt, &invitation.UpdatedAt,
		&tokenHash, &preview.OrganizationName, &preview.OrganizationSlug,
		&preview.InviterDisplayName, &preview.UserExists)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.InvitationPreview{}, domain.ErrNotFound
	}
	if err != nil {
		return application.InvitationPreview{}, err
	}
	if subtle.ConstantTimeCompare(tokenHash, input.TokenHash) != 1 {
		return application.InvitationPreview{}, domain.ErrNotFound
	}
	invitation.ID, invitation.OrganizationID = domain.ID(id), domain.ID(organizationID)
	invitation.InvitedByMembershipID, invitation.AcceptedMembershipID = domain.ID(invitedBy), domain.ID(accepted)
	invitation.Status = domain.InvitationStatus(status)
	if invitation.Grants, err = decodeGrants(grants); err != nil {
		return application.InvitationPreview{}, err
	}
	preview.Invitation = invitation
	switch invitation.Status {
	case domain.InvitationAccepted:
		return application.InvitationPreview{}, fmt.Errorf("this invitation was already accepted: %w", domain.ErrConflict)
	case domain.InvitationRevoked, domain.InvitationExpired:
		return application.InvitationPreview{}, domain.ErrNotFound
	}
	if !invitation.ExpiresAt.After(input.Now) {
		_, _ = s.pool.Exec(ctx, `UPDATE invitations SET status = 'expired', updated_at = $2
            WHERE id = $1 AND status = 'pending'`, invitation.ID, input.Now)
		return application.InvitationPreview{}, domain.ErrNotFound
	}
	return preview, nil
}

func (s *Store) FindPendingInvitationByEmail(ctx context.Context, input application.FindPendingInvitationByEmailInput) (domain.Invitation, error) {
	if err := input.Validate(); err != nil {
		return domain.Invitation{}, err
	}
	row := s.pool.QueryRow(ctx, `SELECT id::text, email, grants, invited_by_membership_id::text,
        status, expires_at, COALESCE(accepted_membership_id::text, ''), created_at, updated_at
        FROM invitations
        WHERE organization_id = $1 AND lower(email) = lower($2)
          AND status = 'pending' AND expires_at > $3`,
		input.OrganizationID, input.Email, input.Now)
	invitation, err := scanInvitation(row, input.OrganizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invitation{}, domain.ErrNotFound
	}
	return invitation, err
}

// AcceptInvitation is the admission transaction: it locks the invitation,
// verifies the token and the email binding, finds or creates the user with
// its external identity (and local credential when a password hash is
// provided), creates the active membership with the invitation's grants,
// marks the invitation accepted, and appends audit events.
func (s *Store) AcceptInvitation(ctx context.Context, input application.AcceptInvitationInput) (domain.Principal, error) {
	if err := input.Validate(); err != nil {
		return domain.Principal{}, err
	}
	var principal domain.Principal
	err := retrySerializable(func() error {
		var err error
		principal, err = s.acceptInvitation(ctx, input)
		return err
	})
	return principal, err
}

func (s *Store) acceptInvitation(ctx context.Context, input application.AcceptInvitationInput) (domain.Principal, error) {
	// The invitation's organization never changes, so it can be read
	// ahead of the transaction to name the seat lock.
	var lockOrganizationID string
	err := s.pool.QueryRow(ctx, `SELECT organization_id::text FROM invitations WHERE id = $1`, input.InvitationID).Scan(&lockOrganizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Principal{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Principal{}, err
	}
	alloc, err := s.beginAllocation(ctx, application.DimensionSeats, lockOrganizationID, input.SeatQuota)
	if err != nil {
		return domain.Principal{}, err
	}
	defer alloc.Close(ctx)
	tx := alloc.Tx()

	var organizationID, email, status, invitedBy string
	var grantsJSON, tokenHash []byte
	var expired bool
	err = tx.QueryRow(ctx, `SELECT organization_id::text, email, grants, invited_by_membership_id::text, status, token_hash, expires_at <= $2
        FROM invitations WHERE id = $1 FOR UPDATE`, input.InvitationID, input.Now).Scan(
		&organizationID, &email, &grantsJSON, &invitedBy, &status, &tokenHash, &expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Principal{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Principal{}, err
	}
	grants, err := decodeGrants(grantsJSON)
	if err != nil {
		return domain.Principal{}, err
	}
	if len(input.TokenHash) > 0 && subtle.ConstantTimeCompare(tokenHash, input.TokenHash) != 1 {
		return domain.Principal{}, domain.ErrNotFound
	}
	switch domain.InvitationStatus(status) {
	case domain.InvitationAccepted:
		return domain.Principal{}, fmt.Errorf("this invitation was already accepted: %w", domain.ErrConflict)
	case domain.InvitationRevoked, domain.InvitationExpired:
		return domain.Principal{}, domain.ErrNotFound
	}
	if expired {
		if _, err := tx.Exec(ctx, `UPDATE invitations SET status = 'expired', updated_at = $2
            WHERE id = $1`, input.InvitationID, input.Now); err != nil {
			return domain.Principal{}, err
		}
		if err := alloc.Commit(ctx); err != nil {
			return domain.Principal{}, err
		}
		return domain.Principal{}, domain.ErrNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(input.AssertedEmail), strings.TrimSpace(email)) {
		return domain.Principal{}, fmt.Errorf("%w: %w", domain.ErrEmailMismatch, domain.ErrForbidden)
	}
	// The quota may have shrunk since the invitation was sent. This
	// invitation's own reservation is excluded from the count: accepting
	// converts the reservation into the membership rather than adding one.
	if err := enforceSeatQuota(ctx, tx, organizationID, input.SeatQuota, input.Now, string(input.InvitationID)); err != nil {
		return domain.Principal{}, err
	}

	userID := input.UserID
	if userID == "" {
		// An unlinked user may already exist under the invitation email,
		// for example an SSO-asserted person who holds a local account in
		// another organization. Link rather than duplicate.
		var existingID string
		err := tx.QueryRow(ctx, `SELECT id::text FROM users
            WHERE lower(primary_email) = lower($1) AND status = 'active'`, email).Scan(&existingID)
		switch {
		case err == nil:
			if !input.LinkExistingUser {
				return domain.Principal{}, fmt.Errorf("%w: %w", domain.ErrUserExists, domain.ErrConflict)
			}
			userID = domain.ID(existingID)
		case errors.Is(err, pgx.ErrNoRows):
			userID = input.NewUserID
			if _, err := tx.Exec(ctx, `INSERT INTO users (id, display_name, primary_email, status, created_at, updated_at)
                VALUES ($1, $2, $3, 'active', $4, $4)`, userID, input.DisplayName, email, input.Now); err != nil {
				if isUniqueViolation(err) {
					return domain.Principal{}, fmt.Errorf("an account with this email already exists: %w", domain.ErrConflict)
				}
				return domain.Principal{}, err
			}
			if input.PasswordHash != "" {
				if _, err := tx.Exec(ctx, `INSERT INTO local_credentials (user_id, password_hash, created_at, updated_at)
                    VALUES ($1, $2, $3, $3)`, userID, input.PasswordHash, input.Now); err != nil {
					return domain.Principal{}, err
				}
			}
		default:
			return domain.Principal{}, err
		}
	}
	if input.ProviderKey != "" {
		subject := input.Subject
		if subject == "" {
			subject = string(userID)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO external_identities
            (user_id, provider_key, issuer, subject, last_observed_email, email_verified, linked_at, last_authenticated_at)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
            ON CONFLICT (provider_key, issuer, subject) DO UPDATE
            SET last_observed_email = EXCLUDED.last_observed_email,
                last_authenticated_at = EXCLUDED.last_authenticated_at`,
			userID, input.ProviderKey, input.Issuer, subject, input.AssertedEmail,
			input.ProviderKey != "local", input.Now); err != nil {
			return domain.Principal{}, err
		}
	}

	if _, err := tx.Exec(ctx, `INSERT INTO memberships
        (id, user_id, organization_id, status, admission_source, created_at, updated_at)
        VALUES ($1, $2, $3, 'active', 'invite', $4, $4)`,
		input.MembershipID, userID, organizationID, input.Now); err != nil {
		if isUniqueViolation(err) {
			return domain.Principal{}, fmt.Errorf("this person is already a member: %w", domain.ErrConflict)
		}
		return domain.Principal{}, err
	}
	// The grants are the inviter's: each row records who granted it, and
	// a role deleted since the invite was sent fails the acceptance
	// rather than silently admitting with less.
	if err := s.grantSpecs(ctx, tx, domain.ID(organizationID), assignmentWrite{
		Principal: domain.PrincipalMembership, PrincipalID: input.MembershipID,
		Source: domain.AssignmentManual, GrantedBy: domain.ID(invitedBy),
		ActorType: domain.ActorHuman, ActorID: domain.ID(invitedBy),
	}, grants, input.Now); err != nil {
		return domain.Principal{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE invitations
        SET status = 'accepted', accepted_membership_id = $2, updated_at = $3
        WHERE id = $1`, input.InvitationID, input.MembershipID, input.Now); err != nil {
		return domain.Principal{}, err
	}
	if err := s.insertInvitationAudit(ctx, tx, domain.ID(organizationID), "invitation.accepted",
		input.MembershipID, input.InvitationID,
		map[string]any{"email": email, "user_id": string(userID)}, input.Now); err != nil {
		return domain.Principal{}, err
	}
	if err := s.insertMembershipAudit(ctx, tx, domain.ID(organizationID), "membership.created",
		userID, input.MembershipID, map[string]any{"grants": grants, "admission_source": "invite"}, input.Now); err != nil {
		return domain.Principal{}, err
	}
	principalGrants, err := loadGrants(ctx, tx, domain.ID(organizationID), domain.PrincipalMembership, input.MembershipID)
	if err != nil {
		return domain.Principal{}, err
	}
	if err := alloc.Commit(ctx); err != nil {
		return domain.Principal{}, err
	}
	return domain.Principal{
		ActorType: domain.ActorHuman, ActorID: userID, OrganizationID: domain.ID(organizationID),
		MembershipID: input.MembershipID,
	}.WithGrants(principalGrants), nil
}

func scanInvitation(row rowScanner, organizationID domain.ID) (domain.Invitation, error) {
	var invitation domain.Invitation
	var id, invitedBy, status, accepted string
	var grants []byte
	err := row.Scan(&id, &invitation.Email, &grants, &invitedBy, &status,
		&invitation.ExpiresAt, &accepted, &invitation.CreatedAt, &invitation.UpdatedAt)
	if err != nil {
		return domain.Invitation{}, err
	}
	invitation.ID, invitation.OrganizationID = domain.ID(id), organizationID
	invitation.InvitedByMembershipID, invitation.AcceptedMembershipID = domain.ID(invitedBy), domain.ID(accepted)
	invitation.Status = domain.InvitationStatus(status)
	if invitation.Grants, err = decodeGrants(grants); err != nil {
		return domain.Invitation{}, err
	}
	return invitation, nil
}

// decodeGrants reads the invitation's stored grant list; a row always
// yields a list, never nil, so the wire shape is stable.
func decodeGrants(raw []byte) ([]domain.AssignmentSpec, error) {
	grants := make([]domain.AssignmentSpec, 0)
	if len(raw) == 0 {
		return grants, nil
	}
	if err := json.Unmarshal(raw, &grants); err != nil {
		return nil, fmt.Errorf("invitation grants are unreadable: %w", err)
	}
	return grants, nil
}

func (s *Store) insertInvitationAudit(ctx context.Context, tx execer, organizationID domain.ID, eventType string, actorMembershipID, invitationID domain.ID, data map[string]any, now time.Time) error {
	payload, _ := json.Marshal(data)
	return s.appendAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: organizationID, EventType: eventType,
		ActorType: domain.ActorHuman, ActorID: actorMembershipID,
		SubjectType: "invitation", SubjectID: invitationID,
		Data: payload, OccurredAt: now,
	})
}

func (s *Store) insertMembershipAudit(ctx context.Context, tx execer, organizationID domain.ID, eventType string, actorID, membershipID domain.ID, data map[string]any, now time.Time) error {
	payload, _ := json.Marshal(data)
	return s.appendAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: organizationID, EventType: eventType,
		ActorType: domain.ActorHuman, ActorID: actorID,
		SubjectType: "membership", SubjectID: membershipID,
		Data: payload, OccurredAt: now,
	})
}

func (s *Store) appendInvitationAudit(ctx context.Context, organizationID domain.ID, eventType string, actorMembershipID, invitationID domain.ID, data map[string]any, now time.Time) error {
	return s.insertInvitationAudit(ctx, s.pool, organizationID, eventType, actorMembershipID, invitationID, data, now)
}
