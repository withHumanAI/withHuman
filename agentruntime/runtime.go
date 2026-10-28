// Package agentruntime is the catalog of agent runtimes withHuman can
// intercept: their keys, the tools each one ships with, how each one names
// MCP tools at hook time, and the commands that make each one exercise a
// gated tool. The server and the CLI both read this catalog, so the agent
// types the pipeline editor sees, the matcher a hook installs, and the
// test command onboarding prints can never disagree.
//
// The package is a leaf: standard library only, no product imports.
package agentruntime

import (
	"fmt"
	"strings"
)

// Key identifies a runtime. Keys double as the agent name and agent type
// onboarding registers, so every key must match the agent type pattern
// ^[a-z0-9][a-z0-9._-]{0,62}$.
type Key string

const (
	ClaudeCode Key = "claude-code"
	Codex      Key = "codex"
	OpenClaw   Key = "openclaw"
	Pi         Key = "pi"
	Hermes     Key = "hermes"
	DeepSeek   Key = "deepseek"
)

// ToolKind is the coarse classification onboarding uses to suggest which
// built-in tools should go through a human: shell tools always, edit tools
// never by default, and read tools not at all.
type ToolKind string

const (
	ToolShell ToolKind = "shell"
	ToolEdit  ToolKind = "edit"
	ToolRead  ToolKind = "read"
	ToolOther ToolKind = "other"
)

// BuiltinTool is a tool the runtime ships with, named exactly as the
// runtime's hook reports it.
type BuiltinTool struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Kind        ToolKind `json:"kind"`
}

// MCPNaming describes how a runtime spells an MCP tool when its hook fires.
type MCPNaming string

const (
	// MCPDoubleUnderscore is the Claude Code convention: mcp__<server>__<tool>.
	MCPDoubleUnderscore MCPNaming = "mcp__server__tool"
	// MCPDoubleUnderscoreSanitized is Codex's spelling of the same shape:
	// every character of the server and tool names outside [A-Za-z0-9_]
	// becomes an underscore (codex-mcp sanitize_responses_api_tool_name),
	// so "mcp-server-linkedin" reads mcp__mcp_server_linkedin__<tool>.
	MCPDoubleUnderscoreSanitized MCPNaming = "mcp__server__tool_sanitized"
	// MCPServerPrefixed is OpenClaw's convention: <server>__<tool>.
	MCPServerPrefixed MCPNaming = "server__tool"
	// MCPNone marks a runtime without an MCP client.
	MCPNone MCPNaming = ""
)

// Runtime is one catalog entry.
type Runtime struct {
	Key         Key           `json:"key"`
	DisplayName string        `json:"display_name"`
	Binary      string        `json:"binary"`
	HomeDir     string        `json:"home_dir"`
	Builtins    []BuiltinTool `json:"builtins"`
	MCPNaming   MCPNaming     `json:"mcp_naming"`
	// HookMatcher reports whether the runtime's hook registration carries
	// a tool-name matcher (Claude Code, Codex, DeepSeek); it is installed
	// as the catch-all so the pipeline on the server decides.
	HookMatcher bool `json:"hook_matcher"`
}

// SupportsMCP reports whether the runtime has an MCP client whose tools
// can be discovered.
func (r Runtime) SupportsMCP() bool { return r.MCPNaming != MCPNone }

// Builtin looks a built-in tool up by its runtime name.
func (r Runtime) Builtin(name string) (BuiltinTool, bool) {
	for _, tool := range r.Builtins {
		if tool.Name == name {
			return tool, true
		}
	}
	return BuiltinTool{}, false
}

