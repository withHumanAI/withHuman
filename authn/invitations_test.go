package authn

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/entitlements"
)

type membershipRepositoryStub struct {
	preview      application.InvitationPreview
	previewErr   error
	accepted     *application.AcceptInvitationInput
	acceptErr    error
	pending      domain.Invitation
	pendingErr   error
	acceptResult domain.Principal
}

func (s *membershipRepositoryStub) CreateInvitation(context.Context, application.CreateInvitationInput) (domain.Invitation, error) {
	return domain.Invitation{}, nil
}

func (s *membershipRepositoryStub) ListInvitations(context.Context, application.ListInvitationsInput) ([]domain.Invitation, error) {
	return nil, nil
}

func (s *membershipRepositoryStub) RotateInvitationToken(context.Context, application.RotateInvitationTokenInput) (domain.Invitation, error) {
	return domain.Invitation{}, nil
}

func (s *membershipRepositoryStub) GetInvitation(context.Context, application.GetInvitationInput) (domain.Invitation, error) {
	return domain.Invitation{}, domain.ErrNotFound
}

func (s *membershipRepositoryStub) RevokeInvitation(context.Context, application.RevokeInvitationInput) error {
	return nil
}

func (s *membershipRepositoryStub) GetInvitationByToken(context.Context, application.GetInvitationByTokenInput) (application.InvitationPreview, error) {
	if s.previewErr != nil {
		return application.InvitationPreview{}, s.previewErr
	}
	return s.preview, nil
}

func (s *membershipRepositoryStub) FindPendingInvitationByEmail(context.Context, application.FindPendingInvitationByEmailInput) (domain.Invitation, error) {
	if s.pendingErr != nil {
		return domain.Invitation{}, s.pendingErr
	}
	return s.pending, nil
}

func (s *membershipRepositoryStub) AcceptInvitation(_ context.Context, input application.AcceptInvitationInput) (domain.Principal, error) {
	if s.acceptErr != nil {
		return domain.Principal{}, s.acceptErr
	}
	s.accepted = &input
	return s.acceptResult, nil
}

func (s *membershipRepositoryStub) ListMembers(context.Context, application.ListMembersInput) ([]application.MemberRecord, error) {
	return nil, nil
}

func (s *membershipRepositoryStub) SetMembershipStatus(context.Context, application.SetMembershipStatusInput) (domain.Membership, error) {
	return domain.Membership{}, nil
}

func invitationTestService(identities *identityRepositoryStub, memberships *membershipRepositoryStub) *Service {
	now := time.Unix(10_000, 0).UTC()
	return NewService(identities, nil, memberships, nil, entitlements.NewUnlimited(), passwordHasherStub{}, serviceClock{now}, ServiceConfig{
		PublicOrigin: "http://localhost:8080", SessionTTL: 24 * time.Hour,
	})
}

func pendingPreview(email string, userExists bool) application.InvitationPreview {
	return application.InvitationPreview{
		Invitation: domain.Invitation{
			ID: "invitation-1", OrganizationID: "org-1", Email: email,
			Grants: []domain.AssignmentSpec{{RoleID: domain.RoleReviewerID, ScopeKind: domain.ScopeOrganization}}, Status: domain.InvitationPending,
		},
		OrganizationName: "Acme", OrganizationSlug: "acme",
		UserExists: userExists,
	}
}

const testToken = "whi_1b4e28ba-2fa1-11d2-883f-0016d3cca427_secret"

func TestAcceptInvitationNewUserSetsPassword(t *testing.T) {
	identities := &identityRepositoryStub{}
	memberships := &membershipRepositoryStub{
		preview:      pendingPreview("new@example.com", false),
		acceptResult: domain.Principal{ActorType: domain.ActorHuman, ActorID: "user-9", OrganizationID: "org-1"},
	}
	service := invitationTestService(identities, memberships)
	result, err := service.AcceptInvitation(context.Background(), AcceptInvitationInput{
		Token: testToken, DisplayName: "New Person", Password: "a-long-password!",
	})
	if err != nil {
		t.Fatal(err)
	}
	if memberships.accepted == nil || memberships.accepted.NewUserID == "" || memberships.accepted.PasswordHash == "" {
		t.Fatalf("new user fields missing: %#v", memberships.accepted)
	}
	if memberships.accepted.ProviderKey != "local" || memberships.accepted.Subject != string(memberships.accepted.NewUserID) {
		t.Fatalf("identity link is wrong: %#v", memberships.accepted)
	}
	if result.Token == "" || identities.session.Auth.Method != "invite_local" {
		t.Fatalf("no session was minted: %#v", identities.session)
	}
}

