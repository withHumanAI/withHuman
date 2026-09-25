// Package runtimeconfig reads the provider's public /api/runtime-config
// for what the CLI acts on: the MCP servers the provider offers every
// enrolled agent.
package runtimeconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/withHumanAI/withHuman/domain"
)

// Config is the part of runtime-config the CLI reads.
type Config struct {
	AgentMCPServers []domain.AgentMCPServer `json:"agent_mcp_servers"`
}

// Fetch reads the provider's runtime-config. Every advertised server is
// validated before it can reach a runtime's configuration, since the
// instance credential is sent to it.
func Fetch(ctx context.Context, client *http.Client, origin string) (Config, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/api/runtime-config", nil)
	if err != nil {
		return Config{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return Config{}, fmt.Errorf("read the provider configuration: %w", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Config{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Config{}, fmt.Errorf("read the provider configuration: HTTP %d", resp.StatusCode)
	}
	var config Config
	if err := json.Unmarshal(payload, &config); err != nil {
		return Config{}, fmt.Errorf("read the provider configuration: %w", err)
	}
	if err := domain.ValidateAgentMCPServers(config.AgentMCPServers); err != nil {
		return Config{}, fmt.Errorf("provider configuration: %w", err)
	}
	return config, nil
}
