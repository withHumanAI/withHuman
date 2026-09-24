# AAP adapters in withHuman

Status: implemented · Last updated: 2026-09-17

## Summary

withHuman imports `github.com/agentapprovalprotocol/agentapprovalprotocol/adapters`
at `v0.0.0-20260917124045-380732b9b7b7`. The library owns detection,
installation, status, uninstall, runtime hook handlers, plugin assets and
the approval client. withHuman owns browser enrollment and provider configuration,
plus tool discovery and provisioning for guided onboarding.

There are no copied installers, hook implementations or plugin assets in
withHuman. Hooks invoke the absolute path of the installing withHuman binary,
which delegates `hook <runtime>` to the library. A separate AAP executable is
not required. The complete AAP URL is `<provider-origin>/api/aap`.

## The shared core

`withhuman onboard --url <origin> --code <code>` keeps the browser-guided
flow. `agent discover` reports local runtime and installation state without
provider calls or MCP tool discovery. `agent install <runtime>` obtains a
credential through browser authorization and uses existing provider pipelines.
`agent eject <runtime>` removes the recorded local integration and credentials.
Remote revocation remains in the website.

The provider resolves from `--url`, `WITHHUMAN_URL`, a saved enrollment, then
`https://app.withhuman.ai`. Fresh instances get a hostname plus a random suffix.
Saved unexpired credentials are reused after an authenticated check. Rejected
or expired credentials require browser authorization; connection errors stop.

Enrollment records live in `<withhuman-config>/credentials` with mode 0600.
The library keeps its state in `<withhuman-config>/aap`, pinned explicitly in
hook commands. Reinstallation updates credentials without duplicating hooks.
Changed-file reports and runtime restart or consent notes are preserved.
Incomplete registration is a failure, with records retained for repair or eject.

Every intercepted call goes to the provider: withHuman supplies no tool glob.
The shared library normalizes runtime tool names, snapshots arguments, creates
a request, polls synchronously within the runtime's timeout margin and cancels
on abandonment. Stable attempt IDs retain idempotency keys through retries;
without an attempt ID each invocation creates a fresh request.

AAP responses contain only protocol fields. Submitted timeout spelling is
stored independently of the accepted deadline. Approved decisions receive
`expires_at = decided_at + 5 minutes` once at storage, including human,
automatic and resumed pipeline decisions. Reads and retries never extend it.
The library checks identity, arguments, status, decision consistency and expiry,
and atomically consumes an approval before granting permission. Malformed
responses, provider failure or local storage failure block the call.

## Claude Code

The library installs a `PreToolUse` command hook in user settings. Approval
continues through Claude Code's own permission checks. The runtime must load
the hook and must not disable it. Other hooks or tool paths outside that seam
are not secured by the adapter.

## Codex

The library installs hook definitions and their trust state in Codex's
configuration. Pre-tool approval preserves local permission checks; a
`PermissionRequest` decision replaces the local prompt. Restart or trust
changes may be required after binary or configuration changes.

## DeepSeek Harness

The library uses DeepSeek's Claude Code hook bridge through the Cordis profile.
Its review applies to the submitted arguments; native guards still run after
approval. The profile and bridge must be loaded by the runtime.

## OpenClaw

An embedded AAP plugin registers on `before_tool_call`. It snapshots arguments
and rejects invalid, expired or unavailable decisions. Later plugins can weaken
enforcement if the runtime lets them modify arguments or override a block.
JSON5 configuration may require the manual steps printed by installation;
that installation remains incomplete until registration is repaired.

## Pi

An embedded AAP extension registers through `pi install` and intercepts
`tool_call`. Start a fresh Pi session after installation. The extension's
review applies to the saved arguments; enforcement relies on Pi loading it
and respecting its block result. Eject needs the Pi executable to unregister it.

## Hermes

The library installs the last `pre_tool_call` hook with fail-closed behavior
and a callback timeout that contains its approval window. Accept the printed
one-time shell hook consent in Hermes. The approved argument snapshot is
returned to override earlier hook modifications; AAP must remain last.

