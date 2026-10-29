package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A runtime plugin that keeps the agent key itself starts the relay with
// the key; the relay finds the server in the provider's runtime-config and
// sends the key only to that provider's own origin.
func TestRelayTargetForAPassedKey(t *testing.T) {
	var servers []map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/runtime-config" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected provider call: %s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"agent_mcp_servers": servers})
	}))
	defer provider.Close()
	env := map[string]string{"WITHHUMAN_AGENT_TOKEN": "whc_passed", "WITHHUMAN_URL": provider.URL}
	a := &app{configDir: t.TempDir(), getenv: func(key string) string { return env[key] }}

	servers = []map[string]any{{"name": "withhuman-gateway", "url": provider.URL + "/gateway/mcp", "label": "withHuman MCP gateway"}}
	endpoint, token, err := a.mcpTarget(context.Background(), &http.Client{}, "gemini", "withhuman-gateway")
	if err != nil || endpoint != provider.URL+"/gateway/mcp" || token != "whc_passed" {
		t.Fatalf("target = %q %q %v", endpoint, token, err)
	}

	servers = []map[string]any{{"name": "withhuman-gateway", "url": "https://gateway.example.com/mcp", "label": "Elsewhere"}}
	if _, _, err := a.mcpTarget(context.Background(), &http.Client{}, "gemini", "withhuman-gateway"); err == nil || !strings.Contains(err.Error(), "not served from") {
		t.Fatalf("a server on another origin got the key: %v", err)
	}

	servers = nil
	if _, _, err := a.mcpTarget(context.Background(), &http.Client{}, "gemini", "withhuman-gateway"); err == nil || !strings.Contains(err.Error(), "does not offer") {
		t.Fatalf("missing server: %v", err)
	}

	// Without a passed key the relay needs the install's saved credential.
	delete(env, "WITHHUMAN_AGENT_TOKEN")
	if _, _, err := a.mcpTarget(context.Background(), &http.Client{}, "gemini", "withhuman-gateway"); err == nil || !strings.Contains(err.Error(), "withhuman agent install gemini") {
		t.Fatalf("no saved credential: %v", err)
	}
}
