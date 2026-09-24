# Guided onboarding: from first sign-in to a decided request

Status: draft · Last updated: 2026-09-24

> **Prototype.** Same caveat as [00-general-design.md](00-general-design.md):
> everything here can change without migration paths.

## Summary

A new organization starts empty: no agents, no pipeline, and no
escalation path. The guided setup takes a
person from their first sign-in to a request they decided themselves, in
one sitting, without leaving the product except to paste one command:

1. Sign in. Members who hold every permission the flow needs land on
   `/welcome` while the organization has not completed onboarding.
2. Install the CLI (`withhuman`) and run the setup command the page shows.
   The command carries a single-use code that ties the CLI to this
   person's session.
3. The CLI detects which agent runtimes are installed (Claude Code, Codex,
   OpenClaw, Pi, Hermes, DeepSeek Harness) and the page shows them.
4. The person picks the runtimes to protect. The CLI discovers each
   chosen runtime's tools in the background: the built-in catalog plus
   every MCP server in the runtime's configuration, listed live, each
   judged by the server (the suggested ones are what the starting rules
   gate).
5. Choose where withHuman reaches them: the page shows their channels
   (the account email is already there), adds and confirms an email,
   text message, or WhatsApp number inline, and explains how Slack,
   Discord, and the phone app arrive. It comes before the agents are
   connected because the escalation path provisioning writes notifies
   them. Continue writes nothing.
6. Decide how each request reaches them: both notification ladders,
   interrupts and standard requests. Together with the step before it
   this is the Notifications settings page inside the flow, on the same
   endpoints; this step's Continue records only that the pair was
   passed (`channels_set_at`), so a reload during either lands back on
   the channels.
7. Choose tools. Arriving here with the channels set and the tools
   discovered provisions the agents: the server writes an escalation
   path that notifies this person, one agent per runtime with a starting
   pipeline that sends the suggested tools to a human and lets the rest
   run; the CLI enrolls one agent instance per
   runtime and installs the hooks, behind a status line under the step's
   question that disappears once they are connected. The step asks how
   the person wants to set up their rules and offers three tiles, none
   chosen at first, each opening its panel below. "With my coding agent"
   shows a prompt to paste into the person's own coding agent: connect
   the hosted MCP server, read the grammar and these agents' rules, ask
   what should wait, write them sending human exits to the setup's
   path, and preview a few calls; the page re-reads the agents'
   pipelines every few seconds while it is open and ticks each one
   whose active revision moved. "Describe it in words" is a box to
   answer in the person's own words ("only kubectl commands that change
   the cluster, and anything that pushes to git") with starters that
   build one sentence. The setup assistant, a model
   holding the management tools the hosted MCP server offers, reads,
   writes, and previews the agents' pipelines and records the test
   prompt for the next step; its answer appears in place with a badge
   saying whether rules changed, the steps folded behind a line, and the
   box stays for the next refinement, with Start over forgetting the
   conversation. "Pick tools myself" is a table per agent: tick the
   tools that should wait, apply, and the agent's rules become that
   list (`PUT /api/v1/onboarding/session/gated-tools`). Without a model
   the describe tile is disabled and the other two still work. Continue opens once
   the hooks are installed and stamps `rules_set_at`.
8. Test it: the page shows a command per runtime that makes it perform
   the call the assistant chose (or, without one, call a suggested tool),
   says where the prompt goes (the standard ladder, since a test request
   carries no urgency), and renders the resulting request inline so the
   person approves or denies it there. Once a loop has closed the way out
   asks whether the prompt reached them: Yes finishes (recording the
   decided request as `tested`, then completing), No returns to the
   channels. Only the arrival is verified; delivery is the person's word.

## Coordination: one shared session

The browser and the CLI never talk to each other. Both read and write one
`onboarding_sessions` row through the server, taking turns:

```
awaiting_cli     --exchange (CLI)---------------> connected
connected        --PUT detections (CLI)---------> detected         (re-PUT allowed in detected)
detected         --PUT agents (human)-----------> agents_selected  (re-PUT allowed in agents_selected, tools_discovered)
agents_selected  --PUT tools (CLI)--------------> tools_discovered (re-PUT allowed in tools_discovered)
tools_discovered --POST provision (human)-------> provisioning     (needs channels_set_at)
provisioning     --POST enroll (CLI)------------> enrolled
enrolled         --PUT install-results (CLI)----> installed
installed        --POST test (human)------------> tested          (the decided request, from an enrolled agent)
tested           --completion finished (human)--> completed
any open         --completion skipped (human), or a new POST /sessions--> skipped
```

