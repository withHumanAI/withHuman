// Package tenants owns the organization lifecycle: the activation
// transaction shared by the bootstrap ceremony and hosted signup, the
// suspension state machine, and deletion with a grace period. Editions
// differ only in which entry surfaces their composition root wires and in
// the TenantHooks adapter.
package tenants

import (
	"context"
	"time"

	"github.com/withHumanAI/withHuman/domain"
)

// TenantHooks observes lifecycle transitions after their canonical commit.
// The hosted adapter attaches billing; the open adapter is a no-op. Hook
// failures are logged and surfaced operationally, never rolled back into
// the product action, so implementations must tolerate replays.
type TenantHooks interface {
	OrganizationActivated(ctx context.Context, input OrganizationActivatedInput) error
	OrganizationSuspended(ctx context.Context, input OrganizationSuspendedInput) error
	OrganizationResumed(ctx context.Context, input OrganizationResumedInput) error
	DeletionScheduled(ctx context.Context, input DeletionScheduledInput) error
}

type OrganizationActivatedInput struct {
	Organization domain.Organization
	OwnerUserID  domain.ID
	OwnerEmail   string
	// OwnerDisplayName is the founder's name as entered at activation, or
	// their email when the edition had nothing better (a social login
	// without a profile name).
	OwnerDisplayName string
}

type OrganizationSuspendedInput struct {
	Organization domain.Organization
	Reason       string
}

type OrganizationResumedInput struct {
	Organization domain.Organization
}

type DeletionScheduledInput struct {
	Organization domain.Organization
	PurgeAt      time.Time
}

// NoopHooks is the open edition adapter: nothing observes lifecycle
// transitions.
type NoopHooks struct{}

func (NoopHooks) OrganizationActivated(context.Context, OrganizationActivatedInput) error { return nil }
func (NoopHooks) OrganizationSuspended(context.Context, OrganizationSuspendedInput) error { return nil }
func (NoopHooks) OrganizationResumed(context.Context, OrganizationResumedInput) error     { return nil }
func (NoopHooks) DeletionScheduled(context.Context, DeletionScheduledInput) error         { return nil }
