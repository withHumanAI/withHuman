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

// CreateSessionForUser mints a session for a person an edition's own
// login resolved (the hosted enterprise SSO completion, browser or
// native), labeled by the authenticator that earned it. The membership
// must be active: the edition resolved the person, core still decides
// whether they may enter.
func (s *Service) CreateSessionForUser(ctx context.Context, userID, organizationID domain.ID, auth domain.AuthContext, client application.SessionClient) (AuthenticationResult, error) {
	membership, err := s.identities.FindMembershipForLogin(ctx, application.FindMembershipForLoginInput{UserID: userID, OrganizationID: organizationID})
	if errors.Is(err, domain.ErrNotFound) {
		return AuthenticationResult{}, domain.ErrUnauthenticated
	}
	if err != nil {
		return AuthenticationResult{}, err
	}
	if membership.Status != domain.MembershipActive {
		return AuthenticationResult{}, domain.ErrMembershipInactive
	}
	if client.Kind == "" {
		client.Kind = application.ClientWeb
	}
	return s.createSessionFor(ctx, userID, organizationID, auth, client)
}

// ResolveAssertionInOrganization is the identity step of an edition's
// external login for one organization: a registered identity resolves
// to its user, whose membership there must be active; an unknown
// identity whose email the provider verified is linked to the existing
// member with that email (an organization that enables external login
// after its members exist is the normal rollout); anyone else is
// unknown, reported as found=false. A known identity without a
// membership here is reported with its user id and found=false, so the
// caller can admit it (an invitation, a verified domain) without
// creating a second account. Linking only, never admission: no
// membership is created here.
func (s *Service) ResolveAssertionInOrganization(ctx context.Context, organizationID domain.ID, assertion Assertion) (domain.ID, bool, error) {
	now := s.clock.Now()
	user, err := s.identities.ResolveExternalIdentity(ctx, application.ResolveExternalIdentityInput{
		ProviderKey: assertion.ProviderKey, Issuer: assertion.Issuer, Subject: assertion.Subject,
		ObservedEmail: assertion.Email, Now: now,
	})
	switch {
	case err == nil:
		membership, err := s.identities.FindMembershipForLogin(ctx, application.FindMembershipForLoginInput{UserID: user.ID, OrganizationID: organizationID})
		if err == nil {
			if membership.Status != domain.MembershipActive {
				return "", false, domain.ErrMembershipInactive
			}
			return user.ID, true, nil
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return "", false, err
		}
		return user.ID, false, nil
	case !errors.Is(err, domain.ErrNotFound):
		return "", false, err
	}
	if !assertion.EmailVerified {
		return "", false, nil
	}
	membership, err := s.identities.LinkVerifiedMemberIdentity(ctx, application.LinkVerifiedMemberIdentityInput{
		OrganizationID: organizationID, Email: assertion.Email,
		ProviderKey: assertion.ProviderKey, Issuer: assertion.Issuer, Subject: assertion.Subject, Now: now,
	})
	switch {
	case err == nil:
		if membership.Status != domain.MembershipActive {
			return "", false, domain.ErrMembershipInactive
		}
		return membership.UserID, true, nil
	case errors.Is(err, domain.ErrNotFound):
		return "", false, nil
	}
	return "", false, err
}

// AdmitExternalMemberInput is an edition's unattended admission: a person
// its identity provider asserted joins with a default role nobody is
// present to confirm (the hosted edition's just-in-time admission under a
// verified domain). The role is checked again at the moment it takes
// effect and may never carry a dangerous permission.
type AdmitExternalMemberInput struct {
	OrganizationID  domain.ID
	Assertion       Assertion
	RoleID          domain.ID
	AdmissionSource domain.AdmissionSource
	// ExistingUserID names the account the assertion already resolved to,
	// when any; otherwise the person's account is found by email or
	// created.
	ExistingUserID domain.ID
}

// AdmitExternalMember creates or links the person, binds the asserted
// identity and mints an active membership with the default role, all in
// one transaction.
func (s *Service) AdmitExternalMember(ctx context.Context, input AdmitExternalMemberInput) (domain.Principal, error) {
	if s.roles == nil {
		return domain.Principal{}, ErrEnterpriseNotConfigured
	}
	role, err := s.roles.GetRole(ctx, application.GetRoleInput{OrganizationID: input.OrganizationID, RoleID: input.RoleID})
	if err != nil {
		return domain.Principal{}, fmt.Errorf("resolve unattended default role: %w", err)
	}
	if role.Dangerous() {
		return domain.Principal{}, fmt.Errorf("roles with dangerous permissions cannot be granted automatically: %w", domain.ErrForbidden)
	}
	admit := application.AdmitExternalMemberInput{
		OrganizationID: input.OrganizationID, MembershipID: domain.ID(uuid.NewString()),
		Email: input.Assertion.Email, RoleID: role.ID, AdmissionSource: input.AdmissionSource,
		ProviderKey: input.Assertion.ProviderKey, Issuer: input.Assertion.Issuer, Subject: input.Assertion.Subject,
		Now: s.clock.Now(),
	}
	if input.ExistingUserID != "" {
		admit.UserID = input.ExistingUserID
	} else {
		admit.NewUserID = domain.ID(uuid.NewString())
		displayName := strings.TrimSpace(input.Assertion.DisplayName)
		if displayName == "" {
			displayName = input.Assertion.Email
		}
		admit.DisplayName = displayName
	}
	return s.identities.AdmitExternalMember(ctx, admit)
}
