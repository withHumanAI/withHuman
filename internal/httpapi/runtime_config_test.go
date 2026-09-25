package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/withHumanAI/withHuman/domain"
)

// An edition's runtime-config entries ride along with core's and can
// never shadow one: the shared frontend reads core's keys from core. The
// guard list and the handler must name the same keys.
func TestRuntimeConfigCarriesEditionEntriesAndGuardsCoreKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	read := func(config Config) map[string]any {
		t.Helper()
		router := NewRouter(Dependencies{Logger: logger, Config: config})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/runtime-config", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("got status %d", response.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	open := read(Config{})
	if _, present := open["signup_enabled"]; present {
		t.Fatal("the open edition must not report an edition entry")
	}
	for key := range open {
		if !runtimeConfigKeys[key] {
			t.Errorf("core reports %q but does not guard it", key)
		}
	}
	for key := range runtimeConfigKeys {
		if _, present := open[key]; !present {
			t.Errorf("the guard lists %q but core does not report it", key)
		}
	}

	extras, err := RuntimeConfigExtras(map[string]any{"signup_enabled": true, "billing_enabled": false})
	if err != nil {
		t.Fatal(err)
	}
	hosted := read(Config{Extras: extras})
	if hosted["signup_enabled"] != true || hosted["billing_enabled"] != false {
		t.Fatalf("edition entries missing: %#v", hosted)
	}
	if _, err := RuntimeConfigExtras(map[string]any{"version": "shadowed"}); err == nil {
		t.Fatal("an entry named after a core key must be refused")
	}
}

// The MCP servers an edition offers its agents are core's entry, so the
// CLI reads one key in every edition: an empty list in the open edition,
// the edition's servers otherwise.
func TestRuntimeConfigReportsAgentMCPServers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	read := func(config Config) map[string]any {
		t.Helper()
		router := NewRouter(Dependencies{Logger: logger, Config: config})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/runtime-config", nil))
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	if servers, ok := read(Config{})["agent_mcp_servers"].([]any); !ok || len(servers) != 0 {
		t.Fatalf("open edition must report an empty list, got %#v", read(Config{})["agent_mcp_servers"])
	}
	hosted := read(Config{AgentMCPServers: []domain.AgentMCPServer{{Name: "withhuman", URL: "https://app.example/gateway/mcp", Label: "withHuman MCP gateway"}}})
	servers, _ := hosted["agent_mcp_servers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("servers = %#v", hosted["agent_mcp_servers"])
	}
	if server, _ := servers[0].(map[string]any); server["name"] != "withhuman" || server["url"] != "https://app.example/gateway/mcp" || server["label"] != "withHuman MCP gateway" {
		t.Fatalf("server = %#v", servers[0])
	}
}
