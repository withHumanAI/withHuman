/* cancelled: the requesting agent instance withdrew the request before
 * anyone decided (its runtime died or stopped waiting). Final, like the
 * other three. */
export type ApprovalStatus = "pending" | "approved" | "denied" | "expired" | "cancelled";

export interface Decision {
  id: string;
  status: ApprovalStatus;
  note?: string;
  channel: string;
  reviewer_display_name?: string;
  decided_at: string;
}

/* email, slack and discord are core's; any other kind is one the
 * edition registered, which the frontend learns about through
 * extensions.contactKinds. */
export type ContactMethodKind = "email" | "slack" | "discord" | (string & {});

/* A place a person can be reached. An email method belongs to the
 * person everywhere and is proved by a code sent to it; slack and
 * discord methods are their identity in one organization's connected
 * workspace or server. Slack is linked by matching addresses; Discord
 * by the person authorizing their account. A method of a kind the
 * edition registered is person-wide like email, and is added, proved
 * and drawn the way its registry entry says. */
export interface ContactMethod {
  available: boolean;
  id: string;
  kind: ContactMethodKind;
  value: string;
  /* Set for organization-scoped kinds only. */
  organization_id?: string;
  /* A display name for kinds whose value is an opaque id. */
  label?: string;
  verified: boolean;
  verified_at?: string;
  primary: boolean;
  created_at: string;
}

/* What adding a method or asking for a fresh code answers: the method,
 * still unconfirmed, and beside it whatever its kind adds (a link the
 * person confirms through, for a kind the edition delivers that way).
 * The kind's own components narrow the extras. */
export interface ContactMethodResponse {
  contact_method: ContactMethod;
  [extra: string]: unknown;
}

/* One rung of a person's notification ladder for an urgency: reach me
 * after delay_seconds via this method. A null contact_method_id is the
 * account email, which is not a stored method. */
export interface NotificationRule {
  delay_seconds: number;
  contact_method_id: string | null;
}

export type NotificationUrgency = "standard" | "interrupt";

/* Both ladders, plus whether each is the person's own or the computed
 * default they have not edited yet. */
export interface NotificationRules {
  standard: NotificationRule[];
  interrupt: NotificationRule[];
  customized: { standard: boolean; interrupt: boolean };
}

export interface Claim {
  id: string;
  membership_id: string;
  display_name?: string;
  claimed_at: string;
  expires_at: string;
}

export interface ApprovalRequest {
  id: string;
  organization_id: string;
  agent_slug: string;
  agent_instance_id: string;
  agent_name: string;
  agent_instance_name: string;
  tool: string;
  /* The MCP server that defines the tool, as the adapter reported it;
   * absent for a runtime's built-in tools. */
  server?: string;
  arguments: unknown;
  agent_reasoning?: string;
  context?: unknown;
  status: ApprovalStatus;
  deadline_at: string;
  created_at: string;
  /* The newest change to the request, when the wire carries it; the
   * detail refetches its review context whenever it moves. */
  updated_at?: string;
  decision?: Decision;
  claim?: Claim;
  /* How to show the arguments, resolved by the server from the
   * organization's tool presentations and the shipped catalog. Absent
   * when neither names the tool: the arguments then render by shape. */
  presentation?: ResolvedToolPresentation;
}

/* How one argument renders. The vocabulary every client interprets;
 * the server validates a presentation against the same list. */
export type PresentationKind =
  | "text"
  | "prose"
  | "markdown"
  | "html"
  | "code"
  | "shell"
  | "diff"
  | "path"
  | "url"
  | "email"
  | "reference"
  | "enum"
  | "datetime"
  | "list"
  | "json";

export type PresentationRole = "primary" | "detail";

export interface PresentationField {
  /* A JSON pointer into the arguments; relative to the element inside
   * a list's item. Absent for a diff pair. */
  path?: string;
  label?: string;
  kind: PresentationKind;
  language?: string;
  before?: string;
  after?: string;
  item?: PresentationField;
  role?: PresentationRole;
}

export interface ToolPresentationSpec {
  title?: string;
  fields: PresentationField[];
}

/* The presentation a request carries. */
export interface ResolvedToolPresentation extends ToolPresentationSpec {
  source: "built_in" | "organization";
  id?: string;
}

/* Which calls a presentation applies to. Empty runtime and server
 * match any; tool is exact. */
export interface ToolPresentationBinding {
  runtime?: string;
  server?: string;
  tool: string;
}

export interface ToolPresentationInput extends ToolPresentationBinding {
  spec: ToolPresentationSpec;
}

/* One of the organization's own presentations. */
export interface ToolPresentation extends ToolPresentationInput {
  id: string;
  created_at: string;
  updated_at: string;
}

export interface ToolPresentationsPage {
  presentations: ToolPresentation[];
  built_in: ToolPresentationInput[];
  kinds: PresentationKind[];
}

/* The kinds of scope a grant can bind at, widest first. An organization
 * grant covers every team and agent; a team or agent grant covers only
 * that one resource. An agent scope's id is the agent's slug. */
export type ScopeKind = "organization" | "team" | "agent";

/* One permission held at one scope: the flattening of the principal's
 * role assignments, direct and inherited through teams. */
export interface Grant {
  permission: string;
  scope_kind: ScopeKind;
  scope_id?: string;
}

/* What an action is about, for can(): the organization itself, one
 * team by id, or one agent by slug. */
export interface Resource {
  kind: ScopeKind;
  id?: string;
}

