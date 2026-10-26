// Package runtime is the exported composition for withHuman
// distributions: NewServer composes the product server and RunWorker the
// worker. The open mains, the hosted SaaS mains, and the single-process
// topology (package singlebinary) all build the same product through
// them; editions differ only in the options: lifecycle hooks, extra
// routes, entitlements, channels, runtime-config entries, and whether the
// bootstrap ceremony is required. Adapter internals stay under internal/;
// this package is the one sanctioned way to compose them from outside.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/authn"
	"github.com/withHumanAI/withHuman/blockruntime"
	"github.com/withHumanAI/withHuman/config"
	"github.com/withHumanAI/withHuman/db"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/entitlements"
	"github.com/withHumanAI/withHuman/internal/aapdelivery"
	"github.com/withHumanAI/withHuman/internal/approvalpipeline"
	"github.com/withHumanAI/withHuman/internal/approvals"
	"github.com/withHumanAI/withHuman/internal/authorization"
	"github.com/withHumanAI/withHuman/internal/contacts"
	"github.com/withHumanAI/withHuman/internal/discord"
	"github.com/withHumanAI/withHuman/internal/discordconnect"
	"github.com/withHumanAI/withHuman/internal/heartbeat"
	"github.com/withHumanAI/withHuman/internal/httpapi"
	"github.com/withHumanAI/withHuman/internal/mcpoauth"
	"github.com/withHumanAI/withHuman/internal/members"
	"github.com/withHumanAI/withHuman/internal/onboarding"
	"github.com/withHumanAI/withHuman/internal/orchestration"
	"github.com/withHumanAI/withHuman/internal/orgsettings"
	"github.com/withHumanAI/withHuman/internal/postgres"
	"github.com/withHumanAI/withHuman/internal/reviewrouting"
	"github.com/withHumanAI/withHuman/internal/secrets"
	"github.com/withHumanAI/withHuman/internal/slack"
	"github.com/withHumanAI/withHuman/internal/slackconnect"
	"github.com/withHumanAI/withHuman/internal/teamescalation"
	"github.com/withHumanAI/withHuman/internal/teams"
	"github.com/withHumanAI/withHuman/internal/toolcatalog"
	"github.com/withHumanAI/withHuman/internal/toolpresentations"
	"github.com/withHumanAI/withHuman/internal/webhookdelivery"
	"github.com/withHumanAI/withHuman/internal/webhookendpoints"
	"github.com/withHumanAI/withHuman/loggermdc"
	"github.com/withHumanAI/withHuman/mcptools"
	"github.com/withHumanAI/withHuman/notifications"
	"github.com/withHumanAI/withHuman/telemetry"
	"github.com/withHumanAI/withHuman/tenants"
	"go.temporal.io/sdk/client"
	temporallog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

