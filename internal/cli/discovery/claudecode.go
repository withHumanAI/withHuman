package discovery

import (
	"context"
	"fmt"
	"github.com/withHumanAI/withHuman/internal/cli/configfile"
	"path/filepath"

	"github.com/withHumanAI/withHuman/agentruntime"
)

// claudeCode discovers runtime tools for guided onboarding.
type claudeCode struct{}

func (claudeCode) Key() agentruntime.Key { return agentruntime.ClaudeCode }

func (claudeCode) settingsPath(env Env) string {
	return filepath.Join(env.Home, ".claude", "settings.json")
}

// servers reads the user-scope and current-project servers from
// ~/.claude.json and the project's .mcp.json. Names collide in that order
// of precedence.
func (claudeCode) servers(env Env) ([]mcpServer, error) {
	var servers []mcpServer
	userConfig := filepath.Join(env.Home, ".claude.json")
	content, existed, err := configfile.ReadFile(userConfig)
	if err != nil {
		return nil, err
	}
	if existed {
		doc, err := configfile.LoadJSONObject(content, true)
		if err != nil {
			return nil, fmt.Errorf("cannot discover tools from Claude Code configuration: %w", err)
		}
		if object, ok := doc.Root.ObjectIfPresent("mcpServers"); ok {
			servers = append(servers, claudeStyleServers(object, userConfig, env.Cwd)...)
		}
		if projects, ok := doc.Root.ObjectIfPresent("projects"); ok && env.Cwd != "" {
			if project, ok := projects.ObjectIfPresent(env.Cwd); ok {
				if object, ok := project.ObjectIfPresent("mcpServers"); ok {
					servers = append(servers, claudeStyleServers(object, userConfig+" (project "+env.Cwd+")", env.Cwd)...)
				}
			}
		}
	}
	if env.Cwd != "" {
		projectConfig := filepath.Join(env.Cwd, ".mcp.json")
		content, existed, err := configfile.ReadFile(projectConfig)
		if err != nil {
			return nil, err
		}
		if existed {
			doc, err := configfile.LoadJSONObject(content, true)
			if err != nil {
				return nil, fmt.Errorf("cannot discover tools from project MCP configuration: %w", err)
			}
			if object, ok := doc.Root.ObjectIfPresent("mcpServers"); ok {
				servers = append(servers, claudeStyleServers(object, projectConfig, env.Cwd)...)
			}
		}
	}
	return env.ownServers(dedupeServers(servers)), nil
}

// Discover probes every configured server, lending remote ones the OAuth
// token Claude Code stored when it signed in to them, so they list the
// way they do for Claude Code.
func (c claudeCode) Discover(ctx context.Context, env Env, _ Detection) (Discovered, error) {
	servers, err := c.servers(env)
	if err != nil {
		return Discovered{}, err
	}
	found := discover(ctx, c.Key(), withClaudeTokens(servers, claudeMCPTokens(ctx, env)))
	return withClaudeSignInHints(found, servers), nil
}
