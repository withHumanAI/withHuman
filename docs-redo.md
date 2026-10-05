# Docs redo

Audit of the withhuman.ai docs (`withHumanSaaS/marketing_website/content/docs`
on `origin/main`) against the product (core `main`, hosted `origin/main`),
2026-10-05. Paths under "evidence" are core unless marked hosted.

## 1. Pipeline model (organization + per-agent pipelines → `entry` + keyed pipelines)

Every request now runs the reserved `entry` pipeline; other pipelines are keyed,
created explicitly and reached only through `delegate` blocks. Agents own no
pipeline. Evidence: CLAUDE.md "Pipelines are keyed objects",
`docs/design/16-pipeline-delegation.md`, `frontend/src/app/pipelines.tsx`.

- [ ] Rewrite `concepts/approval-pipelines.mdx` (diagrams, refund example, "rules for individual agents")
- [ ] Rewrite `web-app/approval-pipelines.mdx`
  - [ ] Overview: entry card, "Delegated pipelines" table (Delegated from, Status), Active/Archived filter, New pipeline, "If no decision is made" card
  - [ ] Human-review fallback order: block path → current pipeline default → delegating pipelines' defaults outward → any authorized reviewer (`internal/approvalpipeline/execution.go:548-594`)
  - [ ] Empty `entry` sends everything to default reviewers (not "passes to agents")
  - [ ] Testing: overview tests from `entry`; a pipeline page tests that pipeline with delegates expanded
  - [ ] Permissions are pipeline-scoped, not agent-scoped
  - [ ] Tool suggestions come from org-wide `GET /tool_definitions` + recent requests, not the agent's catalogue
  - [ ] Add: creating a pipeline (key chosen once, never reused), archiving (refused for `entry` and while delegated to)
  - [ ] Add an `#pipelines` anchor (linked from `pipeline-new.tsx:107`)
- [ ] New page `web-app/delegate-blocks.mdx` with `#when-a-delegate-runs` and `#the-target-pipeline` (the app already links there)
- [ ] `getting-started/quickstart.mdx`: setup writes one `entry` revision with a branch per runtime, not "each agent's own pipeline"; "organization pipeline runs first" wording
- [ ] `concepts/agents.mdx`: remove "Agent pipelines" section and diagram
- [ ] `web-app/agents.mdx`: remove every "agent's pipeline" mention (create, attach, archive, restore, Overview link)
- [ ] `web-app/provisioner-tokens.mdx`: instances don't "use the agent's pipeline"
- [ ] `concepts/escalation-paths.mdx`: "agent pipelines and the organization pipeline share a path" framing and example
- [ ] `web-app/webhook-blocks.mdx`, `web-app/ai-judge-blocks.mdx`, `web-app/mcp-server.mdx`: refund examples use org/agent pipelines
- [ ] `reference/intros/approval-pipelines.mdx`: org + agent pipeline text; add `POST /request-approval-pipelines`, `DELETE /request-approval-pipelines/{key}`, `/uses`, delegate preview, `previous_metadata/<pipeline_key>/<block_key>`
- [ ] `instructions.md`: drop "each agent has its own approval pipeline"; add `mcp-server` and `gateway-tunnels` to STRUCTURE

## 2. Block types

- [ ] `web-app/conditions-blocks.mdx`: the block type is gone (`cel` removed 2026-09-29). Turn the field and operator lists into a shared "Conditions" reference used by branch, delegate and path If/else
- [ ] Rename Always → **Outcome block** everywhere; add `#outcome-blocks` anchor (linked from `ds/components/outcome-block.tsx:32`)
- [ ] Add-menu lists: Outcome block, Webhook block, Branch, Delegate to pipeline (+ AI Judge block on hosted). Fix in `approval-pipelines.mdx` and `ai-judge-blocks.mdx`
- [ ] `web-app/branch-blocks.mdx`
  - [ ] Reason is optional (`approvalblock/branch.go:21-24`)
  - [ ] An unreachable branch end is not drawn (not "shows Unreachable")
  - [ ] "When works like a Conditions block" → point to shared Conditions reference
  - [ ] Example uses a Conditions block → branch with Outcome block
  - [ ] Branch end can hand to **The agent's owner**
- [ ] `reference/intros/approval-pipelines.mdx`: types are `outcome`, `webhook`, `branch`, `delegate` (+ `llm_judge` hosted); fix JSON examples using `always` / `cel` / `on_match`; branch `reason` optional
- [ ] `instructions.md`: remove "23 visual operators" Conditions page spec and "Always blocks inside a branch"

## 3. Roles and permissions

Open edition offers only predefined roles (owner, admin, reviewer, auditor);
custom permission policies are hosted-only. Evidence: `domain/roles.go`,
`frontend/src/app/roles.tsx`, `permission-policies.tsx:586`.