type ServerOptions struct {
	Logger *slog.Logger
	// Version is reported by the version endpoint.
	Version string
	// Hooks observes tenant lifecycle transitions; nil means the no-op
	// open adapter.
	Hooks tenants.TenantHooks
	// Entitlements answers plan questions: capabilities, limits, and usage
	// metering. Nil means the unlimited open adapter; the hosted edition
	// supplies its ledger-backed adapter.
	Entitlements application.Entitlements
	// RequireBootstrapSecret makes startup fail without
	// WITHHUMAN_BOOTSTRAP_SECRET. The open edition requires it because
	// bootstrap is its only organization-creation path.
	RequireBootstrapSecret bool
	// DisableBootstrap blanks any configured bootstrap secret so the
	// ceremony refuses unconditionally. The hosted edition sets it:
	// organizations come into existence only through signup, even when
	// the deployment environment carries a secret.
	DisableBootstrap bool
	// Config is the deployment configuration to compose with. Nil loads
	// it from the environment; the single-process topology supplies one
	// it derived from its embedded services.
	Config *config.Config
	// Frontend is the built product frontend to serve: core's own bundle
	// (webapp.Assets) in the open edition, the hosted edition's overlay
	// build in the hosted one. It is required; a server without one would
	// answer every page with not found.
	Frontend fs.FS
	// RuntimeConfig is what the edition adds to /api/runtime-config for
	// the frontend it ships (the hosted edition's signup_enabled and
	// billing_enabled). Core's own entries cannot be shadowed; a
	// collision fails composition.
	RuntimeConfig map[string]any
	// NotificationChannels are additional delivery channels the edition
	// registers, such as a hosted email provider or mobile push. They join
	// the open channels rather than replacing them.
	NotificationChannels []application.NotificationChannel
	// APIKeyScopingEnabled enables edition-entitled API key permission scoping.
	APIKeyScopingEnabled bool
	// CustomPermissionsEnabled offers custom permission policies beside the
	// predefined roles: a member's or team's own policies and invitations
	// that carry them. The open edition leaves it off and offers the
	// predefined roles alone; the hosted edition turns it on.
	CustomPermissionsEnabled bool
	// RegisterRoutes lets an edition add routes to the composed router
	// before the server starts. Additive routes only; the shared HTTP
	// contract is never forked.
	RegisterRoutes func(*Server)
	// Permissions are edition-owned keys (the hosted edition's billing
	// and audit permissions) added to the registry before anything reads
	// it, so the built-in roles and the OpenAPI conformance checks see
	// them.
	Permissions []domain.PermissionDefinition
	// AuditSink receives every audit event core's writes build, inside
	// the transaction that commits the action it describes. Nil discards
	// them: the open edition keeps no audit log. The hosted edition passes
	// the sink that writes its own table and serves the log from it.
	AuditSink db.AuditSink
	// PurgeHooks remove the edition's own rows for an organization inside
	// core's purge transaction.
	PurgeHooks []db.PurgeHook
	// ApprovalBlocks are the block kinds the edition adds to the pipeline
	// (the hosted AI judge); their grammar must already be registered
	// through approvalblock.RegisterKind. The worker composes with the
	// same list so both compile every revision the same way.
	ApprovalBlocks []blockruntime.Runtime
	// RequestSummaries reads the one-line summaries the edition writes for
	// requests that reach a person (the hosted summarizer); product reads
	// carry them as summary. Nil carries none.
	RequestSummaries application.RequestSummaryReader
	// AgentMCPServers are the MCP servers the edition offers every enrolled
	// agent (the hosted gateway), reported by /api/runtime-config as
	// agent_mcp_servers so the withhuman CLI registers them with each
	// runtime it installs. The open edition offers none.
	AgentMCPServers []domain.AgentMCPServer
	// MCPTools are the edition's own tools on the management MCP server
	// (/api/mcp/v1), for the routes it adds (the hosted gateway servers
	// and audit log). Each dispatches into the composed router with the
	// caller's credential exactly as core's tools do, so a tool may only
	// reach a route that admits the caller's key.
	MCPTools []mcptools.Registration
	// UpdateChecks offers administrators the Version page, for an edition
	// whose worker sends the installation heartbeat (WorkerOptions.HeartbeatURL).
	// It stays off when the operator turned telemetry off.
	UpdateChecks bool
}

// Server is a composed withHuman product server. The exported fields are
// the surfaces an edition's own routes may build on.
type Server struct {
	Config  config.Config
	Router  *gin.Engine
	Auth    *authn.Service
	Tenants *tenants.Service
	Clock   application.Clock
	Logger  *slog.Logger
	// Notifications is the channel registry every edition shares, so
	// edition-owned identity mail (hosted signup verification) goes out
	// through the same transports as invitations rather than a parallel
	// email path.
	Notifications application.NotificationDispatcher
	// Roles is the role repository, exported as the seam the hosted
	// edition's custom role authoring will use when it is built; today
	// the rows are the built-ins and the private snapshots policies make.
	Roles application.RoleRepository
	// PermissionPolicies reads and mutates permission policies under the
	// same validation, live authorization and audit core's own member and
	// team policy routes use; the hosted edition's organization-key
	// policy routes run on it.
	PermissionPolicies application.PermissionPolicyRepository
	// Requests reads approval requests under the caller's authorization
	// and the organization's visibility policy, for an edition route that
	// hangs its own read off a request, such as the hosted timeline.
	Requests RequestReader
	// Contacts is the contacts service's seam for an edition's contact
	// kinds: the adapters behind the kinds it registered, and the
	// vouched, value-addressed operations its own routes and webhooks
	// need (the hosted phone apps and WhatsApp confirmations).
	Contacts ContactService
	// Directory is core's directory-managed membership: the connection
	// records an edition's directory sync keeps and the transactional
	// apply of its normalized commands (the hosted SCIM sync feeds it from
	// its own event inbox and workflows).
	Directory application.DirectoryRepository
	// Members is the member service's seam for an edition's own routes:
	// turning a permission policy into the private role it grants, under
	// the actor's authority (the hosted verified domains' default role).
	Members MemberService
	// Temporal is the workflow client, for an edition's routes that start
	// their own workflows (the hosted directory sync's event apply).
	Temporal client.Client

	store          *postgres.Store
	toolCatalog    *toolcatalog.Service
	temporalClient client.Client
	drain          *httpapi.Drain
	stopObserving  func()
}

