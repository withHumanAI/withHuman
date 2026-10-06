package mcpproxy

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
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
		h.done <- Run(ctx, inReader, outWriter, "claude-code", endpoint, token, &http.Client{})
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
	if answer["id"] != float64(7) || problem == nil || !strings.Contains(problem["message"].(string), "refused this instance's credential") || !strings.Contains(problem["message"].(string), "withhuman agent eject claude-code") {
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

// restartingGateway hands out a session on initialize and answers 404 for
// any session it has forgotten, the way the gateway does after its pods
// restart.
type restartingGateway struct {
	mu          sync.Mutex
	sessions    map[string]bool
	issued      int
	initializes int
	initialized []string
	calls       []string
	versions    []string
	refuse      bool
}

func (g *restartingGateway) restart(refuseInitialize bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sessions, g.refuse = map[string]bool{}, refuseInitialize
}

func (g *restartingGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	_ = json.Unmarshal(body, &msg)
	g.mu.Lock()
	defer g.mu.Unlock()
	if msg.Method == "initialize" {
		g.initializes++
		if r.Header.Get("Mcp-Session-Id") != "" || g.refuse {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		g.issued++
		session := "session-" + strconv.Itoa(g.issued)
		g.sessions[session] = true
		w.Header().Set("Mcp-Session-Id", session)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"id\":" + string(msg.ID) + ",\"result\":{\"protocolVersion\":\"2025-06-18\",\"capabilities\":{\"tools\":{}},\"serverInfo\":{\"name\":\"t\",\"version\":\"1\"}}}\n\n"))
		return
	}
	session := r.Header.Get("Mcp-Session-Id")
	if !g.sessions[session] {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	switch msg.Method {
	case "notifications/initialized":
		g.initialized = append(g.initialized, session)
		w.WriteHeader(http.StatusAccepted)
	case "tools/call":
		g.calls = append(g.calls, session)
		g.versions = append(g.versions, r.Header.Get("MCP-Protocol-Version"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(msg.ID) + `,"result":{"content":[{"type":"text","text":"done"}]}}`))
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

// startHandshake initializes through the relay and waits for the
// gateway to hold the runtime's notifications/initialized, which the
// relay forwards concurrently with later frames.
func startHandshake(t *testing.T, h *host, gateway *restartingGateway) {
	t.Helper()
	h.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"claude","version":"1"}}}`)
	if answer := h.recv(); answer["id"] != float64(1) || answer["result"] == nil {
		t.Fatalf("initialize = %v", answer)
	}
	h.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		gateway.mu.Lock()
		held := len(gateway.initialized) > 0
		gateway.mu.Unlock()
		if held {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("notifications/initialized never reached the gateway")
		}
	}
}

// After a gateway restart forgets the session, the relay starts a new one
// with the runtime's own handshake and retries the calls on it. The
// runtime sees only the answers to its calls: no error and no second
// initialize result. Calls that fail together start one session.
func TestRelayStartsANewSessionWhenTheGatewayForgetsIt(t *testing.T) {
	gateway := &restartingGateway{sessions: map[string]bool{}}
	server := httptest.NewServer(gateway)
	defer server.Close()
	h := startHost(t, server.URL, "whc_secret")
	startHandshake(t, h, gateway)
	h.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo"}}`)
	if answer := h.recv(); answer["id"] != float64(2) || answer["result"] == nil {
		t.Fatalf("call before the restart = %v", answer)
	}

	gateway.restart(false)
	h.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo"}}`)
	h.send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo"}}`)
	answered := map[float64]bool{}
	for range 2 {
		answer := h.recv()
		if answer["result"] == nil {
			t.Fatalf("call after the restart = %v", answer)
		}
		answered[answer["id"].(float64)] = true
	}
	if !answered[3] || !answered[4] {
		t.Fatalf("answered %v", answered)
	}
	h.close()

	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	if gateway.initializes != 2 {
		t.Fatalf("initializes = %d, want the runtime's and one replay", gateway.initializes)
	}
	if strings.Join(gateway.initialized, ",") != "session-1,session-2" {
		t.Fatalf("notifications/initialized on %v", gateway.initialized)
	}
	if strings.Join(gateway.calls, ",") != "session-1,session-2,session-2" {
		t.Fatalf("calls on %v", gateway.calls)
	}
	for _, version := range gateway.versions {
		if version != "2025-06-18" {
			t.Fatalf("protocol versions %v", gateway.versions)
		}
	}
}

// When the new session cannot be started the call fails as before, and
// the next call tries again.
func TestRelayReportsTheForgottenSessionWhenRenewalFails(t *testing.T) {
	gateway := &restartingGateway{sessions: map[string]bool{}}
	server := httptest.NewServer(gateway)
	defer server.Close()
	h := startHost(t, server.URL, "whc_secret")
	startHandshake(t, h, gateway)

	gateway.restart(true)
	h.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo"}}`)
	answer := h.recv()
	problem, _ := answer["error"].(map[string]any)
	if answer["id"] != float64(2) || problem == nil || problem["message"] != "the withHuman gateway answered HTTP 404" {
		t.Fatalf("answer = %v", answer)
	}

	gateway.restart(false)
	h.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo"}}`)
	if answer := h.recv(); answer["id"] != float64(3) || answer["result"] == nil {
		t.Fatalf("call after recovery = %v", answer)
	}
	h.close()
}
