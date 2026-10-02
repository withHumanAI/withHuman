package httpapi

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/approvalblock"
	"github.com/withHumanAI/withHuman/authn"
	"github.com/withHumanAI/withHuman/blockruntime"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/approvalpipeline"
	"github.com/withHumanAI/withHuman/internal/approvals"
	"github.com/withHumanAI/withHuman/internal/authorization"
	"github.com/withHumanAI/withHuman/internal/contacts"
	"github.com/withHumanAI/withHuman/internal/discordconnect"
	"github.com/withHumanAI/withHuman/internal/mcpoauth"
	"github.com/withHumanAI/withHuman/internal/members"
	"github.com/withHumanAI/withHuman/internal/onboarding"
	"github.com/withHumanAI/withHuman/internal/orgsettings"
	"github.com/withHumanAI/withHuman/internal/reviewrouting"
	"github.com/withHumanAI/withHuman/internal/slackconnect"
	"github.com/withHumanAI/withHuman/internal/teamescalation"
	"github.com/withHumanAI/withHuman/internal/teams"
	"github.com/withHumanAI/withHuman/internal/toolcatalog"
	"github.com/withHumanAI/withHuman/internal/toolpresentations"
	"github.com/withHumanAI/withHuman/internal/webhookendpoints"
	"github.com/withHumanAI/withHuman/loggermdc"
)

//go:embed openapi.yaml
var openAPISpec []byte

type Config struct {
	PublicOrigin string
	CookieSecure bool
	Version      string
	// BootstrapEnabled says the first-owner ceremony is available, so the
	// shared frontend can offer that door without probing routes.
	BootstrapEnabled bool
	// CustomPermissions admits permission policy writes: a member's or
	// team's own policies and invitations that carry them. Without it the
	// edition offers predefined roles alone (the open edition); reported
	// as custom_permissions_enabled.
	CustomPermissions bool
	// Extras are the edition's own runtime-config entries, merged into
	// the response after core's; RuntimeConfigExtras refuses a key core
	// reports itself.
	Extras map[string]any

	// CLIDownloadURL is where the onboarding page sends people for the
	// withhuman CLI (install.sh and the per-version binaries).
	CLIDownloadURL string
	// AgentMCPServers are the MCP servers this deployment offers every
	// enrolled agent (the hosted gateway); the CLI registers them with each
	// runtime it installs. Reported as agent_mcp_servers, empty in the
	// open edition.
	AgentMCPServers []domain.AgentMCPServer
}

type pipelineReadiness interface {
	Readiness() error
	// Grammar is what the edition's block kinds add to the pipeline_blocks
	// reference the management MCP server serves.
	Grammar() string
}

type Dependencies struct {
	ToolCatalog      *toolcatalog.Service
	Auth             *authn.Service
	Approvals        *approvals.Service
	Members          *members.Service
	Teams            *teams.Service
	Contacts         *contacts.Service
	Slack            *slackconnect.Service
	Discord          *discordconnect.Service
	WebhookEndpoints *webhookendpoints.Service
	Onboarding       *onboarding.Service
	// OrgSettings reads and changes the organization policy.
	OrgSettings *orgsettings.Service
	// ToolPresentations resolves how each request's arguments are shown
	// and manages the organization's own presentations.
	ToolPresentations *toolpresentations.Service
	Identities        application.IdentityRepository
	// Roles resolves role rows and assignments for the views that show
	// them: /me, invitation grants, and the domain's JIT default role.
	Roles             application.RoleRepository
	ApprovalRepo      application.ApprovalRepository
	Orchestrator      application.LifecycleOrchestrator
	Pipelines         *approvalpipeline.ManagementService
	CompiledPipelines pipelineReadiness
	Routing           *reviewrouting.ManagementService
	RoutingSnapshots  routingSnapshotReader
	// TeamEscalation manages the escalation policy a team runs when a
	// path level targets it.
	TeamEscalation *teamescalation.Service
	// MCPOAuth is the authorization server in front of the management
	// MCP endpoint; nil leaves the OAuth routes answering not found.
	MCPOAuth *mcpoauth.Service
	// Frontend is the built product frontend, served at every path that
	// is not the API's; nil serves the API alone, which is what tests
	// compose.
	Frontend fs.FS
	Logger   *slog.Logger
	Config   Config
}

type API struct {
	frontend           fs.FS
	toolCatalog        *toolcatalog.Service
	auth               *authn.Service
	approvals          *approvals.Service
	members            *members.Service
	teams              *teams.Service
	contacts           *contacts.Service
	slack              *slackconnect.Service
	discord            *discordconnect.Service
	webhooks           *webhookendpoints.Service
	onboarding         *onboarding.Service
	orgSettings        *orgsettings.Service
	toolPresentations  *toolpresentations.Service
	roles              application.RoleRepository
	permissionPolicies application.PermissionPolicyRepository
	identities         application.IdentityRepository
	approvalRepo       application.ApprovalRepository
	orchestrator       application.LifecycleOrchestrator
	pipelines          *approvalpipeline.ManagementService
	compiledPipelines  pipelineReadiness
	routing            *reviewrouting.ManagementService
	routingSnapshots   routingSnapshotReader
	teamEscalation     *teamescalation.Service
	mcpOAuth           *mcpoauth.Service
	logger             *slog.Logger
	config             Config
	// access records every registered route with its label; see access.go.
	access       map[routeKey]string
	requests     atomic.Uint64
	errors       atomic.Uint64
	durationNano atomic.Uint64
	// engine is the composed router; the MCP tools dispatch product API
	// calls to it in process so they run the same path a client would.
	engine *gin.Engine
	mcpServerState
}

const principalKey = "withhuman.principal"

func NewRouter(dependencies Dependencies) *gin.Engine {
	return newAPI(dependencies).router()
}

func newAPI(dependencies Dependencies) *API {
	policies, _ := dependencies.Roles.(application.PermissionPolicyRepository)
	return &API{
		permissionPolicies: policies, auth: dependencies.Auth, approvals: dependencies.Approvals, members: dependencies.Members,
		contacts: dependencies.Contacts, slack: dependencies.Slack, discord: dependencies.Discord,
		teams:    dependencies.Teams,
		webhooks: dependencies.WebhookEndpoints, onboarding: dependencies.Onboarding, orgSettings: dependencies.OrgSettings,
		toolPresentations: dependencies.ToolPresentations, toolCatalog: dependencies.ToolCatalog,
		identities: dependencies.Identities, roles: dependencies.Roles, approvalRepo: dependencies.ApprovalRepo,
		orchestrator:      dependencies.Orchestrator,
		pipelines:         dependencies.Pipelines,
		compiledPipelines: dependencies.CompiledPipelines, routing: dependencies.Routing,
		routingSnapshots: dependencies.RoutingSnapshots, teamEscalation: dependencies.TeamEscalation,
		mcpOAuth: dependencies.MCPOAuth, frontend: dependencies.Frontend,
		logger: loggermdc.Wrap(dependencies.Logger), config: dependencies.Config,
	}
}

