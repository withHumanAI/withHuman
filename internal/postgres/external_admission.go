package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
)

// AdmitExternalMember is an edition's unattended admission transaction,
// the counterpart of the invitation acceptance: create or link the user,
// bind the asserted identity, and mint an active membership with the
// default role the edition chose (the hosted just-in-time admission under
// a verified domain).
func (s *Store) AdmitExternalMember(ctx context.Context, input application.AdmitExternalMemberInput) (domain.Principal, error) {
	if err := input.Validate(); err != nil {
		return domain.Principal{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return domain.Principal{}, err
	}
	defer tx.Rollback(ctx)

	email := strings.ToLower(strings.TrimSpace(input.Email))
	userID := input.UserID
	if userID == "" {
		// The asserted person may already hold a global account under this
		// email (for example a password account in another organization).
		var existingID string
		err := tx.QueryRow(ctx, `SELECT id::text FROM users
            WHERE lower(primary_email) = lower($1) AND status = 'active'`, email).Scan(&existingID)
		switch {
		case err == nil:
			userID = domain.ID(existingID)
		case errors.Is(err, pgx.ErrNoRows):
			userID = input.NewUserID
			if _, err := tx.Exec(ctx, `INSERT INTO users (id, display_name, primary_email, status, created_at, updated_at)
                VALUES ($1, $2, $3, 'active', $4, $4)`, userID, input.DisplayName, email, input.Now); err != nil {
				return domain.Principal{}, err
			}
		default:
			return domain.Principal{}, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO external_identities
        (user_id, provider_key, issuer, subject, last_observed_email, email_verified, linked_at, last_authenticated_at)
        VALUES ($1, $2, $3, $4, $5, true, $6, $6)
        ON CONFLICT (provider_key, issuer, subject) DO UPDATE
        SET last_observed_email = EXCLUDED.last_observed_email,
            last_authenticated_at = EXCLUDED.last_authenticated_at`,
		userID, input.ProviderKey, input.Issuer, input.Subject, email, input.Now); err != nil {
		return domain.Principal{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO memberships
        (id, user_id, organization_id, status, admission_source, created_at, updated_at)
        VALUES ($1, $2, $3, 'active', $4, $5, $5)`,
		input.MembershipID, userID, input.OrganizationID, string(input.AdmissionSource), input.Now); err != nil {
		if isUniqueViolation(err) {
			return domain.Principal{}, fmt.Errorf("this person is already a member: %w", domain.ErrConflict)
		}
		return domain.Principal{}, err
	}
	// Nobody granted the role by hand: the domain's default did, so the
	// grant is the system's and records no granting membership.
	grants := []domain.AssignmentSpec{{RoleID: input.RoleID, ScopeKind: domain.ScopeOrganization}}
	if err := s.grantSpecs(ctx, tx, input.OrganizationID, assignmentWrite{
		Principal: domain.PrincipalMembership, PrincipalID: input.MembershipID,
		Source: domain.AssignmentManual, ActorType: domain.ActorSystem,
	}, grants, input.Now); err != nil {
		return domain.Principal{}, err
	}
	if err := s.insertMembershipAudit(ctx, tx, input.OrganizationID, "membership.created",
		userID, input.MembershipID,
		map[string]any{"grants": grants, "admission_source": input.AdmissionSource, "email": email}, input.Now); err != nil {
		return domain.Principal{}, err
	}
	principalGrants, err := loadGrants(ctx, tx, input.OrganizationID, domain.PrincipalMembership, input.MembershipID)
	if err != nil {
		return domain.Principal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Principal{}, err
	}
	return domain.Principal{
		ActorType: domain.ActorHuman, ActorID: userID, OrganizationID: input.OrganizationID,
		MembershipID: input.MembershipID,
	}.WithGrants(principalGrants), nil
}
