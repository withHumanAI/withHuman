// Package mcpoauth is the OAuth 2.1 authorization server in front of the
// management MCP endpoint: an MCP client registers itself or names the URL
// of its metadata document as its client ID, sends the person to the
// consent page, and exchanges the resulting code for a personal API key
// that acts as that person, narrowed to what they chose.
// The key is the access token, so every credential rule (live grants,
// revocation, expiry, the policy switch, audit) is the API key's own.
package mcpoauth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/authn"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/webhookdelivery"
)

const (
	// DefaultCodeTTL bounds how long a consent waits for its exchange.
	DefaultCodeTTL = 10 * time.Minute
	// maxClientNameLength bounds what the consent page will show.
	maxClientNameLength = 100
	maxRedirectURIs     = 10
	// A client metadata document is a few hundred bytes; the limit and
	// the timeout keep a hostile one from holding the consent page.
	maxMetadataDocumentBytes = 16 * 1024
	metadataDocumentTimeout  = 5 * time.Second
	maxMetadataURLLength     = 2048
	// sealLabel domain-separates the code-derived sealing key.
	sealLabel = "withhuman-oauth-authorization-code-seal"
)

var codeVerifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

// Error is an OAuth refusal: the RFC 6749 code the client reads, the
// description a person could act on, and the HTTP status. It unwraps to
// the matching domain sentinel so the product API's error writer maps it
// on the consent routes, while the token endpoint answers it in the
// OAuth shape.
type Error struct {
	Code        string
	Description string
	Status      int
}

func (e *Error) Error() string { return e.Code + ": " + e.Description }

func (e *Error) Unwrap() error {
	switch e.Status {
	case http.StatusNotFound:
		return domain.ErrNotFound
	case http.StatusUnauthorized:
		return domain.ErrUnauthenticated
	default:
		return domain.ErrInvalid
	}
}

func invalidRequest(description string) *Error {
	return &Error{Code: "invalid_request", Description: description, Status: http.StatusBadRequest}
}

// KeyMinter is the slice of the authentication service consent uses: the
// same minting path the settings page takes, with the same ceremony.
type KeyMinter interface {
	CreateAPIKey(ctx context.Context, principal domain.Principal, request authn.CreateAPIKeyRequest) (domain.APIKey, string, error)
	APIKeyBearable(principal domain.Principal) []domain.PermissionDefinition
	APIKeyScopingEnabled(ctx context.Context, organizationID domain.ID) (bool, error)
	PersonalAPIKeysAllowed(ctx context.Context, organizationID domain.ID) (bool, error)
}

// KeyReader answers the key a consumed code points at, for the token
// response's expiry.
type KeyReader interface {
	GetAPIKey(ctx context.Context, input application.GetAPIKeyInput) (domain.APIKey, error)
}

// DocumentFetcher gets a client's metadata document under the product's
// outbound egress policy (public HTTPS, no private addresses, no
// redirects, a bounded body).
type DocumentFetcher interface {
	Fetch(ctx context.Context, fetch webhookdelivery.Fetch) (webhookdelivery.Response, error)
}

type Config struct {
	// PublicOrigin is the issuer and the base of every endpoint URL.
	PublicOrigin string
	// MCPPath is the protected resource's path under the origin.
	MCPPath string
	// AuthorizePath is the browser page (a product route) that shows the
	// consent screen; the SPA serves it.
	AuthorizePath string
	CodeTTL       time.Duration
	// ClientMetadata fetches the metadata documents of clients whose
	// client ID is a URL. Without it only registered clients are known.
	ClientMetadata DocumentFetcher
}

type Service struct {
	repository application.OAuthRepository
	keys       KeyMinter
	reader     KeyReader
	clock      application.Clock
	config     Config
}

func NewService(repository application.OAuthRepository, keys KeyMinter, reader KeyReader, clock application.Clock, config Config) *Service {
	if config.CodeTTL <= 0 {
		config.CodeTTL = DefaultCodeTTL
	}
	config.PublicOrigin = strings.TrimRight(config.PublicOrigin, "/")
	return &Service{repository: repository, keys: keys, reader: reader, clock: clock, config: config}
}

