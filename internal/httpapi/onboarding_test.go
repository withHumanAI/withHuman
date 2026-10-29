package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/withHumanAI/withHuman/agentruntime"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/authn"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/entitlements"
	"github.com/withHumanAI/withHuman/internal/onboarding"
)

// The onboarding routes have two trust boundaries. These tests pin the
// separation: the whb_ bearer opens only /api/onboarding/v1, the session
// cookie opens only /api/v1, and neither is accepted by the other's
// middleware or by the AAP surface.

const onboardingCookieToken = "session-token"

type onboardingIdentityStub struct {
	application.IdentityRepository
	principal domain.Principal
	// revoked makes the session cookie stop resolving, as an expired or
	// signed-out session does.
	revoked atomic.Bool
}

func (s *onboardingIdentityStub) ResolveSession(_ context.Context, input application.ResolveSessionInput) (domain.Principal, error) {
	if string(input.SecretHash) == string(authn.HashToken(onboardingCookieToken)) && !s.revoked.Load() {
		return s.principal, nil
	}
	return domain.Principal{}, domain.ErrUnauthenticated
}

func (s *onboardingIdentityStub) GetOrganization(_ context.Context, input application.GetOrganizationInput) (domain.Organization, error) {
	return domain.Organization{ID: input.OrganizationID, Name: "Acme", Slug: "acme", Status: domain.OrganizationActive}, nil
}

func (s *onboardingIdentityStub) GetUser(context.Context, application.GetUserInput) (domain.User, error) {
	return domain.User{DisplayName: "Owner", PrimaryEmail: "owner@example.com"}, nil
}

func (s *onboardingIdentityStub) GetLocalCredential(context.Context, application.GetLocalCredentialInput) (application.LocalCredentialRecord, error) {
	return application.LocalCredentialRecord{UserID: s.principal.ActorID, PasswordHash: "$argon2id$stub"}, nil
}

func (s *onboardingIdentityStub) GetMembership(context.Context, application.GetMembershipInput) (domain.Membership, error) {
	return domain.Membership{ID: s.principal.MembershipID, Status: domain.MembershipActive}, nil
}

// onboardingSessionsStub is the smallest OnboardingRepository the route
// tests need: one session, status-checked writes, code and secret lookup.
type onboardingSessionsStub struct {
	mu         sync.Mutex
	session    *domain.OnboardingSession
	codeHash   []byte
	secretHash []byte
	completed  []domain.OnboardingOutcome
}

func (s *onboardingSessionsStub) CreateOnboardingSession(_ context.Context, input application.CreateOnboardingSessionInput) (domain.OnboardingSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := input.Session
	session.Version = 1
	s.session, s.codeHash, s.secretHash = &session, input.SetupCodeHash, nil
	return session, nil
}

func (s *onboardingSessionsStub) GetOnboardingSession(_ context.Context, input application.GetOnboardingSessionInput) (domain.OnboardingSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil || s.session.ID != input.SessionID {
		return domain.OnboardingSession{}, domain.ErrNotFound
	}
	return *s.session, nil
}

func (s *onboardingSessionsStub) GetOpenOnboardingSession(context.Context, application.GetOpenOnboardingSessionInput) (domain.OnboardingSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil || s.session.Status.Terminal() {
		return domain.OnboardingSession{}, domain.ErrNotFound
	}
	return *s.session, nil
}

func (s *onboardingSessionsStub) ExchangeOnboardingSetupCode(_ context.Context, input application.ExchangeOnboardingSetupCodeInput) (domain.OnboardingSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil || s.codeHash == nil || string(s.codeHash) != string(input.SetupCodeHash) {
		return domain.OnboardingSession{}, domain.ErrUnauthenticated
	}
	s.codeHash, s.secretHash = nil, input.CredentialSecretHash
	s.session.Status, s.session.Machine, s.session.Version = domain.OnboardingConnected, input.Machine, s.session.Version+1
	return *s.session, nil
}

func (s *onboardingSessionsStub) ResolveOnboardingCredential(_ context.Context, input application.ResolveOnboardingCredentialInput) (domain.OnboardingSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil || s.session.ID != input.SessionID || s.secretHash == nil || string(s.secretHash) != string(input.SecretHash) {
		return domain.OnboardingSession{}, domain.ErrUnauthenticated
	}
	return *s.session, nil
}

