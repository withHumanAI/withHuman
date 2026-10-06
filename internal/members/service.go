// Package members owns the invitation and membership management surface:
// who may invite, which roles they may grant at which scope, and how
// memberships change state. Authentication-coupled invitation redemption
// lives in authn.
package members

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/authn"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/authorization"
)

const DefaultInvitationTTL = 7 * 24 * time.Hour

type Config struct {
	PublicOrigin  string
	InvitationTTL time.Duration
	// ExposeInviteLinks returns the raw invitation link to the inviter in
	// API responses. The link is the whole proof of acceptance, so handing
	// it to the inviter lets them accept as the invitee; only a deployment
	// with no email delivery, which has no other way to pass it on, should
	// set this.
	ExposeInviteLinks bool
}

type Service struct {
	memberships   application.MembershipRepository
	identities    application.IdentityRepository
	roles         application.RoleRepository
	teams         application.TeamRepository
	notifications application.NotificationDispatcher
	entitlements  application.Entitlements
	clock         application.Clock
	logger        *slog.Logger
	config        Config
}

func NewService(memberships application.MembershipRepository, identities application.IdentityRepository, roles application.RoleRepository, teams application.TeamRepository, notifications application.NotificationDispatcher, entitlements application.Entitlements, clock application.Clock, logger *slog.Logger, config Config) *Service {
	if config.InvitationTTL <= 0 {
		config.InvitationTTL = DefaultInvitationTTL
	}
	return &Service{memberships: memberships, identities: identities, roles: roles, teams: teams, notifications: notifications, entitlements: entitlements, clock: clock, logger: logger, config: config}
}

// seatQuota resolves the organization's seat limit for the repository to
// enforce inside the allocating transaction.
func (s *Service) seatQuota(ctx context.Context, organizationID domain.ID) (application.AllocationQuota, error) {
	limit, err := s.entitlements.Limit(ctx, application.LimitInput{OrganizationID: organizationID, Dimension: application.DimensionSeats})
	if err != nil {
		return application.AllocationQuota{}, err
	}
	return application.ResolvedQuota(limit), nil
}

// InvitationWithURL carries the one-time invite URL next to the invitation.
// The URL embeds the raw token and is never persisted or logged.
type InvitationWithURL struct {
	Invitation domain.Invitation
	InviteURL  string
}

func (s *Service) require(principal domain.Principal, permission domain.Permission, resource domain.Resource) error {
	if !domain.ManagementPrincipal(principal) {
		return domain.ErrForbidden
	}
	return authorization.Check(principal, permission, resource)
}

// actorIsOwner reports whether the acting membership holds the built-in
// owner role everywhere, read from its current assignments rather than
// the session so a revoked owner cannot keep granting owner.
func (s *Service) actorIsOwner(ctx context.Context, principal domain.Principal) (bool, error) {
	actor, err := s.identities.GetMembership(ctx, application.GetMembershipInput{
		OrganizationID: principal.OrganizationID, MembershipID: principal.MembershipID,
	})
	if err != nil {
		return false, err
	}
	return domain.HoldsRoleEverywhere(actor.Assignments, domain.RoleOwner), nil
}

// loadRole resolves a grant's role row. A role id the organization
// cannot see is a bad request, not a missing resource: the id came from
// the request body.
func (s *Service) loadRole(ctx context.Context, organizationID, roleID domain.ID) (domain.RoleRow, error) {
	role, err := s.roles.GetRole(ctx, application.GetRoleInput{OrganizationID: organizationID, RoleID: roleID})
	if errors.Is(err, domain.ErrNotFound) {
		return domain.RoleRow{}, fmt.Errorf("role %s does not exist: %w", roleID, domain.ErrInvalid)
	}
	return role, err
}

// evaluateGrants runs the no-escalation kernel over every proposed grant
// and reports whether any of them carries a dangerous permission, so the
// caller can demand fresh authentication before writing.
func (s *Service) evaluateGrants(ctx context.Context, principal domain.Principal, specs []domain.AssignmentSpec) (dangerous bool, err error) {
	if len(specs) == 0 {
		return false, fmt.Errorf("at least one grant is required: %w", domain.ErrInvalid)
	}
	actorIsOwner, err := s.actorIsOwner(ctx, principal)
	if err != nil {
		return false, err
	}
	for _, spec := range specs {
		role, err := s.loadRole(ctx, principal.OrganizationID, spec.RoleID)
		if err != nil {
			return false, err
		}
		if rejection := authorization.EvaluateGrant(authorization.GrantInput{
			Actor: principal, ActorIsOwner: actorIsOwner, Role: role, Scope: spec.Scope(),
		}); rejection != nil {
			return false, rejection.Err
		}
		dangerous = dangerous || role.Dangerous()
	}
	return dangerous, nil
}