// Endpoint paths under the origin. Registration and token exchange are
// API routes that authenticate themselves; the authorization endpoint is
// the consent page.
const (
	RegistrationPath              = "/auth/oauth/register"
	TokenPath                     = "/auth/oauth/token"
	AuthorizationServerMetaPath   = "/.well-known/oauth-authorization-server"
	ProtectedResourceMetaPathRoot = "/.well-known/oauth-protected-resource"
)

func (s *Service) Issuer() string       { return s.config.PublicOrigin }
func (s *Service) Resource() string     { return s.config.PublicOrigin + s.config.MCPPath }
func (s *Service) AuthorizeURL() string { return s.config.PublicOrigin + s.config.AuthorizePath }

// ResourceMetadataURL is what the MCP endpoint's WWW-Authenticate
// challenge points a client at.
func (s *Service) ResourceMetadataURL() string {
	return s.config.PublicOrigin + ProtectedResourceMetaPathRoot + s.config.MCPPath
}

// AuthorizationServerMetadata is RFC 8414 for this deployment.
type AuthorizationServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint"`
	ScopesSupported                   []string `json:"scopes_supported"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	ResponseModesSupported            []string `json:"response_modes_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	IssParameterSupported             bool     `json:"authorization_response_iss_parameter_supported"`
	ClientIDMetadataDocumentSupported bool     `json:"client_id_metadata_document_supported"`
	ServiceDocumentation              string   `json:"service_documentation"`
}

// ProtectedResourceMetadata is RFC 9728 for the MCP endpoint.
type ProtectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	ResourceName           string   `json:"resource_name"`
	ResourceDocumentation  string   `json:"resource_documentation"`
}

// ScopesSupported is every permission a personal key may carry: the
// scope vocabulary is the permission registry, nothing separate.
func ScopesSupported() []string {
	scopes := make([]string, 0)
	for _, definition := range domain.Permissions() {
		if definition.HeldBy(domain.HolderAPIKey) {
			scopes = append(scopes, string(definition.Key))
		}
	}
	slices.Sort(scopes)
	return scopes
}

const documentationURL = "https://withhuman.ai/docs/web-app/mcp-server"

func (s *Service) AuthorizationServerMetadata() AuthorizationServerMetadata {
	return AuthorizationServerMetadata{
		Issuer:                            s.Issuer(),
		AuthorizationEndpoint:             s.AuthorizeURL(),
		TokenEndpoint:                     s.config.PublicOrigin + TokenPath,
		RegistrationEndpoint:              s.config.PublicOrigin + RegistrationPath,
		ScopesSupported:                   ScopesSupported(),
		ResponseTypesSupported:            []string{"code"},
		ResponseModesSupported:            []string{"query"},
		GrantTypesSupported:               []string{"authorization_code"},
		TokenEndpointAuthMethodsSupported: []string{"none"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		IssParameterSupported:             true,
		ClientIDMetadataDocumentSupported: s.config.ClientMetadata != nil,
		ServiceDocumentation:              documentationURL,
	}
}

func (s *Service) ProtectedResourceMetadata() ProtectedResourceMetadata {
	return ProtectedResourceMetadata{
		Resource:               s.Resource(),
		AuthorizationServers:   []string{s.Issuer()},
		ScopesSupported:        ScopesSupported(),
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "withHuman",
		ResourceDocumentation:  documentationURL,
	}
}

// ClientRegistration is the RFC 7591 request body's relevant part. Every
// client is public: a secret is never issued, PKCE is always required.
type ClientRegistration struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	ClientURI               string   `json:"client_uri"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

// RegisteredClient is the RFC 7591 response.
type RegisteredClient struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	ClientURI               string   `json:"client_uri,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

func (s *Service) RegisterClient(ctx context.Context, registration ClientRegistration) (RegisteredClient, error) {
	name := strings.TrimSpace(registration.ClientName)
	if name == "" {
		name = "MCP client"
	}
	if len(name) > maxClientNameLength {
		return RegisteredClient{}, &Error{Code: "invalid_client_metadata", Description: "client_name is too long.", Status: http.StatusBadRequest}
	}
	if len(registration.RedirectURIs) == 0 || len(registration.RedirectURIs) > maxRedirectURIs {
		return RegisteredClient{}, &Error{Code: "invalid_redirect_uri", Description: "redirect_uris must name between 1 and 10 URIs.", Status: http.StatusBadRequest}
	}
	for _, uri := range registration.RedirectURIs {
		if err := validateRedirectURI(uri); err != nil {
			return RegisteredClient{}, &Error{Code: "invalid_redirect_uri", Description: err.Error(), Status: http.StatusBadRequest}
		}
	}
	if method := registration.TokenEndpointAuthMethod; method != "" && method != "none" {
		return RegisteredClient{}, &Error{Code: "invalid_client_metadata", Description: "Only public clients are supported: token_endpoint_auth_method must be none.", Status: http.StatusBadRequest}
	}
	for _, grant := range registration.GrantTypes {
		if grant != "authorization_code" && grant != "refresh_token" {
			return RegisteredClient{}, &Error{Code: "invalid_client_metadata", Description: fmt.Sprintf("Grant type %q is not supported.", grant), Status: http.StatusBadRequest}
		}
	}
	for _, response := range registration.ResponseTypes {
		if response != "code" {
			return RegisteredClient{}, &Error{Code: "invalid_client_metadata", Description: fmt.Sprintf("Response type %q is not supported.", response), Status: http.StatusBadRequest}
		}
	}
	clientURI := strings.TrimSpace(registration.ClientURI)
	if clientURI != "" {
		if parsed, err := url.Parse(clientURI); err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return RegisteredClient{}, &Error{Code: "invalid_client_metadata", Description: "client_uri must be an HTTP(S) URL.", Status: http.StatusBadRequest}
		}
	}
	client, err := s.repository.CreateOAuthClient(ctx, application.CreateOAuthClientInput{
		ClientID: domain.ID(uuid.NewString()), Name: name, RedirectURIs: registration.RedirectURIs, ClientURI: clientURI, Now: s.clock.Now(),
	})
	if err != nil {
		return RegisteredClient{}, err
	}
	return registeredClient(client), nil
}

