// Package mcpservers registers the MCP servers a provider offers its
// agents (the hosted edition's gateway) with a runtime's MCP client, and
// takes them out again on eject. Each server is registered as a stdio
// server that runs `withhuman mcp <runtime> <server>`, the CLI's own
// relay to the endpoint: the instance credential stays in the CLI's
// private store, never in a runtime's configuration, and runtimes
// without a streamable-HTTP client are served the same way. Each
// runtime keeps its servers in its own file and shape.
package mcpservers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/withHumanAI/withHuman/agentruntime"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/cli/configfile"
)

// Env is what the writers read from the machine and pin into entries.
type Env struct {
	// Home is the user's home directory; runtime homes hang off it.
	Home string
	// Binary is the absolute path of this CLI, the command every entry runs.
	Binary string
	// ConfigDir is the CLI's configuration directory, pinned into every
	// entry so the relay finds the credential the install stored.
	ConfigDir string
	// Getenv reads an environment variable; nil means the process one.
	Getenv func(string) string
}

func (e Env) getenv(key string) string {
	if e.Getenv != nil {
		return e.Getenv(key)
	}
	return os.Getenv(key)
}

// ConfigDirVariable is the variable an entry sets so the relay reads the
// credential store the install wrote.
const ConfigDirVariable = "WITHHUMAN_CONFIG_DIR"

// Args are the relay's arguments for one server.
func Args(runtime agentruntime.Key, server string) []string {
	return []string{"mcp", string(runtime), server}
}

// IsRelay reports whether a configured stdio command is this CLI's relay
// (whatever binary path or config directory it pins).
func IsRelay(command string, args []string) bool {
	return len(args) == 3 && args[0] == "mcp" && filepath.Base(command) == "withhuman"
}

// Registration is one runtime's servers.
type Registration struct {
	Runtime agentruntime.Key
	Servers []domain.AgentMCPServer
}

// Result is what changed, what to tell the person, and the manual step a
// runtime the CLI cannot edit needs.
type Result struct {
	Changes []domain.OnboardingFileChange
	Notes   []string
	Manual  bool
	Message string
	Snippet string
}

func (r *Result) changed(path string, existed bool) {
	action := "created"
	if existed {
		action = "modified"
	}
	r.Changes = append(r.Changes, domain.OnboardingFileChange{Path: path, Action: action})
}

// newFileMode is the mode for a runtime file the CLI creates.
const newFileMode = 0o644

// Install registers every server with the runtime. Registering again is a
// no-op that reports no changes.
func Install(env Env, reg Registration) (Result, error) {
	if len(reg.Servers) == 0 {
		return Result{}, nil
	}
	if err := domain.ValidateAgentMCPServers(reg.Servers); err != nil {
		return Result{}, err
	}
	if env.Binary == "" || env.ConfigDir == "" {
		return Result{}, errors.New("the CLI's executable path and configuration directory are required to register MCP servers")
	}
	switch reg.Runtime {
	case agentruntime.ClaudeCode:
		return installJSON(filepath.Join(env.Home, ".claude.json"), claudeServers, env, reg, nil)
	case agentruntime.Codex:
		return installCodex(filepath.Join(env.Home, ".codex", "config.toml"), env, reg)
	case agentruntime.Hermes:
		return installHermes(filepath.Join(agentruntime.HermesHome(env.Home, env.getenv), "config.yaml"), env, reg)
	case agentruntime.OpenClaw:
		return installOpenClaw(filepath.Join(env.Home, ".openclaw", "openclaw.json"), env, reg)
	case agentruntime.DeepSeek:
		return manualDeepSeek(env, reg), nil
	case agentruntime.Pi:
		return Result{Notes: []string{fmt.Sprintf("Pi has no MCP client, so %s is not available to it.", labels(reg.Servers))}}, nil
	default:
		return Result{}, fmt.Errorf("unknown runtime %q", reg.Runtime)
	}
}

// Remove takes the servers out of the runtime's configuration. An entry a
// person repointed at something other than the relay is left alone, and a
// file that no longer parses is left with a note rather than blocking the
// eject.
func Remove(env Env, reg Registration) (Result, error) {
	if len(reg.Servers) == 0 {
		return Result{}, nil
	}
	result, err := remove(env, reg)
	if errors.Is(err, configfile.ErrUnparsable) {
		return Result{Notes: []string{fmt.Sprintf("The %s configuration could not be parsed; remove %s from its MCP servers by hand.", reg.Runtime, names(reg.Servers))}}, nil
	}
	return result, err
}

func remove(env Env, reg Registration) (Result, error) {
	switch reg.Runtime {
	case agentruntime.ClaudeCode:
		return removeJSON(filepath.Join(env.Home, ".claude.json"), claudeServers, reg.Servers)
	case agentruntime.Codex:
		return removeCodex(filepath.Join(env.Home, ".codex", "config.toml"), reg.Servers)
	case agentruntime.Hermes:
		return removeHermes(filepath.Join(agentruntime.HermesHome(env.Home, env.getenv), "config.yaml"), reg.Servers)
	case agentruntime.OpenClaw:
		return removeJSON(filepath.Join(env.Home, ".openclaw", "openclaw.json"), openClawServers, reg.Servers)
	case agentruntime.DeepSeek:
		return Result{Notes: []string{fmt.Sprintf("Remove %s from the DeepSeek Harness profile's MCP configuration by hand if you added it.", names(reg.Servers))}}, nil
	case agentruntime.Pi:
		return Result{}, nil
	default:
		return Result{}, fmt.Errorf("unknown runtime %q", reg.Runtime)
	}
}

// labels joins the servers' labels for a sentence.
func labels(servers []domain.AgentMCPServer) string {
	out := ""
	for i, server := range servers {
		if i > 0 {
			out += ", "
		}
		out += server.Label
	}
	return out
}

// names joins the servers' names for a sentence.
func names(servers []domain.AgentMCPServer) string {
	out := ""
	for i, server := range servers {
		if i > 0 {
			out += ", "
		}
		out += server.Name
	}
	return out
}

func isNotStrictJSON(err error) bool { return errors.Is(err, configfile.ErrNotStrictJSON) }
