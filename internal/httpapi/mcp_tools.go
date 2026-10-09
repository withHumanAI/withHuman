package httpapi

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/withHumanAI/withHuman/approvalblock"
	"github.com/withHumanAI/withHuman/internal/mcpreference"
	"github.com/withHumanAI/withHuman/mcptools"
)

// Core's tool catalog. Every tool is a thin, well-described door onto the
// product API: inputs mirror the HTTP request bodies, results are the
// HTTP answers, and the compositions (save then activate) exist because
// an agent should not have to choreograph If-Match by hand. Routes a key
// can never use (a member's role, permission policy writes,
// invitations) have no tool: handing out authority stays a person's act
// in the browser.

const (
	mcpAgentsPath         = "/api/v1/agents"
	mcpAgentInstancesPath = "/api/v1/agent_instances"
	mcpProvisionersPath   = "/api/v1/agent-provisioners"
	mcpCredentialsPath    = "/api/v1/agent-credentials"
	mcpToolCatalogPath    = "/api/v1/tool_catalog"
	mcpToolDefinitions    = "/api/v1/tool_definitions"
	mcpMembersPath        = "/api/v1/members"
	mcpRolesPath          = "/api/v1/roles"
	mcpPermissionsPath    = "/api/v1/permissions"
	mcpTeamsPath          = "/api/v1/teams"
	mcpPipelinesPath      = "/api/v1/request-approval-pipelines"
	mcpPathsPath          = "/api/v1/escalation-paths"
	mcpWebhooksPath       = "/api/v1/webhook_endpoints"
)

type mcpStatusInput struct {
	Status string `json:"status,omitempty" jsonschema:"Which to list: live (the default), archived, or all."`
}

type mcpAgentSlugInput struct {
	AgentSlug string `json:"agent_slug" jsonschema:"The agent's slug, from list_agents."`
}

type mcpGetAgentInput struct {
	AgentSlug         string `json:"agent_slug" jsonschema:"The agent's slug, from list_agents."`
	InstanceQ         string `json:"instance_q,omitempty" jsonschema:"Only instances whose name contains this text."`
	InstanceStatus    string `json:"instance_status,omitempty" jsonschema:"Only instances that are active or disabled."`
	InstanceOffset    int64  `json:"instance_offset,omitempty" jsonschema:"How many matching instances to skip; instances come 25 to a page."`
	ProvisionerStatus string `json:"provisioner_status,omitempty" jsonschema:"Only provisioner tokens that are active, expired or revoked."`
	ProvisionerOffset int64  `json:"provisioner_offset,omitempty" jsonschema:"How many matching provisioner tokens to skip; tokens come 25 to a page, newest first."`
}

type mcpCreateAgentInput struct {
	Slug string `json:"slug" jsonschema:"Permanent identifier: lowercase letters, digits, dots, underscores or dashes, unique for good (for example support-assistant)."`
	Name string `json:"name" jsonschema:"Display name, unique among live agents."`
	Kind string `json:"kind,omitempty" jsonschema:"service (the default): an agent the organization runs, whose instances belong to nobody. personal: one people run for themselves, such as a coding assistant; each instance belongs to the member who enrolled it. Fixed for good."`
}

type mcpUpdateAgentInput struct {
	AgentSlug string `json:"agent_slug" jsonschema:"The agent's slug, from list_agents."`
	Name      string `json:"name,omitempty" jsonschema:"A new display name, unique among live agents. Omit to keep the current one."`
	Status    string `json:"status,omitempty" jsonschema:"active or disabled. Disabling keeps every credential but refuses them all until the agent is active again. Omit to keep the current status."`
}

type mcpUpdateAgentInstanceInput struct {
	InstanceID string `json:"instance_id" jsonschema:"The instance's id, from get_agent."`
	Name       string `json:"name,omitempty" jsonschema:"A new name, unique within the agent. Requests already made keep the name they were made with. Omit to keep the current one."`
	Status     string `json:"status,omitempty" jsonschema:"active or disabled. Disabling refuses the instance's credentials until it is active again. Omit to keep the current status."`
}

type mcpCreateAgentInstanceInput struct {
	AgentSlug string         `json:"agent_slug" jsonschema:"The agent's slug, from list_agents."`
	Name      string         `json:"name" jsonschema:"A name for the instance, unique within the agent. Reviewers see it next to the agent's name."`
	Metadata  map[string]any `json:"metadata,omitempty" jsonschema:"Any JSON object to store with the instance, such as a region or pod name."`
}

type mcpProvisionerInput struct {
	ProvisionerID string `json:"provisioner_id" jsonschema:"The provisioner token's id, from get_agent."`
}

type mcpCredentialInput struct {
	CredentialID string `json:"credential_id" jsonschema:"The credential's id, from get_agent."`
}

type mcpToolCatalogInput struct {
	AgentSlug string `json:"agent_slug,omitempty" jsonschema:"Only tools this agent's instances or sources offer."`
	Q         string `json:"q,omitempty" jsonschema:"Only tools whose name or description contains this text."`
	Source    string `json:"source,omitempty" jsonschema:"Only tools from this source: builtin, mcp, or a source the deployment adds, such as gateway."`
	Status    string `json:"status,omitempty" jsonschema:"Only tools that are available, unavailable or stale."`
	Offset    int64  `json:"offset,omitempty" jsonschema:"How many matching tools to skip; tools come 50 to a page."`
	Refresh   bool   `json:"refresh,omitempty" jsonschema:"Bypass the deployment's five-minute discovery cache for its own tool source. Tools a runtime reports locally refresh only through the CLI."`
}

type mcpToolCatalogEntryInput struct {
	ID        string `json:"id" jsonschema:"The catalogue entry's id, from list_tool_catalog."`
	AgentSlug string `json:"agent_slug,omitempty" jsonschema:"Read the entry as this agent sees it."`
}

type mcpMembershipInput struct {
	MembershipID string `json:"membership_id" jsonschema:"The membership id, from list_members."`
}

type mcpUpdateMemberInput struct {
	MembershipID string `json:"membership_id" jsonschema:"The membership id, from list_members."`
	Status       string `json:"status" jsonschema:"active to reactivate a suspended member, or suspended to sign them out everywhere and refuse their sessions, keys and agents until reactivated."`
	Force        bool   `json:"force,omitempty" jsonschema:"Suspend even while an active escalation path names the member. Without it the call is refused and lists the paths."`
}

type mcpRemoveMemberInput struct {
	MembershipID string `json:"membership_id" jsonschema:"The membership id, from list_members."`
	Force        bool   `json:"force,omitempty" jsonschema:"Remove even while an active escalation path names the member. Without it the call is refused and lists the paths."`
}

