package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/withHumanAI/withHuman/domain"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentapprovalprotocol/agentapprovalprotocol/adapters"
	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/internal/cli/credential"
)

// Exercise the shipping executable, including its pinned hook dispatch. Runtime
// executables are fixtures; no real user home or runtime is touched.
func TestExecutableAdapters(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "withhuman")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/withhuman-cli")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	for _, info := range adapters.All() {
		t.Run(info.Key, func(t *testing.T) {
			home, config := t.TempDir(), t.TempDir()
			bin := filepath.Join(home, "bin")
			mustWrite(t, filepath.Join(bin, "pi"), "#!/bin/sh\nexit 0\n", 0700)
			fixture := filepath.Join("discovery", "testdata", info.Key, "before")
			if err := filepath.WalkDir(fixture, func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				rel, _ := filepath.Rel(fixture, path)
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				mustWrite(t, filepath.Join(home, rel), string(raw), 0600)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			token := "whc_fixture_initial_secret"
			requests := map[string]map[string]any{}
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Path == "/api/runtime-config" {
					json.NewEncoder(w).Encode(map[string]any{"public_origin": "http://" + r.Host, "agent_mcp_servers": []map[string]any{{"name": "withhuman", "url": "http://" + r.Host + "/gateway/mcp", "label": "withHuman MCP gateway"}}})
					return
				}
				if r.Header.Get("Authorization") != "Bearer "+token {
					w.WriteHeader(401)
					return
				}
				if r.URL.Path == "/api/v1/agent_instances/self/tool_catalog" {
					var report domain.ToolCatalogReport
					if err := json.NewDecoder(r.Body).Decode(&report); err != nil || report.Validate() != nil || report.Runtime != info.Key {
						t.Errorf("invalid catalogue report: %+v, %v", report, err)
					}
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.URL.Path == "/api/v1/agent_instances/self" {
					w.Write([]byte(`{"agent_slug":"fixture","agent_instance_id":"instance"}`))
					return
				}
				if r.Method != "POST" || r.URL.Path != "/api/aap/v1/requests" {
					t.Errorf("unexpected provider call: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
					return
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					w.WriteHeader(400)
					return
				}
				wantServer := "payments"
				if info.Key == "pi" {
					wantServer = ""
				}
				if body["tool"] != "create_refund" || (body["server"] == nil && wantServer != "") || (body["server"] != nil && body["server"] != wantServer) {
					t.Errorf("tool identity: tool=%v server=%v", body["tool"], body["server"])
				}
				args, _ := body["arguments"].(map[string]any)
				if args["amount"] != float64(42) {
					t.Errorf("arguments: %v", args)
				}
				key := r.Header.Get("Idempotency-Key")
				if existing := requests[key]; existing != nil {
					json.NewEncoder(w).Encode(existing)
					return
				}
				now := time.Now().UTC()
				timeout, _ := time.ParseDuration(body["timeout"].(string))
				body["id"], body["status"], body["created_at"], body["deadline_at"] = uuid.NewString(), "approved", now, now.Add(timeout)
				body["decision"] = map[string]any{"status": "approved", "decided_at": now, "expires_at": now.Add(5 * time.Minute)}
				requests[key] = body
				w.WriteHeader(201)
				json.NewEncoder(w).Encode(body)
			}))
			defer provider.Close()
			save := func(value string) {
				_, err := credential.Save(config, info.Key, credential.Credential{ProviderURL: provider.URL, Runtime: info.Key, Token: value, InstanceName: "test-instance", ExpiresAt: time.Now().Add(time.Hour)})
				if err != nil {
					t.Fatal(err)
				}
			}
			save(token)
			run := func(input string, args ...string) (string, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, args...)
				withhumanConfig := config
				if len(args) > 0 && args[0] == "hook" {
					withhumanConfig = filepath.Join(home, "other-config")
				}
				cmd.Env = []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin", "WITHHUMAN_CONFIG_DIR=" + withhumanConfig, "AAP_CONFIG_DIR=" + filepath.Join(config, "aap"), "XDG_CONFIG_HOME=" + filepath.Join(home, "xdg")}
				cmd.Stdin = strings.NewReader(input)
				out, err := cmd.CombinedOutput()
				if strings.Contains(string(out), "whc_fixture_") {
					t.Error("credential leaked to output")
				}
				return string(out), err
			}
			for i := 0; i < 2; i++ {
				if out, err := run("", "agent", "install", info.Key); err != nil {
					t.Fatalf("install %d: %v\n%s", i, err, out)
				}
			}
			out, err := run("", "agent", "discover", "--json")
			if err != nil {
				t.Fatalf("discover: %v %s", err, out)
			}
			var statuses []adapters.Status
			if json.Unmarshal([]byte(out), &statuses) != nil {
				t.Fatalf("discover JSON: %s", out)
			}
			found := false
			for _, s := range statuses {
				if s.Runtime == info.Key {
					found = s.Complete && s.ToolGlob == "" && s.BaseURL == provider.URL+"/api/aap"
				}
			}
			if !found {
				t.Fatalf("installation not complete: %s", out)
			}
			mu.Lock()
			token = "whc_fixture_replacement_secret"
			mu.Unlock()
			save(token)
			if out, err = run("", "agent", "install", info.Key); err != nil {
				t.Fatalf("replace: %v %s", err, out)
			}
			payload := hookPayload(info.Key, "attempt-1")
			out, err = run(payload, "hook", info.Key)
			if err != nil || hookDenied(out) {
				t.Fatalf("hook: %v %s", err, out)
			}
			if info.Key == "hermes" && !strings.Contains(out, `"amount":42`) {
				t.Fatalf("argument snapshot lost: %s", out)
			}
			out, _ = run(payload, "hook", info.Key)
			if !hookDenied(out) {
				t.Fatalf("approval replay allowed: %s", out)
			}
			out, _ = run(`{}`, "hook", info.Key)
			if !hookDenied(out) {
				t.Fatalf("malformed call allowed: %s", out)
			}
			// Hooks and plugin config must name this executable and state directory;
			// bearer credentials may appear only in restricted credential files.
			pinned := false
			for _, root := range []string{home, config} {
				if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if d.IsDir() {
						return nil
					}
					raw, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					if bytes.Contains(raw, []byte(binary)) && bytes.Contains(raw, []byte(filepath.Join(config, "aap"))) {
						pinned = true
					}
					if bytes.Contains(raw, []byte("whc_fixture_")) {
						st, _ := d.Info()
						if !strings.Contains(path, "credentials") || st.Mode().Perm() != 0600 {
							t.Errorf("credential outside private storage: %s", path)
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			if !pinned {
				t.Fatal("generated integration does not pin executable and state directory")
			}
			// The provider's gateway is registered as this executable's relay on
			// every runtime with an editable MCP client, and recorded for eject.
			relayRuntimes := map[string]bool{"claude-code": true, "codex": true, "openclaw": true, "hermes": true}
			if hasRelay(t, home) != relayRuntimes[info.Key] {
				t.Fatalf("relay registered = %v, want %v", hasRelay(t, home), relayRuntimes[info.Key])
			}
			if saved, err := credential.Load(config, info.Key); err != nil || len(saved.MCPServers) != 1 {
				t.Fatalf("credential does not record the server: %+v %v", saved, err)
			}
			// An edit made after installing must survive removal.
			userFile := editRuntimeConfig(t, home, config, info.Key)
			if out, err = run("", "agent", "eject", info.Key, "--yes"); err != nil {
				t.Fatalf("eject: %v %s", err, out)
			}
			if _, err = credential.Load(config, info.Key); !errors.Is(err, credential.ErrNotFound) {
				t.Fatalf("enrollment not removed: %v", err)
			}
			if _, err = os.Stat(filepath.Join(config, "aap", "credentials", info.Key+".json")); !os.IsNotExist(err) {
				t.Fatalf("AAP credential not removed: %v", err)
			}
			if data, err := os.ReadFile(userFile); err != nil || !bytes.Contains(data, []byte("preserve_after_install")) {
				t.Fatal("user edit lost")
			}
			if hasRelay(t, home) {
				t.Fatal("relay left after eject")
			}
			// Registration failure must not become success just because credentials
			// were acquired. Every runtime can still be ejected after a failure.
			save(token)
			if info.Key == "pi" {
				mustWrite(t, filepath.Join(bin, "pi"), "#!/bin/sh\n[ \"${1:-}\" != install ]\n", 0700)
			} else {
				mustWrite(t, userFile, "{ invalid configuration", 0600)
			}
			if out, err = run("", "agent", "install", info.Key); err == nil {
				t.Fatalf("partial registration reported success: %s", out)
			}
			if out, err = run("", "agent", "install", info.Key); err == nil {
				t.Fatalf("retry hid incomplete registration: %s", out)
			}
			if _, err = credential.Load(config, info.Key); err != nil {
				t.Fatalf("repair credential lost: %v", err)
			}
			if out, err = run("", "agent", "eject", info.Key, "--yes"); err != nil {
				t.Fatalf("partial eject: %v %s", err, out)
			}
		})
	}
}

