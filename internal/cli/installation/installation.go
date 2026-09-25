// Package installation connects provider enrollment to the public AAP
// library and registers the provider's agent MCP servers beside the hook.
package installation

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/agentapprovalprotocol/agentapprovalprotocol/adapters"
	"github.com/withHumanAI/withHuman/agentruntime"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/cli/credential"
	"github.com/withHumanAI/withHuman/internal/cli/mcpservers"
)

func New(configDir string, env adapters.Environment) (*adapters.Manager, error) {
	env.ConfigDir = filepath.Join(configDir, "aap")
	return adapters.NewWithEnvironment(env)
}

// Origin validates the provider origin before any credential-bearing request.
func Origin(value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("provider URL must be an HTTP(S) origin without credentials, a path, query, or fragment")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return "", fmt.Errorf("provider URL requires HTTPS except on localhost")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// Result is one runtime's installation: the hook the AAP library
// registered and the provider's MCP servers registered beside it.
type Result struct {
	Adapter  string
	Complete bool
	Changes  []adapters.FileChange
	Notes    []string
	// Manual, Message and Snippet describe an MCP server registration the
	// CLI could not write and the person has to paste.
	Manual  bool
	Message string
	Snippet string
}

// Install stores the one-time enrollment before installation so local failures
// can be repaired without enrolling another instance, installs the hook,
// then registers the provider's MCP servers with the runtime under the
// same credential. Secrets never enter reports.
func Install(manager *adapters.Manager, configDir, key string, cred credential.Credential, servers []domain.AgentMCPServer, env mcpservers.Env) (Result, error) {
	result := Result{Adapter: key}
	adapter, err := manager.Lookup(key)
	if err != nil {
		return result, err
	}
	origin, err := Origin(cred.ProviderURL)
	if err != nil {
		return result, err
	}
	cred.ProviderURL, cred.Runtime, cred.MCPServers = origin, key, servers
	if cred.Token == "" {
		return result, fmt.Errorf("enrollment returned no instance credential")
	}
	if _, err := credential.Save(configDir, key, cred); err != nil {
		return result, err
	}
	// A failed registration can leave plugin files in place. Remove its
	// recorded changes before retrying, so unchanged assets cannot be mistaken
	// for a successful runtime registration by the pinned library.
	status, err := adapter.Status()
	if err != nil {
		return result, err
	}
	if status.Configured && !status.Complete {
		if err := adapter.Uninstall(); err != nil {
			return result, err
		}
	}
	installed, err := adapter.Install(cred.Token, origin+"/api/aap")
	result.Complete, result.Changes, result.Notes = installed.Complete, installed.Changes, installed.Notes
	if err != nil || !installed.Complete {
		return result, err
	}
	registered, err := mcpservers.Install(env, mcpservers.Registration{Runtime: agentruntime.Key(key), Servers: servers})
	for _, change := range registered.Changes {
		result.Changes = append(result.Changes, adapters.FileChange{Path: change.Path, Action: change.Action})
	}
	result.Notes = append(result.Notes, registered.Notes...)
	result.Manual, result.Message, result.Snippet = registered.Manual, registered.Message, registered.Snippet
	if err != nil {
		return result, fmt.Errorf("register the provider's MCP servers with %s: %w", key, err)
	}
	return result, nil
}

// Remove takes the registered MCP servers out of the runtime after the
// AAP library ejected its hook.
func Remove(key string, cred credential.Credential, env mcpservers.Env) (mcpservers.Result, error) {
	return mcpservers.Remove(env, mcpservers.Registration{Runtime: agentruntime.Key(key), Servers: cred.MCPServers})
}

func Report(result Result, err error) domain.OnboardingInstallResult {
	report := domain.OnboardingInstallResult{
		Runtime: result.Adapter, OK: result.Complete && err == nil, Notes: result.Notes,
		Manual: result.Manual, Message: result.Message, Snippet: result.Snippet,
	}
	for _, change := range result.Changes {
		report.Changes = append(report.Changes, domain.OnboardingFileChange{Path: change.Path, Action: change.Action})
	}
	if err != nil {
		report.Message = err.Error()
	}
	return report
}