// RequestReader is the product read of one approval request: the
// principal must hold request.read on it and, under routed_only
// visibility, be among its readers; anything else is not found or
// forbidden exactly as the request routes answer.
type RequestReader interface {
	Get(ctx context.Context, principal domain.Principal, requestID domain.ID) (domain.ApprovalRequest, error)
}

// MemberService is what an edition's routes need from core's member
// service: BuildPolicyAssignments resolves permission policies to the
// roles that grant them, minting private roles as needed, after the
// grant kernel checked the actor may hand them out; unattended marks a
// grant nobody will be present to confirm when it takes effect.
type MemberService interface {
	BuildPolicyAssignments(ctx context.Context, principal domain.Principal, policies []domain.PermissionPolicySpec, unattended bool) ([]domain.AssignmentSpec, error)
}

// ContactService is what an edition's contact kinds need from core's
// contacts service. RegisterKind installs the adapter a registered kind
// is added and proved through; AddVouched records a person-wide method
// something other than a code vouched for (an installed app, by the
// session on the phone); RemoveByValue removes a person's method of a
// kind by its value; VerifyByValue answers a challenge from the address
// side (a message arriving from the number with the code).
type ContactService interface {
	RegisterKind(adapter application.ContactKindAdapter)
	AddVouched(ctx context.Context, principal domain.Principal, kind domain.ContactMethodKind, value, label string) (domain.ContactMethod, error)
	RemoveByValue(ctx context.Context, principal domain.Principal, kind domain.ContactMethodKind, value string) error
	VerifyByValue(ctx context.Context, kind domain.ContactMethodKind, value, code string) (domain.ContactMethod, error)
}