// router registers every route through handle, so each one carries the
// access label its OpenAPI operation declares.
func (a *API) router() *gin.Engine {
	router := gin.New()
	a.engine = router
	// The setup assistant runs the management tools in process; the
	// catalog is this layer's, so it is handed over here.
	if a.onboarding != nil {
		a.onboarding.RegisterAssistantGrammar(mcpAssistantGrammar{api: a})
	}
	router.Use(a.observability(), a.recovery())
	root := &router.RouterGroup
	a.handle(root, http.MethodGet, "/livez", AccessPublic, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "live"}) })
	a.handle(root, http.MethodGet, "/readyz", AccessPublic, a.readiness)
	a.handle(root, http.MethodGet, "/metrics", AccessPublic, a.metrics)
	a.handle(root, http.MethodGet, "/api/runtime-config", AccessPublic, a.runtimeConfig)
	a.handle(root, http.MethodGet, "/api/openapi.yaml", AccessPublic, func(c *gin.Context) { c.Data(http.StatusOK, "application/yaml", openAPISpec) })
	// /api/mcp/v1 is the management MCP surface for AI agents holding an
	// API key; its tools re-enter /api/v1 in process.
	a.installMCPRoutes(root)

	// Everything under /auth is public: each route authenticates itself
	// (a secret, a token in the body, a provider signature) or is the
	// ceremony that produces the session.
	auth := router.Group("/auth")
	a.handle(auth, http.MethodPost, "/bootstrap", AccessPublic, a.bootstrap)
	// Email-first login: methods discovery, the password step, then the
	// organization selection for multi-organization users.
	a.handle(auth, http.MethodPost, "/login/methods", AccessPublic, a.loginMethods)
	a.handle(auth, http.MethodPost, "/login", AccessPublic, a.login)
	a.handle(auth, http.MethodPost, "/login/select", AccessPublic, a.loginSelect)
	a.handle(auth, http.MethodPost, "/agent_enrollments", AccessPublic, a.beginAgentEnrollment)
	a.handle(auth, http.MethodPost, "/agent_enrollments/token", AccessPublic, a.exchangeAgentEnrollment)
	a.handle(auth, http.MethodPost, "/logout", AccessPublic, a.sessionAuth(), a.logout)
	// Invitation redemption is public: the token in the body is the
	// authorization. The accept route keeps sameOrigin as defense in depth.
	a.handle(auth, http.MethodPost, "/invitations/preview", AccessPublic, a.previewInvitation)
	a.handle(auth, http.MethodPost, "/invitations/accept", AccessPublic, a.sameOrigin(), a.acceptInvitation)
	// Password reset follows the same shape: the request is public and
	// answers the same way for every email, the link's token authorizes
	// the preview and the completion.
	a.handle(auth, http.MethodPost, "/password/reset", AccessPublic, a.requestPasswordReset)
	a.handle(auth, http.MethodPost, "/password/reset/preview", AccessPublic, a.previewPasswordReset)
	a.handle(auth, http.MethodPost, "/password/reset/complete", AccessPublic, a.sameOrigin(), a.completePasswordReset)

	// /api/aap/v1 is the AAP wire surface and nothing else. The instance
	// endpoints carry their own authentication (a provisioner bearer); the runtime endpoints
	// require an instance-bound credential. Browser-assisted enrollment is
	// withHuman product UX and stays under /auth, outside the protocol.
	aap := router.Group("/api/aap/v1")
	a.handle(aap, http.MethodPost, "/instances", AccessPublic, a.mintAgentInstance)
	a.handle(aap, http.MethodGet, "/instances/:id", AccessPublic, a.getProvisionedInstance)
	a.handle(aap, http.MethodPatch, "/instances/:id", AccessPublic, a.updateProvisionedInstance)
	a.handle(aap, http.MethodDelete, "/instances/:id", AccessPublic, a.deleteProvisionedInstance)
	aapRuntime := aap.Group("", a.machineAuth())
	a.handle(aapRuntime, http.MethodPost, "/requests", string(domain.PermissionRequestCreate), a.createApproval)
	a.handle(aapRuntime, http.MethodGet, "/requests/:id", string(domain.PermissionRequestRead), a.getApproval)
	a.handle(aapRuntime, http.MethodDelete, "/requests/:id", string(domain.PermissionRequestCreate), a.cancelApproval)

	// The instance's own product surface, outside AAP: what the credential
	// resolves to (the CLI's authentication probe) and its tool catalogue.
	machine := router.Group("/api/v1", a.machineAuth())
	a.handle(machine, http.MethodGet, "/agent_instances/self", AccessSelf, a.agentInstanceSelf)
	a.handle(machine, http.MethodPut, "/agent_instances/self/tool_catalog", AccessSelf, a.reportToolCatalog)

	appAPI := router.Group("/api/v1", a.principalAuth(), a.sameOrigin())
	a.handle(appAPI, http.MethodGet, "/me", AccessSelf, a.enrichedMe)
	a.handle(appAPI, http.MethodPost, "/me/password", AccessSelf, a.changePassword)
	a.installAPIKeyRoutes(appAPI)
	a.handle(appAPI, http.MethodGet, "/tool_catalog", string(domain.PermissionAgentRead), a.listToolCatalog)
	a.handle(appAPI, http.MethodGet, "/tool_catalog/:id", string(domain.PermissionAgentRead), a.listToolCatalog)
	a.handle(appAPI, http.MethodGet, "/tool_definitions", string(domain.PermissionAgentRead), a.listToolDefinitions)
	a.handle(appAPI, http.MethodGet, "/members", string(domain.PermissionMembershipRead), a.listMembers)
	a.handle(appAPI, http.MethodPatch, "/members/:id", string(domain.PermissionMembershipWrite), a.updateMember)
	a.handle(appAPI, http.MethodDelete, "/members/:id", string(domain.PermissionMembershipWrite), a.removeMember)
	a.handle(appAPI, http.MethodPut, "/members/:id/role", string(domain.PermissionPolicyWrite), a.setMemberRole)
	a.handle(appAPI, http.MethodGet, "/roles", AccessSelf, a.listRoles)
	a.handle(appAPI, http.MethodGet, "/invitations", string(domain.PermissionMembershipInvite), a.listInvitations)
	a.handle(appAPI, http.MethodPost, "/invitations", string(domain.PermissionMembershipInvite), a.createInvitation)
	a.handle(appAPI, http.MethodPost, "/invitations/:id/resend", string(domain.PermissionMembershipInvite), a.resendInvitation)
	a.handle(appAPI, http.MethodDelete, "/invitations/:id", string(domain.PermissionMembershipInvite), a.revokeInvitation)
	a.installPermissionRoutes(appAPI)
	a.installPermissionPolicyRoutes(appAPI)
	a.handle(appAPI, http.MethodGet, "/requests", string(domain.PermissionRequestRead), a.listApprovals)
	a.handle(appAPI, http.MethodGet, "/requests/:id", string(domain.PermissionRequestRead), a.getQueueApproval)
	a.handle(appAPI, http.MethodPost, "/requests/:id/decision", string(domain.PermissionRequestDecide), a.decide)
	a.handle(appAPI, http.MethodPost, "/requests/:id/claim", string(domain.PermissionRequestClaim), a.claim)
	a.handle(appAPI, http.MethodDelete, "/requests/:id/claim", string(domain.PermissionRequestClaim), a.releaseClaim)
	a.handle(appAPI, http.MethodGet, "/requests/:id/routing", string(domain.PermissionRequestRead), a.getApprovalRouting)
	a.handle(appAPI, http.MethodGet, "/requests/:id/review", string(domain.PermissionRequestRead), a.getApprovalReview)
	a.handle(appAPI, http.MethodPost, "/requests/:id/reviewers", string(domain.PermissionRequestDecide), a.addApprovalReviewer)
	a.handle(appAPI, http.MethodGet, "/requests/:id/reviewer-candidates", string(domain.PermissionRequestDecide), a.listReviewerCandidates)
	a.handle(appAPI, http.MethodGet, "/events", string(domain.PermissionRequestRead), a.events)
	a.handle(appAPI, http.MethodGet, "/agents", string(domain.PermissionAgentRead), a.listAgents)
	a.handle(appAPI, http.MethodPost, "/agents", string(domain.PermissionAgentWrite), a.createAgent)
	a.handle(appAPI, http.MethodGet, "/agents/:slug", string(domain.PermissionAgentRead), a.getAgent)
	a.handle(appAPI, http.MethodPatch, "/agents/:slug", string(domain.PermissionAgentWrite), a.updateAgent)
	a.handle(appAPI, http.MethodDelete, "/agents/:slug", string(domain.PermissionAgentWrite), a.archiveAgent)
	a.handle(appAPI, http.MethodPost, "/agents/:slug/restore", string(domain.PermissionAgentWrite), a.restoreAgent)
	a.handle(appAPI, http.MethodPatch, "/agent_instances/:id", string(domain.PermissionAgentWrite), a.updateAgentInstance)
	a.handle(appAPI, http.MethodPost, "/agents/:slug/instances", string(domain.PermissionAgentCredentialIssue), a.createAgentInstance)
	a.handle(appAPI, http.MethodPost, "/agents/:slug/provisioners", string(domain.PermissionAgentCredentialIssue), a.createAgentProvisioner)
	a.handle(appAPI, http.MethodDelete, "/agent-credentials/:id", string(domain.PermissionAgentCredentialRevoke), a.revokeCredential)
	a.handle(appAPI, http.MethodDelete, "/agent-provisioners/:id", string(domain.PermissionAgentCredentialRevoke), a.revokeAgentProvisioner)
	// Enrollment is self service: any member may connect their own
	// instance of a personal agent, and the service checks credential
	// issuance against a service agent.
	a.handle(appAPI, http.MethodPost, "/agent_enrollments/authorize", AccessSelf, a.authorizeAgentEnrollment)
	a.handle(appAPI, http.MethodPost, "/agent_enrollments/preview", AccessSelf, a.previewAgentEnrollment)
	a.handle(appAPI, http.MethodGet, "/agent_enrollments/agents", AccessSelf, a.listEnrollmentAgents)
	a.installRequestApprovalPipelineRoutes(appAPI)
	a.installRoutingRoutes(appAPI)
	a.installOrganizationRoutes(appAPI)
	a.installToolPresentationRoutes(appAPI)
	a.installTeamRoutes(appAPI)
	a.installTeamEscalationRoutes(appAPI)
	a.installContactRoutes(appAPI)
	a.installSlackRoutes(auth, appAPI)
	a.installDiscordRoutes(auth, appAPI)
	a.installWebhookEndpointRoutes(appAPI)
	if a.onboarding != nil {
		a.installOnboardingRoutes(appAPI, router)
	}
	// OAuth for the management MCP server: discovery at the root, client
	// registration and token exchange under /auth, consent under /api/v1.
	a.installMCPOAuthRoutes(root, auth, appAPI)

	a.installFrontend(router)
	return router
}

func (a *API) observability() gin.HandlerFunc {
	return func(c *gin.Context) {
		startedAt := time.Now()
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = uuid.NewString()
		}
		c.Request = c.Request.WithContext(loggermdc.With(c.Request.Context(), loggermdc.Fields{
			loggermdc.KeyRequestID: requestID,
			loggermdc.KeyTraceID:   requestID,
		}))
		c.Header("X-Request-ID", requestID)
		// An MCP tool's in-process call is part of the inbound MCP
		// request already counted, so it is logged but not metered.
		loopback := isMCPLoopback(c.Request.Context())
		if !loopback {
			a.requests.Add(1)
		}
		c.Next()
		duration := time.Since(startedAt)
		level := slog.LevelInfo
		if c.Writer.Status() >= 500 {
			level = slog.LevelError
		}
		if !loopback {
			a.durationNano.Add(uint64(duration.Nanoseconds()))
			if c.Writer.Status() >= 500 {
				a.errors.Add(1)
			}
		}
		a.logger.LogAttrs(c.Request.Context(), level, "http request - "+c.Request.URL.Path,
			slog.String("method", c.Request.Method), slog.String("path", c.Request.URL.Path),
			slog.Int("status", c.Writer.Status()), slog.Int64("duration_ms", duration.Milliseconds()),
			slog.Bool("mcp_loopback", loopback))
	}
}

