package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/authn"
	"github.com/withHumanAI/withHuman/db"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/embeddedpg/embeddedpgtest"
)

// OAuth clients and codes in the store: a code redeems exactly once even
// under concurrent exchanges, expired codes are not found, and a key
// minted for a client reads back with the client's name.
func TestOAuthCodesRedeemOnceAndKeysRememberTheirClient(t *testing.T) {
	server := embeddedpgtest.Start(t)
	ctx := context.Background()
	store, err := Open(ctx, server.URL("postgres"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := db.Apply(ctx, store.Pool(), db.Migrations()); err != nil {
		t.Fatal(err)
	}
	recordAudit(t, store)
	now := time.Now().UTC().Truncate(time.Microsecond)
	org, user, member := domain.ID(uuid.NewString()), domain.ID(uuid.NewString()), domain.ID(uuid.NewString())
	if _, err := store.ActivateOrganization(ctx, application.ActivateOrganizationInput{OrganizationID: org, OrganizationSlug: "oauth", OrganizationName: "OAuth", UserID: user, MembershipID: member, DisplayName: "Owner", Email: "owner@example.com", PasswordHash: "test", AdmissionSource: domain.AdmissionManual, Now: now}); err != nil {
		t.Fatal(err)
	}
	client, err := store.CreateOAuthClient(ctx, application.CreateOAuthClientInput{ClientID: domain.ID(uuid.NewString()), Name: "Claude Code", RedirectURIs: []string{"http://127.0.0.1:4000/callback"}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if fetched, err := store.GetOAuthClient(ctx, application.GetOAuthClientInput{ClientID: client.ID}); err != nil || fetched.Name != "Claude Code" || len(fetched.RedirectURIs) != 1 {
		t.Fatalf("client = %+v, %v", fetched, err)
	}
	if _, err := store.GetOAuthClient(ctx, application.GetOAuthClientInput{ClientID: domain.ID(uuid.NewString())}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown client = %v", err)
	}
	keyID := domain.ID(uuid.NewString())
	key, err := store.CreateAPIKey(ctx, application.CreateAPIKeyInput{
		KeyID: keyID, OrganizationID: org, MembershipID: member, Name: "Claude Code", SecretHash: authn.HashToken("secret"),
		AssuranceAtIssue: "strong", OAuthClientID: client.ID, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if key.OAuthClientID != client.ID || key.OAuthClientName != "Claude Code" {
		t.Fatalf("key provenance = %q %q", key.OAuthClientID, key.OAuthClientName)
	}
	listed, err := store.ListAPIKeys(ctx, application.ListAPIKeysInput{OrganizationID: org, MembershipID: member})
	if err != nil || len(listed) != 1 || listed[0].OAuthClientName != "Claude Code" {
		t.Fatalf("listed = %+v, %v", listed, err)
	}
	plain, err := store.CreateAPIKey(ctx, application.CreateAPIKeyInput{
		KeyID: domain.ID(uuid.NewString()), OrganizationID: org, MembershipID: member, Name: "By hand", SecretHash: authn.HashToken("other"),
		AssuranceAtIssue: "strong", Now: now,
	})
	if err != nil || plain.OAuthClientID != "" || plain.OAuthClientName != "" {
		t.Fatalf("hand-made key carries a client: %+v, %v", plain, err)
	}

	hash := authn.HashToken("code")
	if err := store.CreateOAuthAuthorizationCode(ctx, application.CreateOAuthAuthorizationCodeInput{
		CodeID: domain.ID(uuid.NewString()), ClientID: client.ID, OrganizationID: org, APIKeyID: keyID, CodeHash: hash, SealedToken: []byte("sealed"),
		RedirectURI: "http://127.0.0.1:4000/callback", CodeChallenge: "challenge", Scope: "pipeline.read", Now: now, ExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	var wins, losses int
	var mu sync.Mutex
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			code, err := store.ConsumeOAuthAuthorizationCode(ctx, application.ConsumeOAuthAuthorizationCodeInput{CodeHash: hash, Now: now.Add(time.Second)})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && code.APIKeyID == keyID && string(code.SealedToken) == "sealed" && code.ConsumedAt != nil:
				wins++
			case errors.Is(err, domain.ErrNotFound):
				losses++
			default:
				t.Errorf("consume: %+v %v", code, err)
			}
		}()
	}
	group.Wait()
	if wins != 1 || losses != 7 {
		t.Fatalf("wins=%d losses=%d, want exactly one redemption", wins, losses)
	}
	expired := authn.HashToken("expired")
	if err := store.CreateOAuthAuthorizationCode(ctx, application.CreateOAuthAuthorizationCodeInput{
		CodeID: domain.ID(uuid.NewString()), ClientID: client.ID, OrganizationID: org, APIKeyID: keyID, CodeHash: expired, SealedToken: []byte("sealed"),
		RedirectURI: "http://127.0.0.1:4000/callback", CodeChallenge: "challenge", Scope: "", Now: now, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeOAuthAuthorizationCode(ctx, application.ConsumeOAuthAuthorizationCodeInput{CodeHash: expired, Now: now.Add(2 * time.Minute)}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expired code = %v", err)
	}
}

// A client known by its metadata document's URL keeps one row: the first
// save creates it, later saves refresh what the document says under the
// same ID, and the URL finds it.
func TestOAuthMetadataDocumentClientsKeepOneRowPerURL(t *testing.T) {
	server := embeddedpgtest.Start(t)
	ctx := context.Background()
	store, err := Open(ctx, server.URL("postgres"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := db.Apply(ctx, store.Pool(), db.Migrations()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	const metadataURL = "https://claude.ai/oauth/mcp-oauth-client-metadata"
	first, err := store.SaveOAuthClientMetadataDocument(ctx, application.SaveOAuthClientMetadataDocumentInput{
		ClientID: domain.ID(uuid.NewString()), MetadataURL: metadataURL, Name: "Claude",
		RedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback"}, ClientURI: "https://claude.ai", Now: now,
	})
	if err != nil || first.MetadataURL != metadataURL || first.Name != "Claude" {
		t.Fatalf("first save = %+v, %v", first, err)
	}
	second, err := store.SaveOAuthClientMetadataDocument(ctx, application.SaveOAuthClientMetadataDocumentInput{
		ClientID: domain.ID(uuid.NewString()), MetadataURL: metadataURL, Name: "Claude for Work",
		RedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback", "https://claude.com/api/mcp/auth_callback"}, Now: now.Add(time.Minute),
	})
	if err != nil || second.ID != first.ID || second.Name != "Claude for Work" || len(second.RedirectURIs) != 2 || second.ClientURI != "" || !second.CreatedAt.Equal(now) {
		t.Fatalf("second save = %+v, %v", second, err)
	}
	found, err := store.GetOAuthClient(ctx, application.GetOAuthClientInput{MetadataURL: metadataURL})
	if err != nil || found.ID != first.ID {
		t.Fatalf("by URL = %+v, %v", found, err)
	}
	if byID, err := store.GetOAuthClient(ctx, application.GetOAuthClientInput{ClientID: first.ID}); err != nil || byID.MetadataURL != metadataURL {
		t.Fatalf("by ID = %+v, %v", byID, err)
	}
	if _, err := store.GetOAuthClient(ctx, application.GetOAuthClientInput{MetadataURL: "https://elsewhere.example.com/metadata"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown URL = %v", err)
	}
	registered, err := store.CreateOAuthClient(ctx, application.CreateOAuthClientInput{ClientID: domain.ID(uuid.NewString()), Name: "Cursor", RedirectURIs: []string{"cursor://cb"}, Now: now})
	if err != nil || registered.MetadataURL != "" {
		t.Fatalf("registered client = %+v, %v", registered, err)
	}
}
