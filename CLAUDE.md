# withHuman

This repository (`github.com/withHumanAI/withHuman`) is the self-contained
implementation of withHuman: the single binary, its protocol adapters, the
web frontend and the command-line tool. The hosted edition at withhuman.ai
builds on it from a separate, private repository that imports this module
and never the reverse. Run every command in this file from the repository
root.

`AGENTS.md` must remain a relative symlink to this file.

## Coding conventions

All new and changed code must follow
[`CODING_CONVENTIONS.md`](CODING_CONVENTIONS.md). Any exception must be
recorded in this file with both the reason and the replacement rule.

## Commits and pull requests

Never credit AI agents in commit messages or pull requests. Leave out
co-author trailers, "generated with" lines and agent signatures.
Contributors must be able to stand behind every change they submit as their
own.

## Repository boundary

- This repository must build, test, document, and run on its own. Nothing
  here may depend on the hosted edition; the hosted edition composes this
  module through the exported seams (`runtime`, the registries, the ports)
  and is free to change without a change here. Design documents are kept
  in the hosted edition's repository, not here.
- The Go module path is `github.com/withHumanAI/withHuman`.
- Do not copy privately licensed marketing fonts or assets into core. Product
  assets must be independently distributable.
- Do not add compatibility shims, deprecation windows, or legacy
  fallbacks. Change the design directly and delete the obsolete path.
- Never use em dashes in user-facing product copy.
- Status is always a labeled `Badge`, never a bare status dot. The design
  system deliberately has no status-dot component: each tone has a status
  icon (hollow circle, progress ring, check, cross, exclamation) that
  draws the state, and that toned icon may stand alone only as internal
  anatomy of a component that also carries a label (toast, timeline rail).
  The one dot that is not a status is `IconButton`'s `attention` mark
  (the to-do bell, and the collapsed sidebar's way back while the bell
  has items or the Finish setup card is showing): it says something
  behind the control wants the person,
  and the control's label says the same in words.
  The review queue shows no connection state: once a snapshot has loaded,
  a failed refresh or a dropped stream leaves it on screen without a
  banner or indicator while the stream reconnects (`app/queue-store.ts`
  reopens a stream the browser closed). Only a queue that never loaded
  says so, in place of the list. The phone apps follow the same rule.
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

## Product copy: say less

AI-written product copy says too much, in sentences with too many dependent
clauses. Cut both. These rules cover every user-facing string: page and
section intros, descriptions, labels, empty states, hints, tooltips and
dialogs.

- **Say what it is or what to do, then stop.** One short sentence is
  usually enough. Leave out the mechanism behind it, how it connects to
  other features, settings that would change it, edge cases and
  alternative paths. Those belong in the docs.
