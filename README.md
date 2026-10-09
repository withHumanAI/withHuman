# withHuman

withHuman is the approval layer for AI agents. It intercepts an agent's
tool calls and decides, through approval pipelines and human reviewers,
which run automatically, which are refused and which wait for a person to
approve them.

This repository is withHuman core: the `withhuman` server (one binary with
the web app, its database and its workflow engine built in), the
`withhuman` command-line tool that connects agent runtimes, and the agent
skills. It is source available under the Elastic License 2.0. A hosted
edition runs at [withhuman.ai](https://withhuman.ai).

Documentation: [withhuman.ai/docs](https://withhuman.ai/docs).

## Run it

### With Docker

```sh
docker run -d --name withhuman -p 8080:8080 -v withhuman-data:/data \
  quay.io/withhuman/bundle:latest
docker exec withhuman cat /data/bootstrap-secret
```

Open [http://localhost:8080](http://localhost:8080), choose **First
setup**, and enter the bootstrap secret to create the first owner.

### From source

Requires Go (the version in `go.mod`) and Node 25.

```sh
make frontend
go run ./cmd/withhuman
```

State lives in `~/.withhuman` (set `--data-dir` or `WITHHUMAN_DATA_DIR` to
move it), and the bootstrap secret is in `~/.withhuman/bootstrap-secret`.
The first start downloads the PostgreSQL binaries (about 32 MB); later
starts are offline.

## Connect an agent

After you sign in, the welcome page walks you through connecting your
first agent and prints the command to run on the agent's machine. To
connect another machine later, install the CLI there and point it at your
server:

```sh
curl -fsSL https://downloads.withhuman.ai/install.sh | sh
withhuman agent install --url https://approvals.example.com
```

The CLI supports Claude Code, Codex, OpenClaw, Pi, Hermes and DeepSeek.
The [CLI guide](deployment/cli/README.md) covers its commands and
self-hosted download mirrors. Interactive commands check
`downloads.withhuman.ai` for a newer CLI once a day; set
`WITHHUMAN_NO_UPDATE_CHECK=1` to turn that off.

## Configuration

Settings are environment variables. [`.env.example`](.env.example) lists
them for `docker run --env-file`.

| Variable | Purpose |
|---|---|
| `WITHHUMAN_PUBLIC_ORIGIN` | The exact origin people use to reach the server, such as `https://approvals.example.com`. Default `http://localhost:8080`. |
| `WITHHUMAN_HTTP_ADDRESS` | Listen address. Default `:8080`. |
| `WITHHUMAN_DATA_DIR` | Where all state lives. Default `~/.withhuman`, or `/data` in the image. |
| `WITHHUMAN_BOOTSTRAP_SECRET` | The secret for creating the first owner. Generated into the data directory when unset. |
| `WITHHUMAN_TRUSTED_PROXIES` | CIDRs of reverse proxies whose forwarded headers to trust. |
| `WITHHUMAN_SECRETS_KEY` | Encrypts stored secrets. Required for webhook blocks, Slack and asynchronous agent replies. |
| `WITHHUMAN_SMTP_HOST`, `_PORT`, `_USERNAME`, `_PASSWORD`, `_FROM` | Email delivery for invitations, password resets and notifications. |
| `WITHHUMAN_SLACK_APP_ID`, `_CLIENT_ID`, `_CLIENT_SECRET`, `_SIGNING_SECRET` | Slack notifications. See [deployment/slack](deployment/slack). |
| `WITHHUMAN_DISCORD_CLIENT_ID`, `_CLIENT_SECRET`, `_BOT_TOKEN` | Discord notifications. See [deployment/discord](deployment/discord). |

Generate a secrets key with:

```sh
openssl rand -base64 32 | tr '+/' '-_' | tr -d '='
```

## Deploy

Before running the server beyond your own machine:

1. Put it behind an HTTPS reverse proxy and set `WITHHUMAN_PUBLIC_ORIGIN`
   to that origin.
2. Set `WITHHUMAN_TRUSTED_PROXIES` to your proxy's addresses only.
3. Set `WITHHUMAN_SECRETS_KEY` and keep it safe. Losing it makes stored
   webhook and Slack secrets unreadable.
4. Configure SMTP so invitations and password resets reach people.
5. Back up the whole data directory. It holds the database, the workflow
   state and the bootstrap secret together.

## Hosted edition

The hosted edition adds features that live outside this repository: the
audit log, the AI judge block, the MCP gateway and gateway tunnels, text
message, WhatsApp and phone app notifications, organization API keys,
single sign-on with directory sync, and billing. Core exposes the
extension points they use (the `runtime` package and the registries in
`frontend/src/app/extensions.tsx`).

## Development

```sh
make test        # generate the frontend, then run Go tests and vet
make verify      # also validate the OpenAPI contract and deployment files
make frontend    # rebuild the React assets embedded in the binary
make smoke       # run one real approval against a running server
```

`webapp/assets` is generated and not committed, so use the Make targets
rather than plain `go test` or `go build` from a clean checkout. The smoke
test bootstraps or signs in as `owner@example.com` with
`local-development-password` (set `WITHHUMAN_SMOKE_PASSWORD` to change it).

The HTTP API contract is served at `/api/openapi.yaml` and lives in
[`internal/httpapi/openapi.yaml`](internal/httpapi/openapi.yaml).

Until the Agent Approval Protocol module is public, building the CLI needs
read access to it; see `scripts/private-go-modules.sh`.

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
sealbox/                     AES-GCM sealing for secrets at rest
authz/                       the permission checks every service uses
internal/httpapi/            Gin boundary and embedded frontend
frontend/                    React web application
db/migrations/               application-owned migrations
deployment/temporal/         Temporal-owned migration and namespace setup
```

The server evaluates the active pipeline synchronously, then atomically
commits the request, its state, any automatic decision and a pending-only
workflow-start outbox item. Automatic decisions return without a Temporal
call. Temporal coordinates human approvals, deadlines and retries, and is
never used for policy evaluation or as the read model: API reads, the queue
stream and long polls read PostgreSQL only.

The server and worker log structured JSON through `slog`. Every HTTP
response carries `X-Request-ID`, recorded as `request_id` and `trace_id`,
and Temporal carries those fields into workflows and activities.

## License

withHuman core is licensed under the [Elastic License 2.0](LICENSE). In
short, you may use, modify and self-host it, but you may not provide it to
others as a hosted or managed service or remove its licensing notices. The
license text has the exact terms. The Agent Approval Protocol
specification and its Go library are licensed separately under the Apache
License 2.0.
