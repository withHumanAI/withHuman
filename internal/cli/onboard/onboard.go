// Package onboard is `withhuman onboard`: the CLI half of the guided
// onboarding in design doc 09. It exchanges the setup code the page
// showed, reports what is installed, waits for the person's choices,
// discovers tools, waits for provisioning, enrolls one agent per runtime,
// installs the hooks, and reports back.
package onboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/withHumanAI/withHuman/agentruntime"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/cli/catalogclient"
	"github.com/withHumanAI/withHuman/internal/cli/credential"
	"github.com/withHumanAI/withHuman/internal/cli/discovery"
	"github.com/withHumanAI/withHuman/internal/cli/enroll"
	"github.com/withHumanAI/withHuman/internal/cli/installation"
	"github.com/withHumanAI/withHuman/internal/cli/mcpservers"
	"github.com/withHumanAI/withHuman/internal/cli/onboardingclient"
	"github.com/withHumanAI/withHuman/internal/cli/runtimeconfig"
)

// Options configures one run. Zero values mean the real machine.
type Options struct {
	URL       string
	Code      string
	Binary    string
	Wait      bool
	JSON      bool
	ConfigDir string
	Version   string
	Env       discovery.Env
	Adapters  []discovery.Adapter
	HTTP      *http.Client
	Stdout    io.Writer
	Stderr    io.Writer
	PollWait  time.Duration
}

// Run drives the whole flow. Any failure after the exchange is recorded
// on the session before it is returned so the page can show it.
func Run(ctx context.Context, opts Options) error {
	if opts.URL == "" || opts.Code == "" {
		return errors.New("onboard needs --url and --code (copy the command from the setup page)")
	}
	r, err := newRunner(opts)
	if err != nil {
		return err
	}
	return r.run(ctx)
}

type runner struct {
	opts     Options
	env      discovery.Env
	adapters map[agentruntime.Key]discovery.Adapter
	client   *onboardingclient.Client
	version  int64
	// servers are the MCP servers the provider offers its agents, read
	// from runtime-config once connected: left out of tool discovery and
	// registered with every runtime at install.
	servers []domain.AgentMCPServer
}

func newRunner(opts Options) (*runner, error) {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.PollWait == 0 {
		opts.PollWait = 25 * time.Second
	}
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{Timeout: 60 * time.Second}
	}
	opts.HTTP = enroll.HTTPClient(opts.HTTP)
	if opts.Adapters == nil {
		opts.Adapters = discovery.All()
	}
	binary := opts.Binary
	if binary == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("locate this binary: %w (pass --binary)", err)
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		binary = exe
	}
	if abs, err := filepath.Abs(binary); err == nil {
		binary = abs
	}
	env := opts.Env
	defaults := discovery.DefaultEnv(opts.ConfigDir, binary)
	if env.Home == "" {
		env.Home = defaults.Home
	}
	if env.Path == nil {
		env.Path = defaults.Path
	}
	if env.Cwd == "" {
		env.Cwd = defaults.Cwd
	}
	if env.LookPath == nil && opts.Env.Path == nil {
		env.LookPath = defaults.LookPath
	}
	env.ConfigDir = opts.ConfigDir
	env.Binary = binary
	r := &runner{opts: opts, env: env, adapters: map[agentruntime.Key]discovery.Adapter{}}
	for _, adapter := range opts.Adapters {
		r.adapters[adapter.Key()] = adapter
	}
	return r, nil
}

// event writes one progress line: prose on stderr, or a JSON object on
// stdout under --json.
func (r *runner) event(name, message string, fields map[string]any) {
	if r.opts.JSON {
		payload := map[string]any{"event": name}
		if message != "" {
			payload["message"] = message
		}
		for key, value := range fields {
			payload[key] = value
		}
		raw, _ := json.Marshal(payload)
		fmt.Fprintln(r.opts.Stdout, string(raw))
		return
	}
	if message != "" {
		fmt.Fprintln(r.opts.Stderr, message)
	}
}

// fail records the error on the session and returns it.
func (r *runner) fail(ctx context.Context, code string, err error) error {
	r.event("error", "Error: "+err.Error(), map[string]any{"code": code})
	if r.client != nil {
		reportCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_, _ = r.client.ReportError(reportCtx, code, err.Error())
	}
	return err
}

