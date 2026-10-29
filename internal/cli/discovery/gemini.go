package discovery

import (
	"context"
	"path/filepath"

	"github.com/withHumanAI/withHuman/agentruntime"
	"github.com/withHumanAI/withHuman/internal/cli/configfile"
	"github.com/withHumanAI/withHuman/internal/cli/mcpclient"
)

// gemini discovers Gemini CLI's tools for guided onboarding: the MCP
// servers in the project's .gemini/settings.json, then the user's.
type gemini struct{}

func (gemini) Key() agentruntime.Key { return agentruntime.Gemini }

// settingsPaths are the files whose mcpServers Gemini reads, the project's
// first because it wins a name both define.
func (gemini) settingsPaths(env Env) []string {
	home := env.Home
	if dir := env.getenv("GEMINI_CLI_HOME"); dir != "" {
		home = dir
	}
	var paths []string
	if env.Cwd != "" {
		paths = append(paths, filepath.Join(env.Cwd, ".gemini", "settings.json"))
	}
	return append(paths, filepath.Join(home, ".gemini", "settings.json"))
}

func (g gemini) servers(env Env) ([]mcpServer, error) {
	var servers []mcpServer
	for _, path := range g.settingsPaths(env) {
		raw, existed, err := configfile.ReadFile(path)
		if err != nil {
			return nil, err
		}
		doc, err := configfile.LoadJSONObject(raw, existed)
		if err != nil {
			continue
		}
		if object, ok := doc.Root.ObjectIfPresent("mcpServers"); ok {
			servers = append(servers, geminiServers(object, path, env.Cwd)...)
		}
	}
	return env.ownServers(dedupeServers(servers)), nil
}

// geminiServers reads Gemini's entries: httpUrl is streamable HTTP, url is
// SSE, and the rest are stdio commands.
func geminiServers(object *configfile.JSONObject, source, cwd string) []mcpServer {
	var servers []mcpServer
	for _, name := range object.Keys() {
		entry, ok := object.ObjectIfPresent(name)
		if !ok {
			continue
		}
		spec := mcpclient.Spec{Name: name, Cwd: cwd}
		switch {
		case entry.String("httpUrl") != "":
			spec.URL, spec.Headers = entry.String("httpUrl"), entry.StringMap("headers")
		case entry.String("url") != "":
			spec.URL, spec.Headers = entry.String("url"), entry.StringMap("headers")
		default:
			spec.Command, spec.Args, spec.Env = entry.String("command"), entry.Strings("args"), entry.StringMap("env")
			if dir := entry.String("cwd"); dir != "" {
				spec.Cwd = dir
			}
		}
		if spec.Command == "" && spec.URL == "" {
			continue
		}
		servers = append(servers, mcpServer{Name: name, Source: source, Spec: spec})
	}
	return servers
}

func (g gemini) Detect(env Env) (Detection, error) {
	d, err := detectRuntime(env, g.Key())
	if err != nil || !d.Installed {
		return d, err
	}
	servers, err := g.servers(env)
	d.MCPServers = detectionServers(servers)
	return d, err
}

func (g gemini) Discover(ctx context.Context, env Env, _ Detection) (Discovered, error) {
	servers, err := g.servers(env)
	if err != nil {
		return Discovered{}, err
	}
	return discover(ctx, g.Key(), servers), nil
}

// antigravity discovers Antigravity's tools for guided onboarding: the
// servers in the global ~/.gemini/config/mcp_config.json.
type antigravity struct{}

func (antigravity) Key() agentruntime.Key { return agentruntime.Antigravity }

func (antigravity) configPath(env Env) string {
	return filepath.Join(env.Home, ".gemini", "config", "mcp_config.json")
}

func (a antigravity) servers(env Env) ([]mcpServer, error) {
	path := a.configPath(env)
	raw, existed, err := configfile.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := configfile.LoadJSONObject(raw, existed)
	if err != nil {
		return nil, nil
	}
	object, ok := doc.Root.ObjectIfPresent("mcpServers")
	if !ok {
		return nil, nil
	}
	return env.ownServers(claudeStyleServers(object, path, env.Cwd)), nil
}

func (a antigravity) Detect(env Env) (Detection, error) {
	d, err := detectRuntime(env, a.Key())
	if err != nil || !d.Installed {
		return d, err
	}
	servers, err := a.servers(env)
	d.MCPServers = detectionServers(servers)
	return d, err
}

func (a antigravity) Discover(ctx context.Context, env Env, _ Detection) (Discovered, error) {
	servers, err := a.servers(env)
	if err != nil {
		return Discovered{}, err
	}
	return discover(ctx, a.Key(), servers), nil
}