type mcpTeamIDInput struct {
	TeamID string `json:"team_id" jsonschema:"The team's id, from list_teams."`
}

type mcpCreateTeamInput struct {
	Name          string   `json:"name" jsonschema:"The team's name, unique among live teams."`
	MembershipIDs []string `json:"membership_ids,omitempty" jsonschema:"Membership ids (from list_members) to place on the team right away."`
}

type mcpRenameTeamInput struct {
	TeamID string `json:"team_id" jsonschema:"The team's id, from list_teams."`
	Name   string `json:"name" jsonschema:"The new name, unique among live teams."`
}

type mcpSetTeamMembersInput struct {
	TeamID        string   `json:"team_id" jsonschema:"The team's id, from list_teams."`
	MembershipIDs []string `json:"membership_ids" jsonschema:"The complete manual member list as membership ids from list_members; it replaces the current one, so include members to keep. Members a directory sync placed on the team are unaffected."`
	Force         bool     `json:"force,omitempty" jsonschema:"Also remove members the team's active escalation policy still names. Without it the call is refused and lists them."`
}

type mcpTeamRevisionInput struct {
	TeamID   string `json:"team_id" jsonschema:"The team's id, from list_teams."`
	Revision int64  `json:"revision" jsonschema:"The policy revision number, from list_team_escalation_policy_revisions."`
}

type mcpTeamEscalationPolicyInput struct {
	TeamID       string           `json:"team_id" jsonschema:"The team's id, from list_teams."`
	Nodes        []map[string]any `json:"nodes" jsonschema:"The policy's nodes in order, in the escalation path grammar, except that level targets are user targets naming current team members (membership ids from get_team) or one broadcast target ({type: broadcast}, no id) for every member. Every node needs a UUID id. See read_reference topic escalation_paths."`
	WorkingHours []map[string]any `json:"working_hours,omitempty" jsonschema:"Named working-hours sets that defer nodes and working_hours conditions refer to by id, as an escalation path takes them."`
	Repeat       map[string]any   `json:"repeat,omitempty" jsonschema:"What to do when every level has been paged and nobody answered: {times: 1..9, after: '15m'} walks the levels again."`
	Activate     *bool            `json:"activate,omitempty" jsonschema:"Activate the new revision right away. Defaults to true."`
}

type mcpPipelineKeyInput struct {
	PipelineKey string `json:"pipeline_key" jsonschema:"The pipeline's key. 'entry' is the entry pipeline every request starts in; any other key is a pipeline a delegate block hands requests to."`
}

type mcpPipelineRevisionInput struct {
	PipelineKey string `json:"pipeline_key" jsonschema:"The pipeline's key ('entry' for the entry pipeline)."`
	Revision    int64  `json:"revision" jsonschema:"The revision number, from list_pipeline_revisions."`
}

type mcpCreatePipelineInput struct {
	PipelineKey           string           `json:"pipeline_key" jsonschema:"The new pipeline's key: lowercase letters, digits, dots, underscores and hyphens, up to 63 characters. It is taken for good, archived pipelines included, and can never be 'entry'."`
	Name                  string           `json:"name" jsonschema:"The pipeline's display name, for example Payments."`
	Blocks                []map[string]any `json:"blocks,omitempty" jsonschema:"The first revision's blocks, in the shape update_pipeline takes. Omit or pass an empty list for a pipeline that passes every request that enters it to human review until it is edited."`
	DefaultEscalationPath string           `json:"default_escalation_path,omitempty" jsonschema:"Escalation path key for requests this pipeline hands to a person without a block naming a path, including requests that run out of it. Omit to inherit the defaults of the pipelines that delegate into it."`
}

type mcpUpdatePipelineInput struct {
	PipelineKey           string           `json:"pipeline_key" jsonschema:"The pipeline to replace ('entry' for the entry pipeline)."`
	Name                  string           `json:"name,omitempty" jsonschema:"A new display name for the pipeline. Omit to keep the current one."`
	Blocks                []map[string]any `json:"blocks" jsonschema:"The complete ordered block list for the new revision (it replaces the current blocks, so include the ones to keep). Each block: id (key-shaped, unique across the whole pipeline including blocks inside branches), name, type (outcome | branch | delegate | webhook, or a kind this deployment adds; see read_reference), enabled: true, timeout (for example 50ms), max_attempts (1 for outcome, branch and delegate), and config for its type; a branch holds its own ordered blocks in config.blocks, a delegate names the pipeline it hands matching requests to in config.pipeline_key. See read_reference topic pipeline_blocks. An empty list sends every request to human review."`
	DefaultEscalationPath string           `json:"default_escalation_path,omitempty" jsonschema:"Escalation path key that takes human exits which name no path of their own, and requests the pipeline hands to a person by running out of blocks. Omit to inherit the defaults of the pipelines that delegate into this one, or, for the entry pipeline, to leave them to any authorized reviewer."`
	DefaultAskAgentOwner  bool             `json:"default_ask_agent_owner,omitempty" jsonschema:"Send those requests to the person who owns the agent instance that made them instead, with default_escalation_path, if set, as the fallback for requests from agents without an owner (service agents); without it they go to any authorized reviewer."`
	Activate              *bool            `json:"activate,omitempty" jsonschema:"Activate the new revision right away so new requests use it. Defaults to true. False saves a draft revision you can activate later with activate_pipeline_revision."`
}

type mcpPreviewSample struct {
	Tool           string         `json:"tool" jsonschema:"The tool name the sample request asks for, for example Bash or stripe__refund."`
	Arguments      map[string]any `json:"arguments,omitempty" jsonschema:"The tool call's arguments as the agent would send them."`
	AgentReasoning string         `json:"agent_reasoning,omitempty" jsonschema:"What the agent said it was trying to do, for blocks that include the agent's reasoning."`
	Context        map[string]any `json:"context,omitempty" jsonschema:"Extra request context, if the adapter would send any."`
	AgentSlug      string         `json:"agent_slug,omitempty" jsonschema:"The requesting agent's slug for the sample. Defaults to 'agent'."`
	AgentName      string         `json:"agent_name,omitempty" jsonschema:"The requesting agent's display name for the sample. Defaults to the slug."`
}

