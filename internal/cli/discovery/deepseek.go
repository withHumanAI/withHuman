package discovery

import (
	"context"
	"github.com/withHumanAI/withHuman/internal/cli/configfile"
	"os"
	"path/filepath"
	"sort"

	"github.com/withHumanAI/withHuman/agentruntime"
)

// deepSeek discovers tools from the active DeepSeek Harness profile.
type deepSeek struct{}

func (deepSeek) Key() agentruntime.Key { return agentruntime.DeepSeek }

// home is $DSH_HOME or ~/.dsh.
func (deepSeek) home(env Env) string {
	if home := env.getenv("DSH_HOME"); home != "" {
		return home
	}
	return filepath.Join(env.Home, ".dsh")
}

// profileDir selects the default profile, or the only existing profile.
func (d deepSeek) profileDir(env Env) string {
	profiles := filepath.Join(d.home(env), "profiles")
	if exists(filepath.Join(profiles, "default")) {
		return filepath.Join(profiles, "default")
	}
	entries, err := os.ReadDir(profiles)
	if err == nil {
		var dirs []string
		for _, entry := range entries {
			if entry.IsDir() {
				dirs = append(dirs, entry.Name())
			}
		}
		sort.Strings(dirs)
		if len(dirs) == 1 {
			return filepath.Join(profiles, dirs[0])
		}
	}
	return filepath.Join(profiles, "default")
}

// servers reads any Claude-style mcpServers block the harness profile or
// home carries. The harness's MCP configuration keys are unverified, so
// this checks the obvious files and finds nothing otherwise.
func (d deepSeek) servers(env Env) ([]mcpServer, error) {
	var servers []mcpServer
	for _, path := range []string{
		filepath.Join(d.profileDir(env), "mcp.json"),
		filepath.Join(d.profileDir(env), "config.json"),
		filepath.Join(d.home(env), "mcp.json"),
		filepath.Join(d.home(env), "config.json"),
	} {
		content, existed, err := configfile.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if !existed {
			continue
		}
		doc, err := configfile.LoadJSONObject(content, true)
		if err != nil {
			return nil, err
		}
		if object, ok := doc.Root.ObjectIfPresent("mcpServers"); ok {
			servers = append(servers, claudeStyleServers(object, path, env.Cwd)...)
		}
	}
	return env.ownServers(dedupeServers(servers)), nil
}

func (d deepSeek) Discover(ctx context.Context, env Env, _ Detection) (Discovered, error) {
	servers, err := d.servers(env)
	if err != nil {
		return Discovered{}, err
	}
	return discover(ctx, d.Key(), servers), nil
}