func (a *API) readiness(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	checks := map[string]string{"postgres": "ready", "temporal": "ready"}
	status := http.StatusOK
	if err := a.approvalRepo.Health(ctx); err != nil {
		checks["postgres"], status = err.Error(), http.StatusServiceUnavailable
	}
	if err := a.orchestrator.Health(ctx); err != nil {
		// Automatic CEL decisions do not use Temporal. Keep the request server
		// available and fail only requests that actually require human review.
		checks["temporal"] = "degraded"
	}
	if a.compiledPipelines != nil {
		checks["compiled_pipelines"] = "ready"
		if err := a.compiledPipelines.Readiness(); err != nil {
			checks["compiled_pipelines"], status = "unavailable", http.StatusServiceUnavailable
		}
	}
	c.JSON(status, gin.H{"status": map[bool]string{true: "ready", false: "not_ready"}[status == http.StatusOK], "checks": checks})
}

func (a *API) metrics(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	metrics, err := a.approvalRepo.OperationalMetrics(ctx)
	if err != nil {
		a.logger.ErrorContext(c.Request.Context(), "operational metrics collection failed", "error", err)
		c.String(http.StatusServiceUnavailable, "metrics unavailable\n")
		return
	}
	c.Header("Content-Type", "text/plain; version=0.0.4")
	requestCount := a.requests.Load()
	averageHTTPSeconds := 0.0
	if requestCount > 0 {
		averageHTTPSeconds = float64(a.durationNano.Load()) / float64(requestCount) / float64(time.Second)
	}
	c.String(http.StatusOK, `# HELP withhuman_http_requests_total HTTP requests handled by this process.
# TYPE withhuman_http_requests_total counter
withhuman_http_requests_total %d
# HELP withhuman_http_errors_total HTTP responses with a 5xx status.
# TYPE withhuman_http_errors_total counter
withhuman_http_errors_total %d
# HELP withhuman_http_request_duration_seconds Mean HTTP request duration for this process.
# TYPE withhuman_http_request_duration_seconds gauge
withhuman_http_request_duration_seconds %.6f
# HELP withhuman_approvals Current approval request count by status.
# TYPE withhuman_approvals gauge
withhuman_approvals{status="pending"} %d
withhuman_approvals{status="approved"} %d
withhuman_approvals{status="denied"} %d
withhuman_approvals{status="expired"} %d
withhuman_approvals{status="cancelled"} %d
# HELP withhuman_approval_decision_latency_seconds Mean human decision latency for approved and denied requests.
# TYPE withhuman_approval_decision_latency_seconds gauge
withhuman_approval_decision_latency_seconds %.6f
# HELP withhuman_database_connections PostgreSQL connection-pool state.
# TYPE withhuman_database_connections gauge
withhuman_database_connections{state="acquired"} %d
withhuman_database_connections{state="idle"} %d
withhuman_database_connections{state="max"} %d
`, requestCount, a.errors.Load(), averageHTTPSeconds, metrics.Pending, metrics.Approved, metrics.Denied, metrics.Expired, metrics.Cancelled,
		metrics.AverageDecisionLatencySecs, metrics.DatabaseAcquiredConns, metrics.DatabaseIdleConns, metrics.DatabaseMaxConns)
}

func (a *API) runtimeConfig(c *gin.Context) {
	// Social providers are read live: the edition registers them on the
	// auth service after the router is composed.
	providers := []authn.SocialProvider{}
	if a.auth != nil {
		providers = a.auth.SocialProviders()
	}
	agentMCPServers := []domain.AgentMCPServer{}
	if a.config.AgentMCPServers != nil {
		agentMCPServers = a.config.AgentMCPServers
	}
	payload := gin.H{
		"public_origin": a.config.PublicOrigin, "version": a.config.Version,
		"bootstrap_enabled": a.config.BootstrapEnabled,
		// Predefined roles are always offered; custom permission
		// policies only where the edition switched them on.
		"custom_permissions_enabled": a.config.CustomPermissions,
		// The guided setup's assistant needs the edition's model and the
		// management tool catalog this layer owns.
		"setup_assistant_enabled": a.onboarding != nil && a.onboarding.AssistantEnabled(),
		"webhook_enabled":         a.webhooks != nil && a.webhooks.Configured(),
		"slack_enabled":           a.slack != nil && a.slack.Configured(),
		"discord_enabled":         a.discord != nil && a.discord.Configured(),
		"social_providers":        providers,
		"cli_download_url":        a.config.CLIDownloadURL,
		"agent_mcp_servers":       agentMCPServers,
	}
	for key, value := range a.config.Extras {
		payload[key] = value
	}
	c.JSON(http.StatusOK, payload)
}

// runtimeConfigKeys are the entries runtimeConfig reports itself; an
// edition's extras may not shadow them. The test keeps the two in step.
var runtimeConfigKeys = map[string]bool{
	"public_origin": true, "version": true, "bootstrap_enabled": true,
	"custom_permissions_enabled": true,
	"setup_assistant_enabled": true, "webhook_enabled": true,
	"slack_enabled": true, "discord_enabled": true,
	"social_providers": true,
	"cli_download_url": true, "agent_mcp_servers": true,
}

// RuntimeConfigExtras validates an edition's runtime-config entries for
// Config.Extras: a key core reports itself is refused, so an edition can
// only add to the document, never change what the shared frontend reads
// from core.
func RuntimeConfigExtras(extras map[string]any) (map[string]any, error) {
	for key := range extras {
		if runtimeConfigKeys[key] {
			return nil, fmt.Errorf("runtime config entry %q belongs to core", key)
		}
	}
	return extras, nil
}

type bootstrapRequest struct {
	Secret           string `json:"secret" binding:"required"`
	OrganizationSlug string `json:"organization_slug" binding:"required"`
	OrganizationName string `json:"organization_name" binding:"required"`
	DisplayName      string `json:"display_name" binding:"required"`
	Email            string `json:"email" binding:"required,email"`
	Password         string `json:"password" binding:"required"`
}

func (a *API) bootstrap(c *gin.Context) {
	var request bootstrapRequest
	if !bind(c, &request) {
		return
	}
	result, err := a.auth.Bootstrap(c.Request.Context(), request.Secret, authn.BootstrapInput{
		OrganizationSlug: request.OrganizationSlug, OrganizationName: request.OrganizationName,
		DisplayName: request.DisplayName, Email: request.Email, Password: request.Password,
	})
	if err != nil {
		a.writeError(c, err)
		return
	}
	setPrincipal(c, result.Principal)
	a.setSessionCookie(c, result.Token, result.ExpiresAt)
	c.JSON(http.StatusCreated, gin.H{"principal": result.Principal})
}

func (a *API) loginMethods(c *gin.Context) {
	var request struct {
		Email string `json:"email" binding:"required,email"`
	}
	if !bind(c, &request) {
		return
	}
	methods, err := a.auth.LoginMethods(c.Request.Context(), request.Email)
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"password":          methods.Password,
		"sso_organizations": methods.SSOOrganizations,
	})
}

func (a *API) login(c *gin.Context) {
	var request struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required"`
	}
	if !bind(c, &request) {
		return
	}
	result, err := a.auth.LoginLocal(c.Request.Context(), authn.LocalLoginInput{
		Email: request.Email, Password: request.Password,
	})
	if err != nil {
		a.writeError(c, err)
		return
	}
	if result.Authentication != nil {
		setPrincipal(c, result.Authentication.Principal)
		a.setSessionCookie(c, result.Authentication.Token, result.Authentication.ExpiresAt)
		c.JSON(http.StatusOK, gin.H{"status": "complete", "principal": result.Authentication.Principal})
		return
	}
	if result.SSORequired {
		c.JSON(http.StatusOK, gin.H{"status": "sso_required", "organizations": result.Organizations})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":        "select_organization",
		"ticket":        result.Ticket,
		"expires_at":    result.TicketExpiresAt,
		"organizations": result.Organizations,
	})
}

func (a *API) loginSelect(c *gin.Context) {
	var request struct {
		Ticket         string `json:"ticket" binding:"required"`
		OrganizationID string `json:"organization_id" binding:"required"`
	}
	if !bind(c, &request) {
		return
	}
	result, err := a.auth.SelectLoginOrganization(c.Request.Context(), authn.SelectOrganizationInput{
		Ticket: request.Ticket, OrganizationID: domain.ID(request.OrganizationID),
	})
	if err != nil {
		a.writeError(c, err)
		return
	}
	setPrincipal(c, result.Principal)
	a.setSessionCookie(c, result.Token, result.ExpiresAt)
	c.JSON(http.StatusOK, gin.H{"principal": result.Principal})
}

func (a *API) logout(c *gin.Context) {
	principal := currentPrincipal(c)
	if principal.Auth.SessionID != "" {
		_ = a.identities.RevokeSession(c.Request.Context(), application.RevokeSessionInput{
			SessionID: principal.Auth.SessionID,
		})
	}
	c.SetCookie(authn.SessionCookieName, "", -1, "/", "", a.config.CookieSecure, true)
	c.Status(http.StatusNoContent)
}

