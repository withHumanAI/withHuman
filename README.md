# withHuman

withHuman is a human-in-the-loop platform: an agent asks for approval
before a consequential action, a person approves or denies it, and the
agent continues. This repository (`github.com/withHumanAI/withHuman`) is
the self-contained implementation of the product: the single binary, its
protocol adapters, the web frontend and the command-line tool. The hosted
edition at withhuman.ai builds on it from a separate, private repository.

The implementation has four runtime units, composed by the exported
`runtime` package:

- The server: Gin APIs, synchronous CEL policy evaluation, SSE, health and metrics, and the embedded React reviewer UI.
- The worker: pending-review outbox dispatch, deterministic Temporal workflows, and PostgreSQL or notification activities.
- Temporal: durable human approval coordination, ordering, retries, and deadlines.
- PostgreSQL: separate `withhuman`, `temporal`, and `temporal_visibility` logical databases with separate owners.

Core ships all four in one process, the `withhuman` single binary. The
hosted edition runs the same server and worker compositions as split
processes from its own repository.

## Agent CLI

The client command is built from `cmd/withhuman-cli`. Use `withhuman onboard`
for the welcome wizard, or `withhuman agent discover`, `withhuman agent install
<runtime>` and `withhuman agent eject <runtime>` for an existing organization.
It embeds the AAP adapter library for all six runtimes. See the
[CLI guide](deployment/cli/README.md) for enrollment, configuration, building
with the pinned module and provider-first rollout.

## Single binary

`withhuman` runs all of that in one process with no external services:
embedded PostgreSQL as a managed child process, the Temporal server
in-process on loopback, the worker, and the HTTP server with the bundled
frontend. It is the deployment shape core ships, from a laptop to a server
behind a reverse proxy; the hosted edition runs the same compositions as
split processes. See `design_doc/04-single-binary.md` in the platform
repository.

```sh
make frontend
go run ./cmd/withhuman
```

