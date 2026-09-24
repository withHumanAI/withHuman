package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/authn"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/entitlements"
)

// The /api/v1 surface has two front doors. These tests pin how they
// combine: a whk_ bearer is a personal API key and skips the same-origin
// guard, any other bearer is refused outright, no bearer means the
// cookie, and browser-only routes refuse a key.

const apiKeyCookieToken = "session-token"

var apiKeyTestKeyID = uuid.NewString()

type apiKeyIdentityStub struct {
	application.IdentityRepository
	principal domain.Principal
}

func (s *apiKeyIdentityStub) ResolveSession(_ context.Context, input application.ResolveSessionInput) (domain.Principal, error) {
	if string(input.SecretHash) == string(authn.HashToken(apiKeyCookieToken)) {
		return s.principal, nil
	}
	return domain.Principal{}, domain.ErrUnauthenticated
}

func (s *apiKeyIdentityStub) GetOrganization(_ context.Context, input application.GetOrganizationInput) (domain.Organization, error) {
	return domain.Organization{ID: input.OrganizationID, Name: "Acme", Slug: "acme", Status: domain.OrganizationActive}, nil
}

func (s *apiKeyIdentityStub) GetUser(context.Context, application.GetUserInput) (domain.User, error) {
	return domain.User{DisplayName: "Owner", PrimaryEmail: "owner@example.com"}, nil
}

// The owner signs in through a provider: no local credential exists.
func (s *apiKeyIdentityStub) GetLocalCredential(context.Context, application.GetLocalCredentialInput) (application.LocalCredentialRecord, error) {
	return application.LocalCredentialRecord{}, domain.ErrNotFound
}

func (s *apiKeyIdentityStub) GetMembership(context.Context, application.GetMembershipInput) (domain.Membership, error) {
	return domain.Membership{ID: s.principal.MembershipID, Status: domain.MembershipActive}, nil
}

func (s *apiKeyIdentityStub) GetOrganizationPolicy(context.Context, application.GetOrganizationPolicyInput) (domain.OrganizationPolicy, error) {
	return domain.OrganizationPolicy{PersonalAPIKeysAllowed: true, MFAMode: domain.MFAOptional, ApprovalReauthWindow: time.Hour}, nil
}

// apiKeyStoreStub resolves one key, "secret", to the session principal
// narrowed to request.read, with the auth context of a key.
type apiKeyStoreStub struct {
	principal domain.Principal
}

func (s *apiKeyStoreStub) CreateAPIKey(context.Context, application.CreateAPIKeyInput) (domain.APIKey, error) {
	return domain.APIKey{}, domain.ErrNotFound
}
func (s *apiKeyStoreStub) ListAPIKeys(context.Context, application.ListAPIKeysInput) ([]domain.APIKey, error) {
	return []domain.APIKey{}, nil
}
func (s *apiKeyStoreStub) GetAPIKey(_ context.Context, input application.GetAPIKeyInput) (domain.APIKey, error) {
	return domain.APIKey{ID: input.KeyID, OrganizationID: input.OrganizationID, Kind: domain.APIKeyPersonal, Name: "script", CreatedAt: time.Now()}, nil
}
func (s *apiKeyStoreStub) RevokeAPIKey(context.Context, application.RevokeAPIKeyInput) error {
	return nil
}
func (s *apiKeyStoreStub) ResolveAPIKey(_ context.Context, input application.ResolveAPIKeyInput) (domain.Principal, error) {
	if string(input.KeyID) != apiKeyTestKeyID || string(input.SecretHash) != string(authn.HashToken("secret")) {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	principal := s.principal
	principal.Auth = domain.AuthContext{Method: domain.AuthMethodAPIKey, Assurance: "strong", AuthenticatedAt: input.Now, CredentialID: input.KeyID}
	if principal.ActorType == domain.ActorAPIKey {
		return principal, nil
	}
	return principal.WithGrants(domain.NarrowGrants(s.principal.Grants, []domain.Permission{domain.PermissionRequestRead})), nil
}

type apiKeyClock struct{}

func (apiKeyClock) Now() time.Time { return time.Now() }

func newAPIKeyRouter(t *testing.T, organization ...bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	principal := domain.Principal{
		ActorType: domain.ActorHuman, ActorID: domain.ID(uuid.NewString()), OrganizationID: domain.ID(uuid.NewString()),
		MembershipID: domain.ID(uuid.NewString()),
		Auth:         domain.AuthContext{Method: "password", Assurance: "strong", AuthenticatedAt: time.Now(), SessionID: "session-1"},
	}.WithGrants(domain.GrantsForRoles([]domain.Role{domain.RoleOwner}))
	identities := &apiKeyIdentityStub{principal: principal}
	auth := authn.NewService(identities, nil, nil, nil, entitlements.NewUnlimited(), nil, apiKeyClock{}, authn.ServiceConfig{SessionTTL: time.Hour})
	keyPrincipal := principal
	if len(organization) > 0 && organization[0] {
		keyPrincipal.ActorType = domain.ActorAPIKey
		keyPrincipal.ActorID = domain.ID(apiKeyTestKeyID)
		keyPrincipal.MembershipID = ""
	}
	auth.RegisterAPIKeys(&apiKeyStoreStub{principal: keyPrincipal})
	return NewRouter(Dependencies{
		Auth: auth, Identities: identities, Logger: logger,
		Config: Config{PublicOrigin: "http://localhost:8080"},
	})
}

func apiKeyRequest(method, target string, body any, cookie bool, bearer, origin string) *http.Request {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = strings.NewReader(string(raw))
	}
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Content-Type", "application/json")
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if cookie {
		request.AddCookie(&http.Cookie{Name: authn.SessionCookieName, Value: apiKeyCookieToken})
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	return request
}

func apiKeyErrorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", response.Body.String(), err)
	}
	return body.Error.Code
}