func NewServer(ctx context.Context, options ServerOptions) (*Server, error) {
	logger := loggermdc.Wrap(options.Logger)
	if err := domain.RegisterPermissions(options.Permissions...); err != nil {
		return nil, err
	}
	cfg, err := loadConfig(options.Config)
	if err != nil {
		return nil, err
	}
	extras, err := httpapi.RuntimeConfigExtras(options.RuntimeConfig)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateAgentMCPServers(options.AgentMCPServers); err != nil {
		return nil, err
	}
	if options.Frontend == nil {
		return nil, errors.New("a built frontend is required: pass ServerOptions.Frontend")
	}
	if options.RequireBootstrapSecret && cfg.BootstrapSecret == "" {
		return nil, errors.New("WITHHUMAN_BOOTSTRAP_SECRET is required")
	}
	if options.DisableBootstrap {
		cfg.BootstrapSecret = ""
	}
	store, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if options.AuditSink != nil {
		store.WithAuditSink(options.AuditSink)
	}
	store.WithPurgeHooks(options.PurgeHooks...)
	clock := application.RealClock{}
	// Built-in roles are code: reconcile the rows against the registry
	// (edition keys included) before anything reads them.
	if err := reconcileBuiltInRoles(ctx, store, clock, logger); err != nil {
		store.Close()
		return nil, err
	}
	temporalClient, err := client.NewLazyClient(client.Options{HostPort: cfg.TemporalAddress, Namespace: cfg.TemporalNamespace,
		ContextPropagators: []workflow.ContextPropagator{orchestration.NewMDCPropagator()},
		Logger:             temporallog.NewStructuredLogger(logger), MetricsHandler: telemetry.TemporalMetrics(logger)})
	if err != nil {
		store.Close()
		return nil, err
	}
	stopObservingPool, err := telemetry.ObservePool("server", store.Pool())
	if err != nil {
		temporalClient.Close()
		store.Close()
		return nil, err
	}
	// The pending backlog is read by the server alone: every replica
	// reports the same deployment-wide value, and a worker may not run
	// beside it.
	stopObservingPending, err := telemetry.ObservePendingApprovals(store.PendingApprovals)
	if err != nil {
		stopObservingPool()
		temporalClient.Close()
		store.Close()
		return nil, err
	}
	stopObserving := func() {
		stopObservingPending()
		stopObservingPool()
	}
	orchestrator := orchestration.NewTemporal(temporalClient, cfg.TemporalTaskQueue)
	var plan application.Entitlements = options.Entitlements
	if plan == nil {
		plan = entitlements.NewUnlimited()
	}
	tenantService := tenants.NewService(store, orchestrator, options.Hooks, clock, logger, tenants.Config{})
	authService := authn.NewService(store, store, store, tenantService, plan, authn.NewArgon2idPasswordHasher(), clock, authn.ServiceConfig{
		BootstrapSecret: cfg.BootstrapSecret, PublicOrigin: cfg.PublicOrigin,
		SessionTTL: cfg.SessionTTL, NativeSessionTTL: cfg.NativeSessionTTL, CredentialTTL: cfg.CredentialTTL,
		ProvisionerTTL: cfg.ProvisionerTTL, EnrollmentCodeTTL: cfg.EnrollmentCodeTTL,
		APIKeyScopingEnabled: options.APIKeyScopingEnabled, APIKeyMaxTTL: cfg.APIKeyMaxTTL,
	})
	authService.RegisterRoles(store)
	authService.RegisterAPIKeys(store)
	// Slack is optional. When configured, the store gains the sealer that
	// keeps bot tokens unreadable at rest; when not, the service still
	// exists so the page can say the surface is absent.
	var slackConnector application.SlackConnector
	var slackMessenger application.SlackMessenger
	var sealer *secrets.Sealer
	if cfg.SecretsKey != "" {
		var err error
		if sealer, err = secrets.NewSealer(cfg.SecretsKey); err != nil {
			return nil, err
		}
		store.WithSealer(sealer)
	}
	if cfg.SlackConfigured() {
		slackClient := slack.NewClient(slack.DefaultBaseURL, cfg.SlackClientID, cfg.SlackClientSecret)
		slackConnector, slackMessenger = slackClient, slackClient
	}
	slackService := slackconnect.NewService(slackConnector, slackMessenger, store, store, sealer, clock, logger, slackconnect.Config{PublicOrigin: cfg.PublicOrigin, AppID: cfg.SlackAppID, SigningSecret: cfg.SlackSigningSecret}).WithEventDispatcher(orchestrator)
	channels := append([]application.NotificationChannel{}, options.NotificationChannels...)
	if slackMessenger != nil {
		channels = append(channels, notifications.NewSlackChannel(store, slackMessenger, cfg.PublicOrigin).WithEventDispatcher(orchestrator))
	}
	// Discord is optional too. Its bot token is deployment configuration
	// rather than a per-install credential, so nothing here needs sealing.
	var discordConnector application.DiscordConnector
	var discordMessenger application.DiscordMessenger
	if cfg.DiscordConfigured() {
		discordClient := discord.NewClient(discord.DefaultBaseURL, cfg.DiscordClientID, cfg.DiscordClientSecret, cfg.DiscordBotToken)
		discordConnector, discordMessenger = discordClient, discordClient
	}
	discordService := discordconnect.NewService(discordConnector, discordMessenger, store, store, clock, logger, discordconnect.Config{PublicOrigin: cfg.PublicOrigin})
	if discordMessenger != nil {
		channels = append(channels, notifications.NewDiscordChannel(store, discordMessenger, cfg.PublicOrigin))
	}
	notifier, err := notifications.Build(cfg, logger, channels...)
	if err != nil {
		return nil, err
	}
	authService.RegisterNotifications(notifier)
	memberService := members.NewService(store, store, store, store, notifier, plan, clock, logger, members.Config{PublicOrigin: cfg.PublicOrigin, ExposeInviteLinks: !cfg.SMTPConfigured()})
	teamService := teams.NewService(store, clock)
	contactService := contacts.NewService(store, store, store, notifier, clock, logger, contacts.Config{})
	// The catalogue's external tool source is the edition's to register
	// (RegisterToolSource), once its own services exist.
	catalogService := toolcatalog.NewService(store, nil, clock, logger)
	blockRegistryOptions := []approvalpipeline.BlockRegistryOption{
		approvalpipeline.WithEntitlements(plan), approvalpipeline.WithClock(clock),
		approvalpipeline.WithRuntimes(options.ApprovalBlocks...),
	}
	// Webhook endpoints need the secrets key to seal signing secrets; the
	// egress policy and the service exist either way so the page can say
	// the surface is absent.
	webhookPolicy := webhookdelivery.Policy{AllowInsecure: cfg.WebhookAllowInsecure, AllowPrivateNetworks: cfg.WebhookAllowPrivateNetworks}
	var webhookDeliverer webhookendpoints.Deliverer
	if cfg.WebhookConfigured() {
		client := webhookdelivery.NewClient(webhookPolicy)
		webhookDeliverer = client
		blockRegistryOptions = append(blockRegistryOptions, approvalpipeline.WithWebhooks(approvalpipeline.WebhookRuntime{Endpoints: store, Deliverer: client}))
		// AAP asynchronous delivery shares the egress policy and the
		// sealer: a provisioner's receiver is verified and its secret
		// sealed before the instance is minted.
		authService.RegisterInstanceDeliveries(aapdelivery.NewVerifier(client, webhookPolicy, sealer, clock, logger))
		logger.Info("webhook blocks and AAP asynchronous delivery enabled")
	} else {
		logger.Info("webhook blocks and AAP asynchronous delivery disabled: WITHHUMAN_SECRETS_KEY is not set")
	}
	webhookService := webhookendpoints.NewService(store, sealer, webhookPolicy, webhookDeliverer, clock, logger)
	compiledPipelines := approvalpipeline.NewCompiledPipelineState(store, approvalpipeline.NewBlockRegistry(blockRegistryOptions...))
	if err := compiledPipelines.Initialize(ctx); err != nil {
		logger.Error("approval pipeline initialization failed; server readiness is blocked")
	}
	pipelineExecution := approvalpipeline.NewExecutionService(store, store, compiledPipelines, clock)
	routingEvaluation := reviewrouting.NewEvaluationService(store, clock, logger)
	approvalService := approvals.NewService(store, store, store, pipelineExecution, routingEvaluation, orchestrator, plan, clock)
	pipelineService := approvalpipeline.NewManagementService(store, compiledPipelines, clock, approvalpipeline.WithEscalationPathResolver(store), approvalpipeline.WithPipelineKeyResolver(store))
	routingService := reviewrouting.NewManagementService(store, store, clock)
	teamEscalationService := teamescalation.NewService(store, store, clock)
	authService.RegisterOnboarding(store)
	orgSettingsService := orgsettings.NewService(store, clock, logger)
	toolPresentationService := toolpresentations.NewService(store, clock, logger)
	onboardingService := onboarding.NewService(store, store, store, store, store, authService, pipelineService, routingService, clock, logger, onboarding.Config{
		PublicOrigin: cfg.PublicOrigin, CLIDownloadURL: cfg.CLIDownloadURL,
		SessionTTL: cfg.OnboardingSessionTTL, SetupCodeTTL: cfg.EnrollmentCodeTTL,
	})
	mcpOAuthService := mcpoauth.NewService(store, authService, store, clock, mcpoauth.Config{
		PublicOrigin: cfg.PublicOrigin, MCPPath: httpapi.MCPPath, AuthorizePath: httpapi.MCPAuthorizePath,
	})
	drain := httpapi.NewDrain()
	router := httpapi.NewRouter(httpapi.Dependencies{
		Auth: authService, Approvals: approvalService, Members: memberService, Teams: teamService, Contacts: contactService, Slack: slackService, Discord: discordService,
		ToolCatalog: catalogService, WebhookEndpoints: webhookService, Onboarding: onboardingService, OrgSettings: orgSettingsService,
		Installation: heartbeat.NewStatusReader(store, options.Version), ToolPresentations: toolPresentationService,
		RequestSummaries: options.RequestSummaries,
		Identities:       store, Roles: store, ApprovalRepo: store,
		Orchestrator: orchestrator,
		Pipelines:    pipelineService, CompiledPipelines: compiledPipelines,
		Routing: routingService, RoutingSnapshots: store, TeamEscalation: teamEscalationService, MCPOAuth: mcpOAuthService, MCPTools: options.MCPTools,
		Frontend: options.Frontend, Drain: drain, Logger: logger,
		Config: httpapi.Config{
			PublicOrigin: cfg.PublicOrigin, CookieSecure: cookieSecure(cfg.PublicOrigin), Version: options.Version,
			BootstrapEnabled: cfg.BootstrapSecret != "", Extras: extras, CustomPermissions: options.CustomPermissionsEnabled,
			CLIDownloadURL: cfg.CLIDownloadURL, AgentMCPServers: options.AgentMCPServers,
			UpdateChecks: options.UpdateChecks && cfg.Telemetry,
		},
	})
	if len(cfg.TrustedProxyCIDRs) == 0 {
		_ = router.SetTrustedProxies(nil)
	} else if err := router.SetTrustedProxies(cfg.TrustedProxyCIDRs); err != nil {
		stopObserving()
		temporalClient.Close()
		store.Close()
		return nil, err
	}
	server := &Server{
		toolCatalog: catalogService,
		Config:      cfg, Router: router, Auth: authService, Tenants: tenantService,
		Clock: clock, Logger: logger, Notifications: notifier, Roles: store, PermissionPolicies: store, Requests: approvalService, Contacts: contactService, Directory: store, Members: memberService, Temporal: temporalClient,
		store: store, temporalClient: temporalClient, drain: drain, stopObserving: stopObserving,
	}
	if options.RegisterRoutes != nil {
		options.RegisterRoutes(server)
	}
	return server, nil
}

