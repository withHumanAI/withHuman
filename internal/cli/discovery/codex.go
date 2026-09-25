package discovery

import (
	"context"
	"github.com/withHumanAI/withHuman/internal/cli/configfile"
	"path/filepath"

	"github.com/withHumanAI/withHuman/agentruntime"
	"github.com/withHumanAI/withHuman/internal/cli/mcpclient"
)

// codex discovers runtime tools for guided onboarding.
type codex struct{}

func (codex) Key() agentruntime.Key { return agentruntime.Codex }

func (codex) configPath(env Env) string {
	return filepath.Join(env.Home, ".codex", "config.toml")
}

// codexTrustNote tells the person what was recorded on their behalf.

func (c codex) load(env Env) (map[string]any, error) {
	content, existed, err := configfile.ReadFile(c.configPath(env))
	if err != nil {
		return nil, err
	}
	return configfile.LoadTOML(content, existed)
}

// servers reads [mcp_servers.<name>] tables: command/args/env for stdio,
// url plus http_headers for streamable HTTP. A table with enabled = false
// is one Codex itself will not start.
func (c codex) servers(env Env, table map[string]any, source string) []mcpServer {
	return env.ownServers(c.configuredServers(table, source))
}

func (codex) configuredServers(table map[string]any, source string) []mcpServer {
	configured, ok := configfile.TOMLTableIfPresent(table, "mcp_servers")
	if !ok {
		return nil
	}
	var servers []mcpServer
	for _, name := range sortedKeys(configured) {
		entry, ok := configured[name].(map[string]any)
		if !ok {
			continue
		}
		if enabled, ok := entry["enabled"].(bool); ok && !enabled {
			continue
		}
		spec := mcpclient.Spec{Name: name}
		if url := configfile.TOMLString(entry, "url"); url != "" {
			spec.URL = url
			spec.Headers = configfile.TOMLStringMap(entry["http_headers"])
		} else {
			spec.Command = configfile.TOMLString(entry, "command")
			spec.Args = configfile.TOMLStrings(entry["args"])
			spec.Env = configfile.TOMLStringMap(entry["env"])
			spec.Cwd = configfile.TOMLString(entry, "cwd")
		}
		if spec.Command == "" && spec.URL == "" {
			continue
		}
		servers = append(servers, mcpServer{Name: name, Source: source, Spec: spec})
	}
	return servers
}

// Discover asks Codex itself for its MCP inventory, so servers Codex
// reaches with OAuth tokens it keeps in the OS keychain list the way they
// do for Codex, and servers it has disabled stay out. Without a Codex
// binary the configured servers are probed directly.
func (c codex) Discover(ctx context.Context, env Env, det Detection) (Discovered, error) {
	if det.BinaryPath == "" {
		table, err := c.load(env)
		if err != nil {
			return Discovered{}, err
		}
		return discover(ctx, c.Key(), c.servers(env, table, c.configPath(env))), nil
	}
	statuses, err := codexServerStatuses(ctx, det.BinaryPath, filepath.Join(env.Home, ".codex"))
	if err != nil {
		return Discovered{}, err
	}
	return codexDiscovered(env, statuses), nil
}
