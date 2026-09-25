package configfile

import (
	"bytes"
	"fmt"

	toml "github.com/pelletier/go-toml/v2"
)

// TOMLRewriteNote is the caveat every TOML edit carries: go-toml renders
// the document from its parsed form, so comments and blank lines are lost.
const TOMLRewriteNote = "config.toml was rewritten from its parsed form: comments and formatting were not preserved."

// LoadTOML decodes a TOML document into nested maps. A missing or empty
// file is an empty table.
func LoadTOML(content []byte, existed bool) (map[string]any, error) {
	table := map[string]any{}
	if !existed || len(bytes.TrimSpace(content)) == 0 {
		return table, nil
	}
	if err := toml.Unmarshal(content, &table); err != nil {
		return nil, fmt.Errorf("%w: parse toml: %v", ErrUnparsable, err)
	}
	return table, nil
}

// FormatTOML renders nested maps back to TOML.
func FormatTOML(table map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := toml.NewEncoder(&buf)
	encoder.SetIndentTables(false)
	if err := encoder.Encode(table); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// TOMLTable returns a nested table, creating it when absent.
func TOMLTable(parent map[string]any, key string) map[string]any {
	if child, ok := parent[key].(map[string]any); ok {
		return child
	}
	child := map[string]any{}
	parent[key] = child
	return child
}

// TOMLTableIfPresent returns a nested table without creating it.
func TOMLTableIfPresent(parent map[string]any, key string) (map[string]any, bool) {
	child, ok := parent[key].(map[string]any)
	return child, ok
}

// TOMLString reads a string member.
func TOMLString(table map[string]any, key string) string {
	value, _ := table[key].(string)
	return value
}

// TOMLStrings reads an array of strings.
func TOMLStrings(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// TOMLStringMap reads a table of strings.
func TOMLStringMap(value any) map[string]string {
	table, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for key, item := range table {
		if s, ok := item.(string); ok {
			out[key] = s
		}
	}
	return out
}
