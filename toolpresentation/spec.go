// Package toolpresentation is the catalog of how a tool call's arguments
// are shown to a reviewer. A request's arguments are the ground truth of
// the action; a presentation never adds to them and never
// hides any of them. It only says which argument is an email body to
// render, which is a shell command, which pair of strings is a diff, and
// what the request is called. Every client (the product frontend, the
// phone apps) interprets the same declarative specification, so a
// presentation is data that travels on the request, never code.
//
// The package is a leaf: standard library only, no product imports.
package toolpresentation

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Kind says how one argument is rendered.
type Kind string

const (
	// KindText is a short value on one line.
	KindText Kind = "text"
	// KindProse is long plain text, wrapped, read as writing.
	KindProse Kind = "prose"
	// KindMarkdown is Markdown, rendered with the product's own type.
	KindMarkdown Kind = "markdown"
	// KindHTML is an HTML document or fragment, rendered in a sandbox
	// that runs no script and loads nothing, with the source one click
	// away.
	KindHTML Kind = "html"
	// KindCode is source or machine text in a mono block; Language is
	// a label for the reader.
	KindCode Kind = "code"
	// KindShell is a command line.
	KindShell Kind = "shell"
	// KindDiff is a change: either a unified diff at Path, or the pair of
	// strings at Before and After shown as one.
	KindDiff Kind = "diff"
	// KindPath is a file path.
	KindPath Kind = "path"
	// KindURL is a link, shown as text and never followed automatically.
	KindURL Kind = "url"
	// KindEmail is an address.
	KindEmail Kind = "email"
	// KindReference is an opaque identifier.
	KindReference Kind = "reference"
	// KindEnum is one of a small set of values, shown as a badge.
	KindEnum Kind = "enum"
	// KindDateTime is an instant, shown in the reader's locale.
	KindDateTime Kind = "datetime"
	// KindList is an array whose items render with the Item field, or as
	// text when no Item is given.
	KindList Kind = "list"
	// KindJSON is structured data shown as pretty-printed JSON.
	KindJSON Kind = "json"
)

// Kinds is every kind, in the order the editor offers them.
var Kinds = []Kind{
	KindText, KindProse, KindMarkdown, KindHTML, KindCode, KindShell, KindDiff,
	KindPath, KindURL, KindEmail, KindReference, KindEnum, KindDateTime, KindList, KindJSON,
}

// Valid reports whether the kind is one of Kinds.
func (k Kind) Valid() bool {
	for _, kind := range Kinds {
		if kind == k {
			return true
		}
	}
	return false
}

// Role says where a field sits: in the main block a reviewer reads
// first, or in the details folded away underneath it.
type Role string

const (
	RolePrimary Role = "primary"
	RoleDetail  Role = "detail"
)

// Field is one rendered argument.
type Field struct {
	// Path is a JSON pointer into the arguments ("/body", "/edits/0").
	// Required unless the field is a diff pair. A field whose path is
	// absent from a particular request's arguments is skipped.
	Path string `json:"path,omitempty"`
	// Label replaces the humanised key.
	Label string `json:"label,omitempty"`
	Kind  Kind   `json:"kind"`
	// Language labels a code field for the reader (for example "python").
	Language string `json:"language,omitempty"`
	// Before and After name the two sides of a diff pair. Relative to the
	// item when the field is a list's Item, absolute otherwise.
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
	// Item says how each element of a list renders; its paths are
	// relative to the element. Empty means each element is text.
	Item *Field `json:"item,omitempty"`
	// Role defaults to primary.
	Role Role `json:"role,omitempty"`
}

// Spec is one tool's presentation.
type Spec struct {
	// Title is what the request is called, in place of the humanised
	// tool name. It may interpolate scalar arguments with {{/path}}; a
	// title whose every placeholder is absent falls back to the tool
	// name.
	Title string `json:"title,omitempty"`
	// Fields are the arguments to render, in order. Arguments no field
	// names still show, after these, rendered by shape.
	Fields []Field `json:"fields"`
}

const (
	MaxFields         = 64
	MaxTitleLength    = 200
	MaxLabelLength    = 80
	MaxPathLength     = 200
	MaxLanguageLength = 32
)

// ErrInvalid marks a presentation that fails validation. Every
// validation error wraps it.
var ErrInvalid = errors.New("invalid tool presentation")

var (
	placeholderPattern = regexp.MustCompile(`\{\{([^{}]*)\}\}`)
	languagePattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9+#.\-]*$`)
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), ErrInvalid)
}