func (a *API) beginAgentEnrollment(c *gin.Context) {
	var request struct {
		Runtime            string          `json:"runtime" binding:"required"`
		RequestedAgentSlug string          `json:"requested_agent_slug"`
		InstanceName       string          `json:"instance_name" binding:"required"`
		InstanceMetadata   json.RawMessage `json:"instance_metadata"`
	}
	if !bindStrict(c, &request) {
		return
	}
	result, err := a.auth.BeginAgentEnrollment(c.Request.Context(), request.Runtime, request.RequestedAgentSlug, request.InstanceName, request.InstanceMetadata)
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

func (a *API) exchangeAgentEnrollment(c *gin.Context) {
	var request struct {
		EnrollmentCode string `json:"enrollment_code" binding:"required"`
	}
	if !bindStrict(c, &request) {
		return
	}
	result, err := a.auth.ExchangeAgentEnrollment(c.Request.Context(), request.EnrollmentCode)
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (a *API) authorizeAgentEnrollment(c *gin.Context) {
	var request struct {
		UserCode  string           `json:"user_code" binding:"required"`
		AgentSlug domain.AgentSlug `json:"agent_slug" binding:"required"`
	}
	if !bindStrict(c, &request) {
		return
	}
	agentID, err := a.auth.AgentIDBySlug(c.Request.Context(), currentPrincipal(c), request.AgentSlug)
	if err != nil {
		a.writeError(c, err)
		return
	}
	instance, err := a.auth.AuthorizeAgentEnrollment(c.Request.Context(), currentPrincipal(c), request.UserCode, agentID)
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, instance)
}

func (a *API) createApproval(c *gin.Context) {
	var input approvals.CreateInput
	if !bindStrict(c, &input) {
		return
	}
	input.IdempotencyKey = c.GetHeader("Idempotency-Key")
	request, err := a.approvals.Create(c.Request.Context(), currentPrincipal(c), input)
	if err != nil {
		a.writeError(c, err)
		return
	}
	setApprovalRequest(c, request.ID)
	c.JSON(http.StatusCreated, newAAPApprovalResponse(request))
}

func (a *API) fetchApproval(c *gin.Context) (domain.ApprovalRequest, bool) {
	requestID := domain.ID(c.Param("id"))
	setApprovalRequest(c, requestID)
	wait := parseWait(c.Query("wait"), 30*time.Second)
	var request domain.ApprovalRequest
	var err error
	if wait > 0 {
		request, err = a.approvals.Wait(c.Request.Context(), currentPrincipal(c), requestID, wait)
	} else {
		request, err = a.approvals.Get(c.Request.Context(), currentPrincipal(c), requestID)
	}
	if err != nil {
		a.writeError(c, err)
		return domain.ApprovalRequest{}, false
	}
	return request, true
}

// getApproval is the AAP read: the shared protocol representation, which
// never carries queue state such as claims.
func (a *API) getApproval(c *gin.Context) {
	principal := currentPrincipal(c)
	if principal.ActorType != domain.ActorAgent || principal.AgentInstanceID == "" {
		a.writeError(c, domain.ErrForbidden)
		return
	}
	request, ok := a.fetchApproval(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, newAAPApprovalResponse(request))
}

// cancelApproval is the AAP withdrawal: the requesting instance takes a
// pending request back. The cancelled resource comes back so the adapter
// sees the terminal state it produced.
func (a *API) cancelApproval(c *gin.Context) {
	requestID := domain.ID(c.Param("id"))
	setApprovalRequest(c, requestID)
	request, err := a.approvals.Cancel(c.Request.Context(), currentPrincipal(c), requestID)
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, newAAPApprovalResponse(request))
}

// getQueueApproval is the product read: the same request enriched with the
// open claim.
func (a *API) getQueueApproval(c *gin.Context) {
	request, ok := a.fetchApproval(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, a.presentOne(c, request, time.Now()))
}

func (a *API) listApprovals(c *gin.Context) {
	requests, err := a.approvals.List(c.Request.Context(), currentPrincipal(c), application.ListApprovalsInput{
		Limit: queryInt(c, "limit", 50), Offset: queryInt(c, "offset", 0),
		AgentSlug: domain.AgentSlug(c.Query("agent_slug")), PerAgent: queryInt(c, "per_agent", 0), Query: strings.TrimSpace(c.Query("q")), Status: domain.ApprovalStatus(c.Query("status")),
		Scope: application.QueueScope(c.Query("scope")),
	})
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"requests": a.presentMany(c, requests, time.Now())})
}

func (a *API) claim(c *gin.Context) {
	requestID := domain.ID(c.Param("id"))
	setApprovalRequest(c, requestID)
	claim, err := a.approvals.Claim(c.Request.Context(), currentPrincipal(c), requestID)
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"claim": newClaimResponse(claim)})
}

func (a *API) releaseClaim(c *gin.Context) {
	requestID := domain.ID(c.Param("id"))
	setApprovalRequest(c, requestID)
	if err := a.approvals.ReleaseClaim(c.Request.Context(), currentPrincipal(c), requestID); err != nil {
		a.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *API) decide(c *gin.Context) {
	requestID := domain.ID(c.Param("id"))
	setApprovalRequest(c, requestID)
	var input approvals.DecideInput
	if !bind(c, &input) {
		return
	}
	input.IdempotencyKey = c.GetHeader("Idempotency-Key")
	operation, err := a.approvals.Decide(c.Request.Context(), currentPrincipal(c), requestID, input)
	if err != nil {
		a.writeError(c, err)
		return
	}
	setRequestMDC(c, loggermdc.Fields{loggermdc.KeyDecisionOperationID: string(operation.ID)})
	c.JSON(http.StatusOK, newDecisionOperationResponse(operation))
}

func (a *API) events(c *gin.Context) {
	principal := currentPrincipal(c)
	if principal.ActorType != domain.ActorHuman || !authorization.HoldsAnywhere(principal, domain.PermissionRequestRead) {
		a.writeError(c, authorization.Denied{Permission: domain.PermissionRequestRead, Resource: domain.OrganizationResource()})
		return
	}
	// The stream carries one queue layer, the one the reader is looking
	// at; a bad layer is refused before any frame, like the listing.
	scope := application.QueueScope(c.Query("scope"))
	if !scope.Valid() {
		a.writeError(c, fmt.Errorf("scope: choose notified, decidable, or visible: %w", domain.ErrInvalid))
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	lastFingerprint := ""
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		requests, err := a.approvals.List(c.Request.Context(), principal, application.ListApprovalsInput{Limit: 100, Scope: scope})
		if err != nil {
			return
		}
		payload, _ := json.Marshal(a.presentMany(c, requests, time.Now()))
		fingerprint := string(payload)
		if fingerprint != lastFingerprint {
			fmt.Fprintf(c.Writer, "event: queue\ndata: %s\n\n", payload)
			c.Writer.Flush()
			lastFingerprint = fingerprint
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
			fmt.Fprint(c.Writer, ": keepalive\n\n")
			c.Writer.Flush()
		}
	}
}

func (a *API) listAgents(c *gin.Context) {
	status := application.AgentListStatus(c.DefaultQuery("status", string(application.AgentListStatusLive)))
	agents, err := a.auth.ListAgents(c.Request.Context(), currentPrincipal(c), status, queryInt(c, "limit", 50))
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"agents": agents})
}

func (a *API) createAgent(c *gin.Context) {
	var request struct {
		Slug domain.AgentSlug `json:"slug" binding:"required"`
		Name string           `json:"name" binding:"required"`
		Kind domain.AgentKind `json:"kind"`
	}
	if !bind(c, &request) {
		return
	}
	if request.Kind == "" {
		request.Kind = domain.AgentService
	}
	agent, err := a.auth.CreateAgent(c.Request.Context(), currentPrincipal(c), request.Name, request.Slug, request.Kind)
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, agent)
}

// agentID resolves the slug in the route to the agent's internal id. An
// unknown slug, or one the caller may not resolve, ends the request.
func (a *API) agentID(c *gin.Context) (domain.ID, bool) {
	agentID, err := a.auth.AgentIDBySlug(c.Request.Context(), currentPrincipal(c), domain.AgentSlug(c.Param("slug")))
	if err != nil {
		a.writeError(c, err)
		return "", false
	}
	return agentID, true
}

func (a *API) getAgent(c *gin.Context) {
	agentID, ok := a.agentID(c)
	if !ok {
		return
	}
	agent, err := a.auth.GetAgent(c.Request.Context(), currentPrincipal(c), application.GetAgentDetailInput{
		AgentID: agentID, InstanceLimit: queryInt(c, "instance_limit", 25), InstanceOffset: queryInt(c, "instance_offset", 0),
		InstanceQuery: strings.TrimSpace(c.Query("instance_q")), InstanceStatus: domain.AgentStatus(c.Query("instance_status")),
		ProvisionerLimit: queryInt(c, "provisioner_limit", 25), ProvisionerOffset: queryInt(c, "provisioner_offset", 0),
		ProvisionerQuery: strings.TrimSpace(c.Query("provisioner_q")), ProvisionerStatus: c.Query("provisioner_status"),
	})
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, agent)
}