export interface Principal {
  actor_type: "human" | "agent" | "system" | "api_key";
  actor_id: string;
  organization_id: string;
  membership_id?: string;
  agent_instance_id?: string;
  agent_slug?: string;
  /* The "holds anywhere" projection of grants: every permission the
   * principal holds at some scope. Navigation gates on it. */
  permissions: string[];
  grants: Grant[];
  team_ids?: string[];
}

export type MembershipStatus = "active" | "suspended" | "deprovisioned";

/* One entry of the code-owned permission vocabulary. area is the first
 * segment of the key and groups the matrix. */
export interface PermissionDescriptor {
  key: string;
  area: string;
  description: string;
  holders: Array<PermissionHolder>;
  resource: string;
  dangerous: boolean;
}

/* Who can carry a permission: a human through a role, an agent
 * credential, a personal API key acting as the person who minted it, an
 * organization API key, or a holder kind an edition's own principal
 * adds, which the wire names as it likes. */
export type PermissionHolder = "human" | "agent" | "api_key" | "organization_api_key" | (string & {});

/* A personal API key: a bearer credential that acts as the member who
 * minted it. permissions null means the key inherits everything the
 * member holds, re-evaluated on every call; a list narrows it to the
 * intersection of that list and what the member holds now. */
export interface APIKey {
  id: string;
  /* "whk_<id>", the public half of the token. */
  prefix: string;
  kind: "personal" | "organization";
  grants: Grant[];
  usable: boolean;
  blocked_reason?: string;
  membership_id?: string;
  /* Only on the organization-wide list. */
  created_by?: { membership_id: string; display_name: string; email: string };
  /* The MCP client the key was minted for through the consent page;
   * absent for a key made by hand. */
  oauth_client?: { id: string; name: string };
  name: string;
  permissions: string[] | null;
  assurance_at_issue: string;
  status: "active" | "expired" | "revoked";
  created_at: string;
  expires_at: string | null;
  last_used_at: string | null;
  revoked_at: string | null;
  revocation_reason?: "manual" | "membership_deprovisioned";
}

/* One permission a key may be scoped to: the member holds it and the
 * registry lets an API key carry it. */
export interface APIKeyPermission {
  key: string;
  area: string;
  description: string;
  dangerous: boolean;
}

/* The signed-in member's keys with the policy that governs them. */
export interface MyAPIKeys {
  keys: APIKey[];
  personal_api_keys_allowed: boolean;
  /* Hosted edition with the plan: keys may carry a subset of permissions. */
  scoping_enabled: boolean;
  bearable_permissions: APIKeyPermission[];
}

/* What an MCP client put in the consent page's URL. */
export interface OAuthAuthorizationRequest {
  client_id: string;
  redirect_uri: string;
  state: string;
  code_challenge: string;
  code_challenge_method: string;
  scope: string;
  resource: string;
  response_type: string;
}

export interface OAuthAuthorizationPreview {
  client: { id: string; name: string; client_uri?: string; redirect_uri: string };
  requested_permissions: string[];
  bearable_permissions: APIKeyPermission[];
  scoping_enabled: boolean;
  personal_api_keys_allowed: boolean;
}

export interface OAuthAuthorizationDecision extends OAuthAuthorizationRequest {
  approve: boolean;
  permissions?: string[];
  expires_at?: string;
}

export interface APIKeyInput {
  name: string;
  permissions?: string[];
  expires_at?: string;
}

/* One role pinned to one principal at one scope. via_team_id is set on
 * rows a membership inherits through a team it belongs to. */
export interface RoleAssignment {
  id: string;
  principal_kind: "membership" | "team" | "agent";
  principal_id: string;
  permissions: string[];
  is_owner: boolean;
  permission_policy_id?: string;
  scope_kind: ScopeKind;
  scope_id?: string;
  scope_name?: string;
  source: "manual" | "directory";
  source_key?: string;
  granted_by_actor_id?: string;
  via_team_id?: string;
  via_team_name?: string;
  created_at: string;
}

/* Explicit permissions carried by an invitation. */
export interface InvitationGrant {
  permissions: string[];
  scope_kind: ScopeKind;
  scope_id?: string;
}

/* A directory group linked to a team, optionally mapped to a grant so
 * group membership carries a role. */
export interface DirectoryGroupLink {
  provider_object_id: string;
  team_id: string;
  team_name: string;
  permission_policy?: PermissionPolicyInput;
  updated_at: string;
}

/* The enriched /me response: who is signed in, where. */
export interface Session {
  principal: Principal;
  /* has_password is false for an account that only signs in through an
   * identity provider; it has no password to change or reset. */
  user?: { display_name: string; email: string; has_password: boolean };
  organization?: { name: string; slug: string };
  /* The signed-in membership's own assignments plus the ones inherited
   * through its teams. */
  assignments: RoleAssignment[];
  /* required is true while the organization has not finished (or
   * skipped) the guided setup and the caller holds every permission the
   * flow needs; the app sends such a person to /welcome once per load. */
  onboarding?: { required: boolean; completed_at?: string | null };
}

export interface Member {
  id: string;
  display_name: string;
  email: string;
  status: MembershipStatus;
  assignments: RoleAssignment[];
  admission_source: string;
  directory_managed: boolean;
  team_ids: string[];
  created_at: string;
}

/* Teams are mutable ops data: manual assignments and directory-synced
 * group assignments coexist, and a directory-managed team locks manual
 * rename and delete. */
export type TeamListStatus = "live" | "archived" | "all";

export interface Team {
  archived_at: string | null;
  id: string;
  name: string;
  directory_managed: boolean;
  member_count: number;
  /* The permissions the team holds; every member inherits them live. */
  assignments: RoleAssignment[];
  escalation_policy: TeamEscalationPolicyStatus;
  created_at: string;
}

