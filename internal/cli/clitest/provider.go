package clitest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/agentruntime"
	"github.com/withHumanAI/withHuman/domain"
)

// Provider is an in-memory stand-in for the server's onboarding and AAP
// surfaces: one session driven through the design doc 09 state machine by
// the CLI on one side and the Human helper on the other.
type Provider struct {
	Server    *httptest.Server
	SetupCode string
	Token     string
	Human     *Human

	// Decision is the terminal status every AAP request gets; tests set it
	// to exercise a hook's deny and expiry paths.
	Decision string
	// AgentMCPServers is what runtime-config offers every agent; a test
	// sets it to stand in for the hosted gateway.
	AgentMCPServers []domain.AgentMCPServer

	mu          sync.Mutex
	session     domain.OnboardingSession
	changed     chan struct{}
	enrolled    bool
	credentials map[string]string // whc_ token -> runtime
	requests    map[string]map[string]any
	exchanges   int
}

// Human drives the browser side of the session from a test.
type Human struct{ p *Provider }

// NewProvider starts the fake and registers its shutdown with t.
func NewProvider(t *testing.T) *Provider {
	t.Helper()
	p := &Provider{
		SetupCode:   "ABCD-EFGH-JKLM",
		Token:       "whb_session1_secret",
		Decision:    "approved",
		changed:     make(chan struct{}),
		credentials: map[string]string{},
		requests:    map[string]map[string]any{},
	}
	now := time.Now().UTC()
	p.session = domain.OnboardingSession{
		ID: "session1", OrganizationID: "org1", MembershipID: "member1",
		Status: domain.OnboardingAwaitingCLI, Version: 1,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	p.Human = &Human{p: p}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/runtime-config", p.runtimeConfig)
	mux.HandleFunc("POST /api/onboarding/v1/sessions/exchange", p.exchange)
	mux.HandleFunc("GET /api/onboarding/v1/session", p.auth(p.getSession))
	mux.HandleFunc("PUT /api/onboarding/v1/session/detections", p.auth(p.putDetections))
	mux.HandleFunc("PUT /api/onboarding/v1/session/tools", p.auth(p.putTools))
	mux.HandleFunc("POST /api/onboarding/v1/session/enroll", p.auth(p.enroll))
	mux.HandleFunc("PUT /api/onboarding/v1/session/install-results", p.auth(p.putInstallResults))
	mux.HandleFunc("PUT /api/onboarding/v1/session/error", p.auth(p.putError))
	mux.HandleFunc("GET /api/v1/agent_instances/self", p.aapAuth(p.instanceSelf))
	mux.HandleFunc("POST /api/aap/v1/requests", p.aapAuth(p.aapCreate))
	mux.HandleFunc("GET /api/aap/v1/requests/{id}", p.aapAuth(p.aapGet))
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Server.Close)
	return p
}

// URL is the provider origin.
func (p *Provider) URL() string { return p.Server.URL }

// Session returns a copy of the current session.
func (p *Provider) Session() domain.OnboardingSession {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.session
}

// Exchanges counts setup-code redemptions.
func (p *Provider) Exchanges() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exchanges
}

// Credentials returns the whc_ tokens minted at enroll, by runtime.
func (p *Provider) Credentials() map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[string]string{}
	for token, runtime := range p.credentials {
		out[runtime] = token
	}
	return out
}

// AddCredential registers a whc_ token the AAP surface accepts.
func (p *Provider) AddCredential(token, runtime string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.credentials[token] = runtime
}

// WaitFor blocks until the session reaches status or the timeout passes.
func (p *Provider) WaitFor(t *testing.T, status domain.OnboardingStatus, timeout time.Duration) domain.OnboardingSession {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		p.mu.Lock()
		session, changed := p.session, p.changed
		p.mu.Unlock()
		if session.Status == status {
			return session
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("session did not reach %s within %s (status %s, last_error %+v)", status, timeout, session.Status, session.LastError)
		}
		select {
		case <-changed:
		case <-time.After(remaining):
		}
	}
}

// transition applies a status change under the design doc table.
func (p *Provider) transition(to domain.OnboardingStatus) error {
	if !domain.OnboardingCanTransition(p.session.Status, to) {
		return &providerError{http.StatusConflict, "onboarding_session_state", fmt.Sprintf("cannot move from %s to %s", p.session.Status, to)}
	}
	p.session.Status = to
	return nil
}

// bump records a write: version, timestamp, and a wake-up for pollers.
func (p *Provider) bump() {
	p.session.Version++
	p.session.UpdatedAt = time.Now().UTC()
	close(p.changed)
	p.changed = make(chan struct{})
}

