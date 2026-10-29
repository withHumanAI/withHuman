// Package openai holds withHuman's plugin for OpenAI's plugin directory,
// shared by ChatGPT and Codex: the hosted MCP server and the published
// skills. These tests hold the package to the directory's submission
// rules, so a change that the portal would refuse fails here first.
package openai

import (
	"encoding/json"
	"encoding/xml"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

const pluginDir = "withhuman"

// bundledSkills are the published skills the plugin carries. The CLI
// skill stays out: the directory refuses hooks, and in ChatGPT there is
// no shell to install the CLI from.
var bundledSkills = []string{"withhuman", "withhuman-pipelines"}

type manifest struct {
	Schema      string `json:"$schema"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Author      struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"author"`
	Homepage   string   `json:"homepage"`
	Repository string   `json:"repository"`
	License    string   `json:"license"`
	Keywords   []string `json:"keywords"`
	Extensions struct {
		OpenAI map[string]json.RawMessage `json:"com.openai"`
	} `json:"extensions"`
}

type listing struct {
	DisplayName       string   `json:"displayName"`
	ShortDescription  string   `json:"shortDescription"`
	LongDescription   string   `json:"longDescription"`
	DeveloperName     string   `json:"developerName"`
	Category          string   `json:"category"`
	Capabilities      []string `json:"capabilities"`
	WebsiteURL        string   `json:"websiteURL"`
	SupportURL        string   `json:"supportURL"`
	PrivacyPolicyURL  string   `json:"privacyPolicyURL"`
	TermsOfServiceURL string   `json:"termsOfServiceURL"`
	DefaultPrompt     []string `json:"defaultPrompt"`
	BrandColor        string   `json:"brandColor"`
	BrandColorDark    string   `json:"brandColorDark"`
	ComposerIcon      string   `json:"composerIcon"`
	Logo              string   `json:"logo"`
}

var (
	pluginName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
	semver     = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)
	hexColor   = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	categories = []string{
		"Productivity", "Creativity", "Developer Tools", "Business & Operations",
		"Data & Analytics", "Communication", "Education & Research", "Security",
		"Finance", "Healthcare", "Travel", "Entertainment", "Other",
	}
)

func readJSON(t *testing.T, name string, into any) {
	t.Helper()
	text, err := os.ReadFile(filepath.Join(pluginDir, name))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(text)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func readListing(t *testing.T) (manifest, listing) {
	t.Helper()
	var m manifest
	readJSON(t, "plugin.json", &m)
	keys := make([]string, 0, len(m.Extensions.OpenAI))
	for key := range m.Extensions.OpenAI {
		keys = append(keys, key)
	}
	// apps and hooks are refused in public submissions; the interface is
	// the only OpenAI setting the plugin needs.
	if !slices.Equal(keys, []string{"interface"}) {
		t.Fatalf("extensions.com.openai has %q, want only interface", keys)
	}
	var l listing
	decoder := json.NewDecoder(strings.NewReader(string(m.Extensions.OpenAI["interface"])))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&l); err != nil {
		t.Fatalf("interface: %v", err)
	}
	return m, l
}

func TestManifestIdentity(t *testing.T) {
	m, _ := readListing(t)
	if m.Schema != "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json" {
		t.Errorf("$schema is %q", m.Schema)
	}
	if !pluginName.MatchString(m.Name) || len(m.Name) > 64 {
		t.Errorf("name %q is not a plugin name", m.Name)
	}
	if !semver.MatchString(m.Version) {
		t.Errorf("version %q is not MAJOR.MINOR.PATCH", m.Version)
	}
	if m.Description == "" || utf8.RuneCountInString(m.Description) > 1024 {
		t.Errorf("description must be 1 to 1,024 characters")
	}
	if m.Author.Name == "" {
		t.Errorf("author.name is required")
	}
	for _, url := range []string{m.Author.URL, m.Homepage} {
		if !strings.HasPrefix(url, "https://") {
			t.Errorf("%q must be an HTTPS URL", url)
		}
	}
}

