# Distributing the withhuman CLI

The `withhuman` command (`cmd/withhuman-cli`) is a standalone binary a
person installs on the machine their agents run on. The release workflow
cross-compiles it and uploads it, with an install script, to a Cloudflare
R2 bucket served at `https://downloads.withhuman.ai`. The server tells the
setup page where to fetch it from (`cli_download_url` in
`/api/runtime-config`, set with `WITHHUMAN_CLI_DOWNLOAD_URL`), so a
self-hosted deployment can point at its own mirror.

## Object layout

```
install.sh                                  the installer (max-age 300)
cli/<version>/withhuman-darwin-amd64        one binary per platform (immutable)
cli/<version>/withhuman-darwin-arm64
cli/<version>/withhuman-linux-amd64
cli/<version>/withhuman-linux-arm64
cli/<version>/SHA256SUMS                    checksums for the four binaries
cli/<version>/LICENSE                       withHuman's licence
cli/<version>/THIRD_PARTY_NOTICES           licences of the code the binaries compile in
cli/latest/...                              a copy of the newest version dir (max-age 300)
cli/latest/version                          the version string the copy came from
```

`<version>` is the tag without its `v`. Versioned objects are uploaded with
`Cache-Control: public, max-age=31536000, immutable`; `latest/` and
`install.sh` with `public, max-age=300` so a new release is picked up
within minutes.

## Installing

```sh
curl -fsSL https://downloads.withhuman.ai/install.sh | sh
```

The script maps `uname` to one of the four builds, downloads the binary and
`SHA256SUMS`, verifies the checksum, and installs to `/usr/local/bin` (with
sudo when it is not writable) or else `~/.local/bin` with a PATH hint. It
refuses to overwrite a `withhuman` binary that answers `--help` with
`--data-dir`, which is the server's single binary, not the CLI.

`WITHHUMAN_CLI_VERSION=0.1.52` pins a version; `WITHHUMAN_CLI_BASE` points
at a mirror. `WITHHUMAN_CLI_INSTALL_DIR` chooses an installation directory.
`go build -o withhuman ./cmd/withhuman-cli` builds the same binary from
source.

Arguments after `sh -s --` run an explicit CLI command after installation:

```sh
curl -fsSL https://downloads.withhuman.ai/install.sh | sh -s -- agent install --agent support-assistant --url https://app.withhuman.ai
```

The installer reconnects terminal input so runtime selection works through
the pipe. Without a terminal, pass a runtime after `agent install` if more
than one is installed. Guided onboarding uses `sh -s -- onboard --url
<origin> --code <code>`. With no arguments the script only installs the CLI.

When using a mirror, set `WITHHUMAN_CLI_BASE` on the `sh` side of the pipe
so the installer downloads the binary and checksums from that mirror too.
Release the installer and the product's generated commands together: the
installer no longer adds an implicit `onboard` command.

## Using the CLI

### Updating

Install the latest published CLI with:

```sh
withhuman upgrade
withhuman version
```

Upgrade checks the download's checksum and version before replacing the CLI at
its current location. If administrator access is needed, it prompts through
sudo in an interactive terminal. A noninteractive command prints the command
to rerun with administrator access. Failed downloads or verification leave
the installed CLI in place. An equal or newer installed release is kept.

Your agent connections and installed plugin files stay in place. To refresh
a runtime's plugin files separately, run `withhuman agent install <runtime>`
and follow any restart instructions. That command reuses a valid connection.

Interactive agent and onboarding commands show an update notice when a newer
release is available. Checks are cached for 24 hours, stop after one second,
and do not prevent commands from running when the release server is unavailable.
Hooks, JSON output and noninteractive commands do not check for updates.
Set `WITHHUMAN_NO_UPDATE_CHECK=1` to turn notices off. You can still run
`withhuman upgrade` explicitly.

For a self-hosted download mirror, set `WITHHUMAN_CLI_BASE` to its HTTPS base
URL for both upgrades and notices. Upgrade always selects that source's latest
release; `WITHHUMAN_CLI_VERSION` applies only to the shell installer.
Development builds can upgrade explicitly but do not show update notices.

If your CLI does not recognize `upgrade`, rerun the installation command above
once to get a version that supports it.

### Connecting runtimes

Start from the welcome page for guided setup. It discovers runtime tools,
lets you select tools and notifications in the browser, provisions pipelines,
and prints a command that runs `withhuman onboard` after downloading the CLI.
The direct command is:

```sh
withhuman onboard --url https://app.withhuman.ai --code <one-time-code>
```

For an organization whose pipelines already exist:

```sh
withhuman agent discover --json
withhuman agent install
withhuman agent install claude-code
withhuman agent install codex --agent coding-agents --instance codex-laptop
withhuman agent install codex --url https://approvals.example.com --instance workstation
withhuman agent eject claude-code --yes
withhuman upgrade
withhuman version
```

