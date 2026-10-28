package onboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/withHumanAI/withHuman/agentruntime"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/cli/clitest"
	"github.com/withHumanAI/withHuman/internal/cli/credential"
	"github.com/withHumanAI/withHuman/internal/cli/discovery"
	"github.com/withHumanAI/withHuman/internal/cli/installation"
	"github.com/withHumanAI/withHuman/internal/cli/mcpservers"
)

func TestMain(m *testing.M) {
	clitest.MaybeRunMCPFixture()
	os.Exit(m.Run())
}

const testBinary = "/opt/withhuman/bin/withhuman"

// fixture is a machine with Claude Code and Hermes installed: fake
// executables on a private PATH, a Claude Code user config that registers
// the helper-process MCP fixture, and a Hermes config with one hook.
type fixture struct {
	home, config, bin string
	env               discovery.Env
	settings          string
	hermesConfig      string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{home: filepath.Join(root, "home"), config: filepath.Join(root, "config", "withhuman"), bin: filepath.Join(root, "bin")}
	for _, dir := range []string{filepath.Join(f.home, ".claude"), filepath.Join(f.home, ".hermes"), f.bin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExecutable(t, filepath.Join(f.bin, "claude"), "#!/bin/sh\necho '2.1.0 (Claude Code)'\n")
	writeExecutable(t, filepath.Join(f.bin, "hermes"), "#!/bin/sh\necho 'hermes 0.21.0'\n")

	f.settings = filepath.Join(f.home, ".claude", "settings.json")
	write(t, f.settings, "{\n  \"permissions\": {\n    \"allow\": [\n      \"Bash(git status)\"\n    ]\n  }\n}\n")
	command, env := clitest.MCPFixtureCommand()
	claudeJSON, _ := json.MarshalIndent(map[string]any{
		"numStartups": 3,
		"mcpServers": map[string]any{
			"fixture": map[string]any{"command": command, "args": []string{}, "env": env},
			"broken":  map[string]any{"command": filepath.Join(f.bin, "does-not-exist")},
		},
	}, "", "  ")
	write(t, filepath.Join(f.home, ".claude.json"), string(claudeJSON)+"\n")
	f.hermesConfig = filepath.Join(f.home, ".hermes", "config.yaml")
	write(t, f.hermesConfig, "# Hermes\nmodel: hermes-4\nhooks:\n  pre_tool_call:\n    - command: audit --pre\n      timeout: 10\n")

	f.env = discovery.Env{Home: f.home, ConfigDir: f.config, Binary: testBinary, Path: []string{f.bin}, Cwd: root, Getenv: func(string) string { return "" }}
	return f
}

func TestSetupEndToEnd(t *testing.T) {
	provider := clitest.NewProvider(t)
	provider.AgentMCPServers = []domain.AgentMCPServer{{Name: "withhuman", URL: provider.URL() + "/gateway/mcp", Label: "withHuman MCP gateway"}}
	f := newFixture(t)
	var stdout, stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Options{
			URL: provider.URL(), Code: provider.SetupCode, Binary: testBinary, JSON: true,
			ConfigDir: f.config, Version: "test", Env: f.env, Stdout: &stdout, Stderr: &stderr, PollWait: 2 * time.Second,
		})
	}()

	session := provider.WaitFor(t, domain.OnboardingDetected, 20*time.Second)
	if session.Machine.CLIVersion != "test" || session.Machine.Hostname == "" || session.Machine.OS == "" {
		t.Fatalf("machine = %+v", session.Machine)
	}
	if len(session.Detections) != len(agentruntime.Keys()) {
		t.Fatalf("got %d detections, want one per runtime", len(session.Detections))
	}
	installed := map[string]domain.OnboardingDetection{}
	for _, det := range session.Detections {
		if det.Installed {
			installed[det.Runtime] = det
		}
	}
	if len(installed) != 2 {
		t.Fatalf("installed runtimes = %v, want claude-code and hermes", installed)
	}
	claude := installed["claude-code"]
	if claude.Version != "2.1.0 (Claude Code)" || claude.BinaryPath != filepath.Join(f.bin, "claude") || claude.AlreadyHooked {
		t.Fatalf("claude-code detection = %+v", claude)
	}
	servers := map[string]string{}
	for _, server := range claude.MCPServers {
		servers[server.Name] = server.Transport
	}
	if len(servers) != 2 || servers["fixture"] != "stdio" || servers["broken"] != "stdio" {
		t.Fatalf("claude-code mcp servers = %+v", claude.MCPServers)
	}
	if hermes := installed["hermes"]; hermes.Version != "hermes 0.21.0" || hermes.ConfigPath != f.hermesConfig {
		t.Fatalf("hermes detection = %+v", hermes)
	}

	if err := provider.Human.SelectAgents("claude-code", "hermes"); err != nil {
		t.Fatal(err)
	}
	session = provider.WaitFor(t, domain.OnboardingToolsDiscovered, 30*time.Second)
	tools := map[string]domain.OnboardingTool{}
	for _, tool := range session.Tools["claude-code"] {
		tools[tool.Source+":"+tool.Name] = tool
	}
	if bash := tools["builtin:Bash"]; !bash.Suggested || bash.Kind != "shell" {
		t.Fatalf("Bash = %+v", bash)
	}
	del := tools["mcp:delete_customer"]
	if del.Server != "fixture" || !del.Suggested || del.Reason != "annotation_destructive" || del.Annotations == nil || del.Annotations.DestructiveHint == nil || !*del.Annotations.DestructiveHint {
		t.Fatalf("delete_customer = %+v", del)
	}
	if get := tools["mcp:get_customer"]; get.Suggested || get.Annotations == nil || get.Annotations.ReadOnlyHint == nil || !*get.Annotations.ReadOnlyHint {
		t.Fatalf("get_customer = %+v", get)
	}
	if _, ok := tools["mcp:echo"]; !ok {
		t.Fatalf("echo missing from %v", session.Tools["claude-code"])
	}
	if errs := session.DiscoveryErrors["claude-code"]; len(errs) != 1 || errs[0].Server != "broken" {
		t.Fatalf("discovery errors = %+v, want one for the broken server", errs)
	}
	if len(session.Tools["hermes"]) != 6 {
		t.Fatalf("hermes tools = %d, want the six built-ins", len(session.Tools["hermes"]))
	}

	if err := provider.Human.Provision(); err != nil {
		t.Fatal(err)
	}
	session = provider.WaitFor(t, domain.OnboardingInstalled, 30*time.Second)
	if err := <-done; err != nil {
		t.Fatalf("setup returned %v\nstderr:\n%s\nstdout:\n%s", err, stderr.String(), stdout.String())
	}

	// Credentials: one per runtime, 0600, carrying the minted tokens.
	minted := provider.Credentials()
	for _, runtime := range []string{"claude-code", "hermes"} {
		cred, err := credential.Load(f.config, runtime)
		if err != nil {
			t.Fatalf("credential %s: %v", runtime, err)
		}
		if cred.Token != minted[runtime] || cred.ProviderURL != provider.URL() || cred.Runtime != runtime || cred.AgentSlug != runtime {
			t.Fatalf("credential %s = %+v (minted %s)", runtime, cred, minted[runtime])
		}
		info, err := os.Stat(credential.Path(f.config, runtime))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("credential mode = %o", info.Mode().Perm())
		}
	}

	// Config edits.
	wantSettings := strings.ReplaceAll(readFile(t, filepath.Join("testdata", "settings.json.golden")), "{{BINARY}}", testBinary)
	wantSettings = strings.ReplaceAll(wantSettings, "{{CONFIG}}", filepath.Join(f.config, "aap"))
	if got := readFile(t, f.settings); got != wantSettings {
		t.Fatalf("settings.json:\n%s\nwant:\n%s", got, wantSettings)
	}
	wantHermes := strings.ReplaceAll(readFile(t, filepath.Join("testdata", "hermes.yaml.golden")), "{{BINARY}}", testBinary)
	wantHermes = strings.ReplaceAll(wantHermes, "{{CONFIG}}", filepath.Join(f.config, "aap"))
	wantHermes = strings.ReplaceAll(wantHermes, "{{WITHHUMAN_CONFIG}}", f.config)
	if got := readFile(t, f.hermesConfig); got != wantHermes {
		t.Fatalf("hermes config.yaml:\n%s\nwant:\n%s", got, wantHermes)
	}
	// The provider's gateway is registered with Claude Code as the relay,
	// beside the person's own servers, without the credential.
	claudeJSON := readFile(t, filepath.Join(f.home, ".claude.json"))
	for _, want := range []string{`"fixture": {`, `"withhuman": {`, `"command": "` + testBinary + `"`, `"claude-code",`, `"WITHHUMAN_CONFIG_DIR": "` + f.config + `"`} {
		if !strings.Contains(claudeJSON, want) {
			t.Fatalf(".claude.json lacks %s:\n%s", want, claudeJSON)
		}
	}
	if strings.Contains(claudeJSON, "whc_") {
		t.Fatal("credential written into .claude.json")
	}

	// Install results on the session; the test commands wait for the rules.
	if len(session.InstallResults) != 2 {
		t.Fatalf("install results = %+v", session.InstallResults)
	}
	for _, result := range session.InstallResults {
		// Claude Code: the hook file and the MCP registration. Hermes: the
		// plugin's three files, its aap.json, and config.yaml twice.
		want := map[string]int{"claude-code": 2, "hermes": 6}[result.Runtime]
		if !result.OK || result.Manual || len(result.Changes) != want {
			t.Fatalf("install result = %+v", result)
		}
	}
	for _, name := range []string{"plugin.yaml", "__init__.py", "README.md", "aap.json"} {
		if _, err := os.Stat(filepath.Join(f.home, ".hermes", "plugins", "withhuman", name)); err != nil {
			t.Fatalf("Hermes plugin file %s: %v", name, err)
		}
	}
	manager, err := f.env.Manager()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"claude-code", "hermes"} {
		adapter, _ := manager.Lookup(key)
		status, err := adapter.Status()
		if err != nil || !status.Complete {
			t.Fatalf("incomplete %s: %v", key, err)
		}
	}

	// The JSON stream ends with the install summary.
	events := parseEvents(t, stdout.String())
	for _, want := range []string{"connected", "detected", "waiting_for_agents", "discovering", "tools_reported", "enrolled", "runtime_installed", "installed"} {
		if !events[want] {
			t.Errorf("missing %q event in\n%s", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("--json should keep stderr quiet, got %q", stderr.String())
	}

	// Eject puts everything back.
	for _, key := range []string{"claude-code", "hermes"} {
		adapter, _ := manager.Lookup(key)
		if err := adapter.Uninstall(); err != nil {
			t.Fatal(err)
		}
		cred, err := credential.Load(f.config, key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := installation.Remove(key, *cred, mcpservers.Env{Home: f.home, Binary: testBinary, ConfigDir: f.config}); err != nil {
			t.Fatal(err)
		}
		if err := credential.Delete(f.config, key); err != nil {
			t.Fatal(err)
		}
	}

	if got := readFile(t, f.settings); got != "{\n  \"permissions\": {\n    \"allow\": [\n      \"Bash(git status)\"\n    ]\n  }\n}\n" {
		t.Fatalf("settings.json after eject:\n%s", got)
	}
	if got := readFile(t, f.hermesConfig); !strings.HasPrefix(got, "# Hermes\nmodel: hermes-4\n") || strings.Contains(got, "withhuman") {
		t.Fatalf("hermes config after eject:\n%s", got)
	}
	if agents, _ := credential.List(f.config); len(agents) != 0 {
		t.Fatalf("credentials left after eject: %v", agents)
	}
	if got := readFile(t, filepath.Join(f.home, ".claude.json")); strings.Contains(got, "withhuman") || !strings.Contains(got, `"fixture"`) {
		t.Fatalf(".claude.json after eject:\n%s", got)
	}

}

func TestSetupRediscoversWhenAgentsChange(t *testing.T) {
	provider := clitest.NewProvider(t)
	f := newFixture(t)
	var stdout, stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Options{
			URL: provider.URL(), Code: provider.SetupCode, Binary: testBinary,
			ConfigDir: f.config, Env: f.env, Stdout: &stdout, Stderr: &stderr, PollWait: 2 * time.Second,
		})
	}()
	provider.WaitFor(t, domain.OnboardingDetected, 20*time.Second)
	if err := provider.Human.SelectAgents("hermes"); err != nil {
		t.Fatal(err)
	}
	session := provider.WaitFor(t, domain.OnboardingToolsDiscovered, 30*time.Second)
	if _, ok := session.Tools["claude-code"]; ok {
		t.Fatal("claude-code tools reported before it was selected")
	}
	if err := provider.Human.SelectAgents("hermes", "claude-code"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 30*time.Second, func() bool {
		s := provider.Session()
		_, ok := s.Tools["claude-code"]
		return s.Status == domain.OnboardingToolsDiscovered && ok
	})
	if err := provider.Human.Provision(); err != nil {
		t.Fatal(err)
	}
	provider.WaitFor(t, domain.OnboardingInstalled, 30*time.Second)
	if err := <-done; err != nil {
		t.Fatalf("setup: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Finish in the browser") {
		t.Fatalf("summary missing:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Agent selection changed") {
		t.Fatalf("rediscovery not reported:\n%s", stderr.String())
	}
}

func TestSetupReportsErrorsToTheSession(t *testing.T) {
	provider := clitest.NewProvider(t)
	f := newFixture(t)
	failing := &failingAdapter{}
	err := Run(context.Background(), Options{
		URL: provider.URL(), Code: provider.SetupCode, Binary: testBinary, ConfigDir: f.config, Env: f.env,
		Adapters: []discovery.Adapter{failing}, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, PollWait: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("err = %v", err)
	}
	session := provider.Session()
	if session.LastError == nil || session.LastError.Code != "detect_failed" || !strings.Contains(session.LastError.Message, "disk on fire") {
		t.Fatalf("last_error = %+v", session.LastError)
	}
	if session.Status != domain.OnboardingConnected {
		t.Fatalf("status = %s, want connected (the error does not advance the session)", session.Status)
	}
}

func TestSetupRejectsBadCode(t *testing.T) {
	provider := clitest.NewProvider(t)
	f := newFixture(t)
	err := Run(context.Background(), Options{URL: provider.URL(), Code: "WRONG-CODE-HERE", Binary: testBinary, ConfigDir: f.config, Env: f.env, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "setup code was not accepted") {
		t.Fatalf("err = %v", err)
	}
}

type failingAdapter struct{}

func (failingAdapter) Key() agentruntime.Key { return agentruntime.ClaudeCode }
func (failingAdapter) Detect(discovery.Env) (discovery.Detection, error) {
	return discovery.Detection{}, errors.New("disk on fire")
}
func (failingAdapter) Discover(context.Context, discovery.Env, discovery.Detection) (discovery.Discovered, error) {
	return discovery.Discovered{}, nil
}

func parseEvents(t *testing.T, stream string) map[string]bool {
	t.Helper()
	events := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(stream), "\n") {
		var event struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("bad JSON event %q: %v", line, err)
		}
		events[event.Event] = true
	}
	return events
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func writeExecutable(t *testing.T, path, script string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}