type mcpPreviewPipelineInput struct {
	PipelineKey           string           `json:"pipeline_key" jsonschema:"The pipeline the blocks stand in for ('entry' for the entry pipeline). Delegate blocks expand to their targets' current active revisions."`
	Blocks                []map[string]any `json:"blocks" jsonschema:"The blocks to run the sample through, in order; the same shape update_pipeline takes. Pass the current blocks from get_pipeline to test what is active."`
	DefaultEscalationPath string           `json:"default_escalation_path,omitempty" jsonschema:"The default escalation path to assume, as update_pipeline takes it."`
	DefaultAskAgentOwner  bool             `json:"default_ask_agent_owner,omitempty" jsonschema:"Whether the default asks the agent's owner first, as update_pipeline takes it."`
	Sample                mcpPreviewSample `json:"sample" jsonschema:"The request to run through the blocks. Nothing is created: no approval request, no notification."`
}

type mcpPathKeyInput struct {
	PathKey string `json:"path_key" jsonschema:"The escalation path's key."`
}

type mcpPathRevisionInput struct {
	PathKey  string `json:"path_key" jsonschema:"The escalation path's key."`
	Revision int64  `json:"revision" jsonschema:"The revision number, from list_escalation_path_revisions."`
}

type mcpEscalationPathInput struct {
	PathKey      string           `json:"path_key" jsonschema:"Stable key, lowercase letters, digits, dots, underscores or dashes (for example payments-review). It names the path from pipeline blocks and is never reused, even after archiving."`
	Name         string           `json:"name" jsonschema:"Display name people see, for example Payments review."`
	Nodes        []map[string]any `json:"nodes" jsonschema:"The path's nodes in order: level nodes name targets and how long to wait (escalate_after) before the next level, if_else nodes branch on conditions, defer nodes wait for working hours. Every node needs a UUID id. See read_reference topic escalation_paths."`
	WorkingHours []map[string]any `json:"working_hours,omitempty" jsonschema:"Named working-hours sets that defer nodes and working_hours conditions refer to by id: {id, timezone (IANA), intervals: [{weekdays: [mon, ...], start: '09:00', end: '17:30'}]}."`
	Repeat       map[string]any   `json:"repeat,omitempty" jsonschema:"What to do when every level has been paged and nobody answered: {times: 1..9, after: '15m'} walks the levels again."`
	Activate     *bool            `json:"activate,omitempty" jsonschema:"Activate the new revision right away. Defaults to true. A path with no active revision cannot be named by an active pipeline."`
}

type mcpEndpointKeyInput struct {
	EndpointKey string `json:"endpoint_key" jsonschema:"The webhook endpoint's key."`
}

type mcpEndpointRevisionInput struct {
	EndpointKey string `json:"endpoint_key" jsonschema:"The webhook endpoint's key."`
	Revision    int64  `json:"revision" jsonschema:"The revision number, from list_webhook_endpoint_revisions."`
}

type mcpTestWebhookEndpointInput struct {
	EndpointKey string `json:"endpoint_key" jsonschema:"The webhook endpoint's key."`
	Revision    int64  `json:"revision,omitempty" jsonschema:"The revision whose URL to test, from list_webhook_endpoint_revisions. Omit to test the active revision; a draft can be tested before it is activated."`
}

type mcpSaveWebhookEndpointInput struct {
	EndpointKey string `json:"endpoint_key" jsonschema:"Stable key a pipeline's webhook block names (config.endpoint_key). A new key creates the endpoint and mints its signing secret; an existing key gets a new revision and keeps its secret."`
	Name        string `json:"name" jsonschema:"Display name."`
	URL         string `json:"url" jsonschema:"Public HTTPS URL withHuman posts the signed request to."`
	Activate    *bool  `json:"activate,omitempty" jsonschema:"Activate the new revision right away. Defaults to true."`
}

type mcpReferenceInput struct {
	Topic string `json:"topic" jsonschema:"One of: pipeline_blocks (block envelope and each type's config), conditions (the condition tree branches, delegates and request conditions use: fields, operators, values), escalation_paths (path and team escalation policy documents: nodes, targets, timing, working hours, repeat)."`
}

func (a *API) registerMCPTools(catalog *mcptools.Catalog) {
	registerMCPAgentTools(catalog)
	registerMCPMemberTools(catalog)
	registerMCPTeamTools(catalog)
	registerMCPPipelineTools(catalog)
	registerMCPEscalationPathTools(catalog)
	registerMCPWebhookTools(catalog)

	// Reference.
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "read_reference",
		Description: "Read the exact grammar for pipeline blocks, conditions, or escalation path and team escalation policy documents. Read the relevant topic before writing one: unknown fields are refused and ids must be UUIDs.",
		Annotations: mcptools.ReadOnly,
	}, func(_ *mcptools.Call, input mcpReferenceInput) (*mcp.CallToolResult, any, error) {
		text, err := mcpreference.Read(input.Topic)
		if err != nil {
			return nil, nil, err
		}
		// The edition's block kinds append their grammar; their valid
		// dependencies (a judge model catalog) are deployment configuration
		// the edition renders into it.
		if strings.TrimSpace(input.Topic) == "pipeline_blocks" && a.compiledPipelines != nil {
			text += a.compiledPipelines.Grammar()
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
	})
}

