package application

import "context"

// InvitationGuard is an edition's say over which email addresses may be
// invited: the hosted edition refuses throwaway mail providers. The open
// edition has none and admits every address.
type InvitationGuard interface {
	CheckInvitationEmail(ctx context.Context, email string) error
}
