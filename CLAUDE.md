# withHuman

This repository (`github.com/withHumanAI/withHuman`) is the self-contained
implementation of withHuman: the single binary, its protocol adapters, the
web frontend and the command-line tool. The hosted edition at withhuman.ai
builds on it from a separate, private repository (`withHumanAI/withHumanSaaS`)
that imports this module and never the reverse. Run every command in this
file from the repository root.

`AGENTS.md` must remain a relative symlink to this file.

## Coding conventions

All new and changed code must follow
[`CODING_CONVENTIONS.md`](CODING_CONVENTIONS.md). Any exception must be
recorded in this file with both the reason and the replacement rule.

## Repository boundary

- This repository must build, test, document, and run on its own. Nothing
  here may depend on the hosted edition; the hosted edition composes this
  module through the exported seams (`runtime`, the registries, the ports)
  and is free to change without a change here. Design documents live in
  `docs/design/` as a snapshot; where a document and the code disagree,
  the code has moved on and the document records the reasoning at the
  time.
- The Go module path is `github.com/withHumanAI/withHuman`.
- Do not copy privately licensed marketing fonts or assets into core. Product
  assets must be independently distributable.
- This is a prototype. Do not add compatibility shims, migrations for
  superseded prototype interfaces, deprecation windows, or legacy fallbacks.
  Change the design directly and delete the obsolete path.
- Never use em dashes in user-facing product copy.
- Status is always a labeled `Badge`, never a bare status dot. The design
  system deliberately has no status-dot component: each tone has a status
  icon (hollow circle, progress ring, check, cross, exclamation) that
  draws the state, and that toned icon may stand alone only as internal
  anatomy of a component that also carries a label (toast, timeline rail).
  Loading animations are separate: the review queue uses `LoadingDots` with
  an accessible reconnecting label instead of a disconnected banner, keeping
  the queue layout stable while live updates recover.
- Every workspace screen lays out inside the design system's `Page`
  (`frontend/src/ds/components/page.tsx`), which owns the column's
  padding, gap and one of three measures: `narrow` for something you
  read and act on, the default for lists and forms, `wide` for dense
  tables. Never write the container classes by hand on a screen.
- Page-level back navigation uses `PageHeader` with `PageBackLink` (or
  `PageBackButton` for a callback) from `frontend/src/ds/components/page-header.tsx`.
  Match Agent view: a 14px left arrow and muted label on a separate left-aligned
  row, 12px above the title and actions. Do not place it beside the title or in
  the actions row. The header owns spacing and collapsed-sidebar clearance.

- Enrollment authorization uses its single-use user code as the operation retry
  identity instead of a separate `Idempotency-Key` header. Repeating the same
  organization and agent returns the existing instance; retargeting conflicts.

## Architecture invariants

- `runtime.NewServer` composes the public, stateless HTTP boundary with the
  built product frontend embedded, and `runtime.RunWorker` the Temporal
  workflows and activities. Core ships both in one process as `withhuman`;
  the hosted edition runs them as its own split processes (`saas-server`,
  `saas-worker`, `saas-migrate` in the hosted edition), and the Compose
  stack and Kubernetes chart for that topology live with it. Application
  database migrations are embedded from `db/migrations` via the `db`
  package; `db.Apply` is the one runner, and an edition's own chain uses
  it with `db.WithTable` to keep its record apart.
- A migration file that has shipped in a tagged release is frozen: the
  runner applies each file once by name, so an in-place edit silently never
  reaches any database that already ran it (this once broke the dev
  deployment). Schema changes always go in a new numbered file. Prototype
  mode's no-backwards-compatibility rule is about interfaces and designs;
  it does not extend to editing applied migrations. The chain restarted as
  one `0001_initial.sql` at the repository split (2026-09-24), generated
  from a dump of the schema the old files built; the hosted edition's
  chain restarted the same way, and every deployment was recreated.
- `withhuman` (`cmd/withhuman`) is the single-binary topology: embedded
  PostgreSQL as a child process, the Temporal server in-process, worker and
  HTTP server in one process. The exported `singlebinary` package composes
  it from `runtime.NewServer` and `runtime.RunWorker`, the same
  composition the split mains run, handing each a `config.Config` derived
  from the embedded services (`ServerOptions.Config`,
  `WorkerOptions.Config`; the worker binds no health listener when
  `WorkerHealthAddress` is empty, and `Server.Listen` plus `Server.Serve`
  give the process the resolved port before serving). Every edition
  seam therefore exists exactly once, and an edition's single-process
  main passes its server and worker options through
  `singlebinary.Config`. Only single-process mains and tests may import
  `singlebinary`, `internal/embeddedpg`, and `internal/embeddedtemporal`;
  the split binaries must never pay for the embedded Temporal server or
  embedded PostgreSQL dependency graphs. The one further exception is
  `db/dbtest`, the exported test helper that boots a migrated embedded
  PostgreSQL for integration tests in other modules; it is test-only and
  is never linked into a binary. No component may assume co-location:
  server, worker, Temporal, and PostgreSQL always communicate through the
  same client interfaces in both topologies.
- PostgreSQL is the product read model. Temporal owns durable
  orchestration, but product reads must not query Temporal.
