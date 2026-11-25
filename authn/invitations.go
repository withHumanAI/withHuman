package authn

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
)

// InvitationPreviewResult adds what only the authn layer knows to the
// stored preview: whether the organization signs in through SSO, so the
// accept page routes there instead of offering a password.
type InvitationPreviewResult struct {
	application.InvitationPreview
	SSORequired bool
}

// PreviewInvitation resolves a raw invitation token to what the public
// accept page may show before the invitee authenticates.
func (s *Service) PreviewInvitation(ctx context.Context, token string) (InvitationPreviewResult, error) {
	invitationID, secret, err := ParseOpaqueToken(token, "whi")
	if err != nil {
		return InvitationPreviewResult{}, domain.ErrNotFound
	}
	preview, err := s.memberships.GetInvitationByToken(ctx, application.GetInvitationByTokenInput{
		InvitationID: domain.ID(invitationID),
		TokenHash:    HashToken(secret),
		Now:          s.clock.Now(),
	})
	if err != nil {
		return InvitationPreviewResult{}, err
	}
	ssoRequired, err := s.requiresExternalLogin(ctx, preview.Invitation.OrganizationID)
	if err != nil {
		return InvitationPreviewResult{}, err
	}
	return InvitationPreviewResult{InvitationPreview: preview, SSORequired: ssoRequired}, nil
}

// AcceptInvitationInput carries the three redemption shapes: a new user
// setting a password, an existing user proving their password, or an
// already signed-in user (Session set) accepting with one click.
type AcceptInvitationInput struct {
	Token       string
	DisplayName string
	Password    string
	Session     *domain.Principal
}

// RegisterInvitationGuard wires the edition's say over which addresses may
// be invited. Accepting checks it again for a person without an account,
// so an invitation sent before a rule does not outlive it. A person who
// has an account, or whose organization's identity provider admits them,
// is not checked.
func (s *Service) RegisterInvitationGuard(guard application.InvitationGuard) { s.inviteGuard = guard }

func (s *Service) AcceptInvitation(ctx context.Context, input AcceptInvitationInput) (AuthenticationResult, error) {
	invitationID, secret, err := ParseOpaqueToken(input.Token, "whi")
	if err != nil {
		return AuthenticationResult{}, domain.ErrNotFound
	}
	tokenHash := HashToken(secret)
	now := s.clock.Now()
	preview, err := s.memberships.GetInvitationByToken(ctx, application.GetInvitationByTokenInput{
		InvitationID: domain.ID(invitationID), TokenHash: tokenHash, Now: now,
	})
	if err != nil {
		return AuthenticationResult{}, err
	}
	invitation := preview.Invitation
	// Structural SSO policy: an organization with an enterprise connection
	// never gains a password-based member through the local accept paths.
	ssoRequired, err := s.requiresExternalLogin(ctx, invitation.OrganizationID)
	if err != nil {
		return AuthenticationResult{}, err
	}

	quota, err := s.seatQuota(ctx, invitation.OrganizationID)
	if err != nil {
		return AuthenticationResult{}, err
	}
	accept := application.AcceptInvitationInput{
		InvitationID: invitation.ID,
		TokenHash:    tokenHash,
		MembershipID: domain.ID(uuid.NewString()),
		SeatQuota:    quota,
		Now:          now,
	}

	switch {
	case input.Session != nil:
		// One-click acceptance by a signed-in user whose email matches.
		user, err := s.identities.GetUser(ctx, application.GetUserInput{UserID: input.Session.ActorID})
		if err != nil {
			return AuthenticationResult{}, err
		}
		if !strings.EqualFold(strings.TrimSpace(user.PrimaryEmail), strings.TrimSpace(invitation.Email)) {
			return AuthenticationResult{}, fmt.Errorf("%w: %w", domain.ErrEmailMismatch, domain.ErrForbidden)
		}
		accept.UserID = user.ID
		accept.AssertedEmail = user.PrimaryEmail
	case preview.UserExists:
		// Existing local user proves their password. This check is
		// redemption scoped: no membership in the target organization is
		// required yet.
		if ssoRequired {
			return AuthenticationResult{}, fmt.Errorf("%w: %w", ErrSSORequired, domain.ErrForbidden)
		}
		assertion, err := s.local.Complete(ctx, map[string]string{"email": invitation.Email, "password": input.Password})
		if err != nil {
			return AuthenticationResult{}, err
		}
		accept.UserID = domain.ID(assertion.Subject)
		accept.AssertedEmail = assertion.Email
		accept.ProviderKey = assertion.ProviderKey
		accept.Issuer = assertion.Issuer
		accept.Subject = assertion.Subject
	default:
		// New user sets a password. This is the only signup path in the
		// open edition.
		if ssoRequired {
			return AuthenticationResult{}, fmt.Errorf("%w: %w", ErrSSORequired, domain.ErrForbidden)
		}
		if s.inviteGuard != nil {
			if err := s.inviteGuard.CheckInvitationEmail(ctx, invitation.Email); err != nil {
				return AuthenticationResult{}, err
			}
		}
		displayName := strings.TrimSpace(input.DisplayName)
		if displayName == "" {
			return AuthenticationResult{}, fmt.Errorf("display name is required: %w", domain.ErrInvalid)
		}
		if err := validateNewPassword(input.Password); err != nil {
			return AuthenticationResult{}, err
		}
		passwordHash, err := s.passwords.Hash(input.Password)
		if err != nil {
			return AuthenticationResult{}, fmt.Errorf("hash password: %w", err)
		}
		newUserID := domain.ID(uuid.NewString())
		accept.NewUserID = newUserID
		accept.DisplayName = displayName
		accept.PasswordHash = passwordHash
		accept.AssertedEmail = invitation.Email
		accept.ProviderKey = LocalProviderKey
		accept.Issuer = LocalIssuer
		accept.Subject = string(newUserID)
	}

	principal, err := s.memberships.AcceptInvitation(ctx, accept)
	if err != nil {
		return AuthenticationResult{}, err
	}
	method := "invite_local"
	if input.Session != nil {
		method = "invite_session"
	}
	return s.createSession(ctx, principal.ActorID, principal.OrganizationID, domain.AuthContext{
		Method: method, Assurance: "single_factor", AuthenticatedAt: now,
	})
}