func registeredClient(client domain.OAuthClient) RegisteredClient {
	return RegisteredClient{
		ClientID: string(client.ID), ClientIDIssuedAt: client.CreatedAt.Unix(), ClientName: client.Name,
		RedirectURIs: client.RedirectURIs, ClientURI: client.ClientURI, TokenEndpointAuthMethod: "none",
		GrantTypes: []string{"authorization_code"}, ResponseTypes: []string{"code"},
	}
}

// validateRedirectURI admits what RFC 8252 native apps use: https
// anywhere, http only on the loopback interface, and private-use schemes
// (cursor://...) that cannot run script. No fragments.
func validateRedirectURI(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" {
		return fmt.Errorf("redirect URI %q is not an absolute URI", raw)
	}
	if parsed.Fragment != "" {
		return fmt.Errorf("redirect URI %q must not carry a fragment", raw)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		if parsed.Host == "" {
			return fmt.Errorf("redirect URI %q needs a host", raw)
		}
	case "http":
		if !isLoopbackHost(parsed.Hostname()) {
			return fmt.Errorf("redirect URI %q must use https unless it is a loopback address", raw)
		}
	case "javascript", "data", "vbscript", "file":
		return fmt.Errorf("redirect URI %q uses a scheme that cannot receive a code", raw)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// redirectURIMatches compares a presented redirect URI to a registered
// one exactly, except that a loopback http URI may use any port, as RFC
// 8252 requires for native apps that bind an ephemeral listener.
func redirectURIMatches(registered, presented string) bool {
	if registered == presented {
		return true
	}
	first, err := url.Parse(registered)
	if err != nil {
		return false
	}
	second, err := url.Parse(presented)
	if err != nil {
		return false
	}
	if first.Scheme != "http" || second.Scheme != "http" || !isLoopbackHost(first.Hostname()) || !isLoopbackHost(second.Hostname()) {
		return false
	}
	return first.Hostname() == second.Hostname() && first.Path == second.Path && first.RawQuery == second.RawQuery
}

// GetClient answers the client a client ID names: a registered client's
// UUID, or the HTTPS URL of a client's metadata document, which is fetched
// and checked on every call so the consent page shows what the document
// says now.
func (s *Service) GetClient(ctx context.Context, clientID string) (domain.OAuthClient, error) {
	if isMetadataURLShaped(clientID) {
		return s.metadataDocumentClient(ctx, clientID)
	}
	if _, err := uuid.Parse(clientID); err != nil {
		return domain.OAuthClient{}, unknownClient()
	}
	client, err := s.repository.GetOAuthClient(ctx, application.GetOAuthClientInput{ClientID: domain.ID(clientID)})
	if errors.Is(err, domain.ErrNotFound) {
		return domain.OAuthClient{}, unknownClient()
	}
	return client, err
}

func unknownClient() *Error {
	return &Error{Code: "invalid_client", Description: "Unknown client.", Status: http.StatusNotFound}
}

func invalidMetadataDocument(description string) *Error {
	return &Error{Code: "invalid_client", Description: description, Status: http.StatusBadRequest}
}

// isMetadataURLShaped tells a metadata document URL from a registered
// client's UUID; whether the URL is acceptable is checked afterwards.
func isMetadataURLShaped(clientID string) bool {
	return strings.HasPrefix(clientID, "https://") || strings.HasPrefix(clientID, "http://")
}

// clientMetadataDocument is the part of a client's metadata document this
// server reads (draft-ietf-oauth-client-id-metadata-document, RFC 7591
// field names). ClientSecret is read only to refuse it.
type clientMetadataDocument struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name"`
	ClientURI               string   `json:"client_uri"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	ClientSecret            *string  `json:"client_secret"`
}

// metadataDocumentClient fetches the document a URL client ID names,
// checks it, and records it, so keys and codes refer to the client by a
// row like any registered client's.
func (s *Service) metadataDocumentClient(ctx context.Context, metadataURL string) (domain.OAuthClient, error) {
	if s.config.ClientMetadata == nil {
		return domain.OAuthClient{}, unknownClient()
	}
	if refusal := validateMetadataURL(metadataURL); refusal != nil {
		return domain.OAuthClient{}, refusal
	}
	response, err := s.config.ClientMetadata.Fetch(ctx, webhookdelivery.Fetch{
		URL: metadataURL, Accept: "application/json", Timeout: metadataDocumentTimeout, MaximumBytes: maxMetadataDocumentBytes,
	})
	if err != nil {
		return domain.OAuthClient{}, invalidMetadataDocument("The client's metadata document could not be fetched.")
	}
	if response.StatusCode != http.StatusOK {
		return domain.OAuthClient{}, invalidMetadataDocument(fmt.Sprintf("The client's metadata document answered status %d.", response.StatusCode))
	}
	var document clientMetadataDocument
	if err := json.Unmarshal(response.Body, &document); err != nil {
		return domain.OAuthClient{}, invalidMetadataDocument("The client's metadata document is not a JSON object.")
	}
	name, clientURI, refusal := checkMetadataDocument(metadataURL, document)
	if refusal != nil {
		return domain.OAuthClient{}, refusal
	}
	return s.repository.SaveOAuthClientMetadataDocument(ctx, application.SaveOAuthClientMetadataDocumentInput{
		ClientID: domain.ID(uuid.NewString()), MetadataURL: metadataURL, Name: name,
		RedirectURIs: document.RedirectURIs, ClientURI: clientURI, Now: s.clock.Now(),
	})
}

// validateMetadataURL applies the draft's rules to a URL client ID:
// https, a host, a path, no dot segments, no credentials, query or
// fragment. Whether its host is reachable is the egress policy's call.
func validateMetadataURL(raw string) *Error {
	if len(raw) > maxMetadataURLLength {
		return invalidMetadataDocument("The client ID URL is too long.")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return invalidMetadataDocument("A client ID URL must be an https URL with a host.")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Contains(raw, "#") {
		return invalidMetadataDocument("A client ID URL must not carry credentials, a query or a fragment.")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		return invalidMetadataDocument("A client ID URL must have a path.")
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return invalidMetadataDocument("A client ID URL must not contain dot segments.")
		}
	}
	return nil
}

// checkMetadataDocument holds a fetched document to what this server
// supports: it names the URL it was fetched from, it describes a public
// client that uses the authorization code flow, and its redirect URIs are
// ones a registration would accept. Grant types this server does not
// issue may be listed; they are never offered.
func checkMetadataDocument(metadataURL string, document clientMetadataDocument) (string, string, *Error) {
	if document.ClientID != metadataURL {
		return "", "", invalidMetadataDocument("The client's metadata document names a different client_id.")
	}
	if document.ClientSecret != nil {
		return "", "", invalidMetadataDocument("A client metadata document must not carry a client_secret.")
	}
	if method := document.TokenEndpointAuthMethod; method != "" && method != "none" {
		return "", "", invalidMetadataDocument("Only public clients are supported: token_endpoint_auth_method must be none.")
	}
	if len(document.GrantTypes) > 0 && !slices.Contains(document.GrantTypes, "authorization_code") {
		return "", "", invalidMetadataDocument("The client's metadata document does not use the authorization_code grant.")
	}
	if len(document.ResponseTypes) > 0 && !slices.Contains(document.ResponseTypes, "code") {
		return "", "", invalidMetadataDocument("The client's metadata document does not use response_type code.")
	}
	if len(document.RedirectURIs) == 0 || len(document.RedirectURIs) > maxRedirectURIs {
		return "", "", invalidMetadataDocument("The client's metadata document must name between 1 and 10 redirect_uris.")
	}
	for _, uri := range document.RedirectURIs {
		if err := validateRedirectURI(uri); err != nil {
			return "", "", invalidMetadataDocument(err.Error())
		}
	}
	host, _ := url.Parse(metadataURL)
	name := strings.TrimSpace(document.ClientName)
	if name == "" {
		name = host.Hostname()
	}
	if len(name) > maxClientNameLength {
		return "", "", invalidMetadataDocument("The client's name in its metadata document is too long.")
	}
	clientURI := strings.TrimSpace(document.ClientURI)
	if clientURI != "" {
		if parsed, err := url.Parse(clientURI); err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return "", "", invalidMetadataDocument("client_uri must be an HTTP(S) URL.")
		}
	}
	return name, clientURI, nil
}

// AuthorizationRequest is what the client put in the authorization URL
// and the consent page hands back.
type AuthorizationRequest struct {
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	State               string `json:"state"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	Scope               string `json:"scope"`
	Resource            string `json:"resource"`
	ResponseType        string `json:"response_type"`
}