// updateAgent covers the two mutable agent fields after creation: the
// status switch and the display name. The slug is fixed at creation; a
// different slug is a new agent. Who manages the agent is a matter of
// role assignments at the agent, not a property of it.
func (a *API) updateAgent(c *gin.Context) {
	var request struct {
		Status domain.AgentStatus `json:"status"`
		Name   *string            `json:"name"`
	}
	if !bind(c, &request) {
		return
	}
	if request.Status == "" && request.Name == nil {
		a.writeError(c, fmt.Errorf("status or name is required: %w", domain.ErrInvalid))
		return
	}
	agentID, ok := a.agentID(c)
	if !ok {
		return
	}
	var agent domain.Agent
	var err error
	if request.Name != nil {
		if agent, err = a.auth.RenameAgent(c.Request.Context(), currentPrincipal(c), agentID, *request.Name); err != nil {
			a.writeError(c, err)
			return
		}
	}
	if request.Status != "" {
		if agent, err = a.auth.SetAgentStatus(c.Request.Context(), currentPrincipal(c), agentID, request.Status); err != nil {
			a.writeError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, agent)
}

// archiveAgent retires the agent; the page's Archive action and scripts
// share it. restoreAgent brings it back.
func (a *API) archiveAgent(c *gin.Context) {
	agentID, ok := a.agentID(c)
	if !ok {
		return
	}
	if _, err := a.auth.ArchiveAgent(c.Request.Context(), currentPrincipal(c), agentID); err != nil {
		a.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *API) restoreAgent(c *gin.Context) {
	agentID, ok := a.agentID(c)
	if !ok {
		return
	}
	agent, err := a.auth.RestoreAgent(c.Request.Context(), currentPrincipal(c), agentID)
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, agent)
}

// updateAgentInstance covers the two mutable instance fields: the status
// switch and the display name. Which agent the instance belongs to never
// changes.
func (a *API) updateAgentInstance(c *gin.Context) {
	var request struct {
		Status domain.AgentStatus `json:"status"`
		Name   *string            `json:"name"`
	}
	if !bind(c, &request) {
		return
	}
	if request.Status == "" && request.Name == nil {
		a.writeError(c, fmt.Errorf("status or name is required: %w", domain.ErrInvalid))
		return
	}
	instanceID := domain.ID(c.Param("id"))
	var instance domain.AgentInstance
	var err error
	if request.Name != nil {
		if instance, err = a.auth.RenameAgentInstance(c.Request.Context(), currentPrincipal(c), instanceID, *request.Name); err != nil {
			a.writeError(c, err)
			return
		}
	}
	if request.Status != "" {
		if instance, err = a.auth.SetAgentInstanceStatus(c.Request.Context(), currentPrincipal(c), instanceID, request.Status); err != nil {
			a.writeError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, instance)
}

func (a *API) createAgentInstance(c *gin.Context) {
	var request struct {
		Name     string          `json:"name" binding:"required"`
		Metadata json.RawMessage `json:"metadata"`
	}
	if !bind(c, &request) {
		return
	}
	agentID, ok := a.agentID(c)
	if !ok {
		return
	}
	instance, err := a.auth.CreateAgentInstanceCredential(c.Request.Context(), currentPrincipal(c), agentID, request.Name, request.Metadata)
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, instance)
}

func (a *API) createAgentProvisioner(c *gin.Context) {
	agentID, ok := a.agentID(c)
	if !ok {
		return
	}
	result, err := a.auth.CreateAgentProvisioner(c.Request.Context(), currentPrincipal(c), agentID)
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

func (a *API) revokeAgentProvisioner(c *gin.Context) {
	if err := a.auth.RevokeAgentProvisioner(c.Request.Context(), currentPrincipal(c), domain.ID(c.Param("id"))); err != nil {
		a.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *API) revokeCredential(c *gin.Context) {
	if err := a.auth.RevokeCredential(c.Request.Context(), currentPrincipal(c), domain.ID(c.Param("id"))); err != nil {
		a.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Pipelines are addressed by key. The entry pipeline, where every
// request starts, is the reserved key "entry"; every other pipeline is
// created explicitly and reached through delegate blocks. Handlers read
// the pipeline_key route parameter and hand it to the service, which
// validates it and checks the permission at the pipeline's own scope.
func (a *API) installRequestApprovalPipelineRoutes(router *gin.RouterGroup) {
	read, write, activate := string(domain.PermissionPipelineRead), string(domain.PermissionPipelineWrite), string(domain.PermissionPipelineActivate)
	a.handle(router, http.MethodGet, "/request-approval-pipelines", read, a.listRequestApprovalPipelines)
	a.handle(router, http.MethodPost, "/request-approval-pipelines", write, a.createRequestApprovalPipeline)
	pipeline := "/request-approval-pipelines/:pipeline_key"
	a.handle(router, http.MethodGet, pipeline, read, a.getActiveRequestApprovalPipeline)
	a.handle(router, http.MethodDelete, pipeline, activate, a.archiveRequestApprovalPipeline)
	a.handle(router, http.MethodGet, pipeline+"/uses", read, a.listRequestApprovalPipelineUses)
	a.handle(router, http.MethodPost, pipeline+"/preview", write, a.previewRequestApprovalPipeline)
	a.handle(router, http.MethodPost, pipeline+"/revisions", write, a.createRequestApprovalPipelineRevision)
	a.handle(router, http.MethodGet, pipeline+"/revisions", read, a.listRequestApprovalPipelineRevisions)
	a.handle(router, http.MethodGet, pipeline+"/revisions/:revision", read, a.getRequestApprovalPipelineRevision)
	a.handle(router, http.MethodGet, pipeline+"/revisions/:revision/references", read, a.getRequestApprovalPipelineRevisionReferences)
	a.handle(router, http.MethodPost, pipeline+"/revisions/:revision/activate", activate, a.activateRequestApprovalPipelineRevision)
}

type requestApprovalPipelineDocumentRequest struct {
	Name                  string                     `json:"name,omitempty"`
	Blocks                []approvalblock.Definition `json:"blocks"`
	DefaultEscalationPath string                     `json:"default_escalation_path,omitempty"`
	DefaultAskAgentOwner  bool                       `json:"default_ask_agent_owner,omitempty"`
}

type requestApprovalPipelineCreateRequest struct {
	PipelineKey           string                     `json:"pipeline_key"`
	Name                  string                     `json:"name"`
	Blocks                []approvalblock.Definition `json:"blocks"`
	DefaultEscalationPath string                     `json:"default_escalation_path,omitempty"`
	DefaultAskAgentOwner  bool                       `json:"default_ask_agent_owner,omitempty"`
}

type requestApprovalPipelinePreviewRequest struct {
	Blocks                []approvalblock.Definition `json:"blocks"`
	DefaultEscalationPath string                     `json:"default_escalation_path,omitempty"`
	DefaultAskAgentOwner  bool                       `json:"default_ask_agent_owner,omitempty"`
	Sample                struct {
		Request struct {
			Tool           string          `json:"tool" binding:"required"`
			Server         string          `json:"server"`
			Arguments      json.RawMessage `json:"arguments" binding:"required"`
			AgentReasoning string          `json:"agent_reasoning"`
			Context        json.RawMessage `json:"context"`
		} `json:"request" binding:"required"`
		Agent struct {
			Slug string `json:"slug" binding:"required"`
			Name string `json:"name" binding:"required"`
		} `json:"agent" binding:"required"`
		// PreviousMetadata seeds what earlier blocks returned, nested by
		// pipeline key and block key.
		PreviousMetadata map[string]map[string]json.RawMessage `json:"previous_metadata"`
	} `json:"sample" binding:"required"`
}

func (a *API) listRequestApprovalPipelines(c *gin.Context) {
	status := application.PipelineStatus(c.DefaultQuery("status", string(application.PipelineStatusLive)))
	summaries, err := a.pipelines.List(c.Request.Context(), currentPrincipal(c), status, queryInt(c, "limit", 50))
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"request_approval_pipelines": newRequestApprovalPipelineSummaryResponses(summaries)})
}

func (a *API) createRequestApprovalPipeline(c *gin.Context) {
	var request requestApprovalPipelineCreateRequest
	if !bindStrict(c, &request) {
		return
	}
	if request.Blocks == nil {
		request.Blocks = []approvalblock.Definition{}
	}
	revision, err := a.pipelines.Create(c.Request.Context(), currentPrincipal(c), request.PipelineKey, request.Name, request.Blocks, approvalpipeline.Route{
		EscalationPath: request.DefaultEscalationPath, AskAgentOwner: request.DefaultAskAgentOwner,
	})
	if err != nil {
		a.writeError(c, err)
		return
	}
	setActiveRevisionETag(c, revision.Revision.Revision)
	c.JSON(http.StatusCreated, newRequestApprovalPipelineRevisionResponse(revision))
}

func (a *API) getActiveRequestApprovalPipeline(c *gin.Context) {
	revision, err := a.pipelines.GetActive(c.Request.Context(), currentPrincipal(c), c.Param("pipeline_key"))
	if err != nil {
		a.writeError(c, err)
		return
	}
	setActiveRevisionETag(c, revision.Revision.Revision)
	c.JSON(http.StatusOK, newRequestApprovalPipelineRevisionResponse(revision))
}

func (a *API) archiveRequestApprovalPipeline(c *gin.Context) {
	if _, err := a.pipelines.Archive(c.Request.Context(), currentPrincipal(c), c.Param("pipeline_key")); err != nil {
		a.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *API) listRequestApprovalPipelineUses(c *gin.Context) {
	uses, err := a.pipelines.Uses(c.Request.Context(), currentPrincipal(c), c.Param("pipeline_key"))
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"uses": newPipelineUseResponses(uses)})
}

func (a *API) previewRequestApprovalPipeline(c *gin.Context) {
	var request requestApprovalPipelinePreviewRequest
	if !bindStrict(c, &request) {
		return
	}
	if request.Blocks == nil {
		a.writeError(c, fmt.Errorf("blocks are required: %w", domain.ErrInvalid))
		return
	}
	result, err := a.pipelines.Preview(c.Request.Context(), currentPrincipal(c), c.Param("pipeline_key"), request.Blocks, approvalpipeline.Route{
		EscalationPath: request.DefaultEscalationPath, AskAgentOwner: request.DefaultAskAgentOwner,
	}, approvalpipeline.PreviewSample{
		Request: approvalpipeline.BlockRequest{
			Tool: request.Sample.Request.Tool, Server: request.Sample.Request.Server, Arguments: request.Sample.Request.Arguments,
			AgentReasoning: request.Sample.Request.AgentReasoning, Context: request.Sample.Request.Context,
		},
		Agent:            approvalpipeline.BlockAgent{Slug: request.Sample.Agent.Slug, Name: request.Sample.Agent.Name},
		PreviousMetadata: flattenPreviewMetadata(request.Sample.PreviousMetadata),
	})
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, newRequestApprovalPipelinePreviewResponse(result))
}

// flattenPreviewMetadata turns the wire's nested pipeline/block object
// into the executor's "<pipeline_key>/<block_key>" keys.
func flattenPreviewMetadata(nested map[string]map[string]json.RawMessage) map[string]json.RawMessage {
	flat := make(map[string]json.RawMessage)
	for pipelineKey, blocks := range nested {
		for blockKey, metadata := range blocks {
			flat[pipelineKey+"/"+blockKey] = metadata
		}
	}
	return flat
}

func (a *API) createRequestApprovalPipelineRevision(c *gin.Context) {
	var request requestApprovalPipelineDocumentRequest
	if !bindStrict(c, &request) {
		return
	}
	if request.Blocks == nil {
		a.writeError(c, fmt.Errorf("blocks are required: %w", domain.ErrInvalid))
		return
	}
	revision, err := a.pipelines.CreateRevision(c.Request.Context(), currentPrincipal(c), c.Param("pipeline_key"), request.Name, request.Blocks, approvalpipeline.Route{
		EscalationPath: request.DefaultEscalationPath, AskAgentOwner: request.DefaultAskAgentOwner,
	})
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, newRequestApprovalPipelineRevisionResponse(revision))
}

