package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/withHumanAI/withHuman/domain"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentapprovalprotocol/agentapprovalprotocol/adapters"
	"github.com/withHumanAI/withHuman/internal/cli/credential"
	"github.com/withHumanAI/withHuman/internal/cli/installation"
)

func TestAgentEnrollmentAndRepair(t *testing.T) {
	for _, scenario := range []string{"fresh", "reuse", "expired", "rejected", "unavailable", "cancelled", "partial", "same-agent", "different-agent", "browser-unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			home, config := t.TempDir(), t.TempDir()
			mustWrite(t, filepath.Join(home, ".claude", "settings.json"), `{"userChoice":true}`, 0600)
			begins, probes, opens := 0, 0, 0
			var origin string
			// The flag names an existing agent by slug; the provider answers
			// with the agent's slug and display name.
			requestedAgent, agentName := "coding-agents", "Coding agents"
			if scenario == "different-agent" {
				requestedAgent, agentName = "other-agent", "Other agent"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/runtime-config":
					json.NewEncoder(w).Encode(map[string]any{"public_origin": origin, "agent_mcp_servers": []map[string]any{{"name": "withhuman", "url": origin + "/gateway/mcp", "label": "withHuman MCP gateway"}}})
				case "/api/v1/agent_instances/self/tool_catalog":
					var report domain.ToolCatalogReport
					if err := json.NewDecoder(r.Body).Decode(&report); err != nil || report.Validate() != nil || report.Runtime != "claude-code" {
						t.Errorf("invalid catalogue report: %+v, %v", report, err)
					}
					w.WriteHeader(http.StatusNoContent)
				case "/api/v1/agent_instances/self":
					probes++
					if scenario == "rejected" {
						w.WriteHeader(401)
					} else if scenario == "unavailable" {
						w.WriteHeader(503)
					} else {
						w.Write([]byte(`{"agent_slug":"coding-agents","agent_instance_id":"instance"}`))
					}
				case "/auth/agent_enrollments":
					begins++
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					if body["runtime"] != "claude-code" {
						t.Errorf("missing runtime: %v", body)
					}
					if scenario == "different-agent" && body["requested_agent_slug"] != "other-agent" {
						t.Errorf("missing target: %v", body)
					}
					if name, _ := body["instance_name"].(string); len(name) < 9 {
						t.Errorf("unique default instance missing: %v", body)
					}
					json.NewEncoder(w).Encode(map[string]any{"enrollment_code": "one-time-secret", "user_code": "ABCD-EFGH", "verification_uri": origin + "/connect/authorize", "expires_at": time.Now().Add(time.Minute)})
				case "/auth/agent_enrollments/token":
					json.NewEncoder(w).Encode(map[string]any{"agent": map[string]any{"slug": requestedAgent, "name": agentName}, "agent_instance": map[string]any{"id": "instance", "agent_slug": requestedAgent, "name": "machine"}, "credential": map[string]any{"id": "credential", "token": "whc_new_secret", "expires_at": time.Now().Add(time.Hour)}})
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			origin = server.URL
			if scenario != "fresh" && scenario != "cancelled" && scenario != "partial" && scenario != "browser-unavailable" {
				expires := time.Now().Add(time.Hour)
				if scenario == "expired" {
					expires = time.Now().Add(-time.Hour)
				}
				if _, err := credential.Save(config, "claude-code", credential.Credential{ProviderURL: origin, Token: "whc_old_secret", ExpiresAt: expires, AgentSlug: "coding-agents", AgentName: "Coding agents", Runtime: "claude-code"}); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "partial" {
				mustWrite(t, filepath.Join(home, ".claude", "settings.json"), `{broken`, 0600)
			}
			var stdout, stderr bytes.Buffer
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a := &app{configDir: config, stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr, getenv: func(string) string { return "" }, env: adapters.Environment{Home: home, Executable: "/test/withhuman", LookPath: func(string) (string, error) { return "", os.ErrNotExist }}, pollWait: time.Millisecond, openBrowser: func(_ context.Context, url string) error {
				opens++
				if url != origin+"/connect/authorize?user_code=ABCD-EFGH" {
					t.Error(url)
				}
				if scenario == "browser-unavailable" {
					return errors.New("no browser")
				}
				if scenario == "cancelled" {
					cancel()
				}
				return nil
			}}
			args := []string{"agent", "install", "claude-code", "--url", origin}
			if scenario == "fresh" {
				args = []string{"agent", "install", "--url", origin}
			}
			if scenario == "same-agent" || scenario == "different-agent" {
				args = append(args, "--agent", requestedAgent)
			}
			code, err := a.run(ctx, args)
			shouldFail := scenario == "unavailable" || scenario == "cancelled" || scenario == "partial"
			if (err != nil) != shouldFail || (code != 0) != shouldFail {
				t.Fatalf("exit=%d err=%v output=%s", code, err, &stdout)
			}
			if scenario == "reuse" || scenario == "unavailable" || scenario == "same-agent" {
				if probes != 1 || begins != 0 || opens != 0 {
					t.Fatalf("unexpected new enrollment: probes=%d begins=%d opens=%d", probes, begins, opens)
				}
			} else if begins != 1 || opens != 1 {
				t.Fatalf("browser enrollment missing: %d %d", begins, opens)
			}
			if strings.Contains(stdout.String()+stderr.String(), "whc_") || strings.Contains(stdout.String(), "one-time-secret") {
				t.Fatal("secret leaked")
			}
			if err == nil && begins == 1 {
				saved, err := credential.Load(config, "claude-code")
				if err != nil || saved.AgentSlug != requestedAgent || saved.AgentName != agentName || saved.Runtime != "claude-code" {
					t.Fatalf("lost selected identity: %+v %v", saved, err)
				}
			}
			if err == nil {
				// The provider's gateway is registered as the CLI's relay, and the
				// credential records it for eject.
				claudeJSON, readErr := os.ReadFile(filepath.Join(home, ".claude.json"))
				if readErr != nil || !strings.Contains(string(claudeJSON), `"command": "/test/withhuman"`) || !strings.Contains(string(claudeJSON), `"claude-code",`) || strings.Contains(string(claudeJSON), "whc_") {
					t.Fatalf("relay not registered: %s %v", claudeJSON, readErr)
				}
				if saved, _ := credential.Load(config, "claude-code"); saved == nil || len(saved.MCPServers) != 1 || saved.MCPServers[0].Name != "withhuman" {
					t.Fatalf("credential does not record the server: %+v", saved)
				}
			}
			if scenario == "fresh" {
				if code, err := a.run(ctx, []string{"agent", "eject", "claude-code", "--yes"}); err != nil || code != 0 {
					t.Fatalf("eject: %d %v", code, err)
				}
				if claudeJSON, _ := os.ReadFile(filepath.Join(home, ".claude.json")); strings.Contains(string(claudeJSON), "withhuman") {
					t.Fatalf("relay left after eject: %s", claudeJSON)
				}
			}
			if scenario == "partial" {
				if _, err := credential.Load(config, "claude-code"); err != nil {
					t.Fatalf("interrupted install lost enrollment: %v", err)
				}
				mustWrite(t, filepath.Join(home, ".claude", "settings.json"), `{"userChoice":true}`, 0600)
				if _, err := a.run(ctx, []string{"agent", "install", "claude-code"}); err != nil {
					t.Fatal(err)
				}
				if begins != 1 {
					t.Fatal("repair created another enrollment")
				}
			}
		})
	}
}