// validate checks the request against the client before anything is
// shown or minted. The redirect URI is checked first so a refusal is
// never sent to an address the client did not register.
func (s *Service) validate(ctx context.Context, request AuthorizationRequest) (domain.OAuthClient, error) {
	client, err := s.GetClient(ctx, request.ClientID)
	if err != nil {
		return domain.OAuthClient{}, err
	}
	if request.RedirectURI == "" {
		return domain.OAuthClient{}, invalidRequest("redirect_uri is required.")
	}
	if !slices.ContainsFunc(client.RedirectURIs, func(registered string) bool { return redirectURIMatches(registered, request.RedirectURI) }) {
		return domain.OAuthClient{}, invalidRequest("redirect_uri does not match one the client registered.")
	}
	if request.ResponseType != "" && request.ResponseType != "code" {
		return domain.OAuthClient{}, &Error{Code: "unsupported_response_type", Description: "Only response_type=code is supported.", Status: http.StatusBadRequest}
	}
	if request.CodeChallengeMethod != "S256" {
		return domain.OAuthClient{}, invalidRequest("code_challenge_method must be S256.")
	}
	if !codeVerifierPattern.MatchString(request.CodeChallenge) {
		return domain.OAuthClient{}, invalidRequest("code_challenge must be a base64url S256 challenge.")
	}
	if request.Resource != "" && strings.TrimRight(request.Resource, "/") != s.Resource() {
		return domain.OAuthClient{}, &Error{Code: "invalid_target", Description: "resource must be this deployment's MCP endpoint.", Status: http.StatusBadRequest}
	}
	return client, nil
}

