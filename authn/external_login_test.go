package authn

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/entitlements"
)

// The seams an edition's external login (the hosted enterprise SSO)
// composes: resolving an assertion to a member, admitting through an
// invitation or unattended with a default role, and minting the session.

// builtInRoles answers the built-in role rows, the way the hosted
// edition's JIT default role is resolved.
type builtInRoles struct{ application.RoleRepository }

func (builtInRoles) GetRole(_ context.Context, input application.GetRoleInput) (domain.RoleRow, error) {
	for _, def := range domain.BuiltInRoles() {
		if def.ID == input.RoleID {
			return domain.RoleRow{ID: def.ID, Key: def.Key, Name: def.Name, Permissions: def.Permissions,
				BindableScopeKinds: def.BindableScopeKinds, BuiltIn: true}, nil
		}
	}
	return domain.RoleRow{}, domain.ErrNotFound
}

func externalAssertion(verified bool) Assertion {
	return Assertion{ProviderKey: "polis", Issuer: "issuer", Subject: "subject-1", Email: "person@example.com", EmailVerified: verified, DisplayName: "Person"}
}

func TestResolveAssertionInOrganizationResolvesLinksOrReportsUnknown(t *testing.T) {
	ctx := context.Background()
	// A registered identity with an active membership resolves.
	identities := &identityRepositoryStub{
		externalUser: &domain.User{ID: "user-1", Status: domain.UserActive},
		membership:   domain.Membership{ID: "membership-1", UserID: "user-1", OrganizationID: "org-1", Status: domain.MembershipActive},
	}
	service := invitationTestService(identities, &membershipRepositoryStub{})
	userID, found, err := service.ResolveAssertionInOrganization(ctx, "org-1", externalAssertion(true))
	if err != nil || !found || userID != "user-1" {
		t.Fatalf("registered member = %s, %v, %v", userID, found, err)
	}
	// A registered identity whose membership here is inactive is refused.
	identities.membership.Status = domain.MembershipSuspended
	if _, _, err := service.ResolveAssertionInOrganization(ctx, "org-1", externalAssertion(true)); !errors.Is(err, domain.ErrMembershipInactive) {
		t.Fatalf("suspended member = %v, want inactive", err)
	}
	// A registered identity without a membership here is known but not
	// found, so the caller can admit it without a second account.
	identities.membership = domain.Membership{}
	userID, found, err = service.ResolveAssertionInOrganization(ctx, "org-1", externalAssertion(true))
	if err != nil || found || userID != "user-1" {
		t.Fatalf("known account without a membership = %s, %v, %v", userID, found, err)
	}

	// An unknown identity with a verified email links to the existing
	// member with that email.
	identities = &identityRepositoryStub{
		memberByEmail: domain.Membership{ID: "membership-2", UserID: "user-2", OrganizationID: "org-1", Status: domain.MembershipActive},
	}
	service = invitationTestService(identities, &membershipRepositoryStub{})
	userID, found, err = service.ResolveAssertionInOrganization(ctx, "org-1", externalAssertion(true))
	if err != nil || !found || userID != "user-2" || len(identities.linked) != 1 || identities.linked[0].Email != "person@example.com" {
		t.Fatalf("linked member = %s, %v, %v, links %#v", userID, found, err, identities.linked)
	}
	// An unverified email never links.
	identities.linked = nil
	userID, found, err = service.ResolveAssertionInOrganization(ctx, "org-1", externalAssertion(false))
	if err != nil || found || userID != "" || len(identities.linked) != 0 {
		t.Fatalf("unverified email = %s, %v, %v, links %#v", userID, found, err, identities.linked)
	}
	// A verified email nobody holds is simply unknown.
	identities.memberByEmail = domain.Membership{}
	if userID, found, err := service.ResolveAssertionInOrganization(ctx, "org-1", externalAssertion(true)); err != nil || found || userID != "" {
		t.Fatalf("stranger = %s, %v, %v", userID, found, err)
	}
}

