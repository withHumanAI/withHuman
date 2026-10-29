// Package cli is the withHuman enrollment and onboarding CLI. AAP owns adapters.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/agentapprovalprotocol/agentapprovalprotocol/adapters"
	"github.com/withHumanAI/withHuman/agentruntime"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/cli/catalogclient"
	"github.com/withHumanAI/withHuman/internal/cli/credential"
	"github.com/withHumanAI/withHuman/internal/cli/discovery"
	"github.com/withHumanAI/withHuman/internal/cli/enroll"
	"github.com/withHumanAI/withHuman/internal/cli/installation"
	"github.com/withHumanAI/withHuman/internal/cli/mcpproxy"
	"github.com/withHumanAI/withHuman/internal/cli/mcpservers"
	"github.com/withHumanAI/withHuman/internal/cli/onboard"
	"github.com/withHumanAI/withHuman/internal/cli/runtimeconfig"
	"github.com/withHumanAI/withHuman/internal/cli/upgrade"
)

const defaultProviderURL = "https://app.withhuman.ai"
const usageText = `withhuman: human approval for agent tool calls

Usage:
  withhuman onboard --url <origin> --code <code> [--binary <path>] [--wait] [--json]
  withhuman agent discover [--json]
  withhuman agent refresh-tools <runtime> --agent <slug>
  withhuman agent install [<runtime>] [--agent <slug>] [--url <origin>] [--instance <name>]
  withhuman agent eject <runtime> [--yes]
  withhuman upgrade
  withhuman version
`

func Main(version string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// The sudo helper only replaces its own executable. Do not resolve or
	// access the administrator's CLI configuration in this process.
	if len(args) > 0 && args[0] == upgrade.InstallCommand {
		if err := upgrade.InstallHelper(ctx, args[1:], stderr); err != nil {
			fmt.Fprintln(stderr, "withhuman:", err)
			return 1
		}
		return 0
	}
	dir, err := ConfigDir()
	if err != nil {
		fmt.Fprintln(stderr, "withhuman:", err)
		return 1
	}
	a := &app{version: version, configDir: dir, stdin: stdin, stdout: stdout, stderr: stderr, getenv: os.Getenv}
	code, err := a.run(ctx, args)
	if err != nil {
		fmt.Fprintln(stderr, "withhuman:", err)
	}
	return code
}
func ConfigDir() (string, error) {
	if dir := os.Getenv("WITHHUMAN_CONFIG_DIR"); dir != "" {
		return filepath.Abs(dir)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "withhuman"), nil
}

type app struct {
	version, configDir string
	stdin              io.Reader
	stdout, stderr     io.Writer
	getenv             func(string) string
	env                adapters.Environment
	http               *http.Client
	openBrowser        func(context.Context, string) error
	pollWait           time.Duration
	updateEnv          upgrade.Environment
}

var errUsage = errors.New("usage")

