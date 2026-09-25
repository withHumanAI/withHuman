package credential

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSaveLoadListDelete(t *testing.T) {
	dir := t.TempDir()
	cred := Credential{
		ProviderURL: "https://acme.withhuman.ai", Token: "whc_x_y", CredentialID: "cred-1",
		ExpiresAt: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), AgentName: "claude-code",
		AgentSlug: "claude-code", InstanceName: "laptop", Runtime: "claude-code",
	}
	path, err := Save(dir, "claude-code", cred)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if path != filepath.Join(dir, "credentials", "claude-code.json") {
		t.Fatalf("path = %s", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	if _, err := Save(dir, "hermes", Credential{Token: "whc_h"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir, "claude-code")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(*loaded, cred) {
		t.Fatalf("loaded = %+v, want %+v", *loaded, cred)
	}
	agents, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 2 || agents[0] != "claude-code" || agents[1] != "hermes" {
		t.Fatalf("List = %v", agents)
	}
	if err := Delete(dir, "hermes"); err != nil {
		t.Fatal(err)
	}
	if err := Delete(dir, "hermes"); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
	if _, err := Load(dir, "hermes"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load after delete = %v, want ErrNotFound", err)
	}
}

func TestInvalidAgentNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", `a\b`} {
		if ValidAgent(name) {
			t.Errorf("%q should be invalid", name)
		}
		if _, err := Load(t.TempDir(), name); err == nil {
			t.Errorf("Load(%q) should fail", name)
		}
	}
}

func TestListWithoutDirectory(t *testing.T) {
	agents, err := List(filepath.Join(t.TempDir(), "missing"))
	if err != nil || agents != nil {
		t.Fatalf("List = %v, %v", agents, err)
	}
}