func TestAcceptInvitationRejectsShortPassword(t *testing.T) {
	memberships := &membershipRepositoryStub{preview: pendingPreview("new@example.com", false)}
	service := invitationTestService(&identityRepositoryStub{}, memberships)
	if _, err := service.AcceptInvitation(context.Background(), AcceptInvitationInput{
		Token: testToken, DisplayName: "New Person", Password: "short",
	}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("short password error = %v", err)
	}
}

type refusingInvitationGuard struct{}

func (refusingInvitationGuard) CheckInvitationEmail(context.Context, string) error {
	return fmt.Errorf("use a permanent email address: %w", domain.ErrInvalid)
}

// An invitation that went out before the edition refused its address
// creates no account. A person who already has one still accepts.
func TestAcceptInvitationAsksTheGuardForANewAccount(t *testing.T) {
	memberships := &membershipRepositoryStub{preview: pendingPreview("new@example.com", false)}
	service := invitationTestService(&identityRepositoryStub{}, memberships)
	service.RegisterInvitationGuard(refusingInvitationGuard{})
	if _, err := service.AcceptInvitation(context.Background(), AcceptInvitationInput{
		Token: testToken, DisplayName: "New Person", Password: "a-long-password!",
	}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("refused address error = %v", err)
	}
	if memberships.accepted != nil {
		t.Fatal("a refused address gained an account")
	}

	memberships = &membershipRepositoryStub{
		preview:      pendingPreview("member@example.com", true),
		acceptResult: domain.Principal{ActorType: domain.ActorHuman, ActorID: "user-1", OrganizationID: "org-1"},
	}
	service = invitationTestService(&identityRepositoryStub{user: &domain.User{ID: "user-1", PrimaryEmail: "member@example.com"}}, memberships)
	service.RegisterInvitationGuard(refusingInvitationGuard{})
	if _, err := service.AcceptInvitation(context.Background(), AcceptInvitationInput{
		Token: testToken, Session: &domain.Principal{ActorType: domain.ActorHuman, ActorID: "user-1"},
	}); err != nil {
		t.Fatalf("an existing account was refused: %v", err)
	}
}

func TestAcceptInvitationRefusesLocalPathWhenSSORequired(t *testing.T) {
	memberships := &membershipRepositoryStub{preview: pendingPreview("new@example.com", false)}
	service := invitationTestService(&identityRepositoryStub{}, memberships)
	// The edition's policy says the organization signs in externally: no
	// password user may be created through accept.
	service.RegisterLoginPolicy(externalLoginEverywhere())
	if _, err := service.AcceptInvitation(context.Background(), AcceptInvitationInput{
		Token: testToken, DisplayName: "New Person", Password: "a-long-password!",
	}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("sso_required acceptance error = %v", err)
	}
	if memberships.accepted != nil {
		t.Fatal("an sso_required organization gained a password user")
	}
}

func TestAcceptInvitationSessionMismatchRefused(t *testing.T) {
	identities := &identityRepositoryStub{}
	memberships := &membershipRepositoryStub{preview: pendingPreview("invited@example.com", true)}
	service := invitationTestService(identities, memberships)
	session := domain.Principal{ActorType: domain.ActorHuman, ActorID: "user-2", OrganizationID: "org-2"}
	if _, err := service.AcceptInvitation(context.Background(), AcceptInvitationInput{
		Token: testToken, Session: &session,
	}); !errors.Is(err, domain.ErrEmailMismatch) {
		t.Fatalf("session email mismatch error = %v", err)
	}
}

func TestAssuranceFor(t *testing.T) {
	cases := []struct {
		assertion Assertion
		want      string
	}{
		{Assertion{Methods: []string{"password"}}, "single_factor"},
		{Assertion{Methods: []string{"password", "totp"}}, "strong"},
		{Assertion{Assurance: "strong"}, "strong"},
		{Assertion{Methods: []string{"sso"}}, "single_factor"},
		{Assertion{Methods: []string{"webauthn"}}, "strong"},
	}
	for _, testCase := range cases {
		if got := AssuranceFor(testCase.assertion); got != testCase.want {
			t.Fatalf("AssuranceFor(%#v) = %s, want %s", testCase.assertion, got, testCase.want)
		}
	}
}