// Consent is what the consent page shows: who is asking, what the person
// could grant, and what the client asked for.
type Consent struct {
	Client domain.OAuthClient
	// Requested is the subset of the client's scope the person could
	// actually grant; unknown or unheld scopes are dropped silently, as
	// RFC 6749 allows the server to narrow.
	Requested []domain.Permission
	// Bearable is the person's full vocabulary for the picker.
	Bearable []domain.PermissionDefinition
	// ScopingEnabled says whether a key may carry a list at all; when it
	// is false the key inherits everything the person holds.
	ScopingEnabled bool
	// PersonalKeysAllowed is the organization's policy switch; when it is
	// off the page explains instead of offering to authorize.
	PersonalKeysAllowed bool
}

func (s *Service) Preview(ctx context.Context, principal domain.Principal, request AuthorizationRequest) (Consent, error) {
	client, err := s.validate(ctx, request)
	if err != nil {
		return Consent{}, err
	}
	scoping, err := s.keys.APIKeyScopingEnabled(ctx, principal.OrganizationID)
	if err != nil {
		return Consent{}, err
	}
	allowed, err := s.keys.PersonalAPIKeysAllowed(ctx, principal.OrganizationID)
	if err != nil {
		return Consent{}, err
	}
	bearable := s.keys.APIKeyBearable(principal)
	return Consent{
		Client: client, Requested: requestedPermissions(request.Scope, bearable),
		Bearable: bearable, ScopingEnabled: scoping, PersonalKeysAllowed: allowed,
	}, nil
}