Open [http://localhost:8080](http://localhost:8080). State lives in
`~/.withhuman` (override with `--data-dir` or `WITHHUMAN_DATA_DIR`); the
generated bootstrap secret is in `~/.withhuman/bootstrap-secret`. The first
start downloads and extracts the PostgreSQL binaries (about 32 MB); later
starts are offline and take about half a second.

## Start locally

Requirements are Go, Node, `curl`, and `jq`.

```sh
make frontend
WITHHUMAN_BOOTSTRAP_SECRET=change-me-before-production go run ./cmd/withhuman
./scripts/smoke.sh
```

Open [http://localhost:8080](http://localhost:8080). On a fresh data
directory, use the bootstrap secret under **First setup** (without the
variable, the binary generates one into `~/.withhuman/bootstrap-secret`).
The bootstrap form creates the first owner with an email address and
password, then disables itself as soon as the first active owner exists.
Repeat logins use the owner's email, password, and organization slug.

The machine-readable API contract is available at
[http://localhost:8080/api/openapi.yaml](http://localhost:8080/api/openapi.yaml)
and in [internal/httpapi/openapi.yaml](internal/httpapi/openapi.yaml).

## Fast development loop

```sh
make test        # generate the frontend, then run Go tests/vet
make verify      # add OpenAPI and deployment validation
make frontend    # rebuild the React assets embedded by Go
make smoke       # exercise one real approval against a running single binary
```

The smoke test is deliberately agent-friendly. It bootstraps or logs in,
exercises browser-assisted agent instance enrollment, directly registers a
second agent instance, creates an AAP request, approves it through the product API,
verifies the terminal result through the AAP read path, and exercises a real
Temporal-backed deadline through long polling.

`webapp/assets` is generated output and is not committed. Use the
Make targets rather than invoking `go test` or `go build` directly from a clean
checkout: `make test`, `make verify`, and `make build` generate the Vite bundle
before Go compiles the embedded frontend. The Docker build performs the same
generation in its frontend stage.

On a fresh database, the smoke owner defaults to `owner@example.com` with
password `local-development-password`. Set `WITHHUMAN_SMOKE_PASSWORD` when an
existing smoke owner uses a different password.

## Code boundaries

```text
cmd/                         process composition only
domain/                      provider-neutral identity and approval types
application/                 focused storage and orchestration ports
approvalblock/               serializable CEL block definitions and Go authoring DSL
authn/                       human sessions, agent enrollment and credentials
entitlements/                open plan-gating adapters (unlimited by default)
loggermdc/                   shared logging bootstrap and immutable correlation context
internal/approvalpipeline/   safe block execution contract and compiled CEL runtime
internal/approvals/          use-case validation and long-poll behavior
internal/orchestration/      Temporal client, workflow, and activities
internal/postgres/           canonical product storage and atomic authorization
sealbox/                     AES-GCM sealing for secrets at rest (exported for editions)
authz/                       the permission checks every service, core's or an edition's, uses
internal/httpapi/            Gin boundary and embedded frontend
frontend/                    static React reviewer application
db/migrations/               application-owned migrations
deployment/temporal/         Temporal-owned migration and namespace setup
```

Temporal is never used for CEL policy evaluation or as the product read model.
The server evaluates the active pipeline synchronously, then atomically commits
the request, ordered audit evidence, state, optional automatic decision, and a
pending-only workflow-start outbox item. Automatic decisions return without a
Temporal call. Idempotent activities commit later human and system decisions.
API reads, queue SSE, and long polls read PostgreSQL only. Decision activities
atomically recheck active membership, application permissions, scope,
self-approval policy, authentication strength and freshness, and terminal
state.

The runnable authentication adapter is local email and password. Passwords are
stored separately from canonical users as Argon2id hashes. Enterprise single
sign-on, directory sync and verified email domains are the hosted edition's;
core keeps the login policy seam they plug into and the directory-managed
memberships they write through. The initial
notification adapter logs correlated delivery attempts; the email, Slack and
Discord adapters are optional integrations switched on by configuration.
Text messages, WhatsApp and push to the phone apps are the hosted edition's
channels, registered through the contact-kind registry an edition fills.

## Chokepoint MCP gateway

The chokepoint MCP gateway (a streamable-HTTP MCP server that aggregates an
organization's registered downstream MCP servers, holds their credentials
so agent machines never see them, and holds gated tool calls for approval)
is part of the hosted edition, in the `withhuman_saas` module. Core has no
gateway process and the single binary embeds none: agents call their MCP
servers directly and withHuman sees those calls through the agent's hooks.
What core keeps for it is generic: the `sealbox` sealer, the `authz`
checks, the tool catalogue's external source seam
(`application.ToolSource`, registered by an edition through
`runtime.Server.RegisterToolSource`), and the `agent_mcp_servers`
runtime-config entry (`runtime.ServerOptions.AgentMCPServers`) through
which an edition has the `withhuman` CLI connect every runtime it
installs to such a server.
## Webhook endpoints and webhook blocks

A webhook block posts the request to a service the organization runs and
maps its answer onto an outcome bounded by the block's allowed outcomes
(`design_doc/08-webhook-block.md`). Endpoints are revisioned like
escalation paths: a stable key that blocks reference, immutable revisions
of a name and URL with one active, and one signing secret per key. They
are managed on the `/webhooks` page by members with `webhook.manage`. The
secret is sealed under `WITHHUMAN_SECRETS_KEY`, so that setting enables
the feature on the server and the worker; without it the page reports the
surface absent and the pipeline editor disables the webhook choice.
Deliveries go to public HTTPS only. `WITHHUMAN_WEBHOOK_ALLOW_INSECURE=true`
and `WITHHUMAN_WEBHOOK_ALLOW_PRIVATE_NETWORKS=true` relax that for local
development against a receiver on the same machine. Deliveries leave from
the worker, so it needs network egress.

## Log correlation

The server, worker, and migration commands emit structured JSON through the
standard library `slog` API. The HTTP boundary returns `X-Request-ID`, generating
one when the caller does not supply it, and records that value as both
`request_id` and `trace_id`. Authentication, approval, workflow, and decision
operation identifiers are added as they become known.

Temporal propagates the standard correlation fields into workflows and
activities. A decision activity uses the MDC of the HTTP request that submitted
the decision, while notification and expiration use the workflow-start
context. Request-scoped code must use `InfoContext`,
`ErrorContext`, or `LogAttrs` so the handler can read the MDC.

## Deployment notes

The single binary is the deployment. The `bundle` image
(`quay.io/withhuman/bundle`, built from this Dockerfile) runs it as an
unprivileged user with everything persisted under `/data`; mount a volume
there and publish port 8080.

Before deploying beyond a local machine:

1. Put the binary behind an HTTPS reverse proxy and set `WITHHUMAN_PUBLIC_ORIGIN` to its exact origin.
2. Configure `WITHHUMAN_TRUSTED_PROXIES` only for the proxy CIDRs you operate.
3. Set `WITHHUMAN_BOOTSTRAP_SECRET` yourself, or read the generated one from the data directory once and keep it out of logs.
4. Back up the data directory as a whole: the application database, Temporal's databases, and the bootstrap secret live there together.

The hosted edition runs the same server and worker compositions as
separately scaled processes with managed PostgreSQL and a Temporal
cluster; that topology, its Compose stack, and its Kubernetes chart live
in the hosted repository.

## Programmatic management access

Personal and organization API keys use `Authorization: Bearer whk_...` on
supported `/api/v1` operations. Settings > API keys offers personal keys,
organization keys, and member oversight. Personal keys follow the member's
live access; organization keys carry explicit, immutable scoped grants and
survive their creator leaving. Organization keys cannot review decisions,
manage human identities, administer API keys, or use AAP/gateway credentials.

Organization keys are issued by the hosted edition (`POST /api/v1/api_keys`
with a fresh human session, `api_key.organization.issue`, a required
`Idempotency-Key` header, and a body containing `name`, `permission_policies`
and optional `expires_at`; `GET /api/v1/api_keys/options` provides the
caller's grant choices), through core's exported authn service: the secret
is returned once and only its hash is stored, and replaying a committed
creation returns metadata without the secret. The open edition mounts no
issuance route and registers no permission that could reach it, while
resolving, listing and revoking both key kinds stay here.
`GET /api/v1/api_keys/verify` verifies either kind
of bearer. Inventory, detail and revocation require a human session.

Organization policy independently controls `personal_api_keys_allowed` and
`organization_api_keys_allowed`. `WITHHUMAN_API_KEY_MAX_TTL` caps both kinds.
The OpenAPI operation extension `x-withhuman-credential-kinds` lists supported
credentials. New and edition-owned routes deny organization keys by default.
Authenticated key use, creation, revocation and management mutations are audited.

The hosted edition keeps the manual test plan for organization keys with
its issuance routes.

## License

This repository is licensed under the Elastic License 2.0 (see
[`LICENSE`](LICENSE)). The Agent Approval Protocol the platform speaks is a
separate specification under the Apache License 2.0.