func registerMCPAgentTools(catalog *mcptools.Catalog) {
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "list_agents",
		Description: "List the organization's agents: slug (what pipelines and conditions refer to), name, kind, status, and recent request counts.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpStatusInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(mcpAgentsPath, mcptools.Query("status", input.Status)))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "get_agent",
		Description: "Read one agent: its kind and status, a page of its instances (id, name, owner, status, liveness, credentials) and a page of its provisioner tokens. Instance and token ids are what update_agent_instance, revoke_agent_credential and revoke_provisioner_token take.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpGetAgentInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(agentPath(input.AgentSlug), mcptools.Query(
			"instance_q", input.InstanceQ, "instance_status", input.InstanceStatus, "instance_offset", mcptools.Number(input.InstanceOffset),
			"provisioner_status", input.ProvisionerStatus, "provisioner_offset", mcptools.Number(input.ProvisionerOffset),
		)))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "create_agent",
		Description: "Create an agent. Nothing reaches it until a runtime is connected: with the withhuman CLI on the machine the agent runs on, or with create_agent_instance or create_provisioner_token for a service you run. Every request it makes starts in the entry pipeline; give it rules with a branch on /agent/slug there, or a delegate to a pipeline of its own.",
		Annotations: mcptools.Additive,
	}, func(call *mcptools.Call, input mcpCreateAgentInput) (*mcp.CallToolResult, any, error) {
		body := map[string]any{"slug": input.Slug, "name": input.Name}
		if input.Kind != "" {
			body["kind"] = input.Kind
		}
		return mcptools.Answer(call.Post(mcpAgentsPath, body, nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "update_agent",
		Description: "Rename an agent or switch it between active and disabled. Disabling refuses every instance's credentials until the agent is active again, without revoking them. The slug never changes.",
		Annotations: mcptools.Replacing,
	}, func(call *mcptools.Call, input mcpUpdateAgentInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Patch(agentPath(input.AgentSlug), presentFields("name", input.Name, "status", input.Status)))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "archive_agent",
		Description: "Archive an agent: every live credential and provisioner token is revoked for good, no instance can be enrolled under it, and its name is freed while its slug stays taken. Instances and history stay readable. restore_agent brings the agent back, but revoked credentials stay revoked.",
		Annotations: mcptools.Destructive,
	}, func(call *mcptools.Call, input mcpAgentSlugInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Delete(agentPath(input.AgentSlug), nil, nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "restore_agent",
		Description: "Restore an archived agent to active. Credentials and provisioner tokens revoked when it was archived stay revoked; its instances can be enrolled again.",
		Annotations: mcptools.Replacing,
	}, func(call *mcptools.Call, input mcpAgentSlugInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Post(agentPath(input.AgentSlug)+"/restore", nil, nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "update_agent_instance",
		Description: "Rename one instance of an agent or switch it between active and disabled. Disabling refuses the instance's credentials until it is active again, without revoking them.",
		Annotations: mcptools.Replacing,
	}, func(call *mcptools.Call, input mcpUpdateAgentInstanceInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Patch(mcpAgentInstancesPath+"/"+mcptools.Segment(input.InstanceID), presentFields("name", input.Name, "status", input.Status)))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name: "create_agent_instance",
		Description: "Register an instance of a service agent and mint its credential, for a runtime the withhuman CLI does not install (a service of yours speaking the Agent Approval Protocol). " +
			"The answer carries the credential exactly once: hand it to whoever configures that runtime, because it cannot be read back. Personal agents' instances belong to the member who enrolls them through the CLI.",
		Annotations: mcptools.Additive,
	}, func(call *mcptools.Call, input mcpCreateAgentInstanceInput) (*mcp.CallToolResult, any, error) {
		body := map[string]any{"name": input.Name}
		if input.Metadata != nil {
			body["metadata"] = input.Metadata
		}
		return mcptools.Answer(call.Post(agentPath(input.AgentSlug)+"/instances", body, nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name: "create_provisioner_token",
		Description: "Create a provisioner token for a service agent, which lets a fleet or platform create instances of the agent on its own, each with a credential of its own, through the Agent Approval Protocol. It cannot make approval requests itself. " +
			"The answer carries the token exactly once: hand it to whoever runs the platform, because it cannot be read back.",
		Annotations: mcptools.Additive,
	}, func(call *mcptools.Call, input mcpAgentSlugInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Post(agentPath(input.AgentSlug)+"/provisioners", nil, nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "revoke_provisioner_token",
		Description: "Revoke a provisioner token for good. Instances it already created keep their own credentials.",
		Annotations: mcptools.Destructive,
	}, func(call *mcptools.Call, input mcpProvisionerInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Delete(mcpProvisionersPath+"/"+mcptools.Segment(input.ProvisionerID), nil, nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "revoke_agent_credential",
		Description: "Revoke one instance credential for good: the runtime holding it can no longer make requests. To pause an instance instead, disable it with update_agent_instance.",
		Annotations: mcptools.Destructive,
	}, func(call *mcptools.Call, input mcpCredentialInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Delete(mcpCredentialsPath+"/"+mcptools.Segment(input.CredentialID), nil, nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "list_tool_catalog",
		Description: "List the tools agents have reported or can reach, one entry per tool and source, with its schema, the instances offering it and whether its source is available. Use it to learn the exact tool names and argument fields pipeline conditions test. The catalogue is advisory: it never changes how a request is decided.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpToolCatalogInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(mcpToolCatalogPath, mcptools.Query(
			"agent_slug", input.AgentSlug, "q", input.Q, "source", input.Source, "status", input.Status, "offset", mcptools.Number(input.Offset), "refresh", flag(input.Refresh),
		)))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "get_tool_catalog_entry",
		Description: "Read one tool catalogue entry with its full input schema and description.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpToolCatalogEntryInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(mcpToolCatalogPath+"/"+mcptools.Segment(input.ID), mcptools.Query("agent_slug", input.AgentSlug)))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "list_tool_definitions",
		Description: "List every distinct tool definition across agents in one answer (name, server, schema and description, with the runtimes and instances offering it): the vocabulary to write pipeline conditions against.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, _ struct{}) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(mcpToolDefinitions, nil))
	})
}

func registerMCPMemberTools(catalog *mcptools.Catalog) {
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "list_members",
		Description: "List the organization's members with the membership id (the id an escalation path's user target carries), display name, email, status, roles and team ids.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, _ struct{}) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(mcpMembersPath, nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "update_member",
		Description: "Suspend a member (signs them out everywhere and refuses their sessions, API keys and agents) or reactivate a suspended one. Refused for your own membership, the last active owner, and members your directory manages. Requires a personal API key.",
		Annotations: mcptools.Replacing,
	}, func(call *mcptools.Call, input mcpUpdateMemberInput) (*mcp.CallToolResult, any, error) {
		body := map[string]any{"status": input.Status}
		if input.Force {
			body["force"] = true
		}
		return mcptools.Answer(call.Patch(mcpMembersPath+"/"+mcptools.Segment(input.MembershipID), body))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "remove_member",
		Description: "Remove a member from the organization for good: their sessions, API keys and agent instances stop working. Refused for your own membership, the last active owner, and members your directory manages. Requires a personal API key.",
		Annotations: mcptools.Destructive,
	}, func(call *mcptools.Call, input mcpRemoveMemberInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Delete(mcpMembersPath+"/"+mcptools.Segment(input.MembershipID), mcptools.Query("force", flag(input.Force)), nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "list_roles",
		Description: "List the predefined roles (owner, admin, reviewer, auditor) with the permissions each grants. Assigning roles and permission policies is done by a person in the app. Requires a personal API key.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, _ struct{}) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(mcpRolesPath, nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "list_permissions",
		Description: "List every permission key this deployment knows, with what it allows, who can hold it, the kind of resource it applies to and whether it is dangerous. Use it to tell a person which grant to make, for example pipeline.write at pipeline:<key> for a team that owns a delegated pipeline. Requires a personal API key that can read permission policies.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, _ struct{}) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(mcpPermissionsPath, nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "list_member_permission_policies",
		Description: "List the permission policies granted directly to one member: each policy's permissions, the scope they apply at and its revision. Grants the member inherits from teams are on list_team_permission_policies. Requires a personal API key.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpMembershipInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(mcpMembersPath+"/"+mcptools.Segment(input.MembershipID)+"/permission_policies", nil))
	})
}

func registerMCPTeamTools(catalog *mcptools.Catalog) {
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "list_teams",
		Description: "List teams with their id (what an escalation path's team target carries), name, member count and whether an escalation policy is active.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpStatusInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(mcpTeamsPath, mcptools.Query("status", input.Status)))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "get_team",
		Description: "Read one team and its members.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpTeamIDInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(teamPath(input.TeamID), nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "create_team",
		Description: "Create a team, optionally with its first members. Teams are escalation targets: a level that names a team reaches every current member, or runs the team's escalation policy when it has one. Requires a personal API key; organization keys cannot manage teams.",
		Annotations: mcptools.Additive,
	}, func(call *mcptools.Call, input mcpCreateTeamInput) (*mcp.CallToolResult, any, error) {
		response, err := call.Post(mcpTeamsPath, map[string]any{"name": input.Name}, nil)
		if err != nil {
			return nil, nil, err
		}
		if len(input.MembershipIDs) == 0 {
			return mcptools.Result(response.Body)
		}
		var team struct {
			Team json.RawMessage `json:"team"`
		}
		var identity struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(response.Body, &team); err != nil || json.Unmarshal(team.Team, &identity) != nil || identity.ID == "" {
			return nil, nil, fmt.Errorf("the team was created but its id could not be read; call list_teams and set_team_members")
		}
		members, err := call.Put(teamPath(identity.ID)+"/members", map[string]any{"membership_ids": input.MembershipIDs})
		if err != nil {
			return nil, nil, fmt.Errorf("team %s was created but its members were not set: %w", identity.ID, err)
		}
		var roster struct {
			Members json.RawMessage `json:"members"`
		}
		if err := json.Unmarshal(members.Body, &roster); err != nil {
			return nil, nil, err
		}
		return mcptools.Object(map[string]any{"team": team.Team, "members": roster.Members})
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "rename_team",
		Description: "Rename a team. Escalation paths name teams by id, so nothing else changes. Refused for a team your directory manages. Requires a personal API key.",
		Annotations: mcptools.Replacing,
	}, func(call *mcptools.Call, input mcpRenameTeamInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Patch(teamPath(input.TeamID), map[string]any{"name": input.Name}))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "archive_team",
		Description: "Archive a team: it leaves routing, its own and team-scoped grants are revoked and its escalation policy is deactivated; its roster and history stay readable and its name can be reused. Refused while an active escalation path or a pending request targets it, and for a team your directory manages. Requires a personal API key.",
		Annotations: mcptools.Destructive,
	}, func(call *mcptools.Call, input mcpTeamIDInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Delete(teamPath(input.TeamID), nil, nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "set_team_members",
		Description: "Replace a team's manual member list. Requires a personal API key; organization keys cannot manage teams.",
		Annotations: mcptools.Replacing,
	}, func(call *mcptools.Call, input mcpSetTeamMembersInput) (*mcp.CallToolResult, any, error) {
		ids := input.MembershipIDs
		if ids == nil {
			ids = []string{}
		}
		return mcptools.Answer(call.Put(teamPath(input.TeamID)+"/members", map[string]any{"membership_ids": ids, "force": input.Force}))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "list_team_permission_policies",
		Description: "List the permission policies a team holds, which every member inherits live: each policy's permissions, scope and revision. Requires a personal API key.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpTeamIDInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(teamPath(input.TeamID)+"/permission_policies", nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "get_team_escalation_policy",
		Description: "Read a team's active escalation policy: how a level targeting the team pages its members, with warnings for targets who have left. Not found when no policy is active; such a level then reaches every member at once.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpTeamIDInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(teamPolicyPath(input.TeamID), nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "list_team_escalation_policy_revisions",
		Description: "List a team's escalation policy revisions, newest first, with which one is active.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpTeamIDInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(teamPolicyPath(input.TeamID)+"/revisions", nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "get_team_escalation_policy_revision",
		Description: "Read one team escalation policy revision, active or not, with warnings for targets who have since left the team.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpTeamRevisionInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(teamPolicyPath(input.TeamID)+"/revisions/"+strconv.FormatInt(input.Revision, 10), nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name: "update_team_escalation_policy",
		Description: "Save a new revision of a team's escalation policy (the whole document again, not a diff) and activate it unless activate is false. " +
			"The policy orders how members are paged when a path level targets the team: levels name current members as user targets, or broadcast for everyone; every member a request reaches may still decide it. Call read_reference (escalation_paths) first.",
		Annotations: mcptools.Additive,
	}, func(call *mcptools.Call, input mcpTeamEscalationPolicyInput) (*mcp.CallToolResult, any, error) {
		base := teamPolicyPath(input.TeamID)
		created, err := call.Post(base+"/revisions", documentBody(input.Nodes, input.WorkingHours, input.Repeat), nil)
		if err != nil {
			return nil, nil, err
		}
		return call.SaveRevision(base, created.Body, input.Activate, "activate_team_escalation_policy_revision")
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "activate_team_escalation_policy_revision",
		Description: "Make an existing team escalation policy revision the active one (a rollback, or activating a draft). Refused when it names someone who has since left the team. Requests already routed keep the revision they pinned.",
		Annotations: mcptools.Replacing,
	}, func(call *mcptools.Call, input mcpTeamRevisionInput) (*mcp.CallToolResult, any, error) {
		activated, err := call.ActivateRevision(teamPolicyPath(input.TeamID), input.Revision)
		if err != nil {
			return nil, nil, err
		}
		return mcptools.Result(activated)
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "archive_team_escalation_policy",
		Description: "Deactivate a team's escalation policy, so a path level targeting the team reaches every member at once again. Its revisions stay and can be activated later.",
		Annotations: mcptools.Replacing,
	}, func(call *mcptools.Call, input mcpTeamIDInput) (*mcp.CallToolResult, any, error) {
		if _, err := call.DeactivateRevision(teamPolicyPath(input.TeamID)); err != nil {
			return nil, nil, err
		}
		return mcptools.Object(map[string]any{"team_id": input.TeamID, "active": false})
	})
}

