// Package mcptools is the tool catalog of the management MCP server
// (/api/mcp/v1). Every tool is a thin door onto the product API: its
// handler dispatches requests through a Call, which re-enters the
// composed router in process with the tool caller's own credential, so
// authorization, validation, If-Match, audit provenance and key usage
// recording are the product API's own. Core registers its tools here,
// and an edition adds tools for its own routes (the hosted gateway and
// audit log) through runtime.ServerOptions.MCPTools with the same
// helpers, so every tool shares one result and error shape.
package mcptools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NoActiveRevision is the If-Match a revisioned resource with no active
// revision is activated against.
const NoActiveRevision = `"0"`

// Response is what one in-process product API request answered.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Dispatch sends one request to the product API in process under the
// tool caller's credential and answers whatever the API answered. A
// failure to dispatch at all is an error; an API refusal is a response.
type Dispatch func(method, path string, query url.Values, body any, header http.Header) (Response, error)

// Refusal is a product API refusal relayed to the agent verbatim: the
// same code, message, parameter and details an HTTP client would read,
// because the agent has to act on them the same way.
type Refusal struct {
	Status  int             `json:"-"`
	Type    string          `json:"type"`
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Param   string          `json:"param,omitempty"`
	Details json.RawMessage `json:"details,omitempty"`
}

func (e *Refusal) Error() string {
	var text strings.Builder
	text.WriteString(e.Code)
	text.WriteString(": ")
	text.WriteString(e.Message)
	if e.Param != "" {
		fmt.Fprintf(&text, " (param: %s)", e.Param)
	}
	if len(e.Details) > 0 {
		text.WriteString("\ndetails: ")
		text.Write(e.Details)
	}
	return text.String()
}

// Call is one tool invocation's view of the product API.
type Call struct {
	dispatch Dispatch
}

// NewCall wraps the composition's dispatcher for one tool invocation.
func NewCall(dispatch Dispatch) *Call { return &Call{dispatch: dispatch} }

// Do dispatches one request. A non-2xx answer becomes a *Refusal carrying
// the API's own error envelope.
func (c *Call) Do(method, path string, query url.Values, body any, header http.Header) (Response, error) {
	response, err := c.dispatch(method, path, query, body, header)
	if err != nil {
		return Response{}, err
	}
	if response.Status >= 200 && response.Status < 300 {
		return response, nil
	}
	return response, refusalFrom(response)
}

func (c *Call) Get(path string, query url.Values) (Response, error) {
	return c.Do(http.MethodGet, path, query, nil, nil)
}

func (c *Call) Post(path string, body any, header http.Header) (Response, error) {
	return c.Do(http.MethodPost, path, nil, body, header)
}

func (c *Call) Put(path string, body any) (Response, error) {
	return c.Do(http.MethodPut, path, nil, body, nil)
}

func (c *Call) Patch(path string, body any) (Response, error) {
	return c.Do(http.MethodPatch, path, nil, body, nil)
}

func (c *Call) Delete(path string, query url.Values, header http.Header) (Response, error) {
	return c.Do(http.MethodDelete, path, query, nil, header)
}

// ActiveRevision reads the active revision of a revisioned resource at
// base (an escalation path, a webhook endpoint, a pipeline, a team's
// escalation policy, an edition's own) and answers the If-Match naming
// it: its ETag, or NoActiveRevision when nothing is active.
func (c *Call) ActiveRevision(base string) (string, error) {
	current, err := c.Get(base, nil)
	if err != nil {
		var refusal *Refusal
		if errors.As(err, &refusal) && refusal.Status == http.StatusNotFound {
			return NoActiveRevision, nil
		}
		return "", err
	}
	if etag := current.Header.Get("ETag"); etag != "" {
		return etag, nil
	}
	return NoActiveRevision, nil
}

// ActivateRevision makes one revision of the resource at base active
// against whatever is active now, so an agent never choreographs
// If-Match by hand. It answers the activated revision.
func (c *Call) ActivateRevision(base string, revision int64) ([]byte, error) {
	etag, err := c.ActiveRevision(base)
	if err != nil {
		return nil, err
	}
	activated, err := c.Post(base+"/revisions/"+strconv.FormatInt(revision, 10)+"/activate", nil, http.Header{"If-Match": {etag}})
	if err != nil {
		return nil, err
	}
	return activated.Body, nil
}

// DeactivateRevision deactivates the active revision of the resource at
// base (DELETE base/active against the current ETag), the end of a
// webhook endpoint, a team's escalation policy or a gateway server.
func (c *Call) DeactivateRevision(base string) (Response, error) {
	etag, err := c.ActiveRevision(base)
	if err != nil {
		return Response{}, err
	}
	return c.Delete(base+"/active", nil, http.Header{"If-Match": {etag}})
}

// SaveRevision answers a revision a save tool created: activated against
// the current active revision when activate asks for it (the default),
// else left a draft. When activation is refused the error names the
// draft and the tool that activates it.
func (c *Call) SaveRevision(base string, created []byte, activate *bool, activateTool string) (*mcp.CallToolResult, any, error) {
	if !ActivateWanted(activate) {
		return Object(map[string]any{"revision": json.RawMessage(created), "activated": false})
	}
	number, err := RevisionNumber(created)
	if err != nil {
		return nil, nil, err
	}
	activated, err := c.ActivateRevision(base, number)
	if err != nil {
		return nil, nil, fmt.Errorf("revision %d was saved as a draft but not activated; fix the cause and call %s: %w", number, activateTool, err)
	}
	return Object(map[string]any{"revision": json.RawMessage(activated), "activated": true})
}