- [ ] `web-app/permissions.mdx`
  - [ ] Present predefined roles first; mark custom policies hosted-only
  - [ ] Document **Change role** (`PUT /members/{id}/role`, replaces all direct grants)
  - [ ] Ownership is the `owner` role (only an owner grants it), not separate from policies
  - [ ] Add **Pipelines** scope; Agents scope no longer covers pipelines
  - [ ] Pipeline-scope grants: how a team owns a delegated pipeline
- [ ] Add a "which role holds which permissions" table (note: admin has no `request.read` / `request.decide`)
- [ ] `web-app/permission-reference.mdx`
  - [ ] `pipeline.*` scope: Organization or Pipelines
  - [ ] `pipeline.write` also creates pipelines (org scope); `pipeline.activate` also archives
  - [ ] `permission_policy.*` scope also allows Pipelines
  - [ ] Remove "Editing one agent's pipeline" example and "Agents scope covers pipelines"
  - [ ] Frame per-permission picking as hosted-only
- [ ] `web-app/membership-management.mdx`
  - [ ] Invite dialog picks a role (default Reviewer); Custom permissions tab hosted-only
  - [ ] Column is **Role** (badges, **Custom**)
  - [ ] **Get link** → **Resend**; link shown only when email delivery isn't configured
  - [ ] Deprovisioning also disables personal-agent instances and hands agent-owner requests to the fallback path
  - [ ] Acceptance gives the invitation's role
- [ ] `web-app/team-administration.mdx`: team permissions are hosted-only; add Pipelines scope and team ownership of a pipeline
- [ ] `web-app/organization-api-keys.mdx`: Pipelines scope; fix "Pipeline reporter" example; archiving an agent or pipeline removes the key's assignments there
- [ ] `reference/intros/members.mdx`: direct access is a role (`PUT /members/{id}/role`, `GET /roles`)
- [ ] `reference/intros/teams.mdx`: team policies hosted-only
- [ ] `reference/intros/invitations.mdx`: link returned only without email delivery; invitation carries a `role`

## 4. Audit log: edition caveats and event types

- [ ] Mark every "recorded in Audit log" as hosted-only (core keeps no audit log): webhook-endpoints, organization-policies, tool-presentations, slack-integration, discord-integration, integrations, immutability, mcp-server, agents, review-queue
- [ ] Remove audit claims for things never audited: verified domains, SSO/directory connection setup (`verified-domains.mdx`, `sign-in-and-directory.mdx`)
- [ ] `web-app/audit-log.mdx`: drop "every action produces an event"; request walkthrough says "agent's approval pipeline"
- [ ] `web-app/audit-event-types.mdx`
  - [ ] Pipeline evaluation intro: entry + delegated pipelines, not "organization and agent stages"
  - [ ] `request_approval_pipeline.started` fields: `entry_revision`, `pipelines[]` (`execution.go:637-662`)
  - [ ] `request_approval_block.evaluated`: `pipeline_key`, `block_key`, `position`, no `scope`; `end_of_branch` also on delegates
  - [ ] `request_approval_pipeline.suspended`: `suspended_block_pipeline_key`, `resume_position`, `attempted_block_count`
  - [ ] `request_approval_pipeline.completed`: add `terminal_block_name`, `pipelines[]`
  - [ ] `revision_created` / `activated`: `pipeline_key`, `name`, `revision`
  - [ ] `request_approval_pipeline.archived`: now archiving a pipeline key
  - [ ] Remove `request_approval_pipeline.restored`
  - [ ] `agent.archived` / `agent.restored`: drop `pipeline_archived` / `pipeline_restored`
  - [ ] Add `slack_connection.disconnected` (System, `team_id`, `reason`)
  - [ ] `webhook_endpoint.deactivated` comes from the Archive action
  - [ ] Explain a role change shows as `role_assignment.revoked` + `role_assignment.granted`; `role.created` / `role.deleted` not emitted in practice

## 5. Routing and decisions

- [ ] Reachable-targets-decide: only targets the path can reach for this request (plus added reviewers) may decide; others use break glass (**Decide outside routing?**). `concepts/escalation-paths.mdx`, `web-app/review-queue.mdx`
- [ ] Agent-owner route (`ask_agent_owner`, fallback path, handoff on suspension). `concepts/agents.mdx`, `concepts/escalation-paths.mdx`, `branch-blocks.mdx`, reference
- [ ] Cancelled requests show as **Withdrawn**; only the requesting instance can withdraw. `concepts/requests-and-decisions.mdx`, `review-queue.mdx`
- [ ] `web-app/review-queue.mdx`
  - [ ] Adding a reviewer does not notify them; it only lands in their **Sent to you**
  - [ ] Automatic decisions are in the agent's Approval requests tab (open edition), not "the audit log"
  - [ ] "Reason for asking a human" cause row is hosted-only
