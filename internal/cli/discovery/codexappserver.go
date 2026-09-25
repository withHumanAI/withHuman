package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	aaptool "github.com/agentapprovalprotocol/agentapprovalprotocol/tool"
	"github.com/withHumanAI/withHuman/agentruntime"
	"github.com/withHumanAI/withHuman/domain"

	"github.com/withHumanAI/withHuman/internal/cli/jsonrpc"
	"github.com/withHumanAI/withHuman/internal/cli/mcpclient"
)

// codexAppServerTimeout bounds asking Codex for its MCP inventory. Codex
// starts every configured server itself, each under its own startup
// timeout, before it answers.
const codexAppServerTimeout = 2 * time.Minute

// codexNotLoggedIn is the auth status Codex reports for an OAuth server
// it holds no usable token for.
const codexNotLoggedIn = "notLoggedIn"

// codexOAuth is the auth status of a server Codex signed in to with
// `codex mcp login`. Its token lives in Codex's keychain, so the hook
// cannot list the server itself and submits Codex's sanitised spelling.
const codexOAuth = "oAuth"

// codexServerStatus is one entry of Codex's mcpServerStatus/list answer:
// the server's tools as Codex sees them, or why it could not list them.
type codexServerStatus struct {
	Name       string                    `json:"name"`
	AuthStatus string                    `json:"authStatus"`
	Tools      map[string]mcpclient.Tool `json:"tools"`
	ToolsError *string                   `json:"toolsError"`
}

// codexServerStatuses asks `codex app-server` for every MCP server Codex
// knows and the tools each advertises. Going through Codex means the
// listing carries Codex's own credentials, including OAuth tokens it
// keeps in the OS keychain where config.toml cannot show them, and skips
// servers Codex has disabled.
func codexServerStatuses(ctx context.Context, binary, codexHome string) ([]codexServerStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, codexAppServerTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "app-server")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
	process, err := jsonrpc.StartProcess(cmd)
	if err != nil {
		return nil, fmt.Errorf("codex app-server: %w", err)
	}
	defer process.Close()
	statuses, err := listCodexServerStatuses(ctx, process)
	if err != nil {
		return nil, process.Decorate(fmt.Errorf("codex app-server: %w", err))
	}
	return statuses, nil
}

