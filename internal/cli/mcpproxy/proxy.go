// Package mcpproxy is `withhuman mcp <runtime> <server>`: a stdio MCP
// server the runtime starts, which relays every JSON-RPC frame to a
// provider-offered streamable-HTTP MCP endpoint (the hosted gateway) with
// the instance credential as the bearer. The credential stays in the
// CLI's private store instead of the runtime's configuration, and
// runtimes without a streamable-HTTP client reach the gateway all the
// same.
package mcpproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/withHumanAI/withHuman/internal/cli/jsonrpc"
)

// Run relays frames between stdin/stdout and the endpoint until stdin
// closes. Requests are forwarded concurrently, as the runtime sends them;
// an endpoint failure answers the request with a JSON-RPC error so the
// runtime sees why instead of hanging.
func Run(ctx context.Context, stdin io.Reader, stdout io.Writer, endpoint, token string, client *http.Client) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r := &relay{endpoint: endpoint, token: token, client: client, out: jsonrpc.NewLineWriter(stdout)}
	scanner := jsonrpc.NewScanner(stdin)
	var wg sync.WaitGroup
	for scanner.Scan() {
		frame := bytes.TrimSpace(scanner.Bytes())
		if len(frame) == 0 {
			continue
		}
		frame = append([]byte(nil), frame...)
		var msg jsonrpc.Message
		if err := json.Unmarshal(frame, &msg); err != nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.forward(ctx, frame, msg)
		}()
	}
	// The runtime went away: abandon whatever is still waiting on the
	// endpoint, which withdraws a pending approval on the provider side.
	cancel()
	wg.Wait()
	return scanner.Err()
}

type relay struct {
	endpoint string
	token    string
	client   *http.Client
	out      *jsonrpc.LineWriter

	mu              sync.Mutex
	sessionID       string
	protocolVersion string
	initializeID    json.RawMessage
}

func (r *relay) forward(ctx context.Context, frame []byte, msg jsonrpc.Message) {
	if msg.Method == "initialize" && msg.IsRequest() {
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		r.mu.Lock()
		r.initializeID, r.protocolVersion = msg.ID, params.ProtocolVersion
		r.mu.Unlock()
	}
	resp, err := r.post(ctx, frame)
	if err != nil {
		if ctx.Err() == nil && msg.IsRequest() {
			r.fail(msg.ID, "the withHuman gateway could not be reached: "+err.Error())
		}
		return
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		r.mu.Lock()
		r.sessionID = sid
		r.mu.Unlock()
	}
	switch {
	case resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent:
		return
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		if msg.IsRequest() {
			r.fail(msg.ID, fmt.Sprintf("the withHuman gateway refused this instance's credential (HTTP %d); run withhuman agent install for this runtime", resp.StatusCode))
		}
		return
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		if msg.IsRequest() {
			r.fail(msg.ID, fmt.Sprintf("the withHuman gateway answered HTTP %d", resp.StatusCode))
		}
		return
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		jsonrpc.ScanSSE(resp.Body, r.emit)
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, jsonrpc.MaxFrame))
	if err != nil {
		if msg.IsRequest() {
			r.fail(msg.ID, "the withHuman gateway's answer could not be read: "+err.Error())
		}
		return
	}
	if body = bytes.TrimSpace(body); len(body) > 0 {
		r.emit(body)
	}
}

// emit hands one frame (or batch) from the endpoint to the runtime,
// noting the negotiated protocol version on the way past.
func (r *relay) emit(payload []byte) {
	var msg jsonrpc.Message
	if json.Unmarshal(payload, &msg) == nil && msg.IsRequest() && len(msg.Result) > 0 {
		r.mu.Lock()
		if bytes.Equal(msg.ID, r.initializeID) {
			var result struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			if json.Unmarshal(msg.Result, &result) == nil && result.ProtocolVersion != "" {
				r.protocolVersion = result.ProtocolVersion
			}
		}
		r.mu.Unlock()
	}
	r.out.WriteLine(payload)
}

func (r *relay) fail(id json.RawMessage, message string) {
	_ = r.out.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": id, "error": jsonrpc.Error{Code: -32603, Message: message}})
}

func (r *relay) post(ctx context.Context, frame []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(frame))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+r.token)
	r.mu.Lock()
	if r.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", r.sessionID)
	}
	if r.protocolVersion != "" {
		req.Header.Set("MCP-Protocol-Version", r.protocolVersion)
	}
	r.mu.Unlock()
	return r.client.Do(req)
}