func (a *API) listRequestApprovalPipelineRevisions(c *gin.Context) {
	page, err := a.pipelines.ListRevisions(c.Request.Context(), currentPrincipal(c), c.Param("pipeline_key"), queryInt(c, "limit", 50), c.Query("cursor"))
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, revisionPageResponse[requestApprovalPipelineRevisionSummaryResponse]{
		Revisions: newRequestApprovalPipelineRevisionSummaryResponses(page.Revisions), NextCursor: page.NextCursor, TotalCount: page.TotalCount,
	})
}

func (a *API) getRequestApprovalPipelineRevision(c *gin.Context) {
	revisionNumber, err := parsePipelineRevision(c.Param("revision"))
	if err != nil {
		a.writeError(c, err)
		return
	}
	revision, err := a.pipelines.GetRevision(c.Request.Context(), currentPrincipal(c), c.Param("pipeline_key"), revisionNumber)
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, newRequestApprovalPipelineRevisionResponse(revision))
}

// getRequestApprovalPipelineRevisionReferences reports what a revision
// depends on and whether each dependency is in place, so the page can say
// why a rollback would be refused before it is attempted.
func (a *API) getRequestApprovalPipelineRevisionReferences(c *gin.Context) {
	revisionNumber, err := parsePipelineRevision(c.Param("revision"))
	if err != nil {
		a.writeError(c, err)
		return
	}
	references, err := a.pipelines.References(c.Request.Context(), currentPrincipal(c), c.Param("pipeline_key"), revisionNumber)
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, newPipelineReferencesResponse(references))
}

func (a *API) activateRequestApprovalPipelineRevision(c *gin.Context) {
	revisionNumber, err := parsePipelineRevision(c.Param("revision"))
	if err != nil {
		a.writeError(c, err)
		return
	}
	expected, err := parseRequiredActiveRevisionIfMatch(c.GetHeader("If-Match"))
	if err != nil {
		a.writeError(c, err)
		return
	}
	revision, err := a.pipelines.Activate(c.Request.Context(), currentPrincipal(c), c.Param("pipeline_key"), revisionNumber, expected)
	if err != nil {
		a.writeError(c, err)
		return
	}
	setActiveRevisionETag(c, revision.Revision.Revision)
	c.JSON(http.StatusOK, newRequestApprovalPipelineRevisionResponse(revision))
}

func parsePipelineRevision(value string) (int64, error) {
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil || revision <= 0 {
		return 0, fmt.Errorf("revision must be a positive integer: %w", domain.ErrInvalid)
	}
	return revision, nil
}

func parseActiveRevisionIfMatch(value string) (*int64, error) {
	if value == "" {
		return nil, fmt.Errorf("If-Match is required: %w", domain.ErrPreconditionRequired)
	}
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' || strings.Contains(value[1:len(value)-1], ",") {
		return nil, fmt.Errorf("If-Match must be one quoted active revision: %w", domain.ErrInvalid)
	}
	revision, err := strconv.ParseInt(value[1:len(value)-1], 10, 64)
	if err != nil || revision < 0 {
		return nil, fmt.Errorf("If-Match must be a quoted non-negative revision: %w", domain.ErrInvalid)
	}
	if revision == 0 {
		return nil, nil
	}
	return &revision, nil
}

// parseRequiredActiveRevisionIfMatch reads If-Match for a resource that
// always has an active revision, so "0" (no active revision) is refused.
func parseRequiredActiveRevisionIfMatch(value string) (int64, error) {
	expected, err := parseActiveRevisionIfMatch(value)
	if err != nil {
		return 0, err
	}
	if expected == nil {
		return 0, fmt.Errorf("If-Match must identify an active revision: %w", domain.ErrInvalid)
	}
	return *expected, nil
}

func setActiveRevisionETag(c *gin.Context, revision int64) {
	c.Header("ETag", fmt.Sprintf("\"%d\"", revision))
}

func (a *API) principalAuth() gin.HandlerFunc { return principalAuth(a.auth, a.logger, true) }

// PrincipalAuth authenticates edition-owned management routes. Personal keys
// and sessions retain their existing behavior; organization keys are refused
// until an edition explicitly defines and audits its own credential gates.
func PrincipalAuth(auth *authn.Service, logger *slog.Logger) gin.HandlerFunc {
	return principalAuth(auth, logger, false)
}

// PrincipalAuthAdmittingOrganizationKeys is PrincipalAuth for an edition
// route that has defined its organization-key gate: the permission it
// requires must name HolderOrganizationAPIKey, which the coarse gate then
// enforces through the key's own grants.
func PrincipalAuthAdmittingOrganizationKeys(auth *authn.Service, logger *slog.Logger) gin.HandlerFunc {
	return principalAuth(auth, logger, true)
}

func principalAuth(auth *authn.Service, logger *slog.Logger, allowOrganizationKeys bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		// The setup assistant's in-process calls arrive with the person's
		// principal already resolved from their browser session.
		if principal, ok := loopbackPrincipal(c.Request.Context()); ok {
			setPrincipal(c, principal)
			c.Next()
			return
		}
		if bearer := c.GetHeader("Authorization"); bearer != "" {
			token := strings.TrimSpace(strings.TrimPrefix(bearer, "Bearer "))
			if !strings.HasPrefix(bearer, "Bearer ") || !authn.IsAPIKeyToken(token) {
				WriteError(c, logger, domain.ErrUnauthenticated)
				return
			}
			principal, err := auth.ResolveAPIKey(c.Request.Context(), token)
			if err != nil {
				WriteError(c, logger, err)
				return
			}
			if principal.ActorType == domain.ActorAPIKey && !allowOrganizationKeys {
				WriteError(c, logger, domain.ErrAPIKeyNotAllowed)
				return
			}
			use := application.RecordAPIKeyUseInput{Principal: principal, Method: c.Request.Method, Route: c.FullPath()}
			if call, ok := c.Value(mcpCallUseKey).(mcpCallUse); ok {
				use.MCPMethod, use.MCPTool = call.Method, call.Tool
			}
			if err := auth.RecordAPIKeyUse(c.Request.Context(), use); err != nil {
				WriteError(c, logger, err)
				return
			}
			setPrincipal(c, principal)
			c.Next()
			return
		}
		sessionAuth(auth, logger)(c)
	}
}

func (a *API) sessionAuth() gin.HandlerFunc { return sessionAuth(a.auth, a.logger) }

// sessionAuth resolves the session cookie alone: the logout route, which
// acts on the session itself, and the cookie half of PrincipalAuth.
func sessionAuth(auth *authn.Service, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(authn.SessionCookieName)
		if err != nil {
			WriteError(c, logger, domain.ErrUnauthenticated)
			return
		}
		principal, err := auth.ResolveSession(c.Request.Context(), token)
		if err != nil {
			WriteError(c, logger, err)
			return
		}
		setPrincipal(c, principal)
		c.Next()
	}
}

func (a *API) machineAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		bearer := c.GetHeader("Authorization")
		if !strings.HasPrefix(bearer, "Bearer ") {
			a.writeError(c, domain.ErrUnauthenticated)
			c.Abort()
			return
		}
		principal, err := a.auth.ResolveCredential(c.Request.Context(), strings.TrimSpace(strings.TrimPrefix(bearer, "Bearer ")))
		if err != nil {
			a.writeError(c, err)
			c.Abort()
			return
		}
		setPrincipal(c, principal)
		c.Next()
	}
}

func (a *API) sameOrigin() gin.HandlerFunc { return SameOrigin(a.config.PublicOrigin, a.logger) }