/* The team's escalation policy lineage. Both null when the team never
 * had a policy; active_revision alone null when no revision is active,
 * whether the latest was saved as a draft or the policy was archived.
 * Without an active revision a path level that targets the team
 * notifies every member at once. */
export interface TeamEscalationPolicyStatus {
  active_revision: number | null;
  latest_revision: number | null;
}

/* archived is terminal: nothing of the agent resolves again and nothing
 * can be enrolled under it; the reversible switch is active/disabled. */
export type AgentStatus = "active" | "disabled" | "archived";

/* Which agents a listing returns: live (active and disabled), archived,
 * or both. */
export type AgentListStatus = "live" | "archived" | "all";

/* The stable organization-owned machine identity that asks for
 * approvals. The slug is chosen once and never changes or gets reused,
 * even after archiving; the name is a display label that can be
 * renamed and is unique among live agents only. */
export interface Agent {
  slug: string;
  organization_id: string;
  created_by_actor_id: string;
  name: string;
  status: AgentStatus;
  created_at: string;
  updated_at: string;
  /* Present exactly when status is archived. */
  archived_at?: string;
}

/* One row of the agent inventory. A live instance is an active instance
 * holding an unexpired, unrevoked credential; last_seen_at is the newest
 * authenticated call from any instance and is absent when the agent has
 * no instances. Request counts cover the last seven days and the
 * requests still pending. */
export interface AgentSummary extends Agent {
  instance_count: number;
  live_instance_count: number;
  last_seen_at?: string;
  recent_request_count: number;
  pending_request_count: number;
  /* One bucket per day, oldest first; the last is the past 24 hours. */
  request_trend: number[];
}

export interface AgentInstance {
  id: string;
  organization_id: string;
  agent_slug: string;
  name: string;
  status: AgentStatus;
  metadata: Record<string, unknown>;
  created_at: string;
  last_seen_at: string;
}

/* One instance credential as the inventory shows it, never the
 * secret. Live when it is neither revoked nor expired. */
export interface AgentCredentialState {
  id: string;
  issued_at: string;
  expires_at: string;
  last_used_at?: string;
  revoked_at?: string;
}

export interface AgentInstanceDetail extends AgentInstance {
  /* Every credential the instance has held, newest first. */
  credentials: AgentCredentialState[];
  recent_request_count: number;
  /* One bucket per day, oldest first. */
  request_trend: number[];
}

export interface AgentProvisioner {
  id: string;
  organization_id: string;
  agent_slug: string;
  created_by_actor_id: string;
  issued_at: string;
  expires_at: string;
  last_used_at?: string;
  revoked_at?: string;
}

export interface AgentDetail extends Agent {
  provisioner_count: number;
  matching_provisioner_count: number;
  instance_count: number;
  live_instance_count: number;
  matching_instance_count: number;
  request_count: number;
  created_by_display_name: string;
  created_by_actor_type: "human" | "api_key";
  instances: AgentInstanceDetail[];
  provisioners: AgentProvisioner[];
  recent_request_count: number;
  pending_request_count: number;
}

export interface IssuedCredential {
  id: string;
  token: string;
  expires_at: string;
}

export interface TeamMember {
  membership_id: string;
  display_name: string;
  email: string;
  status: MembershipStatus;
  sources: Array<"manual" | "directory">;
}

export type InvitationStatus = "pending" | "accepted" | "revoked" | "expired";

export interface Invitation {
  id: string;
  email: string;
  grants: InvitationGrant[];
  status: InvitationStatus;
  expires_at: string;
  created_at: string;
}

export interface InvitationPreview {
  organization_name: string;
  organization_slug: string;
  inviter_display_name?: string;
  email: string;
  grants: InvitationGrant[];
  expires_at: string;
  sso_required: boolean;
  existing_user: boolean;
}

/* Email-first login: methods discovery, then the password step, then the
 * organization selection for multi-organization users. */
export interface LoginOrganization {
  id: string;
  slug: string;
  name: string;
  sso: boolean;
}

/* A Slack workspace connected to the organization by an admin's OAuth
 * install. The bot token never reaches the browser. */
export interface SlackConnection {
  status: "connected" | "disconnected";
  disconnected_at?: string;
  disconnect_reason?: string;
  team_id: string;
  team_name: string;
  bot_user_id: string;
  scopes: string[];
  installed_by_membership_id: string;
  installed_at: string;
  /* Members whose Slack identity has been matched in this workspace. */
  linked_members: number;
}

/* A Discord server connected to the organization by an admin's OAuth
 * install. The bot token is deployment configuration and never reaches
 * the browser. */
export interface DiscordConnection {
  guild_id: string;
  guild_name: string;
  installed_by_membership_id: string;
  installed_at: string;
  /* Members who have linked their Discord account in this organization. */
  linked_members: number;
}

export interface LoginMethods {
  password: boolean;
  sso_organizations: LoginOrganization[];
}

export type LoginResponse =
  | {
      status: "complete";
      principal: Principal;
      /* Set when a social signup founded an organization on the way in. */
      organization?: { id: string; slug: string; name: string };
    }
  | {
      status: "select_organization";
      ticket: string;
      expires_at: string;
      organizations: LoginOrganization[];
    }
  /* Every organization signs in through enterprise SSO: no ticket, the
   * client continues through the SSO flow for one of them. */
  | { status: "sso_required"; organizations: LoginOrganization[] };

/* A social login provider the edition registered (Google, GitHub). The
 * open edition registers none and the buttons never render. */
export interface SocialProvider {
  key: string;
  label: string;
}