- Core keeps no audit log. Every write still builds its `domain.AuditEvent`
  where the knowledge is (the store, the pipeline executor, routing
  evaluation, orchestration activities) and hands it to the edition's
  `db.AuditSink` inside the transaction that commits the action
  (`runtime.ServerOptions.AuditSink`, `WorkerOptions.AuditSink`); the
  open edition discards them (`db.DiscardAudit`, the store's default) and
  keeps only the append-only `decisions`, the hosted edition keeps its own
  table and serves the audit log and the request timeline from it.
  `db.PurgeHook` lets an edition remove its own rows inside core's tenant
  purge. Core's store tests attach a table sink of their own
  (`internal/postgres/audit_test.go`) so they assert exactly what a
  keeping edition would have stored, rollbacks included; process-level
  tests use `db.RecordingAudit`.
- Deterministic local request-approval blocks execute synchronously in the
  server hot path. Automatic decisions commit directly and never require
  Temporal. Asynchronous blocks (`llm_judge`, `webhook`) suspend the pipeline: the
  request commits as pending with a row in
  `approval_request_pipeline_suspensions`, unrouted, and the request's
  workflow resumes the pipeline in a worker activity on the pinned
  revisions, routing the request only if it still needs a human. The
  worker therefore compiles pipelines with the same registry the server
  uses. Pending human review enters Temporal through the durable
  PostgreSQL outbox. Workflow code must be deterministic and perform no
  I/O. Put external and database I/O in retry-safe, idempotent
  activities.
- Every protected resource, query, and principal is organization scoped.
  Domain and application packages must not depend on provider SDK types.
- Authorization is a code-owned permission registry (`domain/permissions.go`,
  keys shaped `resource.action`, never built from user data) checked
  through `internal/authorization`: `Check(principal, permission,
  resource)` for one action, `Scope(principal, permission)` for list
  predicates, `HoldsAnywhere` for navigation and the router's coarse gate.
  Principals carry grants (a permission pinned to a scope: organization,
  team, or agent, the last keyed by the agent's slug); `Principal.Permissions` is the flat projection
  for clients and server code never reads it. Never check a role name.
  Every route registers through `httpapi.handle` with an access label,
  and every OpenAPI operation carries the same label in
  `x-withhuman-permission` (`public`, `self`, or a registry key);
  `make api-lint` and the conformance test in `internal/httpapi` fail on
  drift. Edition-owned keys arrive through `runtime.ServerOptions.Permissions`;
  a definition's `BuiltInRoles` names the admin, reviewer, or auditor
  roles that hold it, merged into the built-in role lists at reconcile
  time the way the owner's list already resolves against the registry.
- Personal API keys use `whk_` and follow a membership's live grants.
  Organization keys use `who_` and are `ActorAPIKey` with live policies assigned
  through `permission_policies`, private `roles`, and `role_assignments`.
  Issuing one and delegating access to it is the hosted edition's line
  (the repository split): `api_key.organization.issue` is a
  key core keeps only as vocabulary (`domain.PermissionAPIKeyOrganizationIssue`,
  unregistered here, so nobody holds it and the store's delegation checks
  deny by construction), and `POST /api/v1/api_keys`, `GET
  /api/v1/api_keys/options` and `/api/v1/api_keys/{id}/permission_policies`
  are hosted routes mounted on core's exported seams: `authn.Service`'s
  `OrganizationAPIKeyOptions` and `CreateOrganizationAPIKey`, and
  `runtime.Server.PermissionPolicies`, the store methods that own the
  validation, live authorization, freshness check and audit of every
  policy write. Core resolves, lists, reads and revokes both key kinds
  (`GET /api/v1/api_keys`, `/api_keys/{id}`, `DELETE /api_keys/{id}`,
  `/api_keys/verify`) and keeps the `organization_api_keys_allowed`
  policy knob. The shared policy editor supports `api_key` recipients; no
  separate grant table is used. Policy edits are human-only, scoped and
  revision checked, and require organization-key issuance authority and
  fresh authentication. Holder eligibility is enforced by every
  authorization entry point. New management routes must opt into
  `organizationKeyRoutes` after checking service authorization and
  key-aware provenance. Edition routes deny them by default. Never give
  an organization key a synthetic member or agent. Resource provenance
  references `management_actors`; human audit actors are users,
  organization-key audit actors are keys. Key creation/revocation stays a
  human act, and so does handing out authority: the member and team
  `/permission_policies` writes and `POST /invitations` (plus resend) are
  `browserOnlyRoutes`, so a personal key, which is exempt from the
  re-authentication window, can never grant permissions. Keep
  `x-withhuman-credential-kinds` in OpenAPI synchronized. The guided
  setup's CLI bearer is its own credential class, `whb_`
  (`authn.OnboardingBearerPrefix`), disjoint from both key kinds.
- Application port operations accept one named input struct in addition to
  `context.Context`. Every input struct has a `Validate` method, and production
  adapters validate it before performing I/O or other side effects.
- Request-scoped logging uses `loggermdc` and the context-aware
  `slog` methods. Service entry points call `loggermdc.Configure` before
  composing dependencies so injected loggers and package-level `slog` calls
  share the MDC handler. Temporal workers install both the MDC propagator
  and logging interceptor. Enrich logging context by deriving a new context,
  never by mutating shared fields. Only the standard correlation keys may cross
  Temporal boundaries, and credentials, request payloads, and other sensitive
  values must never be placed in MDC.
- Decision authorization is atomic against current membership, permissions,
  scope, self-approval policy, reauthentication or MFA requirements, and the
  request's terminal state. Requests, decisions, and audit records are
  append-only after reaching a terminal state. The four terminal states
  are `approved`, `denied`, `expired`, and `cancelled`; the last is the
  requesting instance withdrawing its own pending request through
  `DELETE /api/aap/v1/requests/{id}` (`approvals.Service.Cancel`),
  recorded as a decision of source `agent` and channel `aap`, audited as
  `approval.cancelled`, closing any open claim with reason `cancelled`,
  and standing the workflow down through the best-effort `cancel`
  signal (the deadline activity is already a no-op on a terminal
  request). Only the creating instance may cancel; any other terminal
  state answers 409 `already_terminal`, and cancelling twice is
  idempotent. The CLI hooks and the gateway withdraw on interrupt and on
  their local deadline.
- `Agent` is the stable organization-owned machine principal, addressed
  everywhere outside the database by its immutable slug and shown by its
  editable name. `AgentInstance`
  is one concrete enrolled runtime, and every agent credential is bound to an
  instance. Enrollment records are temporary bootstrap state. Do not model
  devices, installations, or service accounts as alternate agent identities.
- Raw session and agent credentials are displayed once. Persist
  only their hashes. Opaque token payloads use URL-safe base64 and can contain
  underscores, so parsers must split only the two structural separators.
- Preserve the last-owner invariant: an organization must never be left
  without an owner.
- `internal/httpapi/openapi.yaml` is the HTTP contract. Update and validate it
  whenever routes or wire formats change.
- Keep the PostgreSQL adapter on one shared pool, but organize its methods by
  repository responsibility. `internal/postgres/store.go` owns connection
  lifecycle and interface assertions only; identity, sessions, agents,
  enrollments, approvals, decisions, and audit behavior live in focused files.

## Current implementation boundaries

- Runtime authentication uses global email and password behind a real
  `Authenticator` adapter (`authn.LocalAuthenticator`). Argon2id hashes
  live in `local_credentials`, separately from canonical users. Login is
  email-first (`docs/design/06-invitations-and-membership.md` amendment): `/auth/login/methods` discovers
  the methods for an email, `/auth/login` verifies the password globally
  and either completes (one organization) or returns a single-use `whl_`
  login ticket plus the organization list, and `/auth/login/select`
  redeems the ticket for one organization. Tickets also retain the session
  client and device context supplied by an assertion-based native login. A wrong password or unknown
  email is 401 `invalid_credentials` (`authn.ErrInvalidCredentials`,
  still an `ErrUnauthenticated`), so the sign-in form can say so instead
  of reporting an expired session. Whether an organization signs in
  only through an external method is the edition's say, asked at every
  login-shaped door through `authn.LoginPolicy`
  (`Service.RegisterLoginPolicy`): `RequiresExternalLogin` (a password or
  social login never mints a session for such an organization; when every
  organization requires it the login answers `sso_required` with those
  organizations and no ticket), `RouteEmail` (home realm discovery before
  any account exists) and `VouchesForEmail` (whether the organization's
  identity provider speaks for an email's domain). Without a policy, the
  open edition's default, every organization signs in directly. The
  hosted edition's policy answers from its enterprise connector and
  verified domains; the `sso`, `sso_organizations` and `sso_required`
  wire vocabulary stays, the phone apps depend on it. The seams that
  edition's login composes are exported: `ResolveAssertionInOrganization`
  (a registered identity to its member, or a verified email linked to
  the existing member, never admission), `AcceptInvitationByAssertion`,
  `AdmitExternalMember` (unattended admission with a default role that is
  re-checked and may never be dangerous; `admission_source` says how the
  membership came to be) and `CreateSessionForUser`. A signed-in person
  changes their password at
  `POST /api/v1/me/password` (current password proved, every other
  session revoked, the acting one kept; a wrong current password is 403
  `password_mismatch`, never 401). Forgotten passwords go through
  `/auth/password/reset`: the request answers 202 for every email and
  mails a `whr_` link (`password_resets`, hashed, single use, one hour)
  only when an active account with a password uses the address;
  `/reset/<token>` previews and completes it, and completion revokes
  every session. The reset mail is `TransactionalPasswordReset` on the
  channel registry (`authn.Service.RegisterNotifications`); without a
  channel the request is accepted and logged. Neither path touches SSO
  accounts, which have no local credential.
- Native apps cannot take a browser cookie from a provider round trip,
  so an edition's browser-based login hands the app a single-use code
  on a registered callback instead, guarded by PKCE, and the app redeems
  it for a session labeled with the client kind and context
  (`sessions.client`, `client_context`). Core keeps what every such
  hand-off shares: `authn.NativeClients` (the allowlist of client kinds
  and callbacks), `ValidateNativeRequest` and `CodeChallenge`; the flows
  themselves (`/auth/sso/begin` with a client, `/auth/sessions/exchange`,
  the `native_sso` runtime-config flag) are the hosted edition's, beside
  its social native flows. Password login needs nothing special: the app
  calls the same endpoints and keeps the cookie itself.
- Social login is edition wiring. Core exports
  `authn.Service.RegisterSocialAuthenticator` (a `SocialAuthenticator` is
  an `Authenticator` with a button `Label`), `SocialProviders` (reported
  live by `/api/runtime-config` as `social_providers`, an empty list in
  the open edition, which registers none), and `LoginWithAssertion`. That
  method resolves a registered provider assertion by its
  `(provider_key, issuer, subject)` triple, else links a provider-verified
  email to the active user with that primary email
  (`IdentityRepository.LinkVerifiedUserIdentity`, audited as
  `membership.identity_linked` in every active organization), else
  refuses (`ErrNoAccount`, `ErrEmailUnverified`); it never creates users
  and then runs the same organization step as password login
  (`finishLogin`). `AssertionLoginInput` also accepts a session client
  and device context. Login tickets retain that context along with
  `auth_method` and `assurance`, so selection preserves the authenticator
  (`social_google`) and the originating app.
  `ActivateOrganizationInput` accepts the founder's identity triple in
  place of a password hash. The provider round trip (state, PKCE, cookie,
  callback routes) is the edition's; core never talks to a provider.
- In the open edition humans join through invitations only: bootstrap
  creates the first owner and every later member accepts an invitation
  (`/invite/<token>`). Invitation and signup tokens travel in request
  bodies, never URL paths, because paths are logged. The invite link is
  the proof of acceptance, so the API returns it to the inviter only when
  no email delivery is configured (`members.Config.ExposeInviteLinks`,
  derived from `SMTPConfigured`); resending re-runs the grant kernel and
  freshness ceremony against the resender. An assertion admitted through
  an invitation (`AcceptInvitationByAssertion`) attaches an existing
  global account by email only when the edition's login policy vouches
  for the address (`VouchesForEmail`; otherwise 409 `ErrUserExists`).
- The tenant lifecycle lives in the exported `tenants` package per
  `docs/design/07-tenant-lifecycle.md`: one activation transaction shared
  by bootstrap and hosted signup, an `organizations.status` state machine
  (`active`, `suspended`, `pending_deletion`) enforced by the
  `status = 'active'` join in session, agent, SSO, and invitation
  queries, and a Temporal grace-period purge workflow. `TenantHooks` is
  the lifecycle observation port (billing and the sales CRM in the hosted
  edition); the open adapter is a no-op. The hosted
  signup flow itself lives in the hosted edition's repository, which
  core must never import or reference in code.
- `runtime` is the exported server composition both editions' mains use.
  Edition differences are its options (hooks, the entitlements adapter,
  extra routes, whether the bootstrap secret is required, the entries the
  edition adds to `/api/runtime-config` through `ServerOptions.RuntimeConfig`,
  which may never shadow a key core reports), never flags inside product
  code. `runtime.Server` exports core's
  session middleware, same-origin guard, principal accessor, and error
  writer so edition routes behave like core's; `WorkerOptions`
  hands hosted workflows a `WorkerContext` (worker, Temporal client,
  tenants service). A seam the SaaS module needs must be exported there
  or in another exported package; reaching into `internal/` from outside
  is impossible by design. The shared frontend reads runtime-config once
  through `use-runtime-config`; the hosted screens (signup, billing, the
  over-quota banner, the add-seats prompt on a refused invitation, the
  audit log, the request timeline) live in the hosted overlay and reach
  core's screens through the registry in `app/extensions.tsx`, gated by
  the hosted edition's own runtime-config entries. `Server.Requests` is
  core's product read of one request for an edition route that hangs its
  own read off it, and `Server.PrincipalAuthAdmittingOrganizationKeys`
  is `PrincipalAuth` for an edition management route that admits
  organization keys through a permission naming that holder.
- Enterprise SSO, directory sync (SCIM) and verified email domains are
  the hosted edition's line (its `enterprise` package, over the Ory
  Polis connector). Core keeps the generic concept of directory-managed
  membership: `memberships.directory_managed` and `admission_source`,
  `membership_teams.source` and `role_assignments.source`,
  `directory_connections` and `directory_object_links`, and
  `application.DirectoryRepository` (exported as `runtime.Server.Directory`
  and `WorkerContext.Directory`) whose `ApplyDirectoryCommands` applies one
  batch of normalized commands in a single serializable transaction of
  idempotent upserts, answering `ErrDirectoryLastOwner`,
  `domain.ErrQuotaExceeded` (the caller passes the seat quota) and
  `ErrDirectoryOrganizationInactive` as typed outcomes; the edition owns
  the inbox, the workflows and the reconciliation sweep that feed it.
  Directory-managed memberships refuse manual permission and status
  edits. Directory group grants (`/api/v1/enterprise/directory/groups*`,
  `authn.Service.SetDirectoryGroupGrant` through the unattended grant
  kernel `AuthorizeUnattendedGrant`) stay in core, since they only read
  the links the edition writes.
- Teams are first-class: manual creation, renaming, archiving, and member assignment behind
  `team.write` and `team.member.write` (`internal/teams`, `/api/v1/teams`; reads need `team.read`). A team
  holds permission policies that its members inherit live. Member and team
  `/permission_policies` endpoints are owned by core. Each policy has a durable
  grouping ID, an immutable private role snapshot, and one binding per resource.
  Mutation checks live authority at both old and new scopes, selected permissions,
  resource existence, revision, reauthentication, and retry identity in one
  serializable transaction. Roles are not exposed or authored through public APIs.
  Manual membership coexists with
  directory group sync by `source` on `membership_teams`. A team's
  directory-managed flag is derived from `directory_object_links`, never
  stored, and locks manual rename and archive. Routing targets use type
  `team` (the `reviewer_group` name is gone); target existence is checked
  at revision create and activate; team archiving is refused while an
  active path revision or a pending request's pinned revision targets the
  team. Suspending or
  deprovisioning a member an active path revision names as a user target
  is likewise refused (409 `membership_in_escalation_path`, the paths in
  the details) unless the caller forces it (`force` in the PATCH body or
  `?force=true` on DELETE), and a forced change is audited with the path
  keys it left dangling; reactivation and directory sync never consult
  the guard. A team owns at most one active escalation policy
  (`team_escalation_policies`, `internal/teamescalation`,
  `/api/v1/teams/{id}/escalation-policy...`; read with `team.read`,
  revisions with `team.escalation.write`, activate and archive with
  `team.escalation.activate`, all checked against the team): the same
  node grammar as a path whose levels name `user` targets that must be
  current members or `broadcast` (everyone); `round_robin` and `on_call`
  are reserved. Archiving a team stamps `archived_at`, preserves its roster and policy
  history, revokes its own and team-scoped grants, and deactivates its
  policy. Archived teams are read-only, excluded from routing and live
  membership through `active_membership_teams`, and listed only with
  `status=archived|all`. Live names can be reused. Directory group removal
  archives unused teams and returns referenced teams to manual control;
  removing a member the active policy names is 409
  `team_member_in_escalation_policy` unless `force`, and the suspension
  guard lists policies beside paths.
- Reviewer routing (design doc 06, redesigned 2026-09-07): there is no
  routing policy and no routing rule list. Escalation paths stay
  standalone, keyed objects (`escalation_paths.path_key` plus a name,
  `/api/v1/escalation-paths/...`, gated by `escalation_path.read`, `escalation_path.write`, and
  `escalation_path.activate`; the `routing.manage` permission is gone). A pipeline block's human outcome
  names the path it hands the request to (`escalation_path` inside each
  block config, a cel block only with `on_match: human`), a pipeline
  revision carries `default_escalation_path` for fall-through and for
  human exits that name no path (the agent section's default wins
  over the organization's), and with neither the request waits for any
  authorized reviewer. Pipeline revision creation requires every named
  path to exist (`approvalpipeline.WithEscalationPathResolver`);
  activation, including rollback, requires each to have an active
  revision and every webhook endpoint the revision posts to to have an
  active revision too, checked by the store inside the activating
  transaction under the path's and the endpoint's advisory locks (409
  `escalation_path_inactive`, `webhook_endpoint_inactive`). A path is
  created explicitly (`POST /escalation-paths` with `path_key`, its
  first revision in the same call, `escalation_path.write`, 409
  `escalation_path_exists` on any key ever used); posting a revision to
  an unknown key is 404. There is no deactivate: the path's end is
  archiving (`DELETE /escalation-paths/{key}`, `escalation_path.activate`,
  the page's Archive button and the public delete alike), which stamps
  every revision with `archived_at`. An archived path stays readable
  (`GET /escalation-paths?status=archived|all`, its revisions and the
  routing snapshots that pin them) but is never active, resolves as
  missing, refuses revisions and activation (409
  `escalation_path_archived`), and keeps its key for good. Archiving is
  refused by the same lock while any active pipeline revision names the
  path, as a block's path or as the revision's default (409
  `escalation_path_in_use`);
  `GET /escalation-paths/{key}/uses` and
  `GET /request-approval-pipelines/{scope}/revisions/{n}/references`
  let the pages explain both refusals before the click. `if_else` path nodes
  carry `conditions` of kind `working_hours`, `urgency`, or `request`
  (the pipeline's CEL condition envelope); document version 2. The
  pipeline executor resolves the route (`application.HumanReviewRoute`)
  and `reviewrouting` pins it as the snapshot (`approval_request_routing`:
  path key, the path's active revision, the pipeline revision and block
  that named it); a path without an active revision at routing time falls
  to the default queue with reason `path_inactive`. The router evaluates every
  `request` condition in the path and its pinned team policies exactly
  once at pin time and stores the results as facts on the snapshot
  (`approval_request_routing.facts`, keyed `<scope>/<condition id>`);
  the executor loads those facts and never re-evaluates the request.
  Decide and claim authorization is reachable-targets-decide: an
  escalated snapshot restricts deciders to the targets on the branches
  the pinned facts select (`routing.PathDocument.ReachableTargets`; both
  branches of a working-hours or urgency test count, timing and level
  order never narrow the set), plus anyone added since (code
  `not_targeted`), with `request.decide.unrouted` (break glass, held by
  owner) bypassing. A path that reaches nobody for a request scopes it
  to break-glass holders regardless of the unrouted policy; the editor
  warns `path_may_reach_nobody` when a document could do that, and the
  routing view carries `targets` so the request page names who can
  decide. Notification of requests the executor does not walk and
  routed-only visibility follow the same set; the editor's own target
  checks stay request-agnostic over the union. The router also pins the
  active policy revision of every reachable team that has one
  (`approval_request_routing_team_policies`). The request
  workflow walks the pinned path with the pure machine in
  `internal/escalation`: levels page their targets when entered (a team
  with a policy starts an independent walker over it, a team without one
  is everyone at once), `if_else`, `defer`,
  `raise_urgency`, and `repeat` behave as design doc 06 describes, a
  claim freezes level and repeat timers until it lapses or is released
  (`claim_released` signal), and every tick writes
  `approval_request_escalation_state` plus `routing.level_entered`,
  `routing.urgency_raised`, `routing.deferred`,
  `routing.repeated`, and `routing.escalation_exhausted` audit events
  under a sequence guard so retries are no-ops. Decision authority is
  unchanged by team policies: every current member of a reachable team may
  decide. Policy scopes are gone from requests and memberships alike. Path revisions
  report `warnings` when a targeted team has members who cannot decide.
  Three named bends in reachable-targets-decide: break glass is the
  dangerous permission `request.decide.unrouted`, exercised only with an
  explicit `outside_routing: true` on the decision (rejection code
  `outside_routing_required` otherwise) and audited as
  `decision.unrouted`; a targeted reviewer can widen a pending routed
  request through `POST /api/v1/requests/{id}/reviewers`
  (`approval_request_reviewers`, append-only, audited as
  `request.reviewer_added`), never narrow it; and
  `organization_policies.unrouted_requests` (`any_reviewer` |
  `break_glass_only`) decides who takes a request no path matched.
  `organization_policies.request_visibility` (`all_readers` |
  `routed_only`) narrows the queue and detail to what a member was
  routed to; anyone holding `request.read.unrouted` (owners and
  auditors) always sees everything. The policy is read and written at
  `/api/v1/organization/policy` (`internal/orgsettings`) and the
  review's `viewer` block tells the client whether the person may
  decide, must break glass, or cannot.
- Reviewer claims ("I'm looking at it") are advisory product state on
  pending requests: same eligibility as deciding (minus self-approval and
  auth-freshness checks), no takeover, idempotent re-claim, and a
  retrigger timeout (`organization_policies.claim_retrigger_seconds`,
  default 900, capped at the request deadline) after which the claim
  lapses via a workflow timer, with a read-time-expiry and next-claim
  sweep as the safety net. Claims never block decisions, never cross AAP
  (the `claim` field exists only on the product representation), and are
  audited as `routing.claimed` / `routing.claim_released` /
  `routing.claim_lapsed`.
- Every live pipeline scope has exactly one active revision at all times.
  Scopes are `organization` and `agent`; the agent scope is keyed by the
  agent's slug and every agent owns exactly one
  (`/api/v1/request-approval-pipelines/agents/{agent_slug}`). The
  organization scope is seeded in the organization's activation
  transaction and an agent's scope in the transaction that inserts the
  agent (agent creation, device-code enrollment, or onboarding
  provisioning, which creates the agent before its pipeline), as an
  empty revision 1 that passes requests through
  (`ensureRequestApprovalPipelineScope`). There is no deactivate and no
  delete: a pipeline that should decide nothing gets an empty revision,
  activation always requires the current active revision in `If-Match`,
  the store refuses a revision for a slug no agent holds (404), and a
  deferred constraint trigger refuses leaving a scope without an active
  row. The one exception is archiving: archiving the agent stamps every
  revision of its scope with `archived_at` and deactivates the active one
  in the same update (`archiveRequestApprovalPipelineScope`, under the
  scope's advisory lock, the lock agent creation takes before it reads
  the scope). An archived scope is listed only with `status=archived|all`
  (no `active_revision`, `archived_at` instead), its revisions stay
  readable but frozen, and creating or activating within it is 409
  `request_approval_pipeline_archived`. Restoring the agent restores the
  scope in the same transaction: the stamps are cleared and the latest
  revision re-activated, audited as
  `request_approval_pipeline.restored`. Slugs are never reused, so no
  other agent can ever take over an archived scope. The organization
  scope is never archived. The pipeline overview never offers to create
  a pipeline; an agent appears there as soon as it is created.
- Request approval block types are a registry
  (`internal/approvalpipeline.BlockRegistry`), constructed once in each
  composition root and shared by compiled state, management, and
  execution. `approvalblock.Definition` is a type-dispatched envelope: the
  wire `config` object decodes into the variant pointer selected by `type`
  (`Always`, `CEL`, `Webhook`, or `Branch` for core's kinds, or the
  `Config` of a registered edition kind), each type owns its envelope
  limits (`approvalblock.LimitsFor`), and the runtime fingerprint is the
  composition of every registered runtime's fingerprint. Core has four
  types: `always`, `cel`, `webhook` (`docs/design/08-webhook-block.md`),
  and `branch` (`docs/design/15-branch-blocks.md`). An edition adds a kind
  on two seams: `approvalblock.RegisterKind` (the grammar: type name,
  envelope limits, and a constructor for its strictly decoded `config`,
  which implements `Validate`, and `RoutedConfig` or `ReasonedConfig`
  when it routes to a path or carries a reason) and the exported
  `blockruntime` package (the execution contract: `Block`, `Input`,
  `Result`, the shared `ErrorCode` vocabulary, and `Runtime` with its
  config version, fingerprint, `Asynchronous` flag, plan `Capability`,
  own error codes, `Compile`, `Problem`, and Markdown `Grammar`),
  handed to `runtime.ServerOptions.ApprovalBlocks` and
  `WorkerOptions.ApprovalBlocks` so both roots compile identically. A
  runtime's `Problem` reports what stands in the way of a definition on
  this deployment: `*blockruntime.Unconfigured` (the deployment lacks the
  kind's dependency; 501 with its code) or `*blockruntime.Refused` (the
  block names something the deployment does not offer; 400) refuses
  revision creation, preview, and activation, and the references list
  carries the code, while `Compile` decides only whether a stored
  revision loads. The registry appends every edition kind's grammar to
  the MCP `pipeline_blocks` reference. The hosted edition's AI judge
  (the hosted edition's `judge` package) is such a
  kind; the open edition refuses an `llm_judge` document as an unknown
  type. Asynchronous kinds never run on the request path: the executor
  suspends in front of the first one and the worker's `ResumePipeline`
  activity finishes the pipeline (or `AbandonPipeline` hands the request
  to a human when the resume cannot run); the resume activity is budgeted
  from the webhook's worst case plus
  `blockruntime.MaximumEditionAsynchronousBudget`, which bounds what an
  edition's asynchronous kinds may take together. The guided setup's
  assistant runs on whatever `application.ToolUseModel` the edition hands
  `runtime.ServerOptions.AssistantModel` (nil disables it and
  `setup_assistant_enabled` reports false); core ships no model adapter.
- Always blocks return their configured `outcome` (`approve`, `deny`, or
  `human`) for every request that reaches them. They use the synchronous
  envelope (50ms default, 1s maximum, one attempt), produce no condition
  trace, and share normal audit, preview, and routing behavior. Their
  `escalation_path` is allowed only for `human`. Later blocks are still
  validated and editable; the editor marks them unreachable, including agent
  sections after an organization Always, and, for an Always inside a
  branch, only that branch's later blocks and its end. These warnings do not
  block activation.
- Branch blocks (`docs/design/15-branch-blocks.md`) hold their own ordered
  blocks (any type, nested without a depth cap, every one counted toward
  the 1,000-block document limit) and run them only for requests their
  `when` matches. The condition is the cel envelope, evaluated
  synchronously under the shared per-segment condition budget, so a skipped
  branch never suspends, delivers, or calls a model. Entering a branch
  commits to it: a block inside that decides ends the pipeline, and a
  branch that runs out of blocks ends it with `human`, the branch's
  `reason`, and its `escalation_path` (else the pipeline default); nothing
  after the branch runs, and an inner branch's end never falls back into
  the outer one. A human exit inside a branch that names no path uses the
  pipeline default, never the branch's, and a branch condition that cannot
  be evaluated is `human` on the pipeline default, never a skip. Storage is
  flat: every block, nested or not, is its own snapshot, the revision's
  `block_ids` lists them in pre-order, and a stored branch config carries
  `block_keys` (its direct blocks, in order) instead of nested blocks, so
  the key trigger, the activation reference guard, the uses queries,
  routing, and the review replay see nested blocks as ordinary rows;
  documents and reads nest children under `config.blocks`. Positions in
  evidence and `resume_position` are pre-order indexes across both
  sections, and a resumed segment re-enters the enclosing branches without
  evaluating their conditions again. A branch's evaluated event carries
  `enter` (evidence only, never a pipeline outcome) or `next` with the
  condition trace; a branch a request runs out of adds a closing evaluated
  event (`human`, `end_of_branch: true`, no attempts, no cost) and the
  completed event's reason code is `end_of_branch`. `approvalblock`
  decodes and encodes a document in one pass (a branch's blocks convert
  from the parsed value, never from re-read bytes), so unbounded nesting
  stays linear, and one pipeline segment commits at most 1,015 audit
  events.
- Webhook blocks post a signed request to an organization webhook
  endpoint referenced by its stable key (`config.endpoint_key`) and map
  the endpoint's closed answer (`next`, `human`, `approve`, `deny` plus
  optional reason and metadata) onto an outcome bounded by the block's
  `allowed_outcomes`; every delivery failure, malformed answer, or
  disallowed outcome is `human` with a safe error code, and the
  `evaluated` event carries `delivery` evidence (endpoint key and
  revision, host, status, latency, the endpoint's reason) and never a
  body or the secret. Endpoints are revisioned exactly like escalation
  paths: `webhook_endpoint_revisions` holds append-only numbered
  revisions of a name and URL per key with at most one active
  (`is_active` the only mutable field, advisory lock
  `webhook_endpoint:<org>:<key>`), and `webhook_endpoint_secrets` holds
  the key's one signing secret, sealed with `internal/secrets` under
  `WITHHUMAN_SECRETS_KEY` and bound to `(organization_id, endpoint_key)`,
  minted with the key's first revision and rotated in place so a URL
  change never makes the receiver reconfigure, plus the key's
  `archived_at`: stamped by the archive, cleared by the next activation,
  so a key saved as a draft and never activated (no active revision, no
  stamp) is never shown as archived. The API has the escalation
  path shape at `/api/v1/webhook_endpoints/{endpoint_key}` (active
  revision, `/revisions`, `/revisions/{n}/activate` with `If-Match`,
  `/revisions/{n}/test`, `/uses`, `DELETE /active` to archive,
  `/rotate_secret`): every read, history included, is `webhook.read` and
  every write is `webhook.write`, so a pipeline author's role must hold
  `webhook.read` to list keys and read the active revision. Deliveries
  resolve the key's active revision at evaluation time, so an archived key
  is `endpoint_unavailable` (human). Pipeline revision creation and
  preview require every referenced key to exist
  (`webhook_endpoint_not_found`), activation requires each to be active,
  and archiving a key is refused with 409 `webhook_endpoint_in_use` while
  any active pipeline revision posts to it; the list carries each key's
  uses so the page marks them and explains a refusal before the click.
  Without the
  secrets key the type decodes and lists but cannot compile
  (`webhook_unconfigured`), and `/api/runtime-config` reports
  `webhook_enabled`. Every outbound delivery, including the endpoint test
  and preview, goes through `internal/webhookdelivery`: public HTTPS only,
  every resolved address checked against loopback, private, link-local,
  and reserved ranges and pinned before dialing, no redirects, 64 KiB
  responses; `WITHHUMAN_WEBHOOK_ALLOW_INSECURE` and
  `WITHHUMAN_WEBHOOK_ALLOW_PRIVATE_NETWORKS` exist for development only.
  CEL and branch conditions read earlier blocks' metadata at
  `/previous_metadata/<scope>/<block_key>/...`; preview namespaces
  metadata by the previewed scope and accepts seeded `previous_metadata`.
- The review queue (`GET /api/v1/requests` and the `queue` SSE stream) is
  for humans: it lists only requests that reached human review (pending,
  decided by a human, or expired while waiting). Requests the pipeline
  decided automatically never appear there; the queue detail route
  fetches one by id when an edition's audit log links to it. The detail
  view's standing comes from `GET /api/v1/requests/{id}/review`
  (`approvals.Service.Review`): the routing snapshot, the reviewers added
  since, and the viewer block, gated by `request.read` alone. The "why am
  I being asked" story (the pipeline's conclusion and terminal block with
  its configured reason, judge assessments, escalation steps, and the
  claim history) is told from the request's own audit events, which core
  no longer keeps: the hosted edition serves it as
  `GET /api/v1/requests/{id}/timeline` and fills the request page's
  `requestDetail` slots with it, while the open page shows the request,
  its routing, its reviewers, and its decision. The
  `request_approval_block.evaluated` event therefore carries the block's
  `name` and configured `reason`, and the completed event its
  `terminal_block_name`, for whichever sink keeps them. The detail shows
  every timestamp as an exact local time with the relative age beside
  it, titles and renders the request through its tool presentation
  (below), and requires a note to deny.
- Tool presentations (`docs/design/13-tool-presentations.md`) say how a
  tool's arguments are shown: `toolpresentation` is the exported leaf
  package (the closed `Kind` vocabulary, `Spec` validation, `Entry`
  bindings, `Resolve`, and the shipped catalog in `builtin.go`, which
  must cover every runtime's shell and edit tools), `tool_presentations`
  holds the organization's own entries (`internal/toolpresentations`,
  `/api/v1/tool_presentations`, read with `organization.read`, written
  with `organization.settings.write`, audited as `tool_presentation.*`
  with the binding only), and every product read of a request attaches
  the resolved `presentation` (organization entries first, then the
  catalog, most specific binding wins, a server binding outranking a
  runtime one) to the queue representation, never to the AAP one. A
  presentation is a reading aid over the arguments: it may fold a field
  into the details but never hide one, every unnamed argument still
  renders by shape, and the raw JSON stays one click away. The frontend
  planner is `frontend/src/app/tool-presentation.ts`; rendered HTML goes
  through `ds/components/html-preview.tsx`, a fully sandboxed frame that
  runs no script and loads nothing.
- The chokepoint MCP gateway is the hosted edition's
  (the hosted edition's `gateway` packages and `saas-gateway`): core has
  no gateway process, no
  gateway routes, no `WITHHUMAN_GATEWAY_*` settings and no
  `gateway_enabled` runtime-config entry, and the single binary embeds
  none. What core keeps for it is generic: the exported `sealbox` (an
  AES-GCM sealer bound to a storage row through associated data, used
  for every reversible credential at rest), the exported `authz` checks
  (`Check`, `Scope`, `HoldsAnywhere`, the typed `Denied`) so an edition's
  services authorize exactly as core's do, the `gateway` permission
  holder and `ActorGateway` actor kind as vocabulary an edition's
  machine principal uses, the AAP `server` field on every request, and
  the tool catalogue's external source seam (below). The gateway's own
  tables stay dead in core's chain until it is squashed.
- The hosted management MCP server is
  `POST /api/mcp/v1` on the product server (`internal/httpapi/mcp.go`,
  tools in `mcp_tools.go`, grammar in `mcp_reference/`): a stateless
  streamable HTTP endpoint that admits `whk_` and `who_` bearers only
  (never a session cookie) and whose tools re-enter `/api/v1` in process
  with the caller's bearer, so permissions, organization-key route
  gating, validation, `If-Match`, audit provenance and key usage
  recording are the product API's own; the loopback is marked on the
  request context and logged but not metered. Save tools create a
  revision and activate it against the current `ETag` unless
  `activate: false`, refusals relay the API's error envelope as tool
  error text, and `read_reference` serves the block, condition and path
  document grammar. It is distinct from the chokepoint gateway's
  `/gateway/mcp`, which sits in the data path of an agent's tool calls.
  Keys are obtained through OAuth rather than copied: `internal/mcpoauth`
  is an OAuth 2.1 authorization server (RFC 8414 and 9728 discovery at
  `/.well-known/...`, RFC 7591 registration at `/auth/oauth/register`
  for public clients with PKCE S256, the token endpoint at
  `/auth/oauth/token`), the MCP refusal carries the resource-metadata
  challenge, and the consent page `/oauth/authorize` (a product route
  outside the shell, browser-only API at `/api/v1/oauth_authorizations`)
  mints a personal API key named after the client through
  `authn.CreateAPIKey`, which is the access token; there is no refresh
  token. The raw key reaches the client sealed under the single-use code
  (`oauth_authorization_codes`), consumed in the statement that reads it.
  `api_keys.oauth_client_id` marks such keys ("Connected agent" on the
  keys page; revoking disconnects the agent). The OAuth routes answer
  RFC 6749 error shapes, the contract's one exception to the error
  envelope, because OAuth clients parse nothing else.
- The agent-side `withhuman` CLI at `cmd/withhuman-cli` embeds the pinned
  `github.com/agentapprovalprotocol/agentapprovalprotocol/adapters` library.
  Its public commands are `onboard`, `agent discover`, `agent install`,
  `agent eject`, `upgrade`, and `version`; internal `hook <runtime>` delegates
  to AAP.
  Runtime installation, hook handling and plugin assets belong only to AAP.
  `internal/cli/discovery` retains the tool discovery used by onboarding.
  Enrollment records live under `<withhuman-config>/credentials` (0600);
  library state lives separately under `<withhuman-config>/aap`. Generated
  hooks pin that directory and the installing executable's absolute path.
  Every intercepted call reaches the provider; its pipeline decides whether
  human review is required. The CLI must not import server implementation
  packages, storage, Gin, or Temporal; keep the dependency-boundary test.
  `upgrade` resolves the release bucket's latest version, verifies its immutable
  download and executable version, and atomically replaces the resolved CLI
  path. It changes neither enrollment nor adapter files. The internal
  `upgrade-install` sudo helper only verifies and replaces its own executable;
  it runs before configuration loading and performs no network requests.
  Interactive agent and onboarding commands check for updates at most daily
  per download source, with a one-second timeout and stderr-only notices.
  Hooks, JSON, help, version and noninteractive commands never check.
  `WITHHUMAN_CLI_BASE` selects the download mirror; setting
  `WITHHUMAN_NO_UPDATE_CHECK=1` disables notices, not explicit upgrades.
  The shell installer forwards explicit CLI commands after `sh -s --`
  (including `onboard`), and reconnects terminal input when available.
  No arguments means install only. `WITHHUMAN_CLI_INSTALL_DIR` overrides
  the target directory. Release installer and command producers together.
  The AAP repository is currently private. Clean builds use GOPRIVATE and
  an authenticated Git credential; CI and container builds use the
  `AAP_MODULE_TOKEN` repository secret with read access to that repository.
- AAP request representations are separate from product views. Preserve the
  exact submitted timeout. Approval idempotency is instance scoped, and
  an instance reads and cancels only its own requests. The database records
  approved decision expiry once at insertion as decided_at plus five minutes.
  Reads never extend it. Duration text in approval_requests.timeout is an
  exception to numeric duration columns: its exact wire spelling is immutable.
- Guided onboarding (`docs/design/09-onboarding.md`) takes a new
  organization from first sign-in to a decided request. One
  `onboarding_sessions` row is the shared state between the browser and
  the CLI: the page mints a single-use setup code shown inside the
  command to paste, the CLI exchanges it for a `who_` bearer, a
  credential class disjoint from sessions, `whc_`, and `whp_`,
  accepted only by `/api/onboarding/v1/*` and resolving live to an open
  session with an active organization and membership. Both sides
  advance a closed status table with version-bumping, status-checked
  writes (`internal/onboarding`); a write outside the table is 409
  `onboarding_session_state`. The person's own steps that gate nobody
  but them are stamps, not statuses: the Notifications settings inside
  the flow come right after the agent choice and stamp
  `channels_set_at`, which provisioning requires; the rules step stamps
  `rules_set_at` once the hooks are in; the CLI is done at `installed`;
  the person then records the request they decided in the one test
  (`tested`, checked against an enrolled agent) and finishes, which
  requires `tested`. Provisioning (the escalation path that notifies
  the person, one starting pipeline revision per runtime's agent that
  sends the suggested tools to a human and approves the rest) runs
  under the person's own session when they reach
  the rules step; enrollment is the one CLI-initiated write that mints,
  under a principal rebuilt from the session's membership at that
  moment, because agent credentials are handed to the CLI once and
  never stored. Tool suggestions are deterministic
  (`internal/toolsuggest`) and carry a reason code; they seed the
  starting pipeline. The rules step offers three ways: a prompt for
  the person's own coding agent (which writes through the hosted MCP
  server while the page polls the agents' pipelines), a gated-tools
  table per agent, and the setup assistant
  (`internal/onboarding/assistant.go`, `POST
  /api/v1/onboarding/session/assistant`): the person describes in their
  own words which calls should wait for them and the edition's assistant
  model (`runtime.ServerOptions.AssistantModel`, an
  `application.ToolUseModel`; the hosted edition hands over its judge
  catalog's default model, and runtime-config reports
  `setup_assistant_enabled`) writes the agents' pipelines
  through the hosted MCP server's own tool catalog, dispatched in
  process under the person's resolved principal (`loopbackPrincipalKey`
  in `internal/httpapi/mcp.go`), never a bearer. The service confines it
  to the pipeline tools on the session's own agents, injects the setup's
  path as the default, and owns `set_test_prompt`, which previews the
  proposed sample against the active rules and refuses one that would
  not reach a person before it becomes the test command. The transcript
  lives on the session (`assistant`, one action per tool call as it
  happens, so the events stream shows progress); the model's context is
  a column of its own. The grammar both the assistant's prompt and
  `read_reference` serve lives in `internal/mcpreference`.
  `organizations.onboarding_completed_at` drives the `/welcome` redirect
  through `/me`'s `onboarding.required`; finishing or skipping sets it.
  No adapter policy exists: every call an adapter or the gateway
  intercepts becomes a request, and the pipeline decides. The CLI checks
  a saved instance credential against `GET /api/v1/agent_instances/self`
  (machine bearer, `self`), which answers with the agent slug and
  instance id the credential resolves to.
- An agent is identified by its slug (`agents.slug`, pattern
  `^[a-z0-9][a-z0-9._-]{0,62}$`, chosen once at creation or first
  connect, immutable) and shown by its editable name. The slug is what
  URLs, role scopes, permission policies, pipeline scopes, audit data
  (`agent_slug`), the `?agent_slug=` request filter, and the CEL
  variables `agent.slug` and `agent.name` carry; the row's UUID primary
  key exists only for foreign keys, role-assignment principal ids, and
  audit subject ids and never reaches the API, URLs, CLI, or UI. Slugs
  are unique per organization forever: an archived agent keeps its slug
  and it is never reused (`POST /api/v1/agents` with `{slug, name}` is
  409 `agent_slug_taken` for a live or archived holder). Names are
  unique among live agents only (the partial index
  `agents_live_name_idx`, 409 `agent_name_taken`), so archiving frees
  the name. `GetAgentBySlug` is the lookup and sees archived rows;
  the enrollment selector and onboarding skip them. Renaming is
  `PATCH /api/v1/agents/{slug}` with `name` (the page's inline Rename),
  audited as `agent.renamed`; the same PATCH takes `status`. The agent
  inventory (`GET /api/v1/agents`, `GET /api/v1/agents/{slug}`, the
  `/agents` pages, `/agents/$agentSlug` in the frontend) is readable by
  every member: reviewers already see agent names on every request.
  Managing agents (new instances, provisioner tokens, revocation,
  disabling, renaming) stays behind `agent.write`,
  `agent.credential.issue`, and `agent.credential.revoke`, checked
  against the agent (resource kind `agent`, id the slug); a scoped
  `admin` at `agent:<slug>` manages only that agent, and the agents,
  pipelines, and teams lists narrow to `authorization.Scope`. Every
  agent identity holds the built-in `agent` role as an assignment (there
  is no `scopes` column in the model any more). Agents carry no owning
  team: which team manages an agent is an ordinary role assignment
  (`admin` at `agent:<slug>`) made on the Teams page, never a property
  of the agent. The UI's "Active" status is derived from credentials,
  never from a heartbeat: agents call in only when they have work, so a
  live instance is an active instance holding an unrevoked, unexpired
  credential, and `last_seen_at` is the newest authenticated call.
  Disabling an agent or instance (`status: disabled`) keeps every
  credential and refuses them all at resolution until it is enabled
  again; revoking a credential is the irreversible alternative. Both
  switches are audited as `agent.disabled` / `agent.enabled` and
  `agent_instance.disabled` / `agent_instance.enabled`. Archiving an
  agent (`DELETE /api/v1/agents/{slug}`, `agent.write`, the page's Archive
  action with the name typed to confirm) is the third status,
  `archived` with `agents.archived_at`: every live credential and
  provisioner token is revoked, instances and history stay readable for
  the record, no instance or provisioner can be minted under it, the
  status switch and instance switches answer 409 `agent_archived`, its
  approval pipeline is archived in the same transaction (audited as
  `agent.archived` with `pipeline_archived` and
  `request_approval_pipeline.archived`), and its name is freed while its
  slug stays taken. Onboarding whose runtime slug (`codex`) belongs to
  an archived agent fails with `agent_archived` and tells the person to
  restore the agent in the app or choose another slug. Archiving is
  reversible: `POST /api/v1/agents/{slug}/restore` (the Restore action
  beside the archived badge) puts the agent back to `active` and
  restores its pipeline with the latest revision re-activated, audited
  as `agent.restored` and `request_approval_pipeline.restored`;
  credentials and provisioner tokens revoked at archive stay revoked,
  instances stay and can be re-enrolled, and restoring a live agent is
  409 `agent_not_archived`. The inventory lists
  live agents by default (`GET /api/v1/agents?status=live|archived|all`).
  Request counts
  on the inventory cover `application.RecentRequestWindow` (seven days);
  `GET /api/v1/requests?agent_slug=` is one agent's full history, automatic
  decisions included, unlike the human-only queue.
- A Slack workspace connects to an organization through an
  organization-level OAuth install (`internal/slackconnect`, `/api/v1/
  enterprise/slack*`, gated by `connection.write`, read by `connection.read`). It exists only when
  `WITHHUMAN_SLACK_APP_ID`, `WITHHUMAN_SLACK_SIGNING_SECRET`,
  `WITHHUMAN_SLACK_CLIENT_ID`, `WITHHUMAN_SLACK_CLIENT_SECRET`, and
  `WITHHUMAN_SECRETS_KEY` are all set; otherwise the surface answers
  `slack_not_configured`. The bot token is the first reversible credential
  stored at rest: it is sealed with AES-GCM under the secrets key
  (`internal/secrets`) and opened only by the adapter that speaks to
  Slack, never by the product read model. The callback from slack.com is
  cross-site, so the strict session cookie does not accompany it; the
  single-use install state, minted for one organization by one admin,
  is what binds the workspace to its tenant. One workspace connects to at most one organization. `deployment/slack/manifest.yaml` describes the
  app a self-hoster creates. Members' Slack identities are contact methods
  of kind `slack`, an organization-scoped kind (`contact_methods.
  organization_id` is set exactly for the chat kinds, `slack` and
  `discord`, under `contact_methods_org_link_idx`; Slack identity uniqueness
  also includes the installation generation): never typed in, but matched by
  looking a member's confirmed addresses up in the workspace, for
  everyone when it connects and on `POST /enterprise/slack/sync`, and
  for the caller alone on `POST /me/slack/link` (the Link Slack button on
  the Notifications page, so nobody waits for an admin), and verified at
  link time because Slack vouched for the match. Approval prompts reach linked
  members as direct messages through `notifications.SlackChannel`,
  registered in every composition root when Slack is configured; the
  message carries the tool, agent, deadline, and an "Open in withHuman"
  link, never the arguments, and deciding still happens in the product.
  Transactional mail stays email-only. Escalation paths reach people
  through their personal notification rules. Deciding from Slack is not built.
- Slack URL buttons send signed callbacks to `/auth/slack/interactions`.
  The endpoint verifies the original form body, timestamp and app ID, then
  acknowledges `block_actions` with an empty 200. It performs no I/O and records
  no decision. Existing Slack apps must enable Interactivity and configure this
  Request URL after deploying the endpoint.
- Slack removals arrive at the signed `/auth/slack/events` endpoint and persist
  normalized metadata in `slack_event_inbox` before starting Temporal processing.
  A minute workflow repairs missed dispatches and retries pending events.
  Connections retain disconnected metadata without credentials;
  only connected rows reserve a workspace. Every install gets a new generation,
  and member links deliver only after matching that generation. Token checks
  distinguish definitive revocation from inconclusive errors. OAuth save failures
  never revoke the returned token, which can belong to a live installation.
  Saved rules survive disconnection; Slack-only ladders temporarily use account
  email, with one best-effort fallback attempt per request and member.
- Discord mirrors Slack with one structural difference: a Discord
  application has a single bot token issued in the Developer Portal, so
  `WITHHUMAN_DISCORD_CLIENT_ID`, `WITHHUMAN_DISCORD_CLIENT_SECRET`, and
  `WITHHUMAN_DISCORD_BOT_TOKEN` are deployment configuration (all or
  nothing, no secrets key) and an organization's install
  (`internal/discordconnect`, `/api/v1/enterprise/discord*`, gated by
  `connection.write`) records only which guild the bot joined
  (`discord_connections`, one guild per organization). Discord has no
  lookup by email, so there is no member sync: each person links their own
  account from the Notifications page (`POST /api/v1/me/discord/link`,
  `DELETE /api/v1/me/discord`) through Discord's OAuth2 `identify` scope,
  and the link is recorded as a `discord` contact method only if the bot
  confirms the account is a member of the connected guild, because a bot
  can only message people it shares a guild with. Both callbacks
  (`/auth/discord/callback`, `/auth/discord/link/callback`) are cross-site
  and bound by single-use states in `discord_oauth_states`, whose
  `purpose` is part of redemption so a link state can never complete an
  install. Approval prompts reach linked members as direct messages through
  `notifications.DiscordChannel` (an embed plus an "Open in withHuman" link
  button, never the arguments); a member whose privacy settings block DMs
  is a logged delivery failure. Disconnecting removes every member's link
  with the guild and makes the bot leave. `deployment/discord/README.md`
  describes the application a self-hoster creates. The default interrupt
  ladder prefers Slack over Discord (`domain.ChatContactKinds`).
- Contact kinds beyond email, Slack and Discord are an edition's
  (the repository split): `domain.RegisterContactKinds`
  takes a kind's label and its policy (`Challengeable`: typed in by its
  owner and proved by a code; `Consent`: express agreement on add;
  `Immediate`: joins a person's default ladders at delay zero;
  `Multiple`: every method of the kind on one rung delivers, else the
  last one on the rung stands), the store validates a kind in code
  (no `CHECK` on `contact_methods.kind`),
  the planner puts a step's targets of such kinds on
  `NotificationRecipient.Targets` keyed by kind (`RecipientTarget`: the
  method and its address), and the contacts service takes one
  `application.ContactKindAdapter` per kind through
  `runtime.Server.Contacts.RegisterKind` (normalisation of what the
  person typed, how the code reaches them and the fields the challenge
  response carries beside the method, a code-send budget). `Add`
  requires consent when the kind says so, refuses a kind without an
  adapter as invalid, and the challenge response merges the adapter's
  extras at top level. `Server.Contacts` also exposes `AddVouched` (a
  person-wide, already verified method something other than a code
  vouched for), `RemoveByValue` and `VerifyByValue` (a challenge
  answered from the address side, by a message arriving from the number
  with the code) for an edition's own routes and webhooks. The hosted
  edition registers `sms` and `whatsapp` (Twilio) and `push` (APNs and
  Firebase) this way, with their tables, routes (`/api/v1/me/devices*`,
  `/api/v1/phone_apps/qr`, `/auth/twilio/*`), settings and
  runtime-config entries (`sms_enabled`, `whatsapp_enabled`,
  `push_enabled`, `push_platforms`); core has none of them, its
  `contact_methods` kind vocabulary on the wire is open, and the dead
  `phone_messages` and `push_devices` tables stay in its chain until it
  is squashed.
- Notifications dispatch through the `notifications.Registry` (design doc
  05's channel registry), not a single notifier. The logging channel is
  always registered; SMTP joins it when `WITHHUMAN_SMTP_HOST` and
  `WITHHUMAN_SMTP_FROM` are set, all or nothing like Polis, and a
  configured but invalid transport fails startup rather than dropping mail
  silently. Editions add channels through
  `runtime.ServerOptions`/`WorkerOptions.NotificationChannels`, which
  is how the hosted phone and push channels join. Who gets
  told, when, and how comes from each person's
  notification rules (`notification_rules`, two ladders per person keyed
  by the request's pinned urgency; defaults computed in
  `internal/orchestration/plan.go` and mirrored for display in
  `internal/contacts`). `PlanNotifications` resolves recipients and
  ladders into steps once; the workflow arms one timer per step, and a
  decision or any claim cancels the rest. A rung can only name a method
  the person holds; the store enforces it. Approval mail carries the tool, agent, deadline, and a link and
  never the request arguments, which routinely hold customer data; the
  invite link is a credential and appears only in the invitation email,
  never in logs. Recipients come from the pinned routing snapshot's reachable
  targets when a path is pinned, and otherwise from every active member
  holding `request.decide`, so the default queue actually reaches people
  before anyone names reviewers. Delivery never blocks or fails the action that
  produced it, and a prompt is delivered once per step: the registry
  logs a channel failure and answers success, and the delivery activity
  runs with a single attempt, because a retry re-sends through every
  channel that already succeeded (a refused APNs key once emailed and
  texted the whole organization on every backoff).
- Plan gating goes through the `application.Entitlements` port. The open
  distribution wires `entitlements.Unlimited`, so no capability is denied and
  no limit is enforced at runtime. Allocation quotas (seats, agents) are
  resolved by the service and carried into the repository as an
  `application.AllocationQuota`; the repository enforces them inside the
  allocating transaction under a per-organization session lock taken
  before the transaction begins (`internal/postgres/allocation.go`), so
  concurrent allocations cannot both slip under the limit. Seats are
  consumed by active memberships and pending invitations and enforced at
  invitation creation, revival, acceptance, and directory provisioning; a
  refusal is `domain.QuotaExceeded` (409 `quota_exceeded`). The pending
  approval cap stays a service-level rate-style check. Limit checks fail
  closed; usage metering (approval creations, gateway tool calls) is
  lossy-tolerant and never fails the recorded action.

## Development workflow

- Use `make test` for Go tests, vetting, and product frontend typechecking.
  Use `make verify` to add OpenAPI and deployment configuration validation.
- Integration tests boot embedded PostgreSQL (and, for the single-binary
  end-to-end test, embedded Temporal). The first run on a machine downloads
  the PostgreSQL archive into a per-user cache; `go test -short ./...` skips
  these tests.
- `webapp/assets` is ignored generated output. Never commit it.
  Supported clean-checkout test and build paths must generate the frontend
  before compiling Go because the `webapp` package embeds the resulting
  files; `cmd/withhuman` hands `webapp.Assets()` to
  `runtime.ServerOptions.Frontend`, which is required, and `httpapi`
  serves whatever bundle it is given (none in tests).
- After product frontend changes, run `make frontend` to rebuild the assets
  embedded by `webapp`. Test the result through the full stack, not
  only the Vite development server.
- The frontend's edition seams live in `app/extensions.tsx`
  (`registerExtensions`: sidebar entries by group and settings pages,
  both feeding the palette and gated by a permission and the edition's
  own runtime-config entries; permission labels; the `auditLink` slot the
  agents, members and request screens offer beside a record; the signup
  door on the sign-in screen; banners above every workspace page; the
  hint under a refused invitation; the request page's `requestDetail`
  slots for the assessments, the cause row and the history an audit log
  can tell, filled from core's exported `requestMoments` and
  `HistoryDisclosure`; and `blockKinds`, the block kinds an edition adds
  to the pipeline editor, each a `BlockKind` built with
  `defineBlockKind`: the add-menu entry gated on the edition's
  runtime-config, draft and wire conversions, validation, JSON-import
  acceptance, the rail summary and exits, the editor and summary cards,
  replay evidence, problem and error code labels, and a styleguide
  section; a stored block of a type this bundle has not registered
  renders read-only as `ds/components/unknown-block.tsx` and blocks
  activation until it is removed; `agentTabs`, the tabs an edition adds
  to the single-agent page, each with its `?tab=` id, label, permission,
  a loader the agent route runs before paint and a component; and the
  tool catalogue's `toolSources` (an edition source's name, label and
  whether it is external, for the catalogue's filter, badges and refresh
  card) and `toolReference` slot (what the tool hover card asks an
  edition about a tool's server: its display name and the matching gated
  pattern); and `contactKinds`, the contact kinds an edition registers
  beside email, Slack and Discord, each built with `defineContactKind`:
  the kind, its label, icon and add-menu description, `available` over
  the edition's runtime-config with the copy shown when it is not, the
  whole add flow as a component (`ContactAddProps`), how a method of
  the kind lists (`ContactMethodRow`: title, subtitle, badge, removal,
  and a confirmation panel under an unverified row), and the ladder
  option label; core ships `typedMethodRow`, `CodeConfirmPanel` and
  `ContactMethodForm` for a kind to build on, and a stored method of a
  kind this bundle has not registered renders as a generic row); and
  `organizationPolicyRows`, the rows an edition adds to the organization
  policy page's Programmatic access section (each a component taking
  the policy draft, its update function and `canEdit`; the hosted
  "Allow organization API keys" switch renders this way while the knob
  itself stays in core's policy). Screens an edition reuses on its own
  routes take their link targets as props rather than naming a route:
  `APIKeysScreen` (`links.create`, `links.key`) and
  `PermissionPoliciesScreen` (`subject`: collection, holder, description
  and back link; core exports `MEMBER_POLICIES` and `TEAM_POLICIES`, the
  hosted organization-key subject lives in the overlay)) and
  `app/docs.ts` (`registerDocsPages`; `docsForPath` prefers the screen
  entry with the most matching search keys, so an edition's
  `?kind=` pages win over core's general add-channel entry). Core never links to a route only an edition
  ships: a hosted route reaches core's typed `Link` through
  `extensionLink`, the one cast, and a hosted-only affordance renders
  from a registry entry or not at all. The hosted overlay
  (the hosted edition's `frontend`) composes its route tree from core's
  `routes/` and its own with TanStack's virtual file routes, so core's
  route files are read there and never rewritten; `main.tsx` in each
  edition shares `app/router-setup.ts`.
- Stop the Vite development server before `npm ci` or any Make target that
  runs `frontend`: reinstalling dependencies removes Vite's optimized module
  cache and breaks later route imports in a running server. Restart Vite after
  the build, then reload open development tabs to use the regenerated modules.
- Product screens load their data in the route `loader`, never in a mount
  effect: the router keeps the previous page on screen until the data is
  ready (preloading on link intent), so a screen never paints an empty
  state or a spinner that the real content then replaces. Data that
  outlives one screen (the review queue, fed by the events stream) lives
  in a module-scope store (`app/queue-store.ts`) that the loader awaits
  and components subscribe to with `useSyncExternalStore`, so a revisit
  renders the last snapshot instantly. The session itself is the same
  kind of store (`app/session-store.ts`), awaited by the `/_app` loader
  so the shell and the page arrive together. Loaders report a failed
  fetch in their data instead of throwing, so an expired session
  reaches the sign-in gate rather than the router's error boundary; the
  gate invalidates the router after sign-in so those loaders run again. Gate
  empty states on a resolved flag, and fix row and section heights so
  late data changes pixels, not geometry. The one exception is a
  reference card: every mention of a person, team, agent, tool,
  request, pipeline, escalation path, or webhook endpoint explains
  itself in a hover card (`ds/components/reference.tsx` wraps the mention;
  `app/reference-cards.tsx` provides the card and gates it on the
  reader's permission), and a card loads its facts when it opens,
  through the short-lived shared reads in `app/reference-store.ts`.
  Wrap a new mention in `Referenced` (or pass `id` to an `Avatar`)
  rather than writing a one-off tooltip.
- Start the product locally with the single binary
  (`WITHHUMAN_BOOTSTRAP_SECRET=change-me-before-production go run ./cmd/withhuman`,
  after `make frontend`). Use `./scripts/smoke.sh` against it for an
  agent-friendly real flow covering bootstrap and login, browser-assisted,
  directly registered, and provisioner-minted agent instances, approval
  creation, idempotency conflicts, decisions, long polling, and expiry.
- Clean-schema verification means a fresh data directory: pass
  `--data-dir` (or `WITHHUMAN_DATA_DIR`) pointing at a disposable path
  rather than deleting `~/.withhuman`.

- The single-agent page has Overview, Provisioner tokens, Instances, Approval
  requests, and Tool Catalogue tabs, plus the tabs an edition registers
  (`extensions.agentTabs`; the hosted Gateway Tools tab). Overview uses a single column of cards linking to the pipeline
  and each table. The Provisioner tokens
  tab contains the provisioner table. Tokens page with `provisioner_limit`,
  `provisioner_offset`, `provisioner_q`, and `provisioner_status`. Search matches
  identifiers, never secrets. Revoked status takes precedence over expiry.
  Tabs, search, status filters, and page indexes live in route search params;
  loaders fetch 25 rows per page. `GET /api/v1/agents/{slug}` pages instances
  with `instance_limit`, `instance_offset`, `instance_q`, and
  `instance_status`; aggregate counts and liveness cover every instance,
  never only the current page. Request history uses `/api/v1/requests`
  with `agent_slug`, `offset`, `q`, and `status`; pagination follows routing
  visibility checks, so hidden rows cannot prevent later visible results.

- New instance opens `/agents/{slug}/instances/new`. The page offers CLI
  installation with browser authorization or manual agent API-key generation.
  CLI commands use runtime-config's public origin and download mirror; the
  API-key result shows the instance credential once with its AAP provider URL.
  Both paths require scoped `agent.read` and `agent.credential.issue` and
  an active agent. The page never creates an instance merely by showing a
  CLI command.

- Tool definitions live in the core-owned catalogue (`internal/toolcatalog`),
  shared by both compositions. Instance-only reports replace successful source
  snapshots within one runtime and discovery context; incomplete sources keep
  their previous definitions. An instance reports only its runtime's own
  sources (`builtin`, `mcp`); every other source name belongs to the
  edition's external tool source, the `application.ToolSource` an edition
  registers through `runtime.Server.RegisterToolSource` (the hosted
  gateway reports `gateway`). A snapshot of an external owner is keyed by
  an opaque `source_ref` and stamped with the owner's `source_version`;
  on every read the source names the owners to refresh (a shared
  five-minute cache keyed by that version) and says which agents see
  each owner, with which tools, and whether it can serve now
  (`ReadToolCatalogInput.External`), so grants and availability are the
  edition's live answer and never a stored decision. Catalogue reads need
  `agent.read` and never return credentials. Local installation and
  onboarding report automatically; later refreshes use
  `withhuman agent refresh-tools <runtime> --agent <slug>` from the project.
  Request history contributes only permission-visible, nonpersistent editor
  suggestions. Catalogue schemas are advisory and must never affect pipeline
  execution or rewrite saved conditions. This product API is outside AAP.