func requestedPermissions(scope string, bearable []domain.PermissionDefinition) []domain.Permission {
	requested := make([]domain.Permission, 0)
	for _, raw := range strings.Fields(scope) {
		permission := domain.Permission(raw)
		if slices.ContainsFunc(bearable, func(definition domain.PermissionDefinition) bool { return definition.Key == permission }) && !slices.Contains(requested, permission) {
			requested = append(requested, permission)
		}
	}
	return requested
}

// Decision is the person's answer on the consent page.
type Decision struct {
	AuthorizationRequest
	Approve bool
	// Permissions narrows the key; nil inherits everything the person
	// holds, which is the only shape when scoping is disabled.
	Permissions []domain.Permission
	ExpiresAt   *time.Time
}

// Authorize records the decision and answers the URL to send the browser
// to: the client's redirect URI with a single-use code, or with the
// refusal when the person declined. Approval mints the key here, under
// the person's live session, so the same freshness and MFA ceremony a
// hand-made key needs applies.
func (s *Service) Authorize(ctx context.Context, principal domain.Principal, decision Decision) (string, error) {
	client, err := s.validate(ctx, decision.AuthorizationRequest)
	if err != nil {
		return "", err
	}
	if !decision.Approve {
		return redirectWith(decision.RedirectURI, url.Values{"error": {"access_denied"}, "error_description": {"The person declined."}, "state": {decision.State}, "iss": {s.Issuer()}}), nil
	}
	scoping, err := s.keys.APIKeyScopingEnabled(ctx, principal.OrganizationID)
	if err != nil {
		return "", err
	}
	permissions := decision.Permissions
	if !scoping {
		permissions = nil
	}
	key, token, err := s.keys.CreateAPIKey(ctx, principal, authn.CreateAPIKeyRequest{
		Name: client.Name, Permissions: permissions, ExpiresAt: decision.ExpiresAt, OAuthClientID: client.ID,
	})
	if err != nil {
		return "", err
	}
	granted := key.Permissions
	if granted == nil {
		for _, definition := range s.keys.APIKeyBearable(principal) {
			granted = append(granted, definition.Key)
		}
	}
	code, err := randomCode()
	if err != nil {
		return "", err
	}
	sealed, err := seal(code, token)
	if err != nil {
		return "", err
	}
	now := s.clock.Now()
	if err := s.repository.CreateOAuthAuthorizationCode(ctx, application.CreateOAuthAuthorizationCodeInput{
		CodeID: domain.ID(uuid.NewString()), ClientID: client.ID, OrganizationID: principal.OrganizationID, APIKeyID: key.ID,
		CodeHash: authn.HashToken(code), SealedToken: sealed, RedirectURI: decision.RedirectURI, CodeChallenge: decision.CodeChallenge,
		Scope: joinPermissions(granted), Now: now, ExpiresAt: now.Add(s.config.CodeTTL),
	}); err != nil {
		return "", err
	}
	return redirectWith(decision.RedirectURI, url.Values{"code": {code}, "state": {decision.State}, "iss": {s.Issuer()}}), nil
}

func joinPermissions(permissions []domain.Permission) string {
	parts := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		parts = append(parts, string(permission))
	}
	slices.Sort(parts)
	return strings.Join(parts, " ")
}

// redirectWith appends the answer to the client's redirect URI, keeping
// any query the client registered.
func redirectWith(redirectURI string, answer url.Values) string {
	parsed, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}
	query := parsed.Query()
	for key, values := range answer {
		if len(values) == 1 && values[0] == "" {
			continue
		}
		query[key] = values
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// TokenRequest is the token endpoint's form body.
type TokenRequest struct {
	GrantType    string
	Code         string
	RedirectURI  string
	ClientID     string
	CodeVerifier string
}

// TokenResponse is RFC 6749 section 5.1. The access token is the personal
// API key; there is no refresh token, because the key lives until it
// expires or is revoked and a client whose key stopped simply
// authorizes again.
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in,omitempty"`
	Scope       string `json:"scope"`
}