func TestRemovedCommandsAndLocalDiscovery(t *testing.T) {
	a := &app{configDir: t.TempDir(), stdin: strings.NewReader("no\n"), stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, getenv: func(string) string { return "" }, env: adapters.Environment{Home: t.TempDir(), LookPath: func(string) (string, error) { return "", os.ErrNotExist }}, http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("discovery contacted provider")
		return nil, errors.New("unexpected network")
	})}}
	for _, args := range [][]string{{"setup"}, {"init"}, {"status"}, {"eject"}, {"mcp", "wrap"}} {
		if code, _ := a.run(context.Background(), args); code != 2 {
			t.Errorf("old command still accepted: %v", args)
		}
	}
	if code, err := a.run(context.Background(), []string{"agent", "discover", "--json"}); code != 0 || err != nil {
		t.Fatal(code, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProviderPrecedence(t *testing.T) {
	for _, tc := range []struct{ name, flag, env, saved, want string }{
		{"flag", "https://flag.example", "https://env.example", "https://saved.example", "https://flag.example"},
		{"env", "", "https://env.example", "https://saved.example", "https://env.example"},
		{"saved", "", "", "https://saved.example", "https://saved.example"},
		{"hosted", "", "", "", defaultProviderURL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, config := t.TempDir(), t.TempDir()
			mustWrite(t, filepath.Join(home, ".claude", "settings.json"), `{}`, 0600)
			if tc.saved != "" {
				credential.Save(config, "claude-code", credential.Credential{ProviderURL: tc.saved, Token: "expired", ExpiresAt: time.Now().Add(-time.Hour)})
			}
			called := false
			a := &app{configDir: config, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, getenv: func(string) string { return tc.env }, env: adapters.Environment{Home: home, LookPath: func(string) (string, error) { return "", os.ErrNotExist }}, http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				if got := r.URL.Scheme + "://" + r.URL.Host; got != tc.want {
					t.Errorf("origin %s, want %s", got, tc.want)
				}
				return nil, errors.New("stop before enrollment")
			})}}
			args := []string{"agent", "install", "claude-code"}
			if tc.flag != "" {
				args = append(args, "--url", tc.flag)
			}
			a.run(context.Background(), args)
			if !called {
				t.Fatal("no provider request")
			}
		})
	}
}

func TestInstallRuntimeDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name     string
		runtimes []string
		input    string
		want     string
		fail     bool
	}{
		{"none", nil, "", "", true},
		{"single", []string{"claude-code"}, "", "claude-code", false},
		{"multiple", []string{"claude-code", "codex"}, "2\n", "codex", false},
		{"cancel", []string{"claude-code", "codex"}, "q\n", "", true},
		{"eof", []string{"claude-code", "codex"}, "", "", true},
		{"invalid", []string{"claude-code", "codex"}, "9\n", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, config := t.TempDir(), t.TempDir()
			for _, key := range tc.runtimes {
				file := ".claude/settings.json"
				if key == "codex" {
					file = ".codex/config.toml"
				}
				mustWrite(t, filepath.Join(home, file), "", 0600)
			}
			var out bytes.Buffer
			a := &app{configDir: config, stdin: strings.NewReader(tc.input), stdout: &out, stderr: &out, getenv: func(string) string { return "" }, env: adapters.Environment{Home: home, LookPath: func(string) (string, error) { return "", os.ErrNotExist }}}
			manager, err := installation.New(config, a.env)
			if err != nil {
				t.Fatal(err)
			}
			got, err := a.chooseRuntime(context.Background(), manager)
			if (err != nil) != tc.fail || got != tc.want {
				t.Fatalf("runtime=%s err=%v output=%s", got, err, &out)
			}
			if tc.name == "multiple" {
				file, err := os.CreateTemp(t.TempDir(), "stdin")
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				a.stdin = file
				if _, err := a.chooseRuntime(context.Background(), manager); err == nil || !strings.Contains(err.Error(), "terminal input") {
					t.Fatalf("non-terminal: %v", err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if _, err := a.chooseRuntime(ctx, manager); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
		})
	}
}
