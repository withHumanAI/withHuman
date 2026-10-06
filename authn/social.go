package authn

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
)

// SocialAuthenticator is a redirect-based consumer identity provider such
// as Google or GitHub. Core never talks to a provider: the edition that
// registers one owns its flow, callback, and CSRF state; core owns what an
// assertion may do once it exists.
type SocialAuthenticator interface {
	Authenticator
	// Label is the provider's display name for the sign-in button.
	Label() string
}

// SocialProvider is what the access screen learns about a registered
// social authenticator through the runtime configuration.
type SocialProvider struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// RegisterSocialAuthenticator wires a social login provider. The
// composition root calls it once per provider before serving; a duplicate
// or reserved key is a wiring error, not a runtime condition.
func (s *Service) RegisterSocialAuthenticator(authenticator SocialAuthenticator) {
	key := authenticator.Key()
	if key == "" || key == LocalProviderKey {
		panic(fmt.Sprintf("authn: social authenticator key %q is reserved", key))
	}
	if s.socialAuthenticator(key) != nil {
		panic(fmt.Sprintf("authn: social authenticator %q registered twice", key))
	}
	s.social = append(s.social, authenticator)
}

// SocialProviders lists the registered social providers in registration
// order. It is never nil so the runtime configuration serializes a list.
func (s *Service) SocialProviders() []SocialProvider {
	providers := make([]SocialProvider, 0, len(s.social))
	for _, authenticator := range s.social {
		providers = append(providers, SocialProvider{Key: authenticator.Key(), Label: authenticator.Label()})
	}
	return providers
}

func (s *Service) socialAuthenticator(key string) SocialAuthenticator {
	for _, authenticator := range s.social {
		if authenticator.Key() == key {
			return authenticator
		}
	}
	return nil
}

// ErrNoAccount is the login outcome for a verified identity that no
// withHuman account uses yet. Login never creates accounts; signup does.
var ErrNoAccount = errors.New("no withHuman account uses this email yet")

// ErrEmailUnverified is the outcome for an unknown identity whose email
// the provider has not verified: an unverified email can neither link to
// an existing account nor found an organization.
var ErrEmailUnverified = errors.New("your sign-in provider has not verified this email address")

type AssertionLoginInput struct {
	Assertion Assertion
	Client    application.SessionClient
}

// LoginWithAssertion signs an existing account in with a social provider
// assertion. A linked identity resolves directly, whatever email the
// provider reports today: the (provider, issuer, subject) triple is the
// identity. An unknown identity whose email the provider verified is
// linked to the active account with that primary email and audited in
// each of its organizations; otherwise it is refused. Login never creates
// users. The organization step is shared with password login, so
// organizations with an enterprise connection stay unreachable this way.
func (s *Service) LoginWithAssertion(ctx context.Context, input AssertionLoginInput) (LoginResult, error) {
	assertion := input.Assertion
	if s.socialAuthenticator(assertion.ProviderKey) == nil || assertion.Issuer == "" || assertion.Subject == "" {
		return LoginResult{}, fmt.Errorf("assertion does not come from a registered social provider: %w", domain.ErrUnauthenticated)
	}
	assertion.Email = strings.ToLower(strings.TrimSpace(assertion.Email))
	now := s.clock.Now()
	if assertion.AuthenticatedAt.IsZero() {
		assertion.AuthenticatedAt = now
	}
	user, err := s.identities.ResolveExternalIdentity(ctx, application.ResolveExternalIdentityInput{
		ProviderKey:   assertion.ProviderKey,
		Issuer:        assertion.Issuer,
		Subject:       assertion.Subject,
		ObservedEmail: assertion.Email,
		Now:           now,
	})
	if errors.Is(err, domain.ErrNotFound) {
		if !assertion.EmailVerified || assertion.Email == "" {
			return LoginResult{}, fmt.Errorf("%w: %w", ErrEmailUnverified, domain.ErrForbidden)
		}
		user, err = s.identities.LinkVerifiedUserIdentity(ctx, application.LinkVerifiedUserIdentityInput{
			Email:       assertion.Email,
			ProviderKey: assertion.ProviderKey,
			Issuer:      assertion.Issuer,
			Subject:     assertion.Subject,
			Now:         now,
		})
		if errors.Is(err, domain.ErrNotFound) {
			return LoginResult{}, fmt.Errorf("%w: %w", ErrNoAccount, domain.ErrForbidden)
		}
	}
	if err != nil {
		return LoginResult{}, err
	}
	// The canonical email, not the asserted one: a provider-side email
	// change never changes which account the person signs in to.
	login, err := s.identities.ListLoginOrganizations(ctx, application.ListLoginOrganizationsInput{Email: user.PrimaryEmail})
	if err != nil {
		return LoginResult{}, err
	}
	if login.UserID != user.ID {
		return LoginResult{}, domain.ErrUnauthenticated
	}
	return s.finishLogin(ctx, user.ID, login.Organizations, domain.AuthContext{
		Method: "social_" + assertion.ProviderKey, Assurance: AssuranceFor(assertion), AuthenticatedAt: assertion.AuthenticatedAt,
	}, input.Client)
}