var catalog = []Runtime{
	{
		Key: ClaudeCode, DisplayName: "Claude Code", Binary: "claude", HomeDir: ".claude",
		MCPNaming: MCPDoubleUnderscore, HookMatcher: true,
		Builtins: []BuiltinTool{
			{Name: "Bash", Description: "Run shell commands", Kind: ToolShell},
			{Name: "Edit", Description: "Edit an existing file", Kind: ToolEdit},
			{Name: "Write", Description: "Create or overwrite a file", Kind: ToolEdit},
			{Name: "MultiEdit", Description: "Apply several edits to one file", Kind: ToolEdit},
			{Name: "NotebookEdit", Description: "Edit a Jupyter notebook cell", Kind: ToolEdit},
			{Name: "Read", Description: "Read a file", Kind: ToolRead},
			{Name: "Glob", Description: "Find files by pattern", Kind: ToolRead},
			{Name: "Grep", Description: "Search file contents", Kind: ToolRead},
			{Name: "WebFetch", Description: "Fetch a web page", Kind: ToolRead},
			{Name: "WebSearch", Description: "Search the web", Kind: ToolRead},
			{Name: "Task", Description: "Delegate work to a subagent", Kind: ToolOther},
		},
	},
	{
		Key: Codex, DisplayName: "Codex", Binary: "codex", HomeDir: ".codex",
		MCPNaming: MCPDoubleUnderscoreSanitized, HookMatcher: true,
		// Codex's hook protocol spells its shell tool Bash, like Claude Code,
		// and file edits arrive as apply_patch.
		Builtins: []BuiltinTool{
			{Name: "Bash", Description: "Run shell commands", Kind: ToolShell},
			{Name: "apply_patch", Description: "Apply a diff to the working tree", Kind: ToolEdit},
		},
	},
	{
		Key: OpenClaw, DisplayName: "OpenClaw", Binary: "openclaw", HomeDir: ".openclaw",
		MCPNaming: MCPServerPrefixed, HookMatcher: false,
		Builtins: []BuiltinTool{
			{Name: "exec", Description: "Run shell commands", Kind: ToolShell},
			{Name: "write", Description: "Create or overwrite a file", Kind: ToolEdit},
			{Name: "edit", Description: "Edit an existing file", Kind: ToolEdit},
			{Name: "apply_patch", Description: "Apply a diff", Kind: ToolEdit},
			{Name: "read", Description: "Read a file", Kind: ToolRead},
			{Name: "browser", Description: "Drive a web browser", Kind: ToolOther},
			{Name: "message", Description: "Send a chat message", Kind: ToolOther},
		},
	},
	{
		Key: Pi, DisplayName: "Pi", Binary: "pi", HomeDir: ".pi",
		MCPNaming: MCPNone, HookMatcher: false,
		Builtins: []BuiltinTool{
			{Name: "bash", Description: "Run shell commands", Kind: ToolShell},
			{Name: "write", Description: "Create or overwrite a file", Kind: ToolEdit},
			{Name: "edit", Description: "Edit an existing file", Kind: ToolEdit},
			{Name: "read", Description: "Read a file", Kind: ToolRead},
			{Name: "grep", Description: "Search file contents", Kind: ToolRead},
			{Name: "find", Description: "Find files", Kind: ToolRead},
			{Name: "ls", Description: "List a directory", Kind: ToolRead},
		},
	},
	{
		Key: Hermes, DisplayName: "Hermes Agent", Binary: "hermes", HomeDir: ".hermes",
		MCPNaming: MCPDoubleUnderscore, HookMatcher: false,
		Builtins: []BuiltinTool{
			{Name: "terminal", Description: "Run shell commands", Kind: ToolShell},
			{Name: "process", Description: "Manage background processes", Kind: ToolShell},
			{Name: "execute_code", Description: "Run a code snippet", Kind: ToolShell},
			{Name: "patch", Description: "Apply a patch to a file", Kind: ToolEdit},
			{Name: "write_file", Description: "Create or overwrite a file", Kind: ToolEdit},
			{Name: "read_file", Description: "Read a file", Kind: ToolRead},
		},
	},
	{
		Key: DeepSeek, DisplayName: "DeepSeek Harness", Binary: "dsh", HomeDir: ".dsh",
		MCPNaming: MCPDoubleUnderscore, HookMatcher: true,
		Builtins: []BuiltinTool{
			{Name: "bash", Description: "Run shell commands", Kind: ToolShell},
			{Name: "run_code", Description: "Run a code snippet", Kind: ToolShell},
			{Name: "write", Description: "Create or overwrite a file", Kind: ToolEdit},
			{Name: "edit", Description: "Edit an existing file", Kind: ToolEdit},
			{Name: "read", Description: "Read a file", Kind: ToolRead},
		},
	},
}

// All returns every runtime in catalog order.
func All() []Runtime {
	out := make([]Runtime, len(catalog))
	copy(out, catalog)
	return out
}

// Keys returns every runtime key in catalog order.
func Keys() []Key {
	keys := make([]Key, len(catalog))
	for index, runtime := range catalog {
		keys[index] = runtime.Key
	}
	return keys
}

// Lookup finds a runtime by key.
func Lookup(key Key) (Runtime, bool) {
	for _, runtime := range catalog {
		if runtime.Key == key {
			return runtime, true
		}
	}
	return Runtime{}, false
}

// Valid reports whether key names a catalogued runtime.
func Valid(key Key) bool {
	_, ok := Lookup(key)
	return ok
}

// MCPRef names one tool on one MCP server as the server defines it.
type MCPRef struct {
	Server string `json:"server"`
	Name   string `json:"name"`
}

// RuntimeToolName is the name the runtime's hook reports for an MCP tool.
// Adapters split it back into a request's tool and server, recovering
// the server-defined name where the runtime sanitised it, so this
// spelling only matters for hook matchers and local allowlists.
func RuntimeToolName(key Key, ref MCPRef) string {
	runtime, _ := Lookup(key)
	switch runtime.MCPNaming {
	case MCPServerPrefixed:
		return ref.Server + "__" + ref.Name
	case MCPDoubleUnderscore:
		return "mcp__" + ref.Server + "__" + ref.Name
	case MCPDoubleUnderscoreSanitized:
		return "mcp__" + strings.TrimRight(sanitizeIdentifier(ref.Server), "_") + "__" + strings.TrimLeft(sanitizeIdentifier(ref.Name), "_")
	default:
		return ref.Name
	}
}

