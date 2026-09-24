package authn

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/entitlements"
	"github.com/withHumanAI/withHuman/tenants"
)

type identityRepositoryStub struct {
	hasOwner           bool
	credential         application.LocalCredentialRecord
	membership         domain.Membership
	memberByEmail      domain.Membership
	linked             []application.LinkVerifiedMemberIdentityInput
	policy             domain.OrganizationPolicy
	organization       domain.Organization
	session            application.CreateSessionInput
	sessions           int
	credentialErr      error
	loginOrganizations application.LoginOrganizations
	loginTickets       []application.CreateLoginTicketInput
	consumedTicket     *application.ConsumeLoginTicketInput
	ticketRecord       application.LoginTicketRecord
	ticketErr          error
	// externalUser is the account a known provider identity resolves to;
	// nil means the identity is unknown.
	externalUser *domain.User
	// userByEmail is the active account a verified email links to; nil
	// means no account exists for that email.
	userByEmail *domain.User
	userLinks   []application.LinkVerifiedUserIdentityInput
	admitted    []application.AdmitExternalMemberInput
	// user is what GetUser answers; nil means not found.
	user                *domain.User
	setCredentials      []application.SetLocalCredentialInput
	revokedUserSessions []application.RevokeUserSessionsInput
	passwordResets      []application.CreatePasswordResetInput
	consumedResets      map[domain.ID]bool
}

func (repository *identityRepositoryStub) HasOwner(context.Context) (bool, error) {
	return repository.hasOwner, nil
}

type activatorStub struct {
	activated tenants.ActivateOrganizationInput
}

func (stub *activatorStub) ActivateOrganization(_ context.Context, input tenants.ActivateOrganizationInput) (application.ActivationResult, error) {
	stub.activated = input
	return application.ActivationResult{
		Organization: domain.Organization{ID: "organization-1", Slug: input.OrganizationSlug, Name: input.OrganizationName, Status: domain.OrganizationActive},
		Principal: domain.Principal{
			ActorType: domain.ActorHuman, ActorID: "user-1",
			OrganizationID: "organization-1", MembershipID: "membership-1",
		},
	}, nil
}

func (repository *identityRepositoryStub) CreateSession(_ context.Context, input application.CreateSessionInput) (domain.Principal, error) {
	repository.session = input
	repository.sessions++
	return domain.Principal{
		ActorType:      domain.ActorHuman,
		ActorID:        input.UserID,
		OrganizationID: input.OrganizationID,
		MembershipID:   repository.membership.ID,
		Auth: domain.AuthContext{
			Method: input.Auth.Method, Assurance: input.Auth.Assurance,
			AuthenticatedAt: input.Auth.AuthenticatedAt, SessionID: "session-1",
		},
	}, nil
}

func (*identityRepositoryStub) ResolveSession(context.Context, application.ResolveSessionInput) (domain.Principal, error) {
	return domain.Principal{}, nil
}

func (*identityRepositoryStub) RevokeSession(context.Context, application.RevokeSessionInput) error {
	return nil
}

func (repository *identityRepositoryStub) GetLocalCredential(context.Context, application.GetLocalCredentialInput) (application.LocalCredentialRecord, error) {
	if repository.credentialErr != nil {
		return application.LocalCredentialRecord{}, repository.credentialErr
	}
	return repository.credential, nil
}

func (repository *identityRepositoryStub) SetLocalCredential(_ context.Context, input application.SetLocalCredentialInput) error {
	repository.setCredentials = append(repository.setCredentials, input)
	return nil
}

func (repository *identityRepositoryStub) RevokeUserSessions(_ context.Context, input application.RevokeUserSessionsInput) error {
	repository.revokedUserSessions = append(repository.revokedUserSessions, input)
	return nil
}

func (repository *identityRepositoryStub) CreatePasswordReset(_ context.Context, input application.CreatePasswordResetInput) error {
	repository.passwordResets = append(repository.passwordResets, input)
	return nil
}

func (repository *identityRepositoryStub) GetPasswordReset(_ context.Context, input application.GetPasswordResetInput) (application.PasswordResetRecord, error) {
	return repository.findPasswordReset(input.ResetID, input.SecretHash, input.Now, false)
}

func (repository *identityRepositoryStub) ConsumePasswordReset(_ context.Context, input application.ConsumePasswordResetInput) (application.PasswordResetRecord, error) {
	return repository.findPasswordReset(input.ResetID, input.SecretHash, input.Now, true)
}