func TestAcceptInvitationByAssertionAdmitsThroughThePendingInvitation(t *testing.T) {
	ctx := context.Background()
	memberships := &membershipRepositoryStub{
		pending:      domain.Invitation{ID: "invitation-1", OrganizationID: "org-1", Email: "person@example.com", Status: domain.InvitationPending},
		acceptResult: domain.Principal{ActorType: domain.ActorHuman, ActorID: "user-9", OrganizationID: "org-1", MembershipID: "membership-9"},
	}
	service := invitationTestService(&identityRepositoryStub{}, memberships)
	principal, err := service.AcceptInvitationByAssertion(ctx, "org-1", externalAssertion(true), "")
	if err != nil || principal.ActorID != "user-9" {
		t.Fatalf("accept = %#v, %v", principal, err)
	}
	accepted := memberships.accepted
	if accepted == nil || accepted.InvitationID != "invitation-1" || accepted.AssertedEmail != "person@example.com" ||
		accepted.ProviderKey != "polis" || accepted.Subject != "subject-1" || accepted.NewUserID == "" || accepted.DisplayName != "Person" || accepted.LinkExistingUser {
		t.Fatalf("accepted input = %#v", accepted)
	}
	// An account the assertion already resolved to joins as itself.
	if _, err := service.AcceptInvitationByAssertion(ctx, "org-1", externalAssertion(true), "user-4"); err != nil || memberships.accepted.UserID != "user-4" || memberships.accepted.NewUserID != "" {
		t.Fatalf("existing account accept = %#v, %v", memberships.accepted, err)
	}
	// The edition's policy says the organization's identity provider
	// vouches for the address, so an existing account under it may be
	// attached.
	service.RegisterLoginPolicy(loginPolicyStub{vouches: map[domain.ID]bool{"org-1": true}})
	if _, err := service.AcceptInvitationByAssertion(ctx, "org-1", externalAssertion(true), ""); err != nil || !memberships.accepted.LinkExistingUser {
		t.Fatalf("vouched accept = %#v, %v", memberships.accepted, err)
	}
	if _, err := service.AcceptInvitationByAssertion(ctx, "org-1", externalAssertion(false), ""); err != nil || memberships.accepted.LinkExistingUser {
		t.Fatalf("unverified accept attached an account: %#v, %v", memberships.accepted, err)
	}
	// No pending invitation: the caller decides what comes next.
	memberships.pendingErr = domain.ErrNotFound
	_, err = service.AcceptInvitationByAssertion(ctx, "org-1", externalAssertion(true), "")
	if !errors.Is(err, ErrInvitationRequired) || !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("no invitation = %v", err)
	}
}

func TestAdmitExternalMemberChecksTheDefaultRoleAtAdmission(t *testing.T) {
	ctx := context.Background()
	identities := &identityRepositoryStub{}
	service := invitationTestService(identities, &membershipRepositoryStub{})
	input := AdmitExternalMemberInput{OrganizationID: "org-1", Assertion: externalAssertion(true), RoleID: domain.RoleReviewerID, AdmissionSource: domain.AdmissionSSOJIT}
	if _, err := service.AdmitExternalMember(ctx, input); !errors.Is(err, ErrEnterpriseNotConfigured) {
		t.Fatalf("without a role repository = %v", err)
	}
	service.RegisterRoles(builtInRoles{})
	principal, err := service.AdmitExternalMember(ctx, input)
	if err != nil || principal.OrganizationID != "org-1" || len(identities.admitted) != 1 {
		t.Fatalf("admit = %#v, %v, admitted %#v", principal, err, identities.admitted)
	}
	admitted := identities.admitted[0]
	if admitted.RoleID != domain.RoleReviewerID || admitted.AdmissionSource != domain.AdmissionSSOJIT || admitted.NewUserID == "" || admitted.UserID != "" ||
		admitted.DisplayName != "Person" || admitted.Email != "person@example.com" || admitted.ProviderKey != "polis" || admitted.MembershipID == "" {
		t.Fatalf("admitted input = %#v", admitted)
	}
	// A blank display name falls back to the email; a known account joins as itself.
	input.Assertion.DisplayName = " "
	input.ExistingUserID = "user-4"
	if _, err := service.AdmitExternalMember(ctx, input); err != nil {
		t.Fatal(err)
	}
	if admitted := identities.admitted[1]; admitted.UserID != "user-4" || admitted.NewUserID != "" || admitted.DisplayName != "" {
		t.Fatalf("existing account admission = %#v", admitted)
	}
	// A default role that carries a dangerous permission is refused when
	// it would take effect: nobody is present to confirm it.
	input.RoleID = domain.RoleAdminID
	if _, err := service.AdmitExternalMember(ctx, input); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("dangerous default role = %v, want forbidden", err)
	}
	input.RoleID = "no-such-role"
	if _, err := service.AdmitExternalMember(ctx, input); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown role = %v, want not found", err)
	}
	if len(identities.admitted) != 2 {
		t.Fatalf("a refused admission reached the store: %d", len(identities.admitted))
	}
}