The person's own steps that gate nobody but them are stamps, not
statuses: `POST channels` sets `channels_set_at` in any status from
`agents_selected` on (provisioning requires it), `POST rules` sets
`rules_set_at` in `installed` or `tested`, and `POST assistant` runs the
setup assistant in any status from `provisioning` on, once the agents
exist. Passing a step again re-stamps it. The CLI is done at
`installed`; `setup --wait` keeps polling until the session is
terminal, so the person's last steps happen while it waits.

Any write outside the table is 409 `onboarding_session_state`. A session
lives one hour (`WITHHUMAN_ONBOARDING_SESSION_TTL`); after `expires_at`
every write is 410 `onboarding_session_expired`. One open session per
member: starting another supersedes the first as `skipped`.

Every write bumps `version`. The CLI long-polls
`GET /api/onboarding/v1/session?wait=25s&since=<version>` and the browser
holds an SSE stream (`GET /api/v1/onboarding/session/events`), both
driven by the same one-second poll loop the review queue uses.

### The `who_` credential class

The page mints a setup code (`XXXX-XXXX-XXXX`, single use, ten minutes,
stored hashed) and shows it inside the command. The CLI exchanges it for
a `who_<session id>_<secret>` bearer, a credential class disjoint from
human sessions, agent credentials (`whc_`), and provisioner tokens
(`whp_`). It is accepted only by
`/api/onboarding/v1/*`, resolves live to its session, organization
(active) and membership (active), and dies with the session.

### Who writes what

Provisioning (path, pipelines) runs under the person's
browser session with their ordinary permissions and audit attribution.
Agent credentials are the exception: they are handed to the CLI once, so
the CLI-initiated `enroll` call mints them. It does so with a delegated
principal rebuilt from the session's membership at that moment (the
membership must still be active and still hold every onboarding
permission), so `agent.created` and the credential issue are attributed
to the person who started the setup. The raw tokens are never stored.

## Data

`organizations.onboarding_completed_at` marks the organization done
(finished or skipped). `/me` reports
`onboarding: {required, completed_at}` where `required` means the column
is null and the caller holds every `OnboardingPermissions`
(`agent.write`, `agent.credential.issue`, `pipeline.write`, `pipeline.activate`,
`escalation_path.write`, `escalation_path.activate`, `organization.settings.write`, `request.read`,
`request.decide`). Admins lack the last two, so in practice owners run
the setup.

`onboarding_sessions` holds the status, version, hashes, and jsonb
columns for what each side reported. The Go shape is
`domain.OnboardingSession` and its JSON is the wire representation on
both surfaces.

## HTTP contract

Human surface (`/api/v1`, session cookie, same-origin guard; every route
requires `OnboardingPermissions` unless noted):