func (repository *identityRepositoryStub) findPasswordReset(id domain.ID, hash []byte, now time.Time, consume bool) (application.PasswordResetRecord, error) {
	for index, reset := range repository.passwordResets {
		if reset.ResetID != id || !bytes.Equal(reset.SecretHash, hash) || !reset.ExpiresAt.After(now) {
			continue
		}
		if repository.consumedResets[reset.ResetID] {
			continue
		}
		if consume {
			if repository.consumedResets == nil {
				repository.consumedResets = map[domain.ID]bool{}
			}
			repository.consumedResets[reset.ResetID] = true
		}
		_ = index
		return application.PasswordResetRecord{UserID: reset.UserID, ExpiresAt: reset.ExpiresAt}, nil
	}
	return application.PasswordResetRecord{}, domain.ErrNotFound
}

func (repository *identityRepositoryStub) FindMembershipForLogin(context.Context, application.FindMembershipForLoginInput) (domain.Membership, error) {
	if repository.membership.ID == "" {
		return domain.Membership{}, domain.ErrNotFound
	}
	return repository.membership, nil
}

func (repository *identityRepositoryStub) ListLoginOrganizations(context.Context, application.ListLoginOrganizationsInput) (application.LoginOrganizations, error) {
	return repository.loginOrganizations, nil
}

func (repository *identityRepositoryStub) CreateLoginTicket(_ context.Context, input application.CreateLoginTicketInput) error {
	repository.loginTickets = append(repository.loginTickets, input)
	return nil
}

func (repository *identityRepositoryStub) ConsumeLoginTicket(_ context.Context, input application.ConsumeLoginTicketInput) (application.LoginTicketRecord, error) {
	repository.consumedTicket = &input
	if repository.ticketErr != nil {
		return application.LoginTicketRecord{}, repository.ticketErr
	}
	return repository.ticketRecord, nil
}

func (repository *identityRepositoryStub) AdmitExternalMember(_ context.Context, input application.AdmitExternalMemberInput) (domain.Principal, error) {
	repository.admitted = append(repository.admitted, input)
	return domain.Principal{ActorType: domain.ActorHuman, ActorID: input.UserID, OrganizationID: input.OrganizationID, MembershipID: input.MembershipID}, nil
}

func (repository *identityRepositoryStub) GetUser(context.Context, application.GetUserInput) (domain.User, error) {
	if repository.user != nil {
		return *repository.user, nil
	}
	return domain.User{}, nil
}

func (repository *identityRepositoryStub) GetMembership(context.Context, application.GetMembershipInput) (domain.Membership, error) {
	return repository.membership, nil
}

func (repository *identityRepositoryStub) GetOrganizationPolicy(context.Context, application.GetOrganizationPolicyInput) (domain.OrganizationPolicy, error) {
	return repository.policy, nil
}

func (repository *identityRepositoryStub) UpdateOrganizationPolicy(context.Context, application.UpdateOrganizationPolicyInput) (domain.OrganizationPolicy, error) {
	return repository.policy, nil
}

func (repository *identityRepositoryStub) GetOrganization(context.Context, application.GetOrganizationInput) (domain.Organization, error) {
	if repository.organization.ID == "" {
		return domain.Organization{}, domain.ErrNotFound
	}
	return repository.organization, nil
}

func (repository *identityRepositoryStub) FindOrganizationBySlug(context.Context, application.FindOrganizationBySlugInput) (domain.Organization, error) {
	if repository.organization.ID == "" {
		return domain.Organization{}, domain.ErrNotFound
	}
	return repository.organization, nil
}

func (repository *identityRepositoryStub) ResolveExternalIdentity(context.Context, application.ResolveExternalIdentityInput) (domain.User, error) {
	if repository.externalUser == nil {
		return domain.User{}, domain.ErrNotFound
	}
	return *repository.externalUser, nil
}

func (repository *identityRepositoryStub) LinkVerifiedUserIdentity(_ context.Context, input application.LinkVerifiedUserIdentityInput) (domain.User, error) {
	repository.userLinks = append(repository.userLinks, input)
	if repository.userByEmail == nil || !strings.EqualFold(repository.userByEmail.PrimaryEmail, input.Email) {
		return domain.User{}, domain.ErrNotFound
	}
	return *repository.userByEmail, nil
}

func (repository *identityRepositoryStub) LinkVerifiedMemberIdentity(_ context.Context, input application.LinkVerifiedMemberIdentityInput) (domain.Membership, error) {
	repository.linked = append(repository.linked, input)
	if repository.memberByEmail.ID == "" {
		return domain.Membership{}, domain.ErrNotFound
	}
	return repository.memberByEmail, nil
}

