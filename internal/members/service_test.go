package members

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/entitlements"
	"github.com/withHumanAI/withHuman/internal/authorization"
)

const (
	organizationID = domain.ID("organization-1")
	actorID        = domain.ID("membership-actor")
	targetID       = domain.ID("membership-target")
	teamID         = domain.ID("team-payments")
	customRoleID   = domain.ID("role-custom")
)

var testNow = time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

type clockStub struct{ now time.Time }

func (c clockStub) Now() time.Time { return c.now }

// identityStub answers membership lookups by id and a fixed policy.
type identityStub struct {
	application.IdentityRepository
	memberships map[domain.ID]domain.Membership
	policy      domain.OrganizationPolicy
}

func (s *identityStub) GetMembership(_ context.Context, input application.GetMembershipInput) (domain.Membership, error) {
	membership, ok := s.memberships[input.MembershipID]
	if !ok || membership.OrganizationID != input.OrganizationID {
		return domain.Membership{}, domain.ErrNotFound
	}
	return membership, nil
}

func (s *identityStub) GetOrganizationPolicy(context.Context, application.GetOrganizationPolicyInput) (domain.OrganizationPolicy, error) {
	return s.policy, nil
}

func (s *identityStub) GetOrganization(_ context.Context, input application.GetOrganizationInput) (domain.Organization, error) {
	return domain.Organization{ID: input.OrganizationID, Name: "Acme"}, nil
}

// membershipStub records the invitation it was asked to create and
// serves one pending invitation for resend.
type membershipStub struct {
	application.MembershipRepository
	created *application.CreateInvitationInput
	pending domain.Invitation
	rotated *application.RotateInvitationTokenInput
}

func (s *membershipStub) GetInvitation(_ context.Context, input application.GetInvitationInput) (domain.Invitation, error) {
	if s.pending.ID == "" || s.pending.ID != input.InvitationID || s.pending.OrganizationID != input.OrganizationID {
		return domain.Invitation{}, domain.ErrNotFound
	}
	return s.pending, nil
}

func (s *membershipStub) RotateInvitationToken(_ context.Context, input application.RotateInvitationTokenInput) (domain.Invitation, error) {
	if err := input.Validate(); err != nil {
		return domain.Invitation{}, err
	}
	s.rotated = &input
	return s.pending, nil
}

func (s *membershipStub) CreateInvitation(_ context.Context, input application.CreateInvitationInput) (domain.Invitation, error) {
	if err := input.Validate(); err != nil {
		return domain.Invitation{}, err
	}
	s.created = &input
	return domain.Invitation{ID: input.InvitationID, OrganizationID: input.OrganizationID, Email: input.Email,
		Grants: input.Grants, Status: domain.InvitationPending, ExpiresAt: input.ExpiresAt}, nil
}

// roleStub serves the built-in rows plus one custom role and records
// assignment replacements.
type roleStub struct {
	application.RoleRepository
	custom   domain.RoleRow
	replaced *application.ReplaceAssignmentsInput
}

func (s *roleStub) GetRole(_ context.Context, input application.GetRoleInput) (domain.RoleRow, error) {
	if input.RoleID == s.custom.ID && s.custom.ID != "" {
		return s.custom, nil
	}
	for _, def := range domain.BuiltInRoles() {
		if def.ID == input.RoleID {
			return domain.RoleRow{ID: def.ID, Key: def.Key, Name: def.Name, Permissions: def.Permissions,
				BindableScopeKinds: def.BindableScopeKinds, BuiltIn: true}, nil
		}
	}
	return domain.RoleRow{}, domain.ErrNotFound
}

func (s *roleStub) ReplaceAssignments(_ context.Context, input application.ReplaceAssignmentsInput) ([]domain.RoleAssignment, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	s.replaced = &input
	return nil, nil
}

type notifierStub struct {
	application.NotificationDispatcher
}

func (notifierStub) SendTransactional(context.Context, application.SendTransactionalInput) error {
	return nil
}

// teamStub answers team reads for the one team assignments are written
// to.
type teamStub struct {
	application.TeamRepository
}

func (teamStub) GetTeam(_ context.Context, input application.GetTeamInput) (domain.Team, []application.TeamMemberRecord, error) {
	if input.TeamID != teamID {
		return domain.Team{}, nil, domain.ErrNotFound
	}
	return domain.Team{ID: teamID, OrganizationID: input.OrganizationID, Name: "Payments"}, nil, nil
}