func (a *app) run(ctx context.Context, args []string) (int, error) {
	if len(args) == 0 {
		fmt.Fprint(a.stderr, usageText)
		return 2, nil
	}
	var err error
	a.updateNotice(ctx, args)
	switch args[0] {
	case "onboard":
		err = a.onboard(ctx, args[1:])
	case "agent":
		err = a.agent(ctx, args[1:])
	case "hook":
		err = a.hook(ctx, args[1:])
	case "mcp":
		err = a.mcp(ctx, args[1:])
	case "upgrade":
		fs := a.flags("upgrade")
		if err = fs.Parse(args[1:]); err == nil {
			if fs.NArg() != 0 {
				err = errUsage
			} else {
				err = a.updater().Run(ctx)
			}
		}
	case "version", "--version", "-v":
		if len(args) != 1 {
			return 2, errUsage
		}
		fmt.Fprintln(a.stdout, "withhuman", a.version)
	case "help", "--help", "-h":
		fmt.Fprint(a.stdout, usageText)
	default:
		fmt.Fprintf(a.stderr, "withhuman: unknown command %q\n\n%s", args[0], usageText)
		return 2, nil
	}
	if errors.Is(err, errUsage) {
		fmt.Fprint(a.stderr, usageText)
		return 2, nil
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0, nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}
func (a *app) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("withhuman "+name, flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	return fs
}
func (a *app) onboard(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(a.stdout, "Visit https://app.withhuman.ai/welcome to get started, then copy and run the setup command shown there.")
		return nil
	}
	fs := a.flags("onboard")
	opts := onboard.Options{ConfigDir: a.configDir, Version: a.version, Stdout: a.stdout, Stderr: a.stderr}
	fs.StringVar(&opts.URL, "url", "", "provider origin from the welcome page")
	fs.StringVar(&opts.Code, "code", "", "one-time onboarding code")
	fs.StringVar(&opts.Binary, "binary", "", "executable path to install into hooks")
	fs.BoolVar(&opts.Wait, "wait", false, "wait for browser completion")
	fs.BoolVar(&opts.JSON, "json", false, "emit JSON progress")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || opts.URL == "" || opts.Code == "" {
		return errUsage
	}
	origin, err := installation.Origin(opts.URL)
	if err != nil {
		return err
	}
	opts.URL = origin
	return onboard.Run(ctx, opts)
}
func (a *app) agent(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	manager, err := installation.New(a.configDir, a.env)
	if err != nil {
		return err
	}
	switch args[0] {
	case "discover":
		fs := a.flags("agent discover")
		asJSON := fs.Bool("json", false, "emit JSON")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errUsage
		}
		statuses := []adapters.Status{}
		var errs []error
		for _, info := range adapters.All() {
			adapter, _ := manager.Lookup(info.Key)
			s, err := adapter.Status()
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", info.Key, err))
				continue
			}
			if s.Installed {
				statuses = append(statuses, s)
			}
		}
		if *asJSON {
			if err := json.NewEncoder(a.stdout).Encode(statuses); err != nil {
				return err
			}
		} else {
			for _, s := range statuses {
				state := "not installed"
				if s.Complete {
					state = "installed"
				} else if s.Configured {
					state = "incomplete"
				}
				fmt.Fprintf(a.stdout, "%s: runtime found; adapter %s\n", s.Runtime, state)
			}
			if len(statuses) == 0 {
				fmt.Fprintln(a.stdout, "No supported runtimes found.")
			}
		}
		return errors.Join(errs...)
	case "refresh-tools":
		return a.refreshToolsCommand(ctx, args[1:])
	case "install":
		return a.install(ctx, manager, args[1:])
	case "eject":
		if len(args) < 2 {
			return errUsage
		}
		adapter, err := manager.Lookup(args[1])
		if err != nil {
			return err
		}
		return a.eject(adapter, args[2:])
	default:
		return errUsage
	}
}
func (a *app) install(ctx context.Context, manager *adapters.Manager, args []string) error {
	runtime := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		runtime, args = args[0], args[1:]
	}
	fs := a.flags("agent install")
	agent := fs.String("agent", "", "existing agent slug (otherwise choose or create in the browser)")
	originFlag := fs.String("url", "", "provider origin")
	instance := fs.String("instance", "", "instance name (default: hostname and a unique suffix)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errUsage
	}
	if *agent != "" {
		*agent = strings.TrimSpace(*agent)
		if *agent == "" {
			return errors.New("agent slug must not be blank")
		}
	}
	if runtime == "" {
		selected, err := a.chooseRuntime(ctx, manager)
		if err != nil {
			return err
		}
		runtime = selected
	}
	adapter, err := manager.Lookup(runtime)
	if err != nil {
		return err
	}
	det, err := adapter.Detect()
	if err != nil {
		return err
	}
	if !det.Installed {
		return fmt.Errorf("%s was not found; install the runtime first", adapter.Key())
	}
	saved, err := credential.Load(a.configDir, adapter.Key())
	if err != nil && !errors.Is(err, credential.ErrNotFound) {
		return err
	}
	origin := *originFlag
	if origin == "" {
		origin = a.getenv("WITHHUMAN_URL")
	}
	if origin == "" && saved != nil {
		origin = saved.ProviderURL
	}
	if origin == "" {
		origin = defaultProviderURL
	}
	origin, err = installation.Origin(origin)
	if err != nil {
		return err
	}
	client := enroll.HTTPClient(a.http)
	// What the provider offers every agent (the hosted gateway) is public
	// and read before enrollment, so a provider the CLI cannot read stops
	// the run before a credential is minted.
	provider, err := runtimeconfig.Fetch(ctx, client, origin)
	if err != nil {
		return err
	}
	reuse := saved != nil && saved.ProviderURL == origin && saved.ExpiresAt.After(time.Now()) && (*agent == "" || *agent == saved.AgentSlug) && (*instance == "" || *instance == saved.InstanceName)
	if reuse {
		reuse, err = enroll.Accepts(ctx, client, *saved)
		if err != nil {
			return err
		}
	}
	if !reuse {
		cred, err := enroll.Enroll(ctx, enroll.Options{BaseURL: origin, Runtime: adapter.Key(), RequestedAgentSlug: *agent, InstanceName: *instance, HTTP: client, Stdout: a.stdout, OpenBrowser: a.openBrowser, PollWait: a.pollWait})
		if err != nil {
			return err
		}
		if _, err := credential.Save(a.configDir, adapter.Key(), cred); err != nil {
			return err
		}
		saved = &cred
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	mcpEnv, err := a.mcpEnv()
	if err != nil {
		return err
	}
	result, err := installation.Install(manager, a.configDir, adapter.Key(), *saved, provider.AgentMCPServers, mcpEnv)
	for _, change := range result.Changes {
		fmt.Fprintf(a.stdout, "%s: %s\n", change.Action, change.Path)
	}
	for _, note := range result.Notes {
		fmt.Fprintln(a.stdout, note)
	}
	if result.Manual {
		fmt.Fprintf(a.stdout, "%s\n\n%s\n", result.Message, result.Snippet)
	}
	if err != nil {
		return err
	}
	if !result.Complete {
		return errors.New("adapter registration is incomplete")
	}
	fmt.Fprintf(a.stdout, "Installed %s as %s under agent %s. Approval rules are managed in withHuman.\n", adapter.Key(), saved.InstanceName, saved.AgentSlug)
	if err := a.refreshTools(ctx, adapter.Key(), *saved); err != nil {
		fmt.Fprintf(a.stderr, "Tool discovery could not be published. Run withhuman agent refresh-tools %s --agent %s to retry: %v\n", adapter.Key(), saved.AgentSlug, err)
	}
	return nil
}