## MCP, guardrail mode: the local wrapper

The `mcp wrap` command is removed. It is not part of this integration.
The separately deployed gateway remains supported and is regression-tested
against the protocol's metadata-free responses.

## MCP, chokepoint mode: the hosted gateway

The gateway is the same gating logic, exposed as a remote
streamable-HTTP MCP server that aggregates an organization's registered
downstream servers and holds their credentials. Built and shipped in
withHuman core as `withhuman-gateway`. Differences from the wrapper:

- **A separate process that speaks AAP.** An earlier draft had the
  gateway enqueue directly into the provider's queue with no network
  hop. That is superseded: it proxies tool execution and holds
  downstream credentials, so it gets its own process and its own
  security and availability boundary (doc 03), and it reaches the
  provider only over HTTP. The hop is worth the isolation, and it keeps
  the gateway honest as a first-class AAP client.
- **One gateway, every organization.** The gateway is deployment
  infrastructure, not something an organization runs: one process
  serves every organization of the provider it is paired with. It
  authenticates to the provider's machine configuration surface with
  the deployment's gateway token (a shared secret both processes are
  started with; the single binary generates its own), and every call on
  that surface names the organization it concerns. Which organization a
  session belongs to is learned from the calling host's credential, so
  a host can only ever see, and be gated by, its own organization's
  servers. Each organization's registry and downstream sessions are
  loaded on first use and released after sitting idle.
- **The calling host's own credential is forwarded.** An MCP host
  authenticates to the gateway with its ordinary instance-bound
  credential, and the gateway presents that same credential on the AAP
  calls it makes on the host's behalf. Attribution therefore stays
  truthful ("claude-code on chris's laptop", not "the gateway"), and a
  revoked host cannot get anything approved. The gateway token reads
  configuration and downstream secrets and can never create approval
  requests.
- **Credential custody is the point.** The downstream credential is
  stored encrypted by the provider, issued to the gateway per call, and
  never reaches an agent machine. Because bypassing the gateway means
  not having the key, enforcement is real rather than advisory (doc
  00's Bash problem does not apply). The provider owns OAuth refresh, so
  refresh tokens and client secrets never leave it either.
- **Tool identity is namespaced.** Aggregated tools are advertised as
  `<server-slug>__<tool>`; slugs never contain underscores, so the
  split is unambiguous. The request carries the AAP tool identity (the
  spec's "Tool Identity" section): `tool` is the name the downstream
  server defines and `server` is the gateway slug. The gateway is the
  first place where trusted server identity is actually available: a
  pipeline condition on `/request/server` can be scoped to one
  registered server rather than to a bare tool name, and only here is
  `server` more than a label the adapter observed.
- **Every call is a request.** The gateway classifies nothing itself:
  like the hooks, it creates a request for every call the agent's tool
  access allows and acts on the pipeline's verdict (decided 2026-09-24;
  the per-server gated-tool list and the adapter policy it used to
  consult are gone). A call the pipeline decides on its own comes back
  terminal on create, so only a human decision waits.
- Same progress-notification keepalive toward the calling host.
- Same fail-closed stance: an unreachable provider means the call does
  not run.

**Scope of the first version.** Tools only: `tools/list` and
`tools/call`. Resources, prompts, sampling, and elicitation are not
proxied, because none of them is needed for gating and each needs its
own passthrough design. Sessions are held in memory, which answers the
old open question for now by making the gateway single-replica; session
affinity or a shared store is the scaling step when it is needed.

## Rollout

Deploy the compatible provider and database migration before distributing the
new CLI. Historical decision expiry is backfilled from the recorded decision
time, never the migration time. There is no automatic migration of existing
local installations, no deployment and no release publishing in this change.

For CLI commands, storage details and private-module build credentials, see
[the CLI guide](../withhuman_core/deployment/cli/README.md).