type harness struct {
	service     *Service
	identities  *identityStub
	memberships *membershipStub
	roles       *roleStub
	teams       teamStub
}

func assignment(role domain.Role) domain.RoleAssignment {
	return domain.RoleAssignment{RoleKey: role, ScopeKind: domain.ScopeOrganization}
}

func newHarness() *harness {
	h := &harness{
		identities: &identityStub{
			memberships: map[domain.ID]domain.Membership{
				actorID:  {ID: actorID, OrganizationID: organizationID, Status: domain.MembershipActive, Assignments: []domain.RoleAssignment{assignment(domain.RoleAdmin)}},
				targetID: {ID: targetID, OrganizationID: organizationID, Status: domain.MembershipActive, Assignments: []domain.RoleAssignment{assignment(domain.RoleReviewer)}},
			},
			policy: domain.OrganizationPolicy{OrganizationID: organizationID, ApprovalReauthWindow: 15 * time.Minute, MFAMode: domain.MFAOptional},
		},
		memberships: &membershipStub{},
		roles: &roleStub{custom: domain.RoleRow{
			ID: customRoleID, OrganizationID: organizationID, Key: "release-manager", Name: "Release manager",
			Permissions:        []domain.Permission{domain.PermissionPipelineRead, domain.PermissionPipelineActivate},
			BindableScopeKinds: []domain.ScopeKind{domain.ScopeOrganization, domain.ScopeAgent},
		}},
	}
	h.service = NewService(h.memberships, h.identities, h.roles, h.teams, notifierStub{}, entitlements.NewUnlimited(), clockStub{testNow},
		slog.New(slog.NewTextHandler(io.Discard, nil)), Config{PublicOrigin: "https://app.example.test", ExposeInviteLinks: true})
	return h
}

// principal builds an acting principal holding the built-in role's grants
// and a session authenticated at the given age.
func principal(role domain.Role, sessionAge time.Duration) domain.Principal {
	return domain.Principal{
		ActorType: domain.ActorHuman, ActorID: "user-actor", OrganizationID: organizationID, MembershipID: actorID,
		Auth: domain.AuthContext{Method: "password", Assurance: "basic", AuthenticatedAt: testNow.Add(-sessionAge)},
	}.WithGrants(domain.GrantsForRoles([]domain.Role{role}))
}

func (h *harness) makeActorOwner() {
	actor := h.identities.memberships[actorID]
	actor.Assignments = []domain.RoleAssignment{assignment(domain.RoleOwner)}
	h.identities.memberships[actorID] = actor
}

func TestCreateInvitationDefaultsToReviewerEverywhere(t *testing.T) {
	h := newHarness()
	result, err := h.service.CreateInvitation(context.Background(), principal(domain.RoleAdmin, time.Minute), " Person@Example.com ", nil)
	if err != nil {
		t.Fatalf("CreateInvitation: %v", err)
	}
	want := []domain.AssignmentSpec{{RoleID: domain.RoleReviewerID, ScopeKind: domain.ScopeOrganization}}
	if h.memberships.created == nil || h.memberships.created.Email != "person@example.com" || len(h.memberships.created.Grants) != 1 || h.memberships.created.Grants[0] != want[0] {
		t.Fatalf("created = %#v", h.memberships.created)
	}
	if result.InviteURL == "" || result.Invitation.Grants[0] != want[0] {
		t.Fatalf("result = %#v", result)
	}
}

type guardStub struct{ refused string }

func (g guardStub) CheckInvitationEmail(_ context.Context, email string) error {
	if email == g.refused {
		return fmt.Errorf("use a permanent email address: %w", domain.ErrInvalid)
	}
	return nil
}

func TestInvitationsAskTheGuard(t *testing.T) {
	h := newHarness()
	h.service.config.Guard = guardStub{refused: "person@throwaway.example"}
	if _, err := h.service.CreateInvitation(context.Background(), principal(domain.RoleAdmin, time.Minute), " Person@Throwaway.example ", nil); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("refused address error = %v", err)
	}
	if h.memberships.created != nil {
		t.Fatal("a refused address was invited")
	}
	if _, err := h.service.CreateInvitation(context.Background(), principal(domain.RoleAdmin, time.Minute), "person@example.com", nil); err != nil {
		t.Fatalf("an admitted address: %v", err)
	}
	h.memberships.pending = domain.Invitation{
		ID: "invitation-1", OrganizationID: organizationID, Email: "person@throwaway.example", Status: domain.InvitationPending,
		Grants: []domain.AssignmentSpec{{RoleID: domain.RoleReviewerID, ScopeKind: domain.ScopeOrganization}},
	}
	if _, err := h.service.ResendInvitation(context.Background(), principal(domain.RoleAdmin, time.Minute), "invitation-1"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("resend to a refused address error = %v", err)
	}
}

