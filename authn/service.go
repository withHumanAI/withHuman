package authn

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/authorization"
	"github.com/withHumanAI/withHuman/tenants"
)

const SessionCookieName = "withhuman_session"

type ServiceConfig struct {
	BootstrapSecret string
	PublicOrigin    string
	SessionTTL      time.Duration
	// NativeSessionTTL is the lifetime of a session for a phone app (a
	// client kind in NativeClients); zero falls back to SessionTTL.
	NativeSessionTTL  time.Duration
	CredentialTTL     time.Duration
	EnrollmentCodeTTL time.Duration
	ProvisionerTTL    time.Duration
	LoginTicketTTL    time.Duration
	// APIKeyScopingEnabled says the edition lets a personal key carry a
	// permission list (the same seam as custom role authoring); the open
	// edition's keys inherit the member's role.
	APIKeyScopingEnabled bool
	// APIKeyMaxTTL caps how far out a personal key may expire; zero means
	// a key may live until revoked.
	APIKeyMaxTTL time.Duration
}

// defaultLoginTicketTTL bounds the gap between a verified password and
// the organization selection; the ticket is single use either way.
const defaultLoginTicketTTL = 5 * time.Minute

// OrganizationActivator is the tenant lifecycle service's activation
// surface, wired by the composition root so bootstrap and every other
// creation path share one transaction.
type OrganizationActivator interface {
	ActivateOrganization(ctx context.Context, input tenants.ActivateOrganizationInput) (application.ActivationResult, error)
}

type Service struct {
	identities    application.IdentityRepository
	agents        application.AgentRepository
	memberships   application.MembershipRepository
	activator     OrganizationActivator
	entitlements  application.Entitlements
	passwords     PasswordHasher
	clock         application.Clock
	config        ServiceConfig
	local         Authenticator
	social        []SocialAuthenticator
	loginPolicy   LoginPolicy
	roles         application.RoleRepository
	gatewayToken  string
	onboarding    application.OnboardingRepository
	apiKeys       application.APIKeyRepository
	notifications application.NotificationDispatcher
	deliveries    application.InstanceDeliveryVerifier
}

func NewService(identities application.IdentityRepository, agents application.AgentRepository, memberships application.MembershipRepository, activator OrganizationActivator, entitlements application.Entitlements, passwords PasswordHasher, clock application.Clock, config ServiceConfig) *Service {
	if passwords == nil {
		passwords = NewArgon2idPasswordHasher()
	}
	if config.LoginTicketTTL <= 0 {
		config.LoginTicketTTL = defaultLoginTicketTTL
	}
	service := &Service{identities: identities, agents: agents, memberships: memberships, activator: activator, entitlements: entitlements, passwords: passwords, clock: clock, config: config}
	service.local = NewLocalAuthenticator(identities, passwords, clock, config.PublicOrigin)
	return service
}

// RegisterInstanceDeliveries wires the AAP receiver verifier. Without it,
// minting an instance with delivery configuration is refused as
// unconfigured.
func (s *Service) RegisterInstanceDeliveries(verifier application.InstanceDeliveryVerifier) {
	s.deliveries = verifier
}

// RegisterRoles wires the role repository, which just-in-time admission
// and directory group grants read to validate the role they hand out.
func (s *Service) RegisterRoles(repository application.RoleRepository) { s.roles = repository }

type AuthenticationResult struct {
	Principal domain.Principal
	Token     string
	ExpiresAt time.Time
}

type BootstrapInput struct {
	OrganizationSlug string
	OrganizationName string
	DisplayName      string
	Email            string
	Password         string
}

func (s *Service) Bootstrap(ctx context.Context, providedSecret string, input BootstrapInput) (AuthenticationResult, error) {
	if s.config.BootstrapSecret == "" || subtle.ConstantTimeCompare([]byte(providedSecret), []byte(s.config.BootstrapSecret)) != 1 {
		return AuthenticationResult{}, domain.ErrUnauthenticated
	}
	input.OrganizationSlug = strings.TrimSpace(input.OrganizationSlug)
	input.OrganizationName = strings.TrimSpace(input.OrganizationName)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if err := validateNewPassword(input.Password); err != nil {
		return AuthenticationResult{}, err
	}
	hasOwner, err := s.identities.HasOwner(ctx)
	if err != nil {
		return AuthenticationResult{}, err
	}
	if hasOwner {
		return AuthenticationResult{}, fmt.Errorf("bootstrap is disabled after the first owner: %w", domain.ErrConflict)
	}
	passwordHash, err := s.passwords.Hash(input.Password)
	if err != nil {
		return AuthenticationResult{}, fmt.Errorf("hash owner password: %w", err)
	}
	now := s.clock.Now()
	result, err := s.activator.ActivateOrganization(ctx, tenants.ActivateOrganizationInput{
		OrganizationName: input.OrganizationName,
		OrganizationSlug: input.OrganizationSlug,
		DisplayName:      input.DisplayName,
		Email:            input.Email,
		PasswordHash:     passwordHash,
		AdmissionSource:  domain.AdmissionManual,
	})
	if err != nil {
		return AuthenticationResult{}, err
	}
	principal := result.Principal
	return s.createSession(ctx, principal.ActorID, principal.OrganizationID, domain.AuthContext{
		Method: "bootstrap", Assurance: "strong", AuthenticatedAt: now,
	})
}

// IssueSession exchanges an already established principal for a withHuman
// session. It exists for edition composition roots whose own admission
// flows (hosted signup) end in core's activation transaction; session
// issuance stays single-path through createSession.
func (s *Service) IssueSession(ctx context.Context, principal domain.Principal, method string) (AuthenticationResult, error) {
	if principal.ActorType != domain.ActorHuman || principal.ActorID == "" || principal.OrganizationID == "" {
		return AuthenticationResult{}, domain.ErrUnauthenticated
	}
	return s.createSession(ctx, principal.ActorID, principal.OrganizationID, domain.AuthContext{
		Method: method, Assurance: "single_factor", AuthenticatedAt: s.clock.Now(),
	})
}