Omit the runtime to detect installed runtimes locally. If several are found,
choose one in the terminal; one installation connects one runtime. With no
terminal input, specify the runtime explicitly. Runtime names are `claude-code`,
`codex`, `openclaw`, `pi`, `hermes` and `deepseek`.

`agent install` opens the browser so you can choose an existing agent or create
one. Each instance uses its parent agent's approval pipeline. For example,
Claude Code on a desktop and Codex on a laptop can both join `coding-agents`.
Use `--agent coding-agents` to select that existing agent by name and go straight
to browser confirmation. A missing or unavailable agent is an error; the flag
never creates one automatically. Sign into the organization containing the agent.

The CLI retrieves the instance credential automatically. If the browser cannot
open, visit the printed address and enter the code. Codes expire after ten
minutes; run the command again if yours expires.

A saved, unexpired connection is reused after an authenticated provider check,
including when `--agent` names its current parent. Omitting `--agent` also keeps
a working connection. A different agent or instance name starts a new browser
connection. Expired or rejected credentials require authorization again.
Connection failures stop without enrolling another instance. The default
instance name is the hostname plus a random suffix.

Provider selection is `--url`, `WITHHUMAN_URL`, the saved enrollment, then
`https://app.withhuman.ai`. Supply an origin, without `/api/aap`. withHuman
passes the complete `<origin>/api/aap` URL to the library. Every intercepted
call reaches the provider; pipeline rules determine whether human review is needed.

A provider can offer MCP servers to every agent (`agent_mcp_servers` in its
`/api/runtime-config`; the hosted edition offers its MCP gateway). Both
`onboard` and `agent install` read that list and register each server with
the runtime as a stdio MCP server that runs `withhuman mcp <runtime>
<server>`: the CLI's own relay, which reads the instance credential from
the credential store and forwards the runtime's frames to the endpoint
with it. The credential is never written into a runtime's configuration.
Claude Code (`~/.claude.json`), Codex (`~/.codex/config.toml`), Hermes
(`~/.hermes/config.yaml`) and OpenClaw (`openclaw.json`, when it is strict
JSON) are edited in place; DeepSeek Harness gets a snippet to paste and Pi
has no MCP client. The saved enrollment records the servers so
`agent eject` removes the entries again, and tool discovery leaves them
out: their tools are the provider's own catalogue. Restart the runtime
after installing so it starts the relay.

`agent discover` checks local runtime presence and installation status. It
never contacts a provider or discovers MCP tools. `onboard` retains MCP tool
discovery, the browser selection flow, provisioning and test instructions.
`--binary` selects the absolute executable used by onboarding; `--wait`
waits for browser completion and `--json` emits progress events.

Enrollment records live in `<withhuman-config>/credentials` with mode 0600.
`<withhuman-config>` is `WITHHUMAN_CONFIG_DIR` or the operating system user
configuration directory plus `withhuman`. Adapter credentials, installation
records and plugin assets live in its `aap` subdirectory. Generated hooks
pin that directory using `AAP_CONFIG_DIR` and invoke the absolute path of
this withHuman executable. Keep the installed executable at that path.
Standalone AAP configuration is separate.

Installation prints changed paths (hook files and MCP server entries),
runtime restart requirements and consent steps. An incomplete registration returns failure and retains state for
repair or `agent eject`. Eject removes only recorded local integration and
credentials, preserving unrelated settings and later user edits. Revoke
remote instance credentials from the website.

## Building and releasing

The CLI embeds the Agent Approval Protocol adapter library
(`github.com/agentapprovalprotocol/agentapprovalprotocol`), pinned in
`go.mod`. Build it with:

```sh
go build -o /tmp/withhuman ./cmd/withhuman-cli
sh scripts/check-cli-builds.sh
```

Until that module is public, builds need read access to it: run them
through `sh scripts/private-go-modules.sh` with `AAP_MODULE_TOKEN` set.

The `cli` job in `.github/workflows/release.yml` builds darwin and linux on
amd64 and arm64 with
`CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=<version>"`,
writes `SHA256SUMS`, mirrors the version directory to `cli/latest/`, and
uploads everything to `downloads.withhuman.ai`.

## Pointing a self-hosted server at a mirror

Copy the object layout above to any HTTPS host (the same bucket layout on
another R2 account, an S3 bucket behind CloudFront, a static file server)
and set `WITHHUMAN_CLI_DOWNLOAD_URL=https://downloads.example.com` on the
server. The setup page then prints
`curl -fsSL https://downloads.example.com/install.sh | sh`, and the script
downloads from that base. Set `WITHHUMAN_CLI_BASE` when running the script
by hand against a mirror the page does not know about.
