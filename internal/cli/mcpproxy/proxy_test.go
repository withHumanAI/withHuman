package mcpproxy

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/withHumanAI/withHuman/internal/cli/clitest"
)

// host stands in for a runtime: it keeps the relay's stdin open while it
// talks, the way a real MCP host does, and reads answers one frame at a
// time.
type host struct {
	t      *testing.T
	stdin  *io.PipeWriter
	stdout *bufio.Scanner
	done   chan error
}

func startHost(t *testing.T, endpoint, token string) *host {
	t.Helper()
	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()
	h := &host{t: t, stdin: inWriter, stdout: bufio.NewScanner(outReader), done: make(chan error, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	go func() {
		defer cancel()
		defer outWriter.Close()
		h.done <- Run(ctx, inReader, outWriter, endpoint, token, &http.Client{})
	}()
	t.Cleanup(func() { inWriter.Close(); cancel() })
	return h
}

func (h *host) send(frame string) {
	h.t.Helper()
	if _, err := io.WriteString(h.stdin, frame+"\n"); err != nil {
		h.t.Fatal(err)
	}
}

func (h *host) recv() map[string]any {
	h.t.Helper()
	if !h.stdout.Scan() {
		h.t.Fatalf("relay ended: %v", h.stdout.Err())
	}
	var msg map[string]any
	if err := json.Unmarshal(h.stdout.Bytes(), &msg); err != nil {
		h.t.Fatalf("relay wrote %q: %v", h.stdout.Text(), err)
	}
	return msg
}

func (h *host) close() {
	h.t.Helper()
	h.stdin.Close()
	if err := <-h.done; err != nil {
		h.t.Fatalf("run: %v", err)
	}
}

func TestRelayForwardsFramesWithTheBearer(t *testing.T) {
	server := httptest.NewServer(clitest.RequireBearer(clitest.MCPHTTPHandler(), "whc_secret"))
	defer server.Close()
	h := startHost(t, server.URL+"/mcp", "whc_secret")
	h.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"claude","version":"1"}}}`)
	if result, _ := h.recv()["result"].(map[string]any); result["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize = %v", result)
	}
	h.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	h.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	if result, _ := h.recv()["result"].(map[string]any); result["nextCursor"] != "page-2" {
		t.Fatalf("tools/list = %v", result)
	}
	h.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`)
	answer := h.recv()
	raw, _ := json.Marshal(answer["result"])
	if answer["id"] != float64(3) || !strings.Contains(string(raw), "echo: hi") {
		t.Fatalf("tools/call = %v", answer)
	}
	h.close()
}

func TestRelayReportsARefusedCredential(t *testing.T) {
	server := httptest.NewServer(clitest.RequireBearer(clitest.MCPHTTPHandler(), "whc_secret"))
	defer server.Close()
	h := startHost(t, server.URL+"/mcp", "whc_revoked")
	h.send(`{"jsonrpc":"2.0","id":7,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	answer := h.recv()
	problem, _ := answer["error"].(map[string]any)
	if answer["id"] != float64(7) || problem == nil || !strings.Contains(problem["message"].(string), "refused this instance's credential") {
		t.Fatalf("answer = %v", answer)
	}
	h.close()
}

// A streamed answer (progress keepalives, then the result) reaches the
// runtime frame by frame, and the session id and negotiated protocol
// version travel on later requests.
func TestRelayStreamsEventsAndKeepsTheSession(t *testing.T) {
	var seenSession, seenVersion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &msg)
		switch msg.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-9")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(msg.ID) + `,"result":{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":"t","version":"1"}}}`))
		case "tools/call":
			seenSession, seenVersion = r.Header.Get("Mcp-Session-Id"), r.Header.Get("MCP-Protocol-Version")
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progressToken\":\"p\",\"progress\":1}}\n\n"))
			_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"id\":" + string(msg.ID) + ",\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"done\"}]}}\n\n"))
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer server.Close()
	h := startHost(t, server.URL, "whc_secret")
	h.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if h.recv()["id"] != float64(1) {
		t.Fatal("initialize answer missing")
	}
	h.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	h.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"slow"}}`)
	if progress := h.recv(); progress["method"] != "notifications/progress" {
		t.Fatalf("first frame = %v", progress)
	}
	if answer := h.recv(); answer["id"] != float64(2) {
		t.Fatalf("second frame = %v", answer)
	}
	if seenSession != "session-9" || seenVersion != "2025-03-26" {
		t.Fatalf("session %q version %q", seenSession, seenVersion)
	}
	h.close()
}

// The runtime closing stdin ends the relay even while a call waits on the
// endpoint, which withdraws the call on the provider side.
func TestRelayEndsWithTheRuntime(t *testing.T) {
	released := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		select {
		case <-r.Context().Done():
			close(released)
		case <-time.After(10 * time.Second):
		}
	}))
	defer server.Close()
	h := startHost(t, server.URL, "whc_secret")
	h.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"slow"}}`)
	time.Sleep(100 * time.Millisecond)
	started := time.Now()
	h.close()
	if time.Since(started) > 2*time.Second {
		t.Fatalf("the relay waited %s for the endpoint after the runtime left", time.Since(started))
	}
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("the endpoint call was not abandoned")
	}
}
