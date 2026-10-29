// Package discovery lists runtime tools for guided onboarding. Runtime
// detection and all installation behavior belong to the imported AAP library.
package discovery

import (
	"context"
	"fmt"
	"github.com/withHumanAI/withHuman/internal/cli/configfile"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/withHumanAI/withHuman/agentruntime"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/cli/mcpclient"
	"github.com/withHumanAI/withHuman/internal/cli/mcpservers"
)

// Env is everything an adapter reads from the machine. Zero-value
// function fields fall back to the real process environment.
type Env struct {
	// Home is the user's home directory; runtime homes hang off it.
	Home string
	// Path is the executable search path used when LookPath is nil.
	Path []string
	// ConfigDir is withhuman's own configuration directory.
	ConfigDir string
	// Binary is the absolute path written into every hook.
	Binary string
	// Cwd is the directory setup was run from; project-scoped MCP
	// configuration is read relative to it.
	Cwd string
	// LookPath finds an executable by name.
	LookPath func(name string) (string, error)
	// Run executes a command and returns its combined output.
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
	// Getenv reads an environment variable.
	Getenv func(key string) string
	// ProviderServers are the MCP servers the provider offers its agents
	// (the hosted gateway), which the CLI registers with the runtime. Their
	// tools are the provider's own catalogue, so discovery leaves them out.
	ProviderServers []domain.AgentMCPServer
}

// providerServer reports whether a configured server is one the provider
// offered, by the name the CLI registered it under or by its URL.
func (e Env) providerServer(name string, spec mcpclient.Spec) bool {
	if mcpservers.IsRelay(spec.Command, spec.Args) {
		return true
	}
	for _, server := range e.ProviderServers {
		if server.Name == name {
			return true
		}
	}
	return false
}

// ownServers drops the provider's servers from a runtime's list.
func (e Env) ownServers(servers []mcpServer) []mcpServer {
	out := servers[:0]
	for _, server := range servers {
		if !e.providerServer(server.Name, server.Spec) {
			out = append(out, server)
		}
	}
	return out
}

func (e Env) lookPath(name string) (string, error) {
	if e.LookPath != nil {
		return e.LookPath(name)
	}
	for _, dir := range e.Path {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s not found on PATH", name)
}

func (e Env) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if e.Run != nil {
		return e.Run(ctx, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "PATH="+strings.Join(e.Path, string(os.PathListSeparator)))
	return cmd.CombinedOutput()
}

func (e Env) getenv(key string) string {
	if e.Getenv != nil {
		return e.Getenv(key)
	}
	return os.Getenv(key)
}

// Detection, Discovered, and InstallReport are the wire shapes the CLI
// sends the provider; adapters produce them directly.
type (
	Detection  = domain.OnboardingDetection
	Discovered = domain.OnboardingReportedTools
)

// Adapter discovers tools for guided onboarding. Installation belongs to AAP.
type Adapter interface {
	Key() agentruntime.Key
	Detect(Env) (Detection, error)
	Discover(context.Context, Env, Detection) (Discovered, error)
}

// All returns every adapter in catalog order.
func All() []Adapter {
	return []Adapter{claudeCode{}, codex{}, openClaw{}, pi{}, hermes{}, deepSeek{}, gemini{}, antigravity{}}
}

// Lookup finds an adapter by runtime key.
func Lookup(key agentruntime.Key) (Adapter, bool) {
	for _, adapter := range All() {
		if adapter.Key() == key {
			return adapter, true
		}
	}
	return nil, false
}

// DetectAll runs every adapter's detection in catalog order.
func DetectAll(env Env) ([]Detection, error) {
	detections := make([]Detection, 0, len(All()))
	for _, adapter := range All() {
		det, err := adapter.Detect(env)
		if err != nil {
			return nil, fmt.Errorf("detect %s: %w", adapter.Key(), err)
		}
		detections = append(detections, det)
	}
	return detections, nil
}

// exists reports whether a path exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// builtinTools reports a runtime's catalog tools.
func builtinTools(key agentruntime.Key) []domain.OnboardingReportedTool {
	runtime, _ := agentruntime.Lookup(key)
	tools := make([]domain.OnboardingReportedTool, 0, len(runtime.Builtins))
	for _, builtin := range runtime.Builtins {
		tools = append(tools, domain.OnboardingReportedTool{Name: builtin.Name, Description: builtin.Description})
	}
	return tools
}

// mcpServer is one configured MCP server, as read from a runtime's files.
type mcpServer struct {
	Name   string
	Source string
	Spec   mcpclient.Spec
}

func (s mcpServer) transport() string {
	if s.Spec.URL != "" {
		return "http"
	}
	return "stdio"
}

// detectionServers renders configured servers into the detection report.
func detectionServers(servers []mcpServer) []domain.OnboardingMCPServer {
	out := make([]domain.OnboardingMCPServer, 0, len(servers))
	for _, server := range servers {
		out = append(out, domain.OnboardingMCPServer{Name: server.Name, Transport: server.transport(), Source: server.Source})
	}
	return out
}

// discoverMCPConcurrency bounds how many servers are spawned at once.
const discoverMCPConcurrency = 4

// discover lists a runtime's built-in tools and every configured server's
// tools. A server that fails to list lands in Errors; the rest continue.
func discover(ctx context.Context, key agentruntime.Key, servers []mcpServer) Discovered {
	out := Discovered{Builtin: builtinTools(key), MCP: map[string][]domain.OnboardingReportedTool{}}
	if len(servers) == 0 {
		return out
	}
	type outcome struct {
		name  string
		tools []domain.OnboardingReportedTool
		err   error
	}
	results := make([]outcome, len(servers))
	var wg sync.WaitGroup
	slots := make(chan struct{}, discoverMCPConcurrency)
	for i, server := range servers {
		wg.Add(1)
		go func(i int, server mcpServer) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			tools, err := mcpclient.ListTools(ctx, server.Spec)
			results[i] = outcome{name: server.Name, tools: reportTools(tools), err: err}
		}(i, server)
	}
	wg.Wait()
	for _, result := range results {
		if result.err != nil {
			out.Errors = append(out.Errors, domain.OnboardingDiscoveryError{Server: result.name, Message: result.err.Error()})
			continue
		}
		out.MCP[result.name] = result.tools
	}
	return out
}

