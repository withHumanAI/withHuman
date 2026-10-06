package authn

import (
	"context"
	"errors"

	"github.com/withHumanAI/withHuman/domain"
)

// ErrEnterpriseNotConfigured is the answer of an enterprise surface the
// deployment did not wire: the hosted edition's SSO, directory sync and
// verified domains, and core's own directory group grants without a role
// repository. Routes map it to 501 enterprise_not_configured.
var ErrEnterpriseNotConfigured = errors.New("single sign-on is not enabled on this deployment")

// LoginPolicy is an edition's say over how people sign in, consulted at
// every login-shaped door: methods discovery, the password and social
// organization step, ticket redemption and invitation redemption. The
// hosted edition answers from its enterprise connector and verified
// domains; without a policy every organization signs in directly.
type LoginPolicy interface {
	// RequiresExternalLogin reports whether the organization signs in only
	// through an external method (enterprise SSO), so a password or social
	// login never mints a session for it and the access screen hands the
	// person over instead.
	RequiresExternalLogin(ctx context.Context, organizationID domain.ID) (bool, error)
	// RouteEmail names the organization an email is routed to before any
	// account exists (home realm discovery by a verified domain), or false.
	RouteEmail(ctx context.Context, email string) (domain.Organization, bool, error)
	// VouchesForEmail reports whether the organization's identity provider
	// speaks for the email's domain, so an invitation accepted with an
	// assertion may attach an existing account under it.
	VouchesForEmail(ctx context.Context, organizationID domain.ID, email string) (bool, error)
}

// RegisterLoginPolicy wires the edition's login policy.
func (s *Service) RegisterLoginPolicy(policy LoginPolicy) { s.loginPolicy = policy }

// requiresExternalLogin asks the policy, answering direct login without
// one.
func (s *Service) requiresExternalLogin(ctx context.Context, organizationID domain.ID) (bool, error) {
	if s.loginPolicy == nil {
		return false, nil
	}
	return s.loginPolicy.RequiresExternalLogin(ctx, organizationID)
}