// requireFresh applies the organization's re-authentication window and
// MFA policy to the actor's session, the ceremony every dangerous grant
// shares with deciding.
func (s *Service) requireFresh(ctx context.Context, principal domain.Principal) error {
	policy, err := s.identities.GetOrganizationPolicy(ctx, application.GetOrganizationPolicyInput{OrganizationID: principal.OrganizationID})
	if err != nil {
		return err
	}
	if rejection := authorization.RequireFresh(principal.Auth, policy.ApprovalReauthWindow, policy.MFAMode, s.clock.Now()); rejection != nil {
		return rejection.Err
	}
	return nil
}

// authorizeGrants is the shared gate for handing out roles: every grant
// passes the kernel, and a dangerous one needs fresh authentication.
func (s *Service) authorizeGrants(ctx context.Context, principal domain.Principal, specs []domain.AssignmentSpec) error {
	dangerous, err := s.evaluateGrants(ctx, principal, specs)
	if err != nil {
		return err
	}
	if dangerous {
		return s.requireFresh(ctx, principal)
	}
	return nil
}

func (s *Service) inviteURL(rawToken string) string {
	return strings.TrimRight(s.config.PublicOrigin, "/") + "/invite/" + rawToken
}

func (s *Service) notifyInvitation(ctx context.Context, organizationID domain.ID, email, inviteURL string, expiresAt time.Time) {
	organization, err := s.identities.GetOrganization(ctx, application.GetOrganizationInput{OrganizationID: organizationID})
	if err == nil {
		err = s.notifications.SendTransactional(ctx, application.SendTransactionalInput{
			Message: application.TransactionalMessage{
				Kind: application.TransactionalInvitation, OrganizationID: organizationID,
				OrganizationName: organization.Name, InviteURL: inviteURL, ExpiresAt: expiresAt,
			},
			Recipient: application.NotificationRecipient{Emails: []string{email}},
			Address:   email,
		})
	}
	if err != nil {
		s.logger.WarnContext(ctx, "invitation notification delivery failed; invitation remains available", "organization_id", organizationID, "error", err)
	}
}