func registerMCPPipelineTools(catalog *mcptools.Catalog) {
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "list_pipelines",
		Description: "List every approval pipeline: the entry pipeline every request starts in, then every pipeline a delegate block can hand requests to, each with its key, name, active revision number, the delegate blocks that hand requests to it, and history.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpStatusInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(mcpPipelinesPath, mcptools.Query("status", input.Status)))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "get_pipeline",
		Description: "Read a pipeline's active revision with its blocks in order and its default escalation path. Blocks come back in the shape update_pipeline accepts, so you can edit and resubmit them.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpPipelineKeyInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(pipelinePath(input.PipelineKey), nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name: "create_pipeline",
		Description: "Create a pipeline that a delegate block in another pipeline can hand matching requests to, with its first revision active at once. " +
			"Use it to give one team its own part of the approval flow: the entry pipeline decides which requests reach it, the new pipeline decides what happens to them. A person then grants the team pipeline.read, pipeline.write and pipeline.activate at pipeline:<key> in the app so they can edit it and nothing else.",
		Annotations: mcptools.Additive,
	}, func(call *mcptools.Call, input mcpCreatePipelineInput) (*mcp.CallToolResult, any, error) {
		body := map[string]any{"pipeline_key": input.PipelineKey, "name": input.Name, "blocks": writableBlocks(input.Blocks)}
		if input.DefaultEscalationPath != "" {
			body["default_escalation_path"] = input.DefaultEscalationPath
		}
		return mcptools.Answer(call.Post(mcpPipelinesPath, body, nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "archive_pipeline",
		Description: "Archive a pipeline: its revisions stay readable but none is active, its key is taken for good, and no request can reach it. Refused for the entry pipeline and while any active revision still delegates to it (list_pipeline_uses names them; remove those delegate blocks first).",
		Annotations: mcptools.Destructive,
	}, func(call *mcptools.Call, input mcpPipelineKeyInput) (*mcp.CallToolResult, any, error) {
		if _, err := call.Delete(pipelinePath(input.PipelineKey), nil, nil); err != nil {
			return nil, nil, err
		}
		return mcptools.Object(map[string]any{"pipeline_key": input.PipelineKey, "archived": true})
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "list_pipeline_uses",
		Description: "List the delegate blocks in active pipeline revisions that hand requests to this pipeline: what stands in the way of archiving it.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpPipelineKeyInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(pipelinePath(input.PipelineKey)+"/uses", nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "list_pipeline_revisions",
		Description: "List a pipeline's revisions, newest first, with which one is active. Use it to find a revision to roll back to.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpPipelineKeyInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(pipelinePath(input.PipelineKey)+"/revisions", nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "get_pipeline_revision",
		Description: "Read one pipeline revision, active or not, with its blocks.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpPipelineRevisionInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(pipelineRevisionPath(input.PipelineKey, input.Revision), nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "get_pipeline_revision_references",
		Description: "List everything a pipeline revision names (escalation paths, webhook endpoints, delegated pipelines, an edition's block dependencies) with each one's state: what stands in the way of activating it.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpPipelineRevisionInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(pipelineRevisionPath(input.PipelineKey, input.Revision)+"/references", nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name: "update_pipeline",
		Description: "Save a new revision of a pipeline (the entry pipeline, or one a delegate hands requests to) and activate it (unless activate is false). " +
			"Blocks run in order and the first to decide wins: outcome blocks decide every request that reaches them, webhook blocks ask a service of yours (and a kind this deployment adds may ask a model), " +
			"branch blocks run their own blocks only for requests their condition matches (so \"if this, then that\" is a branch holding an outcome block), and delegate blocks hand matching requests to another pipeline by key; a request that enters a branch or a delegated pipeline is decided inside it or goes to a human, and nothing after the block runs for it. " +
			"A request no block decides falls through to human review, so end with an outcome block whose outcome is approve when the rest should be automatic. " +
			"Every escalation path a block or the default names, and every pipeline a delegate names, must exist; activation additionally requires each to be active and refuses a delegation cycle. Call read_reference (pipeline_blocks) first.",
		Annotations: mcptools.Additive,
	}, func(call *mcptools.Call, input mcpUpdatePipelineInput) (*mcp.CallToolResult, any, error) {
		base := pipelinePath(input.PipelineKey)
		body := map[string]any{"blocks": writableBlocks(input.Blocks)}
		if input.Name != "" {
			body["name"] = input.Name
		}
		if input.DefaultEscalationPath != "" {
			body["default_escalation_path"] = input.DefaultEscalationPath
		}
		if input.DefaultAskAgentOwner {
			body["default_ask_agent_owner"] = true
		}
		created, err := call.Post(base+"/revisions", body, nil)
		if err != nil {
			return nil, nil, err
		}
		return call.SaveRevision(base, created.Body, input.Activate, "activate_pipeline_revision")
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "activate_pipeline_revision",
		Description: "Make an existing pipeline revision the active one (a rollback, or activating a draft). New requests use it immediately; requests already in flight keep the revision they started on.",
		Annotations: mcptools.Replacing,
	}, func(call *mcptools.Call, input mcpPipelineRevisionInput) (*mcp.CallToolResult, any, error) {
		activated, err := call.ActivateRevision(pipelinePath(input.PipelineKey), input.Revision)
		if err != nil {
			return nil, nil, err
		}
		return mcptools.Result(activated)
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name: "preview_pipeline",
		Description: "Run a sample request through a set of blocks and report the outcome (approve, deny, human, or next), which block decided, its reason, and the escalation path a human exit would take. " +
			"Each step lists a block the request reached, with the pipeline it belongs to; a branch or delegate reports enter or next for its condition, and end_of_branch when the request ran out of its blocks (for a delegate, out of the target pipeline). " +
			"Delegate blocks expand to their targets' current active revisions, so the preview walks what a request would. Nothing is saved and no approval request is created. webhook blocks and other asynchronous kinds really call the endpoint or the model.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpPreviewPipelineInput) (*mcp.CallToolResult, any, error) {
		arguments := input.Sample.Arguments
		if arguments == nil {
			arguments = map[string]any{}
		}
		slug := input.Sample.AgentSlug
		if slug == "" {
			slug = "agent"
		}
		name := input.Sample.AgentName
		if name == "" {
			name = slug
		}
		request := map[string]any{"tool": input.Sample.Tool, "arguments": arguments}
		if input.Sample.AgentReasoning != "" {
			request["agent_reasoning"] = input.Sample.AgentReasoning
		}
		if input.Sample.Context != nil {
			request["context"] = input.Sample.Context
		}
		body := map[string]any{
			"blocks": writableBlocks(input.Blocks),
			"sample": map[string]any{"request": request, "agent": map[string]any{"slug": slug, "name": name}},
		}
		if input.DefaultEscalationPath != "" {
			body["default_escalation_path"] = input.DefaultEscalationPath
		}
		if input.DefaultAskAgentOwner {
			body["default_ask_agent_owner"] = true
		}
		return mcptools.Answer(call.Post(pipelinePath(input.PipelineKey)+"/preview", body, nil))
	})
}