// mcpEnv is the machine the provider's MCP servers are registered on: the
// relay entries pin this executable and configuration directory the way
// the hooks do.
func (a *app) mcpEnv() (mcpservers.Env, error) {
	home := a.env.Home
	if home == "" {
		resolved, err := os.UserHomeDir()
		if err != nil {
			return mcpservers.Env{}, err
		}
		home = resolved
	}
	binary := a.env.Executable
	if binary == "" {
		resolved, err := os.Executable()
		if err != nil {
			return mcpservers.Env{}, err
		}
		if linked, err := filepath.EvalSymlinks(resolved); err == nil {
			resolved = linked
		}
		binary = resolved
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		return mcpservers.Env{}, err
	}
	return mcpservers.Env{Home: home, Binary: binary, ConfigDir: a.configDir, Getenv: a.getenv}, nil
}

// mcp is the relay a runtime starts for one provider-offered MCP server:
// it forwards the runtime's frames to the server with the instance
// credential. That is the one the install stored, or, for a runtime plugin
// that keeps the agent key itself (the Gemini CLI extension), the key in
// WITHHUMAN_AGENT_TOKEN.
func (a *app) mcp(ctx context.Context, args []string) error {
	if len(args) != 2 {
		return errUsage
	}
	runtime, name := args[0], args[1]
	var client http.Client
	if a.http != nil {
		client = *a.http
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	endpoint, token, err := a.mcpTarget(ctx, &client, runtime, name)
	if err != nil {
		return err
	}
	// A tool call waits as long as its approval does, so no timeout; the
	// runtime closing stdin ends every call.
	client.Timeout = 0
	return mcpproxy.Run(ctx, a.stdin, a.stdout, runtime, endpoint, token, &client)
}

// mcpTarget is the server's endpoint and the credential to send it.
func (a *app) mcpTarget(ctx context.Context, client *http.Client, runtime, name string) (string, string, error) {
	if key := a.getenv("WITHHUMAN_AGENT_TOKEN"); key != "" {
		return a.mcpTargetForKey(ctx, client, name, key)
	}
	cred, err := credential.Load(a.configDir, runtime)
	if err != nil {
		return "", "", fmt.Errorf("%w; run withhuman agent install %s", err, runtime)
	}
	var server *domain.AgentMCPServer
	for i := range cred.MCPServers {
		if cred.MCPServers[i].Name == name {
			server = &cred.MCPServers[i]
		}
	}
	if server == nil {
		return "", "", fmt.Errorf("%s is not registered for %s; run withhuman agent install %s", name, runtime, runtime)
	}
	if !cred.ExpiresAt.After(time.Now()) {
		return "", "", fmt.Errorf("the %s instance credential has expired; run withhuman agent install %s", runtime, runtime)
	}
	return server.URL, cred.Token, nil
}

// mcpTargetForKey finds the server in the runtime-config of WITHHUMAN_URL
// (hosted withHuman by default). The key goes only to a server on that
// origin, never to one the configuration points elsewhere.
func (a *app) mcpTargetForKey(ctx context.Context, client *http.Client, name, key string) (string, string, error) {
	origin := a.getenv("WITHHUMAN_URL")
	if origin == "" {
		origin = defaultProviderURL
	}
	origin, err := installation.Origin(origin)
	if err != nil {
		return "", "", err
	}
	lookup := *client
	lookup.Timeout = 30 * time.Second
	provider, err := runtimeconfig.Fetch(ctx, &lookup, origin)
	if err != nil {
		return "", "", err
	}
	for _, server := range provider.AgentMCPServers {
		if server.Name != name {
			continue
		}
		if !sameOrigin(server.URL, origin) {
			return "", "", fmt.Errorf("%s is not served from %s", name, origin)
		}
		return server.URL, key, nil
	}
	return "", "", fmt.Errorf("%s does not offer %s", origin, name)
}

// sameOrigin reports whether raw is a URL on origin.
func sameOrigin(raw, origin string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.User == nil && strings.TrimRight(u.Scheme+"://"+u.Host, "/") == origin
}

// chooseRuntime only inspects the local machine; enrollment starts after a choice.
func (a *app) chooseRuntime(ctx context.Context, manager *adapters.Manager) (string, error) {
	var found []string
	for _, info := range adapters.All() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		adapter, err := manager.Lookup(info.Key)
		if err != nil {
			return "", err
		}
		detection, err := adapter.Detect()
		if err != nil {
			return "", err
		}
		if detection.Installed {
			found = append(found, info.Key)
		}
	}
	if len(found) == 0 {
		return "", errors.New("no supported runtimes found; install claude-code, codex, openclaw, pi, hermes, deepseek, gemini or antigravity first")
	}
	if len(found) == 1 {
		fmt.Fprintf(a.stdout, "Found %s.\n", found[0])
		return found[0], nil
	}
	if a.stdin == nil {
		return "", errors.New("choose a runtime explicitly: withhuman agent install <runtime>")
	}
	if file, ok := a.stdin.(*os.File); ok && !term.IsTerminal(int(file.Fd())) {
		return "", errors.New("terminal input is unavailable; use withhuman agent install <runtime>")
	}
	for i, key := range found {
		fmt.Fprintf(a.stdout, "%d. %s\n", i+1, key)
	}
	fmt.Fprint(a.stdout, "Choose a runtime (number, or q to cancel): ")
	type answer struct {
		text string
		err  error
	}
	answers := make(chan answer, 1)
	go func() { line, err := bufio.NewReader(a.stdin).ReadString('\n'); answers <- answer{line, err} }()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-answers:
		if result.err != nil {
			return "", errors.New("no runtime selected; use withhuman agent install <runtime>")
		}
		value := strings.TrimSpace(result.text)
		if value == "q" || value == "" {
			return "", errors.New("installation cancelled")
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > len(found) {
			return "", errors.New("invalid runtime selection; use a number from the list")
		}
		return found[n-1], nil
	}
}

