import type { Grant,
  OAuthAuthorizationDecision,
  OAuthAuthorizationPreview,
  OAuthAuthorizationRequest,
} from "./types";
import type { QueueScope } from "./app/queue-search";
import type {
  PermissionPoliciesPage, PermissionPolicy, PermissionPolicyCollection, PermissionPolicyInput,
  Agent,
  EnrollmentPreview,
  EnrollmentAgents,
  AgentDetail,
  AgentInstance,
  AgentProvisioner,
  AgentListStatus,
  AgentStatus,
  AgentSummary,
  ToolPresentation,
  ToolPresentationInput,
  ToolPresentationsPage,
  APIError,
  APIKey,
  APIKeyInput,
  MyAPIKeys,
  ApprovalRequest,
  ApprovalReview,
  Claim,
  ContactMethod,
  DiscordConnection,
  NotificationRule,
  NotificationRules,
  NotificationUrgency,
  ToolCatalogPage,
  ToolCatalogEntry,
  Invitation,
  InvitationPreview,
  IssuedCredential,
  LoginMethods,
  LoginResponse,
  OnboardingBegin,
  OnboardingOutcome,
  OnboardingSession,
  OrganizationPolicy,
  Member,
  Principal,
  SlackConnection,
  SocialProvider,
  Session,
  RequestApprovalPipelineDocument,
  RequestApprovalPipelinePreview,
  RequestApprovalPipelinePreviewSample,
  RequestApprovalPipelineRevision,
  RequestApprovalPipelineRevisionSummary,
  RevisionPage,
  RequestApprovalPipelineSummary,
  PipelineListStatus,
  ApprovalRouting,
  ContactMethodKind,
  ContactMethodResponse,
  DirectoryGroupLink,
  PermissionDescriptor,
  EscalationDocumentBodyWire,
  EscalationPathDocumentWire,
  EscalationPathRevisionSummary,
  EscalationPathRevisionWire,
  EscalationPathStatus,
  EscalationPathSummary,
  Team,
  TeamEscalationPolicyRevisionSummary,
  TeamEscalationPolicyRevisionWire,
  TeamMember,
  WebhookEndpointRevision,
  WebhookEndpointSecret,
  WebhookEndpointSummary,
  WebhookEndpointTest,
  PipelineUse,
  PipelineReferences,
} from "./types";

export const APP_API_BASE = "/api/v1";

/* Revision histories page the same way everywhere, an edition's
 * revisioned resources included: a page shows the five newest revisions,
 * and Load more brings the next twenty at a time, addressed by the
 * cursor the previous page returned. */
const REVISION_FIRST_PAGE = 5;
const REVISION_NEXT_PAGE = 20;
export const revisionPageQuery = (cursor: string) => {
  const search = new URLSearchParams({
    limit: String(cursor ? REVISION_NEXT_PAGE : REVISION_FIRST_PAGE),
  });
  if (cursor) search.set("cursor", cursor);
  return search.toString();
};

/* An empty agent slug addresses the organization pipeline scope, whose
 * blocks run before the agent's own section for every request. */
const pipelineScopePath = (agentSlug: string) =>
  agentSlug === ""
    ? `${APP_API_BASE}/request-approval-pipelines/organization`
    : `${APP_API_BASE}/request-approval-pipelines/agents/${encodeURIComponent(agentSlug)}`;

export class RequestError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code: string,
    readonly details?: Record<string, unknown>,
  ) {
    super(message);
  }
}

export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if (init?.body) headers.set("Content-Type", "application/json");
  const response = await fetch(path, { ...init, headers, credentials: "same-origin" });
  if (!response.ok) {
    const body = (await response.json().catch(() => ({
      error: { code: "unknown", message: response.statusText },
    }))) as APIError;
    throw new RequestError(body.error.message, response.status, body.error.code, body.error.details);
  }
  /* 204 never carries a body, and some 202s (password reset) say nothing
   * either; others (signup begin) reply with JSON, so parse whatever is
   * there rather than deciding by status. */
  if (response.status === 204) return undefined as T;
  const text = await response.text();
  return (text === "" ? undefined : JSON.parse(text)) as T;
}