type providerError struct {
	status  int
	code    string
	message string
}

func (e *providerError) Error() string { return e.code + ": " + e.message }

func writeError(w http.ResponseWriter, err error) {
	perr, ok := err.(*providerError)
	if !ok {
		perr = &providerError{http.StatusInternalServerError, "internal", err.Error()}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(perr.status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"type": "invalid_request", "code": perr.code, "message": perr.message, "request_id": "req-fake",
	}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (p *Provider) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+p.Token {
			writeError(w, &providerError{http.StatusUnauthorized, "unauthenticated", "whb_ bearer required"})
			return
		}
		next(w, r)
	}
}

func (p *Provider) runtimeConfig(w http.ResponseWriter, _ *http.Request) {
	p.mu.Lock()
	servers := append([]domain.AgentMCPServer{}, p.AgentMCPServers...)
	p.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"public_origin": p.Server.URL, "version": "test", "agent_mcp_servers": servers})
}

func (p *Provider) exchange(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SetupCode string                   `json:"setup_code"`
		Machine   domain.OnboardingMachine `json:"machine"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, &providerError{http.StatusBadRequest, "invalid_json", err.Error()})
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if body.SetupCode != p.SetupCode || p.exchanges > 0 {
		writeError(w, &providerError{http.StatusNotFound, "setup_code_invalid", "The setup code is invalid or was already used."})
		return
	}
	if err := p.transition(domain.OnboardingConnected); err != nil {
		writeError(w, err)
		return
	}
	p.exchanges++
	p.session.Machine = body.Machine
	p.bump()
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": p.session.ID, "token": p.Token, "expires_at": p.session.ExpiresAt,
		"provider_url": p.Server.URL, "session": p.session,
	})
}

func (p *Provider) getSession(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	wait, _ := time.ParseDuration(r.URL.Query().Get("wait"))
	if wait > 30*time.Second {
		wait = 30 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		p.mu.Lock()
		session, changed := p.session, p.changed
		p.mu.Unlock()
		if session.Version > since || wait == 0 || time.Now().After(deadline) {
			writeJSON(w, http.StatusOK, session)
			return
		}
		select {
		case <-changed:
		case <-time.After(time.Until(deadline)):
		case <-r.Context().Done():
			return
		}
	}
}

func (p *Provider) putDetections(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Runtimes []domain.OnboardingDetection `json:"runtimes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, &providerError{http.StatusBadRequest, "invalid_json", err.Error()})
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.transition(domain.OnboardingDetected); err != nil {
		writeError(w, err)
		return
	}
	p.session.Detections = body.Runtimes
	p.bump()
	writeJSON(w, http.StatusOK, p.session)
}