// CreateInvitation invites an email with the given grants; with none, the
// invitee becomes a reviewer everywhere, the plain "can approve requests"
// choice.
func (s *Service) CreateInvitation(ctx context.Context, principal domain.Principal, email string, grants []domain.AssignmentSpec) (InvitationWithURL, error) {
	if err := s.require(principal, domain.PermissionMembershipInvite, domain.OrganizationResource()); err != nil {
		return InvitationWithURL{}, err
	}
	if len(grants) == 0 {
		grants = []domain.AssignmentSpec{{RoleID: domain.RoleReviewerID, ScopeKind: domain.ScopeOrganization}}
	}
	if err := s.authorizeGrants(ctx, principal, grants); err != nil {
		return InvitationWithURL{}, err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	invitationID := domain.ID(uuid.NewString())
	rawToken, tokenHash, err := authn.MintOpaqueToken("whi", invitationID)
	if err != nil {
		return InvitationWithURL{}, err
	}
	quota, err := s.seatQuota(ctx, principal.OrganizationID)
	if err != nil {
		return InvitationWithURL{}, err
	}
	now := s.clock.Now()
	invitation, err := s.memberships.CreateInvitation(ctx, application.CreateInvitationInput{
		OrganizationID: principal.OrganizationID, InvitationID: invitationID,
		Email: email, Grants: grants, InvitedByMembershipID: principal.MembershipID,
		TokenHash: tokenHash, ExpiresAt: now.Add(s.config.InvitationTTL), SeatQuota: quota, Now: now,
	})
	if err != nil {
		return InvitationWithURL{}, err
	}
	url := s.inviteURL(rawToken)
	s.notifyInvitation(ctx, principal.OrganizationID, email, url, invitation.ExpiresAt)
	return s.withLink(invitation, url), nil
}

// withLink attaches the raw link to the API result only where the
// deployment has no email delivery to carry it; everywhere else the
// invitee's mailbox is the only place the secret goes, so the inviter
// cannot accept on the invitee's behalf.
func (s *Service) withLink(invitation domain.Invitation, url string) InvitationWithURL {
	result := InvitationWithURL{Invitation: invitation}
	if s.config.ExposeInviteLinks {
		result.InviteURL = url
	}
	return result
}

func (s *Service) ListInvitations(ctx context.Context, principal domain.Principal, limit int) ([]domain.Invitation, error) {
	if err := s.require(principal, domain.PermissionMembershipInvite, domain.OrganizationResource()); err != nil {
		return nil, err
	}
	return s.memberships.ListInvitations(ctx, application.ListInvitationsInput{
		OrganizationID: principal.OrganizationID, Limit: limit,
	})
}

// ResendInvitation mints a fresh token for a pending invitation. The
// resender is handing out the invitation's grants again, so they pass the
// same kernel and freshness ceremony as the inviter did: an inviter who
// could not have made the invitation cannot re-arm it either.
func (s *Service) ResendInvitation(ctx context.Context, principal domain.Principal, invitationID domain.ID) (InvitationWithURL, error) {
	if err := s.require(principal, domain.PermissionMembershipInvite, domain.OrganizationResource()); err != nil {
		return InvitationWithURL{}, err
	}
	existing, err := s.memberships.GetInvitation(ctx, application.GetInvitationInput{
		OrganizationID: principal.OrganizationID, InvitationID: invitationID,
	})
	if err != nil {
		return InvitationWithURL{}, err
	}
	if err := s.authorizeGrants(ctx, principal, existing.Grants); err != nil {
		return InvitationWithURL{}, err
	}
	rawToken, tokenHash, err := authn.MintOpaqueToken("whi", invitationID)
	if err != nil {
		return InvitationWithURL{}, err
	}
	quota, err := s.seatQuota(ctx, principal.OrganizationID)
	if err != nil {
		return InvitationWithURL{}, err
	}
	now := s.clock.Now()
	invitation, err := s.memberships.RotateInvitationToken(ctx, application.RotateInvitationTokenInput{
		OrganizationID: principal.OrganizationID, InvitationID: invitationID,
		TokenHash: tokenHash, ExpiresAt: now.Add(s.config.InvitationTTL), SeatQuota: quota, Now: now,
	})
	if err != nil {
		return InvitationWithURL{}, err
	}
	url := s.inviteURL(rawToken)
	s.notifyInvitation(ctx, principal.OrganizationID, invitation.Email, url, invitation.ExpiresAt)
	return s.withLink(invitation, url), nil
}

func (s *Service) RevokeInvitation(ctx context.Context, principal domain.Principal, invitationID domain.ID) error {
	if err := s.require(principal, domain.PermissionMembershipInvite, domain.OrganizationResource()); err != nil {
		return err
	}
	return s.memberships.RevokeInvitation(ctx, application.RevokeInvitationInput{
		OrganizationID: principal.OrganizationID, InvitationID: invitationID, Now: s.clock.Now(),
	})
}

func (s *Service) ListMembers(ctx context.Context, principal domain.Principal, limit int) ([]application.MemberRecord, error) {
	if err := s.require(principal, domain.PermissionMembershipRead, domain.OrganizationResource()); err != nil {
		return nil, err
	}
	return s.memberships.ListMembers(ctx, application.ListMembersInput{
		OrganizationID: principal.OrganizationID, Limit: limit,
	})
}

// targetForMutation loads the target membership and enforces the shared
// guard rails: no self-mutation, and touching a member who holds the owner
// role everywhere requires holding it too.
func (s *Service) targetForMutation(ctx context.Context, principal domain.Principal, membershipID domain.ID) (domain.Membership, error) {
	if principal.MembershipID == membershipID {
		return domain.Membership{}, fmt.Errorf("you cannot change your own membership: %w", domain.ErrForbidden)
	}
	target, err := s.identities.GetMembership(ctx, application.GetMembershipInput{
		OrganizationID: principal.OrganizationID, MembershipID: membershipID,
	})
	if err != nil {
		return domain.Membership{}, err
	}
	if domain.HoldsRoleEverywhere(target.Assignments, domain.RoleOwner) {
		actorIsOwner, err := s.actorIsOwner(ctx, principal)
		if err != nil {
			return domain.Membership{}, err
		}
		if !actorIsOwner {
			return domain.Membership{}, fmt.Errorf("only an owner can change another owner: %w", domain.ErrForbidden)
		}
	}
	return target, nil
}

// UpdateMemberAssignments replaces a member's manual grants with the given
// set. Every grant passes the no-escalation kernel against the actor, a
// dangerous role needs fresh authentication, and directory-sourced
// assignments are left alone by the repository.
func (s *Service) UpdateMemberAssignments(ctx context.Context, principal domain.Principal, membershipID domain.ID, specs []domain.AssignmentSpec) (domain.Membership, error) {
	if err := s.require(principal, domain.PermissionPolicyWrite, domain.OrganizationResource()); err != nil {
		return domain.Membership{}, err
	}
	if _, err := s.targetForMutation(ctx, principal, membershipID); err != nil {
		return domain.Membership{}, err
	}
	if err := s.authorizeGrants(ctx, principal, specs); err != nil {
		return domain.Membership{}, err
	}
	if _, err := s.roles.ReplaceAssignments(ctx, application.ReplaceAssignmentsInput{
		OrganizationID: principal.OrganizationID, PrincipalKind: domain.PrincipalMembership, PrincipalID: membershipID,
		Assignments: specs, ActorMembershipID: principal.MembershipID, Now: s.clock.Now(),
	}); err != nil {
		return domain.Membership{}, err
	}
	return s.identities.GetMembership(ctx, application.GetMembershipInput{
		OrganizationID: principal.OrganizationID, MembershipID: membershipID,
	})
}

// RoleGrant is the grant a predefined role makes: the role everywhere in
// the organization. The agent's role is never a person's.
func RoleGrant(role domain.Role) (domain.AssignmentSpec, error) {
	def, ok := domain.BuiltInRole(role)
	if !ok || !domain.ValidRole(role) {
		return domain.AssignmentSpec{}, fmt.Errorf("role must be owner, admin, reviewer, or auditor: %w", domain.ErrInvalid)
	}
	return domain.AssignmentSpec{RoleID: def.ID, ScopeKind: domain.ScopeOrganization}, nil
}

// SetMemberRole gives a member one predefined role, replacing every grant
// they hold directly (custom permission policies included); what they
// inherit from teams and what the directory manages stay. It shares
// UpdateMemberAssignments' gate: the kernel, the owner rule, fresh
// authentication for a dangerous role, and the last-owner invariant.
func (s *Service) SetMemberRole(ctx context.Context, principal domain.Principal, membershipID domain.ID, role domain.Role) (domain.Membership, error) {
	grant, err := RoleGrant(role)
	if err != nil {
		return domain.Membership{}, err
	}
	return s.UpdateMemberAssignments(ctx, principal, membershipID, []domain.AssignmentSpec{grant})
}

// UpdateTeamAssignments replaces the roles a team holds with the given
// set; its members inherit them live. permission_policy.write is checked
// per grant at the grant's own scope by the kernel (its registry resource
// is the scope itself, so a lead bound to one agent type delegates within
// that type and nowhere else), after the coarse "holds it anywhere" gate.
// A dangerous role needs fresh authentication, as for a person. An empty
// set is allowed: a team with no roles is the default.
func (s *Service) UpdateTeamAssignments(ctx context.Context, principal domain.Principal, teamID domain.ID, specs []domain.AssignmentSpec) (domain.Team, error) {
	if principal.ActorType != domain.ActorHuman || principal.MembershipID == "" || !authorization.HoldsAnywhere(principal, domain.PermissionPolicyWrite) {
		return domain.Team{}, authorization.Denied{Permission: domain.PermissionPolicyWrite, Resource: domain.TeamResource(teamID)}
	}
	if len(specs) > 0 {
		if err := s.authorizeGrants(ctx, principal, specs); err != nil {
			return domain.Team{}, err
		}
	}
	if _, err := s.roles.ReplaceAssignments(ctx, application.ReplaceAssignmentsInput{
		OrganizationID: principal.OrganizationID, PrincipalKind: domain.PrincipalTeam, PrincipalID: teamID,
		Assignments: specs, ActorMembershipID: principal.MembershipID, Now: s.clock.Now(),
	}); err != nil {
		return domain.Team{}, err
	}
	team, _, err := s.teams.GetTeam(ctx, application.GetTeamInput{OrganizationID: principal.OrganizationID, TeamID: teamID})
	return team, err
}

// SetMemberStatus suspends, reactivates, or deprovisions a member.
// Suspending or deprovisioning someone an active escalation path still
// targets is refused with MembershipInEscalationPathError; force overrides
// that, the actor accepting that those targets now reach nobody.
func (s *Service) SetMemberStatus(ctx context.Context, principal domain.Principal, membershipID domain.ID, status domain.MembershipStatus, force bool) (domain.Membership, error) {
	if err := s.require(principal, domain.PermissionMembershipWrite, domain.OrganizationResource()); err != nil {
		return domain.Membership{}, err
	}
	if _, err := s.targetForMutation(ctx, principal, membershipID); err != nil {
		return domain.Membership{}, err
	}
	return s.memberships.SetMembershipStatus(ctx, application.SetMembershipStatusInput{
		OrganizationID: principal.OrganizationID, MembershipID: membershipID,
		Status:            status,
		RevokeSessions:    status == domain.MembershipDeprovisioned,
		Force:             force,
		ActorMembershipID: principal.MembershipID, Now: s.clock.Now(),
	})
}