func registerMCPEscalationPathTools(catalog *mcptools.Catalog) {
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "list_escalation_paths",
		Description: "List escalation paths with their key, name, active revision and latest revision.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpStatusInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(mcpPathsPath, mcptools.Query("status", input.Status)))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "get_escalation_path",
		Description: "Read an escalation path's active revision: its nodes, working hours, repeat setting and warnings. Not found when the path has no active revision; use list_escalation_path_revisions then.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpPathKeyInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(escalationPathPath(input.PathKey), nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "list_escalation_path_revisions",
		Description: "List an escalation path's revisions, newest first, with which one is active.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpPathKeyInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(escalationPathPath(input.PathKey)+"/revisions", nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "get_escalation_path_revision",
		Description: "Read one escalation path revision, active or not, with its nodes and warnings.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpPathRevisionInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(escalationPathPath(input.PathKey)+"/revisions/"+strconv.FormatInt(input.Revision, 10), nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "list_escalation_path_uses",
		Description: "List the active pipeline revisions that name this path, as a block's path or as the default: what stands in the way of archiving it.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpPathKeyInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(escalationPathPath(input.PathKey)+"/uses", nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name: "create_escalation_path",
		Description: "Create an escalation path with its first revision and activate it (unless activate is false). " +
			"A path says who reviews a request the pipeline hands to a person: level nodes page targets (user targets by membership id, team targets by team id) and wait escalate_after before the next level; if_else nodes branch on urgency, working hours or the request itself; defer nodes hold the request until working hours. " +
			"Call read_reference (escalation_paths) first. The key is permanent: pipelines name it, and it stays taken even after archiving.",
		Annotations: mcptools.Additive,
	}, func(call *mcptools.Call, input mcpEscalationPathInput) (*mcp.CallToolResult, any, error) {
		body := documentBody(input.Nodes, input.WorkingHours, input.Repeat)
		body["path_key"], body["name"] = input.PathKey, input.Name
		created, err := call.Post(mcpPathsPath, body, nil)
		if err != nil {
			return nil, nil, err
		}
		return call.SaveRevision(escalationPathPath(input.PathKey), created.Body, input.Activate, "activate_escalation_path_revision")
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "update_escalation_path",
		Description: "Save a new revision of an existing escalation path (the whole document again, not a diff) and activate it unless activate is false. Pipelines that name the key pick up the active revision for new requests; requests already routed keep the revision they were pinned to.",
		Annotations: mcptools.Additive,
	}, func(call *mcptools.Call, input mcpEscalationPathInput) (*mcp.CallToolResult, any, error) {
		base := escalationPathPath(input.PathKey)
		body := documentBody(input.Nodes, input.WorkingHours, input.Repeat)
		body["name"] = input.Name
		created, err := call.Post(base+"/revisions", body, nil)
		if err != nil {
			return nil, nil, err
		}
		return call.SaveRevision(base, created.Body, input.Activate, "activate_escalation_path_revision")
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "activate_escalation_path_revision",
		Description: "Make an existing escalation path revision the active one (a rollback, or activating a draft).",
		Annotations: mcptools.Replacing,
	}, func(call *mcptools.Call, input mcpPathRevisionInput) (*mcp.CallToolResult, any, error) {
		activated, err := call.ActivateRevision(escalationPathPath(input.PathKey), input.Revision)
		if err != nil {
			return nil, nil, err
		}
		return mcptools.Result(activated)
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "archive_escalation_path",
		Description: "Archive an escalation path for good: it stops being active, refuses new revisions, and keeps its key and history. Refused while any active pipeline revision names it (list_escalation_path_uses names them); update those pipelines first.",
		Annotations: mcptools.Destructive,
	}, func(call *mcptools.Call, input mcpPathKeyInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Delete(escalationPathPath(input.PathKey), nil, nil))
	})
}