func (s *Service) Exchange(ctx context.Context, request TokenRequest) (TokenResponse, error) {
	switch request.GrantType {
	case "authorization_code":
	case "":
		return TokenResponse{}, invalidRequest("grant_type is required.")
	default:
		return TokenResponse{}, &Error{Code: "unsupported_grant_type", Description: "Only authorization_code is supported; authorize again when a key stops working.", Status: http.StatusBadRequest}
	}
	if request.Code == "" || request.CodeVerifier == "" || request.ClientID == "" {
		return TokenResponse{}, invalidRequest("code, code_verifier and client_id are required.")
	}
	if !codeVerifierPattern.MatchString(request.CodeVerifier) {
		return TokenResponse{}, invalidRequest("code_verifier is malformed.")
	}
	now := s.clock.Now()
	code, err := s.repository.ConsumeOAuthAuthorizationCode(ctx, application.ConsumeOAuthAuthorizationCodeInput{CodeHash: authn.HashToken(request.Code), Now: now})
	if errors.Is(err, domain.ErrNotFound) {
		return TokenResponse{}, &Error{Code: "invalid_grant", Description: "The authorization code is unknown, expired, or already used.", Status: http.StatusBadRequest}
	}
	if err != nil {
		return TokenResponse{}, err
	}
	// The code is consumed either way: a mismatch below burns it, so a
	// stolen code cannot be retried against a guessed verifier.
	if !s.issuedTo(ctx, code.ClientID, request.ClientID) {
		return TokenResponse{}, &Error{Code: "invalid_grant", Description: "The code was issued to a different client.", Status: http.StatusBadRequest}
	}
	if request.RedirectURI != "" && request.RedirectURI != code.RedirectURI {
		return TokenResponse{}, &Error{Code: "invalid_grant", Description: "redirect_uri does not match the authorization request.", Status: http.StatusBadRequest}
	}
	if !verifierMatches(request.CodeVerifier, code.CodeChallenge) {
		return TokenResponse{}, &Error{Code: "invalid_grant", Description: "code_verifier does not match the code challenge.", Status: http.StatusBadRequest}
	}
	token, err := unseal(request.Code, code.SealedToken)
	if err != nil {
		return TokenResponse{}, &Error{Code: "invalid_grant", Description: "The authorization code could not be redeemed.", Status: http.StatusBadRequest}
	}
	response := TokenResponse{AccessToken: token, TokenType: "Bearer", Scope: code.Scope}
	key, err := s.reader.GetAPIKey(ctx, application.GetAPIKeyInput{OrganizationID: code.OrganizationID, KeyID: code.APIKeyID})
	if err == nil && key.ExpiresAt != nil {
		if remaining := key.ExpiresAt.Sub(now); remaining > 0 {
			response.ExpiresIn = int64(remaining / time.Second)
		}
	}
	return response, nil
}

// issuedTo reports whether the client ID a token request presents names
// the client a code was issued to. A URL client ID is looked up by the URL
// the consent recorded; the document is not fetched again.
func (s *Service) issuedTo(ctx context.Context, issued domain.ID, presented string) bool {
	if !isMetadataURLShaped(presented) {
		return string(issued) == presented
	}
	client, err := s.repository.GetOAuthClient(ctx, application.GetOAuthClientInput{MetadataURL: presented})
	return err == nil && client.ID == issued
}

func verifierMatches(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	expected := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(expected), []byte(challenge)) == 1
}

func randomCode() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

// seal encrypts the key token under a key derived from the code, so the
// stored row is useless without the code the client holds.
func seal(code, token string) ([]byte, error) {
	aead, err := codeCipher(code)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return append(nonce, aead.Seal(nil, nonce, []byte(token), []byte(sealLabel))...), nil
}

func unseal(code string, sealed []byte) (string, error) {
	aead, err := codeCipher(code)
	if err != nil {
		return "", err
	}
	if len(sealed) < aead.NonceSize() {
		return "", errors.New("sealed token is truncated")
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(sealLabel))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func codeCipher(code string) (cipher.AEAD, error) {
	key := sha256.Sum256([]byte(sealLabel + ":" + code))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