- [ ] `web-app/escalation-paths.mdx` + `reference/intros/escalation-paths.mdx`: create with a key (never reused), first revision starts inactive, archive is permanent, `/uses`; "reach nobody" means only break-glass can decide
- [ ] `concepts/immutability.mdx`: tool presentations are hard-deleted; archiving also covers pipelines, paths, webhook endpoints; a new pipeline's first revision is active on creation (paths' isn't)
- [ ] `web-app/organization-policies.mdx`: organization API key switch is hosted-only; remove "self-approval" from screenshot alt text
- [ ] `reference/intros/webhook-endpoints.mdx`: add archive (`DELETE .../active`) and `/uses`

## 6. Agents, CLI and API reference

- [ ] Agent slug vs name (immutable, never reused). `concepts/agents.mdx`
- [ ] Agent `kind` (service / personal) and instance ownership; `instructions.md` says "no separate agent type"
- [ ] `concepts/agents.mdx`: a leaving member's instances are disabled and credentials revoked, rows kept
- [ ] `web-app/agents.mdx`
  - [ ] Status **Connected** → **Active** (`agent-state.ts:22-27`)
  - [ ] Availability labels: Reported, Granted, Unavailable, Stale; no **Observed** (`agent-tools.tsx:64-72`)
  - [ ] Overview cards: Approval requests, Instances, Provisioner tokens (hidden for personal agents)
  - [ ] Personal agents have no Provisioner tokens tab
  - [ ] Instance name defaults to hostname + suffix; `--instance` flag
- [ ] `getting-started/quickstart.mdx`: suggested tools (edit tools not suggested; mutating MCP tools are); finish screen is "Setup complete" with three actions
- [ ] `reference/intros/agent-enrollment.mdx`: `requested_agent_name` → `requested_agent_slug`; instance name unique within the agent
- [ ] `reference/intros/agent-approval-protocol.mdx`: add async `delivery` mode (signed `request.resolved`, 7-day retries, verification challenge), provisioner read/update/delete of instances, request cancellation
- [ ] `reference/overview.mdx`
  - [ ] Agent credential can also cancel its requests, read `/agent_instances/self`, report tools
  - [ ] Credentials also come from `withhuman onboard` and the New instance page's API key option
  - [ ] Mention management MCP server `/api/mcp/v1` and OAuth routes
  - [ ] Idempotency keys expire after seven days (contradicts "kept indefinitely")
  - [ ] Agent quota refusal is 409 `quota_exceeded`, not 429
- [ ] `web-app/api-keys.mdx`, `personal-api-keys.mdx`, `reference/intros/api-keys.mdx`: OAuth-connected keys (**Connected agent** badge, revoking disconnects); org keys hosted-only; `audit.read` example is hosted-only
- [ ] `web-app/account.mdx`: email-first sign-in with organization picker; "invalid credentials"
- [ ] `reference/intros/account.mdx`: `/api/v1` also accepts personal API key bearers, not only the session cookie
- [ ] `web-app/sign-in-and-directory.mdx`: required SSO also refuses social login
- [ ] `web-app/directory-sync.mdx`: deprovisioning also disables personal-agent instances
- [ ] `web-app/mcp-gateway.mdx`: "organizational agent" → "service agent"
- [ ] `instructions.md`: update Agents spec (kind, New instance page, Tool Catalogue, Gateway Tools) and MCP Gateway spec (CLI registers `withhuman-gateway`, scopes, member credentials, `withhuman` server, tunnels)

## 7. Notifications and integrations

- [ ] `web-app/mobile-apps.mdx`: a claim cancels remaining messages for good, only escalation pauses; app queue lacks **Sent to you** / **Yours to decide**; iOS offers Google and GitHub sign-in
- [ ] `web-app/phone-app-notifications.mdx` + `billing.mdx`: Free plan has no mobile push (hosted `catalog/catalog.go`)
- [ ] `web-app/notifications.mdx`: who is notified with no path (every member who can decide) and with the agent-owner route; how a request becomes Interrupt (only a path's **Raise to interrupt**); claim lapse resumes escalation but cancelled messages aren't re-sent
- [ ] `web-app/slack-integration.mdx`: self-host setup (manifest, `WITHHUMAN_SLACK_*` + `WITHHUMAN_SECRETS_KEY`, events and interactivity URLs)
- [ ] `web-app/discord-integration.mdx`: self-host setup (`WITHHUMAN_DISCORD_*`, `deployment/discord/README.md`)
- [ ] `web-app/slack-notifications.mdx`: "link again" when Unavailable, but the add list shows Linked with no button (docs or product fix); members can't unlink Slack themselves

## Product-side fixes found along the way

- [ ] `frontend/src/app/connect.tsx:46-48` and `agent-instance-new.tsx:137` still say instances share an agent's approval pipeline
- [ ] `frontend/src/app/organization.tsx:292` says routed-only readers see "requests their own agents made"; `internal/postgres/reviewers.go:244-311` doesn't appear to do that. Decide which is right
- [ ] Slack re-link affordance when a link is Unavailable (`add-channel.tsx:64,107-113`)
