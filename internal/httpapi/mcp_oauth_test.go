package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/authn"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/entitlements"
	"github.com/withHumanAI/withHuman/internal/approvalpipeline"
	"github.com/withHumanAI/withHuman/internal/mcpoauth"
)

// The OAuth door onto the MCP server, walked by the SDK's own client:
// the 401 challenge, discovery, dynamic registration, the consent page's
// API under a browser session, the code exchange, and the tool call with
// the key it produced. Only the store is a stub.

type oauthMemoryRepository struct {
	mu      sync.Mutex
	clients map[domain.ID]domain.OAuthClient
	codes   map[string]*domain.OAuthAuthorizationCode
}

func (r *oauthMemoryRepository) CreateOAuthClient(_ context.Context, input application.CreateOAuthClientInput) (domain.OAuthClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	client := domain.OAuthClient{ID: input.ClientID, Name: input.Name, RedirectURIs: input.RedirectURIs, ClientURI: input.ClientURI, CreatedAt: input.Now}
	r.clients[input.ClientID] = client
	return client, nil
}

// SaveOAuthClientMetadataDocument is never reached: the harness gives the
// OAuth service no document fetcher, so URL client IDs are unknown here.
func (r *oauthMemoryRepository) SaveOAuthClientMetadataDocument(context.Context, application.SaveOAuthClientMetadataDocumentInput) (domain.OAuthClient, error) {
	return domain.OAuthClient{}, domain.ErrNotFound
}

func (r *oauthMemoryRepository) GetOAuthClient(_ context.Context, input application.GetOAuthClientInput) (domain.OAuthClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	client, ok := r.clients[input.ClientID]
	if !ok {
		return domain.OAuthClient{}, domain.ErrNotFound
	}
	return client, nil
}

func (r *oauthMemoryRepository) CreateOAuthAuthorizationCode(_ context.Context, input application.CreateOAuthAuthorizationCodeInput) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.codes[string(input.CodeHash)] = &domain.OAuthAuthorizationCode{
		ID: input.CodeID, ClientID: input.ClientID, OrganizationID: input.OrganizationID, APIKeyID: input.APIKeyID,
		SealedToken: input.SealedToken, RedirectURI: input.RedirectURI, CodeChallenge: input.CodeChallenge, Scope: input.Scope,
		CreatedAt: input.Now, ExpiresAt: input.ExpiresAt,
	}
	return nil
}

func (r *oauthMemoryRepository) ConsumeOAuthAuthorizationCode(_ context.Context, input application.ConsumeOAuthAuthorizationCodeInput) (domain.OAuthAuthorizationCode, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	code, ok := r.codes[string(input.CodeHash)]
	if !ok || code.ConsumedAt != nil || !code.ExpiresAt.After(input.Now) {
		return domain.OAuthAuthorizationCode{}, domain.ErrNotFound
	}
	now := input.Now
	code.ConsumedAt = &now
	return *code, nil
}

// oauthKeyMinter mints the one key the stub key store resolves, and
// records what consent asked for.
type oauthKeyMinter struct {
	mu       sync.Mutex
	requests []authn.CreateAPIKeyRequest
}

func (m *oauthKeyMinter) CreateAPIKey(_ context.Context, principal domain.Principal, request authn.CreateAPIKeyRequest) (domain.APIKey, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, request)
	expires := time.Now().Add(time.Hour)
	return domain.APIKey{ID: domain.ID(apiKeyTestKeyID), OrganizationID: principal.OrganizationID, Kind: domain.APIKeyPersonal, Name: request.Name, Permissions: request.Permissions, OAuthClientID: request.OAuthClientID, ExpiresAt: &expires},
		"whk_" + apiKeyTestKeyID + "_secret", nil
}

func (m *oauthKeyMinter) APIKeyBearable(domain.Principal) []domain.PermissionDefinition {
	return []domain.PermissionDefinition{{Key: domain.PermissionPipelineRead}, {Key: domain.PermissionPipelineWrite}, {Key: domain.PermissionPipelineActivate}}
}
func (m *oauthKeyMinter) APIKeyScopingEnabled(context.Context, domain.ID) (bool, error) {
	return true, nil
}
func (m *oauthKeyMinter) PersonalAPIKeysAllowed(context.Context, domain.ID) (bool, error) {
	return true, nil
}
func (m *oauthKeyMinter) GetAPIKey(_ context.Context, input application.GetAPIKeyInput) (domain.APIKey, error) {
	expires := time.Now().Add(time.Hour)
	return domain.APIKey{ID: input.KeyID, OrganizationID: input.OrganizationID, ExpiresAt: &expires}, nil
}

type oauthHarness struct {
	server *httptest.Server
	minter *oauthKeyMinter
	store  *mcpPipelineStore
}

