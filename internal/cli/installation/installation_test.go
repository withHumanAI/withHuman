package installation

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentapprovalprotocol/agentapprovalprotocol/adapters"
	"github.com/withHumanAI/withHuman/internal/cli/credential"
	"github.com/withHumanAI/withHuman/internal/cli/mcpservers"
	"github.com/withHumanAI/withHuman/plugins/openclaw"
)

// OpenClaw gets withHuman's own plugin, loaded from the CLI's
// configuration directory, in place of the AAP library's.
func TestInstallGivesOpenClawTheWithHumanPlugin(t *testing.T) {
	home, configDir := t.TempDir(), t.TempDir()
	openclawJSON := filepath.Join(home, ".openclaw", "openclaw.json")
	if err := os.MkdirAll(filepath.Dir(openclawJSON), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(openclawJSON, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := "/opt/withhuman/bin/withhuman"
	manager, err := New(configDir, adapters.Environment{
		Home: home, Executable: binary,
		LookPath: func(name string) (string, error) { return "/fake/" + name, nil },
		Run:      func(context.Context, string, ...string) ([]byte, error) { return []byte("2026.10.8"), nil },
		Getenv:   func(string) string { return "" },
	})
	if err != nil {
		t.Fatal(err)
	}
	cred := credential.Credential{ProviderURL: "https://withhuman.example", Token: "whc_test", AgentSlug: "openclaw-bot"}
	result, err := Install(manager, configDir, "openclaw", cred, nil, mcpservers.Env{Home: home, Binary: binary, ConfigDir: configDir})
	if err != nil || !result.Complete {
		t.Fatalf("install = %+v, %v", result, err)
	}
	dir := filepath.Join(configDir, "aap", "plugins", openclaw.Name)
	err = fs.WalkDir(openclaw.Files(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		_, statErr := os.Stat(filepath.Join(dir, filepath.FromSlash(path)))
		return statErr
	})
	if err != nil {
		t.Fatalf("plugin files: %v", err)
	}
	var config struct {
		Plugins struct {
			Load    struct{ Paths []string }
			Entries map[string]struct{ Enabled bool }
		}
	}
	raw, _ := os.ReadFile(openclawJSON)
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Plugins.Load.Paths) != 1 || config.Plugins.Load.Paths[0] != dir || !config.Plugins.Entries[openclaw.Name].Enabled {
		t.Fatalf("openclaw.json =\n%s", raw)
	}
	if _, ok := config.Plugins.Entries["aap"]; ok {
		t.Fatalf("the AAP library's plugin was installed too:\n%s", raw)
	}
}