export interface APIError {
  error: { code: string; message: string; details?: Record<string, unknown> };
}




/* The details carried by a quota_exceeded error. */
export interface QuotaExceededDetails {
  dimension: string;
  limit: number;
  used: number;
}

/* One active escalation path revision that names a member as a target. */
export interface TargetedEscalationPath {
  path_key: string;
  name: string;
  revision: number;
}

/* One active team escalation policy revision that names a member. */
export interface TargetedTeamEscalationPolicy {
  team_id: string;
  team_name: string;
  revision: number;
}

/* Details of a membership_in_escalation_path refusal: suspending or
 * removing the member would leave these paths and team policies
 * pointing at nobody. */
export interface MembershipInEscalationPathDetails {
  membership_id: string;
  escalation_paths: TargetedEscalationPath[];
  team_escalation_policies: TargetedTeamEscalationPolicy[];
}

/* One page of a revisioned resource's history, newest first: the same
 * envelope for pipelines, escalation paths and webhook endpoints.
 * next_cursor is absent on the last page. */
export interface RevisionPage<Item> {
  revisions: Item[];
  next_cursor?: string;
  total_count: number;
}

export type PipelineOutcome = "next" | "approve" | "deny" | "human";

/* Block definitions are a discriminated union on `type`; each variant
 * carries its own config shape and envelope limits. */
export interface AlwaysBlockDefinition {
  id: string;
  name: string;
  type: "always";
  enabled: true;
  timeout: string;
  max_attempts: 1;
  config: {
    outcome: Exclude<PipelineOutcome, "next">;
    reason: string;
    escalation_path?: string;
  };
}

export interface CELBlockDefinition {
  id: string;
  name: string;
  type: "cel";
  enabled: true;
  timeout: string;
  max_attempts: 1;
  config: {
    when: Record<string, unknown>;
    on_match: Exclude<PipelineOutcome, "next">;
    reason: string;
    /* The escalation path a human match hands the request to; absent
     * inherits the pipeline default. */
    escalation_path?: string;
  };
}

/* A webhook block posts the request to an organization endpoint and maps
 * its answer onto a pipeline outcome, bounded by allowed_outcomes. The
 * endpoint (URL and secret) is a separate revisioned resource referenced
 * by its stable key; deliveries go to the key's active revision. */
export interface WebhookBlockDefinition {
  id: string;
  name: string;
  type: "webhook";
  enabled: true;
  timeout: string;
  max_attempts: 1 | 2 | 3;
  config: {
    endpoint_key: string;
    allowed_outcomes: PipelineOutcome[];
    include: {
      agent_reasoning: boolean;
      context: boolean;
      previous_metadata: boolean;
    };
    reason: string;
    escalation_path?: string;
  };
}

/* A branch runs its own ordered blocks only for requests its condition
 * matches; a request that does not match skips them all. Inside, the
 * blocks run under the usual contract. When none of them decides, the
 * request goes to a human with the branch's reason and escalation path
 * (else the pipeline default), and the pipeline stops there. */
export interface BranchBlockConfig<Child> {
  when: Record<string, unknown>;
  /* At least one child, of any type, branches included. */
  blocks: Child[];
  reason: string;
  escalation_path?: string;
}

export interface BranchBlockDefinition {
  id: string;
  name: string;
  type: "branch";
  enabled: true;
  timeout: string;
  max_attempts: 1;
  config: BranchBlockConfig<RequestApprovalBlockDefinition>;
}

/* Core's own block kinds. A kind an edition adds (EditionBlockDefinition
 * in app/extensions.tsx) arrives in the same array as JSON like any
 * other; the pipeline editor reads it through the block-kind registry,
 * so the union stays core's. */
export type RequestApprovalBlockDefinition =
  | AlwaysBlockDefinition
  | CELBlockDefinition
  | WebhookBlockDefinition
  | BranchBlockDefinition;

export type RequestApprovalBlockType = RequestApprovalBlockDefinition["type"];

export interface StoredAlwaysBlock extends AlwaysBlockDefinition {
  snapshot_id: string;
  config_version: 1;
  content_hash: string;
}

export interface StoredCELBlock extends CELBlockDefinition {
  snapshot_id: string;
  config_version: 1;
  content_hash: string;
}

export interface StoredWebhookBlock extends WebhookBlockDefinition {
  snapshot_id: string;
  config_version: 1;
  content_hash: string;
}

/* Every block at every depth is its own stored snapshot, so a stored
 * branch nests stored children. */
export interface StoredBranchBlock extends BranchBlockDefinition {
  snapshot_id: string;
  config_version: 1;
  content_hash: string;
  config: BranchBlockConfig<StoredRequestApprovalBlock>;
}

export type StoredRequestApprovalBlock =
  | StoredAlwaysBlock
  | StoredCELBlock
  | StoredWebhookBlock
  | StoredBranchBlock;

/* One webhook endpoint key's revision lineage. name and url come from the
 * latest revision; active_revision is absent while none is active, and
 * archived_at tells an archived endpoint from one holding only drafts; uses
 * are the active pipeline revisions posting to the key, so the page can
 * say which endpoints can be archived. The signing secret belongs to the
 * key and is never read back. */
export interface WebhookEndpointSummary {
  endpoint_key: string;
  name: string;
  url: string;
  active_revision?: number;
  /* Present while the endpoint is archived; cleared by the next activation. */
  archived_at?: string;
  latest_revision: number;
  revision_count: number;
  latest_created_at: string;
  secret_rotated_at: string;
  uses: PipelineUse[];
}

/* One immutable revision of a webhook endpoint. signing_secret is present
 * only on the response that created the key's first revision. */