func (p *Provider) putTools(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tools map[string]domain.OnboardingReportedTools `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, &providerError{http.StatusBadRequest, "invalid_json", err.Error()})
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.transition(domain.OnboardingToolsDiscovered); err != nil {
		writeError(w, err)
		return
	}
	tools := map[string][]domain.OnboardingTool{}
	errs := map[string][]domain.OnboardingDiscoveryError{}
	for runtime, reported := range body.Tools {
		catalog, _ := agentruntime.Lookup(agentruntime.Key(runtime))
		for _, tool := range reported.Builtin {
			builtin, _ := catalog.Builtin(tool.Name)
			suggested, reason := suggestBuiltin(builtin.Kind)
			tools[runtime] = append(tools[runtime], domain.OnboardingTool{
				Name: tool.Name, Source: "builtin", Description: tool.Description, Kind: string(builtin.Kind),
				Suggested: suggested, Reason: reason,
			})
		}
		for _, server := range sortedKeys(reported.MCP) {
			for _, tool := range reported.MCP[server] {
				suggested, reason := suggestMCP(tool)
				tools[runtime] = append(tools[runtime], domain.OnboardingTool{
					Name: tool.Name, Source: "mcp", Server: server, Description: tool.Description,
					Annotations: tool.Annotations, Suggested: suggested, Reason: reason,
				})
			}
		}
		if len(reported.Errors) > 0 {
			errs[runtime] = reported.Errors
		}
	}
	p.session.Tools = tools
	p.session.DiscoveryErrors = errs
	p.bump()
	writeJSON(w, http.StatusOK, p.session)
}

func suggestBuiltin(kind agentruntime.ToolKind) (bool, string) {
	switch kind {
	case agentruntime.ToolShell:
		return true, "shell"
	case agentruntime.ToolEdit:
		return false, "edit_not_suggested"
	case agentruntime.ToolRead:
		return false, "read_only_builtin"
	default:
		return false, "unknown"
	}
}

func suggestMCP(tool domain.OnboardingReportedTool) (bool, string) {
	if a := tool.Annotations; a != nil {
		switch {
		case a.ReadOnlyHint != nil && *a.ReadOnlyHint:
			return false, "annotation_read_only"
		case a.DestructiveHint != nil && *a.DestructiveHint:
			return true, "annotation_destructive"
		case a.ReadOnlyHint != nil && !*a.ReadOnlyHint:
			return true, "annotation_mutating"
		}
	}
	for _, verb := range []string{"delete", "create", "deploy", "update", "send", "remove"} {
		if strings.Contains(strings.ToLower(tool.Name), verb) {
			return true, "verb_" + verb
		}
	}
	return false, "unknown"
}

func (p *Provider) enroll(w http.ResponseWriter, _ *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.enrolled {
		writeError(w, &providerError{http.StatusConflict, "already_enrolled", "The session already minted its credentials."})
		return
	}
	if err := p.transition(domain.OnboardingEnrolled); err != nil {
		writeError(w, err)
		return
	}
	p.enrolled = true
	var enrollments []map[string]any
	hooks := map[string]any{}
	p.session.Enrollments = nil
	for _, runtime := range p.session.SelectedRuntimes {
		key := agentruntime.Key(runtime)
		token := "whc_" + runtime + "_secret"
		p.credentials[token] = runtime
		expires := time.Now().Add(90 * 24 * time.Hour).UTC().Truncate(time.Second)
		enrollments = append(enrollments, map[string]any{
			"runtime":        runtime,
			"agent":          map[string]any{"slug": runtime, "name": runtime},
			"agent_instance": map[string]any{"id": "instance-" + runtime, "name": p.session.Machine.Hostname},
			"credential":     map[string]any{"id": "credential-" + runtime, "token": token, "expires_at": expires},
		})
		p.session.Enrollments = append(p.session.Enrollments, domain.OnboardingEnrollment{
			Runtime: runtime, AgentSlug: domain.AgentSlug(runtime), AgentName: runtime,
			AgentInstanceID: domain.ID("instance-" + runtime), AgentInstanceName: p.session.Machine.Hostname,
			CredentialID: domain.ID("credential-" + runtime),
		})
		var builtin []string
		var refs []agentruntime.MCPRef
		for _, tool := range p.session.SuggestedTools(runtime) {
			if tool.Source == "mcp" {
				refs = append(refs, agentruntime.MCPRef{Server: tool.Server, Name: tool.Name})
			} else {
				builtin = append(builtin, tool.Name)
			}
		}
		names := append([]string(nil), builtin...)
		for _, ref := range refs {
			names = append(names, agentruntime.RuntimeToolName(key, ref))
		}
		sort.Strings(names)
		hooks[runtime] = map[string]any{"matcher": agentruntime.HookMatcher(key), "tools": names}
	}
	p.bump()
	writeJSON(w, http.StatusOK, map[string]any{
		"provider_url": p.Server.URL,
		"enrollments":  enrollments,
		"hooks":        hooks,
		"session":      p.session,
	})
}

func (p *Provider) putInstallResults(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Results []domain.OnboardingInstallResult `json:"results"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, &providerError{http.StatusBadRequest, "invalid_json", err.Error()})
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.transition(domain.OnboardingInstalled); err != nil {
		writeError(w, err)
		return
	}
	p.session.InstallResults = body.Results
	p.session.TestCommands = map[string]domain.OnboardingTestCommand{}
	for _, runtime := range p.session.SelectedRuntimes {
		suggested := p.session.SuggestedTools(runtime)
		if len(suggested) == 0 {
			continue
		}
		tool := suggested[0]
		chosen := agentruntime.SelectedTool{Name: tool.Name, Source: agentruntime.Source(tool.Source), Server: tool.Server, Kind: agentruntime.ToolKind(tool.Kind)}
		if command, ok := agentruntime.TestCommand(agentruntime.Key(runtime), chosen); ok {
			p.session.TestCommands[runtime] = domain.OnboardingTestCommand{Tool: chosen.Name, Command: command.Shell, Note: command.Note}
		}
	}
	p.bump()
	writeJSON(w, http.StatusOK, p.session)
}