func registerMCPWebhookTools(catalog *mcptools.Catalog) {
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "list_webhook_endpoints",
		Description: "List webhook endpoints (the URLs a pipeline's webhook block can ask for an outcome) with their key, active revision, and which pipelines use them.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, _ struct{}) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(mcpWebhooksPath, nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "get_webhook_endpoint",
		Description: "Read a webhook endpoint's active revision: name and URL. The signing secret is never returned here; it is shown once when the endpoint is created or its secret rotated.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpEndpointKeyInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(webhookEndpointPath(input.EndpointKey), nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "list_webhook_endpoint_revisions",
		Description: "List a webhook endpoint's revisions, newest first, with which one is active. Use it to find a revision to roll back to.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpEndpointKeyInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(webhookEndpointPath(input.EndpointKey)+"/revisions", nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "get_webhook_endpoint_revision",
		Description: "Read one webhook endpoint revision, active or not.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpEndpointRevisionInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(webhookEndpointPath(input.EndpointKey)+"/revisions/"+strconv.FormatInt(input.Revision, 10), nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "list_webhook_endpoint_uses",
		Description: "List the active pipeline revisions whose webhook blocks post to this endpoint: what stands in the way of archiving it.",
		Annotations: mcptools.ReadOnly,
	}, func(call *mcptools.Call, input mcpEndpointKeyInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Get(webhookEndpointPath(input.EndpointKey)+"/uses", nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name: "save_webhook_endpoint",
		Description: "Create a webhook endpoint or save a new revision of one, and activate it unless activate is false. " +
			"A new key answers with signing_secret exactly once: relay it to whoever operates the receiving service, because it is what verifies withHuman's signature and it cannot be read back. " +
			"The URL must be public HTTPS.",
		Annotations: mcptools.Additive,
	}, func(call *mcptools.Call, input mcpSaveWebhookEndpointInput) (*mcp.CallToolResult, any, error) {
		base := webhookEndpointPath(input.EndpointKey)
		created, err := call.Post(base+"/revisions", map[string]any{"name": input.Name, "url": input.URL}, nil)
		if err != nil {
			return nil, nil, err
		}
		fields := map[string]any{"revision": json.RawMessage(created.Body), "activated": false}
		var minted struct {
			SigningSecret string `json:"signing_secret"`
		}
		if json.Unmarshal(created.Body, &minted) == nil && minted.SigningSecret != "" {
			fields["signing_secret"] = minted.SigningSecret
		}
		if !mcptools.ActivateWanted(input.Activate) {
			return mcptools.Object(fields)
		}
		number, err := mcptools.RevisionNumber(created.Body)
		if err != nil {
			return nil, nil, err
		}
		activated, err := call.ActivateRevision(base, number)
		if err != nil {
			return nil, nil, fmt.Errorf("revision %d was saved but not activated; fix the cause and call activate_webhook_endpoint_revision: %w", number, err)
		}
		fields["revision"], fields["activated"] = json.RawMessage(activated), true
		return mcptools.Object(fields)
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "activate_webhook_endpoint_revision",
		Description: "Make an existing webhook endpoint revision the active one (a rollback, activating a draft, or bringing an archived endpoint back). Webhook blocks post to the active revision's URL from their next delivery on.",
		Annotations: mcptools.Replacing,
	}, func(call *mcptools.Call, input mcpEndpointRevisionInput) (*mcp.CallToolResult, any, error) {
		activated, err := call.ActivateRevision(webhookEndpointPath(input.EndpointKey), input.Revision)
		if err != nil {
			return nil, nil, err
		}
		return mcptools.Result(activated)
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "test_webhook_endpoint",
		Description: "Send one signed test delivery (type webhook_endpoint.test, flagged as a preview) to a revision's URL and report what came back, waiting up to 10 seconds. It checks the receiver's signature verification too. The test is recorded in the audit log.",
		Annotations: mcptools.Reaching,
	}, func(call *mcptools.Call, input mcpTestWebhookEndpointInput) (*mcp.CallToolResult, any, error) {
		base := webhookEndpointPath(input.EndpointKey)
		revision := input.Revision
		if revision == 0 {
			active, err := call.Get(base, nil)
			if err != nil {
				return nil, nil, fmt.Errorf("the endpoint has no active revision to test; pass a revision from list_webhook_endpoint_revisions: %w", err)
			}
			if revision, err = mcptools.RevisionNumber(active.Body); err != nil {
				return nil, nil, err
			}
		}
		return mcptools.Answer(call.Post(base+"/revisions/"+strconv.FormatInt(revision, 10)+"/test", nil, nil))
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name:        "archive_webhook_endpoint",
		Description: "Archive a webhook endpoint by deactivating its active revision. Its history and signing secret are kept, and activate_webhook_endpoint_revision brings it back. Refused while an active pipeline revision posts to it (list_webhook_endpoint_uses names them).",
		Annotations: mcptools.Replacing,
	}, func(call *mcptools.Call, input mcpEndpointKeyInput) (*mcp.CallToolResult, any, error) {
		if _, err := call.DeactivateRevision(webhookEndpointPath(input.EndpointKey)); err != nil {
			return nil, nil, err
		}
		return mcptools.Object(map[string]any{"endpoint_key": input.EndpointKey, "archived": true})
	})
	mcptools.Add(catalog, &mcp.Tool{
		Name: "rotate_webhook_endpoint_secret",
		Description: "Replace a webhook endpoint's signing secret. The old secret stops working at once for every revision, so deliveries fail signature checks until the receiver has the new one. " +
			"The answer carries the new secret exactly once: relay it to whoever operates the receiving service.",
		Annotations: mcptools.Destructive,
	}, func(call *mcptools.Call, input mcpEndpointKeyInput) (*mcp.CallToolResult, any, error) {
		return mcptools.Answer(call.Post(webhookEndpointPath(input.EndpointKey)+"/rotate_secret", nil, nil))
	})
}