// hasRelay reports whether any file under home runs `withhuman mcp`.
func hasRelay(t *testing.T, home string) bool {
	t.Helper()
	found := false
	if err := filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, []byte("WITHHUMAN_CONFIG_DIR")) {
			found = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return found
}

func editRuntimeConfig(t *testing.T, home, config, runtime string) string {
	t.Helper()
	paths := map[string]string{
		"claude-code": filepath.Join(home, ".claude", "settings.json"),
		"codex":       filepath.Join(home, ".codex", "config.toml"),
		"openclaw":    filepath.Join(home, ".openclaw", "openclaw.json"),
		"hermes":      filepath.Join(home, ".hermes", "config.yaml"),
		"deepseek":    filepath.Join(home, ".dsh", "profiles", "default", "cordis.patch.yml"),
		"pi":          filepath.Join(config, "aap", "plugins", "pi", "index.js"),
	}
	path := paths[runtime]
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	switch runtime {
	case "claude-code", "openclaw":
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		doc["preserve_after_install"] = true
		raw, _ = json.MarshalIndent(doc, "", "  ")
	case "codex":
		raw = append(raw, []byte("\n[post_install]\npreserve_after_install = true\n")...)
	case "hermes":
		raw = append(raw, []byte("\npreserve_after_install: true\n")...)
	case "deepseek":
		raw = append(raw, []byte("\n- id: preserve_after_install\n  name: user-plugin\n")...)
	case "pi":
		raw = append(raw, []byte("\n// preserve_after_install\n")...)
	}
	mustWrite(t, path, string(raw), 0600)
	return path
}

func hookPayload(runtime, attempt string) string {
	in := map[string]any{"hook_event_name": "PreToolUse", "tool_name": "mcp__payments__create_refund", "tool_input": map[string]any{"amount": 42}, "session_id": "session", "tool_use_id": attempt}
	switch runtime {
	case "openclaw":
		in = map[string]any{"tool_name": "payments__create_refund", "params": map[string]any{"amount": 42}, "session_id": "session", "tool_call_id": attempt}
	case "pi":
		// Pi has no MCP client: its tool names are plain.
		in = map[string]any{"tool_name": "create_refund", "arguments": map[string]any{"amount": 42}, "session_id": "session", "tool_call_id": attempt}
	case "hermes":
		in["hook_event_name"] = "pre_tool_call"
		in["extra"] = map[string]any{"tool_call_id": attempt}
	}
	raw, _ := json.Marshal(in)
	return string(raw)
}
func hookDenied(out string) bool {
	return strings.Contains(out, `"deny"`) || strings.Contains(out, `"block"`)
}
func mustWrite(t *testing.T, path, body string, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}
