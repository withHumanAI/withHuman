package mcpoauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/authn"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/webhookdelivery"
)

// memoryRepository is the OAuth repository over maps: enough to prove the
// choreography without a database, including single-use consumption.
type memoryRepository struct {
	mu      sync.Mutex
	clients map[domain.ID]domain.OAuthClient
	codes   map[string]*domain.OAuthAuthorizationCode
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{clients: map[domain.ID]domain.OAuthClient{}, codes: map[string]*domain.OAuthAuthorizationCode{}}
}

func (r *memoryRepository) CreateOAuthClient(_ context.Context, input application.CreateOAuthClientInput) (domain.OAuthClient, error) {
	if err := input.Validate(); err != nil {
		return domain.OAuthClient{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	client := domain.OAuthClient{ID: input.ClientID, Name: input.Name, RedirectURIs: input.RedirectURIs, ClientURI: input.ClientURI, CreatedAt: input.Now}
	r.clients[input.ClientID] = client
	return client, nil
}

func (r *memoryRepository) SaveOAuthClientMetadataDocument(_ context.Context, input application.SaveOAuthClientMetadataDocumentInput) (domain.OAuthClient, error) {
	if err := input.Validate(); err != nil {
		return domain.OAuthClient{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	client := domain.OAuthClient{ID: input.ClientID, CreatedAt: input.Now}
	for _, existing := range r.clients {
		if existing.MetadataURL == input.MetadataURL {
			client = existing
		}
	}
	client.Name, client.RedirectURIs, client.ClientURI, client.MetadataURL = input.Name, input.RedirectURIs, input.ClientURI, input.MetadataURL
	r.clients[client.ID] = client
	return client, nil
}

func (r *memoryRepository) GetOAuthClient(_ context.Context, input application.GetOAuthClientInput) (domain.OAuthClient, error) {
	if err := input.Validate(); err != nil {
		return domain.OAuthClient{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if input.MetadataURL != "" {
		for _, client := range r.clients {
			if client.MetadataURL == input.MetadataURL {
				return client, nil
			}
		}
		return domain.OAuthClient{}, domain.ErrNotFound
	}
	client, ok := r.clients[input.ClientID]
	if !ok {
		return domain.OAuthClient{}, domain.ErrNotFound
	}
	return client, nil
}

// fakeDocuments serves client metadata documents by URL and counts the
// fetches, standing in for the egress-checked outbound client.
type fakeDocuments struct {
	mu        sync.Mutex
	documents map[string]string
	status    int
	fetches   int
}

func (f *fakeDocuments) Fetch(_ context.Context, fetch webhookdelivery.Fetch) (webhookdelivery.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetches++
	document, ok := f.documents[fetch.URL]
	if !ok {
		return webhookdelivery.Response{}, errors.New("connection refused")
	}
	status := f.status
	if status == 0 {
		status = http.StatusOK
	}
	return webhookdelivery.Response{StatusCode: status, Body: []byte(document)}, nil
}

func (r *memoryRepository) CreateOAuthAuthorizationCode(_ context.Context, input application.CreateOAuthAuthorizationCodeInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.codes[string(input.CodeHash)] = &domain.OAuthAuthorizationCode{
		ID: input.CodeID, ClientID: input.ClientID, OrganizationID: input.OrganizationID, APIKeyID: input.APIKeyID,
		SealedToken: input.SealedToken, RedirectURI: input.RedirectURI, CodeChallenge: input.CodeChallenge, Scope: input.Scope,
		CreatedAt: input.Now, ExpiresAt: input.ExpiresAt,
	}
	return nil
}

func (r *memoryRepository) ConsumeOAuthAuthorizationCode(_ context.Context, input application.ConsumeOAuthAuthorizationCodeInput) (domain.OAuthAuthorizationCode, error) {
	if err := input.Validate(); err != nil {
		return domain.OAuthAuthorizationCode{}, err
	}
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

// fakeMinter mints a predictable key and remembers what it was asked
// for, standing in for authn.Service.
type fakeMinter struct {
	scoping  bool
	allowed  bool
	bearable []domain.PermissionDefinition
	requests []authn.CreateAPIKeyRequest
	keys     map[domain.ID]domain.APIKey
	expiry   time.Duration
}

func (m *fakeMinter) CreateAPIKey(_ context.Context, principal domain.Principal, request authn.CreateAPIKeyRequest) (domain.APIKey, string, error) {
	m.requests = append(m.requests, request)
	id := domain.ID(uuid.NewString())
	key := domain.APIKey{ID: id, OrganizationID: principal.OrganizationID, Kind: domain.APIKeyPersonal, Name: request.Name, Permissions: request.Permissions, OAuthClientID: request.OAuthClientID}
	if m.expiry > 0 {
		expires := time.Now().Add(m.expiry)
		key.ExpiresAt = &expires
	}
	if m.keys == nil {
		m.keys = map[domain.ID]domain.APIKey{}
	}
	m.keys[id] = key
	return key, "whk_" + string(id) + "_secret", nil
}

func (m *fakeMinter) APIKeyBearable(domain.Principal) []domain.PermissionDefinition {
	return m.bearable
}
func (m *fakeMinter) APIKeyScopingEnabled(context.Context, domain.ID) (bool, error) {
	return m.scoping, nil
}
func (m *fakeMinter) PersonalAPIKeysAllowed(context.Context, domain.ID) (bool, error) {
	return m.allowed, nil
}
func (m *fakeMinter) GetAPIKey(_ context.Context, input application.GetAPIKeyInput) (domain.APIKey, error) {
	key, ok := m.keys[input.KeyID]
	if !ok {
		return domain.APIKey{}, domain.ErrNotFound
	}
	return key, nil
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func fixture(t *testing.T, scoping bool) (*Service, *memoryRepository, *fakeMinter) {
	t.Helper()
	minter := &fakeMinter{scoping: scoping, allowed: true, expiry: time.Hour, bearable: []domain.PermissionDefinition{
		{Key: domain.PermissionPipelineRead}, {Key: domain.PermissionPipelineWrite}, {Key: domain.PermissionEscalationPathRead},
	}}
	repository := newMemoryRepository()
	service := NewService(repository, minter, minter, fixedClock{now: time.Now()}, Config{PublicOrigin: "https://app.example.com/", MCPPath: "/api/mcp/v1", AuthorizePath: "/oauth/authorize"})
	return service, repository, minter
}

// The documents Claude and Claude Code publish, as fetched on 2026-10-29.
const (
	claudeMetadataURL      = "https://claude.ai/oauth/mcp-oauth-client-metadata"
	claudeCodeMetadataURL  = "https://claude.ai/oauth/claude-code-client-metadata"
	claudeMetadataDocument = `{"client_id":"https://claude.ai/oauth/mcp-oauth-client-metadata","client_name":"Claude","client_uri":"https://claude.ai",` +
		`"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"grant_types":["authorization_code","refresh_token","urn:ietf:params:oauth:grant-type:jwt-bearer"],` +
		`"response_types":["code"],"token_endpoint_auth_method":"none"}`
	claudeCodeMetadataDocument = `{"client_id":"https://claude.ai/oauth/claude-code-client-metadata","client_name":"Claude Code","client_uri":"https://claude.ai",` +
		`"redirect_uris":["http://localhost/callback","http://127.0.0.1/callback"],"grant_types":["authorization_code","refresh_token"],` +
		`"response_types":["code"],"token_endpoint_auth_method":"none"}`
)

func metadataFixture(t *testing.T, documents map[string]string) (*Service, *fakeMinter, *fakeDocuments) {
	t.Helper()
	minter := &fakeMinter{scoping: true, allowed: true, expiry: time.Hour, bearable: []domain.PermissionDefinition{{Key: domain.PermissionPipelineRead}}}
	fetcher := &fakeDocuments{documents: documents}
	service := NewService(newMemoryRepository(), minter, minter, fixedClock{now: time.Now()}, Config{
		PublicOrigin: "https://app.example.com", MCPPath: "/api/mcp/v1", AuthorizePath: "/oauth/authorize", ClientMetadata: fetcher,
	})
	return service, minter, fetcher
}

func register(t *testing.T, service *Service, uris ...string) RegisteredClient {
	t.Helper()
	client, err := service.RegisterClient(context.Background(), ClientRegistration{ClientName: "Claude Code", RedirectURIs: uris, TokenEndpointAuthMethod: "none"})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func pkce(t *testing.T) (verifier, challenge string) {
	t.Helper()
	verifier = strings.Repeat("v", 43)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func principal() domain.Principal {
	return domain.Principal{ActorType: domain.ActorHuman, ActorID: "user", OrganizationID: domain.ID(uuid.NewString()), MembershipID: "member"}
}

func oauthCode(t *testing.T, err error) *Error {
	t.Helper()
	var refusal *Error
	if !errors.As(err, &refusal) {
		t.Fatalf("not an OAuth refusal: %v", err)
	}
	return refusal
}

func TestMetadataDescribesThisDeployment(t *testing.T) {
	service, _, _ := fixture(t, true)
	meta := service.AuthorizationServerMetadata()
	if meta.Issuer != "https://app.example.com" || meta.AuthorizationEndpoint != "https://app.example.com/oauth/authorize" ||
		meta.TokenEndpoint != "https://app.example.com/auth/oauth/token" || meta.RegistrationEndpoint != "https://app.example.com/auth/oauth/register" {
		t.Fatalf("metadata = %+v", meta)
	}
	if meta.CodeChallengeMethodsSupported[0] != "S256" || meta.TokenEndpointAuthMethodsSupported[0] != "none" || !meta.IssParameterSupported {
		t.Fatalf("metadata = %+v", meta)
	}
	resource := service.ProtectedResourceMetadata()
	if resource.Resource != "https://app.example.com/api/mcp/v1" || resource.AuthorizationServers[0] != "https://app.example.com" {
		t.Fatalf("resource metadata = %+v", resource)
	}
	if service.ResourceMetadataURL() != "https://app.example.com/.well-known/oauth-protected-resource/api/mcp/v1" {
		t.Fatal(service.ResourceMetadataURL())
	}
	if len(meta.ScopesSupported) == 0 || meta.ScopesSupported[0] > meta.ScopesSupported[len(meta.ScopesSupported)-1] {
		t.Fatalf("scopes = %v", meta.ScopesSupported)
	}
}

func TestRegistrationAdmitsNativeAppRedirectsOnly(t *testing.T) {
	service, _, _ := fixture(t, true)
	for _, uri := range []string{"http://127.0.0.1:5555/callback", "http://localhost/cb", "https://example.com/oauth/cb", "cursor://anysphere.cursor-retrieval/oauth/callback"} {
		if _, err := service.RegisterClient(context.Background(), ClientRegistration{RedirectURIs: []string{uri}}); err != nil {
			t.Errorf("%s refused: %v", uri, err)
		}
	}
	for _, uri := range []string{"http://example.com/cb", "javascript:alert(1)", "https://example.com/cb#frag", "not a url"} {
		_, err := service.RegisterClient(context.Background(), ClientRegistration{RedirectURIs: []string{uri}})
		if refusal := oauthCode(t, err); refusal.Code != "invalid_redirect_uri" {
			t.Errorf("%s: %v", uri, err)
		}
	}
	_, err := service.RegisterClient(context.Background(), ClientRegistration{RedirectURIs: []string{"https://example.com/cb"}, TokenEndpointAuthMethod: "client_secret_basic"})
	if refusal := oauthCode(t, err); refusal.Code != "invalid_client_metadata" {
		t.Fatalf("confidential client admitted: %v", err)
	}
	client := register(t, service, "https://example.com/cb")
	if client.ClientName != "Claude Code" || client.TokenEndpointAuthMethod != "none" || client.ClientIDIssuedAt == 0 {
		t.Fatalf("registered = %+v", client)
	}
}

func TestConsentMintsAKeyTheCodeRedeemsOnce(t *testing.T) {
	service, _, minter := fixture(t, true)
	client := register(t, service, "http://127.0.0.1:4000/callback")
	verifier, challenge := pkce(t)
	person := principal()
	request := AuthorizationRequest{
		ClientID: client.ClientID, RedirectURI: "http://127.0.0.1:52123/callback", State: "xyz", CodeChallenge: challenge, CodeChallengeMethod: "S256",
		Scope: "pipeline.read pipeline.write request.decide nonsense", Resource: "https://app.example.com/api/mcp/v1", ResponseType: "code",
	}
	consent, err := service.Preview(context.Background(), person, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(consent.Requested) != 2 || consent.Requested[0] != domain.PermissionPipelineRead || consent.Requested[1] != domain.PermissionPipelineWrite {
		t.Fatalf("requested = %v, want the bearable subset of the scope", consent.Requested)
	}
	redirect, err := service.Authorize(context.Background(), person, Decision{AuthorizationRequest: request, Approve: true, Permissions: consent.Requested})
	if err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	if target.Host != "127.0.0.1:52123" || target.Query().Get("state") != "xyz" || target.Query().Get("iss") != "https://app.example.com" || target.Query().Get("code") == "" {
		t.Fatalf("redirect = %s", redirect)
	}
	if len(minter.requests) != 1 || minter.requests[0].Name != "Claude Code" || minter.requests[0].OAuthClientID != domain.ID(client.ClientID) || len(minter.requests[0].Permissions) != 2 {
		t.Fatalf("mint request = %+v", minter.requests)
	}
	code := target.Query().Get("code")
	token, err := service.Exchange(context.Background(), TokenRequest{GrantType: "authorization_code", Code: code, ClientID: client.ClientID, RedirectURI: request.RedirectURI, CodeVerifier: verifier})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token.AccessToken, "whk_") || token.TokenType != "Bearer" || token.Scope != "pipeline.read pipeline.write" || token.ExpiresIn <= 0 || token.ExpiresIn > 3600 {
		t.Fatalf("token = %+v", token)
	}
	// Single use.
	_, err = service.Exchange(context.Background(), TokenRequest{GrantType: "authorization_code", Code: code, ClientID: client.ClientID, CodeVerifier: verifier})
	if refusal := oauthCode(t, err); refusal.Code != "invalid_grant" {
		t.Fatalf("replayed code: %v", err)
	}
}

func TestExchangeRefusalsBurnTheCode(t *testing.T) {
	service, _, _ := fixture(t, true)
	client := register(t, service, "https://example.com/cb")
	verifier, challenge := pkce(t)
	request := AuthorizationRequest{ClientID: client.ClientID, RedirectURI: "https://example.com/cb", CodeChallenge: challenge, CodeChallengeMethod: "S256"}
	issue := func() string {
		redirect, err := service.Authorize(context.Background(), principal(), Decision{AuthorizationRequest: request, Approve: true})
		if err != nil {
			t.Fatal(err)
		}
		target, _ := url.Parse(redirect)
		return target.Query().Get("code")
	}
	// A wrong verifier refuses and consumes the code, so the right one
	// cannot follow a guess.
	code := issue()
	_, err := service.Exchange(context.Background(), TokenRequest{GrantType: "authorization_code", Code: code, ClientID: client.ClientID, CodeVerifier: strings.Repeat("w", 43)})
	if refusal := oauthCode(t, err); refusal.Code != "invalid_grant" {
		t.Fatalf("wrong verifier: %v", err)
	}
	_, err = service.Exchange(context.Background(), TokenRequest{GrantType: "authorization_code", Code: code, ClientID: client.ClientID, CodeVerifier: verifier})
	if refusal := oauthCode(t, err); refusal.Code != "invalid_grant" {
		t.Fatalf("code survived a wrong verifier: %v", err)
	}
	// Another client's id, or a different redirect, is refused the same way.
	other := register(t, service, "https://other.example.com/cb")
	code = issue()
	_, err = service.Exchange(context.Background(), TokenRequest{GrantType: "authorization_code", Code: code, ClientID: other.ClientID, CodeVerifier: verifier})
	if refusal := oauthCode(t, err); refusal.Code != "invalid_grant" {
		t.Fatalf("other client: %v", err)
	}
	code = issue()
	_, err = service.Exchange(context.Background(), TokenRequest{GrantType: "authorization_code", Code: code, ClientID: client.ClientID, RedirectURI: "https://example.com/elsewhere", CodeVerifier: verifier})
	if refusal := oauthCode(t, err); refusal.Code != "invalid_grant" {
		t.Fatalf("other redirect: %v", err)
	}
	for _, grant := range []string{"refresh_token", "client_credentials"} {
		_, err = service.Exchange(context.Background(), TokenRequest{GrantType: grant, Code: "x", ClientID: client.ClientID, CodeVerifier: verifier})
		if refusal := oauthCode(t, err); refusal.Code != "unsupported_grant_type" {
			t.Fatalf("%s: %v", grant, err)
		}
	}
}

func TestAuthorizationRequestIsCheckedBeforeAnythingShows(t *testing.T) {
	service, _, minter := fixture(t, true)
	client := register(t, service, "https://example.com/cb")
	_, challenge := pkce(t)
	valid := AuthorizationRequest{ClientID: client.ClientID, RedirectURI: "https://example.com/cb", CodeChallenge: challenge, CodeChallengeMethod: "S256"}
	for name, mutate := range map[string]func(*AuthorizationRequest){
		"unknown client":      func(r *AuthorizationRequest) { r.ClientID = uuid.NewString() },
		"unregistered uri":    func(r *AuthorizationRequest) { r.RedirectURI = "https://example.com/other" },
		"plain pkce":          func(r *AuthorizationRequest) { r.CodeChallengeMethod = "plain" },
		"short challenge":     func(r *AuthorizationRequest) { r.CodeChallenge = "abc" },
		"foreign resource":    func(r *AuthorizationRequest) { r.Resource = "https://elsewhere.example.com/mcp" },
		"token response type": func(r *AuthorizationRequest) { r.ResponseType = "token" },
	} {
		request := valid
		mutate(&request)
		if _, err := service.Preview(context.Background(), principal(), request); err == nil {
			t.Errorf("%s admitted", name)
		} else if _, err := service.Authorize(context.Background(), principal(), Decision{AuthorizationRequest: request, Approve: true}); err == nil {
			t.Errorf("%s authorized", name)
		}
	}
	if len(minter.requests) != 0 {
		t.Fatal("a refused request must mint nothing")
	}
	// Declining mints nothing and answers the client's redirect.
	redirect, err := service.Authorize(context.Background(), principal(), Decision{AuthorizationRequest: valid, Approve: false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(redirect, "https://example.com/cb?") || !strings.Contains(redirect, "error=access_denied") {
		t.Fatalf("decline redirect = %s", redirect)
	}
	if len(minter.requests) != 0 {
		t.Fatal("declining must mint nothing")
	}
	unknown, _ := service.GetClient(context.Background(), "nope")
	if unknown.ID != "" {
		t.Fatal("unknown client resolved")
	}
	if refusal := oauthCode(t, func() error { _, err := service.GetClient(context.Background(), uuid.NewString()); return err }()); refusal.Status != http.StatusNotFound {
		t.Fatalf("unknown client status = %d", refusal.Status)
	}
}

func TestWithoutScopingTheKeyInheritsEverything(t *testing.T) {
	service, _, minter := fixture(t, false)
	client := register(t, service, "https://example.com/cb")
	verifier, challenge := pkce(t)
	request := AuthorizationRequest{ClientID: client.ClientID, RedirectURI: "https://example.com/cb", CodeChallenge: challenge, CodeChallengeMethod: "S256"}
	redirect, err := service.Authorize(context.Background(), principal(), Decision{AuthorizationRequest: request, Approve: true, Permissions: []domain.Permission{domain.PermissionPipelineRead}})
	if err != nil {
		t.Fatal(err)
	}
	if minter.requests[0].Permissions != nil {
		t.Fatalf("permissions = %v, want inherit", minter.requests[0].Permissions)
	}
	target, _ := url.Parse(redirect)
	token, err := service.Exchange(context.Background(), TokenRequest{GrantType: "authorization_code", Code: target.Query().Get("code"), ClientID: client.ClientID, CodeVerifier: verifier})
	if err != nil {
		t.Fatal(err)
	}
	// The scope names what the key can do even when it was not narrowed.
	if token.Scope != "escalation_path.read pipeline.read pipeline.write" {
		t.Fatalf("scope = %q", token.Scope)
	}
}

func TestSealedTokenNeedsTheCode(t *testing.T) {
	sealed, err := seal("code-one", "whk_secret")
	if err != nil {
		t.Fatal(err)
	}
	if token, err := unseal("code-one", sealed); err != nil || token != "whk_secret" {
		t.Fatalf("unseal = %q, %v", token, err)
	}
	if _, err := unseal("code-two", sealed); err == nil {
		t.Fatal("a different code opened the seal")
	}
	if _, err := unseal("code-one", sealed[:5]); err == nil {
		t.Fatal("a truncated seal opened")
	}
}

func TestAMetadataDocumentClientConnectsWithoutRegistering(t *testing.T) {
	service, minter, _ := metadataFixture(t, map[string]string{claudeMetadataURL: claudeMetadataDocument, claudeCodeMetadataURL: claudeCodeMetadataDocument})
	if !service.AuthorizationServerMetadata().ClientIDMetadataDocumentSupported {
		t.Fatal("metadata must advertise client ID metadata documents")
	}
	verifier, challenge := pkce(t)
	person := principal()
	request := AuthorizationRequest{ClientID: claudeMetadataURL, RedirectURI: "https://claude.ai/api/mcp/auth_callback", State: "s", CodeChallenge: challenge, CodeChallengeMethod: "S256"}
	consent, err := service.Preview(context.Background(), person, request)
	if err != nil {
		t.Fatal(err)
	}
	if consent.Client.Name != "Claude" || consent.Client.MetadataURL != claudeMetadataURL || consent.Client.ID == "" {
		t.Fatalf("client = %+v", consent.Client)
	}
	redirect, err := service.Authorize(context.Background(), person, Decision{AuthorizationRequest: request, Approve: true})
	if err != nil {
		t.Fatal(err)
	}
	if minter.requests[0].Name != "Claude" || minter.requests[0].OAuthClientID != consent.Client.ID {
		t.Fatalf("mint request = %+v", minter.requests[0])
	}
	target, _ := url.Parse(redirect)
	token, err := service.Exchange(context.Background(), TokenRequest{GrantType: "authorization_code", Code: target.Query().Get("code"), ClientID: claudeMetadataURL, RedirectURI: request.RedirectURI, CodeVerifier: verifier})
	if err != nil || !strings.HasPrefix(token.AccessToken, "whk_") {
		t.Fatalf("token = %+v, %v", token, err)
	}
	// The same URL is the same client; another URL is not, even for a
	// code it did not get.
	again, err := service.GetClient(context.Background(), claudeMetadataURL)
	if err != nil || again.ID != consent.Client.ID {
		t.Fatalf("second resolution = %+v, %v", again, err)
	}
	redirect, err = service.Authorize(context.Background(), person, Decision{AuthorizationRequest: request, Approve: true})
	if err != nil {
		t.Fatal(err)
	}
	target, _ = url.Parse(redirect)
	_, err = service.Exchange(context.Background(), TokenRequest{GrantType: "authorization_code", Code: target.Query().Get("code"), ClientID: claudeCodeMetadataURL, CodeVerifier: verifier})
	if refusal := oauthCode(t, err); refusal.Code != "invalid_grant" {
		t.Fatalf("another client redeemed the code: %v", err)
	}
	// Claude Code's loopback redirect matches on any port.
	if _, err := service.Preview(context.Background(), person, AuthorizationRequest{
		ClientID: claudeCodeMetadataURL, RedirectURI: "http://localhost:3118/callback", CodeChallenge: challenge, CodeChallengeMethod: "S256",
	}); err != nil {
		t.Fatalf("Claude Code: %v", err)
	}
}

func TestAMetadataDocumentIsReadAgainOnEveryAuthorization(t *testing.T) {
	documents := map[string]string{claudeMetadataURL: claudeMetadataDocument}
	service, _, fetcher := metadataFixture(t, documents)
	first, err := service.GetClient(context.Background(), claudeMetadataURL)
	if err != nil {
		t.Fatal(err)
	}
	documents[claudeMetadataURL] = strings.Replace(claudeMetadataDocument, `"client_name":"Claude"`, `"client_name":"Claude for Work"`, 1)
	second, err := service.GetClient(context.Background(), claudeMetadataURL)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.Name != "Claude for Work" || fetcher.fetches != 2 {
		t.Fatalf("second = %+v after %d fetches", second, fetcher.fetches)
	}
}

func TestMetadataDocumentsAreHeldToWhatThisServerSupports(t *testing.T) {
	const documentURL = "https://client.example.com/oauth/metadata"
	document := func(fields string) string {
		return `{"client_id":"` + documentURL + `","redirect_uris":["https://client.example.com/cb"]` + fields + `}`
	}
	for name, body := range map[string]string{
		"another client_id":   strings.Replace(document(""), documentURL, "https://evil.example.com/oauth/metadata", 1),
		"a client secret":     document(`,"client_secret":"shh"`),
		"a confidential auth": document(`,"token_endpoint_auth_method":"private_key_jwt"`),
		"no code grant":       document(`,"grant_types":["client_credentials"]`),
		"no code response":    document(`,"response_types":["token"]`),
		"an http redirect":    strings.Replace(document(""), "https://client.example.com/cb", "http://client.example.com/cb", 1),
		"no redirects":        `{"client_id":"` + documentURL + `"}`,
		"not JSON":            "<html>",
	} {
		service, minter, _ := metadataFixture(t, map[string]string{documentURL: body})
		_, err := service.GetClient(context.Background(), documentURL)
		if refusal := oauthCode(t, err); refusal.Code != "invalid_client" || refusal.Status != http.StatusBadRequest {
			t.Errorf("%s: %v", name, err)
		}
		if len(minter.requests) != 0 {
			t.Errorf("%s minted a key", name)
		}
	}
	// A document without a name is shown by its host.
	service, _, fetcher := metadataFixture(t, map[string]string{documentURL: document("")})
	client, err := service.GetClient(context.Background(), documentURL)
	if err != nil || client.Name != "client.example.com" {
		t.Fatalf("unnamed client = %+v, %v", client, err)
	}
	// A failed or refused fetch is a refusal, never a client.
	fetcher.status = http.StatusNotFound
	if _, err := service.GetClient(context.Background(), documentURL); err == nil {
		t.Fatal("a 404 document resolved")
	}
	if _, err := service.GetClient(context.Background(), "https://unreachable.example.com/metadata"); err == nil {
		t.Fatal("an unreachable document resolved")
	}
	// URLs the draft forbids are refused before anything is fetched.
	fetches := fetcher.fetches
	for _, clientID := range []string{
		"http://client.example.com/metadata", "https://client.example.com", "https://client.example.com/",
		"https://client.example.com/metadata?v=1", "https://client.example.com/metadata#x", "https://user:pass@client.example.com/metadata",
		"https://client.example.com/a/../metadata", "https://client.example.com/./metadata",
	} {
		if _, err := service.GetClient(context.Background(), clientID); err == nil {
			t.Errorf("%s resolved", clientID)
		}
	}
	if fetcher.fetches != fetches {
		t.Fatal("a forbidden URL was fetched")
	}
}

func TestWithoutAFetcherAURLIsAnUnknownClient(t *testing.T) {
	service, _, _ := fixture(t, true)
	if service.AuthorizationServerMetadata().ClientIDMetadataDocumentSupported {
		t.Fatal("metadata documents advertised without a fetcher")
	}
	_, err := service.GetClient(context.Background(), claudeMetadataURL)
	if refusal := oauthCode(t, err); refusal.Code != "invalid_client" || refusal.Status != http.StatusNotFound {
		t.Fatalf("URL client without a fetcher: %v", err)
	}
}