func listCodexServerStatuses(ctx context.Context, process *jsonrpc.Process) ([]codexServerStatus, error) {
	if _, err := process.Call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{"name": "withhuman", "version": "onboarding"},
	}); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	if err := process.Notify(ctx, "initialized", nil); err != nil {
		return nil, fmt.Errorf("initialized: %w", err)
	}
	var statuses []codexServerStatus
	cursor := ""
	for page := 0; page < 100; page++ {
		params := map[string]any{"detail": "toolsAndAuthOnly"}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := process.Call(ctx, "mcpServerStatus/list", params)
		if err != nil {
			return nil, fmt.Errorf("mcpServerStatus/list: %w", err)
		}
		var result struct {
			Data       []codexServerStatus `json:"data"`
			NextCursor string              `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("mcpServerStatus/list: %w", err)
		}
		statuses = append(statuses, result.Data...)
		if result.NextCursor == "" || result.NextCursor == cursor {
			return statuses, nil
		}
		cursor = result.NextCursor
	}
	return nil, errors.New("mcpServerStatus/list: cursor never ended")
}

// codexDiscovered renders Codex's inventory as a discovery report. A
// server Codex is not logged in to, or could not list, lands in Errors.
// A server with an empty catalog and no error is one Codex has disabled
// (or one with nothing to gate) and is left out.
func codexDiscovered(env Env, statuses []codexServerStatus) Discovered {
	out := Discovered{Builtin: builtinTools(agentruntime.Codex), MCP: map[string][]domain.OnboardingReportedTool{}}
	for _, status := range statuses {
		switch {
		case env.providerServer(status.Name, mcpclient.Spec{}):
			continue
		case status.AuthStatus == codexNotLoggedIn:
			message := "Codex is not logged in to this server"
			if status.ToolsError != nil {
				message = *status.ToolsError
			}
			out.Errors = append(out.Errors, domain.OnboardingDiscoveryError{
				Server: status.Name, Message: fmt.Sprintf("%s (run: codex mcp login %s)", message, status.Name),
			})
		case status.ToolsError != nil:
			out.Errors = append(out.Errors, domain.OnboardingDiscoveryError{Server: status.Name, Message: *status.ToolsError})
		case len(status.Tools) == 0:
			continue
		default:
			out.MCP[status.Name] = codexReportTools(status.Name, status.AuthStatus == codexOAuth, status.Tools)
		}
	}
	return out
}

// codexReportTools renders one server's catalog under the names Codex's
// hook will submit for its tools, sorted, so what the page shows is what
// a gate rule has to match. Two raw names that collapse into one (Codex
// disambiguates those with a hash suffix it derives from its own
// bookkeeping) keep the first.
func codexReportTools(server string, sanitised bool, catalog map[string]mcpclient.Tool) []domain.OnboardingReportedTool {
	tools := sortedTools(catalog)
	for index := range tools {
		tools[index].Name = codexCanonicalTool(server, sanitised, tools[index])
	}
	sort.SliceStable(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	seen := map[string]bool{}
	unique := tools[:0]
	for _, tool := range tools {
		if seen[tool.Name] {
			continue
		}
		seen[tool.Name] = true
		unique = append(unique, tool)
	}
	return reportTools(unique)
}

// sortedTools orders a catalog by tool name, since Codex keys it as a map
// and the report should read the same on every run.
func sortedTools(catalog map[string]mcpclient.Tool) []mcpclient.Tool {
	tools := make([]mcpclient.Tool, 0, len(catalog))
	for name, tool := range catalog {
		if tool.Name == "" {
			tool.Name = name
		}
		tools = append(tools, tool)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools
}

// codexAppsServer is the MCP server Codex itself hosts for Codex Apps,
// the ChatGPT connectors (Gmail, Slack, GitHub) a person links in the
// Codex settings. It never appears in config.toml.
const codexAppsServer = "codex_apps"

// codexCanonicalTool is the tool name an approval request carries once
// Codex calls the tool. Codex sanitises server and tool names for the
// model (anything outside [A-Za-z0-9_] becomes an underscore) and its
// hooks see that spelling; the AAP hook lists the server itself to
// recover the server-defined name, so for a server it can reach the
// request carries "gmail.send_email" as advertised. For a server signed
// in through Codex's keychain (sanitised) the hook cannot list it and
// the sanitised spelling is what a gate rule has to match.
//
// Codex Apps tools go further: Codex namespaces them per connector
// ("codex_apps__gmail") and strips the connector from the tool name
// ("_send_email"), joins the two for the hook as
// mcp__codex_apps__gmail__send_email, and the adapter splits the host
// prefix off to tool gmail__send_email under server codex_apps. Mirrors
// codex-rs/codex-mcp/src/codex_apps.rs and
// codex-rs/core/src/tools/handlers/mcp.rs (join_tool_name).
func codexCanonicalTool(server string, sanitised bool, tool mcpclient.Tool) string {
	namespace, name := server, tool.Name
	if server == codexAppsServer {
		namespace, name = codexAppsCallable(tool)
	} else if !sanitised {
		return name
	}
	hookName := agentruntime.RuntimeToolName(agentruntime.Codex, agentruntime.MCPRef{Server: namespace, Name: name})
	return aaptool.Identify(hookName, aaptool.NamingMCPPrefixed).Tool
}

// codexAppsCallable splits a Codex Apps tool into the namespace and name
// Codex exposes: the connector's slug under codex_apps, and the tool name
// with the connector's slug (or its id) taken off the front.
func codexAppsCallable(tool mcpclient.Tool) (namespace, name string) {
	namespace, name = codexAppsServer, codexSlug(tool.Name)
	var connectorID, connectorName string
	if tool.Meta != nil {
		connectorID, connectorName = strings.TrimSpace(tool.Meta.ConnectorID), strings.TrimSpace(tool.Meta.ConnectorName)
	}
	if connectorName != "" {
		connector := codexSlug(connectorName)
		namespace = codexAppsServer + "__" + connector
		if stripped := strings.TrimPrefix(name, connector); stripped != name && stripped != "" {
			return namespace, stripped
		}
	}
	if connectorID != "" {
		if stripped := strings.TrimPrefix(name, codexSlug(connectorID)); stripped != name && stripped != "" {
			return namespace, stripped
		}
	}
	return namespace, name
}

// codexSlug mirrors Codex's connector name sanitiser: ASCII letters and
// digits lower-cased, every other character an underscore, runs at the
// ends dropped, and "app" when nothing is left.
func codexSlug(value string) string {
	var out strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			out.WriteRune(r + ('a' - 'A'))
		default:
			out.WriteByte('-')
		}
	}
	slug := strings.Trim(out.String(), "-")
	if slug == "" {
		return "app"
	}
	return strings.ReplaceAll(slug, "-", "_")
}