func agentPath(slug string) string { return mcpAgentsPath + "/" + mcptools.Segment(slug) }

func teamPath(teamID string) string { return mcpTeamsPath + "/" + mcptools.Segment(teamID) }

func teamPolicyPath(teamID string) string { return teamPath(teamID) + "/escalation-policy" }

func pipelinePath(pipelineKey string) string {
	return mcpPipelinesPath + "/" + mcptools.Segment(pipelineKey)
}

func pipelineRevisionPath(pipelineKey string, revision int64) string {
	return pipelinePath(pipelineKey) + "/revisions/" + strconv.FormatInt(revision, 10)
}

func escalationPathPath(pathKey string) string { return mcpPathsPath + "/" + mcptools.Segment(pathKey) }

func webhookEndpointPath(endpointKey string) string {
	return mcpWebhooksPath + "/" + mcptools.Segment(endpointKey)
}

// flag formats an optional boolean query parameter: "true" when set,
// else empty, which Query leaves out.
func flag(set bool) string {
	if set {
		return "true"
	}
	return ""
}

// presentFields builds a PATCH body from the fields the agent set, so an
// omitted one keeps its current value.
func presentFields(pairs ...string) map[string]any {
	body := map[string]any{}
	for index := 0; index+1 < len(pairs); index += 2 {
		if pairs[index+1] != "" {
			body[pairs[index]] = pairs[index+1]
		}
	}
	return body
}

// writableBlocks strips the fields a stored block carries beyond its
// definition (snapshot id, config version, content hash), at every level of
// a branch, so an agent can pass back exactly what get_pipeline returned.
func writableBlocks(blocks []map[string]any) []map[string]any {
	if blocks == nil {
		return []map[string]any{}
	}
	cleaned := make([]map[string]any, len(blocks))
	for index, block := range blocks {
		cleaned[index] = writableBlock(block)
	}
	return cleaned
}

func writableBlock(block map[string]any) map[string]any {
	copied := make(map[string]any, len(block))
	for key, value := range block {
		switch key {
		case "snapshot_id", "config_version", "content_hash":
			continue
		}
		copied[key] = value
	}
	config, isObject := copied["config"].(map[string]any)
	if copied["type"] != string(approvalblock.TypeBranch) || !isObject {
		return copied
	}
	children, isList := config["blocks"].([]any)
	if !isList {
		return copied
	}
	nested := make(map[string]any, len(config))
	for key, value := range config {
		nested[key] = value
	}
	cleanedChildren := make([]any, len(children))
	for index, child := range children {
		if childBlock, isBlock := child.(map[string]any); isBlock {
			cleanedChildren[index] = writableBlock(childBlock)
		} else {
			cleanedChildren[index] = child
		}
	}
	nested["blocks"] = cleanedChildren
	copied["config"] = nested
	return copied
}

// documentBody is the routing document an escalation path or a team's
// escalation policy revision carries: nodes, working hours and repeat.
func documentBody(nodes, workingHours []map[string]any, repeat map[string]any) map[string]any {
	if nodes == nil {
		nodes = []map[string]any{}
	}
	if workingHours == nil {
		workingHours = []map[string]any{}
	}
	body := map[string]any{"nodes": nodes, "working_hours": workingHours}
	if repeat != nil {
		body["repeat"] = repeat
	}
	return body
}
