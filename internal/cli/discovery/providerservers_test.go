package discovery

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/withHumanAI/withHuman/agentruntime"
	"github.com/withHumanAI/withHuman/domain"
)

// The relay the CLI registered for the provider's own MCP server is left
// out of detection and discovery: its tools are the provider's catalogue,
// and starting it would ask the provider about itself.
func TestProviderServersAreLeftOutOfDiscovery(t *testing.T) {
	env, _ := fixtureEnv(t, "claude-code", agentruntime.ClaudeCode)
	config, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"withhuman": map[string]any{"type": "stdio", "command": testBinary, "args": []string{"mcp", "claude-code", "withhuman"}},
		"by-name":   map[string]any{"type": "http", "url": "http://127.0.0.1:9/mcp"},
	}})
	if err := os.WriteFile(filepath.Join(env.Home, ".claude.json"), config, 0o600); err != nil {
		t.Fatal(err)
	}
	env.ProviderServers = []domain.AgentMCPServer{{Name: "by-name", URL: "https://app.example/gateway/mcp", Label: "gateway"}}
	det, err := claudeCode{}.Detect(env)
	if err != nil || len(det.MCPServers) != 0 {
		t.Fatalf("detection = %+v, %v", det.MCPServers, err)
	}
	found, err := claudeCode{}.Discover(context.Background(), env, det)
	if err != nil || len(found.MCP) != 0 || len(found.Errors) != 0 {
		t.Fatalf("discovered = %+v, %v", found, err)
	}
}