// newOAuthHarness is the MCP harness with the OAuth service composed on
// the test server's own origin, so discovery, the resource indicator and
// the issuer all agree with the URL the client dials.
func newOAuthHarness(t *testing.T) *oauthHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	organization := domain.ID(uuid.NewString())
	principal := domain.Principal{
		ActorType: domain.ActorHuman, ActorID: domain.ID(uuid.NewString()), OrganizationID: organization,
		MembershipID: domain.ID(uuid.NewString()),
		Auth:         domain.AuthContext{Method: "password", Assurance: "strong", AuthenticatedAt: time.Now(), SessionID: "session-1"},
	}.WithGrants(domain.GrantsForRoles([]domain.Role{domain.RoleOwner}))
	identities := &apiKeyIdentityStub{principal: principal}
	authService := authn.NewService(identities, nil, nil, nil, entitlements.NewUnlimited(), nil, apiKeyClock{}, authn.ServiceConfig{SessionTTL: time.Hour})
	authService.RegisterAPIKeys(&mcpAPIKeyStore{principal: principal})
	store := newMCPPipelineStore(organization)
	minter := &oauthKeyMinter{}
	var engine http.Handler
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { engine.ServeHTTP(w, r) }))
	t.Cleanup(server.Close)
	repository := &oauthMemoryRepository{clients: map[domain.ID]domain.OAuthClient{}, codes: map[string]*domain.OAuthAuthorizationCode{}}
	oauth := mcpoauth.NewService(repository, minter, minter, apiKeyClock{}, mcpoauth.Config{PublicOrigin: server.URL, MCPPath: MCPPath, AuthorizePath: MCPAuthorizePath})
	engine = NewRouter(Dependencies{
		Auth: authService, Identities: identities, Logger: logger, MCPOAuth: oauth,
		Pipelines: approvalpipeline.NewManagementService(store, approvalpipeline.NewCompiledPipelineState(store, approvalpipeline.NewBlockRegistry()), pipelineHTTPClock{}),
		Config:    Config{PublicOrigin: server.URL, Version: "test"},
	})
	return &oauthHarness{server: server, minter: minter, store: store}
}

// consent plays the browser: it takes the authorization URL the client
// built, previews it under the session cookie, approves with the
// requested permissions, and hands the code back.
func (h *oauthHarness) consent(t *testing.T, approve bool) auth.AuthorizationCodeFetcher {
	return func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		t.Helper()
		target, err := url.Parse(args.URL)
		if err != nil {
			return nil, err
		}
		if target.Path != MCPAuthorizePath {
			t.Fatalf("authorization URL path = %s, want the consent page", target.Path)
		}
		query := target.Query()
		request := map[string]any{
			"client_id": query.Get("client_id"), "redirect_uri": query.Get("redirect_uri"), "state": query.Get("state"),
			"code_challenge": query.Get("code_challenge"), "code_challenge_method": query.Get("code_challenge_method"),
			"scope": query.Get("scope"), "resource": query.Get("resource"), "response_type": query.Get("response_type"),
		}
		var preview struct {
			Client struct {
				Name string `json:"name"`
			} `json:"client"`
			Requested []string `json:"requested_permissions"`
		}
		h.post(t, "/api/v1/oauth_authorizations/preview", request, http.StatusOK, &preview)
		if preview.Client.Name != "Test agent" {
			t.Fatalf("preview client = %+v", preview.Client)
		}
		decision := map[string]any{"approve": approve, "permissions": preview.Requested}
		for key, value := range request {
			decision[key] = value
		}
		var answer struct {
			RedirectURL string `json:"redirect_url"`
		}
		h.post(t, "/api/v1/oauth_authorizations", decision, http.StatusOK, &answer)
		redirect, err := url.Parse(answer.RedirectURL)
		if err != nil {
			return nil, err
		}
		if redirect.Query().Get("error") != "" {
			return nil, &url.Error{Op: "authorize", URL: answer.RedirectURL, Err: io.ErrUnexpectedEOF}
		}
		return &auth.AuthorizationResult{Code: redirect.Query().Get("code"), State: redirect.Query().Get("state"), Iss: redirect.Query().Get("iss")}, nil
	}
}

func (h *oauthHarness) post(t *testing.T, path string, body any, want int, into any) {
	t.Helper()
	encoded, _ := json.Marshal(body)
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, h.server.URL+path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: authn.SessionCookieName, Value: apiKeyCookieToken})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != want {
		t.Fatalf("POST %s = %d: %s", path, response.StatusCode, raw)
	}
	if into != nil {
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
	}
}

