package mcpservers

import (
	"fmt"

	"github.com/withHumanAI/withHuman/agentruntime"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/cli/configfile"
)

// claudeEntry is one relay in the mcpServers shape Claude Code and the
// runtimes that copied it read: a stdio command with args and env.
func claudeEntry(env Env, runtime agentruntime.Key, server domain.AgentMCPServer) *configfile.JSONObject {
	args := []configfile.JSONValue{}
	for _, arg := range Args(runtime, server.Name) {
		args = append(args, arg)
	}
	variables := configfile.NewJSONObject()
	variables.Set(ConfigDirVariable, env.ConfigDir)
	entry := configfile.NewJSONObject()
	entry.Set("type", "stdio")
	entry.Set("command", env.Binary)
	entry.Set("args", args)
	entry.Set("env", variables)
	return entry
}

// claudeEntryMatches reports whether the configured entry already says
// what claudeEntry would write.
func claudeEntryMatches(entry *configfile.JSONObject, env Env, runtime agentruntime.Key, server domain.AgentMCPServer) bool {
	if entry.String("command") != env.Binary || entry.StringMap("env")[ConfigDirVariable] != env.ConfigDir {
		return false
	}
	want := Args(runtime, server.Name)
	got := entry.Strings("args")
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// installClaudeStyle upserts the servers into the file's mcpServers
// object. notes are appended when the file changed.
func installClaudeStyle(path string, env Env, reg Registration, notes []string) (Result, error) {
	content, existed, err := configfile.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	doc, err := configfile.LoadJSONObject(content, existed)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", path, err)
	}
	changed := false
	servers := doc.Root.Object("mcpServers")
	for _, server := range reg.Servers {
		if entry, ok := servers.ObjectIfPresent(server.Name); ok && claudeEntryMatches(entry, env, reg.Runtime, server) {
			continue
		}
		servers.Set(server.Name, claudeEntry(env, reg.Runtime, server))
		changed = true
	}
	var result Result
	if !changed {
		return result, nil
	}
	if err := configfile.Write(path, doc.Bytes(), newFileMode); err != nil {
		return Result{}, err
	}
	result.changed(path, existed)
	result.Notes = append(result.Notes, notes...)
	return result, nil
}

// removeClaudeStyle deletes the servers' entries that still run the relay.
func removeClaudeStyle(path string, servers []domain.AgentMCPServer) (Result, error) {
	content, existed, err := configfile.ReadFile(path)
	if err != nil || !existed {
		return Result{}, err
	}
	doc, err := configfile.LoadJSONObject(content, true)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", path, err)
	}
	configured, ok := doc.Root.ObjectIfPresent("mcpServers")
	if !ok {
		return Result{}, nil
	}
	changed := false
	for _, server := range servers {
		if entry, ok := configured.ObjectIfPresent(server.Name); ok && IsRelay(entry.String("command"), entry.Strings("args")) {
			configured.Delete(server.Name)
			changed = true
		}
	}
	if !changed {
		return Result{}, nil
	}
	if err := configfile.Write(path, doc.Bytes(), newFileMode); err != nil {
		return Result{}, err
	}
	var result Result
	result.changed(path, true)
	return result, nil
}

// snippet renders the mcpServers block a person pastes when the CLI
// cannot edit the runtime's file.
func snippet(env Env, reg Registration) string {
	servers := configfile.NewJSONObject()
	for _, server := range reg.Servers {
		servers.Set(server.Name, claudeEntry(env, reg.Runtime, server))
	}
	root := configfile.NewJSONObject()
	root.Set("mcpServers", servers)
	return string(configfile.FormatJSON(root, "  "))
}
