package application

import (
	"encoding/json"
	"time"

	"github.com/withHumanAI/withHuman/domain"
)

type CreateSessionInput struct {
	UserID         domain.ID
	OrganizationID domain.ID
	SecretHash     []byte
	IssuedAt       time.Time
	ExpiresAt      time.Time
	Auth           domain.AuthContext
	// Client is who minted the session; the zero value is the browser.
	Client SessionClient
}

// SessionClient records which client a session belongs to and what that
// client reported about itself. Kind is "web" for the browser and a
// platform key ("ios") for native apps; Context is the client's own
// description (app version, OS version, device model, device id), stored
// as given and never interpreted by the server.
type SessionClient struct {
	Kind    string
	Context json.RawMessage
}

// ClientWeb is the browser: the client every session had before native
// apps existed.
const ClientWeb = "web"

// Normalized fills the browser defaults so repositories store one shape.
func (client SessionClient) Normalized() SessionClient {
	if client.Kind == "" {
		client.Kind = ClientWeb
	}
	if len(client.Context) == 0 {
		client.Context = json.RawMessage(`{}`)
	}
	return client
}

func (input CreateSessionInput) Validate() error {
	checks := []error{
		requiredID("user ID", input.UserID),
		requiredID("organization ID", input.OrganizationID),
		requiredTokenHash("session secret hash", input.SecretHash),
		requiredTime("session issued time", input.IssuedAt),
		requiredTime("session expiry time", input.ExpiresAt),
		requiredString("authentication method", input.Auth.Method),
		requiredString("authentication assurance", input.Auth.Assurance),
		requiredTime("authentication time", input.Auth.AuthenticatedAt),
	}
	if err := firstError(checks); err != nil {
		return err
	}
	if !input.ExpiresAt.After(input.IssuedAt) {
		return invalidField("session expiry time", "must be after the issued time")
	}
	return nil
}

type ResolveSessionInput struct {
	SecretHash []byte
	Now        time.Time
}

func (input ResolveSessionInput) Validate() error {
	return firstError([]error{
		requiredTokenHash("session secret hash", input.SecretHash),
		requiredTime("current time", input.Now),
	})
}

type RevokeSessionInput struct {
	SessionID domain.ID
}

func (input RevokeSessionInput) Validate() error {
	return requiredID("session ID", input.SessionID)
}

type GetLocalCredentialInput struct {
	Email string
}

func (input GetLocalCredentialInput) Validate() error {
	return requiredString("email", input.Email)
}

type LocalCredentialRecord struct {
	UserID       domain.ID
	PasswordHash string
}

// SetLocalCredentialInput replaces (or creates) the password hash of a
// user. Callers hash first; the repository never sees a raw password.
type SetLocalCredentialInput struct {
	UserID       domain.ID
	PasswordHash string
	Now          time.Time
}

func (input SetLocalCredentialInput) Validate() error {
	return firstError([]error{
		requiredID("user ID", input.UserID),
		requiredString("password hash", input.PasswordHash),
		requiredTime("time", input.Now),
	})
}

// RevokeUserSessionsInput signs a person out everywhere. ExceptSessionID
// keeps the session that performed the action, so a password change does
// not sign out the browser it was made from.
type RevokeUserSessionsInput struct {
	UserID          domain.ID
	ExceptSessionID domain.ID
	Now             time.Time
}

func (input RevokeUserSessionsInput) Validate() error {
	return firstError([]error{
		requiredID("user ID", input.UserID),
		requiredTime("time", input.Now),
	})
}

type CreatePasswordResetInput struct {
	ResetID    domain.ID
	UserID     domain.ID
	SecretHash []byte
	IssuedAt   time.Time
	ExpiresAt  time.Time
}

func (input CreatePasswordResetInput) Validate() error {
	return firstError([]error{
		requiredID("reset ID", input.ResetID),
		requiredID("user ID", input.UserID),
		requiredBytes("secret hash", input.SecretHash),
		requiredTime("issue time", input.IssuedAt),
		requiredTime("expiry time", input.ExpiresAt),
	})
}

// GetPasswordResetInput checks a reset is live without consuming it, so
// the page can show the form before the person types anything.
type GetPasswordResetInput struct {
	ResetID    domain.ID
	SecretHash []byte
	Now        time.Time
}

func (input GetPasswordResetInput) Validate() error {
	return firstError([]error{
		requiredID("reset ID", input.ResetID),
		requiredBytes("secret hash", input.SecretHash),
		requiredTime("time", input.Now),
	})
}

type ConsumePasswordResetInput struct {
	ResetID    domain.ID
	SecretHash []byte
	Now        time.Time
}

func (input ConsumePasswordResetInput) Validate() error {
	return firstError([]error{
		requiredID("reset ID", input.ResetID),
		requiredBytes("secret hash", input.SecretHash),
		requiredTime("time", input.Now),
	})
}

type PasswordResetRecord struct {
	UserID    domain.ID
	ExpiresAt time.Time
}

func (record LocalCredentialRecord) Validate() error {
	return firstError([]error{
		requiredID("user ID", record.UserID),
		requiredString("password hash", record.PasswordHash),
	})
}

type FindMembershipForLoginInput struct {
	UserID         domain.ID
	OrganizationID domain.ID
}

func (input FindMembershipForLoginInput) Validate() error {
	return firstError([]error{
		requiredID("user ID", input.UserID),
		requiredID("organization ID", input.OrganizationID),
	})
}

// ListLoginOrganizationsInput resolves an email to everything the
// email-first login flow needs: the user (when one exists), whether a
// local password is set, and the active organizations the user can sign
// in to. Callers must treat the result as sensitive before the password
// verifies: only the SSO routing subset may be revealed pre-auth.
type ListLoginOrganizationsInput struct {
	Email string
}