export interface WebhookEndpointRevision {
  id: string;
  endpoint_key: string;
  revision: number;
  is_active: boolean;
  name: string;
  url: string;
  created_by_actor_id: string;
  created_at: string;
  signing_secret?: string;
}

/* The key's new signing secret, shown once at rotation. */
export interface WebhookEndpointSecret {
  endpoint_key: string;
  signing_secret: string;
  secret_rotated_at: string;
}

/* One active pipeline revision's reference to a shared object (a webhook
 * endpoint or an escalation path). block_key is absent when the revision
 * names an escalation path as its default. */
export interface PipelineUse {
  pipeline_id: string;
  scope: PipelineScope;
  agent_slug?: string;
  revision: number;
  block_key?: string;
}

/* What one synthetic test delivery produced. problem is absent when the
 * endpoint answered a valid outcome document. */
export interface WebhookEndpointTest {
  status_code: number;
  duration_ms: number;
  outcome?: PipelineOutcome;
  reason?: string;
  problem?: "blocked" | "response_too_large" | "timeout" | "unreachable" | "status" | "invalid_answer";
  message?: string;
}

/* Safe record of a webhook block's delivery: what was called and what
 * came back, never the bodies or the secret. */
export interface DeliveryEvidence {
  endpoint_key: string;
  /* Absent when the key had no active revision to deliver to. */
  endpoint_revision?: number;
  endpoint_host: string;
  http_status?: number;
  duration_ms: number;
  reason?: string;
}

export interface RequestApprovalPipelineDocument {
  blocks: RequestApprovalBlockDefinition[];
  /* The escalation path a fall-through request and every human exit
   * that names no path go to. The agent section's default wins over the
   * organization's. */
  default_escalation_path?: string;
}

/* "organization" is the scope whose blocks run before the agent section
 * for every request; agent_slug is present only for the agent scope. */
export type PipelineScope = "organization" | "agent";

/* Which pipelines a listing returns: live (with an active revision),
 * archived with their agent, or both. */
export type PipelineListStatus = "live" | "archived" | "all";

export interface RequestApprovalPipelineSummary {
  scope: PipelineScope;
  agent_slug?: string;
  /* Absent only for an archived pipeline. */
  active_revision?: number;
  latest_revision: number;
  revision_count: number;
  latest_created_at: string;
  /* Present when the pipeline was archived with its agent: readable,
   * never active again until the agent is restored. */
  archived_at?: string;
}

export interface RequestApprovalPipelineRevisionSummary {
  id: string;
  scope: PipelineScope;
  agent_slug?: string;
  revision: number;
  is_active: boolean;
  /* Every block, including the blocks inside branches. */
  block_count: number;
  default_escalation_path?: string;
  created_by_actor_id: string;
  created_at: string;
  /* Present when the revision was archived with its agent; it can be
   * read but never activated while the agent stays archived. */
  archived_at?: string;
}

export interface RequestApprovalPipelineRevision
  extends RequestApprovalPipelineRevisionSummary {
  blocks: StoredRequestApprovalBlock[];
}

/* What one immutable revision depends on and whether each dependency is
 * in place right now; activatable is false while any entry would refuse
 * activation. */
export interface PipelineReferences {
  escalation_paths: {
    path_key: string;
    block_keys: string[];
    default: boolean;
    exists: boolean;
    active: boolean;
  }[];
  webhook_endpoints: {
    endpoint_key: string;
    name?: string;
    block_keys: string[];
    exists: boolean;
    active: boolean;
  }[];
  blocks: {
    block_key: string;
    type: string;
    /* Why the block cannot run: `plan` or `webhook_unconfigured` from
     * core, or a code a kind the edition adds reports, labelled by its
     * registry entry. */
    problem: string;
  }[];
  activatable: boolean;
}

export interface RequestApprovalPipelinePreviewSample {
  request: {
    tool: string;
    server?: string;
    arguments: Record<string, unknown>;
    agent_reasoning?: string;
    context?: Record<string, unknown>;
  };
  agent: { slug: string; name: string };
  /* Metadata earlier blocks returned, nested by scope then block key, so a
   * stitched effective test can carry the organization preview's
   * metadata into the agent preview. */
  previous_metadata?: {
    organization?: Record<string, Record<string, unknown>>;
    agent?: Record<string, Record<string, unknown>>;
  };
}

/* What one evaluation in a preview produced. enter is evidence only: a
 * branch whose condition matched, so its blocks run next. It is never a
 * block's answer, so no block's outcome mapping may name it. */
export type PreviewBlockOutcome = PipelineOutcome | "enter";

/* Blocks run in execution order as one flat list, whatever their depth.
 * A branch appears when its condition is checked (next when it did not
 * match, enter when it did) and, when a request runs out of it with no
 * decision, once more at the end: outcome human, end_of_branch set. */
export interface RequestApprovalPipelinePreviewBlock {
  block_id: string;
  /* The pre-order index within the previewed document. */
  position: number;
  delivery?: DeliveryEvidence;
  outcome: PreviewBlockOutcome;
  /* Set on a branch's closing entry: nothing inside it decided. */
  end_of_branch?: boolean;
  reason?: string;
  metadata?: Record<string, unknown>;
  cost: number;
  error_code?: string;
  trace: Array<{ node_id: string; matched: boolean; error: boolean }>;
}