- **One idea per sentence.** No dependent-clause chains ("…, whose X can
  Y the Z that W"), no trailing qualifiers ("…, notified or not", "…,
  whoever decides it"), no "including X" asides, no "X is Y: A does B,
  and C does D" constructions.
- **One point per description.** Don't follow it with a sentence that
  elaborates on or reassures about the first, and don't bundle a second
  topic in ("X and what happens when Y").
- **Lead with what the user can do.** Frame features by what they let the
  user do ("let you decide… automatically"). On a page for doing
  something, say what to do there. Don't open with a definition, and don't
  present withHuman or its features as actors ("withHuman asked you…",
  "pipelines decide…").
- **Give the default path only.** No optional shortcuts or alternatives
  ("To connect right away, …", "Or, you can…").
- **Plain, literal words.** Labels name the capability ("Can review"), with
  no turns of phrase ("Yours to decide"). Name the noun ("All requests"),
  not "Everything". Prefer the user's words over system internals
  ("notified", "sent to you" over "routed"), and use the concrete action
  the user sees. Use one term per concept, and the same words in a label
  and its description.

Examples:

- "Requests sent to you will appear here. Switch to Can decide for
  everything routed to you, notified or not." → "Requests sent to you will
  appear here."
- "Requests withHuman asked you to review." → "Requests you were notified
  about"
- "Yours to decide" / "Everything routed to you, notified or not." → "Can
  review" / "Everything you can review"
- "Everything you can see, whoever decides it." → "All requests you can
  see"
- "Approval pipelines decide what to do with an agent's request. Every
  request enters the entry pipeline, whose delegate blocks can hand
  matching requests to pipelines other teams own." → "Approval pipelines
  let you decide what to do with approval requests automatically. All
  requests go through the entry pipeline first."
- "Escalation paths define which humans get asked to approve a request
  after approval pipelines decide that a human request is needed." →
  "Escalation paths define which specific humans get notified to approve
  requests"
- "An endpoint is a service you run that a webhook block can consult:
  withHuman signs and posts the request, and the endpoint answers with an
  outcome." → "Add webhooks here to use in your pipelines"
- "Let your scripts act with your permissions. Your keys follow your
  permissions as they change." → "Let your scripts act with your
  permissions."
- "Claude Code opens the sign-in in your browser the first time it uses a
  withHuman tool. To connect right away, run /mcp and choose withhuman,
  then Authenticate." → "Claude Code opens the sign-in in your browser the
  first time it uses a withHuman tool."
- "Full setup for each client, including headless use with an existing
  key, is in the withHuman MCP server guide." → "Full setup for each
  client is in the withHuman MCP server guide."

## Architecture invariants

- `runtime.NewServer` composes the public, stateless HTTP boundary with the
  built product frontend embedded, and `runtime.RunWorker` the Temporal
  workflows and activities. Core ships both in one process as `withhuman`;
  the hosted edition runs them as its own split processes, and the
  deployment files for that topology live with it. Application
  database migrations are embedded from `db/migrations` via the `db`
  package; `db.Apply` is the one runner, and an edition's own chain uses
  it with `db.WithTable` to keep its record apart.
- A migration file that has shipped in a tagged release is frozen: the
  runner applies each file once by name, so an in-place edit silently never
  reaches any database that already ran it. Schema changes always go in a new numbered file. The
  no-compatibility-shims rule is about interfaces and designs; it does not
  extend to editing applied migrations. The chain restarted as
  one `0001_initial.sql` at the repository split (2026-09-24), generated
  from a dump of the schema the old files built.
  Because the squashed file reuses the old first file's name, `db.Apply`
  refuses a database whose record table names a file the chain does not
  ship (`*db.ForeignChainError`) instead of skipping the schema, and the
  single binary tells the person to move the data directory aside.
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
  team, agent, or pipeline, the last two keyed by the agent's slug and
  the pipeline's key); `Principal.Permissions` is the flat projection
  for clients and server code never reads it. Never check a role name.
  Every route registers through `httpapi.handle` with an access label,
  and every OpenAPI operation carries the same label in
  `x-withhuman-permission` (`public`, `self`, or a registry key);
  `make api-lint` and the conformance test in `internal/httpapi` fail on
  drift. Edition-owned keys arrive through `runtime.ServerOptions.Permissions`;
  a definition's `BuiltInRoles` names the admin, reviewer, or auditor
  roles that hold it, merged into the built-in role lists at reconcile
  time the way the owner's list already resolves against the registry.
- Every edition offers the predefined roles (`domain.HumanRoles`: owner,
  admin, reviewer, auditor; `GET /api/v1/roles`): an invitation carries
  a `role`, `PUT /api/v1/members/{id}/role` (browser-only) replaces a
  member's direct grants with one role everywhere through
  `members.SetMemberRole`, and `RoleAssignment.built_in_role` names the
  role an assignment grants. Custom permission policies (member and team
  `/permission_policies` writes, an invitation's `permission_policies`)
  are the edition's choice: `runtime.ServerOptions.CustomPermissionsEnabled`
  admits them and runtime-config reports `custom_permissions_enabled`.
  The open edition leaves it off, so those writes answer 403
  `custom_permissions_unavailable` and the frontend offers roles alone;
  the hosted edition turns it on and offers both. The policy engine
  itself (private role snapshots, reads, the grant kernel) stays in core
  either way, since organization keys and directory grants use it.
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
- Metrics are OpenTelemetry, in the exported `telemetry` package: each
  process's main calls `telemetry.Start` (the single binary's
  `cmd/withhuman`, an edition's mains), which installs the meter provider
  only when the standard `OTEL_*` variables configure an exporter; every
  core instrument is declared in `telemetry/instruments.go` with semantic
  convention names where one exists (`http.server.request.duration`,
  `db.client.connection.*`, Temporal's own through its SDK handler) and
  `withhuman.*` otherwise, and an edition makes its own from
  `telemetry.Meter`. Attributes are closed vocabularies, never an
  organization, agent, member, tool or request identifier. Request
  outcomes are counted in the store after the transaction commits, so a
  rollback or an idempotent replay counts nothing. Tests read metrics
  through `telemetry/telemetrytest`, comparing counts before and after.
  Exporter settings are the `OTEL_*` variables of the OpenTelemetry
  specification, the one exception to the `WITHHUMAN_` prefix: operators
  and collectors already speak them.
- Request-scoped logging uses `loggermdc` and the context-aware
  `slog` methods. Service entry points call `loggermdc.Configure` before
  composing dependencies so injected loggers and package-level `slog` calls
  share the MDC handler. Temporal workers install both the MDC propagator
  and logging interceptor. Enrich logging context by deriving a new context,
  never by mutating shared fields. Only the standard correlation keys may cross
  Temporal boundaries, and credentials, request payloads, and other sensitive
  values must never be placed in MDC.
- Decision authorization is atomic against current membership, permissions,
  scope, routing, reauthentication or MFA requirements, and the request's
  terminal state. There is no self-approval rule: a person may decide a
  request their own agent raised wherever routing reaches them. Requests, decisions, and audit records are
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
- AAP asynchronous mode and the provisioner's instance lifecycle live in
  `internal/aapdelivery` and `authn/aap_instances.go`. A provisioner
  creates, reads, updates and deletes instances at
  `/api/aap/v1/instances[/{id}]`. Creation and updates require an
  `Idempotency-Key` (keys in `agent_instance_provisioning_keys` and
  `agent_instance_update_keys`, leased per attempt, kept seven days, pruned
  by the dispatcher). A replayed creation returns the original credential
  without storing it: the secret is an HMAC of the provisioner's own
  secret and a per-key salt, so only the hash is persisted and the salt
  goes when the key expires. A `delivery` receiver must echo a signed
  `webhook.verification` challenge before the instance is created or its
  receiver replaced; its `whsec_` secret is sealed in
  `agent_instance_deliveries` (so it needs `WITHHUMAN_SECRETS_KEY` and the
  webhook egress policy). Every store method that inserts a terminal
  decision also calls `recordRequestResolvedEvent` in the same
  transaction; a new decision path must too. The worker's dispatcher
  delivers the ID-only `request.resolved` events, signed with Standard
  Webhooks, with backoff for seven days until a `2xx`, pausing while the
  instance is disabled. Deleting an instance keeps the row for the record
  (`deleted_at`, disabled, never re-enabled), revokes its credentials,
  removes the receiver with its queued events first, and then cancels its
  pending requests, so they send no notification. The app's New
  instance page reaches the same verification for any agent, personal
  included, in two steps, because the receiver must hold the secret
  before it can answer: `POST /api/v1/agents/{slug}/instance_registrations`
  generates the registration ID and `whsec_` secret, shows them once,
  and keeps the secret sealed (`InstanceRegistrationSigningSecretLabel`)
  in `agent_instance_registrations` for an hour with a reserved instance
  ID, bound to the agent and the caller's actor; then
  `POST /api/v1/agents/{slug}/instances` with `delivery` (registration ID
  and URL) opens it, verifies through the same `VerifyInstanceDelivery`,
  and creates the instance with its receiver while deleting the
  registration in one transaction (`agent_instance.registered` carries
  the delivery). A failed verification keeps the registration for a
  retry; an expired or used one is 410 `instance_registration_expired`,
  and expired rows are pruned with the AAP state. Not built yet:
  per-destination rate limits on registration and delivery, delivery on
  the CLI and onboarding enrollment paths, and editing or removing a
  receiver in the app.
- `Agent` is the stable organization-owned machine principal, addressed
  everywhere outside the database by its immutable slug and shown by its
  editable name. `AgentInstance`
  is one concrete enrolled runtime, and every agent credential is bound to an
  instance. Enrollment records are temporary bootstrap state. Do not model
  devices, installations, or service accounts as alternate agent identities.
- An agent has a `kind`, chosen at creation and immutable (a database
  trigger refuses the flip): `service` for an agent the organization runs
  (a support bot; instances belong to nobody, administrators enroll them
  with `agent.credential.issue`, provisioner tokens mint them) and
  `personal` for one people run for themselves (a coding assistant; the
  guided setup creates its runtime agents as personal). An instance of a
  personal agent carries `agent_instances.membership_id`, the member who
  enrolled it, and a trigger holds the rule that an owner exists exactly
  when the agent is personal. Ownership is what changes: any active
  member may enroll their own instance of a personal agent (the
  enrollment routes are `self`, `ListEnrollmentAgents` always lists
  personal agents and narrows service agents by the issuance scope,
  `requireEnrollmentAuthority` is the one rule), a personal agent has no
  provisioner tokens (`ErrPersonalAgent`), an organization key cannot
  register a personal instance (`ErrAgentOwnerRequired`), the credential
  resolves to a principal whose `MembershipID` is the owner and refuses
  while the owner's membership is not active, a request made by the
  instance copies the owner into `approval_requests.requester_membership_id`
  (product reads add `requester_display_name`), and deprovisioning the
  member disables their instances and revokes their credentials in the
  same transaction as their personal API keys
  (`disableMembershipAgentInstances`, audited `agent_instance.disabled`
  with reason `membership_deprovisioned`). The request also carries the
  owner from the principal before the pipeline runs, so routing can ask
  them (the agent-owner route below).
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
  email-first: `/auth/login/methods` discovers
  the methods for an email, `/auth/login` verifies the password globally
  and either completes (one organization) or returns a single-use `whl_`
  login ticket plus the organization list, and `/auth/login/select`
  redeems the ticket for one organization. Tickets also retain the session
  client and device context supplied by an assertion-based native login. A wrong password or unknown
  email is 401 `invalid_credentials` (`authn.ErrInvalidCredentials`,
  still an `ErrUnauthenticated`), so the sign-in form can say so instead
  of reporting an expired session. A refused password for an email no
  active user has is handed to the edition's `authn.SignupOffer`
  (`Service.RegisterSignupOffer`; the hosted edition mails the address a
  link to create an account). The answer stays `invalid_credentials`
  either way, so the form never says which emails have accounts, and
  runtime-config's `password_login_offers_signup` lets the form point
  to the inbox in every refusal. Whether an organization signs in
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
- The tenant lifecycle lives in the exported `tenants` package: one activation transaction shared
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
  `Server.ResolvePrincipal` and `Server.StreamsEnded` let an edition's
  event stream behave like core's own: resolve the credential again on
  every tick, so the stream ends once the session expires or loses its
  grant, and return when shutdown starts.
- The open edition reports itself once a day (`internal/heartbeat`): a
  random installation ID from the one `installations` row, the version,
  OS, architecture and deployment kind, nothing else. `cmd/withhuman`
  sets `WorkerOptions.HeartbeatURL` (sending) and
  `ServerOptions.UpdateChecks` (the Version settings page and the to-do
  bell's update item, runtime-config `update_checks_enabled`); other
  editions leave both unset. `WITHHUMAN_TELEMETRY=off` or `DO_NOT_TRACK=1`
  (`config.Telemetry`) turns both off, and a version that is not
  major.minor.patch never sends. Replicas claim each send on the row,
  once per `application.HeartbeatInterval` or straight after an upgrade;
  the answer (latest release, advisories) is validated before it is
  stored, because its advisory links are rendered to administrators.
  Never add a field to the heartbeat without updating the Updates and
  telemetry docs page that lists every field.
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
  block config, an outcome block only with `outcome: human`), a pipeline
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
  to the default queue with reason `path_inactive`. The agent-owner route
  (`ask_agent_owner` beside a block's `escalation_path`,
  `default_ask_agent_owner` beside the revision default; the path is the
  required fallback) pins an `agent_owner` snapshot naming the requester
  when their membership is active and holds `request.decide` for the
  request, and the fallback path beside it; otherwise the fallback takes
  the request at routing time with `agent_owner_fallback`
  (`no_agent_owner`, `agent_owner_inactive`, `agent_owner_cannot_decide`).
  The owner is the only target, paged through their own ladder, with no
  levels and no timer: the fallback never takes over because of time.
  Suspending or deprovisioning the owner (`handOffAgentOwnerRequests`, in
  the member and directory transactions) appends
  `approval_request_agent_owner_handoffs` rows and outbox events
  (`approval.agent_owner_handoff`) the worker's dispatcher turns into the
  workflow's `agent_owner_deactivated` signal, which walks the pinned
  fallback. Every reader of the snapshot goes through
  `readPinnedRouting`/`effectiveRouting`, which read an unhanded
  `agent_owner` row as its owner alone and a handed-off one as its
  fallback. The router evaluates every
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
  pending requests: same eligibility as deciding (minus the
  auth-freshness check), no takeover, idempotent re-claim, and a
  retrigger timeout (`organization_policies.claim_retrigger_seconds`,
  default 900, capped at the request deadline) after which the claim
  lapses via a workflow timer, with a read-time-expiry and next-claim
  sweep as the safety net. Claims never block decisions, never cross AAP
  (the `claim` field exists only on the product representation), and are
  audited as `routing.claimed` / `routing.claim_released` /
  `routing.claim_lapsed`.
- Pipelines are keyed objects:
  `request_approval_pipelines.pipeline_key` (the agent slug pattern,
  chosen once, never reused) with a `name` on every revision (renaming is
  a revision), numbered revisions, `default_escalation_path`,
  and exactly one active revision per live key at all times (a deferred
  constraint trigger refuses leaving a key without an active row). A
  revision that was never active is a draft and changes in place
  (`PUT .../revisions/{n}`, the create body, keeping its number); its
  first activation stamps `activated_at` (the table trigger does it) and
  freezes it for good, 409 `revision_not_draft` after that. Escalation
  paths, webhook endpoints and team escalation policies follow the same
  draft rule. Every
  organization has the reserved `entry` pipeline (`domain.EntryPipelineKey`,
  seeded active in the activation transaction by `ensureEntryPipeline`
  with one outcome block, `approve-every-call`, so an agent connected
  before anyone writes a rule is never held; running out of blocks stays
  a human outcome, and removing that block is how an organization starts
  sending calls to people), which every request runs; every other pipeline
  is created explicitly (`POST /api/v1/request-approval-pipelines` with
  `pipeline_key`, `name` and its first revision, organization-scope
  `pipeline.write`, 409 `request_approval_pipeline_exists` on any key
  ever used, 409 `request_approval_pipeline_entry` for `entry`) and
  reached only through `delegate` blocks. A delegate (`config: {when,
  pipeline_key}`, synchronous, no reason, no path, never `entry` or its
  own pipeline) is a branch whose body is the target's active revision:
  the condition belongs to the pipeline being branched out of, what
  happens after the match to the target's owners, who hold `pipeline.*`
  at the scope kind `pipeline` (id the key; `admin` is bindable there).
  A request that enters the target is decided inside it; when the target
  runs out of blocks the run ends `human` with a closing evaluated event
  on the delegate (`end_of_branch: true`, reason code `end_of_branch`),
  routed to the target's default path, else the enclosing pipelines'
  defaults walking outward, else the default queue. The effective
  pipeline is flattened per run in `internal/approvalpipeline/effective.go`
  from the pinned or active revisions (a delegate immediately followed
  by the target's blocks; positions index the flat list,
  `resume_position` stays one integer, metadata keys are
  `<pipeline_key>/<block_key>`, webhook idempotency keys
  `requestID:pipelineKey:blockKey`), and a suspension pins one revision
  per pipeline in `approval_request_pipeline_suspension_pins`. Activation
  (`If-Match` on the current active revision) takes the organization-wide
  advisory lock `<org>:pipeline-graph` before the key lock and checks the
  whole active graph with the candidate standing in: every delegated key
  live (409 `request_approval_pipeline_inactive`), no cycle (409
  `request_approval_pipeline_delegation_cycle`), depth at most
  `domain.MaximumDelegationDepth` (8, 409
  `request_approval_pipeline_delegation_too_deep`), flattened size from
  `entry` at most `domain.MaximumEffectivePipelineBlocks` (10,000, 409
  `request_approval_pipeline_too_large`); diamonds are legal. Revision
  creation and preview require every delegated key to exist
  (`approvalpipeline.WithPipelineKeyResolver`, 400
  `request_approval_pipeline_not_found`), `/revisions/{n}/references`
  reports each target's state, and a run-time violation is
  `compiled_cache_unavailable` (human on the entry default), never a
  partial run. There is no deactivate and no restore: `DELETE
  /request-approval-pipelines/{key}` (`pipeline.activate`) archives the
  key, refused for `entry` and while any active revision delegates to it
  (409 `request_approval_pipeline_in_use` with `uses`;
  `GET /request-approval-pipelines/{key}/uses` explains it before the
  click; the list carries each key's `referenced_by`, the delegate blocks
  in active revisions that name it, which the overview's Delegated from
  column links to block by block), stamping every revision with `archived_at`, deactivating the
  active one and removing API-key assignments at `pipeline:<key>`
  (audited `request_approval_pipeline.archived`). An archived key is
  listed only with `status=archived|all`, stays readable, and refuses
  revisions and activation (409 `request_approval_pipeline_archived`).
  Agents own no pipeline: creating, archiving and restoring an agent
  touch none, and "rules for one agent" is a branch or delegate on
  `/agent/slug`. The overview offers New pipeline. A delegate draws as
  one card and an exit ("if conditions are met, continue in <pipeline>");
  the target's blocks are never inlined into the delegating pipeline's
  page, and a test replay folds whatever ran inside the target onto the
  delegate. The one place a target unfolds is a recorded run
  (`frontend/src/app/pipeline-run.tsx`, `RecordedPipelineRun`): the
  revisions a real request ran, drawn read only with each step's outcome
  and no evidence, a delegate the request entered drawing its target in
  a well under the card (the rail's `delegateContents`), and a revision
  the reader cannot read drawn as a line. An edition's audit log draws
  it.
- Request approval block types are a registry
  (`internal/approvalpipeline.BlockRegistry`), constructed once in each
  composition root and shared by compiled state, management, and
  execution. `approvalblock.Definition` is a type-dispatched envelope: the
  wire `config` object decodes into the variant pointer selected by `type`
  (`Outcome`, `Webhook`, `Branch`, or `Delegate` for core's kinds,
  or the `Config` of a registered edition kind), each type owns its
  envelope limits (`approvalblock.LimitsFor`), and the runtime
  fingerprint is the composition of every registered runtime's
  fingerprint. Core has four types: `outcome`, `webhook`,
  `branch`, and `delegate`. There is no standalone
  condition block: "if this, then that" is a branch whose blocks end in
  an outcome block (the `cel` type was removed on 2026-09-29, and
  migration `0006_outcome_blocks.sql` wiped stored pipelines). The
  condition engine (`internal/approvalpipeline/cel.go`, cel-go) stays and
  evaluates every branch and delegate condition; its version is part of
  both runtimes' fingerprints (`ConditionEngineFingerprint`). An edition adds a kind
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
  edition's asynchronous kinds may take together.
- Outcome blocks (type `outcome`, renamed from `always`) return their
  configured `outcome` (`approve`, `deny`, or `human`) for every request
  that reaches them. They use the synchronous envelope (50ms default, 1s
  maximum, one attempt), produce no condition trace, and share normal
  audit, preview, and routing behavior. Their `escalation_path` is allowed
  only for `human`. Later blocks are still validated and editable; the
  editor marks them unreachable and, for an outcome block inside a branch,
  only that branch's later blocks. A branch end nothing can reach is not
  drawn at all, since a branch holding an outcome block is the normal
  "if this, then that". These warnings do not block activation.
- Branch blocks hold their own ordered
  blocks (any type, nested without a depth cap, every one counted toward
  the 1,000-block document limit) and run them only for requests their
  `when` matches. The condition is the condition envelope, evaluated
  synchronously under the shared per-segment condition budget, so a skipped
  branch never suspends, delivers, or calls a model. Entering a branch
  commits to it: a block inside that decides ends the pipeline, and a
  branch that runs out of blocks ends it with `human`, the branch's
  optional `reason`, and its `escalation_path` (else the pipeline default); nothing
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
  paths: `webhook_endpoint_revisions` holds numbered revisions of a
  name and URL per key with at most one active (a draft's name and URL
  change in place until its first activation, then `is_active` is the
  only mutable field, advisory lock
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
  CEL, branch and delegate conditions read earlier blocks' metadata at
  `/previous_metadata/<pipeline_key>/<block_key>/...`; preview
  namespaces metadata by pipeline key and accepts seeded
  `previous_metadata`.
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
- Tool presentations say how a
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
  renders by shape (except the top-level names an edition adds to calls
  itself and registers as `hiddenArguments`), and the raw JSON stays one
  click away. A single detail field shows with the rest, unfolded. The frontend
  planner is `frontend/src/app/tool-presentation.ts`; rendered HTML goes
  through `ds/components/html-preview.tsx`, a fully sandboxed frame that
  runs no script and loads nothing.
- Request summaries are an edition's: core composes no model and writes
  none. Two seams carry them. `WorkerOptions.HumanReviewHooks`
  (`application.HumanReviewHook`) are told once a request's workflow
  finds it waiting for a person, after `resumePipeline` and before
  notification planning, through the `RunHumanReviewHooks` activity,
  which the workflow starts and never waits on (two attempts, twenty
  seconds each; a worker without hooks finishes it at once): a hook
  cannot delay a prompt or a decision, and a request a pipeline decided
  never reaches one. Adding that activity changed the workflow's command
  sequence, so a release carrying it deploys at zero pending approvals.
  `ServerOptions.RequestSummaries` (`application.RequestSummaryReader`)
  is read once per product read in `API.present`, beside the
  presentation, and fills the optional `summary` (`text`,
  `generated_at`) on the queue representation, never the AAP one; a
  failed read leaves it out. The web queue row shows a summarized
  request's summary alone over both lines, and the request page
  shows it as the title in place of the presentation title.
  `WorkerOptions.NotificationSummaries` (the same reader port, which the
  edition answers per organization) lets prompts and digests lead with
  the summary (`SendApprovalPromptInput.Summary`,
  `SendApprovalDigestInput.Summaries`). Each notification step waits for
  the hooks' activity inside its own goroutine, at most
  `humanReviewHookWait` (ten seconds) after the request started waiting,
  so a first prompt usually carries it; the main loop never waits, so a
  decision is never held. A failed read sends without it.
- The chokepoint MCP gateway is the hosted edition's: core has
  no gateway process, no
  gateway routes, no `WITHHUMAN_GATEWAY_*` settings and no
  `gateway_enabled` runtime-config entry, and the single binary embeds
  none. What core keeps for it is generic: the exported `sealbox` (an
  AES-GCM sealer bound to a storage row through associated data, used
  for every reversible credential at rest), the exported `authz` checks
  (`Check`, `Scope`, `HoldsAnywhere`, the typed `Denied`) so an edition's
  services authorize exactly as core's do, the `gateway` permission
  holder and `ActorGateway` actor kind as vocabulary an edition's
  machine principal uses, the AAP `server` field on every request, the
  tool catalogue's external source seam (below), and the
  `agent_mcp_servers` runtime-config entry through which an edition tells
  the CLI to connect every installed runtime to it. The gateway's own
  tables stay dead in core's chain until it is squashed.
- The hosted management MCP server is
  `POST /api/mcp/v1` on the product server (`internal/httpapi/mcp.go`,
  core's tools in `mcp_tools.go`, grammar in `internal/mcpreference/`): a stateless
  streamable HTTP endpoint that admits `whk_` and `who_` bearers only
  (never a session cookie) and whose tools re-enter `/api/v1` in process
  with the caller's bearer, so permissions, organization-key route
  gating, validation, `If-Match`, audit provenance and key usage
  recording are the product API's own; the loopback is marked on the
  request context and logged but not metered. Save tools create a
  revision and activate it against the current `ETag` unless
  `activate: false`, refusals relay the API's error envelope as tool
  error text, and `read_reference` serves the block, condition and path
  document grammar. The catalog is the exported `mcptools` package
  (`Catalog`, `Add`, `Call` with its revision helpers, `Result`, the
  annotations): core registers its tools there and an edition hands its
  own for its own routes through `runtime.ServerOptions.MCPTools` (the
  hosted gateway servers and audit log), so every tool dispatches,
  shapes results and relays refusals the same way. A tool exists only for
  a route that admits API keys: a member's role, permission policy
  writes and invitations are browser-only, so no tool hands out
  authority. It is distinct from the chokepoint gateway's
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
  `agent refresh-tools`, `agent eject`, `upgrade`, and `version`; internal
  `hook <runtime>` delegates to AAP and internal `mcp <runtime> <server>`
  is the relay below.
  Runtime installation, hook handling and plugin assets belong only to AAP.
  The first exception is the Hermes plugin (`plugins/hermes/withhuman`): it
  carries withHuman's name so Hermes's plugin catalog can list it under
  that name, and it can be installed with Hermes's own `hermes plugins
  install`. Replacement rule: the plugin runs `withhuman hook hermes` for
  every call and returns its verdict, so the gate stays AAP's. It is off
  until connected, in one of two ways. `hermes withhuman setup` signs in
  through the CLI's enrollment endpoints (or checks a pasted agent key
  against `/api/v1/agent_instances/self`), saves the key in the Hermes
  profile's secrets as `WITHHUMAN_AGENT_TOKEN` and the address in the
  plugin's settings, asks (default yes) before setting
  `plugins.hook_callback_timeout` to 0, since that applies to every plugin's
  hooks, and sends a test request; the hook passes the key to the CLI as
  AAP's `AAP_INSTANCE_TOKEN` and `AAP_BASE_URL`. The CLI gets only an
  allowlisted environment (`CLI_ENV_NAMES`, `LC_*`, `AAP_*`), never the
  provider keys and other plugins' secrets in the Hermes process. While the
  limit stays on, each request expires a few seconds before it and the CLI
  is stopped just before it, so no request outlives its call. This is the one place an agent
  credential lives in a runtime's configuration: Hermes keeps secrets per
  profile, so each profile is its own instance. `withhuman agent install
  hermes` instead passes the files to `adapters.WithPlugin`, and AAP
  writes and ejects every runtime file, writes an `aap.json` naming the
  CLI and its saved credential, and sets the timeout. The plugin waits at
  most a week, stops the hook on a Hermes interrupt so the request is
  withdrawn, and has no third-party dependencies. Hermes paths follow
  `HERMES_HOME` (`agentruntime.HermesHome`). When no CLI is installed, or
  an older one is, setup downloads the CLI release pinned in `connect.py`
  (`CLI_VERSION` with one SHA-256 per platform, as Hermes's catalog
  requires) into the plugin's data directory (`ctx.state.data_dir`), and the
  hook prefers that copy; on load, a connected plugin with no CLI downloads
  it too.
  Raise the pin, with hashes checked against the release, whenever the
  plugin needs a newer CLI: on load, the plugin replaces a copy setup
  downloaded that is older than the pin, in a background thread, so
  `hermes plugins update` brings the CLI along. A CLI the person installed
  is never replaced. Setup also adds the servers in
  `agent_mcp_servers` (the hosted gateway) as remote MCP servers whose
  `Authorization` header references `${WITHHUMAN_AGENT_TOKEN}`, so Hermes
  sends the profile's key without the CLI relay; only servers on the
  withHuman address's own origin are added. It then offers to install the
  `withhuman` and `withhuman-pipelines` skills through Hermes's own skill
  installer from `https://withhuman.ai/.well-known/skills/`, so they are
  ordinary hub skills that `hermes skills update` keeps current. Requests from setup send their own user agent,
  because Cloudflare refuses Python's default one. Its tests run with
  `make hermes-plugin-test`; the ones against a real Hermes need it
  importable.
  The OpenClaw plugin (`plugins/openclaw/withhuman`, id `withhuman`,
  published to ClawHub as `@withhuman/openclaw` from that directory) is
  the second exception, with the same replacement rule: it runs
  `withhuman hook openclaw` for every call from its `before_tool_call`
  hook, is off until connected, gives the CLI the same allowlisted
  environment, and has no dependencies, so it loads from a load path or a
  ClawHub install without a build. `openclaw withhuman setup` (registered
  with `api.registerCli`, declared in the manifest's `cliCommands`) signs
  in or checks a pasted key the same way, saves the key and address in
  `plugins.entries.withhuman.config` through
  `api.runtime.config.mutateConfigFile` (a config edit hot-reloads the
  plugin; a `.env` value would need a Gateway restart), adds the
  `agent_mcp_servers` on the address's own origin to `mcp.servers` as
  `streamable-http` entries whose `Authorization` header carries the key
  (OpenClaw's MCP headers take no secret references), offers to install
  the `@withhuman/withhuman` and `@withhuman/withhuman-pipelines` ClawHub
  skills with `openclaw skills install --global`, and sends a test
  request. The hook registers its own `timeoutMs` (a week plus margins);
  an operator's lower `plugins.entries.withhuman.hooks` limit makes the
  request expire just before it and stops the CLI just before that, so no
  request outlives its call. `api.runtime` is unavailable while OpenClaw
  reads CLI metadata, so `register` touches it only inside handlers. The
  pinned CLI (`CLI_RELEASE` in `setup.js`) downloads into
  `<state dir>/withhuman/bin`, with the same refresh rules as Hermes.
  `withhuman agent install openclaw` passes the files to
  `adapters.WithPlugin`; AAP writes them to its configuration directory
  with an `aap.json`, adds a `plugins.load.paths` entry, which outranks a
  ClawHub install of the same id, and removes its own embedded plugin;
  the plugin finds the CLI and its credential in that `aap.json`.
  Its tests run with `make openclaw-plugin-test`.
  `internal/cli/discovery` retains the tool discovery used by onboarding.
  The MCP servers a provider offers every agent (`agent_mcp_servers` in
  `/api/runtime-config`, `domain.AgentMCPServer`, core's key filled by an
  edition through `runtime.ServerOptions.AgentMCPServers`; the hosted
  gateway) are registered by `onboard` and `agent install` with each
  runtime as a stdio MCP server running `withhuman mcp <runtime> <server>`
  with `WITHHUMAN_CONFIG_DIR` pinned (`internal/cli/mcpservers` writes the
  per-runtime entries, `internal/cli/mcpproxy` is the relay that loads the
  instance credential and forwards the runtime's frames to the endpoint as
  a streamable-HTTP client). The credential never enters a runtime's
  configuration; the enrollment record's `mcp_servers` lets `agent eject`
  remove the entries, and discovery leaves the relay out because its tools
  are the provider's own catalogue. `internal/cli/configfile` is the
  shared ordered-JSON, TOML and YAML read-modify-write used by discovery
  and registration.
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
- AAP request and instance representations are separate from product views
  (`aap_representations.go`), and the instance routes answer with the
  protocol's own `Instance` and `ProvisionInstanceResponse`, checked against
  the pinned AAP module's OpenAPI document. Preserve the
  exact submitted timeout. Approval idempotency is instance scoped, and
  an instance reads, cancels, consumes and reports outcomes only for its
  own requests. The database records approved decision expiry once at
  insertion (`set_approval_decision_expiry`): decided_at plus 30 minutes
  when the request's instance has a row in `agent_instance_deliveries`,
  plus five minutes otherwise. Reads never extend it. Duration text in
  approval_requests.timeout is an exception to numeric duration columns:
  its exact wire spelling is immutable.
- One approval runs one call, enforced by the provider: the adapter
  consumes an approved request (`POST /api/aap/v1/requests/:id/consume`,
  `Idempotency-Key` per execution attempt) before it runs the call, and
  may then report the outcome (`.../outcome`, once, audit only). The
  `approval_consumptions` row (one per request, protected by
  `protect_approval_consumption`: approved requests only, the outcome set
  once, never deleted outside the purge) is checked under the request's
  row lock against the database clock, with `approval.consumed` and
  `approval.outcome_reported` audited. The same key replays the current
  state even after expiry; another key is 409 `already_consumed`, and
  other refusals are `not_approved`, `approval_expired`, `not_consumed`
  and `outcome_conflict`. A consumption is the agent using an approval;
  it is unrelated to a reviewer's claim on a pending request.
- Guided onboarding takes a new
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
  `rules_set_at` once the hooks are in (the first time only) and, every
  time it is left, chooses `test_commands` by previewing the stock test
  calls against the active entry pipeline (`internal/onboarding/testcall.go`:
  shell, then a file write, then MCP tools other than
  `toolsuggest.Irreversible` ones; a runtime nothing reaches a person
  for gets no command, and the page sends the person to their coding
  agent for a prompt); the CLI is done at `installed`;
  the person then records the request they decided in the one test
  (`tested`, checked against an enrolled agent) and finishes, which
  requires `tested`. Provisioning (the escalation path that notifies
  the person, the runtimes' agents, and one entry pipeline revision
  holding a top-level `branch` per runtime, block id the runtime key
  and condition `/agent/slug equals <runtime>`, whose body approves
  every call, so the coding agent the rules step asks to write the rules
  is never held waiting for a person who does not know the product yet;
  `internal/onboarding/generate.go`,
  inserted before the first top-level outcome block, other blocks untouched,
  no revision when the document is unchanged) runs under the person's
  own session when they reach the rules step; enrollment is the one CLI-initiated write that mints,
  under a principal rebuilt from the session's membership at that
  moment, because agent credentials are handed to the CLI once and
  never stored. Tool suggestions are deterministic
  (`internal/toolsuggest`) and carry a reason code; they mark tools in
  the gated-tools table and gate nothing until the person picks them. The approval rules step offers two ways: the
  published withHuman skills (`npx skills add https://withhuman.ai`)
  plus a prompt for the person's own coding agent (which writes the
  entry pipeline through the hosted MCP server while the page polls
  it), recommended, and a gated-tools table per agent (which rewrites
  that runtime's branch alone), offered only for a quick try. There is
  no setup assistant: writing rules from a description is the coding
  agent's job, through the skills and the MCP server.
  Nothing in the flow takes the person elsewhere in the product before
  they finish or skip: the Slack and Discord installs and the Discord
  link start in place with `return_to: onboarding`, which the OAuth
  state carries as a prefix (`internal/oauthreturn`), so the callback
  sends the browser back to `/welcome` even when the provider refuses,
  and the shell's `useOAuthOutcome` reports the result there.
  `organizations.onboarding_completed_at` drives the `/welcome` redirect
  through `/me`'s `onboarding.required`; finishing or skipping sets it.
  After that, `/me`'s `onboarding.checklist` (for the people who can run
  the setup, `onboarding.Service.Checklist`) says what the organization
  has set up, however it was set up: `agent_connected` (a live instance
  of an active agent), `rules_written` (the active entry pipeline,
  through its delegates, can hold or deny a call; the seeded pipeline and
  the setup's starting branches approve every call, so they do not
  count) and `tested` (a person decided a request). While an item is
  left, a Finish setup card at the sidebar's foot
  (`app/setup-reminder.tsx`) lists them and opens `/welcome`; it is
  hidden per browser with its close button.
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
  `agent_instance.disabled` / `agent_instance.enabled`. The same
  `PATCH /api/v1/agent_instances/{id}` takes `name` (the Instances tab's
  Rename instance action), audited as `agent_instance.renamed`: the
  name stays unique within the agent (409 `agent_instance_name_taken`),
  nothing addresses an instance by name after enrollment, and requests
  already made keep the `agent_instance_name` they were created with.
  Archiving an
  agent (`DELETE /api/v1/agents/{slug}`, `agent.write`, the page's Archive
  action with the name typed to confirm) is the third status,
  `archived` with `agents.archived_at`: every live credential and
  provisioner token is revoked, instances and history stay readable for
  the record, no instance or provisioner can be minted under it, the
  status switch and instance switches answer 409 `agent_archived`
  (audited as `agent.archived`), and its name is freed while its slug
  stays taken. Onboarding whose runtime slug (`codex`) belongs to
  an archived agent fails with `agent_archived` and tells the person to
  restore the agent in the app or choose another slug. Archiving is
  reversible: `POST /api/v1/agents/{slug}/restore` (the Restore action
  beside the archived badge) puts the agent back to `active`, audited
  as `agent.restored`;
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
  link, never the arguments (a request summary is the one exception, see
  notifications below), and deciding still happens in the product.
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
  button, never the arguments apart from a request summary); a member
  whose privacy settings block DMs
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
  never the request arguments, which routinely hold customer data. The one
  exception is a request summary (`SendApprovalPromptInput.Summary`, a
  digest's `Summaries`): the edition writes it and checks it against the
  arguments, and passes it only when the organization allows summaries in
  notifications. Email, Slack and Discord then lead with it and keep the
  tool as a detail line; the logging channel never writes it. The
  invite link is a credential and appears only in the invitation email,
  never in logs. Recipients come from the pinned routing snapshot's reachable
  targets when a path is pinned, and otherwise from every active member
  holding `request.decide`, so the default queue actually reaches people
  before anyone names reviewers. Delivery never blocks or fails the action that
  produced it, and a prompt is delivered once per step: the registry
  logs a channel failure and answers success, and the delivery activity
  runs with a single attempt, because a retry re-sends through every
  channel that already succeeded (a refused APNs key once emailed and
  texted the whole organization on every backoff). Underneath the ladder,
  notification windows (`notification_windows` and `notification_holds`) throttle prompts per
  person, organization and contact kind: the first prompt in a quiet
  period sends at once, prompts due while a window is open are held, and
  the workflow that held one closes the window at its instant
  (`CloseNotificationWindow`, a no-op when another holder got there first)
  and sends one digest (`SendApprovalDigest`) of the held requests still
  pending and unclaimed, or the plain prompt when only one is left. Each
  kind declares widening windows (`domain.ContactKind.DigestWindows`, core's
  in `coreDigestWindows`); the last is the kind's cap. A window that cannot
  be read sends at once, and transactional messages never pass through one.
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
  door on the sign-in screen; banners above every workspace page, kept
  for notices that stop approvals working; `todoSources`, hooks that
  list what the person should do under the bell in the sidebar header
  (no source, no bell; an item stays until it is done); the
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
  card, and `serverPath`, the edition's page where one of its servers'
  connection is fixed, which the Tools tab's Servers card opens), `toolReference` slot (what the tool hover card asks an
  edition about a tool's server: its display name and the matching gated
  pattern) and `serverDirectory` read (the servers an edition registers
  centrally, by the alias a request carries as `server`, with their
  names, which the pipeline route loader offers in the condition
  editors' MCP server field before any call through them is seen);
  `hiddenArguments`, top-level argument names the edition writes into
  calls itself, which the request page's summary leaves out; and
  `contactKinds`, the contact kinds an edition registers
  beside email, Slack and Discord, each built with `defineContactKind`:
  the kind, its label, icon and add-menu description, `available` over
  the edition's runtime-config with the copy shown when it is not, the
  whole add flow as a component (`ContactAddProps`), how a method of
  the kind lists (`ContactMethodRow`: title, a badge only for a state
  that needs the person, removal with the reason when the list cannot
  remove one, and a confirmation panel under an unverified row; the
  kind's label is the row icon's tooltip), and the ladder
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
  rather than writing a one-off tooltip. The pipeline editors'
  suggestions (tools, argument fields, values, the test sample) are the
  other: they are not content, so the pipeline page never waits for
  them. They load into a module-scope store
  (`requestPipelineSuggestions` in `app/pipeline-suggestions.ts`) when
  someone starts editing or testing, or from the loader when the link
  opens the editor, and they are kept for a minute per signed-in person
  across pipelines.
- Start the product locally with the single binary
  (`WITHHUMAN_BOOTSTRAP_SECRET=change-me-before-production go run ./cmd/withhuman`,
  after `make frontend`). Use `./scripts/smoke.sh` against it for an
  agent-friendly real flow covering bootstrap and login, browser-assisted,
  directly registered, and provisioner-minted agent instances, approval
  creation, idempotency conflicts, decisions, long polling, and expiry.
- Clean-schema verification means a fresh data directory: pass
  `--data-dir` (or `WITHHUMAN_DATA_DIR`) pointing at a disposable path
  rather than deleting `~/.withhuman`.
- `skills/<name>/SKILL.md` are the published agent skills (`npx skills
  add https://withhuman.ai`, served from the site's
  `/.well-known/agent-skills/`): user-facing, so they follow the user guide
  writing rules and never use em dashes. A change to a CLI command, a
  management MCP tool, or behaviour a skill describes updates the skill
  in the same change. The skills name hosted-only features as such and
  send agents to `read_reference` for grammar instead of copying it.
  `internal/cli/skills_test.go` checks frontmatter and CLI commands, and
  `TestMCPListsTheManagementCatalog` checks the MCP tools they name.
  Contributor-only skills go in `.agents/skills/` with
  `metadata: internal: true`.
- `examples/` holds user-facing examples that connect an agent framework
  to withHuman through the AAP SDKs: one self-contained refund agent per
  framework (`python/` runs with `uv run`, `typescript/` on Node's type
  stripping). Nothing in `make` builds or tests them, so run one against a
  local single binary after changing it. Keep them small, keep the same
  scenario across frameworks, and follow the user guide writing rules.
- Workflows under `.github/workflows/` pin every action to a full commit
  SHA with the version in a trailing comment. Dependabot bumps minor and
  patch versions; bump a major version by hand after reading its changelog.
  Every job runs on GitHub-hosted runners, never a self-hosted or
  third-party runner.
  Only `tag.yml` pushes, with `RELEASE_CREATION_TOKEN`; every other
  checkout sets `persist-credentials: false`. Every merge to `main` is
  tagged as the next patch version and released; run `tag.yml` by hand
  for a minor or major bump.

- The single-agent page has Provisioner tokens, Instances, Approval
  requests, and Tool Catalogue tabs, plus the tabs an edition registers
  (`extensions.agentTabs`; the hosted Gateway Tools tab). It opens on
  Instances when the URL names no tab. The Provisioner tokens
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
  installation with browser authorization, manual agent API-key generation,
  and, when runtime-config's `webhook_enabled` is set, Pause runs while
  waiting: a Generate signing secret step that shows the registration ID
  and signing secret once, then the instance name and receiver URL with
  Verify receiver (a failure keeps the secret on screen for a retry; an
  expired registration starts again).
  CLI commands use runtime-config's public origin and download mirror; the
  API-key and webhook results show the instance credential once with its
  AAP provider URL. Every path requires scoped `agent.read` and
  `agent.credential.issue` and an active agent. The page never creates an
  instance merely by showing a CLI command.

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
  `agent.read` and never return credentials. A failed source carries a
  reason: an instance names it in one of a few fixed sentences
  (`catalogclient.failureReason`), never the raw error, which can carry a
  server's URL, arguments or a credential, and an edition's source says
  why in its own words. Each source reports the instance's name and
  whether its owner can serve now (`available`); the Tools tab folds
  sources into one row per server across instances and projects, leaves
  out sources of instances that can no longer report, and opens a
  failing row into its reasons and the fix. Local installation and
  onboarding report automatically; later refreshes use
  `withhuman agent refresh-tools <runtime> --agent <slug>` from the project.
  `GET /api/v1/tool_definitions` is the same read folded to distinct
  definitions (name, server, schema and description, with every runtime
  and instance offering them) in one unpaged response, because an
  external source's tools repeat once per agent that sees them; the
  pipeline editors read it instead of paging the catalogue. Request
  history contributes only permission-visible, nonpersistent editor
  suggestions, read in one listing (`GET /api/v1/requests?per_agent=`,
  each agent's newest requests, automatic decisions included). Catalogue schemas are advisory and must never affect pipeline
  execution or rewrite saved conditions. This product API is outside AAP.
