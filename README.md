<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/readme/logo-dark.svg">
    <img src=".github/readme/logo-light.svg" alt="withHuman" width="320">
  </picture>
</p>

<p align="center"><strong>The approval layer for AI agents.</strong><br>
Put your most consequential agent actions behind human approvals, so your
agents can do real work.</p>

<p align="center">
  <a href="https://github.com/withHumanAI/withHuman/tags"><img src="https://img.shields.io/github/v/tag/withHumanAI/withHuman?sort=semver&label=release" alt="Latest release"></a>
  <a href="https://github.com/withHumanAI/withHuman/actions/workflows/release.yml"><img src="https://github.com/withHumanAI/withHuman/actions/workflows/release.yml/badge.svg" alt="Release build"></a>
  <a href="https://quay.io/repository/withhuman/bundle"><img src="https://img.shields.io/badge/docker-quay.io%2Fwithhuman%2Fbundle-2496ED?logo=docker&logoColor=white" alt="Docker image"></a>
  <a href="go.mod"><img src="https://img.shields.io/github/go-mod/go-version/withHumanAI/withHuman" alt="Go version"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Elastic%202.0-blue" alt="License: Elastic 2.0"></a>
  <a href="https://agentapprovalprotocol.io"><img src="https://img.shields.io/badge/protocol-AAP-6c47ff" alt="Agent Approval Protocol"></a>
  <a href="https://x.com/withHumanAI"><img src="https://img.shields.io/badge/follow-%40withHumanAI-000000?logo=x&logoColor=white" alt="Follow @withHumanAI on X"></a>
</p>

<p align="center">
  <a href="https://withhuman.ai">Website</a> ·
  <a href="https://withhuman.ai/docs">Docs</a> ·
  <a href="#quickstart">Quickstart</a> ·
  <a href="https://withhuman.ai/docs/concepts/how-withhuman-works">How it works</a> ·
  <a href="CONTRIBUTING.md">Contributing</a>
</p>

---

withHuman gives your whole organization one place to oversee what its AI
agents do. Every agent's tool calls come to withHuman before they run.
Automated policies approve or deny the routine ones. Escalation policies
send the rest to the right team or person, who decides in a review queue
the whole team shares.

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/readme/request-dark.png">
    <img src=".github/readme/request-light.png" alt="A request in withHuman: an agent wants to call issue_refund for 75 USD, with its arguments, its explanation, and Deny and Approve buttons" width="720">
  </picture>
</p>

## Everything you need to let agents do real work

### Every tool call, whatever the agent

