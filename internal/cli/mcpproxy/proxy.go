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
// runtime sees why instead of hanging. runtime is the runtime key the
// install enrolled, named in the advice for a refused credential.
func Run(ctx context.Context, stdin io.Reader, stdout io.Writer, runtime, endpoint, token string, client *http.Client) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r := &relay{runtime: runtime, endpoint: endpoint, token: token, client: client, out: jsonrpc.NewLineWriter(stdout)}
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
	runtime  string
	endpoint string
	token    string
	client   *http.Client
	out      *jsonrpc.LineWriter

	// renewing serialises session renewal, so calls that all find the
	// session gone after a gateway restart start one new session.
	renewing sync.Mutex

	mu              sync.Mutex
	sessionID       string
	protocolVersion string
	initializeID    json.RawMessage
	// initialize and initialized are the runtime's own handshake frames,
	// replayed to start a new session when the gateway forgets this one.
	initialize  []byte
	initialized []byte
}

func (r *relay) forward(ctx context.Context, frame []byte, msg jsonrpc.Message) {
	r.remember(frame, msg)
	sessionID := r.session()
	resp, err := r.post(ctx, frame, sessionID)
	// The gateway answers 404 for a session it does not know, which is
	// every session after it restarts. The transport says to start a new
	// session and carry on, so the runtime never sees the restart.
	if err == nil && resp.StatusCode == http.StatusNotFound && sessionID != "" {
		resp.Body.Close()
		if !r.renew(ctx, sessionID) {
			if ctx.Err() == nil && msg.IsRequest() {
				r.fail(msg.ID, fmt.Sprintf("the withHuman gateway answered HTTP %d", http.StatusNotFound))
			}
			return
		}
		if msg.Method == "notifications/initialized" {
			return
		}
		resp, err = r.post(ctx, frame, r.session())
	}
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
			// An archived agent's credentials are revoked and a disabled
			// one's stop resolving, so enrolling again is not always the way
			// out: say what may have happened and how to remove withHuman.
			r.fail(msg.ID, fmt.Sprintf("the withHuman gateway refused this instance's credential (HTTP %d); its agent may have been archived or disabled, or the credential revoked. Ask whoever manages the agent, run withhuman agent install %s to enroll again, or run withhuman agent eject %s to remove withHuman from this runtime", resp.StatusCode, r.runtime, r.runtime))
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

// remember keeps the runtime's handshake for renew. A new initialize
// starts a new session, so it goes out without the old session id.
func (r *relay) remember(frame []byte, msg jsonrpc.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case msg.Method == "initialize" && msg.IsRequest():
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		r.initialize, r.initializeID, r.protocolVersion = frame, msg.ID, params.ProtocolVersion
		r.initialized, r.sessionID = nil, ""
	case msg.Method == "notifications/initialized" && !msg.IsRequest():
		r.initialized = frame
	}
}

func (r *relay) session() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessionID
}

// renew replaces the session the gateway no longer knows (stale) by
// replaying the runtime's initialize and notifications/initialized
// without a session id. The runtime already has its initialize result,
// so the replayed one is kept from it. The gateway's tools are not
// tied to a session, and it declares no tools listChanged capability,
// so the runtime is not told to list them again. renew reports whether
// a live session is in place to retry on; on failure the stale id stays,
// so the next call tries again.
func (r *relay) renew(ctx context.Context, stale string) bool {
	r.renewing.Lock()
	defer r.renewing.Unlock()
	r.mu.Lock()
	current, initialize, initialized := r.sessionID, r.initialize, r.initialized
	r.mu.Unlock()
	if current != stale {
		return true
	}
	if initialize == nil {
		return false
	}
	sessionID, version, err := r.handshake(ctx, initialize, initialized)
	if err != nil {
		return false
	}
	r.mu.Lock()
	r.sessionID = sessionID
	if version != "" {
		r.protocolVersion = version
	}
	r.mu.Unlock()
	return true
}

// handshake starts a session with the runtime's own initialize frame and
// returns the session id and protocol version the gateway answered with.
func (r *relay) handshake(ctx context.Context, initialize, initialized []byte) (sessionID, version string, err error) {
	var request jsonrpc.Message
	if err := json.Unmarshal(initialize, &request); err != nil {
		return "", "", err
	}
	resp, err := r.post(ctx, initialize, "")
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("initialize answered HTTP %d", resp.StatusCode)
	}
	sessionID = resp.Header.Get("Mcp-Session-Id")
	var result json.RawMessage
	keep := func(payload []byte) {
		var msg jsonrpc.Message
		if json.Unmarshal(payload, &msg) == nil && bytes.Equal(msg.ID, request.ID) && len(msg.Result) > 0 {
			result = msg.Result
		}
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		jsonrpc.ScanSSE(resp.Body, keep)
	} else {
		body, err := io.ReadAll(io.LimitReader(resp.Body, jsonrpc.MaxFrame))
		if err != nil {
			return "", "", err
		}
		keep(bytes.TrimSpace(body))
	}
	if result == nil {
		return "", "", fmt.Errorf("initialize answered without a result")
	}
	var answer struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(result, &answer)
	if initialized != nil {
		resp, err := r.post(ctx, initialized, sessionID)
		if err != nil {
			return "", "", err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", "", fmt.Errorf("notifications/initialized answered HTTP %d", resp.StatusCode)
		}
	}
	return sessionID, answer.ProtocolVersion, nil
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

// post sends one frame on the given session; an empty sessionID sends
// none.
func (r *relay) post(ctx context.Context, frame []byte, sessionID string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(frame))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+r.token)
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	r.mu.Lock()
	if r.protocolVersion != "" {
		req.Header.Set("MCP-Protocol-Version", r.protocolVersion)
	}
	r.mu.Unlock()
	return r.client.Do(req)
}
