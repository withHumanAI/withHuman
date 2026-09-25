package mcpservers

import (
	"slices"

	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/cli/configfile"
)

// codexEntry is one relay as a [mcp_servers.<name>] table: command, args
// and env, the shape Codex reads for stdio servers.
func codexEntry(env Env, reg Registration, server domain.AgentMCPServer) map[string]any {
	return map[string]any{
		"command": env.Binary, "args": Args(reg.Runtime, server.Name),
		"env": map[string]any{ConfigDirVariable: env.ConfigDir},
	}
}

func codexEntryMatches(entry map[string]any, env Env, reg Registration, server domain.AgentMCPServer) bool {
	return configfile.TOMLString(entry, "command") == env.Binary &&
		slices.Equal(configfile.TOMLStrings(entry["args"]), Args(reg.Runtime, server.Name)) &&
		configfile.TOMLStringMap(entry["env"])[ConfigDirVariable] == env.ConfigDir
}

// installCodex upserts the servers' tables in config.toml.
func installCodex(path string, env Env, reg Registration) (Result, error) {
	content, existed, err := configfile.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	table, err := configfile.LoadTOML(content, existed)
	if err != nil {
		return Result{}, err
	}
	changed := false
	servers := configfile.TOMLTable(table, "mcp_servers")
	for _, server := range reg.Servers {
		if entry, ok := servers[server.Name].(map[string]any); ok && codexEntryMatches(entry, env, reg, server) {
			continue
		}
		servers[server.Name] = codexEntry(env, reg, server)
		changed = true
	}
	if !changed {
		return Result{}, nil
	}
	rendered, err := configfile.FormatTOML(table)
	if err != nil {
		return Result{}, err
	}
	if err := configfile.Write(path, rendered, newFileMode); err != nil {
		return Result{}, err
	}
	var result Result
	result.changed(path, existed)
	if existed {
		result.Notes = append(result.Notes, configfile.TOMLRewriteNote)
	}
	return result, nil
}

// removeCodex deletes the servers' tables that still run the relay, and
// the mcp_servers table when that empties it.
func removeCodex(path string, servers []domain.AgentMCPServer) (Result, error) {
	content, existed, err := configfile.ReadFile(path)
	if err != nil || !existed {
		return Result{}, err
	}
	table, err := configfile.LoadTOML(content, true)
	if err != nil {
		return Result{}, err
	}
	configured, ok := configfile.TOMLTableIfPresent(table, "mcp_servers")
	if !ok {
		return Result{}, nil
	}
	changed := false
	for _, server := range servers {
		if entry, ok := configured[server.Name].(map[string]any); ok && IsRelay(configfile.TOMLString(entry, "command"), configfile.TOMLStrings(entry["args"])) {
			delete(configured, server.Name)
			changed = true
		}
	}
	if !changed {
		return Result{}, nil
	}
	if len(configured) == 0 {
		delete(table, "mcp_servers")
	}
	rendered, err := configfile.FormatTOML(table)
	if err != nil {
		return Result{}, err
	}
	if err := configfile.Write(path, rendered, newFileMode); err != nil {
		return Result{}, err
	}
	var result Result
	result.changed(path, true)
	result.Notes = append(result.Notes, configfile.TOMLRewriteNote)
	return result, nil
}