func reportTools(tools []mcpclient.Tool) []domain.OnboardingReportedTool {
	out := make([]domain.OnboardingReportedTool, 0, len(tools))
	for _, tool := range tools {
		reported := domain.OnboardingReportedTool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema, DisplayName: tool.Title}
		if a := tool.Annotations; a != nil {
			if reported.DisplayName == "" {
				reported.DisplayName = a.Title
			}
			reported.Annotations = &domain.OnboardingToolAnnotations{
				Title:        a.Title,
				ReadOnlyHint: a.ReadOnlyHint, DestructiveHint: a.DestructiveHint,
				IdempotentHint: a.IdempotentHint, OpenWorldHint: a.OpenWorldHint,
			}
		}
		out = append(out, reported)
	}
	return out
}

// claudeStyleServers reads an `mcpServers` object in the shape Claude Code
// (and the runtimes that copied it) use: stdio entries carry
// command/args/env, remote entries carry type http|sse with url/headers.
func claudeStyleServers(object *configfile.JSONObject, source, cwd string) []mcpServer {
	var servers []mcpServer
	for _, name := range object.Keys() {
		entry, ok := object.ObjectIfPresent(name)
		if !ok {
			continue
		}
		spec := mcpclient.Spec{Name: name, Cwd: cwd}
		switch strings.ToLower(entry.String("type")) {
		case "http", "sse", "streamable-http", "streamable_http":
			spec.URL = entry.String("url")
			spec.Headers = entry.StringMap("headers")
		default:
			if url := entry.String("url"); url != "" && entry.String("command") == "" {
				spec.URL = url
				spec.Headers = entry.StringMap("headers")
				break
			}
			spec.Command = entry.String("command")
			spec.Args = entry.Strings("args")
			spec.Env = entry.StringMap("env")
			if cwd := entry.String("cwd"); cwd != "" {
				spec.Cwd = cwd
			}
		}
		if spec.Command == "" && spec.URL == "" {
			continue
		}
		servers = append(servers, mcpServer{Name: name, Source: source, Spec: spec})
	}
	return servers
}

// dedupeServers keeps the first server of each name, in order.
func dedupeServers(servers []mcpServer) []mcpServer {
	seen := map[string]bool{}
	out := servers[:0]
	for _, server := range servers {
		if seen[server.Name] {
			continue
		}
		seen[server.Name] = true
		out = append(out, server)
	}
	return out
}

// sortedKeys is a small helper for deterministic iteration.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// DefaultEnv describes the real machine: the user's home, the process
// PATH and working directory, and the given config directory and binary.
func DefaultEnv(configDir, binary string) Env {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	return Env{
		Home: home, Path: filepath.SplitList(os.Getenv("PATH")), ConfigDir: configDir, Binary: binary, Cwd: cwd,
		LookPath: exec.LookPath,
	}
}