// reconcileBuiltInRoles upserts the built-in role rows from their code
// definitions and strips every role of keys the registry no longer
// knows. Failure is fatal: a server whose owner role lags the registry
// would deny owners the edition's own permissions.
func reconcileBuiltInRoles(ctx context.Context, store *postgres.Store, clock application.Clock, logger *slog.Logger) error {
	report, err := store.ReconcileBuiltInRoles(ctx, application.ReconcileBuiltInRolesInput{
		Roles: domain.BuiltInRoles(), Permissions: domain.PermissionKeys(), Now: clock.Now(),
	})
	if err != nil {
		return fmt.Errorf("reconcile built-in roles: %w", err)
	}
	logger.Info(fmt.Sprintf("built-in roles reconciled - %d seeded, %d updated, %d permission keys removed", report.Seeded, report.Updated, len(report.StrippedKeys)), "seeded", report.Seeded, "updated", report.Updated, "stripped_keys", report.StrippedKeys)
	return nil
}

// SetSessionCookie writes the withHuman session cookie with the same
// attributes the core handlers use, for edition routes that end in a
// session.
func (s *Server) SetSessionCookie(c *gin.Context, token string, expiresAt time.Time) {
	http.SetCookie(c.Writer, &http.Cookie{Name: authn.SessionCookieName, Value: token, Path: "/", HttpOnly: true,
		Secure: s.CookieSecure(), SameSite: http.SameSiteStrictMode, Expires: expiresAt})
}