// SameOrigin refuses state-changing requests whose Origin header names
// another site. The guard exists because cookies are ambient; a request
// that authenticated with an API key carried its credential deliberately
// and passes. Exported for an edition's additive routes; it runs after
// PrincipalAuth.
func SameOrigin(publicOrigin string, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		if principal, ok := c.Get(principalKey); ok {
			if principal, ok := principal.(domain.Principal); ok && principal.Auth.Method == domain.AuthMethodAPIKey {
				c.Next()
				return
			}
		}
		origin := c.GetHeader("Origin")
		if origin != "" && strings.TrimRight(origin, "/") != strings.TrimRight(publicOrigin, "/") {
			WriteError(c, logger, domain.ErrForbidden)
			return
		}
		c.Next()
	}
}

func (a *API) setSessionCookie(c *gin.Context, token string, expiresAt time.Time) {
	http.SetCookie(c.Writer, &http.Cookie{Name: authn.SessionCookieName, Value: token, Path: "/", HttpOnly: true,
		Secure: a.config.CookieSecure, SameSite: http.SameSiteStrictMode, Expires: expiresAt})
}

func currentPrincipal(c *gin.Context) domain.Principal { return Principal(c) }

// Principal returns the principal PrincipalAuth resolved for this request.
func Principal(c *gin.Context) domain.Principal {
	principal, _ := c.MustGet(principalKey).(domain.Principal)
	return principal
}

func setPrincipal(c *gin.Context, principal domain.Principal) {
	c.Set(principalKey, principal)
	fields := loggermdc.Fields{
		loggermdc.KeyOrganizationID: string(principal.OrganizationID),
		loggermdc.KeyActorType:      string(principal.ActorType),
		loggermdc.KeyActorID:        string(principal.ActorID),
	}
	if principal.AgentInstanceID != "" {
		fields[loggermdc.KeyAgentInstanceID] = string(principal.AgentInstanceID)
	}
	setRequestMDC(c, fields)
}

func setApprovalRequest(c *gin.Context, requestID domain.ID) {
	setRequestMDC(c, loggermdc.Fields{loggermdc.KeyApprovalRequestID: string(requestID)})
}

func setRequestMDC(c *gin.Context, fields loggermdc.Fields) {
	c.Request = c.Request.WithContext(loggermdc.With(c.Request.Context(), fields))
}

func bind(c *gin.Context, destination any) bool {
	if err := c.ShouldBindJSON(destination); err != nil {
		writeBindError(c, err)
		return false
	}
	return true
}

func bindStrict(c *gin.Context, destination any) bool {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeBindError(c, err)
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("request body must contain one JSON value")
		}
		writeBindError(c, err)
		return false
	}
	if err := binding.Validator.ValidateStruct(destination); err != nil {
		writeBindError(c, err)
		return false
	}
	return true
}

func writeBindError(c *gin.Context, err error) {
	writeAPIError(c, http.StatusBadRequest, "invalid_request", "invalid_request", err.Error(), nil)
}

func (a *API) writeError(c *gin.Context, err error) { WriteError(c, a.logger, err) }

