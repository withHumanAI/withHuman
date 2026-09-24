package httpapi

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/withHumanAI/withHuman/domain"
	"gopkg.in/yaml.v3"
)

// SpecPermission is one registry entry as the contract publishes it under
// the top-level x-withhuman-permissions block. The block is generated from
// domain.Permissions by cmd/withhuman-openapi-permissions so the docs site
// can render the vocabulary without linking Go; the conformance test fails
// when the committed block drifts from the registry.
type SpecPermission struct {
	Key         string   `yaml:"key"`
	Area        string   `yaml:"area"`
	Description string   `yaml:"description"`
	Holders     []string `yaml:"holders,flow"`
	Resource    string   `yaml:"resource"`
	Dangerous   bool     `yaml:"dangerous"`
}

// SpecPermissions lists the registry in key order, in the contract's shape.
func SpecPermissions() []SpecPermission {
	defs := domain.Permissions()
	entries := make([]SpecPermission, 0, len(defs))
	for _, def := range defs {
		holders := make([]string, 0, len(def.Holders))
		for _, holder := range def.Holders {
			holders = append(holders, string(holder))
		}
		entries = append(entries, SpecPermission{
			Key:         string(def.Key),
			Area:        def.Area(),
			Description: def.Description,
			Holders:     holders,
			Resource:    string(def.Resource),
			Dangerous:   def.Dangerous,
		})
	}
	return entries
}

const specPermissionsKey = "x-withhuman-permissions"

// A top-level block runs from its key at column zero to the next line that
// starts at column zero; the tags block is where the vocabulary is inserted
// when the document carries none yet.
var (
	specPermissionsBlock = regexp.MustCompile(`(?m)^` + specPermissionsKey + `:\n(?:[ \t].*\n|\n)*`)
	specTagsBlock        = regexp.MustCompile(`(?m)^tags:\n(?:[ \t].*\n|\n)*`)
)

// RenderSpecPermissions returns the contract with its x-withhuman-permissions
// block replaced by (or, when absent, inserted after the tags block as) the
// registry's current vocabulary. Every other byte is left alone so the
// generator never reflows hand-written YAML.
func RenderSpecPermissions(doc []byte) ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteString(specPermissionsKey + ":\n")
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(SpecPermissions()); err != nil {
		return nil, fmt.Errorf("encode permission vocabulary: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("encode permission vocabulary: %w", err)
	}
	block := indentSequence(buffer.Bytes())

	if loc := specPermissionsBlock.FindIndex(doc); loc != nil {
		return append(append(append([]byte{}, doc[:loc[0]]...), block...), doc[loc[1]:]...), nil
	}
	loc := specTagsBlock.FindIndex(doc)
	if loc == nil {
		return nil, fmt.Errorf("openapi document has no top-level tags block to insert %s after", specPermissionsKey)
	}
	return append(append(append([]byte{}, doc[:loc[1]]...), block...), doc[loc[1]:]...), nil
}

// indentSequence nests the encoded sequence two spaces under its key, the
// way the hand-written tags block is laid out.
func indentSequence(block []byte) []byte {
	lines := bytes.Split(bytes.TrimRight(block, "\n"), []byte("\n"))
	var out bytes.Buffer
	for index, line := range lines {
		if index > 0 && len(line) > 0 {
			out.WriteString("  ")
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

// RenderSpecCredentialKinds documents the same credential gates as the router,
// preserving the hand-written contract's formatting.
func RenderSpecCredentialKinds(doc []byte) []byte {
	lines := strings.Split(string(doc), "\n")
	out := make([]string, 0, len(lines))
	path, method := "", ""
	for _, line := range lines {
		if strings.HasPrefix(line, "  /api/") {
			path = strings.TrimSuffix(strings.TrimSpace(line), ":")
		}
		switch strings.TrimSpace(line) {
		case "get:", "post:", "put:", "patch:", "delete:":
			method = strings.ToUpper(strings.TrimSuffix(strings.TrimSpace(line), ":"))
		}
		if strings.Contains(line, "x-withhuman-credential-kinds:") {
			continue
		}
		out = append(out, line)
		if !strings.HasPrefix(path, "/api/v1/") || !strings.HasPrefix(line, "      x-withhuman-permission:") {
			continue
		}
		access := strings.TrimSpace(strings.TrimPrefix(line, "      x-withhuman-permission:"))
		if access == AccessPublic {
			continue
		}
		routePath := regexp.MustCompile(`\{([^}]+)\}`).ReplaceAllString(path, ":$1")
		kinds := []string{"session"}
		if !BrowserOnly(method, routePath) {
			kinds = append(kinds, "personal_api_key")
		}
		if OrganizationKeyAllowed(method, routePath, access) {
			kinds = append(kinds, "organization_api_key")
		}
		if path == "/api/v1/agent_instances/self" || path == "/api/v1/agent_instances/self/tool_catalog" {
			kinds = []string{"agent_instance"}
		}
		if path == "/api/v1/api_keys/verify" {
			kinds = []string{"personal_api_key", "organization_api_key"}
		}
		out = append(out, "      x-withhuman-credential-kinds: ["+strings.Join(kinds, ", ")+"]")
	}
	return []byte(strings.Join(out, "\n"))
}