export interface RequestApprovalPipelinePreview {
  outcome: Exclude<PipelineOutcome, "next">;
  /* end_of_branch: the request ran out of a branch, which
   * terminal_block_id names. */
  reason_code: "block_outcome" | "block_error" | "end_of_pipeline" | "end_of_branch";
  terminal_block_id?: string;
  blocks: RequestApprovalPipelinePreviewBlock[];
  /* The path a human outcome is handed to within this scope alone. */
  escalation_path?: string;
  escalation_path_reason?: "block_escalation" | "pipeline_default" | "no_escalation";
}

/* Reviewer routing wire types, mirroring the OpenAPI schemas. */

export type RoutingUrgency = "standard" | "interrupt";

/* Who a level notifies: one membership or one team by id, or every
 * current member of the team that owns a team escalation policy.
 * Organization paths accept user and team; team policies accept user
 * (members of that team) and broadcast. */
export type EscalationTargetWire = { type: "user" | "team"; id: string } | { type: "broadcast" };

export interface WeekdayIntervalWire {
  weekdays: string[];
  start: string;
  end: string;
}

export interface WorkingHoursSetWire {
  id: string;
  name: string;
  timezone: string;
  intervals: WeekdayIntervalWire[];
}

/* The CEL condition envelope, shared with pipeline blocks. */
export interface RoutingCondition {
  id: string;
  field?: string;
  operator?: string;
  value?: unknown;
  all?: RoutingCondition[];
  any?: RoutingCondition[];
  some?: { field: string; where: RoutingCondition };
  every?: { field: string; where: RoutingCondition };
}

/* One branch test: a working-hours window open or closed, the request's
 * urgency, or the request itself. */
export interface EscalationPathConditionWire {
  type: "working_hours" | "urgency" | "request";
  working_hours_id?: string;
  active?: boolean;
  urgency?: RoutingUrgency;
  when?: RoutingCondition;
}

export interface EscalationPathNodeWire {
  id: string;
  type: "level" | "if_else" | "defer";
  targets?: EscalationTargetWire[];
  escalate_after?: string;
  raise_urgency?: boolean;
  conditions?: EscalationPathConditionWire[];
  then?: EscalationPathNodeWire[];
  else?: EscalationPathNodeWire[];
  working_hours_id?: string;
  standard_only?: boolean;
}

/* One path key's revision lineage. */
export interface EscalationPathSummary {
  path_key: string;
  name: string;
  active_revision?: number;
  latest_revision: number;
  revision_count: number;
  latest_created_at: string;
  /* Present when the path was archived: readable, never active again. */
  archived_at?: string;
}

/* Which paths a listing returns. */
export type EscalationPathStatus = "live" | "archived" | "all";

/* The path narrows, it never grants: a team target whose members lack
 * request.decide is saved, but the server says how many of them the
 * path names in vain. A branch layout some request walks without
 * meeting a level is saved too, with a warning that names no team. */
export type EscalationPathWarning =
  | {
      code: "team_members_cannot_decide";
      team_id: string;
      team_name: string;
      count: number;
      member_count: number;
    }
  | { code: "path_may_reach_nobody" };

/* The node program shared by organization escalation paths and team
 * escalation policies: working hours, steps, and the optional repeat. */
export interface EscalationDocumentBodyWire {
  working_hours: WorkingHoursSetWire[];
  nodes: EscalationPathNodeWire[];
  repeat?: { times: number; after: string };
}

/* One row of a path's history: the revision without its document. */
export interface EscalationPathRevisionSummary {
  id: string;
  path_key: string;
  revision: number;
  is_active: boolean;
  name: string;
  created_by_actor_id: string;
  created_at: string;
  archived_at?: string;
}

export interface EscalationPathRevisionWire extends EscalationDocumentBodyWire {
  id: string;
  path_key: string;
  revision: number;
  is_active: boolean;
  name: string;
  document_version: number;
  created_by_actor_id: string;
  created_at: string;
  archived_at?: string;
  warnings: EscalationPathWarning[];
}

export interface EscalationPathDocumentWire extends EscalationDocumentBodyWire {
  name: string;
}

/* Advice about a team escalation policy against the team's roster and
 * grants, never a refusal. target_not_member names a user target who
 * left the team, so the level naming them skips them;
 * team_members_cannot_decide counts members without request.decide. */
export interface TeamEscalationPolicyWarning {
  code: "team_members_cannot_decide" | "target_not_member";
  membership_id?: string;
  display_name?: string;
  count: number;
  member_count: number;
}

export interface TeamEscalationPolicyRevisionSummary {
  id: string;
  team_id: string;
  revision: number;
  is_active: boolean;
  created_by_actor_id: string;
  created_at: string;
}

/* One immutable team escalation policy revision with its document. */
export interface TeamEscalationPolicyRevisionWire
  extends TeamEscalationPolicyRevisionSummary,
    EscalationDocumentBodyWire {
  document_version: number;
  warnings: TeamEscalationPolicyWarning[];
}

/* The request's routing and the reader's standing on it, readable with
 * request.read alone. routing is absent for requests the pipeline
 * decided itself. What the pipeline concluded and what has happened to
 * the request since is an edition's timeline, told from its audit log. */
export interface ApprovalReview {
  routing?: ApprovalRouting;
  /* Where the signed-in person stands with this request. */
  viewer: ReviewViewer;
  added_reviewers: AddedReviewer[];
}

/* The server's answer to "may I decide this": targeted means the
 * pinned routing names the viewer, directly, through a team, or by
 * being added. break_glass means the viewer may decide only outside
 * routing, which the decision must say out loud. */
export interface ReviewViewer {
  targeted: boolean;
  decidable: "yes" | "break_glass" | "no";
  reason?: "not_targeted" | "break_glass_only" | "no_permission";
}