func TestGrantingOwnerNeedsAnOwner(t *testing.T) {
	h := newHarness()
	owner := []domain.AssignmentSpec{{RoleID: domain.RoleOwnerID, ScopeKind: domain.ScopeOrganization}}
	// The actor's session carries owner grants, but their membership
	// says admin: the row wins.
	if _, err := h.service.CreateInvitation(context.Background(), principal(domain.RoleOwner, time.Minute), "a@example.com", owner); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin granting owner = %v, want forbidden", err)
	}
	if h.memberships.created != nil {
		t.Fatal("an invitation was created despite the refusal")
	}
	h.makeActorOwner()
	if _, err := h.service.CreateInvitation(context.Background(), principal(domain.RoleOwner, time.Minute), "a@example.com", owner); err != nil {
		t.Fatalf("owner granting owner: %v", err)
	}
}

// A custom role wider than the actor is refused by the kernel and the
// refusal reads as forbidden at the boundary.
func TestGrantExceedingTheActorIsForbidden(t *testing.T) {
	h := newHarness()
	actor := principal(domain.RoleAdmin, time.Minute)
	// Strip pipeline.activate from the actor while keeping the assign right.
	var grants []domain.Grant
	for _, grant := range actor.Grants {
		if grant.Permission != domain.PermissionPipelineActivate {
			grants = append(grants, grant)
		}
	}
	actor = actor.WithGrants(grants)
	spec := []domain.AssignmentSpec{{RoleID: customRoleID, ScopeKind: domain.ScopeOrganization}}
	_, err := h.service.UpdateMemberAssignments(context.Background(), actor, targetID, spec)
	var denied authorization.Denied
	if !errors.Is(err, domain.ErrForbidden) || !errors.As(err, &denied) || denied.Permission != domain.PermissionPipelineActivate {
		t.Fatalf("exceeding grant = %v, want denied on pipeline.activate", err)
	}
	if h.roles.replaced != nil {
		t.Fatal("assignments were replaced despite the refusal")
	}
	// A scope the role does not bind at is a bad request.
	team := []domain.AssignmentSpec{{RoleID: customRoleID, ScopeKind: domain.ScopeTeam, ScopeID: "team-1"}}
	if _, err := h.service.UpdateMemberAssignments(context.Background(), principal(domain.RoleAdmin, time.Minute), targetID, team); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unbindable scope = %v, want invalid", err)
	}
	unknown := []domain.AssignmentSpec{{RoleID: "role-missing", ScopeKind: domain.ScopeOrganization}}
	if _, err := h.service.UpdateMemberAssignments(context.Background(), principal(domain.RoleAdmin, time.Minute), targetID, unknown); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown role = %v, want invalid", err)
	}
}

func TestDangerousRoleRequiresFreshAuthentication(t *testing.T) {
	h := newHarness()
	admin := []domain.AssignmentSpec{{RoleID: domain.RoleAdminID, ScopeKind: domain.ScopeOrganization}}
	stale := principal(domain.RoleAdmin, time.Hour)
	if _, err := h.service.UpdateMemberAssignments(context.Background(), stale, targetID, admin); !errors.Is(err, domain.ErrAuthenticationStale) {
		t.Fatalf("stale session granting admin = %v, want authentication stale", err)
	}
	if h.roles.replaced != nil {
		t.Fatal("assignments were replaced with a stale session")
	}
	// The same stale session may still hand out a role with nothing
	// dangerous in it.
	reviewer := []domain.AssignmentSpec{{RoleID: domain.RoleReviewerID, ScopeKind: domain.ScopeOrganization}}
	if _, err := h.service.UpdateMemberAssignments(context.Background(), stale, targetID, reviewer); err != nil {
		t.Fatalf("stale session granting reviewer: %v", err)
	}
	h.roles.replaced = nil
	fresh := principal(domain.RoleAdmin, time.Minute)
	if _, err := h.service.UpdateMemberAssignments(context.Background(), fresh, targetID, admin); err != nil {
		t.Fatalf("fresh session granting admin: %v", err)
	}
	if h.roles.replaced == nil || h.roles.replaced.PrincipalID != targetID || h.roles.replaced.PrincipalKind != domain.PrincipalMembership ||
		h.roles.replaced.ActorMembershipID != actorID || len(h.roles.replaced.Assignments) != 1 || h.roles.replaced.Assignments[0] != admin[0] {
		t.Fatalf("replaced = %#v", h.roles.replaced)
	}
	// MFA policy applies the same way.
	h.identities.policy.MFAMode = domain.MFAEveryoneRequired
	if _, err := h.service.UpdateMemberAssignments(context.Background(), fresh, targetID, admin); !errors.Is(err, domain.ErrAuthenticationWeak) {
		t.Fatalf("basic assurance under MFA policy = %v, want authentication weak", err)
	}
}