export const api = {
  /* A principal's permission policies, under the collection it is
   * addressed in: core's members and teams, or one an edition serves
   * the same way. */
  permissionPolicies: (collection: PermissionPolicyCollection, id: string) => request<PermissionPoliciesPage>(`${APP_API_BASE}/${collection}/${id}/permission_policies`),
  savePermissionPolicy: (collection: PermissionPolicyCollection, id: string, policy: PermissionPolicyInput, key: string, existing?: { id: string; revision: number }) => request<PermissionPolicy>(`${APP_API_BASE}/${collection}/${id}/permission_policies${existing ? `/${existing.id}` : ""}`, { method: existing ? "PUT" : "POST", headers: { "Idempotency-Key": key }, body: JSON.stringify({ ...policy, ...(existing ? { revision: existing.revision } : {}) }) }),
  deletePermissionPolicy: (collection: PermissionPolicyCollection, id: string, policy: PermissionPolicy, key: string) => request<void>(`${APP_API_BASE}/${collection}/${id}/permission_policies/${policy.id}?revision=${policy.revision}`, { method: "DELETE", headers: { "Idempotency-Key": key } }),
  me: () => request<Session>(`${APP_API_BASE}/me`),
  runtimeConfig: () =>
    request<{
      public_origin: string;
      version: string;
      bootstrap_enabled: boolean;
      /* Whether the guided setup can write rules from a description:
       * the deployment configured a model for the assistant. */
      setup_assistant_enabled: boolean;
      webhook_enabled: boolean;
      slack_enabled: boolean;
      discord_enabled: boolean;
      social_providers: SocialProvider[];
      cli_download_url: string;
      /* MCP servers this deployment offers every enrolled agent (the
       * hosted gateway); the CLI registers them at install. */
      agent_mcp_servers: { name: string; url: string; label: string }[];
    }>("/api/runtime-config"),
  bootstrap: (input: Record<string, string>) =>
    request<{ principal: Principal }>("/auth/bootstrap", {
      method: "POST",
      body: JSON.stringify(input),
    }),
  loginMethods: (email: string) =>
    request<LoginMethods>("/auth/login/methods", {
      method: "POST",
      body: JSON.stringify({ email }),
    }),
  login: (email: string, password: string) =>
    request<LoginResponse>("/auth/login", {
      method: "POST",
      body: JSON.stringify({ email, password }),
    }),
  loginSelect: (ticket: string, organizationId: string) =>
    request<{ principal: Principal }>("/auth/login/select", {
      method: "POST",
      body: JSON.stringify({ ticket, organization_id: organizationId }),
    }),
  logout: () => request<void>("/auth/logout", { method: "POST" }),
  changePassword: (currentPassword: string, newPassword: string) =>
    request<void>(`${APP_API_BASE}/me/password`, {
      method: "POST",
      body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }),
    }),
  /* Answers 202 whether or not an account exists: the form is not an
   * oracle for addresses. The raw token travels in the body, never in a
   * URL path. */
  requestPasswordReset: (email: string) =>
    request<void>("/auth/password/reset", {
      method: "POST",
      body: JSON.stringify({ email }),
    }),
  passwordResetPreview: (token: string) =>
    request<{ expires_at: string }>("/auth/password/reset/preview", {
      method: "POST",
      body: JSON.stringify({ token }),
    }),
  completePasswordReset: (token: string, password: string) =>
    request<void>("/auth/password/reset/complete", {
      method: "POST",
      body: JSON.stringify({ token, password }),
    }),
  /* Hosted-only social login (Google, GitHub); 404 in the open edition.
   * Begin returns the provider redirect and sets the flow cookie; the
   * provider sends the browser back to /queue?social=continue, where
   * continue turns the stored assertion into the same shape as login. */
  socialBegin: (
    provider: string,
    input: { intent: "login" | "signup"; organization_name?: string },
  ) =>
    request<{ authorization_url: string; expires_at: string }>(
      `/auth/social/${encodeURIComponent(provider)}/begin`,
      { method: "POST", body: JSON.stringify(input) },
    ),
  socialContinue: () =>
    request<LoginResponse>("/auth/social/continue", { method: "POST" }),
  /* One layer of the review queue: what the reader was told about, what
   * they may decide, or everything they may see. */
  approvals: (scope: QueueScope) =>
    request<{ requests: ApprovalRequest[] }>(
      `${APP_API_BASE}/requests?${new URLSearchParams({ limit: "100", scope })}`,
    ),
  /* One agent's own history: everything it asked for, automatic
   * decisions included. */
  agentApprovals: (agentSlug: string, options: { limit: number; offset: number; q?: string; status?: string }) =>
    request<{ requests: ApprovalRequest[] }>(
      `${APP_API_BASE}/requests?${new URLSearchParams({ agent_slug: agentSlug, limit: String(options.limit), offset: String(options.offset), q: options.q ?? "", status: options.status ?? "" })}`,
    ),
  approval: (id: string) =>
    request<ApprovalRequest>(`${APP_API_BASE}/requests/${id}`),
  /* outsideRouting is the break-glass acknowledgement: a reviewer the
   * routing did not name may decide only by saying so, and the server
   * refuses the decision without it. */
  decide: (
    id: string,
    status: "approved" | "denied",
    note: string,
    options: { outsideRouting?: boolean } = {},
  ) =>
    request(`${APP_API_BASE}/requests/${id}/decision`, {
      method: "POST",
      headers: { "Idempotency-Key": crypto.randomUUID() },
      body: JSON.stringify({
        status,
        note,
        channel: "web",
        ...(options.outsideRouting ? { outside_routing: true } : {}),
      }),
    }),
  /* Pulls a colleague into the request's routing. The server refuses
   * unless the caller is targeted or may decide outside routing. */
  reviewerCandidates: (requestId: string) =>
    request<{ candidates: Array<{ membership_id: string; display_name: string; email: string }> }>(
      `${APP_API_BASE}/requests/${requestId}/reviewer-candidates`,
    ),
  addReviewer: (requestId: string, membershipId: string) =>
    request<ApprovalReview>(`${APP_API_BASE}/requests/${requestId}/reviewers`, {
      method: "POST",
      body: JSON.stringify({ membership_id: membershipId }),
    }),
  contactMethods: () =>
    request<{
      contact_methods: ContactMethod[];
      slack_workspace: { team_name: string } | null;
      discord_server: { guild_name: string } | null;
    }>(
      `${APP_API_BASE}/me/contact-methods`,
    ),
  /* Adds a typed-in method. The answer carries the method and whatever
   * its kind adds beside it: email is sent a code and adds nothing; a
   * kind the edition registered may confirm another way. Such a kind may
   * also require the person's express agreement to be messaged there,
   * which consent carries; email ignores it. */
  addContactMethod: (kind: ContactMethodKind, value: string, consent = false) =>
    request<ContactMethodResponse>(`${APP_API_BASE}/me/contact-methods`, {
      method: "POST",
      body: JSON.stringify({ kind, value, consent }),
    }),
  verifyContactMethod: (id: string, code: string) =>
    request<{ contact_method: ContactMethod }>(`${APP_API_BASE}/me/contact-methods/${id}/verify`, {
      method: "POST",
      body: JSON.stringify({ code }),
    }),
  /* A fresh code, or for a kind that confirms another way, whatever it
   * needs afresh, in the same shape as adding. */
  resendContactMethodCode: (id: string) =>
    request<ContactMethodResponse>(`${APP_API_BASE}/me/contact-methods/${id}/resend`, {
      method: "POST",
    }),
  removeContactMethod: (id: string) =>
    request<void>(`${APP_API_BASE}/me/contact-methods/${id}`, { method: "DELETE" }),
  notificationRules: () =>
    request<NotificationRules>(`${APP_API_BASE}/me/notification-rules`),
  saveNotificationRules: (urgency: NotificationUrgency, rules: NotificationRule[]) =>
    request<NotificationRules>(`${APP_API_BASE}/me/notification-rules/${urgency}`, {
      method: "PUT",
      body: JSON.stringify({ rules }),
    }),
  claim: (id: string) =>
    request<{ claim: Claim }>(`${APP_API_BASE}/requests/${id}/claim`, { method: "POST" }),
  releaseClaim: (id: string) =>
    request<void>(`${APP_API_BASE}/requests/${id}/claim`, { method: "DELETE" }),
  previewAgentEnrollment: (userCode: string) =>
    request<EnrollmentPreview>(`${APP_API_BASE}/agent_enrollments/preview`, {
      method: "POST", body: JSON.stringify({ user_code: userCode }),
    }),
  enrollmentAgents: (q: string, offset: number) =>
    request<EnrollmentAgents>(`${APP_API_BASE}/agent_enrollments/agents?${new URLSearchParams({ q, offset: String(offset), limit: "25" })}`),
  authorizeAgentEnrollment: (userCode: string, agentSlug: string) =>
    request<AgentInstance>(`${APP_API_BASE}/agent_enrollments/authorize`, {
      method: "POST", body: JSON.stringify({ user_code: userCode, agent_slug: agentSlug }),
    }),
  /* The inventory; archived agents come only when asked for. */
  agents: (status: AgentListStatus = "live") =>
    request<{ agents: AgentSummary[] }>(`${APP_API_BASE}/agents?limit=200&status=${status}`),
  agent: (slug: string, options: { limit?: number; offset?: number; q?: string; status?: string; provisionerLimit?: number; provisionerOffset?: number; provisionerQuery?: string; provisionerStatus?: string } = {}) =>
    request<AgentDetail>(`${APP_API_BASE}/agents/${encodeURIComponent(slug)}?${new URLSearchParams({ instance_limit: String(options.limit ?? 25), instance_offset: String(options.offset ?? 0), instance_q: options.q ?? "", instance_status: options.status ?? "", provisioner_limit: String(options.provisionerLimit ?? 25), provisioner_offset: String(options.provisionerOffset ?? 0), provisioner_q: options.provisionerQuery ?? "", provisioner_status: options.provisionerStatus ?? "" })}`),
  setAgentStatus: (slug: string, status: AgentStatus) =>
    request<Agent>(`${APP_API_BASE}/agents/${encodeURIComponent(slug)}`, {
      method: "PATCH",
      body: JSON.stringify({ status }),
    }),
  /* The display name is the one thing about an agent that changes; the
   * slug never does. */
  renameAgent: (slug: string, name: string) =>
    request<Agent>(`${APP_API_BASE}/agents/${encodeURIComponent(slug)}`, {
      method: "PATCH",
      body: JSON.stringify({ name }),
    }),
  /* Archives the agent and its approval pipeline together. Revoked
   * credentials stay revoked; restore brings the agent back. */
  archiveAgent: (slug: string) =>
    request<void>(`${APP_API_BASE}/agents/${encodeURIComponent(slug)}`, { method: "DELETE" }),
  restoreAgent: (slug: string) =>
    request<Agent>(`${APP_API_BASE}/agents/${encodeURIComponent(slug)}/restore`, { method: "POST" }),
  setAgentInstanceStatus: (id: string, status: AgentStatus) =>
    request<AgentInstance>(`${APP_API_BASE}/agent_instances/${id}`, {
      method: "PATCH",
      body: JSON.stringify({ status }),
    }),
  revokeAgentCredential: (id: string) =>
    request<void>(`${APP_API_BASE}/agent-credentials/${id}`, { method: "DELETE" }),
  createAgentProvisioner: (agentSlug: string) =>
    request<{ provisioner: AgentProvisioner; credential: IssuedCredential }>(
      `${APP_API_BASE}/agents/${encodeURIComponent(agentSlug)}/provisioners`,
      { method: "POST" },
    ),
  revokeAgentProvisioner: (id: string) =>
    request<void>(`${APP_API_BASE}/agent-provisioners/${id}`, { method: "DELETE" }),
  createAgent: (slug: string, name: string) =>
    request<Agent>(`${APP_API_BASE}/agents`, {
      method: "POST",
      body: JSON.stringify({ slug, name }),
    }),
  createAgentInstance: (agentSlug: string, name: string) =>
    request<{ agent_instance: AgentInstance; credential: IssuedCredential }>(
      `${APP_API_BASE}/agents/${encodeURIComponent(agentSlug)}/instances`,
      { method: "POST", body: JSON.stringify({ name }) },
    ),
  members: () => request<{ members: Member[] }>(`${APP_API_BASE}/members`),
  teams: (status: "live" | "archived" | "all" = "live") =>
    request<{ teams: Team[] }>(`${APP_API_BASE}/teams?status=${status}`),
  createTeam: (name: string) =>
    request<{ team: Team }>(`${APP_API_BASE}/teams`, {
      method: "POST",
      body: JSON.stringify({ name }),
    }),
  getTeam: (id: string) =>
    request<{ team: Team; members: TeamMember[] }>(
      `${APP_API_BASE}/teams/${id}`,
    ),
  renameTeam: (id: string, name: string) =>
    request<{ team: Team }>(`${APP_API_BASE}/teams/${id}`, {
      method: "PATCH",
      body: JSON.stringify({ name }),
    }),
  archiveTeam: (id: string) =>
    request<void>(`${APP_API_BASE}/teams/${id}`, { method: "DELETE" }),
  /* Webhook endpoints are revisioned like escalation paths: a key with
   * append-only revisions, one active. */
  webhookEndpoints: () =>
    request<{ webhook_endpoints: WebhookEndpointSummary[] }>(
      `${APP_API_BASE}/webhook_endpoints?limit=200`,
    ),
  activeWebhookEndpoint: (endpointKey: string) =>
    request<WebhookEndpointRevision>(
      `${APP_API_BASE}/webhook_endpoints/${encodeURIComponent(endpointKey)}`,
    ),
  webhookEndpointRevisions: (endpointKey: string, cursor = "") =>
    request<RevisionPage<WebhookEndpointRevision>>(
      `${APP_API_BASE}/webhook_endpoints/${encodeURIComponent(endpointKey)}/revisions?${revisionPageQuery(cursor)}`,
    ),
  /* The first revision of a key answers with the signing secret. */
  createWebhookEndpointRevision: (endpointKey: string, document: { name: string; url: string }) =>
    request<WebhookEndpointRevision>(
      `${APP_API_BASE}/webhook_endpoints/${encodeURIComponent(endpointKey)}/revisions`,
      { method: "POST", body: JSON.stringify(document) },
    ),
  activateWebhookEndpointRevision: (
    endpointKey: string,
    revision: number,
    expectedActiveRevision: number | null,
  ) =>
    request<WebhookEndpointRevision>(
      `${APP_API_BASE}/webhook_endpoints/${encodeURIComponent(endpointKey)}/revisions/${revision}/activate`,
      { method: "POST", headers: { "If-Match": `"${expectedActiveRevision ?? 0}"` } },
    ),
  testWebhookEndpointRevision: (endpointKey: string, revision: number) =>
    request<{ test: WebhookEndpointTest }>(
      `${APP_API_BASE}/webhook_endpoints/${encodeURIComponent(endpointKey)}/revisions/${revision}/test`,
      { method: "POST" },
    ),
  /* The active pipeline revisions posting to the key: the same set that
   * refuses archiving it. */
  webhookEndpointUses: (endpointKey: string) =>
    request<{ uses: PipelineUse[] }>(
      `${APP_API_BASE}/webhook_endpoints/${encodeURIComponent(endpointKey)}/uses`,
    ),
  archiveWebhookEndpoint: (endpointKey: string, expectedActiveRevision: number) =>
    request<void>(`${APP_API_BASE}/webhook_endpoints/${encodeURIComponent(endpointKey)}/active`, {
      method: "DELETE",
      headers: { "If-Match": `"${expectedActiveRevision}"` },
    }),
  rotateWebhookEndpointSecret: (endpointKey: string) =>
    request<WebhookEndpointSecret>(
      `${APP_API_BASE}/webhook_endpoints/${encodeURIComponent(endpointKey)}/rotate_secret`,
      { method: "POST" },
    ),
  /* Removing a member the team's active escalation policy names is
   * refused with team_member_in_escalation_policy unless force accepts
   * that the level naming them skips them. */
  setTeamMembers: (id: string, membershipIds: string[], force = false) =>
    request<{ members: TeamMember[] }>(`${APP_API_BASE}/teams/${id}/members`, {
      method: "PUT",
      body: JSON.stringify(
        force ? { membership_ids: membershipIds, force } : { membership_ids: membershipIds },
      ),
    }),
  /* A team's escalation policy is revisioned like an escalation path:
   * append-only numbered revisions, at most one active, and 404 while
   * none is. Activation and archiving carry the active revision in
   * If-Match, "0" while there is none. */
  teamEscalationPolicy: (teamId: string) =>
    request<TeamEscalationPolicyRevisionWire>(`${APP_API_BASE}/teams/${teamId}/escalation-policy`),
  teamEscalationPolicyRevisions: (teamId: string) =>
    request<{ revisions: TeamEscalationPolicyRevisionSummary[] }>(
      `${APP_API_BASE}/teams/${teamId}/escalation-policy/revisions?limit=50`,
    ),
  teamEscalationPolicyRevision: (teamId: string, revision: number) =>
    request<TeamEscalationPolicyRevisionWire>(
      `${APP_API_BASE}/teams/${teamId}/escalation-policy/revisions/${revision}`,
    ),
  createTeamEscalationPolicyRevision: (teamId: string, document: EscalationDocumentBodyWire) =>
    request<TeamEscalationPolicyRevisionWire>(
      `${APP_API_BASE}/teams/${teamId}/escalation-policy/revisions`,
      { method: "POST", body: JSON.stringify(document) },
    ),
  activateTeamEscalationPolicyRevision: (
    teamId: string,
    revision: number,
    expectedActiveRevision: number | null,
  ) =>
    request<TeamEscalationPolicyRevisionWire>(
      `${APP_API_BASE}/teams/${teamId}/escalation-policy/revisions/${revision}/activate`,
      { method: "POST", headers: { "If-Match": `"${expectedActiveRevision ?? 0}"` } },
    ),
  archiveTeamEscalationPolicy: (teamId: string, expectedActiveRevision: number) =>
    request<void>(`${APP_API_BASE}/teams/${teamId}/escalation-policy/active`, {
      method: "DELETE",
      headers: { "If-Match": `"${expectedActiveRevision}"` },
    }),
  toolCatalog: (params: { agent_slug?: string; q?: string; source?: string; status?: string; limit?: number; offset?: number; refresh?: boolean } = {}) => {
    const query = new URLSearchParams();
    for (const [key, value] of Object.entries(params)) if (value !== undefined && value !== "") query.set(key, String(value));
    return request<ToolCatalogPage>(`/api/v1/tool_catalog?${query}`);
  },
  toolCatalogEntry: (id: string, agentSlug: string) => request<ToolCatalogEntry>(`/api/v1/tool_catalog/${encodeURIComponent(id)}?agent_slug=${encodeURIComponent(agentSlug)}`),
  /* Code-owned permission vocabulary. */
  permissions: () =>
    request<{ permissions: PermissionDescriptor[] }>(`${APP_API_BASE}/permissions`),
  /* Replaces the member's manual, direct assignments. Directory-sourced
   * and team-inherited rows are not the member's own and are untouched. */

  /* Suspending a member an active escalation path targets is refused with
   * membership_in_escalation_path unless force accepts leaving those
   * targets pointing at nobody; reactivation never needs it. */
  setMemberStatus: (id: string, status: "active" | "suspended", force = false) =>
    request<{ member: Member }>(`${APP_API_BASE}/members/${id}`, {
      method: "PATCH",
      body: JSON.stringify(force ? { status, force } : { status }),
    }),
  removeMember: (id: string, force = false) =>
    request<void>(`${APP_API_BASE}/members/${id}${force ? "?force=true" : ""}`, {
      method: "DELETE",
    }),
  invitations: () =>
    request<{ invitations: Invitation[] }>(`${APP_API_BASE}/invitations`),
  createInvitation: (email: string, permissionPolicies: PermissionPolicyInput[]) =>
    request<{ invitation: Invitation; invite_url?: string }>(
      `${APP_API_BASE}/invitations`,
      { method: "POST", body: JSON.stringify({ email, permission_policies: permissionPolicies }) },
    ),
  resendInvitation: (id: string) =>
    request<{ invitation: Invitation; invite_url?: string }>(
      `${APP_API_BASE}/invitations/${id}/resend`,
      { method: "POST" },
    ),
  revokeInvitation: (id: string) =>
    request<void>(`${APP_API_BASE}/invitations/${id}`, { method: "DELETE" }),
  /* The raw token travels in the body, never in a URL path. */
  invitationPreview: (token: string) =>
    request<InvitationPreview>("/auth/invitations/preview", {
      method: "POST",
      body: JSON.stringify({ token }),
    }),
  acceptInvitation: (
    token: string,
    body: { display_name?: string; password?: string },
  ) =>
    request<{ principal: Principal }>("/auth/invitations/accept", {
      method: "POST",
      body: JSON.stringify({ token, ...body }),
    }),
  slackConnection: () =>
    request<{ connection: SlackConnection | null }>(`${APP_API_BASE}/enterprise/slack`),
  beginSlackInstall: () =>
    request<{ authorize_url: string }>(`${APP_API_BASE}/enterprise/slack/install`, {
      method: "POST",
    }),
  syncSlackMembers: () =>
    request<{ members: number; linked: number }>(`${APP_API_BASE}/enterprise/slack/sync`, {
      method: "POST",
    }),
  disconnectSlack: () =>
    request<void>(`${APP_API_BASE}/enterprise/slack`, { method: "DELETE" }),
  linkSlack: () =>
    request<{ contact_method: ContactMethod }>(`${APP_API_BASE}/me/slack/link`, {
      method: "POST",
    }),
  discordConnection: () =>
    request<{ connection: DiscordConnection | null }>(`${APP_API_BASE}/enterprise/discord`),
  beginDiscordInstall: () =>
    request<{ authorize_url: string }>(`${APP_API_BASE}/enterprise/discord/install`, {
      method: "POST",
    }),
  disconnectDiscord: () =>
    request<void>(`${APP_API_BASE}/enterprise/discord`, { method: "DELETE" }),
  beginDiscordLink: () =>
    request<{ authorize_url: string }>(`${APP_API_BASE}/me/discord/link`, { method: "POST" }),
  unlinkDiscord: () => request<void>(`${APP_API_BASE}/me/discord`, { method: "DELETE" }),
  /* Directory groups linked to teams, and the grant each carries. The
   * grant is keyed by the provider's object id because that is what the
   * directory sends. The links are written by the edition's directory
   * sync; core only reads them and holds the grants. */
  directoryGroups: () =>
    request<{ groups: DirectoryGroupLink[] }>(`${APP_API_BASE}/enterprise/directory/groups`),
  setDirectoryGroupGrant: (providerObjectId: string, grant: PermissionPolicyInput) =>
    request<void>(
      `${APP_API_BASE}/enterprise/directory/groups/${encodeURIComponent(providerObjectId)}/grant`,
      { method: "PUT", body: JSON.stringify(grant) },
    ),
  clearDirectoryGroupGrant: (providerObjectId: string) =>
    request<void>(
      `${APP_API_BASE}/enterprise/directory/groups/${encodeURIComponent(providerObjectId)}/grant`,
      { method: "DELETE" },
    ),
  /* The overview; pipelines archived with the last agent of their type
   * come only when asked for. */
  requestApprovalPipelines: (status: PipelineListStatus = "live") =>
    request<{ request_approval_pipelines: RequestApprovalPipelineSummary[] }>(
      `${APP_API_BASE}/request-approval-pipelines?limit=100&status=${status}`,
    ),
  /* The scope's active revision with its blocks; 404 when no agent has
   * the slug or its pipeline is archived. */
  activeRequestApprovalPipeline: (agentSlug: string) =>
    request<RequestApprovalPipelineRevision>(pipelineScopePath(agentSlug)),
  requestApprovalPipelineRevisions: (agentSlug: string, cursor = "") =>
    request<RevisionPage<RequestApprovalPipelineRevisionSummary>>(
      `${pipelineScopePath(agentSlug)}/revisions?${revisionPageQuery(cursor)}`,
    ),
  requestApprovalPipelineRevision: (agentSlug: string, revision: number) =>
    request<RequestApprovalPipelineRevision>(
      `${pipelineScopePath(agentSlug)}/revisions/${revision}`,
    ),
  previewRequestApprovalPipeline: (
    agentSlug: string,
    document: RequestApprovalPipelineDocument,
    sample: RequestApprovalPipelinePreviewSample,
  ) =>
    request<RequestApprovalPipelinePreview>(
      `${pipelineScopePath(agentSlug)}/preview`,
      {
        method: "POST",
        body: JSON.stringify({ ...document, sample }),
      },
    ),
  createRequestApprovalPipelineRevision: (
    agentSlug: string,
    document: RequestApprovalPipelineDocument,
  ) =>
    request<RequestApprovalPipelineRevision>(
      `${pipelineScopePath(agentSlug)}/revisions`,
      { method: "POST", body: JSON.stringify(document) },
    ),
  /* The live state of everything a revision depends on, so a rollback
   * can be explained before it is attempted. */
  requestApprovalPipelineRevisionReferences: (agentSlug: string, revision: number) =>
    request<PipelineReferences>(
      `${pipelineScopePath(agentSlug)}/revisions/${revision}/references`,
    ),
  activateRequestApprovalPipelineRevision: (
    agentSlug: string,
    revision: number,
    expectedActiveRevision: number,
  ) =>
    request<RequestApprovalPipelineRevision>(
      `${pipelineScopePath(agentSlug)}/revisions/${revision}/activate`,
      {
        method: "POST",
        headers: { "If-Match": `"${expectedActiveRevision}"` },
      },
    ),
  /* The guided setup (design doc 09). Starting a session supersedes any
   * open one; the commands in the answer carry the single-use setup code
   * and are never returned again. */
  onboardingBegin: () =>
    request<OnboardingBegin>(`${APP_API_BASE}/onboarding/sessions`, {
      method: "POST",
      body: "{}",
    }),
  /* 404 onboarding_session_not_found when the member has no session. */
  onboardingSession: () =>
    request<OnboardingSession>(`${APP_API_BASE}/onboarding/session`),
  onboardingSelectAgents: (runtimes: string[]) =>
    request<OnboardingSession>(`${APP_API_BASE}/onboarding/session/agents`, {
      method: "PUT",
      body: JSON.stringify({ runtimes }),
    }),
  /* Stamps channels_set_at; provisioning waits for it. */
  onboardingConfirmChannels: () =>
    request<OnboardingSession>(`${APP_API_BASE}/onboarding/session/channels`, {
      method: "POST",
    }),
  /* On failure the session is unchanged with last_error set and the
   * answer is 502 onboarding_provision_failed. */
  onboardingProvision: () =>
    request<OnboardingSession>(`${APP_API_BASE}/onboarding/session/provision`, {
      method: "POST",
      body: "{}",
    }),
  /* One turn of the setup assistant. The answer arrives when the turn
   * is done; the session events stream shows the actions in between.
   * 501 setup_assistant_unconfigured without a model, 409
   * onboarding_assistant_busy while the previous turn runs, 502
   * onboarding_assistant_failed for a turn recorded as failed. */
  onboardingAsk: (message: string) =>
    request<OnboardingSession>(`${APP_API_BASE}/onboarding/session/assistant`, {
      method: "POST",
      body: JSON.stringify({ message }),
    }),
  /* Forgets the conversation; the rules stay. */
  onboardingClearAssistant: () =>
    request<OnboardingSession>(`${APP_API_BASE}/onboarding/session/assistant`, {
      method: "DELETE",
    }),
  /* The by-hand alternative: the named tools wait, the rest runs.
   * 400 tool_not_discovered for a name the CLI did not report. */
  onboardingSetGatedTools: (runtime: string, tools: string[]) =>
    request<OnboardingSession>(`${APP_API_BASE}/onboarding/session/gated-tools`, {
      method: "PUT",
      body: JSON.stringify({ runtime, tools }),
    }),
  /* Stamps rules_set_at once the hooks are installed. */
  onboardingRecordRules: () =>
    request<OnboardingSession>(`${APP_API_BASE}/onboarding/session/rules`, {
      method: "POST",
    }),
  /* The request the person decided in the test; 400
   * test_request_pending until it is decided. */
  onboardingRecordTest: (requestId: string) =>
    request<OnboardingSession>(`${APP_API_BASE}/onboarding/session/test`, {
      method: "POST",
      body: JSON.stringify({ request_id: requestId }),
    }),
  onboardingComplete: (outcome: OnboardingOutcome) =>
    request<void>(`${APP_API_BASE}/onboarding/completion`, {
      method: "POST",
      body: JSON.stringify({ outcome }),
    }),
  /* Personal API keys: the signed-in member's own, with the policy
   * and the permissions a key may be scoped to. Creating one answers
   * with the raw token exactly once. */
  myAPIKeys: () => request<MyAPIKeys>(`${APP_API_BASE}/me/api_keys`),
  createAPIKey: (input: APIKeyInput) =>
    request<{ key: APIKey; token: string }>(`${APP_API_BASE}/me/api_keys`, {
      method: "POST",
      body: JSON.stringify(input),
    }),
  /* The MCP server's consent page: what the client asked for, then the
   * person's decision, answered with where the browser goes next. */
  previewOAuthAuthorization: (input: OAuthAuthorizationRequest) =>
    request<OAuthAuthorizationPreview>(`${APP_API_BASE}/oauth_authorizations/preview`, {
      method: "POST",
      body: JSON.stringify(input),
    }),
  createOAuthAuthorization: (input: OAuthAuthorizationDecision) =>
    request<{ redirect_url: string }>(`${APP_API_BASE}/oauth_authorizations`, {
      method: "POST",
      body: JSON.stringify(input),
    }),
  revokeMyAPIKey: (id: string) =>
    request<void>(`${APP_API_BASE}/me/api_keys/${id}`, { method: "DELETE" }),
  /* The organization's keys of either kind, behind api_key.read;
   * revoking one needs api_key.revoke. Issuing an organization key and
   * editing its policies are an edition's routes. */
  getAPIKey: (id: string) => request<APIKey & { effective_grants: Grant[] }>(`${APP_API_BASE}/api_keys/${id}`),
  organizationAPIKeys: (kind: APIKey["kind"]) => request<{ keys: APIKey[] }>(`${APP_API_BASE}/api_keys?kind=${kind}`),
  revokeAPIKey: (id: string) =>
    request<void>(`${APP_API_BASE}/api_keys/${id}`, { method: "DELETE" }),
  organizationPolicy: () =>
    request<{ policy: OrganizationPolicy }>(`${APP_API_BASE}/organization/policy`),
  setOrganizationPolicy: (patch: Partial<OrganizationPolicy>) =>
    request<{ policy: OrganizationPolicy }>(`${APP_API_BASE}/organization/policy`, {
      method: "PATCH",
      body: JSON.stringify(patch),
    }),
  /* The organization's own tool presentations with the shipped catalog
   * they layer over; writes need organization.settings.write. */
  toolPresentations: () => request<ToolPresentationsPage>(`${APP_API_BASE}/tool_presentations`),
  createToolPresentation: (input: ToolPresentationInput) =>
    request<ToolPresentation>(`${APP_API_BASE}/tool_presentations`, {
      method: "POST",
      body: JSON.stringify(input),
    }),
  updateToolPresentation: (id: string, input: ToolPresentationInput) =>
    request<ToolPresentation>(`${APP_API_BASE}/tool_presentations/${id}`, {
      method: "PUT",
      body: JSON.stringify(input),
    }),
  deleteToolPresentation: (id: string) =>
    request<void>(`${APP_API_BASE}/tool_presentations/${id}`, { method: "DELETE" }),
  approvalRouting: (id: string) =>
    request<ApprovalRouting>(`${APP_API_BASE}/requests/${id}/routing`),
  approvalReview: (id: string) =>
    request<ApprovalReview>(`${APP_API_BASE}/requests/${id}/review`),
  /* The live paths by default; archived ones on request. */
  escalationPaths: (status: EscalationPathStatus = "live") =>
    request<{ escalation_paths: EscalationPathSummary[] }>(
      `${APP_API_BASE}/escalation-paths?status=${status}&limit=200`,
    ),
  activeEscalationPath: (pathKey: string) =>
    request<EscalationPathRevisionWire>(
      `${APP_API_BASE}/escalation-paths/${encodeURIComponent(pathKey)}`,
    ),
  /* The path's history, newest first. */
  escalationPathRevisions: (pathKey: string, cursor = "") =>
    request<RevisionPage<EscalationPathRevisionSummary>>(
      `${APP_API_BASE}/escalation-paths/${encodeURIComponent(pathKey)}/revisions?${revisionPageQuery(cursor)}`,
    ),
  escalationPathRevision: (pathKey: string, revision: number) =>
    request<EscalationPathRevisionWire>(
      `${APP_API_BASE}/escalation-paths/${encodeURIComponent(pathKey)}/revisions/${revision}`,
    ),
  /* Creates the path with its first revision; a key that already exists
   * answers 409 escalation_path_exists. */
  createEscalationPath: (pathKey: string, document: EscalationPathDocumentWire) =>
    request<EscalationPathRevisionWire>(`${APP_API_BASE}/escalation-paths`, {
      method: "POST",
      body: JSON.stringify({ path_key: pathKey, ...document }),
    }),
  /* Appends the next revision to an existing path; an unknown key is 404. */
  createEscalationPathRevision: (pathKey: string, document: EscalationPathDocumentWire) =>
    request<EscalationPathRevisionWire>(
      `${APP_API_BASE}/escalation-paths/${encodeURIComponent(pathKey)}/revisions`,
      { method: "POST", body: JSON.stringify(document) },
    ),
  activateEscalationPathRevision: (
    pathKey: string,
    revision: number,
    expectedActiveRevision: number | null,
  ) =>
    request<EscalationPathRevisionWire>(
      `${APP_API_BASE}/escalation-paths/${encodeURIComponent(pathKey)}/revisions/${revision}/activate`,
      { method: "POST", headers: { "If-Match": `"${expectedActiveRevision ?? 0}"` } },
    ),
  /* The active pipeline revisions naming the path: the same set that
   * refuses archiving it. */
  escalationPathUses: (pathKey: string) =>
    request<{ uses: PipelineUse[] }>(
      `${APP_API_BASE}/escalation-paths/${encodeURIComponent(pathKey)}/uses`,
    ),
  /* Archives the path, revisions included. It stays readable but is never
   * active again; refused with escalation_path_in_use while an active
   * pipeline revision names it. */
  archiveEscalationPath: (pathKey: string) =>
    request<void>(`${APP_API_BASE}/escalation-paths/${encodeURIComponent(pathKey)}`, {
      method: "DELETE",
    }),
};
