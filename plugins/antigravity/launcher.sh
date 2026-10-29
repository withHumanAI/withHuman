#!/bin/sh
# withHuman for Antigravity: the plugin's PreToolUse hook and its MCP
# gateway relay both run this file.
#
#   sh launcher.sh hook   reads one step on stdin and prints Antigravity's verdict
#   sh launcher.sh mcp    relays the withHuman MCP gateway over stdio
#
# The hook runs the withhuman CLI's `hook antigravity` and prints its
# verdict: {"decision":"ask"} to let Antigravity's own permissions decide,
# or a deny with the reason. `withhuman agent install antigravity` connects
# the plugin by saving the credential the CLI reads; Antigravity gives hooks
# no plugin settings, so that is the only way. Until then the plugin is off
# and every call goes to Antigravity's own permissions.
#
# Antigravity blocks a call when its hook fails, and needs a decision on
# stdout, so every path prints one. The CLI runs the release pinned in
# ../cli.pin, downloaded and checked on first use; a withhuman on PATH is the
# fallback when that fails.

set -u

root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
state="$root/.withhuman"
runtime=antigravity
gateway=withhuman-gateway
downloads="${WITHHUMAN_CLI_BASE:-https://downloads.withhuman.ai}"

fail_closed_text="withHuman could not get an approval decision. The call was not run."
no_cli_text="The withHuman CLI could not be downloaded, so this call was not run. Ask the user to install it: https://withhuman.ai/docs/getting-started/quickstart"

say() { printf '%s\n' "$*" >&2; }
ask() { printf '{"decision":"ask"}\n'; }
deny() { printf '{"decision":"deny","reason":"%s"}\n' "$1"; }

# The CLI's configuration directory, as Go's os.UserConfigDir finds it.
config_dir() {
  if [ -n "${WITHHUMAN_CONFIG_DIR:-}" ]; then
    printf '%s\n' "$WITHHUMAN_CONFIG_DIR"
  elif [ "$(uname -s)" = Darwin ]; then
    printf '%s\n' "$HOME/Library/Application Support/withhuman"
  else
    printf '%s\n' "${XDG_CONFIG_HOME:-$HOME/.config}/withhuman"
  fi
}

connected() { [ -f "$(config_dir)/aap/credentials/$runtime.json" ]; }

pinned() { awk -v name="$1" '$1 == name { print $2; exit }' "$root/plugins/cli.pin"; }

platform() {
  case "$(uname -s)" in Darwin) os=darwin ;; Linux) os=linux ;; *) return 1 ;; esac
  case "$(uname -m)" in x86_64 | amd64) arch=amd64 ;; arm64 | aarch64) arch=arm64 ;; *) return 1 ;; esac
  printf '%s-%s\n' "$os" "$arch"
}

digest() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d' ' -f1
  else
    sha256sum "$1" | cut -d' ' -f1
  fi
}

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --max-time 60 "$1" -o "$2"
  else
    wget -q -T 60 "$1" -O "$2"
  fi
}

# download fetches the pinned release to $1 and checks its checksum.
download() {
  name=$(platform) || return 1
  version=$(pinned version)
  expected=$(pinned "$name")
  [ -n "$version" ] && [ -n "$expected" ] || return 1
  mkdir -p "$(dirname "$1")" || return 1
  # Another hook may be downloading too: write aside, then rename over.
  partial="$1.$$.partial"
  if fetch "$downloads/cli/$version/withhuman-$name" "$partial" && [ "$(digest "$partial")" = "$expected" ]; then
    chmod 0755 "$partial" && mv -f "$partial" "$1"
    return
  fi
  rm -f "$partial"
  return 1
}

# find_cli prints the CLI to run: the pinned copy, else one on PATH.
find_cli() {
  copy="$state/cli/$(pinned version)/withhuman"
  if [ -x "$copy" ] || download "$copy"; then
    printf '%s\n' "$copy"
    return
  fi
  say "withhuman: could not download the CLI"
  command -v withhuman 2>/dev/null
}

# cli_env runs a command with only the variables the CLI needs; with
# --exec as its first argument it replaces this shell.
cli_env() {
  run=env
  if [ "$1" = --exec ]; then
    run="exec env"
    shift
  fi
  for name in SSL_CERT_DIR SSL_CERT_FILE no_proxy NO_PROXY https_proxy HTTPS_PROXY \
    WITHHUMAN_CLI_BASE WITHHUMAN_CONFIG_DIR XDG_CONFIG_HOME TMPDIR TZ LANG USER HOME PATH; do
    eval "set=\${$name+x}"
    if [ -n "$set" ]; then
      eval "value=\$$name"
      set -- "$name=$value" "$@"
    fi
  done
  $run -i "$@"
}

hook() {
  work=$(mktemp -d 2>/dev/null || mktemp -d -t withhuman) || { deny "$fail_closed_text"; return; }
  trap 'rm -rf "$work"' EXIT
  cat >"$work/in"
  if ! connected; then
    ask
    return
  fi
  cli=$(find_cli)
  if [ -z "$cli" ]; then
    deny "$no_cli_text"
    return
  fi
  # A stopped turn stops the CLI, which withdraws the request.
  cli_env "$cli" hook "$runtime" <"$work/in" >"$work/out" &
  pid=$!
  trap 'kill -TERM "$pid" 2>/dev/null' INT TERM HUP
  wait "$pid"
  # A signal interrupts the first wait; the second collects the CLI.
  wait "$pid" 2>/dev/null
  verdict=$(cat "$work/out")
  case "$verdict" in
    '{"decision":"ask"}' | '{"decision":"deny","reason":"'*'"}') printf '%s\n' "$verdict" ;;
    *) deny "$fail_closed_text" ;;
  esac
}

mcp() {
  if ! connected; then
    say "withHuman is not connected. Run: withhuman agent install antigravity"
    return 1
  fi
  cli=$(find_cli)
  if [ -z "$cli" ]; then
    say "$no_cli_text"
    return 1
  fi
  cli_env --exec "$cli" mcp "$runtime" "$gateway"
}

case "${1:-}" in
  hook) hook ;;
  mcp) mcp ;;
  *) say "usage: launcher.sh hook|mcp"; exit 2 ;;
esac