// The limits are the final submission's, which are stricter than the
// upload's.
func TestListingFitsTheDirectory(t *testing.T) {
	_, l := readListing(t)
	limits := []struct {
		field, value string
		max          int
	}{
		{"displayName", l.DisplayName, 30},
		{"shortDescription", l.ShortDescription, 30},
		{"longDescription", l.LongDescription, 4000},
		{"developerName", l.DeveloperName, 80},
	}
	for _, limit := range limits {
		if limit.value == "" || utf8.RuneCountInString(limit.value) > limit.max {
			t.Errorf("%s must be 1 to %d characters, is %q", limit.field, limit.max, limit.value)
		}
	}
	if strings.Contains(l.ShortDescription, "\n") {
		t.Errorf("shortDescription must be one line")
	}
	if !slices.Contains(categories, l.Category) {
		t.Errorf("category %q is not one the directory offers", l.Category)
	}
	if len(l.Capabilities) > 20 {
		t.Errorf("at most 20 capabilities")
	}
	if len(l.DefaultPrompt) == 0 || len(l.DefaultPrompt) > 3 {
		t.Errorf("want 1 to 3 starter prompts, have %d", len(l.DefaultPrompt))
	}
	seen := map[string]bool{}
	for _, prompt := range l.DefaultPrompt {
		normalized := strings.ToLower(strings.TrimSpace(prompt))
		switch {
		case prompt == "" || utf8.RuneCountInString(prompt) > 128:
			t.Errorf("starter prompt %q must be 1 to 128 characters", prompt)
		case strings.ContainsAny(prompt, "@\n"):
			t.Errorf("starter prompt %q must be one line with no @mention", prompt)
		case seen[normalized]:
			t.Errorf("starter prompt %q is repeated", prompt)
		}
		seen[normalized] = true
	}
	// A remote MCP submission needs all four listing URLs.
	for field, url := range map[string]string{
		"websiteURL": l.WebsiteURL, "supportURL": l.SupportURL,
		"privacyPolicyURL": l.PrivacyPolicyURL, "termsOfServiceURL": l.TermsOfServiceURL,
	} {
		if !strings.HasPrefix(url, "https://") || len(url) > 1024 {
			t.Errorf("%s %q must be an HTTPS URL of at most 1,024 characters", field, url)
		}
	}
	for _, color := range []string{l.BrandColor, l.BrandColorDark} {
		if !hexColor.MatchString(color) {
			t.Errorf("brand color %q is not six-digit hex", color)
		}
	}
	for _, text := range append([]string{l.DisplayName, l.ShortDescription, l.LongDescription}, l.DefaultPrompt...) {
		if strings.Contains(text, "—") {
			t.Errorf("listing copy uses an em dash: %q", text)
		}
	}
}

func TestIconsAreSquareSVGs(t *testing.T) {
	_, l := readListing(t)
	for field, path := range map[string]string{"logo": l.Logo, "composerIcon": l.ComposerIcon} {
		if !strings.HasPrefix(path, "./assets/") || strings.Contains(path, "..") {
			t.Errorf("%s %q must start with ./assets/ and stay inside the plugin", field, path)
			continue
		}
		text, err := os.ReadFile(filepath.Join(pluginDir, path))
		if err != nil {
			t.Errorf("%s: %v", field, err)
			continue
		}
		var svg struct {
			XMLName xml.Name
			ViewBox string `xml:"viewBox,attr"`
		}
		if err := xml.Unmarshal(text, &svg); err != nil || svg.XMLName.Local != "svg" {
			t.Errorf("%s %q is not an SVG document", field, path)
			continue
		}
		box := strings.Fields(svg.ViewBox)
		var width, height float64
		if len(box) == 4 {
			width, _ = strconv.ParseFloat(box[2], 64)
			height, _ = strconv.ParseFloat(box[3], 64)
		}
		if width != height || width < 48 {
			t.Errorf("%s viewBox %q must be square and at least 48 by 48", field, svg.ViewBox)
		}
	}
}

// One remote server and nothing else. Its sign-in is OAuth with dynamic
// client registration, found through the server's own discovery: Codex
// refuses the documented `extensions["com.openai"].auth` block (unknown
// field, through 0.165) and drops the server, and the portal refuses
// headers in a public submission.
func TestMCPServerIsTheHostedServer(t *testing.T) {
	var config struct {
		Schema     string `json:"$schema"`
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	readJSON(t, "mcp.json", &config)
	if config.Schema != "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json" {
		t.Errorf("$schema is %q", config.Schema)
	}
	server, ok := config.MCPServers["withhuman"]
	if len(config.MCPServers) != 1 || !ok {
		t.Fatalf("want exactly the withhuman server, have %d servers", len(config.MCPServers))
	}
	if server.Type != "streamable-http" || server.URL != "https://app.withhuman.ai/api/mcp/v1" {
		t.Errorf("server is %s %s, want the hosted streamable HTTP endpoint", server.Type, server.URL)
	}
	if len(server.Headers) > 0 {
		t.Errorf("headers are refused in a public submission")
	}
}

// A plugin cannot link outside its folder and the portal ignores
// symlinks, so the plugin carries copies of the published skills. `make
// openai-plugin` refreshes them.
func TestSkillsMatchThePublishedSkills(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join(pluginDir, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if entry.Type()&fs.ModeSymlink != 0 || !entry.IsDir() {
			t.Errorf("skills/%s must be a real directory", entry.Name())
			continue
		}
		names = append(names, entry.Name())
	}
	if !slices.Equal(names, bundledSkills) {
		t.Fatalf("plugin carries skills %q, want %q", names, bundledSkills)
	}
	for _, name := range bundledSkills {
		copied, err := os.ReadFile(filepath.Join(pluginDir, "skills", name, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		published, err := os.ReadFile(filepath.Join("..", "..", "skills", name, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		if string(copied) != string(published) {
			t.Errorf("skills/%s differs from the published skill; run make openai-plugin", name)
		}
		if len("withhuman:"+name) > 64 {
			t.Errorf("skill identity withhuman:%s is over 64 characters", name)
		}
	}
}

func TestPluginHasNothingTheDirectoryRefuses(t *testing.T) {
	for _, refused := range []string{"hooks", ".app.json", ".mcp.json", ".codex-plugin"} {
		if _, err := os.Lstat(filepath.Join(pluginDir, refused)); err == nil {
			t.Errorf("%s is refused in a public submission", refused)
		}
	}
}
