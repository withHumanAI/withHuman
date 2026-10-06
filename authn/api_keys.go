package authn

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/authorization"
)

// maxAPIKeyNameLength bounds the label a person gives a key.
const maxAPIKeyNameLength = 100

// RegisterAPIKeys wires the personal API key repository. The whk_
// credential class stays here so every opaque token format has one home.
func (s *Service) RegisterAPIKeys(repository application.APIKeyRepository) { s.apiKeys = repository }

// CreateAPIKeyRequest is what a person asks for when minting a key.
// Permissions nil inherits everything they hold; a list narrows, which
// only the hosted edition allows.
type CreateAPIKeyRequest struct {
	Name        string
	Permissions []domain.Permission
	ExpiresAt   *time.Time
	// OAuthClientID marks a key minted for an MCP client through the
	// consent page rather than by hand.
	OAuthClientID domain.ID
}

// APIKeyBearable lists the registry entries the person could place on a
// key: held anywhere by them and marked holdable by an API key. The
// settings page renders its picker from this, so a reviewer without
// permission_policy.read still sees their own vocabulary.
func (s *Service) APIKeyBearable(principal domain.Principal) []domain.PermissionDefinition {
	bearable := make([]domain.PermissionDefinition, 0)
	for _, definition := range domain.Permissions() {
		if definition.HeldBy(domain.HolderAPIKey) && authorization.HoldsAnywhere(principal, definition.Key) {
			bearable = append(bearable, definition)
		}
	}
	return bearable
}

// APIKeyScopingEnabled reports whether this deployment and the
// organization's plan let a key carry a permission list: the open
// edition's keys inherit the member's role unchanged, the hosted edition
// lets people scope down behind the api_key_scoping capability.
func (s *Service) APIKeyScopingEnabled(ctx context.Context, organizationID domain.ID) (bool, error) {
	if !s.config.APIKeyScopingEnabled {
		return false, nil
	}
	decision, err := s.entitlements.CheckCapability(ctx, application.CheckCapabilityInput{
		OrganizationID: organizationID, Capability: application.CapabilityAPIKeyScoping,
	})
	if err != nil {
		return false, err
	}
	return decision.Allowed, nil
}

// PersonalAPIKeysAllowed reads the organization's policy switch.
func (s *Service) PersonalAPIKeysAllowed(ctx context.Context, organizationID domain.ID) (bool, error) {
	policy, err := s.identities.GetOrganizationPolicy(ctx, application.GetOrganizationPolicyInput{OrganizationID: organizationID})
	if err != nil {
		return false, err
	}
	return policy.PersonalAPIKeysAllowed, nil
}

