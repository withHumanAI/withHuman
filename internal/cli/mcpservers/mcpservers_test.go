package mcpservers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/withHumanAI/withHuman/agentruntime"
	"github.com/withHumanAI/withHuman/domain"
)

var gateway = domain.AgentMCPServer{Name: "withhuman", URL: "https://app.withhuman.ai/gateway/mcp", Label: "withHuman MCP gateway"}

func testEnv(t *testing.T) Env {
	t.Helper()
	return Env{Home: t.TempDir(), Binary: "/opt/withhuman/bin/withhuman", ConfigDir: "/home/me/.config/withhuman", Getenv: func(string) string { return "" }}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Every runtime with an editable MCP client gets the relay written once,
// unchanged on a second install, and taken out again by Remove with the
// rest of the file left as it was.
func TestInstallAndRemoveRelayEntries(t *testing.T) {
	for _, tc := range []struct {
		runtime agentruntime.Key
		path    string
		before  string
		want    []string
	}{
		{
			agentruntime.ClaudeCode, ".claude.json",
			"{\n  \"numStartups\": 3,\n  \"mcpServers\": {\n    \"crm\": {\n      \"type\": \"http\",\n      \"url\": \"https://crm.example.com/mcp\"\n    }\n  }\n}\n",
			[]string{`"withhuman": {`, `"type": "stdio"`, `"command": "/opt/withhuman/bin/withhuman"`, `"mcp",`, `"claude-code",`, `"WITHHUMAN_CONFIG_DIR": "/home/me/.config/withhuman"`, `"numStartups": 3`},
		},
		{
			agentruntime.Codex, ".codex/config.toml",
			"model = \"o3\"\n\n[mcp_servers.crm]\nurl = \"https://crm.example.com/mcp\"\n",
			[]string{"[mcp_servers.withhuman]", `command = '/opt/withhuman/bin/withhuman'`, `args = ['mcp', 'codex', 'withhuman']`, `WITHHUMAN_CONFIG_DIR = '/home/me/.config/withhuman'`, "model = 'o3'", "[mcp_servers.crm]"},
		},
		{
			agentruntime.Hermes, ".hermes/config.yaml",
			"# Hermes\nmodel: hermes-4\nmcp_servers:\n  crm:\n    url: https://crm.example.com/mcp\n",
			[]string{"# Hermes\nmodel: hermes-4\n", "  withhuman:\n    command: /opt/withhuman/bin/withhuman\n    args:\n      - mcp\n      - hermes\n      - withhuman\n    env:\n      WITHHUMAN_CONFIG_DIR: /home/me/.config/withhuman\n", "  crm:\n    url: https://crm.example.com/mcp\n"},
		},
		{
			agentruntime.OpenClaw, ".openclaw/openclaw.json",
			"{\n  \"gateway\": {\n    \"port\": 18789\n  }\n}\n",
			[]string{`"withhuman": {`, `"command": "/opt/withhuman/bin/withhuman"`, `"openclaw",`, `"port": 18789`},
		},
	} {
		t.Run(string(tc.runtime), func(t *testing.T) {
			env := testEnv(t)
			path := filepath.Join(env.Home, tc.path)
			write(t, path, tc.before)
			reg := Registration{Runtime: tc.runtime, Servers: []domain.AgentMCPServer{gateway}}
			result, err := Install(env, reg)
			if err != nil {
				t.Fatalf("install: %v", err)
			}
			if len(result.Changes) != 1 || result.Changes[0].Path != path || result.Changes[0].Action != "modified" || result.Manual {
				t.Fatalf("result = %+v", result)
			}
			got := read(t, path)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("%s lacks %q:\n%s", tc.path, want, got)
				}
			}
			if tc.runtime == agentruntime.OpenClaw && (len(result.Notes) != 1 || !strings.Contains(result.Notes[0], "Restart the OpenClaw Gateway")) {
				t.Fatalf("openclaw notes = %v", result.Notes)
			}
			again, err := Install(env, reg)
			if err != nil || len(again.Changes) != 0 {
				t.Fatalf("second install = %+v, %v", again, err)
			}
			if read(t, path) != got {
				t.Fatal("second install rewrote the file")
			}
			removed, err := Remove(env, reg)
			if err != nil || len(removed.Changes) != 1 {
				t.Fatalf("remove = %+v, %v", removed, err)
			}
			after := read(t, path)
			if strings.Contains(after, "withhuman") {
				t.Fatalf("relay left behind:\n%s", after)
			}
			if !strings.Contains(after, "crm") && tc.runtime != agentruntime.OpenClaw {
				t.Fatalf("the person's own server was lost:\n%s", after)
			}
			if tc.runtime == agentruntime.ClaudeCode && after != tc.before {
				t.Fatalf("claude-code file not restored:\n%s", after)
			}
			if tc.runtime == agentruntime.Hermes && after != tc.before {
				t.Fatalf("hermes file not restored:\n%s", after)
			}
		})
	}
}

