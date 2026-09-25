package configfile

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// LoadYAMLMapping decodes a YAML document into a node tree whose root is
// a mapping, so edits keep the person's comments and key order. A missing
// or empty file is an empty mapping.
func LoadYAMLMapping(content []byte, existed bool) (*yaml.Node, error) {
	if !existed || len(bytes.TrimSpace(content)) == 0 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("%w: parse yaml: %v", ErrUnparsable, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%w: parse yaml: root is not a mapping", ErrUnparsable)
	}
	return root, nil
}

// FormatYAML renders a root node with two-space indentation.
func FormatYAML(root *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(root); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// YAMLGet returns the value node for key in a mapping.
func YAMLGet(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// YAMLEnsureMapping returns the mapping under key, appending one when
// absent. A present node of another kind is replaced.
func YAMLEnsureMapping(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			if mapping.Content[i+1].Kind != yaml.MappingNode {
				mapping.Content[i+1] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			}
			return mapping.Content[i+1]
		}
	}
	value := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapping.Content = append(mapping.Content, YAMLScalar(key), value)
	return value
}

// YAMLSet sets key in a mapping to value, replacing an existing entry in
// place or appending a new one.
func YAMLSet(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content, YAMLScalar(key), value)
}

// YAMLDeleteKey removes key from a mapping and reports whether it was there.
func YAMLDeleteKey(mapping *yaml.Node, key string) bool {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return true
		}
	}
	return false
}

// YAMLScalar builds a plain string scalar.
func YAMLScalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

// YAMLMapping builds a mapping from key/value pairs in order.
func YAMLMapping(pairs ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: pairs}
}

// YAMLString reads a string scalar under key, or "".
func YAMLString(mapping *yaml.Node, key string) string {
	node := YAMLGet(mapping, key)
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return node.Value
}

// YAMLStrings reads a sequence of scalars under key.
func YAMLStrings(mapping *yaml.Node, key string) []string {
	node := YAMLGet(mapping, key)
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		if item.Kind == yaml.ScalarNode {
			out = append(out, item.Value)
		}
	}
	return out
}

// YAMLStringMap reads a mapping of scalars under key.
func YAMLStringMap(mapping *yaml.Node, key string) map[string]string {
	node := YAMLGet(mapping, key)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	out := map[string]string{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i+1].Kind == yaml.ScalarNode {
			out[node.Content[i].Value] = node.Content[i+1].Value
		}
	}
	return out
}