func (*identityRepositoryStub) Health(context.Context) error { return nil }

type passwordHasherStub struct{}

type serviceClock struct{ now time.Time }

func (clock serviceClock) Now() time.Time { return clock.now }

func (passwordHasherStub) Hash(password string) (string, error) {
	return "hashed:" + password, nil
}

func (passwordHasherStub) Verify(encodedHash, password string) (bool, error) {
	return encodedHash == "hashed:"+password, nil
}

func TestBootstrapPersistsHashedLocalPassword(t *testing.T) {
	now := time.Unix(1_000, 0).UTC()
	repository := &identityRepositoryStub{policy: domain.OrganizationPolicy{SessionMaxAge: 24 * time.Hour}}
	activator := &activatorStub{}
	service := NewService(repository, nil, nil, activator, entitlements.NewUnlimited(), passwordHasherStub{}, serviceClock{now}, ServiceConfig{
		BootstrapSecret: "bootstrap-secret", SessionTTL: 24 * time.Hour,
	})

	result, err := service.Bootstrap(context.Background(), "bootstrap-secret", BootstrapInput{
		OrganizationSlug: " acme ", OrganizationName: " Acme ", DisplayName: " Ada ",
		Email: " ADA@EXAMPLE.COM ", Password: "correct horse battery staple",
	})
	if err != nil {
		t.Fatal(err)
	}
	if activator.activated.PasswordHash != "hashed:correct horse battery staple" {
		t.Fatalf("unexpected password hash %q", activator.activated.PasswordHash)
	}
	if activator.activated.Email != "ada@example.com" || activator.activated.OrganizationSlug != "acme" {
		t.Fatalf("bootstrap identity was not normalized: %#v", activator.activated)
	}
	if activator.activated.AdmissionSource != domain.AdmissionManual {
		t.Fatalf("bootstrap must admit manually, got %q", activator.activated.AdmissionSource)
	}
	if result.Token == "" || repository.session.Auth.Method != "bootstrap" {
		t.Fatalf("bootstrap session was not created: %#v", repository.session)
	}
}

func loginTestRepository() *identityRepositoryStub {
	return &identityRepositoryStub{
		credential: application.LocalCredentialRecord{UserID: "user-1", PasswordHash: "hashed:correct horse battery staple"},
		membership: domain.Membership{ID: "membership-1", UserID: "user-1", OrganizationID: "organization-1", Status: domain.MembershipActive},
		policy:     domain.OrganizationPolicy{OrganizationID: "organization-1", SessionMaxAge: 24 * time.Hour},
		loginOrganizations: application.LoginOrganizations{
			UserID: "user-1", HasPassword: true,
			Organizations: []domain.Organization{{ID: "organization-1", Slug: "acme", Name: "Acme", Status: domain.OrganizationActive}},
		},
	}
}

func loginTestService(repository *identityRepositoryStub, now time.Time) *Service {
	return NewService(repository, nil, nil, nil, entitlements.NewUnlimited(), passwordHasherStub{}, serviceClock{now}, ServiceConfig{SessionTTL: 24 * time.Hour})
}