func refusalFrom(response Response) *Refusal {
	var envelope struct {
		Error Refusal `json:"error"`
	}
	if err := json.Unmarshal(response.Body, &envelope); err != nil || envelope.Error.Code == "" {
		return &Refusal{Status: response.Status, Type: "internal", Code: "unexpected_response", Message: fmt.Sprintf("The management API answered status %d.", response.Status)}
	}
	envelope.Error.Status = response.Status
	return &envelope.Error
}

// Catalog is the management MCP server's tool list.
type Catalog struct {
	server  *mcp.Server
	newCall func(ctx context.Context, request *mcp.CallToolRequest) *Call
}

// NewCatalog is the composition's: it binds the server to the dispatcher
// every tool call goes through.
func NewCatalog(server *mcp.Server, newCall func(ctx context.Context, request *mcp.CallToolRequest) *Call) *Catalog {
	return &Catalog{server: server, newCall: newCall}
}

// Registration adds tools to the catalog; an edition hands its own
// through runtime.ServerOptions.MCPTools.
type Registration func(*Catalog)

// Handler answers one typed tool call through the product API.
type Handler[In any] func(call *Call, input In) (*mcp.CallToolResult, any, error)

// Add registers one typed tool. Its input schema comes from In's json
// and jsonschema tags, and its handler answers with raw JSON from the
// product API, so every tool shares the same result and error shaping.
// A tool without a title is titled after its name ("list_pipelines" is
// "List pipelines"), and the title is repeated in its annotations, where
// clients that predate Tool.Title read it.
func Add[In any](catalog *Catalog, tool *mcp.Tool, handler Handler[In]) {
	if tool.Annotations == nil {
		panic("mcp tool " + tool.Name + " has no annotations")
	}
	titled := *tool
	if titled.Title == "" {
		titled.Title = title(titled.Name)
	}
	annotations := *titled.Annotations
	annotations.Title = titled.Title
	titled.Annotations = &annotations
	mcp.AddTool(catalog.server, &titled, func(ctx context.Context, request *mcp.CallToolRequest, input In) (*mcp.CallToolResult, any, error) {
		return handler(catalog.newCall(ctx, request), input)
	})
}

// title reads a tool name as a title: "list_pipelines" is "List pipelines".
func title(name string) string {
	words := strings.ReplaceAll(name, "_", " ")
	if words == "" {
		return ""
	}
	return strings.ToUpper(words[:1]) + words[1:]
}

// Result turns a JSON answer into the tool result: the structured
// content is the API's own object, and the text block is the same JSON
// for clients that read only text. A bodiless success answers ok.
func Result(body []byte) (*mcp.CallToolResult, any, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte(`{"ok":true}`)
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(body)}},
		StructuredContent: json.RawMessage(body),
	}, nil, nil
}

// Object assembles a composed answer (a saved revision plus whether it
// was activated) from raw JSON parts.
func Object(fields map[string]any) (*mcp.CallToolResult, any, error) {
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, nil, err
	}
	return Result(encoded)
}

// Answer relays a response, or the error that stood in its way, as the
// tool result: the shape of every tool that is one request.
func Answer(response Response, err error) (*mcp.CallToolResult, any, error) {
	if err != nil {
		return nil, nil, err
	}
	return Result(response.Body)
}

// ActivateWanted reads a save tool's activate flag, which defaults to true.
func ActivateWanted(activate *bool) bool { return activate == nil || *activate }

// RevisionNumber reads the number of a revision the API just created.
func RevisionNumber(body []byte) (int64, error) {
	var created struct {
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.Revision <= 0 {
		return 0, fmt.Errorf("the revision was saved but its number could not be read; list the revisions and activate it by number")
	}
	return created.Revision, nil
}

// Query builds a query from the non-empty values given, so a tool's
// optional filters reach the API only when the agent set them.
func Query(pairs ...string) url.Values {
	query := url.Values{}
	for index := 0; index+1 < len(pairs); index += 2 {
		if pairs[index+1] != "" {
			query.Set(pairs[index], pairs[index+1])
		}
	}
	if len(query) == 0 {
		return nil
	}
	return query
}

// Segment escapes one path segment.
func Segment(value string) string { return url.PathEscape(value) }

// Number formats an optional numeric filter for Query: empty, and so
// left out, when zero, which every such filter treats as its default.
func Number(value int64) string {
	if value == 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}

var (
	// ReadOnly tools change nothing.
	ReadOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPointer(false), OpenWorldHint: boolPointer(false)}
	// Additive writes: a new revision, a new key, a new team. Nothing
	// earlier is lost, so a retry at worst adds another revision.
	Additive = &mcp.ToolAnnotations{DestructiveHint: boolPointer(false), OpenWorldHint: boolPointer(false)}
	// Replacing writes: the call sets the whole value and repeating it
	// changes nothing further. The value it replaces is gone, which MCP
	// counts as destructive, so clients confirm the call first.
	Replacing = &mcp.ToolAnnotations{DestructiveHint: boolPointer(true), IdempotentHint: true, OpenWorldHint: boolPointer(false)}
	// Destructive writes end or revoke something for good, even though
	// its history stays: an archive, a revoked credential, a rotated
	// secret.
	Destructive = &mcp.ToolAnnotations{DestructiveHint: boolPointer(true), IdempotentHint: true, OpenWorldHint: boolPointer(false)}
	// Reaching tools send something out of withHuman (a signed webhook
	// test delivery) and change nothing inside it beyond the record.
	Reaching = &mcp.ToolAnnotations{DestructiveHint: boolPointer(false), OpenWorldHint: boolPointer(true)}
)

func boolPointer(value bool) *bool { return &value }
