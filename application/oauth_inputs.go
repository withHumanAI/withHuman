package application

import (
	"context"
	"time"

	"github.com/withHumanAI/withHuman/domain"
)

// OAuthRepository persists the management MCP server's OAuth clients and
// the single-use authorization codes that carry a consented key to them.
type OAuthRepository interface {
	CreateOAuthClient(context.Context, CreateOAuthClientInput) (domain.OAuthClient, error)
	// SaveOAuthClientMetadataDocument records what a client's metadata
	// document says, creating the client's row the first time its URL is
	// seen and refreshing it after that.
	SaveOAuthClientMetadataDocument(context.Context, SaveOAuthClientMetadataDocumentInput) (domain.OAuthClient, error)
	GetOAuthClient(context.Context, GetOAuthClientInput) (domain.OAuthClient, error)
	CreateOAuthAuthorizationCode(context.Context, CreateOAuthAuthorizationCodeInput) error
	// ConsumeOAuthAuthorizationCode redeems one code exactly once: an
	// unknown, expired, or already consumed code is not found.
	ConsumeOAuthAuthorizationCode(context.Context, ConsumeOAuthAuthorizationCodeInput) (domain.OAuthAuthorizationCode, error)
}

type CreateOAuthClientInput struct {
	ClientID     domain.ID
	Name         string
	RedirectURIs []string
	ClientURI    string
	Now          time.Time
}

func (input CreateOAuthClientInput) Validate() error {
	if err := firstError([]error{
		requiredID("client ID", input.ClientID),
		requiredString("client name", input.Name),
		requiredTime("current time", input.Now),
	}); err != nil {
		return err
	}
	if len(input.RedirectURIs) == 0 {
		return invalidField("redirect URIs", "must name at least one URI")
	}
	for _, uri := range input.RedirectURIs {
		if err := requiredString("redirect URI", uri); err != nil {
			return err
		}
	}
	return nil
}

// SaveOAuthClientMetadataDocumentInput carries a fetched and checked
// metadata document. ClientID is the row's ID if the URL is new.
type SaveOAuthClientMetadataDocumentInput struct {
	ClientID     domain.ID
	MetadataURL  string
	Name         string
	RedirectURIs []string
	ClientURI    string
	Now          time.Time
}

func (input SaveOAuthClientMetadataDocumentInput) Validate() error {
	if err := requiredString("metadata URL", input.MetadataURL); err != nil {
		return err
	}
	return CreateOAuthClientInput{ClientID: input.ClientID, Name: input.Name, RedirectURIs: input.RedirectURIs, ClientURI: input.ClientURI, Now: input.Now}.Validate()
}

// GetOAuthClientInput finds a client by its row ID or, for a client
// identified by its metadata document, by that document's URL.
type GetOAuthClientInput struct {
	ClientID    domain.ID
	MetadataURL string
}

func (input GetOAuthClientInput) Validate() error {
	if (input.ClientID == "") == (input.MetadataURL == "") {
		return invalidField("client", "name exactly one of a client ID or a metadata URL")
	}
	if input.ClientID != "" {
		return requiredID("client ID", input.ClientID)
	}
	return nil
}

type CreateOAuthAuthorizationCodeInput struct {
	CodeID         domain.ID
	ClientID       domain.ID
	OrganizationID domain.ID
	APIKeyID       domain.ID
	CodeHash       []byte
	SealedToken    []byte
	RedirectURI    string
	CodeChallenge  string
	Scope          string
	Now            time.Time
	ExpiresAt      time.Time
}

func (input CreateOAuthAuthorizationCodeInput) Validate() error {
	if err := firstError([]error{
		requiredID("code ID", input.CodeID),
		requiredID("client ID", input.ClientID),
		requiredID("organization ID", input.OrganizationID),
		requiredID("API key ID", input.APIKeyID),
		requiredTokenHash("code hash", input.CodeHash),
		requiredString("redirect URI", input.RedirectURI),
		requiredString("code challenge", input.CodeChallenge),
		requiredTime("current time", input.Now),
		requiredTime("expiry time", input.ExpiresAt),
	}); err != nil {
		return err
	}
	if len(input.SealedToken) == 0 {
		return invalidField("sealed token", "is required")
	}
	if !input.ExpiresAt.After(input.Now) {
		return invalidField("expiry time", "must be in the future")
	}
	return nil
}

type ConsumeOAuthAuthorizationCodeInput struct {
	CodeHash []byte
	Now      time.Time
}

func (input ConsumeOAuthAuthorizationCodeInput) Validate() error {
	return firstError([]error{
		requiredTokenHash("code hash", input.CodeHash),
		requiredTime("current time", input.Now),
	})
}