func TestLoginWithOneOrganizationCompletesImmediately(t *testing.T) {
	now := time.Unix(2_000, 0).UTC()
	repository := loginTestRepository()
	service := loginTestService(repository, now)

	result, err := service.LoginLocal(context.Background(), LocalLoginInput{
		Email: " ADA@EXAMPLE.COM ", Password: "correct horse battery staple",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Authentication == nil || result.Authentication.Principal.OrganizationID != "organization-1" {
		t.Fatalf("single-organization login did not complete: %#v", result)
	}
	if repository.session.Auth.Method != "local_password" || repository.session.Auth.AuthenticatedAt != now {
		t.Fatalf("unexpected local session: %#v", repository.session)
	}
	if len(repository.loginTickets) != 0 {
		t.Fatal("a ticket was minted for a single-organization login")
	}
}

func TestLoginWithSeveralOrganizationsMintsTicket(t *testing.T) {
	now := time.Unix(2_500, 0).UTC()
	repository := loginTestRepository()
	repository.loginOrganizations.Organizations = append(repository.loginOrganizations.Organizations,
		domain.Organization{ID: "organization-2", Slug: "beta", Name: "Beta", Status: domain.OrganizationActive})
	service := loginTestService(repository, now)

	result, err := service.LoginLocal(context.Background(), LocalLoginInput{
		Email: "ada@example.com", Password: "correct horse battery staple",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Authentication != nil || repository.sessions != 0 {
		t.Fatalf("multi-organization login minted a session: %#v", result)
	}
	if !strings.HasPrefix(result.Ticket, "whl_") || len(result.Organizations) != 2 {
		t.Fatalf("unexpected selection step: %#v", result)
	}
	if len(repository.loginTickets) != 1 || repository.loginTickets[0].UserID != "user-1" {
		t.Fatalf("ticket was not persisted: %#v", repository.loginTickets)
	}
	if repository.loginTickets[0].ExpiresAt != now.Add(defaultLoginTicketTTL) {
		t.Fatalf("unexpected ticket expiry: %v", repository.loginTickets[0].ExpiresAt)
	}
}

func TestLoginRejectsInvalidCredentials(t *testing.T) {
	repository := loginTestRepository()
	service := loginTestService(repository, time.Unix(3_000, 0).UTC())

	_, err := service.LoginLocal(context.Background(), LocalLoginInput{
		Email: "ada@example.com", Password: "wrong password",
	})
	if !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("got %v, want unauthenticated", err)
	}
	if repository.sessions != 0 || len(repository.loginTickets) != 0 {
		t.Fatal("invalid password produced a session or ticket")
	}
}

func TestLoginEnforcesAccountAndOrganizationState(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*identityRepositoryStub, *Service)
		want      error
	}{
		{name: "unknown email", configure: func(repository *identityRepositoryStub, _ *Service) {
			repository.credentialErr = domain.ErrNotFound
		}, want: domain.ErrUnauthenticated},
		{name: "no organizations", configure: func(repository *identityRepositoryStub, _ *Service) {
			repository.loginOrganizations.Organizations = nil
		}, want: ErrNoOrganizations},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := loginTestRepository()
			service := loginTestService(repository, time.Unix(4_000, 0).UTC())
			test.configure(repository, service)

			_, err := service.LoginLocal(context.Background(), LocalLoginInput{
				Email: "ada@example.com", Password: "correct horse battery staple",
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if repository.sessions != 0 {
				t.Fatal("unavailable account created a session")
			}
		})
	}
}

// A verified person whose every organization is connected to enterprise
// SSO is handed to SSO, never refused and never given a ticket.
func TestLoginRoutesSSOOnlyAccountsToSSO(t *testing.T) {
	now := time.Unix(4_500, 0).UTC()
	repository := loginTestRepository()
	service := loginTestService(repository, now)
	service.RegisterLoginPolicy(externalLoginEverywhere())

	result, err := service.LoginLocal(context.Background(), LocalLoginInput{
		Email: "ada@example.com", Password: "correct horse battery staple",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.SSORequired || result.Authentication != nil || result.Ticket != "" {
		t.Fatalf("SSO-only account was not handed to SSO: %#v", result)
	}
	if len(result.Organizations) != 1 || !result.Organizations[0].SSO || result.Organizations[0].Slug != "acme" {
		t.Fatalf("SSO organizations missing: %#v", result.Organizations)
	}
	if repository.sessions != 0 || len(repository.loginTickets) != 0 {
		t.Fatal("SSO hand-off minted a session or ticket")
	}
}

func TestSelectLoginOrganizationMintsSessionOnce(t *testing.T) {
	issued := time.Unix(5_000, 0).UTC()
	repository := loginTestRepository()
	repository.ticketRecord = application.LoginTicketRecord{UserID: "user-1", AuthMethod: "local_password", Assurance: "single_factor", IssuedAt: issued}
	service := loginTestService(repository, issued.Add(time.Minute))

	result, err := service.SelectLoginOrganization(context.Background(), SelectOrganizationInput{
		Ticket: "whl_1b4e28ba-2fa1-11d2-883f-0016d3cca427_secret", OrganizationID: "organization-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Principal.OrganizationID != "organization-1" || repository.session.Auth.Method != "local_password" {
		t.Fatalf("unexpected selected session: %#v", repository.session)
	}
	if repository.session.Auth.AuthenticatedAt != issued {
		t.Fatalf("session authentication time should be the ticket issue time: %#v", repository.session.Auth)
	}
	if repository.consumedTicket == nil || string(repository.consumedTicket.TicketID) != "1b4e28ba-2fa1-11d2-883f-0016d3cca427" {
		t.Fatalf("ticket was not consumed: %#v", repository.consumedTicket)
	}
}

func TestSelectLoginOrganizationRefusals(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*identityRepositoryStub, *Service)
		want      error
	}{
		{name: "consumed or expired ticket", configure: func(repository *identityRepositoryStub, _ *Service) {
			repository.ticketErr = domain.ErrUnauthenticated
		}, want: domain.ErrUnauthenticated},
		{name: "no membership in the chosen organization", configure: func(repository *identityRepositoryStub, _ *Service) {
			repository.membership = domain.Membership{}
		}, want: domain.ErrUnauthenticated},
		{name: "suspended membership", configure: func(repository *identityRepositoryStub, _ *Service) {
			repository.membership.Status = domain.MembershipSuspended
		}, want: domain.ErrMembershipInactive},
		{name: "SSO organization is never password-selectable", configure: func(_ *identityRepositoryStub, service *Service) {
			service.RegisterLoginPolicy(externalLoginEverywhere())
		}, want: ErrSSORequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := loginTestRepository()
			repository.ticketRecord = application.LoginTicketRecord{UserID: "user-1", IssuedAt: time.Unix(5_000, 0).UTC()}
			service := loginTestService(repository, time.Unix(5_100, 0).UTC())
			test.configure(repository, service)

			if _, err := service.SelectLoginOrganization(context.Background(), SelectOrganizationInput{
				Ticket: "whl_1b4e28ba-2fa1-11d2-883f-0016d3cca427_secret", OrganizationID: "organization-1",
			}); !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if repository.sessions != 0 {
				t.Fatal("a refused selection created a session")
			}
		})
	}
}

func TestLoginMethodsPartitionsOrganizations(t *testing.T) {
	now := time.Unix(6_000, 0).UTC()
	repository := loginTestRepository()
	repository.loginOrganizations.Organizations = append(repository.loginOrganizations.Organizations,
		domain.Organization{ID: "organization-2", Slug: "beta", Name: "Beta", Status: domain.OrganizationActive})
	service := loginTestService(repository, now)
	service.RegisterLoginPolicy(externalLoginFor("organization-2"))

	methods, err := service.LoginMethods(context.Background(), "ada@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !methods.Password {
		t.Fatal("a password organization exists, so the password field must show")
	}
	if len(methods.SSOOrganizations) != 1 || methods.SSOOrganizations[0].Slug != "beta" {
		t.Fatalf("unexpected SSO organizations: %#v", methods.SSOOrganizations)
	}
}

func TestLoginMethodsIsUniformForUnknownEmails(t *testing.T) {
	repository := &identityRepositoryStub{}
	service := loginTestService(repository, time.Unix(6_100, 0).UTC())
	methods, err := service.LoginMethods(context.Background(), "nobody@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !methods.Password || len(methods.SSOOrganizations) != 0 {
		t.Fatalf("unknown emails must look like password accounts: %#v", methods)
	}
}

func TestLoginMethodsHidesPasswordWhenEveryOrganizationHasSSO(t *testing.T) {
	now := time.Unix(6_200, 0).UTC()
	repository := loginTestRepository()
	service := loginTestService(repository, now)
	service.RegisterLoginPolicy(externalLoginEverywhere())

	methods, err := service.LoginMethods(context.Background(), "ada@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if methods.Password || len(methods.SSOOrganizations) != 1 {
		t.Fatalf("SSO-only account still shows a password field: %#v", methods)
	}
}

func TestParseOpaqueTokenPreservesURLSafeSecret(t *testing.T) {
	credentialID := "1b4e28ba-2fa1-11d2-883f-0016d3cca427"
	id, secret, err := ParseOpaqueToken("whc_"+credentialID+"_contains_an_underscore", "whc")
	if err != nil {
		t.Fatal(err)
	}
	if id != credentialID || secret != "contains_an_underscore" {
		t.Fatalf("unexpected token parts: id=%q secret=%q", id, secret)
	}
}

func TestParseOpaqueTokenRejectsWrongOrIncompletePrefix(t *testing.T) {
	// A non-UUID identifier is rejected here rather than at the
	// database, where a uuid-typed lookup would fail as an internal
	// error instead of a failed authentication.
	for _, token := range []string{
		"whd_1b4e28ba-2fa1-11d2-883f-0016d3cca427_secret",
		"whc__secret", "whc_id_", "whc",
		"whc_not-a-uuid_secret",
	} {
		if _, _, err := ParseOpaqueToken(token, "whc"); err == nil {
			t.Fatalf("expected %q to be rejected", token)
		}
	}
}
