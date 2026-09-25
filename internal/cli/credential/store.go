// Package credential owns what the CLI keeps on disk: one agent credential
// per file under <config>/credentials, for enrollment and installation retries.
package credential

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/withHumanAI/withHuman/domain"
)

// Credential is one enrolled agent instance's whc_ bearer and the identity
// it resolves to, as `withhuman onboard` or `withhuman agent install` stored it.
type Credential struct {
	ProviderURL  string    `json:"provider_url"`
	Token        string    `json:"token"`
	CredentialID string    `json:"credential_id"`
	ExpiresAt    time.Time `json:"expires_at"`
	AgentSlug    string    `json:"agent_slug"`
	AgentName    string    `json:"agent_name"`
	InstanceName string    `json:"instance_name"`
	Runtime      string    `json:"runtime,omitempty"`
	// MCPServers are the provider-offered servers the CLI registered with
	// the runtime at install, so eject can take them out and tool
	// discovery can leave them to the provider's own catalogue.
	MCPServers []domain.AgentMCPServer `json:"mcp_servers,omitempty"`
}

// ErrNotFound reports a missing credential file.
var ErrNotFound = errors.New("credential not found")

// Dir is where credentials live under a config directory.
func Dir(configDir string) string { return filepath.Join(configDir, "credentials") }

// Path is the file for one agent.
func Path(configDir, agent string) string {
	return filepath.Join(Dir(configDir), agent+".json")
}

// ValidAgent rejects names that would escape the credentials directory.
func ValidAgent(agent string) bool {
	return agent != "" && agent != "." && agent != ".." && !strings.ContainsAny(agent, `/\`)
}

// Load reads one agent's credential.
func Load(configDir, agent string) (*Credential, error) {
	if !ValidAgent(agent) {
		return nil, fmt.Errorf("invalid agent name %q", agent)
	}
	raw, err := os.ReadFile(Path(configDir, agent))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, agent)
	}
	if err != nil {
		return nil, err
	}
	var cred Credential
	if err := json.Unmarshal(raw, &cred); err != nil {
		return nil, fmt.Errorf("credential %s: %w", agent, err)
	}
	if cred.Token == "" {
		return nil, fmt.Errorf("credential %s has no token", agent)
	}
	return &cred, nil
}

// Save writes one agent's credential at mode 0600, replacing any earlier
// file for the same agent.
func Save(configDir, agent string, cred Credential) (string, error) {
	if !ValidAgent(agent) {
		return "", fmt.Errorf("invalid agent name %q", agent)
	}
	if err := os.MkdirAll(Dir(configDir), 0o700); err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(cred, "", "  ")
	if err != nil {
		return "", err
	}
	path := Path(configDir, agent)
	file, err := os.CreateTemp(Dir(configDir), ".credential-*")
	if err != nil {
		return "", err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if _, err = file.Write(append(raw, '\n')); err != nil {
		file.Close()
		return "", err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	// Rename keeps the temp file's mode, but be explicit for pre-existing
	// files on filesystems that ignore create modes.
	return path, os.Chmod(path, 0o600)
}

// List returns the agent names that have a credential, sorted.
func List(configDir string) ([]string, error) {
	entries, err := os.ReadDir(Dir(configDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var agents []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		agents = append(agents, strings.TrimSuffix(name, ".json"))
	}
	sort.Strings(agents)
	return agents, nil
}

// Delete removes one agent's credential. A missing file is not an error.
func Delete(configDir, agent string) error {
	if !ValidAgent(agent) {
		return fmt.Errorf("invalid agent name %q", agent)
	}
	err := os.Remove(Path(configDir, agent))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
