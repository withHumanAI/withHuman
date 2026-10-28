package agentruntime

import (
	"regexp"
	"strings"
	"testing"
)

var agentTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

func TestCatalogInvariants(t *testing.T) {
	seen := map[Key]bool{}
	for _, runtime := range All() {
		if !agentTypePattern.MatchString(string(runtime.Key)) {
			t.Fatalf("%s is not a valid agent type", runtime.Key)
		}
		if seen[runtime.Key] {
			t.Fatalf("%s listed twice", runtime.Key)
		}
		seen[runtime.Key] = true
		if runtime.DisplayName == "" || runtime.Binary == "" || runtime.HomeDir == "" {
			t.Fatalf("%s is missing display name, binary, or home dir", runtime.Key)
		}
		shell := false
		names := map[string]bool{}
		for _, tool := range runtime.Builtins {
			if names[tool.Name] {
				t.Fatalf("%s lists %s twice", runtime.Key, tool.Name)
			}
			names[tool.Name] = true
			if tool.Kind == ToolShell {
				shell = true
			}
		}
		if !shell {
			t.Fatalf("%s has no shell tool to demonstrate with", runtime.Key)
		}
		if _, ok := Lookup(runtime.Key); !ok {
			t.Fatalf("Lookup(%s) failed", runtime.Key)
		}
	}
	if Valid("cursor") {
		t.Fatal("unknown runtime reported valid")
	}
	if len(Keys()) != len(All()) {
		t.Fatal("Keys and All disagree")
	}
}

func TestRuntimeToolName(t *testing.T) {
	ref := MCPRef{Server: "stripe", Name: "create_refund"}
	cases := map[Key]string{
		ClaudeCode: "mcp__stripe__create_refund",
		Codex:      "mcp__stripe__create_refund",
		OpenClaw:   "stripe__create_refund",
		Hermes:     "mcp__stripe__create_refund",
		DeepSeek:   "mcp__stripe__create_refund",
		Pi:         "create_refund",
	}
	for key, want := range cases {
		if got := RuntimeToolName(key, ref); got != want {
			t.Fatalf("%s: got %q, want %q", key, got, want)
		}
	}
	// Codex sanitises both halves; observed live: server "mcp-server-linkedin".
	if got := RuntimeToolName(Codex, MCPRef{Server: "mcp-server-linkedin", Name: "get_my_profile"}); got != "mcp__mcp_server_linkedin__get_my_profile" {
		t.Fatalf("codex sanitised name: %q", got)
	}
	if got := RuntimeToolName(Codex, MCPRef{Server: "stripe_", Name: "_get.balance"}); got != "mcp__stripe__get_balance" {
		t.Fatalf("codex trimmed and sanitised name: %q", got)
	}
	if got := RuntimeToolName(ClaudeCode, MCPRef{Server: "mcp-server-linkedin", Name: "get_my_profile"}); got != "mcp__mcp-server-linkedin__get_my_profile" {
		t.Fatalf("claude code keeps the server name: %q", got)
	}
}

func TestHookMatcher(t *testing.T) {
	for _, runtime := range All() {
		got := HookMatcher(runtime.Key)
		if runtime.HookMatcher && got != CatchAllMatcher {
			t.Fatalf("%s: got %q, want the catch-all", runtime.Key, got)
		}
		if !runtime.HookMatcher && got != "" {
			t.Fatalf("%s: got %q, want no matcher", runtime.Key, got)
		}
	}
	if HookMatcher("cursor") != "" {
		t.Fatal("unknown runtime should have no matcher")
	}
}

func TestTestCommand(t *testing.T) {
	for _, runtime := range All() {
		for _, tool := range runtime.Builtins {
			command, ok := TestCommand(runtime.Key, SelectedTool{Name: tool.Name, Source: SourceBuiltin, Kind: tool.Kind})
			if !ok || command.Shell == "" {
				t.Fatalf("%s/%s produced no command", runtime.Key, tool.Name)
			}
			if !strings.HasPrefix(command.Shell, runtime.Binary+" ") {
				t.Fatalf("%s command does not start with its binary: %s", runtime.Key, command.Shell)
			}
		}
		command, ok := TestCommand(runtime.Key, SelectedTool{Name: "create_refund", Source: SourceMCP, Server: "stripe"})
		if !ok || !strings.Contains(command.Shell, "create_refund") || !strings.Contains(command.Shell, "stripe") {
			t.Fatalf("%s MCP command is incomplete: %s", runtime.Key, command.Shell)
		}
	}
	command, _ := TestCommand(ClaudeCode, SelectedTool{Name: "Bash", Source: SourceBuiltin, Kind: ToolShell})
	if !strings.Contains(command.Shell, `--allowedTools "Bash"`) {
		t.Fatalf("Claude Code command must allowlist the tool: %s", command.Shell)
	}
	// --allowedTools is variadic, so a prompt placed after it is swallowed as
	// a tool name and print mode reports a missing prompt.
	if !strings.HasSuffix(command.Shell, `--allowedTools "Bash"`) {
		t.Fatalf("Claude Code command must put the prompt before the allowlist: %s", command.Shell)
	}
	command, _ = TestCommand(ClaudeCode, SelectedTool{Name: "create_refund", Source: SourceMCP, Server: "stripe"})
	if !strings.Contains(command.Shell, `--allowedTools "mcp__stripe__create_refund"`) {
		t.Fatalf("Claude Code MCP command must allowlist the runtime name: %s", command.Shell)
	}
	if _, ok := TestCommand("cursor", SelectedTool{Name: "Bash"}); ok {
		t.Fatal("unknown runtime should have no command")
	}
	if got := shellQuote(`say "hi" $HOME`); got != `"say \"hi\" \$HOME"` {
		t.Fatalf("shellQuote: %s", got)
	}
}

func TestHermesHomeFollowsHermesHome(t *testing.T) {
	unset := func(string) string { return "" }
	if got := HermesHome("/home/me", unset); got != "/home/me/.hermes" {
		t.Fatalf("default = %s", got)
	}
	docker := func(key string) string {
		if key == "HERMES_HOME" {
			return "/opt/data"
		}
		return ""
	}
	if got := HermesHome("/home/me", docker); got != "/opt/data" {
		t.Fatalf("HERMES_HOME = %s", got)
	}
}