// ErrSSORequired is the login outcome for an organization with an
// enterprise connection: its presence is the policy, and a password never
// mints a session there.
var ErrSSORequired = errors.New("this organization signs in through single sign-on")

// ErrNoOrganizations is the login outcome for a verified account that
// belongs to no active organization.
var ErrNoOrganizations = errors.New("this account does not belong to any organization")

// LoginOrganizationOption is one entry in the organization picker. SSO
// organizations are routed to the enterprise flow instead of being
// selectable with the password ticket.
type LoginOrganizationOption struct {
	ID   domain.ID `json:"id"`
	Slug string    `json:"slug"`
	Name string    `json:"name"`
	SSO  bool      `json:"sso"`
}

// LoginMethods is what the access screen may learn from an email alone:
// whether to show a password field and which organizations sign in
// through SSO. It deliberately reveals nothing else about the account.
type LoginMethods struct {
	Password         bool
	SSOOrganizations []LoginOrganizationOption
}

// LoginMethods resolves an email to its available login methods. Unknown
// emails get the password field so the form does not confirm which
// addresses exist; the SSO organization list is the deliberate,
// industry-standard exception that home realm discovery requires.
func (s *Service) LoginMethods(ctx context.Context, email string) (LoginMethods, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return LoginMethods{}, fmt.Errorf("email is required: %w", domain.ErrInvalid)
	}
	login, err := s.identities.ListLoginOrganizations(ctx, application.ListLoginOrganizationsInput{Email: email})
	if err != nil {
		return LoginMethods{}, err
	}
	options, err := s.loginOrganizationOptions(ctx, login.Organizations)
	if err != nil {
		return LoginMethods{}, err
	}
	methods := LoginMethods{SSOOrganizations: make([]LoginOrganizationOption, 0, len(options))}
	passwordOrganizations := 0
	for _, option := range options {
		if option.SSO {
			methods.SSOOrganizations = append(methods.SSOOrganizations, option)
		} else {
			passwordOrganizations++
		}
	}
	// Home realm discovery: the edition's policy may route an email to an
	// organization's SSO even when the person has never signed in before
	// (the hosted edition's verified domains). This is what makes
	// provisioning invisible.
	domainRouted := false
	if organization, captured, err := s.routeEmail(ctx, email); err != nil {
		return LoginMethods{}, err
	} else if captured {
		domainRouted = true
		alreadyListed := false
		for _, option := range methods.SSOOrganizations {
			if option.ID == organization.ID {
				alreadyListed = true
				break
			}
		}
		if !alreadyListed {
			methods.SSOOrganizations = append(methods.SSOOrganizations, LoginOrganizationOption{
				ID: organization.ID, Slug: organization.Slug, Name: organization.Name, SSO: true,
			})
		}
	}
	methods.Password = passwordOrganizations > 0 || (len(options) == 0 && !domainRouted)
	return methods, nil
}

type LocalLoginInput struct {
	Email    string
	Password string
	// Client is the phone app that signed in, empty for the browser. Only
	// kinds in NativeClients are accepted: the kind picks the session's
	// lifetime.
	Client application.SessionClient
}

// LoginResult is one of three outcomes: a completed login (Authentication
// set), an organization selection step (Ticket and Organizations set), or
// a hand-off to enterprise SSO (SSORequired with Organizations listing the
// connected organizations and no ticket, because a ticket can never be
// redeemed for a connected organization).
type LoginResult struct {
	Authentication  *AuthenticationResult
	Ticket          string
	TicketExpiresAt time.Time
	Organizations   []LoginOrganizationOption
	SSORequired     bool
}

// LoginLocal verifies the password globally, then hands the verified user
// to the shared organization step: exactly one organization completes the
// login, several mint a single-use ticket for the organization picker.
func (s *Service) LoginLocal(ctx context.Context, input LocalLoginInput) (LoginResult, error) {
	if _, native := NativeClients[input.Client.Kind]; input.Client.Kind != "" && !native {
		return LoginResult{}, fmt.Errorf("client kind %q is not registered: %w", input.Client.Kind, domain.ErrInvalid)
	}
	assertion, err := s.local.Complete(ctx, map[string]string{"email": input.Email, "password": input.Password})
	if err != nil {
		return LoginResult{}, err
	}
	userID := domain.ID(assertion.Subject)
	login, err := s.identities.ListLoginOrganizations(ctx, application.ListLoginOrganizationsInput{Email: assertion.Email})
	if err != nil {
		return LoginResult{}, err
	}
	if login.UserID != userID {
		return LoginResult{}, ErrInvalidCredentials
	}
	return s.finishLogin(ctx, userID, login.Organizations, domain.AuthContext{
		Method: "local_password", Assurance: AssuranceFor(assertion), AuthenticatedAt: assertion.AuthenticatedAt,
	}, input.Client)
}