// A missing file is created around the entry.
func TestInstallCreatesMissingFiles(t *testing.T) {
	env := testEnv(t)
	reg := Registration{Runtime: agentruntime.ClaudeCode, Servers: []domain.AgentMCPServer{gateway}}
	result, err := Install(env, reg)
	if err != nil || len(result.Changes) != 1 || result.Changes[0].Action != "created" {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if got := read(t, filepath.Join(env.Home, ".claude.json")); !strings.HasPrefix(got, "{\n  \"mcpServers\": {\n    \"withhuman\": {") {
		t.Fatalf("created file:\n%s", got)
	}
}

// An entry the person repointed is theirs: Remove leaves it alone.
func TestRemoveLeavesRepointedEntries(t *testing.T) {
	env := testEnv(t)
	path := filepath.Join(env.Home, ".claude.json")
	write(t, path, `{"mcpServers":{"withhuman":{"type":"http","url":"https://elsewhere.example/mcp"}}}`)
	removed, err := Remove(env, Registration{Runtime: agentruntime.ClaudeCode, Servers: []domain.AgentMCPServer{gateway}})
	if err != nil || len(removed.Changes) != 0 {
		t.Fatalf("remove = %+v, %v", removed, err)
	}
}

// OpenClaw's JSON5 and DeepSeek's unverified profile get a snippet to
// paste; Pi has nothing to register.
func TestManualRuntimes(t *testing.T) {
	env := testEnv(t)
	write(t, filepath.Join(env.Home, ".openclaw", "openclaw.json"), "{\n  // comments make this JSON5\n  \"gateway\": {}\n}\n")
	servers := []domain.AgentMCPServer{gateway}
	openclaw, err := Install(env, Registration{Runtime: agentruntime.OpenClaw, Servers: servers})
	if err != nil || !openclaw.Manual || len(openclaw.Changes) != 0 || !strings.Contains(openclaw.Message, "not strict JSON") || !strings.Contains(openclaw.Snippet, `"openclaw"`) {
		t.Fatalf("openclaw = %+v, %v", openclaw, err)
	}
	removed, err := Remove(env, Registration{Runtime: agentruntime.OpenClaw, Servers: servers})
	if err != nil || len(removed.Notes) != 1 || !strings.Contains(removed.Notes[0], "could not be parsed") {
		t.Fatalf("openclaw remove = %+v, %v", removed, err)
	}
	deepseek, err := Install(env, Registration{Runtime: agentruntime.DeepSeek, Servers: servers})
	if err != nil || !deepseek.Manual || !strings.Contains(deepseek.Message, filepath.Join(env.Home, ".dsh", "profiles", "default")) || !strings.Contains(deepseek.Snippet, `"deepseek"`) {
		t.Fatalf("deepseek = %+v, %v", deepseek, err)
	}
	pi, err := Install(env, Registration{Runtime: agentruntime.Pi, Servers: servers})
	if err != nil || pi.Manual || len(pi.Changes) != 0 || len(pi.Notes) != 1 || !strings.Contains(pi.Notes[0], "no MCP client") {
		t.Fatalf("pi = %+v, %v", pi, err)
	}
	if none, err := Install(env, Registration{Runtime: agentruntime.ClaudeCode}); err != nil || len(none.Changes) != 0 {
		t.Fatalf("no servers = %+v, %v", none, err)
	}
}

func TestIsRelay(t *testing.T) {
	if !IsRelay("/usr/local/bin/withhuman", Args(agentruntime.Codex, "withhuman")) {
		t.Fatal("the relay must be recognised")
	}
	if IsRelay("/usr/local/bin/other", []string{"mcp", "codex", "withhuman"}) || IsRelay("/usr/local/bin/withhuman", []string{"hook", "codex"}) {
		t.Fatal("other commands must not be")
	}
}