func (s *onboardingSessionsStub) UpdateOnboardingSession(_ context.Context, input application.UpdateOnboardingSessionInput) (domain.OnboardingSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil || s.session.ID != input.SessionID {
		return domain.OnboardingSession{}, domain.ErrNotFound
	}
	if len(input.ExpectedStatuses) > 0 {
		allowed := false
		for _, status := range input.ExpectedStatuses {
			allowed = allowed || status == s.session.Status
		}
		if !allowed {
			return domain.OnboardingSession{}, fmt.Errorf("session is %s: %w", s.session.Status, domain.ErrOnboardingState)
		}
	}
	if input.Patch.Detections != nil {
		s.session.Detections = *input.Patch.Detections
	}
	if input.Patch.LastError != nil {
		s.session.LastError = input.Patch.LastError
	}
	if input.NextStatus != "" {
		s.session.Status = input.NextStatus
	}
	s.session.Version++
	return *s.session, nil
}

func (s *onboardingSessionsStub) WaitOnboardingSession(ctx context.Context, input application.WaitOnboardingSessionInput) (domain.OnboardingSession, error) {
	return s.GetOnboardingSession(ctx, application.GetOnboardingSessionInput{OrganizationID: input.OrganizationID, SessionID: input.SessionID})
}

func (s *onboardingSessionsStub) CompleteOnboarding(_ context.Context, input application.CompleteOnboardingInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed = append(s.completed, input.Outcome)
	if s.session != nil {
		s.session.Status = domain.OnboardingSkipped
		now := input.Now
		s.session.CompletedAt = &now
	}
	return nil
}

func (s *onboardingSessionsStub) GetSetupProgress(context.Context, application.GetSetupProgressInput) (domain.SetupChecklist, error) {
	return domain.SetupChecklist{}, nil
}

type onboardingClock struct{}

func (onboardingClock) Now() time.Time { return time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC) }

func newOnboardingRouter(t *testing.T) (*gin.Engine, *onboardingSessionsStub) {
	t.Helper()
	return newDrainingOnboardingRouter(t, nil)
}

func newDrainingOnboardingRouter(t *testing.T, drain *Drain) (*gin.Engine, *onboardingSessionsStub) {
	t.Helper()
	return newOnboardingRouterFor(t, drain, newOnboardingIdentities())
}

func newOnboardingIdentities() *onboardingIdentityStub {
	return &onboardingIdentityStub{principal: domain.Principal{
		ActorType: domain.ActorHuman, ActorID: domain.ID(uuid.NewString()), OrganizationID: domain.ID(uuid.NewString()),
		MembershipID: domain.ID(uuid.NewString()),
	}.WithGrants(domain.GrantsForRoles([]domain.Role{domain.RoleOwner}))}
}

func newOnboardingRouterFor(t *testing.T, drain *Drain, identities *onboardingIdentityStub) (*gin.Engine, *onboardingSessionsStub) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessions := &onboardingSessionsStub{}
	auth := authn.NewService(identities, nil, nil, nil, entitlements.NewUnlimited(), nil, onboardingClock{}, authn.ServiceConfig{SessionTTL: time.Hour})
	auth.RegisterOnboarding(sessions)
	service := onboarding.NewService(sessions, identities, nil, nil, nil, nil, nil, nil, onboardingClock{}, logger, onboarding.Config{
		PublicOrigin: "http://localhost:8080", CLIDownloadURL: "https://downloads.withhuman.ai",
	})
	router := NewRouter(Dependencies{
		Auth: auth, Identities: identities, Onboarding: service, Drain: drain, Logger: logger,
		Config: Config{PublicOrigin: "http://localhost:8080", CLIDownloadURL: "https://downloads.withhuman.ai"},
	})
	return router, sessions
}

func onboardingRequest(method, target string, body any, cookie bool, bearer string) *http.Request {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = strings.NewReader(string(raw))
	}
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://localhost:8080")
	if cookie {
		request.AddCookie(&http.Cookie{Name: authn.SessionCookieName, Value: onboardingCookieToken})
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	return request
}

