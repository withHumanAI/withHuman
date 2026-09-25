package discovery

import (
	"context"
	"github.com/withHumanAI/withHuman/internal/cli/configfile"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/withHumanAI/withHuman/agentruntime"
	"github.com/withHumanAI/withHuman/internal/cli/mcpclient"
)

// hermes discovers runtime tools for guided onboarding.
type hermes struct{}

func (hermes) Key() agentruntime.Key { return agentruntime.Hermes }

func (hermes) configPath(env Env) string {
	return filepath.Join(env.Home, ".hermes", "config.yaml")
}

func (h hermes) load(env Env) (*yaml.Node, error) {
	content, existed, err := configfile.ReadFile(h.configPath(env))
	if err != nil {
		return nil, err
	}
	return configfile.LoadYAMLMapping(content, existed)
}

// servers reads mcp_servers: <name>: {command, args, env} or {url, headers}.
func (h hermes) servers(env Env, root *yaml.Node, source string) []mcpServer {
	return env.ownServers(h.configuredServers(root, source))
}

func (hermes) configuredServers(root *yaml.Node, source string) []mcpServer {
	configured := configfile.YAMLGet(root, "mcp_servers")
	if configured == nil || configured.Kind != yaml.MappingNode {
		return nil
	}
	var servers []mcpServer
	for i := 0; i+1 < len(configured.Content); i += 2 {
		name, entry := configured.Content[i].Value, configured.Content[i+1]
		if entry.Kind != yaml.MappingNode {
			continue
		}
		spec := mcpclient.Spec{Name: name}
		if url := configfile.YAMLString(entry, "url"); url != "" {
			spec.URL = url
			spec.Headers = configfile.YAMLStringMap(entry, "headers")
		} else {
			spec.Command = configfile.YAMLString(entry, "command")
			spec.Args = configfile.YAMLStrings(entry, "args")
			spec.Env = configfile.YAMLStringMap(entry, "env")
			spec.Cwd = configfile.YAMLString(entry, "cwd")
		}
		if spec.Command == "" && spec.URL == "" {
			continue
		}
		servers = append(servers, mcpServer{Name: name, Source: source, Spec: spec})
	}
	return servers
}

func (h hermes) Discover(ctx context.Context, env Env, _ Detection) (Discovered, error) {
	root, err := h.load(env)
	if err != nil {
		return Discovered{}, err
	}
	return discover(ctx, h.Key(), h.servers(env, root, h.configPath(env))), nil
}
