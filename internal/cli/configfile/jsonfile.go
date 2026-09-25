// Package configfile reads and renders the JSON, TOML and YAML files agent
// runtimes keep their configuration in, keeping a person's key order and
// (for YAML) comments across a read-modify-write.
package configfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// JSONValue is one parsed JSON value: *JSONObject, []JSONValue, string,
// json.Number, bool, or nil. Objects keep their key order for stable
// output.
type JSONValue = any

// JSONObject is an insertion-ordered JSON object.
type JSONObject struct {
	keys   []string
	values map[string]JSONValue
}

// NewJSONObject returns an empty object.
func NewJSONObject() *JSONObject { return &JSONObject{values: map[string]JSONValue{}} }

// Get returns a member.
func (o *JSONObject) Get(key string) (JSONValue, bool) {
	value, ok := o.values[key]
	return value, ok
}

// Set adds or replaces a member, keeping an existing key's position.
func (o *JSONObject) Set(key string, value JSONValue) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

// Delete removes a member.
func (o *JSONObject) Delete(key string) {
	if _, ok := o.values[key]; !ok {
		return
	}
	delete(o.values, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// Keys returns the member names in order.
func (o *JSONObject) Keys() []string { return append([]string(nil), o.keys...) }

// Len reports the member count.
func (o *JSONObject) Len() int { return len(o.keys) }

// Object returns the member as an object, creating it when absent. A
// member of another type is replaced.
func (o *JSONObject) Object(key string) *JSONObject {
	if child, ok := o.values[key].(*JSONObject); ok {
		return child
	}
	child := NewJSONObject()
	o.Set(key, child)
	return child
}

// ObjectIfPresent returns the member as an object without creating it.
func (o *JSONObject) ObjectIfPresent(key string) (*JSONObject, bool) {
	child, ok := o.values[key].(*JSONObject)
	return child, ok
}

// Array returns the member as an array (nil when absent or another type).
func (o *JSONObject) Array(key string) ([]JSONValue, bool) {
	array, ok := o.values[key].([]JSONValue)
	return array, ok
}

// String returns a string member.
func (o *JSONObject) String(key string) string {
	value, _ := o.values[key].(string)
	return value
}

// Strings returns an array member's string items.
func (o *JSONObject) Strings(key string) []string {
	array, ok := o.Array(key)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(array))
	for _, item := range array {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// StringMap returns an object member's string values.
func (o *JSONObject) StringMap(key string) map[string]string {
	child, ok := o.ObjectIfPresent(key)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for _, name := range child.Keys() {
		if s, ok := child.values[name].(string); ok {
			out[name] = s
		}
	}
	return out
}

// ParseJSON decodes document into an ordered value tree.
func ParseJSON(document []byte) (JSONValue, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	value, err := parseJSONValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing data after the JSON document")
	}
	return value, nil
}

func parseJSONValue(decoder *json.Decoder) (JSONValue, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	return parseJSONFrom(decoder, token)
}

func parseJSONFrom(decoder *json.Decoder, token json.Token) (JSONValue, error) {
	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '{':
			object := NewJSONObject()
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("object key %v is not a string", keyToken)
				}
				value, err := parseJSONValue(decoder)
				if err != nil {
					return nil, err
				}
				object.Set(key, value)
			}
			if _, err := decoder.Token(); err != nil { // consume '}'
				return nil, err
			}
			return object, nil
		case '[':
			array := []JSONValue{}
			for decoder.More() {
				value, err := parseJSONValue(decoder)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			if _, err := decoder.Token(); err != nil { // consume ']'
				return nil, err
			}
			return array, nil
		}
		return nil, fmt.Errorf("unexpected delimiter %v", t)
	default:
		return t, nil // string, json.Number, bool, nil
	}
}

// FormatJSON renders a value tree with the given indent and a trailing
// newline, in the layout encoding/json uses for indented output.
func FormatJSON(value JSONValue, indent string) []byte {
	var buf bytes.Buffer
	writeJSON(&buf, value, indent, 0)
	buf.WriteByte('\n')
	return buf.Bytes()
}