func (a *app) eject(adapter *adapters.Adapter, args []string) error {
	fs := a.flags("agent eject " + adapter.Key())
	yes := fs.Bool("yes", false, "remove without prompting")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errUsage
	}
	if !*yes {
		fmt.Fprintf(a.stdout, "Remove the %s adapter and local credentials? [y/N] ", adapter.Key())
		line, _ := bufio.NewReader(a.stdin).ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer != "y" && answer != "yes" {
			return errors.New("eject cancelled")
		}
	}
	// The enrollment record says which MCP server entries are ours. A record
	// that cannot be read must not keep the hook in place, so eject goes on
	// and says what it left behind.
	saved, err := credential.Load(a.configDir, adapter.Key())
	if err != nil && !errors.Is(err, credential.ErrNotFound) {
		fmt.Fprintf(a.stderr, "The saved enrollment could not be read, so any MCP server entries the install added were left in place: %v\n", err)
	}
	if err := adapter.Uninstall(); err != nil {
		return err
	}
	if saved != nil {
		mcpEnv, err := a.mcpEnv()
		if err != nil {
			return err
		}
		removed, err := installation.Remove(adapter.Key(), *saved, mcpEnv)
		for _, change := range removed.Changes {
			fmt.Fprintf(a.stdout, "%s: %s\n", change.Action, change.Path)
		}
		for _, note := range removed.Notes {
			fmt.Fprintln(a.stdout, note)
		}
		if err != nil {
			return err
		}
	}
	if err := credential.Delete(a.configDir, adapter.Key()); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Removed %s. Revoke its instance credential in withHuman if it will no longer be used.\n", adapter.Key())
	return nil
}
func (a *app) hook(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errUsage
	}
	env := a.env
	env.ConfigDir = filepath.Join(a.configDir, "aap")
	if pinned := a.getenv("AAP_CONFIG_DIR"); pinned != "" {
		env.ConfigDir = pinned
	}
	manager, err := adapters.NewWithEnvironment(env)
	if err != nil {
		return err
	}
	adapter, err := manager.Lookup(args[0])
	if err != nil {
		return err
	}
	return adapter.RunHook(ctx, a.stdin, a.stdout)
}

