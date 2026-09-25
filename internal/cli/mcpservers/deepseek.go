package mcpservers

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// manualDeepSeek hands the person the entry to add: the harness's MCP
// configuration keys are unverified, so the CLI writes nothing there.
func manualDeepSeek(env Env, reg Registration) Result {
	return Result{
		Manual:  true,
		Message: fmt.Sprintf("DeepSeek Harness reads MCP servers from its profile, which the CLI does not edit. Add %s to the MCP configuration of the profile at %s.", labels(reg.Servers), deepSeekProfileDir(env)),
		Snippet: snippet(env, reg),
	}
}

// deepSeekProfileDir is the profile the hooks bridge was mounted in:
// `default` when present, the only profile when there is one, else
// `default`.
func deepSeekProfileDir(env Env) string {
	home := env.getenv("DSH_HOME")
	if home == "" {
		home = filepath.Join(env.Home, ".dsh")
	}
	profiles := filepath.Join(home, "profiles")
	if _, err := os.Stat(filepath.Join(profiles, "default")); err == nil {
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