func (input ListLoginOrganizationsInput) Validate() error {
	return requiredString("email", input.Email)
}

type LoginOrganizations struct {
	UserID        domain.ID
	HasPassword   bool
	Organizations []domain.Organization
}

// CreateLoginTicketInput persists the hash of a single-use ticket that
// bridges a verified authentication and the organization selection. The
// ticket carries how the person authenticated (method and assurance) so
// the session it later mints is labeled by the authenticator that earned
// it, not by the redemption step.
type CreateLoginTicketInput struct {
	Client     SessionClient
	TicketID   domain.ID
	UserID     domain.ID
	SecretHash []byte
	AuthMethod string
	Assurance  string
	IssuedAt   time.Time
	ExpiresAt  time.Time
}

func (input CreateLoginTicketInput) Validate() error {
	return firstError([]error{
		requiredID("ticket ID", input.TicketID),
		requiredID("user ID", input.UserID),
		requiredTokenHash("ticket secret hash", input.SecretHash),
		requiredString("authentication method", input.AuthMethod),
		requiredString("assurance", input.Assurance),
		requiredTime("issue time", input.IssuedAt),
		requiredTime("expiry time", input.ExpiresAt),
	})
}

// ConsumeLoginTicketInput redeems a ticket exactly once.
type ConsumeLoginTicketInput struct {
	TicketID   domain.ID
	SecretHash []byte
	Now        time.Time
}

func (input ConsumeLoginTicketInput) Validate() error {
	return firstError([]error{
		requiredID("ticket ID", input.TicketID),
		requiredTokenHash("ticket secret hash", input.SecretHash),
		requiredTime("current time", input.Now),
	})
}

type LoginTicketRecord struct {
	Client     SessionClient
	UserID     domain.ID
	AuthMethod string
	Assurance  string
	IssuedAt   time.Time
}

// CreateNativeLoginFlowInput records what a native app asked for when it
// started an enterprise SSO round trip: where to send the app afterwards
// and the PKCE challenge the exchange must answer.

// LinkVerifiedUserIdentityInput binds a provider identity to the active
// user whose primary email the provider verified. Unlike the
// organization-scoped member linking, this is global: the person owns the
// mailbox the provider vouched for, so the identity belongs to the
// account rather than to one membership. The caller must ensure the
// provider verified the email.
type LinkVerifiedUserIdentityInput struct {
	Email       string
	ProviderKey string
	Issuer      string
	Subject     string
	Now         time.Time
}

func (input LinkVerifiedUserIdentityInput) Validate() error {
	return firstError([]error{
		requiredString("email", input.Email),
		requiredString("provider key", input.ProviderKey),
		requiredString("issuer", input.Issuer),
		requiredString("subject", input.Subject),
		requiredTime("current time", input.Now),
	})
}

// LinkVerifiedMemberIdentityInput binds a provider identity to the
// organization member whose email the organization's own IdP asserted.
// This is identity linking for someone who already belongs, never
// admission: no membership is created, and the caller must ensure the
// email was verified by the asserting provider.
type LinkVerifiedMemberIdentityInput struct {
	OrganizationID domain.ID
	Email          string
	ProviderKey    string
	Issuer         string
	Subject        string
	Now            time.Time
}

func (input LinkVerifiedMemberIdentityInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredString("email", input.Email),
		requiredString("provider key", input.ProviderKey),
		requiredString("issuer", input.Issuer),
		requiredString("subject", input.Subject),
		requiredTime("current time", input.Now),
	})
}

type GetUserInput struct {
	UserID domain.ID
}

func (input GetUserInput) Validate() error {
	return requiredID("user ID", input.UserID)
}

type GetMembershipInput struct {
	OrganizationID domain.ID
	MembershipID   domain.ID
}

func (input GetMembershipInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("membership ID", input.MembershipID),
	})
}

type GetOrganizationPolicyInput struct {
	OrganizationID domain.ID
}

func (input GetOrganizationPolicyInput) Validate() error {
	return requiredID("organization ID", input.OrganizationID)
}

// UpdateOrganizationPolicyInput replaces the settable policy fields and
// audits the change as organization.policy_updated.
type UpdateOrganizationPolicyInput struct {
	OrganizationID             domain.ID
	AllowSelfApproval          bool
	MFAMode                    domain.MFAMode
	ApprovalReauthWindow       time.Duration
	ClaimRetriggerWindow       time.Duration
	UnroutedRequests           domain.UnroutedRequestsPolicy
	RequestVisibility          domain.RequestVisibilityPolicy
	PersonalAPIKeysAllowed     bool
	OrganizationAPIKeysAllowed bool
	ActorMembershipID          domain.ID
	Now                        time.Time
}

func (input UpdateOrganizationPolicyInput) Validate() error {
	if err := firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("acting membership ID", input.ActorMembershipID),
		requiredTime("current time", input.Now),
	}); err != nil {
		return err
	}
	switch input.MFAMode {
	case domain.MFAOptional, domain.MFAReviewersRequired, domain.MFAEveryoneRequired:
	default:
		return invalidField("mfa mode", "must be optional, reviewers_required, or everyone_required")
	}
	if input.ApprovalReauthWindow <= 0 {
		return invalidField("approval re-authentication window", "must be positive")
	}
	if input.ClaimRetriggerWindow <= 0 {
		return invalidField("claim window", "must be positive")
	}
	if !input.UnroutedRequests.Valid() {
		return invalidField("unrouted requests", "must be any_reviewer or break_glass_only")
	}
	if !input.RequestVisibility.Valid() {
		return invalidField("request visibility", "must be all_readers or routed_only")
	}
	return nil
}
