package mcpservers

import (
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/cli/configfile"
)

// hermesEntry is one relay as mcp_servers.<name>: {command, args, env}.
func hermesEntry(env Env, reg Registration, server domain.AgentMCPServer) *yaml.Node {
	args := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, arg := range Args(reg.Runtime, server.Name) {
		args.Content = append(args.Content, configfile.YAMLScalar(arg))
	}
	return configfile.YAMLMapping(
		configfile.YAMLScalar("command"), configfile.YAMLScalar(env.Binary),
		configfile.YAMLScalar("args"), args,
		configfile.YAMLScalar("env"), configfile.YAMLMapping(configfile.YAMLScalar(ConfigDirVariable), configfile.YAMLScalar(env.ConfigDir)),
	)
}

func hermesEntryMatches(entry *yaml.Node, env Env, reg Registration, server domain.AgentMCPServer) bool {
	return entry != nil && entry.Kind == yaml.MappingNode &&
		configfile.YAMLString(entry, "command") == env.Binary &&
		slices.Equal(configfile.YAMLStrings(entry, "args"), Args(reg.Runtime, server.Name)) &&
		configfile.YAMLStringMap(entry, "env")[ConfigDirVariable] == env.ConfigDir
}

// installHermes upserts the servers in config.yaml, keeping the person's
// comments and order.
func installHermes(path string, env Env, reg Registration) (Result, error) {
	content, existed, err := configfile.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	root, err := configfile.LoadYAMLMapping(content, existed)
	if err != nil {
		return Result{}, err
	}
	changed := false
	servers := configfile.YAMLEnsureMapping(root, "mcp_servers")
	for _, server := range reg.Servers {
		if hermesEntryMatches(configfile.YAMLGet(servers, server.Name), env, reg, server) {
			continue
		}
		configfile.YAMLSet(servers, server.Name, hermesEntry(env, reg, server))
		changed = true
	}
	if !changed {
		return Result{}, nil
	}
	rendered, err := configfile.FormatYAML(root)
	if err != nil {
		return Result{}, err
	}
	if err := configfile.Write(path, rendered, newFileMode); err != nil {
		return Result{}, err
	}
	var result Result
	result.changed(path, existed)
	return result, nil
}

// removeHermes deletes the servers' entries that still run the relay, and
// mcp_servers when that empties it.
func removeHermes(path string, servers []domain.AgentMCPServer) (Result, error) {
	content, existed, err := configfile.ReadFile(path)
	if err != nil || !existed {
		return Result{}, err
	}
	root, err := configfile.LoadYAMLMapping(content, true)
	if err != nil {
		return Result{}, err
	}
	configured := configfile.YAMLGet(root, "mcp_servers")
	if configured == nil || configured.Kind != yaml.MappingNode {
		return Result{}, nil
	}
	changed := false
	for _, server := range servers {
		if entry := configfile.YAMLGet(configured, server.Name); entry != nil && IsRelay(configfile.YAMLString(entry, "command"), configfile.YAMLStrings(entry, "args")) {
			configfile.YAMLDeleteKey(configured, server.Name)
			changed = true
		}
	}
	if !changed {
		return Result{}, nil
	}
	if len(configured.Content) == 0 {
		configfile.YAMLDeleteKey(root, "mcp_servers")
	}
	rendered, err := configfile.FormatYAML(root)
	if err != nil {
		return Result{}, err
	}
	if err := configfile.Write(path, rendered, newFileMode); err != nil {
		return Result{}, err
	}
	var result Result
	result.changed(path, true)
	return result, nil
}