func TestAPIKeyBearerOpensTheManagementSurface(t *testing.T) {
	router := newAPIKeyRouter(t)
	token := "whk_" + apiKeyTestKeyID + "_secret"

	// A key resolves to the person, narrowed, and /me shows the method.
	response := httptest.NewRecorder()
	router.ServeHTTP(response, apiKeyRequest(http.MethodGet, "/api/v1/me", nil, false, token, ""))
	if response.Code != http.StatusOK {
		t.Fatalf("GET /me with key = %d: %s", response.Code, response.Body.String())
	}
	var me struct {
		Principal domain.Principal `json:"principal"`
		User      struct {
			HasPassword bool `json:"has_password"`
		} `json:"user"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me.User.HasPassword {
		t.Fatalf("an account without a local credential should not report has_password: %s", response.Body.String())
	}
	if me.Principal.Auth.Method != domain.AuthMethodAPIKey || me.Principal.Auth.CredentialID != domain.ID(apiKeyTestKeyID) {
		t.Fatalf("principal auth = %+v", me.Principal.Auth)
	}
	if len(me.Principal.Permissions) != 1 || me.Principal.Permissions[0] != domain.PermissionRequestRead {
		t.Fatalf("narrowed permissions = %v", me.Principal.Permissions)
	}

	// verify answers the key row and what it can do right now.
	response = httptest.NewRecorder()
	router.ServeHTTP(response, apiKeyRequest(http.MethodGet, "/api/v1/api_keys/verify", nil, false, token, ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"effective_permissions":["request.read"]`) {
		t.Fatalf("verify = %d: %s", response.Code, response.Body.String())
	}

	// A session cannot verify a key it does not have.
	response = httptest.NewRecorder()
	router.ServeHTTP(response, apiKeyRequest(http.MethodGet, "/api/v1/api_keys/verify", nil, true, "", ""))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("verify with a session = %d, want 400", response.Code)
	}

	// The router's coarse gate sees the narrowed grants: membership.read
	// is not on the key even though the owner holds it.
	response = httptest.NewRecorder()
	router.ServeHTTP(response, apiKeyRequest(http.MethodGet, "/api/v1/members", nil, false, token, ""))
	if response.Code != http.StatusForbidden || apiKeyErrorCode(t, response) != "forbidden" {
		t.Fatalf("GET /members with a narrowed key = %d %s", response.Code, response.Body.String())
	}
}

func TestAPIKeyBearerRefusals(t *testing.T) {
	router := newAPIKeyRouter(t)
	for name, bearer := range map[string]string{
		"wrong secret":     "whk_" + apiKeyTestKeyID + "_nope",
		"agent credential": "whc_" + uuid.NewString() + "_secret",
		"onboarding token": "who_" + uuid.NewString() + "_secret",
		"garbage":          "whk_",
	} {
		response := httptest.NewRecorder()
		// A valid cookie rides along to prove a bad bearer never falls
		// through to the session.
		router.ServeHTTP(response, apiKeyRequest(http.MethodGet, "/api/v1/me", nil, true, bearer, ""))
		if response.Code != http.StatusUnauthorized {
			t.Errorf("%s: = %d, want 401", name, response.Code)
		}
	}
	// Bearer-less requests still need the cookie.
	response := httptest.NewRecorder()
	router.ServeHTTP(response, apiKeyRequest(http.MethodGet, "/api/v1/me", nil, false, "", ""))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("no credential = %d, want 401", response.Code)
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, apiKeyRequest(http.MethodGet, "/api/v1/me", nil, true, "", ""))
	if response.Code != http.StatusOK {
		t.Fatalf("cookie alone = %d, want 200", response.Code)
	}
	// A key never opens the agent surface.
	response = httptest.NewRecorder()
	router.ServeHTTP(response, apiKeyRequest(http.MethodGet, "/api/v1/agent_instances/self", nil, false, "whk_"+apiKeyTestKeyID+"_secret", ""))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("key on the instance surface = %d, want 401", response.Code)
	}
}