// finishLogin is the organization step shared by every global
// authenticator once the person is verified: exactly one organization
// completes the login, several mint a single-use ticket for the picker,
// and organizations with an enterprise connection are never reachable
// this way. When every organization is connected the person is handed to
// SSO rather than refused: the access screen redirects to the one
// organization's SSO, or offers the picker when there are several. The
// ticket carries the authentication context so the session it later
// mints is labeled by the authenticator that earned it.
func (s *Service) finishLogin(ctx context.Context, userID domain.ID, organizations []domain.Organization, auth domain.AuthContext, client application.SessionClient) (LoginResult, error) {
	options, err := s.loginOrganizationOptions(ctx, organizations)
	if err != nil {
		return LoginResult{}, err
	}
	directOptions := make([]LoginOrganizationOption, 0, len(options))
	for _, option := range options {
		if !option.SSO {
			directOptions = append(directOptions, option)
		}
	}
	switch {
	case len(options) == 0:
		return LoginResult{}, fmt.Errorf("%w: %w", ErrNoOrganizations, domain.ErrForbidden)
	case len(directOptions) == 0:
		return LoginResult{Organizations: options, SSORequired: true}, nil
	case len(options) == 1:
		authentication, err := s.createSessionFor(ctx, userID, options[0].ID, auth, client)
		if err != nil {
			return LoginResult{}, err
		}
		return LoginResult{Authentication: &authentication}, nil
	}
	ticketID := domain.ID(uuid.NewString())
	ticket, secretHash, err := MintOpaqueToken("whl", ticketID)
	if err != nil {
		return LoginResult{}, err
	}
	now := s.clock.Now()
	expiresAt := now.Add(s.config.LoginTicketTTL)
	if err := s.identities.CreateLoginTicket(ctx, application.CreateLoginTicketInput{
		TicketID: ticketID, UserID: userID, SecretHash: secretHash,
		AuthMethod: auth.Method, Assurance: auth.Assurance, Client: client,
		IssuedAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Ticket: ticket, TicketExpiresAt: expiresAt, Organizations: options}, nil
}

type SelectOrganizationInput struct {
	Ticket         string
	OrganizationID domain.ID
}

// SelectLoginOrganization redeems a login ticket for one organization and
// mints the session. The ticket is consumed on redemption whatever the
// outcome of the membership checks: a failed selection means signing in
// again, never retrying an old ticket.
func (s *Service) SelectLoginOrganization(ctx context.Context, input SelectOrganizationInput) (AuthenticationResult, error) {
	ticketID, secret, err := ParseOpaqueToken(input.Ticket, "whl")
	if err != nil {
		return AuthenticationResult{}, domain.ErrUnauthenticated
	}
	record, err := s.identities.ConsumeLoginTicket(ctx, application.ConsumeLoginTicketInput{
		TicketID: domain.ID(ticketID), SecretHash: HashToken(secret), Now: s.clock.Now(),
	})
	if err != nil {
		return AuthenticationResult{}, err
	}
	membership, err := s.identities.FindMembershipForLogin(ctx, application.FindMembershipForLoginInput{
		UserID:         record.UserID,
		OrganizationID: input.OrganizationID,
	})
	if errors.Is(err, domain.ErrNotFound) {
		return AuthenticationResult{}, domain.ErrUnauthenticated
	}
	if err != nil {
		return AuthenticationResult{}, err
	}
	if membership.Status != domain.MembershipActive {
		return AuthenticationResult{}, domain.ErrMembershipInactive
	}
	ssoEnabled, err := s.requiresExternalLogin(ctx, membership.OrganizationID)
	if err != nil {
		return AuthenticationResult{}, err
	}
	if ssoEnabled {
		return AuthenticationResult{}, fmt.Errorf("%w: %w", ErrSSORequired, domain.ErrForbidden)
	}
	return s.createSessionFor(ctx, record.UserID, membership.OrganizationID, domain.AuthContext{
		Method: record.AuthMethod, Assurance: record.Assurance, AuthenticatedAt: record.IssuedAt,
	}, record.Client)
}

// loginOrganizationOptions decorates a user's organizations with whether
// each one signs in through SSO.
func (s *Service) loginOrganizationOptions(ctx context.Context, organizations []domain.Organization) ([]LoginOrganizationOption, error) {
	options := make([]LoginOrganizationOption, 0, len(organizations))
	for _, organization := range organizations {
		ssoEnabled, err := s.requiresExternalLogin(ctx, organization.ID)
		if err != nil {
			return nil, err
		}
		options = append(options, LoginOrganizationOption{
			ID: organization.ID, Slug: organization.Slug, Name: organization.Name, SSO: ssoEnabled,
		})
	}
	return options, nil
}

// routeEmail asks the policy which organization an email belongs to
// before any account exists; without a policy no email is routed.
func (s *Service) routeEmail(ctx context.Context, email string) (domain.Organization, bool, error) {
	if s.loginPolicy == nil {
		return domain.Organization{}, false, nil
	}
	return s.loginPolicy.RouteEmail(ctx, email)
}

// AssuranceFor maps a provider assertion onto the session assurance
// vocabulary the decision authorization checks consume. Only an assertion
// that carries a multi-factor signal earns "strong".
func AssuranceFor(assertion Assertion) string {
	if assertion.Assurance == "strong" {
		return "strong"
	}
	for _, method := range assertion.Methods {
		switch strings.ToLower(method) {
		case "mfa", "otp", "totp", "hwk", "swk", "webauthn", "passkey":
			return "strong"
		}
	}
	return "single_factor"
}

func validateNewPassword(password string) error {
	if utf8.RuneCountInString(password) < minimumPasswordCharacters {
		return fmt.Errorf("password must contain at least %d characters: %w", minimumPasswordCharacters, domain.ErrInvalid)
	}
	if len(password) > maximumPasswordBytes {
		return fmt.Errorf("password must not exceed %d bytes: %w", maximumPasswordBytes, domain.ErrInvalid)
	}
	return nil
}

// createSession mints a browser session; native clients go through
// createSessionFor with their own client record.
func (s *Service) createSession(ctx context.Context, userID, organizationID domain.ID, auth domain.AuthContext) (AuthenticationResult, error) {
	return s.createSessionFor(ctx, userID, organizationID, auth, application.SessionClient{Kind: application.ClientWeb})
}

func (s *Service) createSessionFor(ctx context.Context, userID, organizationID domain.ID, auth domain.AuthContext, client application.SessionClient) (AuthenticationResult, error) {
	raw, err := randomToken(32)
	if err != nil {
		return AuthenticationResult{}, err
	}
	ttl := s.config.SessionTTL
	if _, native := NativeClients[client.Kind]; native && s.config.NativeSessionTTL > 0 {
		ttl = s.config.NativeSessionTTL
	}
	policy, err := s.identities.GetOrganizationPolicy(ctx, application.GetOrganizationPolicyInput{
		OrganizationID: organizationID,
	})
	if err != nil {
		return AuthenticationResult{}, err
	}
	if policy.SessionMaxAge > 0 && policy.SessionMaxAge < ttl {
		ttl = policy.SessionMaxAge
	}
	now := s.clock.Now()
	expiresAt := now.Add(ttl)
	principal, err := s.identities.CreateSession(ctx, application.CreateSessionInput{
		UserID:         userID,
		OrganizationID: organizationID,
		SecretHash:     HashToken(raw),
		IssuedAt:       now,
		ExpiresAt:      expiresAt,
		Auth:           auth,
		Client:         client.Normalized(),
	})
	if err != nil {
		return AuthenticationResult{}, err
	}
	return AuthenticationResult{Principal: principal, Token: raw, ExpiresAt: expiresAt}, nil
}

func (s *Service) ResolveSession(ctx context.Context, token string) (domain.Principal, error) {
	if token == "" {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	return s.identities.ResolveSession(ctx, application.ResolveSessionInput{
		SecretHash: HashToken(token),
		Now:        s.clock.Now(),
	})
}

func (s *Service) ResolveCredential(ctx context.Context, token string) (domain.Principal, error) {
	credentialID, secret, err := ParseOpaqueToken(token, "whc")
	if err != nil {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	return s.agents.ResolveAgentCredential(ctx, application.ResolveAgentCredentialInput{
		CredentialID: domain.ID(credentialID),
		SecretHash:   HashToken(secret),
		Now:          s.clock.Now(),
	})
}

// CreateAgent registers a new agent identity under a slug chosen once.
// Who may manage it afterwards is a matter of role assignments at the
// agent; the agent carries no ownership of its own.
func (s *Service) CreateAgent(ctx context.Context, principal domain.Principal, name string, agentSlug domain.AgentSlug, kind domain.AgentKind) (domain.Agent, error) {
	// The requested slug is the resource: an admin bound to one agent may
	// create that agent and no other.
	agentSlug = domain.AgentSlug(strings.TrimSpace(string(agentSlug)))
	if err := requireManagement(principal, domain.PermissionAgentWrite, domain.AgentResource(agentSlug)); err != nil {
		return domain.Agent{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.Agent{}, fmt.Errorf("agent name is required: %w", domain.ErrInvalid)
	}
	if !agentSlug.Valid() {
		return domain.Agent{}, fmt.Errorf("agent slug must match ^[a-z0-9][a-z0-9._-]{0,62}$: %w", domain.ErrInvalid)
	}
	if !kind.Valid() {
		return domain.Agent{}, fmt.Errorf("agent kind must be service or personal: %w", domain.ErrInvalid)
	}
	// The quota is resolved here and enforced inside the creating
	// transaction, so concurrent creations cannot both slip under it.
	limit, err := s.entitlements.Limit(ctx, application.LimitInput{
		OrganizationID: principal.OrganizationID,
		Dimension:      application.DimensionAgents,
	})
	if err != nil {
		return domain.Agent{}, err
	}
	return s.agents.CreateAgent(ctx, application.CreateAgentInput{
		Kind:           kind,
		OrganizationID: principal.OrganizationID,
		ActorID:        principal.ManagementActorID(),
		Name:           name,
		AgentSlug:      agentSlug,
		AgentQuota:     application.ResolvedQuota(limit),
		Now:            s.clock.Now(),
	})
}

// AgentIDBySlug resolves the internal id behind the slug clients address
// agents by. Any management principal may resolve a slug: the agent page
// and every write check their own permission against the agent next.
func (s *Service) AgentIDBySlug(ctx context.Context, principal domain.Principal, agentSlug domain.AgentSlug) (domain.ID, error) {
	if !domain.ManagementPrincipal(principal) {
		return "", domain.ErrForbidden
	}
	if !agentSlug.Valid() {
		return "", domain.ErrNotFound
	}
	agent, err := s.agents.GetAgentBySlug(ctx, application.GetAgentBySlugInput{OrganizationID: principal.OrganizationID, AgentSlug: agentSlug})
	if err != nil {
		return "", err
	}
	return agent.ID, nil
}

// ListAgents is the inventory: anyone holding agent.read somewhere may
// list it (reviewers already see agent names on every request, so the
// roster is not a secret), narrowed to the agents they hold it at.
// Managing agents stays behind agent.write.
func (s *Service) ListAgents(ctx context.Context, principal domain.Principal, status application.AgentListStatus, limit int) ([]domain.AgentSummary, error) {
	if !domain.ManagementPrincipal(principal) {
		return nil, domain.ErrForbidden
	}
	// The scope narrows the query: an admin bound to one agent type sees
	// agents of that type and nothing else crosses the wire.
	scope := authorization.Scope(principal, domain.PermissionAgentRead)
	if scope.Empty() {
		return nil, authorization.Denied{Permission: domain.PermissionAgentRead, Resource: domain.OrganizationResource()}
	}
	return s.agents.ListAgentSummaries(ctx, application.ListAgentSummariesInput{
		OrganizationID: principal.OrganizationID,
		Scope:          scope,
		Status:         status,
		Limit:          limit,
		Now:            s.clock.Now(),
	})
}

// GetAgent is the agent page. The agent is loaded first so agent.read
// can be checked against its type; a denied read returns nothing of it.
func (s *Service) GetAgent(ctx context.Context, principal domain.Principal, input application.GetAgentDetailInput) (domain.AgentDetail, error) {
	if !domain.ManagementPrincipal(principal) {
		return domain.AgentDetail{}, domain.ErrForbidden
	}
	input.OrganizationID = principal.OrganizationID
	input.Now = s.clock.Now()
	agent, err := s.agents.GetAgentDetail(ctx, input)
	if err != nil {
		return domain.AgentDetail{}, err
	}
	if err := authorization.Check(principal, domain.PermissionAgentRead, domain.AgentResource(agent.AgentSlug)); err != nil {
		return domain.AgentDetail{}, err
	}
	return agent, nil
}

// SetAgentStatus disables or re-enables an agent. Disabling keeps every
// instance and credential in place but refuses them all at resolution,
// which is the point: one switch, reversible, no re-enrollment.
func (s *Service) SetAgentStatus(ctx context.Context, principal domain.Principal, agentID domain.ID, status domain.AgentStatus) (domain.Agent, error) {
	if err := s.requireAgentPermission(ctx, principal, domain.PermissionAgentWrite, application.LookupAgentSlugInput{AgentID: agentID}); err != nil {
		return domain.Agent{}, err
	}
	if !status.Valid() {
		return domain.Agent{}, fmt.Errorf("agent status must be active or disabled: %w", domain.ErrInvalid)
	}
	return s.agents.SetAgentStatus(ctx, application.SetAgentStatusInput{
		OrganizationID: principal.OrganizationID,
		AgentID:        agentID,
		Status:         status,
		ActorID:        principal.ManagementActorID(),
		Now:            s.clock.Now(),
	})
}

// RenameAgent changes the display name reviewers see. The slug is the
// identity and stays.
func (s *Service) RenameAgent(ctx context.Context, principal domain.Principal, agentID domain.ID, name string) (domain.Agent, error) {
	if err := s.requireAgentPermission(ctx, principal, domain.PermissionAgentWrite, application.LookupAgentSlugInput{AgentID: agentID}); err != nil {
		return domain.Agent{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.Agent{}, fmt.Errorf("agent name is required: %w", domain.ErrInvalid)
	}
	return s.agents.RenameAgent(ctx, application.RenameAgentInput{
		OrganizationID: principal.OrganizationID,
		AgentID:        agentID,
		Name:           name,
		ActorID:        principal.ManagementActorID(),
		Now:            s.clock.Now(),
	})
}

// ArchiveAgent retires an agent. Unlike disabling, nothing of it
// resolves and nothing can be enrolled under it while it is archived;
// its instances, credentials, and history stay readable, and its
// approval pipeline is archived with it, so the same permission that
// manages the agent is enough: the pipeline has nothing left to decide
// for. RestoreAgent reverses both.
func (s *Service) ArchiveAgent(ctx context.Context, principal domain.Principal, agentID domain.ID) (domain.Agent, error) {
	if err := s.requireAgentPermission(ctx, principal, domain.PermissionAgentWrite, application.LookupAgentSlugInput{AgentID: agentID}); err != nil {
		return domain.Agent{}, err
	}
	return s.agents.ArchiveAgent(ctx, application.ArchiveAgentInput{
		OrganizationID: principal.OrganizationID,
		AgentID:        agentID,
		ActorID:        principal.ManagementActorID(),
		Now:            s.clock.Now(),
	})
}

// RestoreAgent brings an archived agent and its pipeline back. The
// credentials and provisioner tokens revoked at archive stay revoked;
// instances are enrolled again the ordinary way.
func (s *Service) RestoreAgent(ctx context.Context, principal domain.Principal, agentID domain.ID) (domain.Agent, error) {
	if err := s.requireAgentPermission(ctx, principal, domain.PermissionAgentWrite, application.LookupAgentSlugInput{AgentID: agentID}); err != nil {
		return domain.Agent{}, err
	}
	return s.agents.RestoreAgent(ctx, application.RestoreAgentInput{
		OrganizationID: principal.OrganizationID,
		AgentID:        agentID,
		ActorID:        principal.ManagementActorID(),
		Now:            s.clock.Now(),
	})
}

// SetAgentInstanceStatus disables or re-enables one runtime instance.
func (s *Service) SetAgentInstanceStatus(ctx context.Context, principal domain.Principal, instanceID domain.ID, status domain.AgentStatus) (domain.AgentInstance, error) {
	if err := s.requireAgentPermission(ctx, principal, domain.PermissionAgentWrite, application.LookupAgentSlugInput{AgentInstanceID: instanceID}); err != nil {
		return domain.AgentInstance{}, err
	}
	if !status.Valid() {
		return domain.AgentInstance{}, fmt.Errorf("agent instance status must be active or disabled: %w", domain.ErrInvalid)
	}
	return s.agents.SetAgentInstanceStatus(ctx, application.SetAgentInstanceStatusInput{
		OrganizationID: principal.OrganizationID,
		InstanceID:     instanceID,
		Status:         status,
		ActorID:        principal.ManagementActorID(),
		Now:            s.clock.Now(),
	})
}

// RenameAgentInstance changes one instance's display name. Requests
// already made keep the name they were created with.
func (s *Service) RenameAgentInstance(ctx context.Context, principal domain.Principal, instanceID domain.ID, name string) (domain.AgentInstance, error) {
	if err := s.requireAgentPermission(ctx, principal, domain.PermissionAgentWrite, application.LookupAgentSlugInput{AgentInstanceID: instanceID}); err != nil {
		return domain.AgentInstance{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.AgentInstance{}, fmt.Errorf("agent instance name is required: %w", domain.ErrInvalid)
	}
	return s.agents.RenameAgentInstance(ctx, application.RenameAgentInstanceInput{
		OrganizationID: principal.OrganizationID,
		InstanceID:     instanceID,
		Name:           name,
		ActorID:        principal.ManagementActorID(),
		Now:            s.clock.Now(),
	})
}

// humanMember is the precondition every management call shares: a
// signed-in person with a membership row. Agents, gateways, and sessions
// that are not yet attached to an organization never manage anything.
func humanMember(principal domain.Principal) bool {
	return principal.ActorType == domain.ActorHuman && principal.MembershipID != ""
}

// requireHuman gates a management call: the human precondition, then the
// grant check against the resource. Denials come back as an
// authorization.Denied that wraps domain.ErrForbidden.
func requireHuman(principal domain.Principal, permission domain.Permission, resource domain.Resource) error {
	if !humanMember(principal) {
		return domain.ErrForbidden
	}
	return authorization.Check(principal, permission, resource)
}

// requireAgentPermission resolves the agent the target row belongs to
// and checks the permission against it. The lookup is scoped by the
// principal's organization, so a missing or foreign row reads as not
// found before any grant is consulted, exactly as the write itself would.
func (s *Service) requireAgentPermission(ctx context.Context, principal domain.Principal, permission domain.Permission, lookup application.LookupAgentSlugInput) error {
	if !domain.ManagementPrincipal(principal) {
		return domain.ErrForbidden
	}
	lookup.OrganizationID = principal.OrganizationID
	agentSlug, err := s.agents.LookupAgentSlug(ctx, lookup)
	if err != nil {
		return err
	}
	return authorization.Check(principal, permission, domain.AgentResource(agentSlug))
}

func (s *Service) CreateAgentInstanceCredential(ctx context.Context, principal domain.Principal, agentID domain.ID, name string, metadata json.RawMessage) (domain.AgentInstanceCredential, error) {
	if !domain.ManagementPrincipal(principal) {
		return domain.AgentInstanceCredential{}, domain.ErrForbidden
	}
	if principal.ActorType == domain.ActorAPIKey {
		policy, err := s.identities.GetOrganizationPolicy(ctx, application.GetOrganizationPolicyInput{OrganizationID: principal.OrganizationID})
		if err != nil {
			return domain.AgentInstanceCredential{}, err
		}
		if rejected := authorization.RequireFresh(principal.Auth, policy.ApprovalReauthWindow, policy.MFAMode, s.clock.Now()); rejected != nil {
			return domain.AgentInstanceCredential{}, rejected.Err
		}
	}

	agent, err := s.agents.GetAgent(ctx, application.GetAgentInput{
		OrganizationID: principal.OrganizationID,
		AgentID:        agentID,
	})
	if err != nil {
		return domain.AgentInstanceCredential{}, err
	}
	if err := authorization.Check(principal, domain.PermissionAgentCredentialIssue, domain.AgentResource(agent.AgentSlug)); err != nil {
		return domain.AgentInstanceCredential{}, err
	}
	if agent.Status == domain.AgentArchived {
		return domain.AgentInstanceCredential{}, domain.ErrAgentArchived
	}
	if agent.Status != domain.AgentActive {
		return domain.AgentInstanceCredential{}, domain.ErrAgentInactive
	}
	// An instance of a personal agent belongs to the person registering
	// it. An organization key is nobody, so it cannot register one.
	var ownerID domain.ID
	if agent.Kind == domain.AgentPersonal {
		if !humanMember(principal) {
			return domain.AgentInstanceCredential{}, domain.ErrAgentOwnerRequired
		}
		ownerID = principal.MembershipID
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.AgentInstanceCredential{}, fmt.Errorf("agent instance name is required: %w", domain.ErrInvalid)
	}
	metadata, err = normalizeMetadata(metadata)
	if err != nil {
		return domain.AgentInstanceCredential{}, err
	}
	credential, record, err := s.newCredential()
	if err != nil {
		return domain.AgentInstanceCredential{}, err
	}
	instance, err := s.agents.CreateAgentInstanceWithCredential(ctx, application.CreateAgentInstanceWithCredentialInput{ActorType: principal.ActorType,
		OrganizationID: principal.OrganizationID,
		AgentID:        agentID,
		ActorID:        principal.ActorID,
		MembershipID:   ownerID,
		Name:           name,
		Metadata:       metadata,
		Credential:     record,
		Now:            s.clock.Now(),
	})
	if err != nil {
		return domain.AgentInstanceCredential{}, err
	}
	return domain.AgentInstanceCredential{AgentInstance: instance, Credential: credential}, nil
}

type EnrollmentCodeResult struct {
	EnrollmentCode  string    `json:"enrollment_code"`
	UserCode        string    `json:"user_code"`
	VerificationURI string    `json:"verification_uri"`
	ExpiresAt       time.Time `json:"expires_at"`
}

func (s *Service) BeginAgentEnrollment(ctx context.Context, runtime, requestedAgentSlug, instanceName string, metadata json.RawMessage) (EnrollmentCodeResult, error) {
	runtime, requestedAgentSlug, instanceName = strings.TrimSpace(runtime), strings.TrimSpace(requestedAgentSlug), strings.TrimSpace(instanceName)
	if runtime == "" || instanceName == "" {
		return EnrollmentCodeResult{}, fmt.Errorf("runtime and instance name are required: %w", domain.ErrInvalid)
	}
	var err error
	metadata, err = normalizeMetadata(metadata)
	if err != nil {
		return EnrollmentCodeResult{}, err
	}
	secret, err := randomToken(32)
	if err != nil {
		return EnrollmentCodeResult{}, err
	}
	userCode, err := readableCode()
	if err != nil {
		return EnrollmentCodeResult{}, err
	}
	now := s.clock.Now()
	enrollment := domain.AgentEnrollment{
		ID: domain.ID(uuid.NewString()), EnrollmentCodeHash: HashToken(secret), UserCode: userCode,
		Runtime: runtime, RequestedAgentSlug: requestedAgentSlug, InstanceName: instanceName, InstanceMetadata: metadata,
		Status: "pending", CreatedAt: now, ExpiresAt: now.Add(s.config.EnrollmentCodeTTL),
	}
	if err := s.agents.CreateAgentEnrollment(ctx, application.CreateAgentEnrollmentInput{Enrollment: enrollment}); err != nil {
		return EnrollmentCodeResult{}, err
	}
	return EnrollmentCodeResult{
		EnrollmentCode: "whe_" + string(enrollment.ID) + "_" + secret,
		UserCode:       userCode, VerificationURI: strings.TrimRight(s.config.PublicOrigin, "/") + "/connect/authorize",
		ExpiresAt: enrollment.ExpiresAt,
	}, nil
}

// EnrollmentPreview exposes only the context a person needs to authorize an instance.
type EnrollmentPreview struct {
	Runtime            string        `json:"runtime"`
	InstanceName       string        `json:"instance_name"`
	RequestedAgentSlug string        `json:"requested_agent_slug,omitempty"`
	ExpiresAt          time.Time     `json:"expires_at"`
	Status             string        `json:"status"`
	Agent              *domain.Agent `json:"agent,omitempty"`
}

// PreviewAgentEnrollment shows a signed-in member what a pending code
// would connect. Any member may connect their own instance of a personal
// agent; a service agent needs the credential issuance permission on it.
func (s *Service) PreviewAgentEnrollment(ctx context.Context, principal domain.Principal, userCode string) (EnrollmentPreview, error) {
	if !humanMember(principal) {
		return EnrollmentPreview{}, domain.ErrForbidden
	}
	enrollment, err := s.agents.GetAgentEnrollment(ctx, application.GetAgentEnrollmentInput{OrganizationID: principal.OrganizationID, UserCode: strings.ToUpper(strings.TrimSpace(userCode)), Now: s.clock.Now()})
	if err != nil {
		return EnrollmentPreview{}, err
	}
	result := EnrollmentPreview{Runtime: enrollment.Runtime, InstanceName: enrollment.InstanceName, RequestedAgentSlug: enrollment.RequestedAgentSlug, ExpiresAt: enrollment.ExpiresAt, Status: enrollment.Status}
	var agent domain.Agent
	if enrollment.AgentID != "" {
		agent, err = s.agents.GetAgent(ctx, application.GetAgentInput{OrganizationID: principal.OrganizationID, AgentID: enrollment.AgentID})
	} else if enrollment.RequestedAgentSlug != "" {
		agent, err = s.agents.GetAgentBySlug(ctx, application.GetAgentBySlugInput{OrganizationID: principal.OrganizationID, AgentSlug: domain.AgentSlug(enrollment.RequestedAgentSlug)})
	} else {
		return result, nil
	}
	if err != nil {
		return EnrollmentPreview{}, err
	}
	if err := requireEnrollmentAuthority(principal, agent); err != nil {
		return EnrollmentPreview{}, err
	}
	if agent.Status != domain.AgentActive {
		return EnrollmentPreview{}, domain.ErrAgentInactive
	}
	result.Agent = &agent
	return result, nil
}

// requireEnrollmentAuthority says whether the member may attach an
// instance to the agent: their own instance of a personal agent always,
// a service agent's instance only with credential issuance on it.
func requireEnrollmentAuthority(principal domain.Principal, agent domain.Agent) error {
	if agent.Kind == domain.AgentPersonal {
		return nil
	}
	return requireHuman(principal, domain.PermissionAgentCredentialIssue, domain.AgentResource(agent.AgentSlug))
}

// ListEnrollmentAgents is what the member may connect the pending
// instance to: every active personal agent, and the service agents their
// credential issuance permission names.
func (s *Service) ListEnrollmentAgents(ctx context.Context, principal domain.Principal, query string, offset, limit int) (domain.EnrollmentAgents, error) {
	if !humanMember(principal) {
		return domain.EnrollmentAgents{}, domain.ErrForbidden
	}
	return s.agents.ListEnrollmentAgents(ctx, application.ListEnrollmentAgentsInput{OrganizationID: principal.OrganizationID, Scope: authorization.Scope(principal, domain.PermissionAgentCredentialIssue), Query: strings.TrimSpace(query), Offset: offset, Limit: limit})
}

func (s *Service) AuthorizeAgentEnrollment(ctx context.Context, principal domain.Principal, userCode string, agentID domain.ID) (domain.AgentInstance, error) {
	if !humanMember(principal) {
		return domain.AgentInstance{}, domain.ErrForbidden
	}
	agent, err := s.agents.GetAgent(ctx, application.GetAgentInput{OrganizationID: principal.OrganizationID, AgentID: agentID})
	if err != nil {
		return domain.AgentInstance{}, err
	}
	if err := requireEnrollmentAuthority(principal, agent); err != nil {
		return domain.AgentInstance{}, err
	}
	return s.agents.AuthorizeAgentEnrollment(ctx, application.AuthorizeAgentEnrollmentInput{
		OrganizationID: principal.OrganizationID, UserCode: strings.ToUpper(strings.TrimSpace(userCode)), MembershipID: principal.MembershipID, AgentID: agentID, Now: s.clock.Now(),
	})
}

func (s *Service) ExchangeAgentEnrollment(ctx context.Context, enrollmentCode string) (domain.AgentEnrollmentCredential, error) {
	_, secret, err := ParseOpaqueToken(enrollmentCode, "whe")
	if err != nil {
		return domain.AgentEnrollmentCredential{}, domain.ErrUnauthenticated
	}
	credential, record, err := s.newCredential()
	if err != nil {
		return domain.AgentEnrollmentCredential{}, err
	}
	identity, err := s.agents.ExchangeAgentEnrollment(ctx, application.ExchangeAgentEnrollmentInput{EnrollmentCodeHash: HashToken(secret), Credential: record, Now: s.clock.Now()})
	if err != nil {
		return domain.AgentEnrollmentCredential{}, err
	}
	return domain.AgentEnrollmentCredential{AgentEnrollmentIdentity: identity, Credential: credential}, nil
}

func (s *Service) newCredential() (domain.IssuedCredential, application.AgentCredentialRecord, error) {
	return s.newCredentialWithTTL(s.config.CredentialTTL)
}

func (s *Service) newCredentialWithTTL(ttl time.Duration) (domain.IssuedCredential, application.AgentCredentialRecord, error) {
	credentialID := domain.ID(uuid.NewString())
	secret, err := randomToken(32)
	if err != nil {
		return domain.IssuedCredential{}, application.AgentCredentialRecord{}, err
	}
	now := s.clock.Now()
	expiresAt := now.Add(ttl)
	issued := domain.IssuedCredential{ID: credentialID, Token: "whc_" + string(credentialID) + "_" + secret, ExpiresAt: expiresAt}
	record := application.AgentCredentialRecord{ID: credentialID, SecretHash: HashToken(secret), IssuedAt: now, ExpiresAt: expiresAt}
	return issued, record, nil
}

func (s *Service) CreateAgentProvisioner(ctx context.Context, principal domain.Principal, agentID domain.ID) (domain.AgentProvisionerCredential, error) {
	if !domain.ManagementPrincipal(principal) {
		return domain.AgentProvisionerCredential{}, domain.ErrForbidden
	}
	if principal.ActorType == domain.ActorAPIKey {
		policy, err := s.identities.GetOrganizationPolicy(ctx, application.GetOrganizationPolicyInput{OrganizationID: principal.OrganizationID})
		if err != nil {
			return domain.AgentProvisionerCredential{}, err
		}
		if rejected := authorization.RequireFresh(principal.Auth, policy.ApprovalReauthWindow, policy.MFAMode, s.clock.Now()); rejected != nil {
			return domain.AgentProvisionerCredential{}, rejected.Err
		}
	}

	agent, err := s.agents.GetAgent(ctx, application.GetAgentInput{
		OrganizationID: principal.OrganizationID,
		AgentID:        agentID,
	})
	if err != nil {
		return domain.AgentProvisionerCredential{}, err
	}
	if err := authorization.Check(principal, domain.PermissionAgentCredentialIssue, domain.AgentResource(agent.AgentSlug)); err != nil {
		return domain.AgentProvisionerCredential{}, err
	}
	if agent.Status == domain.AgentArchived {
		return domain.AgentProvisionerCredential{}, domain.ErrAgentArchived
	}
	if agent.Status != domain.AgentActive {
		return domain.AgentProvisionerCredential{}, domain.ErrAgentInactive
	}
	provisionerID := domain.ID(uuid.NewString())
	secret, err := randomToken(32)
	if err != nil {
		return domain.AgentProvisionerCredential{}, err
	}
	now := s.clock.Now()
	expiresAt := now.Add(s.config.ProvisionerTTL)
	provisioner, err := s.agents.CreateAgentProvisioner(ctx, application.CreateAgentProvisionerInput{
		OrganizationID: principal.OrganizationID,
		AgentID:        agentID,
		ActorID:        principal.ManagementActorID(),
		Provisioner: application.AgentProvisionerRecord{
			ID: provisionerID, SecretHash: HashToken(secret), IssuedAt: now, ExpiresAt: expiresAt,
		},
		Now: now,
	})
	if err != nil {
		return domain.AgentProvisionerCredential{}, err
	}
	return domain.AgentProvisionerCredential{
		Provisioner: provisioner,
		Credential:  domain.IssuedCredential{ID: provisionerID, Token: "whp_" + string(provisionerID) + "_" + secret, ExpiresAt: expiresAt},
	}, nil
}

func (s *Service) RevokeAgentProvisioner(ctx context.Context, principal domain.Principal, provisionerID domain.ID) error {
	if err := s.requireAgentPermission(ctx, principal, domain.PermissionAgentCredentialRevoke, application.LookupAgentSlugInput{ProvisionerID: provisionerID}); err != nil {
		return err
	}
	return s.agents.RevokeAgentProvisioner(ctx, application.RevokeAgentProvisionerInput{ActorID: principal.ManagementActorID(), Now: s.clock.Now(),
		OrganizationID: principal.OrganizationID,
		ProvisionerID:  provisionerID,
	})
}

func (s *Service) RevokeCredential(ctx context.Context, principal domain.Principal, credentialID domain.ID) error {
	if err := s.requireAgentPermission(ctx, principal, domain.PermissionAgentCredentialRevoke, application.LookupAgentSlugInput{CredentialID: credentialID}); err != nil {
		return err
	}
	return s.agents.RevokeAgentCredential(ctx, application.RevokeAgentCredentialInput{ActorID: principal.ManagementActorID(), Now: s.clock.Now(),
		OrganizationID: principal.OrganizationID,
		CredentialID:   credentialID,
	})
}

func normalizeMetadata(metadata json.RawMessage) (json.RawMessage, error) {
	if len(metadata) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(metadata) {
		return nil, fmt.Errorf("instance metadata must be valid JSON: %w", domain.ErrInvalid)
	}
	var object map[string]any
	if err := json.Unmarshal(metadata, &object); err != nil || object == nil {
		return nil, fmt.Errorf("instance metadata must be a JSON object: %w", domain.ErrInvalid)
	}
	return metadata, nil
}

func randomToken(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func readableCode() (string, error) {
	buffer := make([]byte, 5)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	for index := range buffer {
		buffer[index] = alphabet[int(buffer[index])%len(alphabet)]
	}
	return strings.ToUpper(string(buffer[:3]) + "-" + string(buffer[3:])), nil
}

// HashToken is the SHA-256 digest stored at rest for an opaque token
// secret, so raw secrets never persist. Exported for edition modules that
// mint their own opaque tokens with MintOpaqueToken.
func HashToken(token string) []byte {
	hash := sha256.Sum256([]byte(token))
	return hash[:]
}

// MintOpaqueToken issues a `<prefix>_<id>_<secret>` opaque token and the
// SHA-256 hash of its secret, the only part persisted at rest.
func MintOpaqueToken(prefix string, id domain.ID) (string, []byte, error) {
	secret, err := randomToken(32)
	if err != nil {
		return "", nil, err
	}
	return prefix + "_" + string(id) + "_" + secret, HashToken(secret), nil
}

// ParseOpaqueToken splits `<prefix>_<id>_<secret>` into its identifier
// and secret. Only the two structural separators are split on, because
// the base64url secret can itself contain underscores. The identifier
// must be a UUID: every minted token carries one, and rejecting anything
// else here keeps malformed input from reaching a uuid-typed query, where
// it would surface as an internal error instead of a failed
// authentication. Exported for edition modules that mint their own
// opaque tokens with MintOpaqueToken.
func ParseOpaqueToken(token, expectedPrefix string) (string, string, error) {
	parts := strings.SplitN(token, "_", 3)
	if len(parts) != 3 || parts[0] != expectedPrefix || parts[2] == "" {
		return "", "", domain.ErrUnauthenticated
	}
	if _, err := uuid.Parse(parts[1]); err != nil {
		return "", "", domain.ErrUnauthenticated
	}
	return parts[1], parts[2], nil
}

func ParseDuration(value string, fallback time.Duration) time.Duration {
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err == nil {
		return duration
	}
	seconds, err := strconv.Atoi(value)
	if err == nil {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func IsNotFound(err error) bool { return errors.Is(err, domain.ErrNotFound) }

func requireManagement(principal domain.Principal, permission domain.Permission, resource domain.Resource) error {
	if !domain.ManagementPrincipal(principal) {
		return domain.ErrForbidden
	}
	return authorization.Check(principal, permission, resource)
}
