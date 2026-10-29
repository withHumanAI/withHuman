package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
)

// OAuth clients and authorization codes for the management MCP server.
// The keys they mint live in api_keys like every other personal key.

func (s *Store) CreateOAuthClient(ctx context.Context, input application.CreateOAuthClientInput) (domain.OAuthClient, error) {
	if err := input.Validate(); err != nil {
		return domain.OAuthClient{}, err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO oauth_clients (id, name, redirect_uris, client_uri, created_at)
		VALUES ($1, $2, $3, $4, $5)`, input.ClientID, input.Name, input.RedirectURIs, input.ClientURI, input.Now); err != nil {
		return domain.OAuthClient{}, err
	}
	return s.GetOAuthClient(ctx, application.GetOAuthClientInput{ClientID: input.ClientID})
}

func (s *Store) SaveOAuthClientMetadataDocument(ctx context.Context, input application.SaveOAuthClientMetadataDocumentInput) (domain.OAuthClient, error) {
	if err := input.Validate(); err != nil {
		return domain.OAuthClient{}, err
	}
	row := s.pool.QueryRow(ctx, `INSERT INTO oauth_clients (id, name, redirect_uris, client_uri, metadata_url, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (metadata_url) WHERE metadata_url IS NOT NULL
		DO UPDATE SET name = EXCLUDED.name, redirect_uris = EXCLUDED.redirect_uris, client_uri = EXCLUDED.client_uri
		RETURNING `+oauthClientColumns,
		input.ClientID, input.Name, input.RedirectURIs, input.ClientURI, input.MetadataURL, input.Now)
	return scanOAuthClient(row)
}

func (s *Store) GetOAuthClient(ctx context.Context, input application.GetOAuthClientInput) (domain.OAuthClient, error) {
	if err := input.Validate(); err != nil {
		return domain.OAuthClient{}, err
	}
	if input.MetadataURL != "" {
		return scanOAuthClient(s.pool.QueryRow(ctx, `SELECT `+oauthClientColumns+` FROM oauth_clients WHERE metadata_url = $1`, input.MetadataURL))
	}
	return scanOAuthClient(s.pool.QueryRow(ctx, `SELECT `+oauthClientColumns+` FROM oauth_clients WHERE id = $1`, input.ClientID))
}

const oauthClientColumns = `id::text, name, redirect_uris, client_uri, COALESCE(metadata_url, ''), created_at`

func scanOAuthClient(row pgx.Row) (domain.OAuthClient, error) {
	var client domain.OAuthClient
	var id string
	err := row.Scan(&id, &client.Name, &client.RedirectURIs, &client.ClientURI, &client.MetadataURL, &client.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OAuthClient{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.OAuthClient{}, err
	}
	client.ID = domain.ID(id)
	return client, nil
}

func (s *Store) CreateOAuthAuthorizationCode(ctx context.Context, input application.CreateOAuthAuthorizationCodeInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO oauth_authorization_codes
		(id, client_id, organization_id, api_key_id, code_hash, sealed_token, redirect_uri, code_challenge, scope, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		input.CodeID, input.ClientID, input.OrganizationID, input.APIKeyID, input.CodeHash, input.SealedToken,
		input.RedirectURI, input.CodeChallenge, input.Scope, input.Now, input.ExpiresAt)
	return err
}

// ConsumeOAuthAuthorizationCode stamps the code consumed in the same
// statement that reads it, so two concurrent exchanges cannot both
// succeed: the second sees no row.
func (s *Store) ConsumeOAuthAuthorizationCode(ctx context.Context, input application.ConsumeOAuthAuthorizationCodeInput) (domain.OAuthAuthorizationCode, error) {
	if err := input.Validate(); err != nil {
		return domain.OAuthAuthorizationCode{}, err
	}
	var code domain.OAuthAuthorizationCode
	var id, clientID, organizationID, keyID string
	err := s.pool.QueryRow(ctx, `UPDATE oauth_authorization_codes SET consumed_at = $2
		WHERE code_hash = $1 AND consumed_at IS NULL AND expires_at > $2
		RETURNING id::text, client_id::text, organization_id::text, api_key_id::text, sealed_token, redirect_uri, code_challenge, scope, created_at, expires_at, consumed_at`,
		input.CodeHash, input.Now).
		Scan(&id, &clientID, &organizationID, &keyID, &code.SealedToken, &code.RedirectURI, &code.CodeChallenge, &code.Scope, &code.CreatedAt, &code.ExpiresAt, &code.ConsumedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OAuthAuthorizationCode{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.OAuthAuthorizationCode{}, err
	}
	code.ID, code.ClientID, code.OrganizationID, code.APIKeyID = domain.ID(id), domain.ID(clientID), domain.ID(organizationID), domain.ID(keyID)
	return code, nil
}