// WriteError maps a domain error onto the shared error envelope and aborts
// the request. Exported so an edition's additive routes answer with the
// same statuses and codes as core's.
func WriteError(c *gin.Context, logger *slog.Logger, err error) {
	status, errorType, code := http.StatusInternalServerError, "internal", "internal_error"
	var details map[string]any
	var quota domain.QuotaExceeded
	var denied authorization.Denied
	var routing approvals.RoutingRejection
	var pathInUse domain.EscalationPathInUseError
	var pathsInactive domain.EscalationPathsInactiveError
	var endpointInUse domain.WebhookEndpointInUseError
	var pipelineInUse domain.RequestApprovalPipelineInUseError
	var pipelinesInactive domain.PipelinesInactiveError
	var delegationCycle domain.PipelineDelegationCycleError
	var delegationTooDeep domain.PipelineDelegationTooDeepError
	var endpointsInactive domain.WebhookEndpointsInactiveError
	var memberInPath domain.MembershipInEscalationPathError
	var teamMemberInPolicy domain.TeamMemberInEscalationPolicyError
	var credentialRefused domain.CredentialRefusedError
	var unconfigured *blockruntime.Unconfigured
	var refused *blockruntime.Refused
	switch {
	case errors.As(err, &routing):
		status, errorType, code = http.StatusForbidden, "authorization", "forbidden"
		details = map[string]any{"reason": routing.Code}
	case errors.As(err, &denied):
		status, errorType, code = http.StatusForbidden, "authorization", "forbidden"
		details = map[string]any{"permission": denied.Permission, "resource_kind": denied.Resource.Kind}
		if denied.Resource.ID != "" {
			details["resource_id"] = denied.Resource.ID
		}
	case errors.Is(err, slackconnect.ErrNotConfigured):
		status, code = http.StatusNotImplemented, "slack_not_configured"
	case errors.Is(err, slackconnect.ErrNoSlackMatch):
		status, errorType, code = http.StatusNotFound, "not_found", "slack_no_match"
	case errors.Is(err, discordconnect.ErrNotConfigured):
		status, code = http.StatusNotImplemented, "discord_not_configured"
	case errors.Is(err, discordconnect.ErrNotConnected):
		status, errorType, code = http.StatusNotFound, "not_found", "discord_not_connected"
	case errors.As(err, &quota):
		status, errorType, code = http.StatusConflict, "conflict", "quota_exceeded"
		details = map[string]any{"dimension": quota.Dimension, "limit": quota.Limit, "used": quota.Used}
	case errors.Is(err, authn.ErrEnterpriseNotConfigured):
		status, code = http.StatusNotImplemented, "enterprise_not_configured"
	case errors.As(err, &unconfigured):
		status, errorType, code = http.StatusNotImplemented, "dependency", unconfigured.Code
	case errors.As(err, &refused):
		status, errorType, code = http.StatusBadRequest, "invalid_request", refused.Code
	case errors.Is(err, domain.ErrWebhookUnconfigured):
		status, errorType, code = http.StatusNotImplemented, "dependency", "webhook_unconfigured"
	case errors.Is(err, domain.ErrUnsupportedDeliveryType):
		status, errorType, code = http.StatusBadRequest, "invalid_request", "unsupported_delivery_type"
	case errors.Is(err, domain.ErrWebhookVerificationFailed):
		status, errorType, code = http.StatusUnprocessableEntity, "invalid_request", "webhook_verification_failed"
	case errors.Is(err, domain.ErrInvalidWebhookURL):
		status, errorType, code = http.StatusBadRequest, "invalid_request", "invalid_webhook_url"
	case errors.Is(err, domain.ErrInstanceDeleted):
		status, errorType, code = http.StatusConflict, "conflict", "instance_deleted"
	case errors.Is(err, domain.ErrIdempotencyInProgress):
		status, errorType, code = http.StatusConflict, "conflict", "idempotency_in_progress"
	case errors.Is(err, domain.ErrOnboardingExpired):
		status, errorType, code = http.StatusGone, "conflict", "onboarding_session_expired"
	case errors.Is(err, domain.ErrOnboardingState):
		status, errorType, code = http.StatusConflict, "conflict", "onboarding_session_state"
	case errors.As(err, &endpointInUse):
		status, errorType, code = http.StatusConflict, "conflict", "webhook_endpoint_in_use"
		details = map[string]any{"endpoint_key": endpointInUse.EndpointKey, "uses": newPipelineUseResponses(endpointInUse.Uses)}
	case errors.Is(err, domain.ErrEscalationPathExists):
		status, errorType, code = http.StatusConflict, "conflict", "escalation_path_exists"
	case errors.Is(err, domain.ErrToolPresentationExists):
		status, errorType, code = http.StatusConflict, "conflict", "tool_presentation_exists"
	case errors.Is(err, domain.ErrEscalationPathArchived):
		status, errorType, code = http.StatusConflict, "conflict", "escalation_path_archived"
	case errors.Is(err, domain.ErrAgentArchived):
		status, errorType, code = http.StatusConflict, "conflict", "agent_archived"
	case errors.Is(err, domain.ErrAgentOwnerRequired):
		status, errorType, code = http.StatusBadRequest, "invalid_request", "agent_owner_required"
	case errors.Is(err, domain.ErrPersonalAgent):
		status, errorType, code = http.StatusBadRequest, "invalid_request", "personal_agent"
	case errors.Is(err, domain.ErrAgentNotArchived):
		status, errorType, code = http.StatusConflict, "conflict", "agent_not_archived"
	case errors.Is(err, domain.ErrAgentSlugTaken):
		status, errorType, code = http.StatusConflict, "conflict", "agent_slug_taken"
	case errors.Is(err, domain.ErrAgentNameTaken):
		status, errorType, code = http.StatusConflict, "conflict", "agent_name_taken"
	case errors.Is(err, domain.ErrAgentInstanceNameTaken):
		status, errorType, code = http.StatusConflict, "conflict", "agent_instance_name_taken"
	case errors.Is(err, domain.ErrRequestApprovalPipelineArchived):
		status, errorType, code = http.StatusConflict, "conflict", "request_approval_pipeline_archived"
	case errors.Is(err, domain.ErrRequestApprovalPipelineExists):
		status, errorType, code = http.StatusConflict, "conflict", "request_approval_pipeline_exists"
	case errors.Is(err, domain.ErrRequestApprovalPipelineEntry):
		status, errorType, code = http.StatusConflict, "conflict", "request_approval_pipeline_entry"
	case errors.As(err, &pipelineInUse):
		status, errorType, code = http.StatusConflict, "conflict", "request_approval_pipeline_in_use"
		details = map[string]any{"pipeline_key": pipelineInUse.PipelineKey, "uses": newPipelineUseResponses(pipelineInUse.Uses)}
	case errors.As(err, &pipelinesInactive):
		status, errorType, code = http.StatusConflict, "conflict", "request_approval_pipeline_inactive"
		details = map[string]any{"pipelines": pipelinesInactive.PipelineKeys}
	case errors.As(err, &delegationCycle):
		status, errorType, code = http.StatusConflict, "conflict", "request_approval_pipeline_delegation_cycle"
		details = map[string]any{"pipelines": delegationCycle.PipelineKeys}
	case errors.As(err, &delegationTooDeep):
		status, errorType, code = http.StatusConflict, "conflict", "request_approval_pipeline_delegation_too_deep"
		details = map[string]any{"depth": delegationTooDeep.Depth, "maximum": domain.MaximumDelegationDepth}
	case errors.Is(err, domain.ErrEffectivePipelineTooLarge):
		status, errorType, code = http.StatusConflict, "conflict", "request_approval_pipeline_too_large"
	case errors.Is(err, domain.ErrRequestApprovalPipelineNotFound):
		status, errorType, code = http.StatusBadRequest, "invalid_request", "request_approval_pipeline_not_found"
	case errors.As(err, &pathInUse):
		status, errorType, code = http.StatusConflict, "conflict", "escalation_path_in_use"
		details = map[string]any{"path_key": pathInUse.PathKey, "uses": newPipelineUseResponses(pathInUse.Uses)}
	case errors.As(err, &pathsInactive):
		status, errorType, code = http.StatusConflict, "conflict", "escalation_path_inactive"
		details = map[string]any{"escalation_paths": pathsInactive.PathKeys}
	case errors.As(err, &memberInPath):
		status, errorType, code = http.StatusConflict, "conflict", "membership_in_escalation_path"
		details = map[string]any{
			"membership_id":            memberInPath.MembershipID,
			"escalation_paths":         newEscalationPathReferenceResponses(memberInPath.Paths),
			"team_escalation_policies": newTeamEscalationPolicyReferenceResponses(memberInPath.TeamPolicies),
		}
	case errors.As(err, &teamMemberInPolicy):
		status, errorType, code = http.StatusConflict, "conflict", "team_member_in_escalation_policy"
		details = map[string]any{
			"team_id":        teamMemberInPolicy.TeamID,
			"revision":       teamMemberInPolicy.Revision,
			"membership_ids": idsOrEmpty(teamMemberInPolicy.MembershipIDs),
		}
	case errors.As(err, &endpointsInactive):
		status, errorType, code = http.StatusConflict, "conflict", "webhook_endpoint_inactive"
		details = map[string]any{"webhook_endpoints": endpointsInactive.EndpointKeys}
	case errors.Is(err, domain.ErrWebhookEndpointNotFound):
		status, errorType, code = http.StatusBadRequest, "invalid_request", "webhook_endpoint_not_found"
	case errors.Is(err, domain.ErrRoleBuiltIn):
		status, errorType, code = http.StatusForbidden, "authorization", "role_built_in"
	case errors.Is(err, domain.ErrRoleAssigned):
		status, errorType, code = http.StatusConflict, "conflict", "role_in_use"
	case errors.Is(err, authn.ErrInvitationRequired):
		status, code = http.StatusForbidden, "invitation_required"
	case errors.Is(err, authn.ErrSSORequired):
		status, code = http.StatusForbidden, "sso_required"
	case errors.Is(err, authn.ErrNoOrganizations):
		status, code = http.StatusForbidden, "no_organizations"
	case errors.Is(err, authn.ErrNoAccount):
		status, errorType, code = http.StatusForbidden, "authorization", "no_account"
	case errors.Is(err, authn.ErrEmailUnverified):
		status, errorType, code = http.StatusForbidden, "authorization", "email_unverified"
	case errors.Is(err, domain.ErrEmailMismatch):
		status, code = http.StatusForbidden, "invitation_email_mismatch"
	case errors.Is(err, authn.ErrPasswordMismatch):
		status, errorType, code = http.StatusForbidden, "authorization", "password_mismatch"
	case errors.Is(err, authn.ErrInvalidCredentials):
		status, errorType, code = http.StatusUnauthorized, "authentication", "invalid_credentials"
	case errors.Is(err, domain.ErrInvalid):
		status, errorType, code = http.StatusBadRequest, "invalid_request", "invalid_request"
	case errors.As(err, &credentialRefused):
		status, errorType, code = http.StatusUnauthorized, "authentication", credentialRefused.Reason
	case errors.Is(err, domain.ErrUnauthenticated):
		status, errorType, code = http.StatusUnauthorized, "authentication", "unauthenticated"
	case errors.Is(err, domain.ErrPlanRequired):
		status, errorType, code = http.StatusForbidden, "authorization", "plan_required"
	case errors.Is(err, domain.ErrAPIKeysDisabled):
		status, errorType, code = http.StatusForbidden, "authorization", "api_keys_disabled"
	case errors.Is(err, domain.ErrAPIKeyScopingUnavailable):
		status, errorType, code = http.StatusForbidden, "authorization", "api_key_scoping_unavailable"
	case errors.Is(err, domain.ErrCustomPermissionsUnavailable):
		status, errorType, code = http.StatusForbidden, "authorization", "custom_permissions_unavailable"
	case errors.Is(err, domain.ErrAPIKeyNotAllowed):
		status, errorType, code = http.StatusForbidden, "authorization", "api_key_not_allowed"
	case errors.Is(err, domain.ErrForbidden), errors.Is(err, domain.ErrMembershipInactive), errors.Is(err, domain.ErrAgentInactive), errors.Is(err, domain.ErrAuthenticationStale), errors.Is(err, domain.ErrAuthenticationWeak), errors.Is(err, domain.ErrNotTargeted):
		status, errorType, code = http.StatusForbidden, "authorization", "forbidden"
	case errors.Is(err, domain.ErrNotFound):
		status, errorType, code = http.StatusNotFound, "not_found", "not_found"
	case errors.Is(err, domain.ErrAlreadyTerminal):
		status, errorType, code = http.StatusConflict, "conflict", "already_terminal"
	case errors.Is(err, domain.ErrIdempotencyConflict):
		status, errorType, code = http.StatusConflict, "conflict", "idempotency_conflict"
	case errors.Is(err, domain.ErrConflict):
		status, errorType, code = http.StatusConflict, "conflict", "conflict"
	case errors.Is(err, domain.ErrPreconditionFailed):
		status, errorType, code = http.StatusPreconditionFailed, "conflict", "active_revision_changed"
	case errors.Is(err, domain.ErrPreconditionRequired):
		status, errorType, code = http.StatusPreconditionRequired, "invalid_request", "if_match_required"
	case errors.Is(err, domain.ErrLimitExceeded):
		status, errorType, code = http.StatusTooManyRequests, "rate_limit", "limit_exceeded"
	case errors.Is(err, context.DeadlineExceeded):
		status, errorType, code = http.StatusGatewayTimeout, "dependency", "timeout"
	case errors.Is(err, context.Canceled):
		status, errorType, code = 499, "dependency", "canceled"
	}
	message := err.Error()
	if errorType == "internal" {
		logger.ErrorContext(c.Request.Context(), fmt.Sprintf("http request failed - %s %s", c.Request.Method, c.Request.URL.Path), "error", err)
		message = "An internal error occurred."
	}
	writeAPIError(c, status, errorType, code, message, details)
}

func writeAPIError(c *gin.Context, status int, errorType, code, message string, details map[string]any) {
	requestID := c.Writer.Header().Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.NewString()
		c.Header("X-Request-ID", requestID)
	}
	body := gin.H{"type": errorType, "code": code, "message": message, "request_id": requestID}
	if details != nil {
		body["details"] = details
	}
	c.AbortWithStatusJSON(status, gin.H{"error": body})
}

// queryTime parses an optional RFC 3339 query parameter; absence is the
// zero time and a malformed value is an invalid request.
func queryInt(c *gin.Context, key string, fallback int) int {
	value, err := strconv.Atoi(c.Query(key))
	if err != nil {
		return fallback
	}
	return value
}

// queryBool reads a boolean flag from the query string; absent or
// unparseable means false.
func queryBool(c *gin.Context, key string) bool {
	value, err := strconv.ParseBool(c.Query(key))
	return err == nil && value
}

func parseWait(value string, maximum time.Duration) time.Duration {
	if value == "" {
		return 0
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < 0 {
		return 0
	}
	if duration > maximum {
		return maximum
	}
	return duration
}

func (a *API) installFrontend(router *gin.Engine) {
	var fileServer http.Handler
	if a.frontend != nil {
		fileServer = http.FileServer(http.FS(a.frontend))
	}
	router.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if a.frontend == nil || strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/auth/") || path == "/livez" || path == "/readyz" || path == "/metrics" {
			writeAPIError(c, http.StatusNotFound, "not_found", "route_not_found", "Route not found.", nil)
			return
		}
		if strings.Contains(path, ".") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}
		contents, err := fs.ReadFile(a.frontend, "index.html")
		if err != nil {
			a.writeError(c, err)
			return
		}
		c.Header("Cache-Control", "no-cache")
		c.Data(http.StatusOK, "text/html; charset=utf-8", contents)
	})
}

func (a *API) previewAgentEnrollment(c *gin.Context) {
	var request struct {
		UserCode string `json:"user_code" binding:"required"`
	}
	if !bindStrict(c, &request) {
		return
	}
	result, err := a.auth.PreviewAgentEnrollment(c.Request.Context(), currentPrincipal(c), request.UserCode)
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (a *API) listEnrollmentAgents(c *gin.Context) {
	result, err := a.auth.ListEnrollmentAgents(c.Request.Context(), currentPrincipal(c), c.Query("q"), queryInt(c, "offset", 0), queryInt(c, "limit", 25))
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