func (p *Provider) putError(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, &providerError{http.StatusBadRequest, "invalid_json", err.Error()})
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.session.LastError = &domain.OnboardingError{Source: "cli", Code: body.Code, Message: body.Message}
	p.bump()
	writeJSON(w, http.StatusOK, p.session)
}

// SelectAgents is the person choosing runtimes on the page.
func (h *Human) SelectAgents(runtimes ...string) error {
	p := h.p
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, runtime := range runtimes {
		det, ok := p.session.Detection(runtime)
		if !ok || !det.Installed {
			return &providerError{http.StatusBadRequest, "runtime_not_detected", runtime + " was not detected"}
		}
	}
	if err := p.transition(domain.OnboardingAgentsSelected); err != nil {
		return err
	}
	p.session.SelectedRuntimes = runtimes
	p.bump()
	return nil
}

// Provision is the person arriving at the rules step with their
// channels set: it writes the generated objects (the starting rules gate
// the suggested tools) and moves to provisioning so the CLI enrolls.
func (h *Human) Provision() error {
	p := h.p
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.transition(domain.OnboardingProvisioning); err != nil {
		return err
	}
	now := time.Now().UTC()
	p.session.ChannelsSetAt = &now
	pipelines := map[string]domain.OnboardingGeneratedPipeline{}
	for _, runtime := range p.session.SelectedRuntimes {
		pipelines[runtime] = domain.OnboardingGeneratedPipeline{Revision: 2, ReplacedActiveRevision: 1}
	}
	p.session.Generated = domain.OnboardingGenerated{
		EscalationPathKey: "notify-test-owner", EscalationPathName: "Notify Test Owner", EscalationPathRevision: 1,
		Pipelines: pipelines,
	}
	p.bump()
	return nil
}

// Finish closes the session as completed, passing through the person's
// browser-only steps on the way (leaving the rules step, deciding the
// test request), which the CLI never sees.
func (h *Human) Finish() error {
	p := h.p
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now().UTC()
	if p.session.Status == domain.OnboardingInstalled {
		p.session.RulesSetAt = &now
		if err := p.transition(domain.OnboardingTested); err != nil {
			return err
		}
		p.session.TestedAt = &now
	}
	if err := p.transition(domain.OnboardingCompleted); err != nil {
		return err
	}
	p.session.CompletedAt = &now
	p.bump()
	return nil
}

// Skip closes the session as skipped.
func (h *Human) Skip() error {
	p := h.p
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.transition(domain.OnboardingSkipped); err != nil {
		return err
	}
	now := time.Now().UTC()
	p.session.CompletedAt = &now
	p.bump()
	return nil
}

func (p *Provider) aapAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		p.mu.Lock()
		_, ok := p.credentials[token]
		p.mu.Unlock()
		if !ok {
			writeError(w, &providerError{http.StatusUnauthorized, "unauthenticated", "unknown agent credential"})
			return
		}
		next(w, r)
	}
}

// instanceSelf is the authentication probe `withhuman agent install`
// uses: the identity the instance credential resolves to.
func (p *Provider) instanceSelf(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	p.mu.Lock()
	runtime := p.credentials[token]
	p.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"agent_slug": runtime, "agent_instance_id": "instance-" + runtime})
}

func (p *Provider) aapCreate(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, &providerError{http.StatusBadRequest, "invalid_json", err.Error()})
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(r.Header.Get("Idempotency-Key"))).String()
	if existing, ok := p.requests[id]; ok {
		writeJSON(w, http.StatusOK, existing)
		return
	}
	now := time.Now().UTC()
	window, _ := time.ParseDuration(body["timeout"].(string))
	request := map[string]any{
		"id": id, "tool": body["tool"], "arguments": body["arguments"], "context": body["context"], "timeout": body["timeout"],
		"status": p.Decision, "created_at": now, "deadline_at": now.Add(window),
	}
	if reasoning, ok := body["agent_reasoning"]; ok {
		request["agent_reasoning"] = reasoning
	}
	if p.Decision != "pending" {
		decision := map[string]any{"status": p.Decision, "decided_at": now, "note": "fixture decision"}
		if p.Decision == "approved" {
			decision["expires_at"] = now.Add(5 * time.Minute)
		}
		request["decision"] = decision
	}
	p.requests[id] = request
	writeJSON(w, http.StatusCreated, request)
}

func (p *Provider) aapGet(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	request, ok := p.requests[r.PathValue("id")]
	p.mu.Unlock()
	if !ok {
		writeError(w, &providerError{http.StatusNotFound, "not_found", "no such request"})
		return
	}
	writeJSON(w, http.StatusOK, request)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