func TestCreateSessionForUserNeedsAnActiveMembership(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(10_000, 0).UTC()
	identities := &identityRepositoryStub{}
	service := invitationTestService(identities, &membershipRepositoryStub{})
	auth := domain.AuthContext{Method: "sso", Assurance: "strong", AuthenticatedAt: now}
	if _, err := service.CreateSessionForUser(ctx, "user-1", "org-1", auth, application.SessionClient{}); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("no membership = %v, want unauthenticated", err)
	}
	identities.membership = domain.Membership{ID: "membership-1", UserID: "user-1", OrganizationID: "org-1", Status: domain.MembershipSuspended}
	if _, err := service.CreateSessionForUser(ctx, "user-1", "org-1", auth, application.SessionClient{}); !errors.Is(err, domain.ErrMembershipInactive) {
		t.Fatalf("suspended membership = %v, want inactive", err)
	}
	identities.membership.Status = domain.MembershipActive
	result, err := service.CreateSessionForUser(ctx, "user-1", "org-1", auth, application.SessionClient{})
	if err != nil || result.Token == "" || result.Principal.ActorID != "user-1" || identities.sessions != 1 {
		t.Fatalf("session = %#v, %v", result, err)
	}
	if identities.session.Client.Kind != application.ClientWeb || identities.session.Auth.Method != "sso" || identities.session.Auth.Assurance != "strong" {
		t.Fatalf("session record = %#v", identities.session)
	}
	if _, err := service.CreateSessionForUser(ctx, "user-1", "org-1", auth, application.SessionClient{Kind: "ios"}); err != nil || identities.session.Client.Kind != "ios" {
		t.Fatalf("native session = %#v, %v", identities.session.Client, err)
	}
}

func TestNativeClientsGetTheNativeSessionLifetimeUnderThePolicyCap(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(10_000, 0).UTC()
	identities := &identityRepositoryStub{
		membership: domain.Membership{ID: "membership-1", UserID: "user-1", OrganizationID: "org-1", Status: domain.MembershipActive},
	}
	service := NewService(identities, nil, &membershipRepositoryStub{}, nil, entitlements.NewUnlimited(), passwordHasherStub{}, serviceClock{now}, ServiceConfig{
		PublicOrigin: "http://localhost:8080", SessionTTL: 7 * 24 * time.Hour, NativeSessionTTL: 30 * 24 * time.Hour,
	})
	auth := domain.AuthContext{Method: "sso", Assurance: "strong", AuthenticatedAt: now}
	for _, tc := range []struct {
		client application.SessionClient
		cap    time.Duration
		want   time.Duration
	}{
		{application.SessionClient{}, 0, 7 * 24 * time.Hour},
		{application.SessionClient{Kind: "ios"}, 0, 30 * 24 * time.Hour},
		{application.SessionClient{Kind: "android"}, 0, 30 * 24 * time.Hour},
		{application.SessionClient{Kind: "desktop"}, 0, 30 * 24 * time.Hour},
		{application.SessionClient{Kind: "ios"}, 24 * time.Hour, 24 * time.Hour},
	} {
		identities.policy.SessionMaxAge = tc.cap
		result, err := service.CreateSessionForUser(ctx, "user-1", "org-1", auth, tc.client)
		if err != nil || !result.ExpiresAt.Equal(now.Add(tc.want)) {
			t.Fatalf("%q under cap %s expires %s, %v; want %s", tc.client.Kind, tc.cap, result.ExpiresAt, err, now.Add(tc.want))
		}
	}
}

func TestLoginLocalRefusesAnUnregisteredClientKind(t *testing.T) {
	service := invitationTestService(&identityRepositoryStub{}, &membershipRepositoryStub{})
	_, err := service.LoginLocal(context.Background(), LocalLoginInput{
		Email: "person@example.com", Password: "secret", Client: application.SessionClient{Kind: "watch"},
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unregistered client = %v, want invalid", err)
	}
}