func TestUpdateMemberAssignmentsGuardRails(t *testing.T) {
	h := newHarness()
	reviewer := []domain.AssignmentSpec{{RoleID: domain.RoleReviewerID, ScopeKind: domain.ScopeOrganization}}
	if _, err := h.service.UpdateMemberAssignments(context.Background(), principal(domain.RoleAdmin, time.Minute), actorID, reviewer); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("self edit = %v, want forbidden", err)
	}
	if _, err := h.service.UpdateMemberAssignments(context.Background(), principal(domain.RoleAdmin, time.Minute), targetID, nil); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty assignment set = %v, want invalid", err)
	}
	target := h.identities.memberships[targetID]
	target.Assignments = []domain.RoleAssignment{assignment(domain.RoleOwner)}
	h.identities.memberships[targetID] = target
	if _, err := h.service.UpdateMemberAssignments(context.Background(), principal(domain.RoleAdmin, time.Minute), targetID, reviewer); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin editing an owner = %v, want forbidden", err)
	}
	if _, err := h.service.UpdateMemberAssignments(context.Background(), principal(domain.RoleReviewer, time.Minute), targetID, reviewer); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("reviewer assigning = %v, want forbidden", err)
	}
}

// Team-held roles go through the same kernel as a person's, checked at
// each grant's own scope, and the team comes back with the write.
func TestUpdateTeamAssignments(t *testing.T) {
	h := newHarness()
	ctx := context.Background()
	refundAdmin := []domain.AssignmentSpec{{RoleID: domain.RoleAdminID, ScopeKind: domain.ScopeAgent, ScopeID: "refund-bot"}}

	team, err := h.service.UpdateTeamAssignments(ctx, principal(domain.RoleAdmin, time.Minute), teamID, refundAdmin)
	if err != nil {
		t.Fatalf("admin granting the team admin on refund-bot: %v", err)
	}
	if team.ID != teamID || team.Name != "Payments" {
		t.Fatalf("team = %#v", team)
	}
	if h.roles.replaced == nil || h.roles.replaced.PrincipalKind != domain.PrincipalTeam || h.roles.replaced.PrincipalID != teamID ||
		h.roles.replaced.ActorMembershipID != actorID || len(h.roles.replaced.Assignments) != 1 || h.roles.replaced.Assignments[0] != refundAdmin[0] {
		t.Fatalf("replaced = %#v", h.roles.replaced)
	}

	// An empty set clears the team's manual roles.
	h.roles.replaced = nil
	if _, err := h.service.UpdateTeamAssignments(ctx, principal(domain.RoleAdmin, time.Minute), teamID, nil); err != nil {
		t.Fatalf("clearing team roles: %v", err)
	}
	if h.roles.replaced == nil || len(h.roles.replaced.Assignments) != 0 {
		t.Fatalf("clearing did not reach the repository: %#v", h.roles.replaced)
	}

	// A reviewer holds permission_policy.write nowhere.
	h.roles.replaced = nil
	if _, err := h.service.UpdateTeamAssignments(ctx, principal(domain.RoleReviewer, time.Minute), teamID, refundAdmin); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("reviewer assigning = %v, want forbidden", err)
	}
	// An admin bound to release-bot may delegate within release-bot only.
	scopedAdmin := principal(domain.RoleAdmin, time.Minute)
	var grants []domain.Grant
	for _, grant := range scopedAdmin.Grants {
		grants = append(grants, domain.Grant{Permission: grant.Permission, ScopeKind: domain.ScopeAgent, ScopeID: "release-bot"})
	}
	scopedAdmin = scopedAdmin.WithGrants(grants)
	if _, err := h.service.UpdateTeamAssignments(ctx, scopedAdmin, teamID, refundAdmin); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("scoped admin delegating outside its type = %v, want forbidden", err)
	}
	if h.roles.replaced != nil {
		t.Fatal("assignments were replaced despite the refusal")
	}
	releaseAdmin := []domain.AssignmentSpec{{RoleID: domain.RoleAdminID, ScopeKind: domain.ScopeAgent, ScopeID: "release-bot"}}
	if _, err := h.service.UpdateTeamAssignments(ctx, scopedAdmin, teamID, releaseAdmin); err != nil {
		t.Fatalf("scoped admin delegating within its type: %v", err)
	}
	// Owner is not bindable at a team and a stale session cannot hand
	// out a dangerous role.
	owner := []domain.AssignmentSpec{{RoleID: domain.RoleOwnerID, ScopeKind: domain.ScopeTeam, ScopeID: string(teamID)}}
	if _, err := h.service.UpdateTeamAssignments(ctx, principal(domain.RoleAdmin, time.Minute), teamID, owner); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("owner at a team = %v, want invalid", err)
	}
	if _, err := h.service.UpdateTeamAssignments(ctx, principal(domain.RoleAdmin, time.Hour), teamID, refundAdmin); !errors.Is(err, domain.ErrAuthenticationStale) {
		t.Fatalf("stale session granting admin = %v, want authentication stale", err)
	}
	if _, err := h.service.UpdateTeamAssignments(ctx, principal(domain.RoleAdmin, time.Minute), "team-missing", refundAdmin); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown team = %v, want not found", err)
	}
}