// The same-origin guard applies to cookies only: a key is never ambient.
// Browser-only routes refuse a key with their own code.
func TestAPIKeySkipsSameOriginAndIsRefusedOnBrowserOnlyRoutes(t *testing.T) {
	router := newAPIKeyRouter(t)
	token := "whk_" + apiKeyTestKeyID + "_secret"

	// A cross-site POST with the cookie is refused before any handler.
	response := httptest.NewRecorder()
	router.ServeHTTP(response, apiKeyRequest(http.MethodPost, "/api/v1/me/api_keys", map[string]any{"name": "x"}, true, "", "https://evil.example"))
	if response.Code != http.StatusForbidden || apiKeyErrorCode(t, response) != "forbidden" {
		t.Fatalf("cross-site cookie POST = %d %s", response.Code, response.Body.String())
	}
	// The same POST under a key passes the guard and is refused by the
	// browser-only rule instead, proving the guard let it through.
	response = httptest.NewRecorder()
	router.ServeHTTP(response, apiKeyRequest(http.MethodPost, "/api/v1/me/api_keys", map[string]any{"name": "x"}, false, token, "https://evil.example"))
	if response.Code != http.StatusForbidden || apiKeyErrorCode(t, response) != "api_key_not_allowed" {
		t.Fatalf("cross-site key POST to a browser-only route = %d %s", response.Code, response.Body.String())
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/me/api_keys"},
		{http.MethodDelete, "/api/v1/me/api_keys/" + uuid.NewString()},
		{http.MethodGet, "/api/v1/api_keys"},
		{http.MethodPost, "/api/v1/me/password"},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, apiKeyRequest(route.method, route.path, map[string]any{}, false, token, ""))
		if response.Code != http.StatusForbidden || apiKeyErrorCode(t, response) != "api_key_not_allowed" {
			t.Errorf("%s %s with a key = %d %s, want 403 api_key_not_allowed", route.method, route.path, response.Code, response.Body.String())
		}
	}
	// The same routes work with the cookie and a matching origin.
	response = httptest.NewRecorder()
	router.ServeHTTP(response, apiKeyRequest(http.MethodGet, "/api/v1/me/api_keys", nil, true, "", ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"scoping_enabled":false`) {
		t.Fatalf("GET /me/api-keys with cookie = %d %s", response.Code, response.Body.String())
	}
}

func (r *apiKeyStoreStub) ListAPIKeyScopes(context.Context, application.ListAPIKeyScopesInput) ([]domain.PermissionResource, error) {
	return []domain.PermissionResource{}, nil
}

func (s *apiKeyStoreStub) RecordAPIKeyUse(context.Context, application.RecordAPIKeyUseInput) error {
	return nil
}

func TestOrganizationAPIKeyHTTPBoundary(t *testing.T) {
	router := newAPIKeyRouter(t, true)
	token := "who_" + apiKeyTestKeyID + "_secret"
	// Include a cookie and a foreign origin: the explicit bearer wins and does
	// not inherit the cookie's authority or require its same-origin guard.
	response := httptest.NewRecorder()
	router.ServeHTTP(response, apiKeyRequest(http.MethodGet, "/api/v1/api_keys/verify", nil, true, token, "https://automation.example"))
	if response.Code != http.StatusOK {
		t.Fatalf("verify: %d %s", response.Code, response.Body.String())
	}
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/v1/me"}, {"GET", "/api/v1/events"}, {"GET", "/api/v1/api_keys"},
		{"POST", "/api/v1/me/api_keys"},
		{"GET", "/api/v1/api_keys/" + apiKeyTestKeyID},
		{"DELETE", "/api/v1/api_keys/" + apiKeyTestKeyID},
		{"PATCH", "/api/v1/organization/policy"},
	} {
		t.Run(route.method+route.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, apiKeyRequest(route.method, route.path, nil, true, token, "http://localhost:8080"))
			if response.Code != http.StatusForbidden || apiKeyErrorCode(t, response) != "api_key_not_allowed" {
				t.Fatalf("response: %d %s", response.Code, response.Body.String())
			}
		})
	}
	for _, route := range []struct {
		method, path, permission string
		allowed                  bool
	}{
		{"GET", "/api/v1/members", "membership.read", true},
		{"GET", "/api/v1/requests/:id/review", "request.read", true},
		{"POST", "/api/v1/new-agent-operation", "agent.write", false},
		{"POST", "/api/v1/onboarding/sessions", "agent.write", false},
		{"GET", "/api/v1/me", "self", false},
	} {
		if got := OrganizationKeyAllowed(route.method, route.path, route.permission); got != route.allowed {
			t.Errorf("%s %s admitted=%v", route.method, route.path, got)
		}
	}
}

// The hosted issuance routes are absent here, so "options" and any other
// non-uuid path segment under /api_keys is absence, not a storage error.
func TestAPIKeyRoutesTreatNonKeyIDsAsAbsent(t *testing.T) {
	router := newAPIKeyRouter(t)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/api_keys/options"},
		{http.MethodGet, "/api/v1/api_keys/not-a-key"},
		{http.MethodDelete, "/api/v1/api_keys/not-a-key"},
		{http.MethodDelete, "/api/v1/me/api_keys/not-a-key"},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, apiKeyRequest(route.method, route.path, nil, true, "", "http://localhost:8080"))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s %s = %d %s, want 404", route.method, route.path, response.Code, response.Body.String())
		}
	}
}