/* A person a targeted reviewer pulled into the request's routing. */
export interface AddedReviewer {
  membership_id: string;
  display_name: string;
  added_by_membership_id: string;
  added_by_display_name: string;
  created_at: string;
}

/* One team escalation policy revision pinned beside the path, run
 * inline when the path reaches the team. */
export interface PinnedTeamPolicy {
  team_id: string;
  team_name: string;
  policy_revision_id: string;
  revision: number;
}

/* Where a walker stands: level (notified, timer armed), deferred
 * (waiting for a window), repeat_wait (ran out, waiting to repeat),
 * paused (a claim holds it), exhausted (nothing left to do). */
export type EscalationPhase = "level" | "deferred" | "repeat_wait" | "paused" | "exhausted";

/* One team policy walker's position while its policy runs inline. */
export interface RoutingWalkerState {
  team_id: string;
  phase: EscalationPhase;
  current_node_id?: string;
  /* 1-based position of the current level in walk order; 0 before the
   * first level. */
  level_ordinal: number;
  level_count: number;
  repeat: number;
  next_escalation_at?: string;
}

/* Where the escalation machine stands after its latest tick: the path's
 * walker flattened onto the object, one entry per team policy in teams. */
export interface RoutingEscalation extends Omit<RoutingWalkerState, "team_id"> {
  sequence: number;
  urgency: RoutingUrgency;
  updated_at: string;
  teams: RoutingWalkerState[];
}

/* One person (a membership) or team the pinned path reaches for the
 * request. */
export interface ApprovalRoutingTarget {
  type: "user" | "team";
  id: string;
  name: string;
}

/* Where a request was handed when it entered human review. */
export interface ApprovalRouting {
  outcome: "escalated" | "default_queue";
  reason: "block_escalation" | "pipeline_default" | "no_escalation" | "path_inactive" | "evaluation_error";
  urgency: RoutingUrgency;
  escalation_path_key?: string;
  escalation_path_name?: string;
  escalation_path_revision_id?: string;
  pipeline_revision_id?: string;
  block_id?: string;
  created_at: string;
  /* Who the pinned path reaches for this request, in path order: only
   * they and the added reviewers may claim or decide. Empty for
   * default_queue; empty for escalated means the path reaches nobody and
   * only a break-glass reviewer may decide. */
  targets: ApprovalRoutingTarget[];
  added_reviewers: AddedReviewer[];
  /* Always present; empty for default_queue. A team absent here is
   * notified all at once when a level reaches it. */
  team_policies: PinnedTeamPolicy[];
  /* Null before the executor's first tick and for default_queue. */
  escalation: RoutingEscalation | null;
}

/* The guided setup: one shared onboarding session the browser and the
 * withhuman CLI take turns writing, mirroring domain.OnboardingSession
 * field for field. The CLI advances the odd phases (connect, detect,
 * discover, enroll, install) and the person the even ones; the person's
 * own steps that never gate the CLI (channels, rules) are stamps on the
 * session rather than statuses. */
export type OnboardingStatus =
  | "awaiting_cli"
  | "connected"
  | "detected"
  | "agents_selected"
  | "tools_discovered"
  | "provisioning"
  | "enrolled"
  | "installed"
  | "tested"
  | "completed"
  | "skipped";

export interface OnboardingMachine {
  hostname: string;
  os: string;
  arch: string;
  cli_version: string;
  user?: string;
}

export interface OnboardingMCPServer {
  name: string;
  transport: string;
  source: string;
}

/* The CLI's report on one runtime. */
export interface OnboardingDetection {
  runtime: string;
  installed: boolean;
  version?: string;
  binary_path?: string;
  config_path?: string;
  already_hooked: boolean;
  /* Not omitempty on the wire, so an empty report can arrive as null. */
  mcp_servers: OnboardingMCPServer[] | null;
}

/* MCP tool annotations as advertised; absent means not stated. */
export interface OnboardingToolAnnotations {
  title?: string;
  read_only_hint?: boolean;
  destructive_hint?: boolean;
  idempotent_hint?: boolean;
  open_world_hint?: boolean;
}

/* A discovered tool with the server's suggestion attached. reason is
 * one of the toolsuggest codes (shell, annotation_destructive, verb:<v>,
 * edit_not_suggested, read_only_builtin, unknown, ...). */
export interface OnboardingTool {
  input_schema?: Record<string, unknown>;
  display_name?: string;
  name: string;
  source: string;
  server?: string;
  description?: string;
  kind?: string;
  annotations?: OnboardingToolAnnotations | null;
  suggested: boolean;
  reason: string;
}

export interface OnboardingDiscoveryError {
  server: string;
  message: string;
}

export interface OnboardingGeneratedPipeline {
  revision: number;
  replaced_active_revision: number;
}

/* Every object provisioning wrote, so the page can show them. */
export interface OnboardingGenerated {
  escalation_path_key: string;
  escalation_path_name: string;
  escalation_path_revision: number;
  pipelines: Record<string, OnboardingGeneratedPipeline>;
  policy_tools: string[];
}

/* The agent, instance, and credential id minted for one runtime. The
 * raw credential went to the CLI once and is never here. */
export interface OnboardingEnrollment {
  runtime: string;
  agent_slug: string;
  agent_name: string;
  agent_instance_id: string;
  agent_instance_name: string;
  credential_id: string;
}

export interface OnboardingFileChange {
  path: string;
  action: string;
}

/* The CLI's report on wiring one runtime. manual means it could not
 * edit the configuration itself and snippet is what to paste. */
export interface OnboardingInstallResult {
  runtime: string;
  ok: boolean;
  changes?: OnboardingFileChange[];
  notes?: string[];
  manual?: boolean;
  message?: string;
  snippet?: string;
}