// Where email delivery exists the raw link never reaches the inviter: the
// invitee's mailbox is the only holder of the acceptance secret.
func TestInviteLinkIsWithheldWhenEmailDelivers(t *testing.T) {
	h := newHarness()
	h.service = NewService(h.memberships, h.identities, h.roles, h.teams, notifierStub{}, entitlements.NewUnlimited(), clockStub{testNow},
		slog.New(slog.NewTextHandler(io.Discard, nil)), Config{PublicOrigin: "https://app.example.test"})
	result, err := h.service.CreateInvitation(context.Background(), principal(domain.RoleAdmin, time.Minute), "person@example.com", nil)
	if err != nil {
		t.Fatalf("CreateInvitation: %v", err)
	}
	if result.InviteURL != "" {
		t.Fatalf("invite link %q was returned to the inviter", result.InviteURL)
	}
}

// Resending re-arms the invitation's grants, so the resender must hold the
// same authority the inviter needed: an admin cannot re-arm an owner's
// invitation carrying dangerous permissions, and a dangerous grant still
// needs a fresh session.
func TestResendInvitationReappliesTheGrantCeremony(t *testing.T) {
	h := newHarness()
	h.roles.custom = domain.RoleRow{
		ID: customRoleID, OrganizationID: organizationID, Key: "org-deleter", Name: "Organization deleter",
		Permissions:        []domain.Permission{domain.PermissionOrganizationDelete},
		BindableScopeKinds: []domain.ScopeKind{domain.ScopeOrganization},
	}
	h.memberships.pending = domain.Invitation{
		ID: "invitation-1", OrganizationID: organizationID, Email: "newcto@example.com",
		Grants: []domain.AssignmentSpec{{RoleID: customRoleID, ScopeKind: domain.ScopeOrganization}}, Status: domain.InvitationPending,
	}
	// An admin holds membership.invite but not organization.delete.
	if _, err := h.service.ResendInvitation(context.Background(), principal(domain.RoleAdmin, time.Minute), "invitation-1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin re-arming an organization-delete invitation = %v, want forbidden", err)
	}
	if h.memberships.rotated != nil {
		t.Fatal("the token was rotated despite the refusal")
	}
	// An owner holds the permission, but the grant is dangerous, so a
	// stale session is refused too.
	h.makeActorOwner()
	if _, err := h.service.ResendInvitation(context.Background(), principal(domain.RoleOwner, time.Hour), "invitation-1"); !errors.Is(err, domain.ErrAuthenticationStale) {
		t.Fatalf("stale owner re-arming a dangerous grant = %v, want authentication_stale", err)
	}
	if h.memberships.rotated != nil {
		t.Fatal("the token was rotated despite the stale session")
	}
	result, err := h.service.ResendInvitation(context.Background(), principal(domain.RoleOwner, time.Minute), "invitation-1")
	if err != nil {
		t.Fatalf("fresh owner resend: %v", err)
	}
	if h.memberships.rotated == nil || result.InviteURL == "" {
		t.Fatalf("fresh owner resend did not rotate: %#v %#v", h.memberships.rotated, result)
	}
}