func TestMCPOAuthFlowWithTheSDKClient(t *testing.T) {
	h := newOAuthHarness(t)
	handler, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{
			ClientName: "Test agent", RedirectURIs: []string{"http://127.0.0.1:41999/callback"}, TokenEndpointAuthMethod: "none",
		}},
		RedirectURL:              "http://127.0.0.1:41999/callback",
		AuthorizationCodeFetcher: h.consent(t, true),
		Client:                   http.DefaultClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: h.server.URL + MCPPath, OAuthHandler: handler}, nil)
	if err != nil {
		t.Fatalf("connect through OAuth: %v", err)
	}
	defer session.Close()
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil || len(listed.Tools) == 0 {
		t.Fatalf("tools/list with the OAuth key: %v", err)
	}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_pipeline", Arguments: map[string]any{"pipeline_key": "entry"}})
	if err != nil || result.IsError {
		t.Fatalf("get_pipeline with the OAuth key: %v %s", err, mcpText(result))
	}
	h.minter.mu.Lock()
	defer h.minter.mu.Unlock()
	if len(h.minter.requests) != 1 {
		t.Fatalf("consent minted %d keys, want 1", len(h.minter.requests))
	}
	minted := h.minter.requests[0]
	if minted.Name != "Test agent" || minted.OAuthClientID == "" {
		t.Fatalf("minted key = %+v, want named after the client and marked with it", minted)
	}
}

func TestMCPChallengeAndDiscoveryDocuments(t *testing.T) {
	h := newOAuthHarness(t)
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, h.server.URL+MCPPath, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	challenge := response.Header.Get("WWW-Authenticate")
	if response.StatusCode != http.StatusUnauthorized || !strings.Contains(challenge, `resource_metadata="`+h.server.URL+`/.well-known/oauth-protected-resource/api/mcp/v1"`) {
		t.Fatalf("status %d challenge %q", response.StatusCode, challenge)
	}
	// An authenticated answer carries no challenge.
	request, _ = http.NewRequestWithContext(t.Context(), http.MethodGet, h.server.URL+MCPPath, nil)
	request.Header.Set("Authorization", "Bearer whk_"+apiKeyTestKeyID+"_secret")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed || response.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("authenticated GET = %d, challenge %q", response.StatusCode, response.Header.Get("WWW-Authenticate"))
	}
	for _, path := range []string{"/.well-known/oauth-protected-resource/api/mcp/v1", "/.well-known/oauth-protected-resource"} {
		var document struct {
			Resource string   `json:"resource"`
			Servers  []string `json:"authorization_servers"`
		}
		h.get(t, path, &document)
		if document.Resource != h.server.URL+MCPPath || len(document.Servers) != 1 || document.Servers[0] != h.server.URL {
			t.Fatalf("%s = %+v", path, document)
		}
	}
	var metadata struct {
		Issuer     string   `json:"issuer"`
		Authorize  string   `json:"authorization_endpoint"`
		Token      string   `json:"token_endpoint"`
		Register   string   `json:"registration_endpoint"`
		Challenges []string `json:"code_challenge_methods_supported"`
	}
	h.get(t, "/.well-known/oauth-authorization-server", &metadata)
	if metadata.Issuer != h.server.URL || metadata.Authorize != h.server.URL+MCPAuthorizePath || metadata.Token != h.server.URL+"/auth/oauth/token" || metadata.Register != h.server.URL+"/auth/oauth/register" || metadata.Challenges[0] != "S256" {
		t.Fatalf("authorization server metadata = %+v", metadata)
	}
}

func (h *oauthHarness) get(t *testing.T, path string, into any) {
	t.Helper()
	response, err := http.Get(h.server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, response.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
}

// The consent routes are the browser's: a key, even one with every
// permission, cannot approve a client on a person's behalf, and the
// token endpoint answers in OAuth's shape.
func TestMCPOAuthConsentIsBrowserOnlyAndTokenErrorsAreOAuthShaped(t *testing.T) {
	h := newOAuthHarness(t)
	body, _ := json.Marshal(map[string]any{"client_id": uuid.NewString(), "redirect_uri": "https://example.com/cb", "code_challenge": strings.Repeat("c", 43), "code_challenge_method": "S256"})
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, h.server.URL+"/api/v1/oauth_authorizations/preview", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer whk_"+apiKeyTestKeyID+"_secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden || apiKeyErrorCode(t, recorded(response)) != "api_key_not_allowed" {
		t.Fatalf("preview with a key = %d", response.StatusCode)
	}
	token, err := http.PostForm(h.server.URL+"/auth/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {"nope"}, "client_id": {uuid.NewString()}, "code_verifier": {strings.Repeat("v", 43)}})
	if err != nil {
		t.Fatal(err)
	}
	defer token.Body.Close()
	var refusal struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(token.Body).Decode(&refusal); err != nil || token.StatusCode != http.StatusBadRequest || refusal.Error != "invalid_grant" {
		t.Fatalf("token refusal = %d %+v %v", token.StatusCode, refusal, err)
	}
}

// recorded adapts a live response to the error-code reader the API key
// tests share.
func recorded(response *http.Response) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	recorder.Code = response.StatusCode
	raw, _ := io.ReadAll(response.Body)
	recorder.Body.Write(raw)
	return recorder
}