| Route | Body | Response |
|---|---|---|
| `POST /api/v1/onboarding/sessions` | `{}` | 201 `{"session": S, "setup_code": "ABCD-EFGH-JKLM", "setup_command": "curl -fsSL <cli_download_url>/install.sh \| sh -s -- onboard --url <origin> --code ABCD-EFGH-JKLM"}` (one line: the script installs the CLI and runs `withhuman onboard` with the arguments) |
| `GET /api/v1/onboarding/session` | | 200 S, or 404 `onboarding_session_not_found` |
| `GET /api/v1/onboarding/session/events` | | SSE, `event: session` with S on every version change, `: keepalive` otherwise |
| `PUT /api/v1/onboarding/session/agents` | `{"runtimes": ["claude-code", "codex"]}` | 200 S; 400 `runtime_not_detected` |
| `POST /api/v1/onboarding/session/channels` | `{}` | 200 S with `channels_set_at`, status unchanged; allowed from `agents_selected` on. The channels themselves are the person's contact methods and notification rules under `/api/v1/me`; this records only the step |
| `POST /api/v1/onboarding/session/provision` | `{}` | 200 S in `provisioning` with `generated` filled; 400 `channels_not_set` before the notification steps; on failure S unchanged with `last_error` and 502 `onboarding_provision_failed` |
| `POST /api/v1/onboarding/session/assistant` | `{"message": "..."}` | 200 S with the turn appended to `assistant` and, when the assistant chose one, `test_commands` replaced; allowed from `provisioning` on. 501 `setup_assistant_unconfigured` without a model, 409 `onboarding_assistant_busy` while the previous turn runs, 502 `onboarding_assistant_failed` for a turn recorded as failed |
| `PUT /api/v1/onboarding/session/gated-tools` | `{"runtime": "codex", "tools": ["Bash"]}` | 200 S with `test_commands[runtime]` following the choice; replaces the agent's pipeline with the starting shape over the list (empty list: every call runs); allowed from `provisioning` on. 400 `runtime_not_selected`, `tool_not_discovered` |
| `POST /api/v1/onboarding/session/rules` | `{}` | 200 S with `rules_set_at`, status unchanged; allowed in `installed` and `tested` |
| `POST /api/v1/onboarding/session/test` | `{"request_id": "..."}` | 200 S in `tested` with `test_request_id` and `tested_at`; 400 `test_request_unknown` (not the organization's), `test_request_not_enrolled` (not from an agent this session enrolled), `test_request_pending` (not decided yet) |
| `POST /api/v1/onboarding/completion` | `{"outcome": "finished" \| "skipped"}` | 204. `finished` needs no open session or one in `tested`. Requires only `agent.write` |

Machine surface (`/api/onboarding/v1`, Bearer `who_` except the exchange):

| Route | Body | Response |
|---|---|---|
| `POST /api/onboarding/v1/sessions/exchange` (no auth) | `{"setup_code": "ABCD-EFGH-JKLM", "machine": {"hostname", "os", "arch", "cli_version", "user"}}` | 200 `{"session_id", "token": "who_...", "expires_at", "provider_url", "session": S}`; 404 `setup_code_invalid` |
| `GET /api/onboarding/v1/session?wait=25s&since=<version>` | | 200 S, early when `version > since`, wait capped at 30s |
| `PUT /api/onboarding/v1/session/detections` | `{"runtimes": [Detection × 6]}` | 200 S |
| `PUT /api/onboarding/v1/session/tools` | `{"tools": {"<runtime>": {"builtin": [ReportedTool], "mcp": {"<server>": [ReportedTool]}, "errors": [{"server", "message"}]}}}` | 200 S with `tools[runtime][]` carrying `suggested` and `reason` |
| `POST /api/onboarding/v1/session/enroll` | `{}` | 200 `{"provider_url", "enrollments": [{"runtime", "agent": {"slug", "name"}, "agent_instance": {"id", "name"}, "credential": {"id", "token", "expires_at"}}], "policy": {"tools": [...]}, "hooks": {"<runtime>": {"matcher", "tools": [...]}}, "session": S}`; 409 `already_enrolled` |
| `PUT /api/onboarding/v1/session/install-results` | `{"results": [InstallResult]}` | 200 S in `installed` with `test_commands` |
| `PUT /api/onboarding/v1/session/error` | `{"code", "message"}` | 200 S (`last_error` set, status unchanged) |

`Detection`: `{"runtime", "installed", "version", "binary_path", "config_path", "already_hooked", "mcp_servers": [{"name", "transport", "source"}]}`.
`ReportedTool`: `{"name", "description", "annotations": {"read_only_hint", "destructive_hint", "idempotent_hint", "open_world_hint"}, "input_fields": [...]}`.
`InstallResult`: `{"runtime", "ok", "changes": [{"path", "action"}], "notes": [...], "manual", "message", "snippet"}`.

Session `S` (see `domain.OnboardingSession`): `id, organization_id,
membership_id, status, version, machine, detections[],
selected_runtimes[], tools{runtime: [{name, source, server, description,
kind, annotations, input_schema, suggested, reason}]},
discovery_errors{runtime: [...]}, generated{escalation_path_key,
escalation_path_name, escalation_path_revision, pipelines{runtime:
{revision, replaced_active_revision}}, policy_tools[]}, enrollments[{runtime,
agent_slug, agent_name, agent_instance_id, agent_instance_name,
credential_id}], install_results[], test_commands{runtime: {tool,
command, note}}, test_request_id, tested_at, channels_set_at,
rules_set_at, assistant[{role, text, actions[{tool, summary, ok}],
status, error, at}], last_error, created_at, updated_at, expires_at,
completed_at`. The page counts the test's calls from `rules_set_at`, so
the count survives a reload. It never contains the setup code, the
`who_` secret, an agent credential, or the assistant's model context
(the tool calls and results the model saw), which the row keeps in a
column of its own.

## Suggesting tools

Suggestion is deterministic and server side (`internal/toolsuggest`),
first rule wins:

1. Built-in tools by kind from the `agentruntime` catalog: shell tools are
   suggested (`shell`), edit tools are not by default
   (`edit_not_suggested`), read tools are not (`read_only_builtin`).
2. MCP annotations: `readOnlyHint: true` is not suggested;
   `destructiveHint: true` is (`annotation_destructive`); an explicit
   `readOnlyHint: false` is (`annotation_mutating`).
3. Verbs in the tool name (split on `_ - . /` and camel case), then in the
   first sentence of the description: the first token that is a known
   verb decides, a mutating verb suggests and a read verb does not. Names
   lead with their verb (`list_refunds`, `create_refund`), so position
   beats severity.
4. Otherwise not suggested (`unknown`).

The reason travels with the tool so the page can say why. The
suggested set is what the starting pipeline gates until the person
writes other rules in the rules step.

## The setup assistant

The rules step takes the person's own words. `POST
/api/v1/onboarding/session/assistant` runs one turn of an agent loop in
the server (`internal/onboarding/assistant.go`) on the
`application.ToolUseModel` the edition hands
`runtime.ServerOptions.AssistantModel` (the hosted edition passes the
default model of its judge catalog, the first `WITHHUMAN_LLM_MODELS`
entry, since the repository split moved the model adapters to
`withhuman_saas/judge`); `/api/runtime-config` reports
`setup_assistant_enabled`, and without a model the step shows the
starting rules alone. The prompt's grammar section carries every block
kind the deployment registered, so the hosted assistant can write judge
blocks.

The assistant's tools are the hosted MCP server's own catalog
(`internal/httpapi/mcp_tools.go`), dispatched in process under the
person's resolved principal rather than a bearer, so every write is
authorized, validated, and audited as theirs. It sees only the pipeline
tools (`get_pipeline`, `update_pipeline`, `preview_pipeline`,
`list_pipeline_revisions`, `activate_pipeline_revision`) and the
service refuses any call outside the session's own agents or aimed at
the organization pipeline; `update_pipeline` gets the setup's
escalation path as `default_escalation_path` unless the model named
one. One tool is the setup's own: `set_test_prompt` records, per agent,
the instruction the test step shows, and refuses a sample the active
rules would not hand to a person (the service previews it first), so
the test always produces a request.

The system prompt carries the agents, their discovered tools with
argument names, the escalation path, the working method, and the
pipeline and condition grammar from `internal/mcpreference` (shared with
`read_reference`), so no turn is spent reading it. A turn is bounded
(16 tool calls, 4 minutes), each tool call is appended to the session's
`assistant` transcript as it happens (`running` until the answer, then
`done` or `failed` with the reason), and the model's own context is
persisted separately and trimmed to the last six turns. A running
message blocks the next for five minutes, after which it is assumed
abandoned.

## Generated objects

- Escalation path `notify-<slug of display name>` named "Notify <Display
  Name>", one `level` node targeting the person's membership with
  `escalate_after: 1h`. An existing path with that key whose active
  document already targets the membership is reused; otherwise the key
  gets a numeric suffix.
- One agent per selected runtime, its slug the runtime key (`codex`) and
  its name the runtime's display name ("Codex"), created here so the
  agent's pipeline exists before it is revised (enrollment reuses a live
  agent with that slug). If the slug belongs to an archived agent the
  step fails with `agent_archived` and tells the person to restore the
  agent in the app or choose another slug.
- One starting pipeline revision per selected runtime, in that agent's
  pipeline scope (`agents/<slug>`): block `gate-selected-tools`, named
  "Review tools chosen in setup" (`/request/tool in [suggested...]` →
  human, escalation path above) then `approve-the-rest`, named "Approve
  every other tool" (`/request/tool exists` → approve), default
  escalation path set to the same path. Names are verb-first like every
  other block because the product quotes them inside sentences ("Review
  tools chosen in setup" asked for a human). A second machine unions the
  gated list with the active revision's; onboarding never removes a
  tool. The assistant then replaces the revision with the person's own
  rules.
- One agent per runtime (slug the runtime key, reused when present) with
  one instance named after the machine's hostname.
- Per runtime a hook plan: the catch-all matcher (`.*`) for runtimes
  whose hook registration takes one, plus the suggested tool names. Every
  call reaches the hook and is sent to the provider; the pipeline decides
  which wait for a human, so the gated set changes centrally and the
  runtime's own spelling of tool
  names (Codex sanitises server and tool names to `[A-Za-z0-9_]`, so
  `mcp-server-linkedin` fires as `mcp__mcp_server_linkedin__<tool>`) only
  matters for the test command's allowlist, which `agentruntime` still
  reproduces.

## The CLI

`withhuman` lives at `withhuman_core/cmd/withhuman-cli` (binary name
`withhuman`), built from `internal/cli`, which never imports the
application layer, `authn`, `internal/postgres`, or `internal/httpapi`;
it shares `domain` (wire types) and `agentruntime` (catalog) with the
server.

```text
withhuman onboard --url <origin> --code <code> [--binary <path>] [--wait] [--json]
withhuman agent discover [--json]
withhuman agent install <runtime> [--url <origin>] [--agent <slug>] [--instance <name>]
withhuman agent eject <runtime> [--yes]
withhuman hook <runtime>
withhuman version
```

The public AAP library owns runtime detection, installation, status, uninstall
and hook execution. withHuman retains runtime tool discovery for the wizard.
No separate `aap` binary is required. The adapter package is pinned at
`v0.0.0-20260917124045-380732b9b7b7`, without a module replacement.

Enrollment records are per runtime in `<config>/credentials/<runtime>.json`
with mode 0600. `<config>` is `WITHHUMAN_CONFIG_DIR` or the operating system
user config directory plus `withhuman`. The library stores its state under
`<config>/aap`. Generated hooks pin that directory and this executable's
absolute path, then call the internal `hook <runtime>` command.

`onboard` runs: exchange, detect, report, wait for runtime selection,
discover tools, report, wait for provisioning, enroll, save credentials,
install adapters, report and print test commands. Library results preserve
changed files and restart or consent notes. Incomplete registration fails
and remains repairable or ejectable.

`agent install` validates one runtime, resolves the provider from `--url`,
`WITHHUMAN_URL`, saved enrollment or the hosted default, then authorizes in
the browser. `--agent <slug>` names an existing agent by slug; without it the
slug defaults to the runtime key and the authorization page shows it. The
saved credential records `agent_slug`, `agent_name`, `instance_name` and
`runtime`. It provisions no pipelines. A saved unexpired credential is
reused after authentication succeeds. An unavailable provider stops the run;
an expired or rejected credential starts browser authorization again.

All intercepted tools are sent to `<origin>/api/aap`. The provider's pipeline
decides which need human review. Eject is local; remote revocation is on the
website. There is no automatic migration of old local integrations and no
compatibility aliases for removed commands.

## Distribution

The release workflow cross-compiles the CLI for darwin and linux on amd64
and arm64 and uploads `install.sh`, `cli/<version>/withhuman-<os>-<arch>`
with `SHA256SUMS`, and a `cli/latest/` mirror to a Cloudflare R2 bucket
served at `downloads.withhuman.ai`. The server reports the base URL as
`cli_download_url` in `/api/runtime-config`
(`WITHHUMAN_CLI_DOWNLOAD_URL`), so self-hosters can point at their own
mirror. Until a release exists the page also shows the `go build` line
when the server version is `dev`.

## Open questions

- Codex's trust hash is reproduced rather than obtained through its
  review prompt; a Codex release that changes the hash input silently
  leaves the hook untrusted until the CLI catches up. Managed hooks
  (`requirements.toml`) are the policy-trusted alternative for fleets.
- OpenClaw, Pi, Hermes and dsh one-shot prompt flags and MCP config keys
  are unverified; the templates live in `agentruntime` so a fix is one
  edit.
- Setup codes are 60 bits, single use and short lived, but core has no
  request rate limiter; a per-address limiter on the exchange route is a
  follow-up.
- Admins cannot decide requests, so they cannot run the flow. Granting
  admins `request.read` and `request.decide`, or letting the last step
  name a reviewer, are the options.

## Appendix: adapter findings kept from the spike

The spike (`aap/spike`, deleted when the CLI moved into core) recorded
these provider-side findings while integrating each runtime:

- **No claim endpoint.** Single-use approval consumption from design doc
  01 has no provider support, so a re-attached approval passes again
  without a fresh decision. The provider needs claim (or equivalent)
  before gating is trustworthy against replay.
- **Re-attach is permanent.** Request ids derive from the idempotency key
  and the key never expires, so a denied request re-attaches as denied
  forever. Safe direction, but the retry-after-deny reviewer signal is
  lost.
- **Agent reasoning is often empty on the first gated call.** The
  transcript has no assistant text yet. By design: reasoning is optional
  and unverified, and the reviewer UI renders the action from the
  arguments.
- Verified live: Claude Code approve and deny paths with the boundary
  wording holding; Codex PreToolUse and PermissionRequest; the DeepSeek
  bridge fails open on a missing binary (setup-time verification is the
  fix) while OpenClaw and Pi fail closed; Hermes composes hook
  modifications in registration order so withHuman must be registered
  last; the MCP wrapper's progress keepalive works because Claude Code
  sends a `progressToken`.
