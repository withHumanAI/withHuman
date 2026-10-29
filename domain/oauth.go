package domain

import "time"

// OAuthClient is an MCP client known to the management MCP server's
// authorization endpoint: one that registered itself (RFC 7591), or one
// that names the URL of its metadata document as its client ID
// (MetadataURL), whose name and redirect URIs are refreshed from that
// document on every authorization. It is a public client: no secret, PKCE
// on every authorization. It belongs to no organization, because it
// arrives before anyone signs in and one installation connects to
// whichever organizations its people authorize.
type OAuthClient struct {
	ID           ID        `json:"id"`
	Name         string    `json:"name"`
	RedirectURIs []string  `json:"redirect_uris"`
	ClientURI    string    `json:"client_uri,omitempty"`
	MetadataURL  string    `json:"metadata_url,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// OAuthAuthorizationCode is one consent: the person approved a client in
// the browser, a personal API key was minted for it, and this single-use
// code carries that key to the client's token request. The raw key is
// sealed under the code, which only the client holds.
type OAuthAuthorizationCode struct {
	ID             ID
	ClientID       ID
	OrganizationID ID
	APIKeyID       ID
	SealedToken    []byte
	RedirectURI    string
	CodeChallenge  string
	Scope          string
	CreatedAt      time.Time
	ExpiresAt      time.Time
	ConsumedAt     *time.Time
}