// CreateAPIKey mints a personal key bound to the caller's membership. The
// raw token is returned exactly once; only its hash is stored. A key that
// would be able to decide requests or exercise a dangerous permission is
// minted only from a session that passes the organization's freshness
// and MFA rules now, and the key remembers that session's assurance.
func (s *Service) CreateAPIKey(ctx context.Context, principal domain.Principal, request CreateAPIKeyRequest) (domain.APIKey, string, error) {
	if s.apiKeys == nil {
		return domain.APIKey{}, "", domain.ErrNotFound
	}
	if principal.Auth.Method == domain.AuthMethodAPIKey {
		return domain.APIKey{}, "", domain.ErrAPIKeyNotAllowed
	}
	if err := requireHuman(principal, domain.PermissionAPIKeyIssue, domain.OrganizationResource()); err != nil {
		return domain.APIKey{}, "", err
	}
	policy, err := s.identities.GetOrganizationPolicy(ctx, application.GetOrganizationPolicyInput{OrganizationID: principal.OrganizationID})
	if err != nil {
		return domain.APIKey{}, "", err
	}
	if !policy.PersonalAPIKeysAllowed {
		return domain.APIKey{}, "", domain.ErrAPIKeysDisabled
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return domain.APIKey{}, "", fmt.Errorf("API key name is required: %w", domain.ErrInvalid)
	}
	if len(name) > maxAPIKeyNameLength {
		return domain.APIKey{}, "", fmt.Errorf("API key name must be at most %d characters: %w", maxAPIKeyNameLength, domain.ErrInvalid)
	}
	permissions, err := s.apiKeyPermissions(ctx, principal, request.Permissions)
	if err != nil {
		return domain.APIKey{}, "", err
	}
	now := s.clock.Now()
	expiresAt, err := s.apiKeyExpiry(request.ExpiresAt, now)
	if err != nil {
		return domain.APIKey{}, "", err
	}
	// The key will carry whatever the member holds, narrowed by the list:
	// that effective set decides whether minting needs the ceremony.
	effective := domain.NarrowGrants(principal.Grants, permissions)
	if apiKeyNeedsCeremony(domain.ProjectPermissions(effective)) {
		if rejection := authorization.RequireFresh(principal.Auth, policy.ApprovalReauthWindow, policy.MFAMode, now); rejection != nil {
			return domain.APIKey{}, "", rejection.Err
		}
	}
	keyID := domain.ID(uuid.NewString())
	token, secretHash, err := MintOpaqueToken(domain.APIKeyPrefix, keyID)
	if err != nil {
		return domain.APIKey{}, "", err
	}
	key, err := s.apiKeys.CreateAPIKey(ctx, application.CreateAPIKeyInput{
		KeyID: keyID, OrganizationID: principal.OrganizationID, MembershipID: principal.MembershipID,
		Name: name, Permissions: permissions, SecretHash: secretHash,
		AssuranceAtIssue: principal.Auth.Assurance, OAuthClientID: request.OAuthClientID, Now: now, ExpiresAt: expiresAt,
	})
	if err != nil {
		return domain.APIKey{}, "", err
	}
	return key, token, nil
}

// apiKeyPermissions validates a requested narrowing list: scoping must be
// available, and every entry must be a registered, key-bearable
// permission the caller holds somewhere. The list can only remove, so a
// permission the caller lacks is refused rather than silently dropped.
func (s *Service) apiKeyPermissions(ctx context.Context, principal domain.Principal, requested []domain.Permission) ([]domain.Permission, error) {
	if requested == nil {
		return nil, nil
	}
	enabled, err := s.APIKeyScopingEnabled(ctx, principal.OrganizationID)
	if err != nil {
		return nil, err
	}
	if !enabled {
		if !s.config.APIKeyScopingEnabled {
			return nil, domain.ErrAPIKeyScopingUnavailable
		}
		return nil, fmt.Errorf("scoped API keys are %w", domain.ErrPlanRequired)
	}
	if len(requested) == 0 {
		return nil, fmt.Errorf("API key permissions must name at least one permission: %w", domain.ErrInvalid)
	}
	permissions := make([]domain.Permission, 0, len(requested))
	for _, permission := range requested {
		if !domain.APIKeyBearable(permission) {
			return nil, fmt.Errorf("permission %q cannot be placed on an API key: %w", permission, domain.ErrInvalid)
		}
		if !authorization.HoldsAnywhere(principal, permission) {
			return nil, fmt.Errorf("permission %q is not held by the caller: %w", permission, domain.ErrInvalid)
		}
		if !slices.Contains(permissions, permission) {
			permissions = append(permissions, permission)
		}
	}
	slices.Sort(permissions)
	return permissions, nil
}

// apiKeyExpiry applies the deployment's optional cap: a key may expire
// whenever the person chooses, or never, unless WITHHUMAN_API_KEY_MAX_TTL
// bounds it.
func (s *Service) apiKeyExpiry(requested *time.Time, now time.Time) (*time.Time, error) {
	if requested != nil && !requested.After(now) {
		return nil, fmt.Errorf("API key expiry must be in the future: %w", domain.ErrInvalid)
	}
	if s.config.APIKeyMaxTTL <= 0 {
		return requested, nil
	}
	latest := now.Add(s.config.APIKeyMaxTTL)
	if requested == nil {
		return &latest, nil
	}
	if requested.After(latest) {
		return nil, fmt.Errorf("API key expiry must be within %s: %w", s.config.APIKeyMaxTTL, domain.ErrInvalid)
	}
	return requested, nil
}