func (r *runner) run(ctx context.Context) error {
	client, exchanged, err := onboardingclient.Exchange(ctx, r.opts.HTTP, r.opts.URL, r.opts.Code, machine(r.opts.Version))
	if err != nil {
		if onboardingclient.IsCode(err, "setup_code_invalid") {
			return fmt.Errorf("the setup code was not accepted (it is single use and expires after ten minutes): start again from the setup page")
		}
		return fmt.Errorf("connect to %s: %w", r.opts.URL, err)
	}
	r.client = client
	r.version = exchanged.Session.Version
	r.event("connected", fmt.Sprintf("Connected to %s (session %s).", client.BaseURL, exchanged.SessionID), map[string]any{"session_id": exchanged.SessionID, "provider_url": client.BaseURL})
	provider, err := runtimeconfig.Fetch(ctx, r.opts.HTTP, client.BaseURL)
	if err != nil {
		return r.fail(ctx, "provider_config_failed", err)
	}
	r.servers = provider.AgentMCPServers
	r.env.ProviderServers = r.servers

	detections, err := r.detect()
	if err != nil {
		return r.fail(ctx, "detect_failed", err)
	}
	session, err := client.PutDetections(ctx, detections)
	if err != nil {
		return r.fail(ctx, "report_failed", fmt.Errorf("report detections: %w", err))
	}
	r.version = session.Version
	r.event("detected", "Reported "+describeDetections(detections)+".", map[string]any{"detections": detections})

	r.event("waiting_for_agents", "Waiting for you to choose agents in the browser...", nil)
	session, err = r.waitFor(ctx, func(s *domain.OnboardingSession) bool { return s.Status == domain.OnboardingAgentsSelected })
	if err != nil {
		return r.fail(ctx, "wait_failed", err)
	}

	var reported map[string]domain.OnboardingReportedTools
	for {
		reported, err = r.discover(ctx, detections, session.SelectedRuntimes)
		if err != nil {
			return r.fail(ctx, "discover_failed", err)
		}
		session, err = client.PutTools(ctx, reported)
		if err != nil {
			if onboardingclient.IsCode(err, "onboarding_session_state") {
				// The person changed their selection while we were listing
				// tools; pick up the new state and discover again.
				session, err = r.waitFor(ctx, func(s *domain.OnboardingSession) bool { return s.Status == domain.OnboardingAgentsSelected })
				if err != nil {
					return r.fail(ctx, "wait_failed", err)
				}
				continue
			}
			return r.fail(ctx, "report_failed", fmt.Errorf("report tools: %w", err))
		}
		r.version = session.Version
		r.event("tools_reported", "Reported "+describeTools(reported)+".", map[string]any{"tools": reported})
		r.event("waiting_for_provisioning", "Waiting for you to set up your notifications in the browser...", nil)
		session, err = r.waitFor(ctx, func(s *domain.OnboardingSession) bool {
			return s.Status == domain.OnboardingProvisioning || s.Status == domain.OnboardingAgentsSelected
		})
		if err != nil {
			return r.fail(ctx, "wait_failed", err)
		}
		if session.Status == domain.OnboardingProvisioning {
			break
		}
		r.event("agents_reselected", "Agent selection changed; discovering tools again.", nil)
	}

	enrolled, err := client.Enroll(ctx)
	if err != nil {
		return r.fail(ctx, "enroll_failed", fmt.Errorf("enroll: %w", err))
	}
	r.version = enrolled.Session.Version
	r.event("enrolled", fmt.Sprintf("Enrolled %d agent(s).", len(enrolled.Enrollments)), map[string]any{"enrollments": len(enrolled.Enrollments)})

	results, err := r.install(ctx, detections, enrolled)
	if err != nil {
		return r.fail(ctx, "install_failed", err)
	}
	for _, result := range results {
		if !result.OK {
			continue
		}
		cred, loadErr := credential.Load(r.opts.ConfigDir, result.Runtime)
		if loadErr == nil {
			loadErr = catalogclient.Publish(ctx, r.opts.HTTP, *cred, catalogclient.Report(result.Runtime, r.env, reported[result.Runtime]))
		}
		if loadErr != nil {
			r.event("catalogue_warning", fmt.Sprintf("Tool catalogue could not be published for %s. Run withhuman agent refresh-tools %s --agent <slug> to retry.", result.Runtime, result.Runtime), nil)
		}
	}
	session, err = client.PutInstallResults(ctx, results)
	if err != nil {
		return r.fail(ctx, "report_failed", fmt.Errorf("report install results: %w", err))
	}
	r.version = session.Version
	r.event("installed", "", map[string]any{"results": results, "test_commands": session.TestCommands})
	r.printSummary(results, session.TestCommands)
	for _, result := range results {
		if !result.OK {
			return errors.New("adapter registration is incomplete; follow the installation notes and run agent install for the affected runtime")
		}
	}

	if !r.opts.Wait {
		return nil
	}
	r.event("waiting_for_completion", "Waiting for you to finish in the browser...", nil)
	session, err = r.waitFor(ctx, func(s *domain.OnboardingSession) bool { return s.Status.Terminal() })
	if err != nil {
		return err
	}
	r.event("completed", "Setup "+string(session.Status)+".", map[string]any{"status": session.Status})
	return nil
}