func (a *app) refreshToolsCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	runtime := args[0]
	fs := a.flags("agent refresh-tools")
	agent := fs.String("agent", "", "enrolled agent slug")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *agent == "" || fs.NArg() != 0 {
		return errUsage
	}
	cred, err := credential.Load(a.configDir, runtime)
	if err != nil {
		return err
	}
	if cred.AgentSlug != *agent {
		return errors.New("the saved runtime instance belongs to a different agent")
	}
	return a.refreshTools(ctx, runtime, *cred)
}
func (a *app) refreshTools(ctx context.Context, runtime string, cred credential.Credential) error {
	adapter, ok := discovery.Lookup(agentruntime.Key(runtime))
	if !ok {
		return fmt.Errorf("unknown runtime %s", runtime)
	}
	env := discovery.DefaultEnv(a.configDir, a.env.Executable)
	if a.env.Home != "" {
		env.Home = a.env.Home
	}
	env.Getenv = a.getenv
	env.ProviderServers = cred.MCPServers
	if a.env.LookPath != nil {
		env.LookPath = a.env.LookPath
	}
	if a.env.Run != nil {
		env.Run = a.env.Run
	}
	detected, err := adapter.Detect(env)
	if err != nil {
		return err
	}
	found, err := adapter.Discover(ctx, env, detected)
	if err != nil {
		return err
	}
	report := catalogclient.Report(runtime, env, found)
	if err := catalogclient.Publish(ctx, a.http, cred, report); err != nil {
		return err
	}
	for _, source := range report.Sources {
		if source.Complete {
			name := source.Server
			if name == "" {
				name = "Built-in tools"
			}
			fmt.Fprintf(a.stdout, "%s: reported %d tools.\n", name, len(source.Tools))
		}
	}
	for _, failure := range found.Errors {
		fmt.Fprintf(a.stderr, "%s: tool discovery failed: %s\n", failure.Server, failure.Message)
	}
	fmt.Fprintf(a.stdout, "Reported tools for %s (%d servers discovered, %d failed).\n", cred.AgentSlug, len(found.MCP), len(found.Errors))
	return nil
}