// apiKeyNeedsCeremony says whether a key carrying these permissions must
// be minted from a fresh, strong session: deciding requests, or any
// dangerous permission, the same rule dangerous role grants use.
func apiKeyNeedsCeremony(permissions []domain.Permission) bool {
	for _, permission := range permissions {
		if permission == domain.PermissionRequestDecide || domain.PermissionIsDangerous(permission) {
			return true
		}
	}
	return false
}

// ListAPIKeys answers the caller's own keys, or every key in the
// organization when they hold api_key.read. Listing needs no permission
// beyond being a member: a person whose role changed still has to see
// and revoke the keys they minted.
func (s *Service) ListAPIKeys(ctx context.Context, principal domain.Principal, organizationWide bool) ([]domain.APIKey, error) {
	if s.apiKeys == nil {
		return nil, domain.ErrNotFound
	}
	if !humanMember(principal) || principal.Auth.Method == domain.AuthMethodAPIKey {
		return nil, domain.ErrForbidden
	}
	input := application.ListAPIKeysInput{OrganizationID: principal.OrganizationID, MembershipID: principal.MembershipID}
	if organizationWide {
		if err := authorization.Check(principal, domain.PermissionAPIKeyRead, domain.OrganizationResource()); err != nil {
			return nil, err
		}
		input.MembershipID = ""
	}
	return s.apiKeys.ListAPIKeys(ctx, input)
}

// RevokeAPIKey revokes one of the caller's own keys, or anyone's when
// they hold api_key.revoke. Revoking twice is idempotent.
func (s *Service) RevokeAPIKey(ctx context.Context, principal domain.Principal, keyID domain.ID, organizationWide bool) error {
	if s.apiKeys == nil {
		return domain.ErrNotFound
	}
	if !humanMember(principal) || principal.Auth.Method == domain.AuthMethodAPIKey {
		return domain.ErrForbidden
	}
	input := application.RevokeAPIKeyInput{
		OrganizationID: principal.OrganizationID, KeyID: keyID,
		MembershipID: principal.MembershipID, ActorMembershipID: principal.MembershipID, Now: s.clock.Now(),
	}
	if organizationWide {
		if err := authorization.Check(principal, domain.PermissionAPIKeyRevoke, domain.OrganizationResource()); err != nil {
			return err
		}
		input.MembershipID = ""
	}
	return s.apiKeys.RevokeAPIKey(ctx, input)
}

// VerifyAPIKey answers the key row behind the calling principal, the
// what-am-I a script runs first. A session has no key to verify.
func (s *Service) VerifyAPIKey(ctx context.Context, principal domain.Principal) (domain.APIKey, error) {
	if s.apiKeys == nil {
		return domain.APIKey{}, domain.ErrNotFound
	}
	if principal.Auth.Method != domain.AuthMethodAPIKey || principal.Auth.CredentialID == "" {
		return domain.APIKey{}, fmt.Errorf("the request was not authenticated with an API key: %w", domain.ErrInvalid)
	}
	return s.apiKeys.GetAPIKey(ctx, application.GetAPIKeyInput{
		OrganizationID: principal.OrganizationID, KeyID: principal.Auth.CredentialID,
	})
}

// ResolveAPIKey authenticates a presented whk_ bearer with a live lookup.
func (s *Service) ResolveAPIKey(ctx context.Context, token string) (domain.Principal, error) {
	if s.apiKeys == nil {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	prefix, kind := domain.APIKeyPrefix, domain.APIKeyPersonal
	if strings.HasPrefix(token, domain.OrganizationAPIKeyPrefix+"_") {
		prefix, kind = domain.OrganizationAPIKeyPrefix, domain.APIKeyOrganization
	}
	keyID, secret, err := ParseOpaqueToken(token, prefix)
	if err != nil {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	return s.apiKeys.ResolveAPIKey(ctx, application.ResolveAPIKeyInput{
		Kind: kind, KeyID: domain.ID(keyID), SecretHash: HashToken(secret), Now: s.clock.Now(),
	})
}

// IsAPIKeyToken reports whether a bearer value is a whk_ key, so the
// /api/v1 middleware can route it without parsing.
func IsAPIKeyToken(token string) bool {
	return strings.HasPrefix(token, domain.APIKeyPrefix+"_") || strings.HasPrefix(token, domain.OrganizationAPIKeyPrefix+"_")
}