// PrincipalAuth is core's /api/v1 middleware, for edition routes that
// serve signed-in humans and personal API keys alike. Read the resolved
// principal with Principal.
func (s *Server) PrincipalAuth() gin.HandlerFunc { return httpapi.PrincipalAuth(s.Auth, s.Logger) }

// RegisterToolSource installs the edition's external tool source on the
// tool catalogue: the owners it refreshes on read and the agents that
// see them. The hosted gateway is the one that exists.
func (s *Server) RegisterToolSource(source application.ToolSource) {
	s.toolCatalog.RegisterSource(source)
}

// PrincipalAuthAdmittingOrganizationKeys is PrincipalAuth for an edition
// management route that has decided to admit organization API keys too,
// the bar core's own organizationKeyRoutes hold: the permission the route
// requires names HolderOrganizationAPIKey and the route is a read or a
// reviewed write. The hosted audit log is one.
func (s *Server) PrincipalAuthAdmittingOrganizationKeys() gin.HandlerFunc {
	return httpapi.PrincipalAuthAdmittingOrganizationKeys(s.Auth, s.Logger)
}

// SameOrigin is core's cross-site write guard, for edition routes that
// change state on behalf of a browser session.
func (s *Server) SameOrigin() gin.HandlerFunc {
	return httpapi.SameOrigin(s.Config.PublicOrigin, s.Logger)
}

// WriteError answers with core's error envelope and status mapping.
func (s *Server) WriteError(c *gin.Context, err error) { httpapi.WriteError(c, s.Logger, err) }

