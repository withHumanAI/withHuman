package tenants

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
)

const (
	defaultDeletionGracePeriod = 30 * 24 * time.Hour
	slugSuffixLength           = 4
	maxSlugAttempts            = 5
)

type Config struct {
	// DeletionGracePeriod is how long a scheduled deletion can be canceled
	// before the purge runs. Zero means the thirty-day default.
	DeletionGracePeriod time.Duration
}

type Service struct {
	repo         application.TenantRepository
	orchestrator application.TenantOrchestrator
	hooks        TenantHooks
	clock        application.Clock
	logger       *slog.Logger
	config       Config
}

func NewService(repo application.TenantRepository, orchestrator application.TenantOrchestrator, hooks TenantHooks, clock application.Clock, logger *slog.Logger, config Config) *Service {
	if hooks == nil {
		hooks = NoopHooks{}
	}
	if config.DeletionGracePeriod <= 0 {
		config.DeletionGracePeriod = defaultDeletionGracePeriod
	}
	return &Service{repo: repo, orchestrator: orchestrator, hooks: hooks, clock: clock, logger: logger, config: config}
}

// ActivateOrganizationInput carries a founding owner whose email the caller
// has already verified: the bootstrap ceremony trusts the operator, hosted
// signup verifies by email round-trip or through an identity provider that
// verified it. Slug is optional; when empty it is derived from the
// organization name with a random suffix on collision.
type ActivateOrganizationInput struct {
	OrganizationName string
	OrganizationSlug string
	DisplayName      string
	Email            string
	// PasswordHash is the pre-hashed local credential for a new user, or
	// empty when the founder is an existing user or authenticates through
	// a provider.
	PasswordHash string
	// ProviderKey, Issuer, and Subject are the founder's provider identity
	// when an identity provider verified the email: all three set or all
	// empty, and exclusive with PasswordHash.
	ProviderKey     string
	Issuer          string
	Subject         string
	AdmissionSource domain.AdmissionSource
}

func (s *Service) ActivateOrganization(ctx context.Context, input ActivateOrganizationInput) (application.ActivationResult, error) {
	input.OrganizationName = strings.TrimSpace(input.OrganizationName)
	input.OrganizationSlug = strings.TrimSpace(input.OrganizationSlug)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if input.OrganizationName == "" || input.DisplayName == "" || input.Email == "" {
		return application.ActivationResult{}, fmt.Errorf("organization name, display name, and email are required: %w", domain.ErrInvalid)
	}
	explicitSlug := input.OrganizationSlug != ""
	slug := input.OrganizationSlug
	now := s.clock.Now()
	for attempt := 0; attempt < maxSlugAttempts; attempt++ {
		if !explicitSlug {
			slug = slugify(input.OrganizationName)
			if attempt > 0 {
				suffix, err := randomSlugSuffix()
				if err != nil {
					return application.ActivationResult{}, err
				}
				slug = slug + "-" + suffix
			}
		}
		result, err := s.repo.ActivateOrganization(ctx, application.ActivateOrganizationInput{
			OrganizationID:   domain.ID(uuid.NewString()),
			OrganizationSlug: slug,
			OrganizationName: input.OrganizationName,
			UserID:           domain.ID(uuid.NewString()),
			MembershipID:     domain.ID(uuid.NewString()),
			DisplayName:      input.DisplayName,
			Email:            input.Email,
			PasswordHash:     input.PasswordHash,
			ProviderKey:      input.ProviderKey,
			Issuer:           input.Issuer,
			Subject:          input.Subject,
			AdmissionSource:  input.AdmissionSource,
			Now:              now,
		})
		if errors.Is(err, domain.ErrConflict) && !explicitSlug {
			continue
		}
		if err != nil {
			return application.ActivationResult{}, err
		}
		s.fireHook(ctx, "organization_activated", func() error {
			return s.hooks.OrganizationActivated(ctx, OrganizationActivatedInput{
				Organization:     result.Organization,
				OwnerUserID:      result.Principal.ActorID,
				OwnerEmail:       input.Email,
				OwnerDisplayName: input.DisplayName,
			})
		})
		return result, nil
	}
	return application.ActivationResult{}, fmt.Errorf("could not find a free organization address. Try another name: %w", domain.ErrConflict)
}

type LifecycleActionInput struct {
	OrganizationID domain.ID
	Reason         string
	// ActorType is "human" for an operator action, "system" for automated
	// policy such as dunning. ActorID may be empty for system actors.
	ActorType string
	ActorID   domain.ID
}