func serve(router http.Handler, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func errorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope from %q: %v", response.Body.String(), err)
	}
	return envelope.Error.Code
}

func TestOnboardingRoutesSeparateTheTwoCredentialClasses(t *testing.T) {
	router, sessions := newOnboardingRouter(t)

	// The human surface needs the cookie.
	if response := serve(router, onboardingRequest(http.MethodPost, "/api/v1/onboarding/sessions", map[string]any{}, false, "")); response.Code != http.StatusUnauthorized {
		t.Fatalf("begin without cookie: %d %s", response.Code, response.Body.String())
	}
	if response := serve(router, onboardingRequest(http.MethodGet, "/api/v1/onboarding/session", nil, true, "")); response.Code != http.StatusNotFound || errorCode(t, response) != "onboarding_session_not_found" {
		t.Fatalf("no open session: %d %s", response.Code, response.Body.String())
	}
	response := serve(router, onboardingRequest(http.MethodPost, "/api/v1/onboarding/sessions", map[string]any{}, true, ""))
	if response.Code != http.StatusCreated {
		t.Fatalf("begin: %d %s", response.Code, response.Body.String())
	}
	var begun struct {
		SetupCode    string                   `json:"setup_code"`
		SetupCommand string                   `json:"setup_command"`
		Session      domain.OnboardingSession `json:"session"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &begun); err != nil {
		t.Fatal(err)
	}
	if begun.SetupCode == "" || !strings.HasPrefix(begun.SetupCommand, "curl -fsSL https://downloads.withhuman.ai/install.sh | sh -s -- onboard --url http://localhost:8080 --code ") || !strings.HasSuffix(begun.SetupCommand, begun.SetupCode) {
		t.Fatalf("begin body: %+v", begun)
	}
	if strings.Contains(response.Body.String(), "setup_code_hash") {
		t.Fatal("hashes must never leave the server")
	}

	// A wrong code is 404 setup_code_invalid; the right one yields a whb_ bearer.
	if response := serve(router, onboardingRequest(http.MethodPost, "/api/onboarding/v1/sessions/exchange", map[string]any{"setup_code": "NOPE-NOPE-NOPE"}, false, "")); response.Code != http.StatusNotFound || errorCode(t, response) != "setup_code_invalid" {
		t.Fatalf("bad code: %d %s", response.Code, response.Body.String())
	}
	response = serve(router, onboardingRequest(http.MethodPost, "/api/onboarding/v1/sessions/exchange", map[string]any{
		"setup_code": begun.SetupCode, "machine": map[string]any{"hostname": "laptop", "os": "darwin", "arch": "arm64", "cli_version": "0.1.0"},
	}, false, ""))
	if response.Code != http.StatusOK {
		t.Fatalf("exchange: %d %s", response.Code, response.Body.String())
	}
	var exchanged struct {
		Token       string                   `json:"token"`
		ProviderURL string                   `json:"provider_url"`
		Session     domain.OnboardingSession `json:"session"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &exchanged); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(exchanged.Token, "whb_") || exchanged.ProviderURL != "http://localhost:8080" || exchanged.Session.Status != domain.OnboardingConnected {
		t.Fatalf("exchange body: %+v", exchanged)
	}

	// The whb_ bearer opens the machine surface and nothing else.
	if response := serve(router, onboardingRequest(http.MethodGet, "/api/onboarding/v1/session", nil, false, exchanged.Token)); response.Code != http.StatusOK {
		t.Fatalf("whb_ on machine surface: %d %s", response.Code, response.Body.String())
	}
	if response := serve(router, onboardingRequest(http.MethodGet, "/api/onboarding/v1/session", nil, true, "")); response.Code != http.StatusUnauthorized {
		t.Fatalf("cookie on machine surface: %d", response.Code)
	}
	if response := serve(router, onboardingRequest(http.MethodGet, "/api/onboarding/v1/session", nil, false, "whc_"+uuid.NewString()+"_secret")); response.Code != http.StatusUnauthorized {
		t.Fatalf("agent credential on machine surface: %d", response.Code)
	}
	if response := serve(router, onboardingRequest(http.MethodGet, "/api/v1/agent_instances/self", nil, false, exchanged.Token)); response.Code != http.StatusUnauthorized {
		t.Fatalf("whb_ on the instance surface: %d", response.Code)
	}
	if response := serve(router, onboardingRequest(http.MethodGet, "/api/v1/onboarding/session", nil, false, exchanged.Token)); response.Code != http.StatusUnauthorized {
		t.Fatalf("whb_ on the human surface: %d", response.Code)
	}

	// Long-poll returns at once when the version already moved on.
	if response := serve(router, onboardingRequest(http.MethodGet, "/api/onboarding/v1/session?wait=25s&since=1", nil, false, exchanged.Token)); response.Code != http.StatusOK {
		t.Fatalf("wait: %d %s", response.Code, response.Body.String())
	}

	// A status the table does not allow is a conflict; a valid report advances.
	sessions.session.Status = domain.OnboardingProvisioning
	full := detectionsBody()
	if response := serve(router, onboardingRequest(http.MethodPut, "/api/onboarding/v1/session/detections", full, false, exchanged.Token)); response.Code != http.StatusConflict || errorCode(t, response) != "onboarding_session_state" {
		t.Fatalf("detections in wrong status: %d %s", response.Code, response.Body.String())
	}
	sessions.session.Status = domain.OnboardingConnected
	if response := serve(router, onboardingRequest(http.MethodPut, "/api/onboarding/v1/session/detections", map[string]any{"runtimes": []any{}}, false, exchanged.Token)); response.Code != http.StatusBadRequest {
		t.Fatalf("partial detections: %d %s", response.Code, response.Body.String())
	}
	response = serve(router, onboardingRequest(http.MethodPut, "/api/onboarding/v1/session/detections", full, false, exchanged.Token))
	if response.Code != http.StatusOK {
		t.Fatalf("detections: %d %s", response.Code, response.Body.String())
	}
	var detected domain.OnboardingSession
	if err := json.Unmarshal(response.Body.Bytes(), &detected); err != nil || detected.Status != domain.OnboardingDetected || len(detected.Detections) != len(agentruntime.Keys()) {
		t.Fatalf("detected session: %v %+v", err, detected.Status)
	}
	if response := serve(router, onboardingRequest(http.MethodPut, "/api/onboarding/v1/session/error", map[string]any{"code": "discovery_failed", "message": "spawn failed"}, false, exchanged.Token)); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"code":"discovery_failed"`) {
		t.Fatalf("report error: %d %s", response.Code, response.Body.String())
	}

	// The person's own steps are stamps, refused only outside the flow.
	if response := serve(router, onboardingRequest(http.MethodPost, "/api/v1/onboarding/session/rules", nil, true, "")); response.Code != http.StatusConflict || errorCode(t, response) != "onboarding_session_state" {
		t.Fatalf("rules before install: %d %s", response.Code, response.Body.String())
	}

	// Skipping is a human action and closes the session.
	if response := serve(router, onboardingRequest(http.MethodPost, "/api/v1/onboarding/completion", map[string]any{"outcome": "skipped"}, true, "")); response.Code != http.StatusNoContent {
		t.Fatalf("skip: %d %s", response.Code, response.Body.String())
	}
	if len(sessions.completed) != 1 || sessions.completed[0] != domain.OnboardingSkip {
		t.Fatalf("completion not recorded: %v", sessions.completed)
	}
	if response := serve(router, onboardingRequest(http.MethodPost, "/api/v1/onboarding/completion", map[string]any{"outcome": "later"}, true, "")); response.Code != http.StatusBadRequest {
		t.Fatalf("bad outcome: %d", response.Code)
	}
}

func detectionsBody() map[string]any {
	runtimes := []any{}
	for _, key := range agentruntime.Keys() {
		runtimes = append(runtimes, map[string]any{"runtime": string(key), "installed": key == agentruntime.ClaudeCode, "mcp_servers": []any{}})
	}
	return map[string]any{"runtimes": runtimes}
}

func TestOnboardingSessionEventsStreamsTheSession(t *testing.T) {
	router, _ := newOnboardingRouter(t)
	serve(router, onboardingRequest(http.MethodPost, "/api/v1/onboarding/sessions", map[string]any{}, true, ""))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	request := onboardingRequest(http.MethodGet, "/api/v1/onboarding/session/events", nil, true, "").WithContext(ctx)
	response := serve(router, request)
	if got := response.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type %q", got)
	}
	body := response.Body.String()
	if !strings.HasPrefix(body, "event: session\ndata: {") || !strings.Contains(body, `"status":"awaiting_cli"`) {
		t.Fatalf("stream body: %q", body)
	}
}

// A stream would otherwise hold the server's shutdown open until its
// timeout; ending it lets the browser reconnect to another replica.
func TestOnboardingSessionEventsEndWhenTheDrainEndsStreams(t *testing.T) {
	drain := NewDrain()
	router, _ := newDrainingOnboardingRouter(t, drain)
	serve(router, onboardingRequest(http.MethodPost, "/api/v1/onboarding/sessions", map[string]any{}, true, ""))
	drain.EndStreams()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request := onboardingRequest(http.MethodGet, "/api/v1/onboarding/session/events", nil, true, "").WithContext(ctx)
	started := time.Now()
	response := serve(router, request)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("the stream outlived the drain: %s", elapsed)
	}
	if body := response.Body.String(); !strings.HasPrefix(body, "event: session\ndata: {") {
		t.Fatalf("stream body: %q", body)
	}
}

// A stream checks the session on every tick, so one that expires or is
// signed out elsewhere ends the stream and the browser's reconnect meets
// the 401 that sends it to sign-in.
func TestOnboardingSessionEventsEndWhenTheSessionEnds(t *testing.T) {
	identities := newOnboardingIdentities()
	router, _ := newOnboardingRouterFor(t, nil, identities)
	serve(router, onboardingRequest(http.MethodPost, "/api/v1/onboarding/sessions", map[string]any{}, true, ""))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request := onboardingRequest(http.MethodGet, "/api/v1/onboarding/session/events", nil, true, "").WithContext(ctx)
	revoke := time.AfterFunc(100*time.Millisecond, func() { identities.revoked.Store(true) })
	defer revoke.Stop()
	started := time.Now()
	response := serve(router, request)
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("the stream outlived the session: %s", elapsed)
	}
	if body := response.Body.String(); !strings.HasPrefix(body, "event: session\ndata: {") {
		t.Fatalf("stream body: %q", body)
	}
	if response := serve(router, request.Clone(context.Background())); response.Code != http.StatusUnauthorized || errorCode(t, response) != "unauthenticated" {
		t.Fatalf("reconnect: %d %s", response.Code, response.Body.String())
	}
}

func TestMeReportsOnboardingRequirement(t *testing.T) {
	router, _ := newOnboardingRouter(t)
	response := serve(router, onboardingRequest(http.MethodGet, "/api/v1/me", nil, true, ""))
	if response.Code != http.StatusOK {
		t.Fatalf("me: %d %s", response.Code, response.Body.String())
	}
	var me struct {
		User struct {
			HasPassword bool `json:"has_password"`
		} `json:"user"`
		Onboarding struct {
			Required bool `json:"required"`
		} `json:"onboarding"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &me); err != nil || !me.Onboarding.Required {
		t.Fatalf("owner of a fresh organization should be sent to onboarding: %v %s", err, response.Body.String())
	}
	if !me.User.HasPassword {
		t.Fatalf("an owner with a local credential should report has_password: %s", response.Body.String())
	}
	config := serve(router, httptest.NewRequest(http.MethodGet, "/api/runtime-config", nil))
	if !strings.Contains(config.Body.String(), `"cli_download_url":"https://downloads.withhuman.ai"`) {
		t.Fatalf("runtime config: %s", config.Body.String())
	}
}

func TestOnboardingRoutesAbsentWithoutService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := NewRouter(Dependencies{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if response := serve(router, httptest.NewRequest(http.MethodPost, "/api/onboarding/v1/sessions/exchange", strings.NewReader(`{"setup_code":"x"}`))); response.Code != http.StatusNotFound {
		t.Fatalf("machine surface without the service: %d", response.Code)
	}
}
