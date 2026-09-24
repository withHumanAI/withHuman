package application

import (
	"fmt"
	"time"

	"github.com/withHumanAI/withHuman/domain"
)

// CreateOnboardingSessionInput opens a guided setup for one member. Any
// open session the member already holds is closed as skipped in the same
// transaction, so the partial unique index never refuses the insert.
type CreateOnboardingSessionInput struct {
	Session            domain.OnboardingSession
	SetupCodeHash      []byte
	SetupCodeExpiresAt time.Time
}

func (input CreateOnboardingSessionInput) Validate() error {
	return firstError([]error{
		requiredID("session ID", input.Session.ID),
		requiredID("organization ID", input.Session.OrganizationID),
		requiredID("membership ID", input.Session.MembershipID),
		func() error {
			if input.Session.Status != domain.OnboardingAwaitingCLI {
				return fmt.Errorf("a new session starts awaiting the CLI: %w", domain.ErrInvalid)
			}
			if len(input.SetupCodeHash) == 0 {
				return fmt.Errorf("setup code hash is required: %w", domain.ErrInvalid)
			}
			if input.SetupCodeExpiresAt.IsZero() || input.Session.ExpiresAt.IsZero() || input.Session.CreatedAt.IsZero() {
				return fmt.Errorf("session timestamps are required: %w", domain.ErrInvalid)
			}
			return nil
		}(),
	})
}

type GetOnboardingSessionInput struct {
	OrganizationID domain.ID
	SessionID      domain.ID
}

func (input GetOnboardingSessionInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("session ID", input.SessionID),
	})
}

// GetOpenOnboardingSessionInput finds the member's one open session.
type GetOpenOnboardingSessionInput struct {
	OrganizationID domain.ID
	MembershipID   domain.ID
}

func (input GetOpenOnboardingSessionInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("membership ID", input.MembershipID),
	})
}

// ExchangeOnboardingSetupCodeInput redeems a setup code for the CLI's
// bearer: one atomic update that requires the code to be unspent and
// unexpired, records the machine, stores the bearer's secret hash, and
// moves the session to connected.
type ExchangeOnboardingSetupCodeInput struct {
	SetupCodeHash        []byte
	CredentialSecretHash []byte
	Machine              domain.OnboardingMachine
	Now                  time.Time
}

func (input ExchangeOnboardingSetupCodeInput) Validate() error {
	if len(input.SetupCodeHash) == 0 || len(input.CredentialSecretHash) == 0 {
		return fmt.Errorf("setup code hash and credential secret hash are required: %w", domain.ErrInvalid)
	}
	if input.Now.IsZero() {
		return fmt.Errorf("now is required: %w", domain.ErrInvalid)
	}
	return nil
}

// ResolveOnboardingCredentialInput authenticates a who_ bearer: the
// session must exist, hold that secret, be open and unexpired, and its
// organization and membership must both be active.
type ResolveOnboardingCredentialInput struct {
	SessionID  domain.ID
	SecretHash []byte
	Now        time.Time
}

func (input ResolveOnboardingCredentialInput) Validate() error {
	if err := requiredID("session ID", input.SessionID); err != nil {
		return err
	}
	if len(input.SecretHash) == 0 {
		return fmt.Errorf("secret hash is required: %w", domain.ErrInvalid)
	}
	if input.Now.IsZero() {
		return fmt.Errorf("now is required: %w", domain.ErrInvalid)
	}
	return nil
}

// UpdateOnboardingSessionInput is the one optimistic write both sides use:
// the row must currently be in one of ExpectedStatuses (empty means any
// open status), the patch replaces the fields it names, the status moves
// to NextStatus (empty keeps it), and the version increments. A row in an
// unexpected status is domain.ErrConflict; an expired one is
// domain.ErrExpired.
type UpdateOnboardingSessionInput struct {
	OrganizationID   domain.ID
	SessionID        domain.ID
	ExpectedStatuses []domain.OnboardingStatus
	Patch            domain.OnboardingSessionPatch
	NextStatus       domain.OnboardingStatus
	Now              time.Time
}

func (input UpdateOnboardingSessionInput) Validate() error {
	if err := firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("session ID", input.SessionID),
	}); err != nil {
		return err
	}
	if input.Now.IsZero() {
		return fmt.Errorf("now is required: %w", domain.ErrInvalid)
	}
	if input.NextStatus != "" && !input.NextStatus.Valid() {
		return fmt.Errorf("next status %q is unknown: %w", input.NextStatus, domain.ErrInvalid)
	}
	if input.NextStatus.Terminal() {
		return fmt.Errorf("terminal statuses are set through CompleteOnboarding: %w", domain.ErrInvalid)
	}
	for _, status := range input.ExpectedStatuses {
		if !status.Valid() {
			return fmt.Errorf("expected status %q is unknown: %w", status, domain.ErrInvalid)
		}
	}
	return nil
}

// WaitOnboardingSessionInput long-polls a session until its version
// exceeds SinceVersion or Timeout elapses, whichever comes first.
type WaitOnboardingSessionInput struct {
	OrganizationID domain.ID
	SessionID      domain.ID
	SinceVersion   int64
	Timeout        time.Duration
}

func (input WaitOnboardingSessionInput) Validate() error {
	if err := firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("session ID", input.SessionID),
	}); err != nil {
		return err
	}
	if input.Timeout < 0 || input.Timeout > time.Minute {
		return fmt.Errorf("wait must be between 0 and 60s: %w", domain.ErrInvalid)
	}
	return nil
}

// CompleteOnboardingInput closes the member's open session (if any) with
// the outcome and marks the organization's onboarding complete the first
// time. Finished requires the open session, when there is one, to be in
// the tested status.
type CompleteOnboardingInput struct {
	OrganizationID    domain.ID
	MembershipID      domain.ID
	ActorMembershipID domain.ID
	Outcome           domain.OnboardingOutcome
	Now               time.Time
}

func (input CompleteOnboardingInput) Validate() error {
	if err := firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		requiredID("membership ID", input.MembershipID),
	}); err != nil {
		return err
	}
	if input.Outcome != domain.OnboardingFinished && input.Outcome != domain.OnboardingSkip {
		return fmt.Errorf("outcome must be finished or skipped: %w", domain.ErrInvalid)
	}
	if input.Now.IsZero() {
		return fmt.Errorf("now is required: %w", domain.ErrInvalid)
	}
	return nil
}

// GetAgentBySlugInput looks an agent up by its slug, which names exactly
// one agent of the organization, live or archived.
type GetAgentBySlugInput struct {
	OrganizationID domain.ID
	AgentSlug      domain.AgentSlug
}

func (input GetAgentBySlugInput) Validate() error {
	return firstError([]error{
		requiredID("organization ID", input.OrganizationID),
		validAgentSlug(input.AgentSlug),
	})
}