// Requires is the router's coarse gate for edition routes: the principal
// must be a human holding the permission somewhere. The service behind the
// route still checks it against the resource.
func (s *Server) Requires(permission domain.Permission) gin.HandlerFunc {
	return httpapi.Requires(s.Logger, permission)
}

// Check answers whether the principal may exercise the permission on the
// resource, for edition services that own their own rules.
func Check(principal domain.Principal, permission domain.Permission, resource domain.Resource) error {
	return authorization.Check(principal, permission, resource)
}

// HoldsAnywhere reports whether the principal holds the permission at any
// scope, the navigation-gate question.
func HoldsAnywhere(principal domain.Principal, permission domain.Permission) bool {
	return authorization.HoldsAnywhere(principal, permission)
}

// ResolvePrincipal resolves the request's credential again, for an
// edition's event stream to call on every tick: the stream ends once the
// session expires or is revoked, and reads with live grants.
func (s *Server) ResolvePrincipal(c *gin.Context) (domain.Principal, error) {
	return httpapi.ResolvePrincipal(c, s.Auth)
}

// StreamsEnded closes once shutdown starts. An edition's event stream
// returns on it, so the wait for in-flight requests is bounded by real
// work and the browser reconnects to another replica.
func (s *Server) StreamsEnded() <-chan struct{} { return s.drain.StreamsEnded() }

// Principal returns the principal PrincipalAuth resolved for the request.
func Principal(c *gin.Context) domain.Principal { return httpapi.Principal(c) }

// CookieSecure reports whether cookies set by this deployment carry the
// Secure attribute, so edition routes that set their own cookies match
// the session cookie.
func (s *Server) CookieSecure() bool { return cookieSecure(s.Config.PublicOrigin) }

// Run serves HTTP on the configured address until the context is
// canceled, then shuts down gracefully and releases the store and
// Temporal client.
func (s *Server) Run(ctx context.Context) error {
	listener, err := s.Listen()
	if err != nil {
		s.Close()
		return err
	}
	return s.Serve(ctx, listener)
}

// Listen binds the configured HTTP address and hands back the listener,
// for a composition that needs the resolved port before serving: an
// ephemeral port in tests, or an embedded gateway that must know the
// provider's URL. Serve takes it from here.
func (s *Server) Listen() (net.Listener, error) {
	return net.Listen("tcp", s.Config.HTTPAddress)
}

// Serve serves HTTP on the listener until the context is canceled, then
// drains and releases the store and Temporal client; see serveUntilDrained.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	defer s.Close()
	server := &http.Server{Handler: s.Router, ReadHeaderTimeout: 5 * time.Second}
	address := listener.Addr().String()
	s.Logger.Info("withHuman server listening - "+address, "address", address)
	return serveUntilDrained(ctx, server, listener, s.drain, s.Config.ShutdownDrainDelay, s.Config.ShutdownTimeout, s.Logger)
}

// serveUntilDrained serves until the context is canceled, then keeps
// serving for drainDelay with readiness failing, so load balancers stop
// routing here before the listener closes, and finally gives in-flight
// requests up to shutdownTimeout to finish. It returns only once they
// have: http.Server.Serve returns the moment Shutdown begins, and a
// caller that exits then cuts every request off and leaves a balancer
// routing to an address that no longer answers.
func serveUntilDrained(ctx context.Context, server *http.Server, listener net.Listener, drain *httpapi.Drain, drainDelay, shutdownTimeout time.Duration, logger *slog.Logger) error {
	server.RegisterOnShutdown(drain.EndStreams)
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	drain.Begin()
	logger.Info("withHuman server draining - "+drainDelay.String(), "drain_delay", drainDelay.String(), "shutdown_timeout", shutdownTimeout.String())
	select {
	case err := <-served:
		return err
	case <-time.After(drainDelay):
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		// What is still running at the deadline is cut off rather than
		// holding the process past its grace period.
		logger.Warn("withHuman server shutdown timed out - closing open connections", "error", err)
		_ = server.Close()
	}
	return nil
}

// Close releases the store and Temporal client without serving, for a
// composition that fails between NewServer and Serve.
func (s *Server) Close() {
	s.stopObserving()
	s.temporalClient.Close()
	s.store.Close()
}

func cookieSecure(publicOrigin string) bool {
	return len(publicOrigin) >= 8 && publicOrigin[:8] == "https://"
}