// acceptInvitationForAssertion is the SSO admission path: a provider
// assertion with a verified email that matches a pending invitation admits
// the user without a token, because the invitation was addressed to the
// email the enterprise IdP just asserted.
func (s *Service) acceptInvitationForAssertion(ctx context.Context, invitation domain.Invitation, assertion Assertion, existingUserID domain.ID) (domain.Principal, error) {
	quota, err := s.seatQuota(ctx, invitation.OrganizationID)
	if err != nil {
		return domain.Principal{}, err
	}
	accept := application.AcceptInvitationInput{
		InvitationID:  invitation.ID,
		AssertedEmail: assertion.Email,
		MembershipID:  domain.ID(uuid.NewString()),
		ProviderKey:   assertion.ProviderKey,
		Issuer:        assertion.Issuer,
		Subject:       assertion.Subject,
		SeatQuota:     quota,
		Now:           s.clock.Now(),
	}
	if existingUserID != "" {
		accept.UserID = existingUserID
	} else {
		accept.NewUserID = domain.ID(uuid.NewString())
		displayName := strings.TrimSpace(assertion.DisplayName)
		if displayName == "" {
			displayName = assertion.Email
		}
		accept.DisplayName = displayName
		// An organization's IdP vouches for the addresses under domains
		// that organization proved it owns; for any other address the
		// assertion says nothing about who holds the withHuman account
		// with that email, so an existing account is never attached.
		verified, err := s.assertionDomainVerified(ctx, invitation.OrganizationID, assertion)
		if err != nil {
			return domain.Principal{}, err
		}
		accept.LinkExistingUser = verified
	}
	return s.memberships.AcceptInvitation(ctx, accept)
}

// assertionDomainVerified reports whether the organization's identity
// provider vouches for the asserted email: the edition's policy answers
// (the hosted edition's verified domains), and without one it never does.
func (s *Service) assertionDomainVerified(ctx context.Context, organizationID domain.ID, assertion Assertion) (bool, error) {
	if s.loginPolicy == nil || !assertion.EmailVerified {
		return false, nil
	}
	return s.loginPolicy.VouchesForEmail(ctx, organizationID, assertion.Email)
}

// AcceptInvitationByAssertion admits a person an edition's external login
// asserted through the pending invitation addressed to the asserted
// email, which the organization's identity provider verified by
// asserting it. ErrInvitationRequired says no invitation is pending.
func (s *Service) AcceptInvitationByAssertion(ctx context.Context, organizationID domain.ID, assertion Assertion, existingUserID domain.ID) (domain.Principal, error) {
	invitation, err := s.memberships.FindPendingInvitationByEmail(ctx, application.FindPendingInvitationByEmailInput{
		OrganizationID: organizationID, Email: assertion.Email, Now: s.clock.Now(),
	})
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Principal{}, fmt.Errorf("%w: %w", ErrInvitationRequired, domain.ErrForbidden)
	}
	if err != nil {
		return domain.Principal{}, err
	}
	return s.acceptInvitationForAssertion(ctx, invitation, assertion, existingUserID)
}

// IsInvitationRequired reports whether the error is the SSO login outcome
// where the asserted identity has no membership and no pending invitation.
var ErrInvitationRequired = errors.New("an invitation is required to join this organization")

// seatQuota resolves the organization's seat limit for the admission
// transaction to enforce.
func (s *Service) seatQuota(ctx context.Context, organizationID domain.ID) (application.AllocationQuota, error) {
	limit, err := s.entitlements.Limit(ctx, application.LimitInput{OrganizationID: organizationID, Dimension: application.DimensionSeats})
	if err != nil {
		return application.AllocationQuota{}, err
	}
	return application.ResolvedQuota(limit), nil
}