func writeJSON(buf *bytes.Buffer, value JSONValue, indent string, depth int) {
	switch v := value.(type) {
	case *JSONObject:
		if v.Len() == 0 {
			buf.WriteString("{}")
			return
		}
		buf.WriteString("{\n")
		for i, key := range v.keys {
			buf.WriteString(strings.Repeat(indent, depth+1))
			writeJSONString(buf, key)
			buf.WriteString(": ")
			writeJSON(buf, v.values[key], indent, depth+1)
			if i < len(v.keys)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		buf.WriteString(strings.Repeat(indent, depth))
		buf.WriteByte('}')
	case []JSONValue:
		if len(v) == 0 {
			buf.WriteString("[]")
			return
		}
		buf.WriteString("[\n")
		for i, item := range v {
			buf.WriteString(strings.Repeat(indent, depth+1))
			writeJSON(buf, item, indent, depth+1)
			if i < len(v)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		buf.WriteString(strings.Repeat(indent, depth))
		buf.WriteByte(']')
	case string:
		writeJSONString(buf, v)
	case json.Number:
		buf.WriteString(v.String())
	case bool:
		if v {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case nil:
		buf.WriteString("null")
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			buf.WriteString("null")
			return
		}
		buf.Write(raw)
	}
}

// writeJSONString escapes like JSON.stringify, without HTML escaping, so
// a path containing "&" survives a round trip unchanged.
func writeJSONString(buf *bytes.Buffer, s string) {
	encoder := json.NewEncoder(buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(s)
	buf.Truncate(buf.Len() - 1) // Encode appends a newline
}

// JSONDocument is a JSON file whose root is an object, open for a
// read-modify-write that keeps the file's indentation.
type JSONDocument struct {
	Root    *JSONObject
	Existed bool
	indent  string
}

// ErrUnparsable marks a file that exists but could not be read in its
// format, which callers turn into a manual step rather than an edit.
var ErrUnparsable = errors.New("file could not be parsed")

// ErrNotStrictJSON marks a file that exists but is not strict JSON (a
// JSON5 OpenClaw config, say).
var ErrNotStrictJSON = fmt.Errorf("%w: file is not strict JSON", ErrUnparsable)

// LoadJSONObject reads a JSON file whose root is an object. A missing or
// empty file yields an empty document.
func LoadJSONObject(content []byte, existed bool) (*JSONDocument, error) {
	doc := &JSONDocument{Root: NewJSONObject(), Existed: existed, indent: "  "}
	if !existed || len(bytes.TrimSpace(content)) == 0 {
		return doc, nil
	}
	value, err := ParseJSON(content)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotStrictJSON, err)
	}
	root, ok := value.(*JSONObject)
	if !ok {
		return nil, fmt.Errorf("%w: root is not an object", ErrNotStrictJSON)
	}
	doc.Root = root
	doc.indent = detectIndent(content)
	return doc, nil
}

// Bytes renders the document.
func (d *JSONDocument) Bytes() []byte { return FormatJSON(d.Root, d.indent) }

// detectIndent reads the indentation of the first indented line and falls
// back to two spaces.
func detectIndent(content []byte) string {
	for _, line := range bytes.Split(content, []byte("\n")) {
		trimmed := bytes.TrimLeft(line, " \t")
		if len(trimmed) == len(line) || len(trimmed) == 0 {
			continue
		}
		prefix := line[:len(line)-len(trimmed)]
		if prefix[0] == '\t' {
			return "\t"
		}
		return string(prefix)
	}
	return "  "
}

// ReadFile returns a file's content and whether it existed.
func ReadFile(path string) ([]byte, bool, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return content, true, nil
}

// Write replaces a file atomically. An existing file keeps its mode; a
// new one gets mode.
func Write(path string, content []byte, mode os.FileMode) error {
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, path)
}