// waitFor long-polls until accept says so. A session that ends first is
// an error, because the flow cannot continue without the person.
func (r *runner) waitFor(ctx context.Context, accept func(*domain.OnboardingSession) bool) (*domain.OnboardingSession, error) {
	for {
		session, err := r.client.Wait(ctx, r.version, r.opts.PollWait)
		if err != nil {
			if onboardingclient.IsCode(err, "onboarding_session_expired") {
				return nil, errors.New("the setup session expired: start again from the setup page")
			}
			var perr *onboardingclient.Error
			if errors.As(err, &perr) || ctx.Err() != nil {
				return nil, err
			}
			// A dropped poll is not fatal; the provider keeps the session.
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		}
		if session.Version > r.version {
			r.version = session.Version
		}
		if accept(session) {
			return session, nil
		}
		if session.Status.Terminal() {
			return nil, fmt.Errorf("the setup session ended (%s) before this step; start again from the setup page", session.Status)
		}
	}
}

func (r *runner) detect() ([]domain.OnboardingDetection, error) {
	detections := make([]domain.OnboardingDetection, 0, len(agentruntime.Keys()))
	for _, key := range agentruntime.Keys() {
		adapter, ok := r.adapters[key]
		if !ok {
			detections = append(detections, domain.OnboardingDetection{Runtime: string(key), MCPServers: []domain.OnboardingMCPServer{}})
			continue
		}
		det, err := adapter.Detect(r.env)
		if err != nil {
			return nil, fmt.Errorf("detect %s: %w", key, err)
		}
		if det.MCPServers == nil {
			det.MCPServers = []domain.OnboardingMCPServer{}
		}
		detections = append(detections, det)
	}
	return detections, nil
}

func (r *runner) discover(ctx context.Context, detections []domain.OnboardingDetection, selected []string) (map[string]domain.OnboardingReportedTools, error) {
	reported := map[string]domain.OnboardingReportedTools{}
	for _, name := range selected {
		key := agentruntime.Key(name)
		adapter, ok := r.adapters[key]
		if !ok {
			return nil, fmt.Errorf("unknown runtime %q selected", name)
		}
		r.event("discovering", fmt.Sprintf("Discovering %s tools...", displayName(key)), map[string]any{"runtime": name})
		det := findDetection(detections, name)
		discovered, err := adapter.Discover(ctx, r.env, det)
		if err != nil {
			return nil, fmt.Errorf("discover %s: %w", name, err)
		}
		if discovered.MCP == nil {
			discovered.MCP = map[string][]domain.OnboardingReportedTool{}
		}
		if discovered.Builtin == nil {
			discovered.Builtin = []domain.OnboardingReportedTool{}
		}
		for _, failure := range discovered.Errors {
			r.event("discovery_error", fmt.Sprintf("  %s: MCP server %s could not be listed: %s", displayName(key), failure.Server, failure.Message), map[string]any{"runtime": name, "server": failure.Server})
		}
		reported[name] = discovered
	}
	return reported, nil
}

func (r *runner) install(ctx context.Context, detections []domain.OnboardingDetection, enrolled *onboardingclient.EnrollResult) ([]domain.OnboardingInstallResult, error) {
	manager, err := r.env.Manager()
	if err != nil {
		return nil, err
	}
	providerURL := enrolled.ProviderURL
	if providerURL == "" {
		providerURL = r.client.BaseURL
	}
	var results []domain.OnboardingInstallResult
	for _, enrollment := range enrolled.Enrollments {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		result, err := installation.Install(manager, r.opts.ConfigDir, enrollment.Runtime, credential.Credential{
			ProviderURL: providerURL, Token: enrollment.Credential.Token, CredentialID: enrollment.Credential.ID,
			ExpiresAt: enrollment.Credential.ExpiresAt, AgentSlug: enrollment.Agent.Slug, AgentName: enrollment.Agent.Name,
			InstanceName: enrollment.AgentInstance.Name, Runtime: enrollment.Runtime,
		}, r.servers, mcpservers.Env{Home: r.env.Home, Binary: r.env.Binary, ConfigDir: r.opts.ConfigDir, Getenv: r.env.Getenv})
		result.Adapter = enrollment.Runtime
		report := installation.Report(result, err)
		r.event("runtime_installed", describeInstall(agentruntime.Key(enrollment.Runtime), report), map[string]any{"runtime": enrollment.Runtime, "result": report})
		results = append(results, report)
	}

	return results, nil
}