func (s *Service) SuspendOrganization(ctx context.Context, input LifecycleActionInput) (domain.Organization, error) {
	organization, err := s.repo.SetOrganizationStatus(ctx, application.SetOrganizationStatusInput{
		OrganizationID: input.OrganizationID, Status: domain.OrganizationSuspended,
		Reason: input.Reason, ActorType: input.ActorType, ActorID: input.ActorID, Now: s.clock.Now(),
	})
	if err != nil {
		return domain.Organization{}, err
	}
	s.fireHook(ctx, "organization_suspended", func() error {
		return s.hooks.OrganizationSuspended(ctx, OrganizationSuspendedInput{Organization: organization, Reason: input.Reason})
	})
	return organization, nil
}

// ResumeOrganization restores an organization from suspended, and also
// serves as deletion cancellation from pending_deletion: the deletion
// workflow is canceled best-effort either way, and its purge activity
// independently refuses an organization that is no longer eligible.
func (s *Service) ResumeOrganization(ctx context.Context, input LifecycleActionInput) (domain.Organization, error) {
	organization, err := s.repo.SetOrganizationStatus(ctx, application.SetOrganizationStatusInput{
		OrganizationID: input.OrganizationID, Status: domain.OrganizationActive,
		Reason: input.Reason, ActorType: input.ActorType, ActorID: input.ActorID, Now: s.clock.Now(),
	})
	if err != nil {
		return domain.Organization{}, err
	}
	if err := s.orchestrator.CancelOrganizationDeletion(ctx, application.CancelOrganizationDeletionInput{
		OrganizationID: input.OrganizationID,
	}); err != nil {
		s.logger.WarnContext(ctx, "deletion workflow cancellation failed; purge activity will refuse on its own",
			"organization_id", string(input.OrganizationID), "error", err)
	}
	s.fireHook(ctx, "organization_resumed", func() error {
		return s.hooks.OrganizationResumed(ctx, OrganizationResumedInput{Organization: organization})
	})
	return organization, nil
}

// CancelDeletion restores a pending_deletion organization to active.
func (s *Service) CancelDeletion(ctx context.Context, input LifecycleActionInput) (domain.Organization, error) {
	return s.ResumeOrganization(ctx, input)
}

// ScheduleDeletion marks the organization pending_deletion and starts the
// durable purge timer. The workflow starts first: if the status write then
// fails, the purge activity finds an ineligible organization and no-ops.
func (s *Service) ScheduleDeletion(ctx context.Context, input LifecycleActionInput) (domain.Organization, error) {
	now := s.clock.Now()
	purgeAt := now.Add(s.config.DeletionGracePeriod)
	if err := s.orchestrator.StartOrganizationDeletion(ctx, application.StartOrganizationDeletionInput{
		OrganizationID: input.OrganizationID, PurgeAt: purgeAt,
	}); err != nil {
		return domain.Organization{}, fmt.Errorf("start deletion workflow: %w", err)
	}
	organization, err := s.repo.SetOrganizationStatus(ctx, application.SetOrganizationStatusInput{
		OrganizationID: input.OrganizationID, Status: domain.OrganizationPendingDeletion,
		Reason: input.Reason, DeletionScheduledFor: &purgeAt,
		ActorType: input.ActorType, ActorID: input.ActorID, Now: now,
	})
	if err != nil {
		return domain.Organization{}, err
	}
	s.fireHook(ctx, "deletion_scheduled", func() error {
		return s.hooks.DeletionScheduled(ctx, DeletionScheduledInput{Organization: organization, PurgeAt: purgeAt})
	})
	return organization, nil
}

// fireHook runs a lifecycle hook lossy-tolerantly: a hook failure is an
// operational signal, never a rollback of the committed action.
func (s *Service) fireHook(ctx context.Context, name string, hook func() error) {
	if err := hook(); err != nil {
		s.logger.WarnContext(ctx, "tenant lifecycle hook failed - "+name+"; organization change remains committed", "hook", name, "error", err)
	}
}

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	slug := strings.Trim(slugStrip.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	if slug == "" {
		slug = "org"
	}
	return slug
}

func randomSlugSuffix() (string, error) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	buffer := make([]byte, slugSuffixLength)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	for index := range buffer {
		buffer[index] = alphabet[int(buffer[index])%len(alphabet)]
	}
	return string(buffer), nil
}