export interface OnboardingTestCommand {
  tool: string;
  command: string;
  note?: string;
}

export interface OnboardingError {
  source: string;
  code: string;
  message: string;
}

/* One tool call the setup assistant made during a turn, as the page
 * shows it: the tool, one line of plain language, whether it went
 * through. */
export interface OnboardingAssistantAction {
  tool: string;
  summary: string;
  ok: boolean;
}

/* One message of the setup assistant's conversation. The person's carry
 * text alone; the assistant's carry its actions as they happen, its
 * answer once done, and an error in place of one when the turn failed. */
export interface OnboardingAssistantMessage {
  role: "user" | "assistant";
  text: string;
  actions: OnboardingAssistantAction[];
  status: "running" | "done" | "failed";
  error?: string;
  at: string;
}

export interface OnboardingSession {
  id: string;
  organization_id: string;
  membership_id: string;
  status: OnboardingStatus;
  version: number;
  machine: OnboardingMachine;
  detections: OnboardingDetection[];
  selected_runtimes: string[];
  tools: Record<string, OnboardingTool[]>;
  discovery_errors: Record<string, OnboardingDiscoveryError[]>;
  generated: OnboardingGenerated;
  enrollments: OnboardingEnrollment[];
  install_results: OnboardingInstallResult[];
  test_commands: Record<string, OnboardingTestCommand>;
  /* The request decided in the test and when; set with tested. */
  test_request_id: string | null;
  tested_at: string | null;
  /* Stamped when the person passed the notification steps (provisioning
   * waits for it) and when they left the rules step for the test, which
   * counts the gated calls that arrived after it. */
  channels_set_at: string | null;
  rules_set_at: string | null;
  /* The setup assistant's conversation, oldest first. */
  assistant: OnboardingAssistantMessage[];
  last_error: OnboardingError | null;
  created_at: string;
  updated_at: string;
  expires_at: string;
  completed_at: string | null;
}

/* How a person closes the flow; both mark the organization done. */
export type OnboardingOutcome = "finished" | "skipped";

/* What starting a session hands back: the commands carry the single-use
 * setup code, which the session itself never repeats. */
export interface OnboardingBegin {
  session: OnboardingSession;
  setup_code: string;
  setup_command: string;
}

export type MFAMode = "optional" | "reviewers_required" | "everyone_required";
export type UnroutedRequestsPolicy = "any_reviewer" | "break_glass_only";
export type RequestVisibilityPolicy = "all_readers" | "routed_only";

/* The organization's approval policy knobs. Durations are Go duration
 * strings ("24h0m0s"); src/app/duration.ts converts them. */
export interface OrganizationPolicy {
  organization_api_keys_allowed: boolean;
  allow_self_approval: boolean;
  mfa_mode: MFAMode;
  approval_reauth_window: string;
  claim_retrigger_window: string;
  unrouted_requests: UnroutedRequestsPolicy;
  request_visibility: RequestVisibilityPolicy;
  /* Off refuses every personal API key at once without deleting it. */
  personal_api_keys_allowed: boolean;
}

/* The collection under /api/v1 a policy holder is addressed in: core's
 * members and teams, or one an edition serves the same way. */
export type PermissionPolicyCollection = "members" | "teams" | (string & {});

export interface PermissionPolicyInput {
  scope_kind: ScopeKind;
  resource_ids: string[];
  permissions: string[];
}
export interface PermissionResource { id: string; name: string; kind: ScopeKind }
export interface PermissionPolicy extends PermissionPolicyInput {
  id: string;
  revision: number;
  resources: PermissionResource[];
  source: "manual" | "directory";
  via_team_id?: string;
  via_team_name?: string;
  can_edit: boolean;
  read_only_reason?: string;
}
export interface PermissionPoliciesPage {
  name: string;
  policies: PermissionPolicy[];
  resources: PermissionResource[];
  permissions: PermissionDescriptor[];
  can_create: boolean;
  is_owner: boolean;
  read_only_reason?: string;
}

export interface EnrollmentPreview {
  runtime: string;
  instance_name: string;
  /* The slug the CLI asked for; absent when the person picks the agent. */
  requested_agent_slug?: string;
  expires_at: string;
  status: "pending" | "authorized" | "consumed";
  agent?: Agent;
}

export interface EnrollmentAgents {
  agents: Agent[];
  next_offset?: number;
}

export interface ToolDefinition {
 name: string;
 display_name?: string;
 description?: string;
 input_schema?: Record<string, unknown>;
 annotations?: { read_only_hint?: boolean; destructive_hint?: boolean; idempotent_hint?: boolean; open_world_hint?: boolean };
}
export interface ToolCatalogEntry extends ToolDefinition {
 id: string;
 agent_slug: string;
 agent_instance_id?: string;
 instance_name?: string;
 discovery_context: string;
 runtime: string;
 /* builtin or mcp from an agent runtime about itself; an edition's own
  * external sources carry the names it registers. */
 source: string;
 server?: string;
 available: boolean;
 stale: boolean;
 error?: string;
 schema_fingerprint: string;
 last_seen_at: string;
 last_attempt_at: string;
}
export interface ToolCatalogSource {
 id: string; agent_slug: string; agent_instance_id?: string;
 runtime: string; discovery_context: string; source: string; server?: string;
 last_attempt_at: string; last_success_at: string | null; error?: string;
}
export interface ToolCatalogPage {
 tools: ToolCatalogEntry[];
 sources: ToolCatalogSource[];
 total: number;
 next_offset: number | null;
}
