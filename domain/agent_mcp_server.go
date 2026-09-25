package domain

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
)

// AgentMCPServer is an MCP server the provider offers to every enrolled
// agent, reported in /api/runtime-config as agent_mcp_servers. The CLI
// registers each one with the runtime's MCP client, authenticated with
// the instance's own whc_ credential, when it installs a runtime. The
// open edition offers none; the hosted edition offers its gateway.
type AgentMCPServer struct {
	// Name is the key the runtime knows the server by; it prefixes the
	// server's tools in the runtime's tool names.
	Name string `json:"name"`
	// URL is the streamable HTTP endpoint.
	URL string `json:"url"`
	// Label names the server to a person in CLI output and notes.
	Label string `json:"label"`
}

var agentMCPServerNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// Validate checks the shape a CLI may act on: a name every runtime's
// configuration accepts as a key, a label, and an HTTPS endpoint (HTTP
// only on localhost, for development), since the instance credential is
// sent to it.
func (s AgentMCPServer) Validate() error {
	if !agentMCPServerNamePattern.MatchString(s.Name) {
		return fmt.Errorf("agent mcp server name %q must match %s", s.Name, agentMCPServerNamePattern)
	}
	if s.Label == "" {
		return errors.New("agent mcp server label is required")
	}
	u, err := url.Parse(s.URL)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("agent mcp server %s: url must be an absolute URL without credentials or a fragment", s.Name)
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return fmt.Errorf("agent mcp server %s: url requires HTTPS except on localhost", s.Name)
	}
	return nil
}

// ValidateAgentMCPServers checks every server and that names are unique.
func ValidateAgentMCPServers(servers []AgentMCPServer) error {
	seen := map[string]bool{}
	for _, server := range servers {
		if err := server.Validate(); err != nil {
			return err
		}
		if seen[server.Name] {
			return fmt.Errorf("agent mcp server name %q is repeated", server.Name)
		}
		seen[server.Name] = true
	}
	return nil
}