// sanitizeIdentifier mirrors Codex: anything outside ASCII letters, digits,
// and underscore becomes an underscore. Codex additionally appends a hash
// suffix when two servers or tools collide after sanitising or the name
// exceeds 128 bytes; those cases are rare and not reproduced.
func sanitizeIdentifier(value string) string {
	var out strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			out.WriteRune(r)
		} else {
			out.WriteByte('_')
		}
	}
	if out.Len() == 0 {
		return "_"
	}
	return out.String()
}

// CatchAllMatcher is the tool-name matcher every hook registration that
// takes one is installed with. The hook intercepts every call and sends
// it to the provider, whose pipeline decides which ones wait for a human,
// so the gated set changes centrally without touching the machine, and
// the runtime's own spelling of tool names never has to be reproduced
// here.
const CatchAllMatcher = ".*"

// HookMatcher returns the matcher a runtime's hook registration carries:
// the catch-all for runtimes whose registration takes one, empty for the
// rest (their plugins consult the adapter on every call already).
func HookMatcher(key Key) string {
	runtime, ok := Lookup(key)
	if !ok || !runtime.HookMatcher {
		return ""
	}
	return CatchAllMatcher
}

// Source says where a tool came from.
type Source string

const (
	SourceBuiltin Source = "builtin"
	SourceMCP     Source = "mcp"
)

// SelectedTool is one tool a person chose to gate, enough to phrase a
// command that makes the runtime call it.
type SelectedTool struct {
	Name   string   `json:"name"`
	Source Source   `json:"source"`
	Server string   `json:"server,omitempty"`
	Kind   ToolKind `json:"kind,omitempty"`
}

// Command is a shell one-liner a person pastes to make the runtime call a
// gated tool, plus a note about what to expect.
type Command struct {
	Shell string `json:"shell"`
	Note  string `json:"note,omitempty"`
}

// The test prompts ask for a harmless call whose arguments are known in
// advance, so onboarding can check the call against the active rules
// before it shows the command.
const (
	TestShellCommand = "echo hello from withHuman"
	TestFileName     = "withhuman-test.txt"
	TestFileContent  = "hello from withHuman"

	shellPrompt = "Run this shell command and show me the output: " + TestShellCommand
	editPrompt  = "Create a file named " + TestFileName + " in the current directory containing the single line: " + TestFileContent
)

func mcpPrompt(tool SelectedTool) string {
	return fmt.Sprintf("Call the %s tool from the %s MCP server with harmless test arguments and report the result", tool.Name, tool.Server)
}

// TestCommand phrases the command onboarding shows for a runtime and a
// gated tool, with the stock prompt for the tool's kind. It reports false
// for a runtime it does not know.
//
// Claude Code print mode auto-denies tools that need local permission, and
// an approved withHuman request is passthrough rather than an allow, so
// the tool is allowlisted on the command line: the person still sees the
// org approval first and the local permission model stays intact.
func TestCommand(key Key, tool SelectedTool) (Command, bool) {
	prompt := promptFor(tool)
	switch key {
	case ClaudeCode:
		return Command{
			Shell: fmt.Sprintf("claude -p %s --allowedTools %s", shellQuote(prompt), shellQuote(allowlistName(key, tool))),
			Note:  "Run it in any directory. The request appears below as soon as Claude Code calls the tool.",
		}, true
	case Codex:
		return Command{Shell: "codex exec --skip-git-repo-check " + shellQuote(prompt)}, true
	case OpenClaw:
		return Command{
			Shell: "openclaw agent --message " + shellQuote(prompt),
			Note:  "The Gateway must be running with the withHuman plugin loaded. Restart it if it was already open.",
		}, true
	case Pi:
		return Command{Shell: "pi -p " + shellQuote(prompt)}, true
	case Hermes:
		return Command{
			Shell: "hermes chat -q " + shellQuote(prompt),
			Note:  "Start a new Hermes session so it loads the withHuman plugin.",
		}, true
	case DeepSeek:
		return Command{
			Shell: "dsh -p " + shellQuote(prompt),
			Note:  "Run it under the profile that mounts the withHuman hooks bridge.",
		}, true
	default:
		return Command{}, false
	}
}

func promptFor(tool SelectedTool) string {
	if tool.Source == SourceMCP {
		return mcpPrompt(tool)
	}
	if tool.Kind == ToolEdit {
		return editPrompt
	}
	return shellPrompt
}

func allowlistName(key Key, tool SelectedTool) string {
	if tool.Source == SourceMCP {
		return RuntimeToolName(key, MCPRef{Server: tool.Server, Name: tool.Name})
	}
	return tool.Name
}

// shellQuote wraps a value in double quotes, escaping what the shell
// would otherwise interpret. Prompts are plain prose, so this stays small.
func shellQuote(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`")
	return `"` + replacer.Replace(value) + `"`
}