Each tool call is intercepted through the
[Agent Approval Protocol](https://agentapprovalprotocol.io) and sent to
withHuman for approval. If the agent supports AAP, withHuman just works.

- Hooks for Claude Code, Codex, OpenClaw, Pi, Hermes and DeepSeek,
  [connected with one command](https://withhuman.ai/docs/web-app/agents)
- The open Agent Approval Protocol for your own agents

### No approval fatigue

Write policy rules that approve or deny routine requests automatically.
Only send the tough stuff to humans. Read more about
[approval pipelines](https://withhuman.ai/docs/concepts/approval-pipelines).

- Conditions on agent, tool, arguments and agent context
- Webhooks to your own policy service

### The right human, every time

Write rules to decide which requests go to which team or person. Read
more about [escalation paths](https://withhuman.ai/docs/concepts/escalation-paths).

- Route by agent, tool, arguments or team
- Teams set their own escalation order and on-call flows
- Escalate when nobody answers in time

### Meet reviewers where they are

Reviewers get requests where they already work, so it fits into their
workflow.

- [Email](https://withhuman.ai/docs/web-app/email-notifications),
  [Slack](https://withhuman.ai/docs/web-app/slack-notifications) and
  [Discord](https://withhuman.ai/docs/web-app/discord-notifications)
- [Notification rules](https://withhuman.ai/docs/web-app/notifications) for standard and urgent
  requests

### One queue for your whole team

Everyone in your organization reviews requests in one shared
[review queue](https://withhuman.ai/docs/web-app/review-queue).

- Reviewers see the tool call and request context to decide
- Claim a request so other reviewers stand down, or add a reviewer
- [Teams](https://withhuman.ai/docs/web-app/team-administration) and
  [permissions](https://withhuman.ai/docs/web-app/permissions) decide who can review what

### Run it yourself

One binary with its database and workflow engine built in. Start it with
Docker or from source, with no other services.

## How it works

Follow a consequential action. A support agent refunds a $75 duplicate
charge. Refunds over $20 need a human.

```mermaid
sequenceDiagram
    participant Agent as Support agent
    participant WH as withHuman
    participant Team as Payments team
    Agent->>WH: issue_refund(amount: 75)
    Note over WH: Your approval pipeline checks it
    WH->>Team: Notifies whoever is on call
    Team->>WH: Approve
    WH->>Agent: The call runs
```

1. **Your agent's tool call is intercepted.** The refund is held by
   withHuman before it reaches Stripe.
2. **Your rules check it.** Your approval pipeline approves or denies the
   call automatically or asks a human to take a look.
3. **The request gets sent to a human.** Refunds go to the Payments team,
   to whoever is on call.
4. **The human gets notified** on their preferred platform.
5. **The human decides.** Reviewers see the tool call and request context
   to decide.
6. **The call runs.** The approved refund reaches Stripe and the agent
   carries on.

Learn the concepts behind it:

- [How withHuman works](https://withhuman.ai/docs/concepts/how-withhuman-works): the three core
  concepts and how they fit together.
- [Agents](https://withhuman.ai/docs/concepts/agents): what agents and instances are.
- [Approval pipelines](https://withhuman.ai/docs/concepts/approval-pipelines): how a request
  gets its decision.
- [Escalation paths](https://withhuman.ai/docs/concepts/escalation-paths): who reviews a request,
  when they're contacted, and what happens if nobody decides.
- [Requests and decisions](https://withhuman.ai/docs/concepts/requests-and-decisions): what
  happens next for the agent.

## Quickstart

You need [Docker](https://docs.docker.com/get-docker/).

**1. Start withHuman**

```sh
docker run -d --name withhuman -p 8080:8080 -v withhuman-data:/data \
  quay.io/withhuman/bundle:latest
```

**2. Create your account**

Print the one-time setup secret:

```sh
docker exec withhuman cat /data/bootstrap-secret
```

Open [http://localhost:8080](http://localhost:8080), choose **First
setup**, and enter the secret.

**3. Connect your agent**

The welcome page walks you through connecting your first agent and choosing
which calls need your approval. It ends with a test call for you to approve.

To connect another machine later, install the CLI there and point it at
your server:

```sh
curl -fsSL https://downloads.withhuman.ai/install.sh | sh
withhuman agent install --url https://approvals.example.com
```

The [quickstart guide](https://withhuman.ai/docs/getting-started/quickstart)
covers each step in detail, and the [CLI guide](deployment/cli/README.md)
covers every command.

<details>
<summary><strong>Run from source instead</strong></summary>

You need Go (the version in [`go.mod`](go.mod)) and Node 25.

```sh
make frontend
go run ./cmd/withhuman
```

State lives in `~/.withhuman` (set `--data-dir` or `WITHHUMAN_DATA_DIR` to
move it), and the setup secret is in `~/.withhuman/bootstrap-secret`. The
first start downloads PostgreSQL (about 32 MB); later starts work offline.

</details>

## Let your coding agent set it up

withHuman publishes [agent skills](skills/) that teach a coding agent how
to connect runtimes and write approval pipelines for you:

```sh
npx skills add https://withhuman.ai
```

## Configuration

Settings are environment variables. [`.env.example`](.env.example) lists
them for `docker run --env-file`. Nothing is required to try withHuman on
your own machine.

<details>
<summary><strong>All settings</strong></summary>

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

Interactive CLI commands check `downloads.withhuman.ai` for a newer CLI
once a day. Set `WITHHUMAN_NO_UPDATE_CHECK=1` to turn that off.

</details>

## Deploy to a server

<details>
<summary><strong>Checklist before you go beyond your own machine</strong></summary>

1. Put it behind an HTTPS reverse proxy and set `WITHHUMAN_PUBLIC_ORIGIN`
   to that origin.
2. Set `WITHHUMAN_TRUSTED_PROXIES` to your proxy's addresses only.
3. Set `WITHHUMAN_SECRETS_KEY` and keep it safe. Losing it makes stored
   webhook and Slack secrets unreadable.
4. Configure SMTP so invitations and password resets reach people.
5. Back up the whole data directory. It holds the database, the workflow
   state and the setup secret together.

</details>

## Hosted edition

Don't want to run it yourself? [withhuman.ai](https://withhuman.ai) runs
it for you, and adds:

- **Every decision on the record:** an append-only audit log with the
  reviewer and the full request kept with each decision
- **An AI judge** for calls rules can't describe
- **More places to reach reviewers:** text message, WhatsApp, and iPhone
  and Android apps with push
- The MCP gateway, organization API keys, and single sign-on with
  directory sync

## Contributing

Bug reports, ideas and pull requests are welcome. Read
[CONTRIBUTING.md](CONTRIBUTING.md) to get set up, and follow our
[code of conduct](CODE_OF_CONDUCT.md). Report security vulnerabilities
privately as described in [SECURITY.md](SECURITY.md).

<details>
<summary><strong>Development commands and code layout</strong></summary>

```sh
make test        # build the frontend, then run Go tests and vet
make verify      # also validate the OpenAPI contract and deployment files
make frontend    # rebuild the React assets embedded in the binary
make smoke       # run one real approval against a running server
```

`webapp/assets` is generated and not committed, so use the Make targets
rather than plain `go test` or `go build` from a clean checkout. The smoke
test signs in as `owner@example.com` with `local-development-password`
(set `WITHHUMAN_SMOKE_PASSWORD` to change it). The HTTP API contract is
served at `/api/openapi.yaml` and lives in
[`internal/httpapi/openapi.yaml`](internal/httpapi/openapi.yaml).

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
workflow-start outbox item. Temporal coordinates human approvals, deadlines
and retries, and is never used for policy evaluation or as the read model:
API reads, the queue stream and long polls read PostgreSQL only. The
architecture rules live in [CLAUDE.md](CLAUDE.md).

</details>

## License

withHuman core is licensed under the [Elastic License 2.0](LICENSE). In
short, you may use, modify and self-host it, but you may not provide it to
others as a hosted or managed service or remove its licensing notices. The
license text has the exact terms. The Agent Approval Protocol
specification and its Go library are licensed separately under the Apache
License 2.0.

withHuman and the withHuman logo are trademarks of Metoro, Inc. The
license covers the code, not the name or logo. Don't use them in a way
that suggests your product or service is withHuman or is endorsed by us.