// Validate checks the specification's shape. It does not need a sample
// of the arguments: paths are checked for form only, since the same
// tool may be called with different shapes.
func (s Spec) Validate() error {
	if len(s.Title) > MaxTitleLength {
		return invalid("title must be at most %d characters", MaxTitleLength)
	}
	if strings.ContainsAny(s.Title, "\n\r") {
		return invalid("title must be one line")
	}
	for _, match := range placeholderPattern.FindAllStringSubmatch(s.Title, -1) {
		if err := validatePointer(strings.TrimSpace(match[1])); err != nil {
			return invalid("title placeholder %q: %v", match[0], err)
		}
	}
	if len(s.Fields) > MaxFields {
		return invalid("at most %d fields", MaxFields)
	}
	for index, field := range s.Fields {
		if err := field.validate(false); err != nil {
			return invalid("field %d: %v", index+1, err)
		}
	}
	return nil
}

func (f Field) validate(item bool) error {
	if !f.Kind.Valid() {
		return fmt.Errorf("unknown kind %q", f.Kind)
	}
	if len(f.Label) > MaxLabelLength {
		return fmt.Errorf("label must be at most %d characters", MaxLabelLength)
	}
	if strings.ContainsAny(f.Label, "\n\r") {
		return fmt.Errorf("label must be one line")
	}
	switch f.Role {
	case "", RolePrimary, RoleDetail:
	default:
		return fmt.Errorf("unknown role %q", f.Role)
	}
	if f.Language != "" {
		if f.Kind != KindCode {
			return fmt.Errorf("language applies to code fields only")
		}
		if len(f.Language) > MaxLanguageLength || !languagePattern.MatchString(f.Language) {
			return fmt.Errorf("language must be a short lowercase name")
		}
	}
	pair := f.Before != "" || f.After != ""
	if pair {
		if f.Kind != KindDiff {
			return fmt.Errorf("before and after apply to diff fields only")
		}
		if f.Before == "" || f.After == "" {
			return fmt.Errorf("a diff pair needs both before and after")
		}
		if f.Path != "" {
			return fmt.Errorf("a diff pair takes no path")
		}
		if err := validateFieldPointer(f.Before, item); err != nil {
			return fmt.Errorf("before: %w", err)
		}
		if err := validateFieldPointer(f.After, item); err != nil {
			return fmt.Errorf("after: %w", err)
		}
	} else {
		if f.Path == "" && !item {
			return fmt.Errorf("path is required")
		}
		if f.Path != "" {
			if err := validateFieldPointer(f.Path, item); err != nil {
				return fmt.Errorf("path: %w", err)
			}
		}
	}
	if f.Item != nil {
		if f.Kind != KindList {
			return fmt.Errorf("item applies to list fields only")
		}
		if item {
			return fmt.Errorf("a list item cannot itself be a list of items")
		}
		if f.Item.Item != nil {
			return fmt.Errorf("item: a list item cannot itself be a list of items")
		}
		if f.Item.Role != "" {
			return fmt.Errorf("item: role applies to top-level fields only")
		}
		if err := f.Item.validate(true); err != nil {
			return fmt.Errorf("item: %w", err)
		}
	}
	return nil
}

// validateFieldPointer accepts an absolute pointer for a top-level field
// and a relative one (no leading slash, or empty meaning the element
// itself) for a list item's field.
func validateFieldPointer(pointer string, item bool) error {
	if item {
		if pointer == "" {
			return nil
		}
		if strings.HasPrefix(pointer, "/") {
			return fmt.Errorf("must be relative to the list element")
		}
		return validatePointer("/" + pointer)
	}
	return validatePointer(pointer)
}

// validatePointer checks an RFC 6901 pointer: a leading slash, sane
// length, and only the two defined escapes.
func validatePointer(pointer string) error {
	if pointer == "" || !strings.HasPrefix(pointer, "/") {
		return fmt.Errorf("must be a JSON pointer starting with /")
	}
	if len(pointer) > MaxPathLength {
		return fmt.Errorf("must be at most %d characters", MaxPathLength)
	}
	for index := 0; index < len(pointer); index++ {
		if pointer[index] != '~' {
			continue
		}
		if index+1 >= len(pointer) || (pointer[index+1] != '0' && pointer[index+1] != '1') {
			return fmt.Errorf("has a bad ~ escape")
		}
	}
	return nil
}

// Normalize fills in the defaults a client would otherwise have to: the
// primary role and a non-nil field list.
func (s Spec) Normalize() Spec {
	out := Spec{Title: strings.TrimSpace(s.Title), Fields: make([]Field, len(s.Fields))}
	for index, field := range s.Fields {
		if field.Role == "" {
			field.Role = RolePrimary
		}
		out.Fields[index] = field
	}
	return out
}