func (r *runner) printSummary(results []domain.OnboardingInstallResult, commands map[string]domain.OnboardingTestCommand) {
	if r.opts.JSON {
		return
	}
	w := r.opts.Stderr
	fmt.Fprintln(w)
	complete := true
	for _, result := range results {
		if !result.OK {
			complete = false
			fmt.Fprintf(w, "%s: installation incomplete. %s\n", result.Runtime, result.Message)
		}
		if result.Manual && result.Snippet != "" {
			fmt.Fprintf(w, "%s needs one manual step. %s\n\n%s\n\n", displayName(agentruntime.Key(result.Runtime)), result.Message, indent(result.Snippet))
		}
		for _, note := range result.Notes {
			fmt.Fprintf(w, "Note (%s): %s\n", displayName(agentruntime.Key(result.Runtime)), note)
		}
	}
	if !complete {
		fmt.Fprintln(w, "Repair the reported installation steps and rerun onboarding, or use withhuman agent eject <runtime> --yes.")
		return
	}
	if len(commands) > 0 {
		fmt.Fprintln(w, "\nHooks are in place. Make each agent call a gated tool:")
		for _, runtime := range sortedKeys(commands) {
			command := commands[runtime]
			fmt.Fprintf(w, "\n  %s (%s):\n    %s\n", displayName(agentruntime.Key(runtime)), command.Tool, command.Command)
			if command.Note != "" {
				fmt.Fprintf(w, "    %s\n", command.Note)
			}
		}
	}
	fmt.Fprintln(w, "\nFinish in the browser: the request appears there for you to approve or deny.")
}

func machine(version string) domain.OnboardingMachine {
	hostname, _ := os.Hostname()
	m := domain.OnboardingMachine{Hostname: hostname, OS: runtime.GOOS, Arch: runtime.GOARCH, CLIVersion: version}
	if current, err := user.Current(); err == nil {
		m.User = current.Username
	} else {
		m.User = os.Getenv("USER")
	}
	return m
}

func findDetection(detections []domain.OnboardingDetection, runtime string) domain.OnboardingDetection {
	for _, det := range detections {
		if det.Runtime == runtime {
			return det
		}
	}
	return domain.OnboardingDetection{Runtime: runtime}
}

func displayName(key agentruntime.Key) string {
	if runtime, ok := agentruntime.Lookup(key); ok {
		return runtime.DisplayName
	}
	return string(key)
}

func describeDetections(detections []domain.OnboardingDetection) string {
	var found []string
	for _, det := range detections {
		if det.Installed {
			found = append(found, displayName(agentruntime.Key(det.Runtime)))
		}
	}
	if len(found) == 0 {
		return "no installed agent runtimes"
	}
	return "installed runtimes: " + strings.Join(found, ", ")
}

func describeTools(reported map[string]domain.OnboardingReportedTools) string {
	var parts []string
	for _, runtime := range sortedKeys(reported) {
		tools := reported[runtime]
		count := len(tools.Builtin)
		for _, mcp := range tools.MCP {
			count += len(mcp)
		}
		parts = append(parts, fmt.Sprintf("%d %s tools", count, displayName(agentruntime.Key(runtime))))
	}
	return strings.Join(parts, ", ")
}

func describeInstall(key agentruntime.Key, report domain.OnboardingInstallResult) string {
	if !report.OK {
		return fmt.Sprintf("%s: installation incomplete. %s", displayName(key), report.Message)
	}
	if report.Manual {
		return fmt.Sprintf("%s: one manual step remains (shown below).", displayName(key))
	}
	if len(report.Changes) == 0 {
		return fmt.Sprintf("%s: hook already in place.", displayName(key))
	}
	var paths []string
	for _, change := range report.Changes {
		paths = append(paths, change.Path)
	}
	return fmt.Sprintf("%s: hook installed (%s).", displayName(key), strings.Join(paths, ", "))
}

func indent(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		lines[i] = "    " + line
	}
	return strings.Join(lines, "\n")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
