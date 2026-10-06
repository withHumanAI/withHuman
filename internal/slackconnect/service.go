// Package slackconnect owns the organization-level Slack install: opening
// the OAuth door, completing it, and closing it. Delivery to Slack and the
// per-person identity link build on the connection it produces.
package slackconnect

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/authorization"
	"github.com/withHumanAI/withHuman/internal/secrets"
)

// ErrNotConfigured means this deployment has no Slack app credentials, so
// no organization can connect a workspace here. The HTTP layer maps it to
// its own code so the page can say so instead of showing a broken button.
var ErrNotConfigured = errors.New("slack is not configured on this deployment")

// ErrInstallExpired means the callback carried a state that was never
// issued, already redeemed, or past its window. It wraps ErrInvalid so
// generic handling still treats it as a bad request, while the callback
// can tell it apart from Slack itself refusing the exchange.
var ErrInstallExpired = fmt.Errorf("this Slack install has expired or was already used. Start again: %w", domain.ErrInvalid)

// ErrNoSlackMatch means none of a person's confirmed addresses belongs to
// an account in the connected workspace. It wraps ErrNotFound for generic
// handling; the HTTP layer gives it its own code so the page can tell
// "add the address you use in Slack" apart from "no workspace is
// connected".
var ErrNoSlackMatch = fmt.Errorf("no Slack account uses one of your verified email addresses: %w", domain.ErrNotFound)

// StateTTL bounds how long an install may sit between opening Slack and
// coming back. Long enough for an admin to read the consent screen, short
// enough that an abandoned state cannot be replayed a day later.
const StateTTL = 10 * time.Minute

const (
	callbackPath = "/auth/slack/callback"
	tokenLabel   = "slack_bot_token"
)

type Config struct {
	PublicOrigin  string
	AppID         string
	SigningSecret string
}

type Service struct {
	dispatcher  application.SlackEventDispatcher
	connector   application.SlackConnector
	messenger   application.SlackMessenger
	connections application.SlackConnectionRepository
	contacts    application.ContactMethodRepository
	sealer      *secrets.Sealer
	clock       application.Clock
	logger      *slog.Logger
	config      Config
}

func (s *Service) WithEventDispatcher(dispatcher application.SlackEventDispatcher) *Service {
	s.dispatcher = dispatcher
	return s
}

// NewService returns a service that refuses every operation with
// ErrNotConfigured when connector or sealer are nil, so composition roots
// can always construct one and the page can explain the absence.
func NewService(connector application.SlackConnector, messenger application.SlackMessenger, connections application.SlackConnectionRepository, contacts application.ContactMethodRepository, sealer *secrets.Sealer, clock application.Clock, logger *slog.Logger, config Config) *Service {
	return &Service{connector: connector, messenger: messenger, connections: connections, contacts: contacts, sealer: sealer, clock: clock, logger: logger, config: config}
}

// SyncResult reports what a member match found.
type SyncResult struct {
	Members int
	Linked  int
}

// SyncMembers matches every active member against the workspace by their
// verified addresses and links the ones it finds, the way incident.io
// pre-populates Slack for people whose emails match. Unmatched members
// keep whatever link they had; a person who left Slack is not unlinked
// here, because a missing lookup and a transient Slack error look alike.
func (s *Service) SyncMembers(ctx context.Context, principal domain.Principal) (SyncResult, error) {
	if err := s.requireManager(principal); err != nil {
		return SyncResult{}, err
	}
	return s.syncOrganization(ctx, principal.OrganizationID)
}

func (s *Service) syncOrganization(ctx context.Context, organizationID domain.ID) (SyncResult, error) {
	connection, token, err := s.activeConnection(ctx, organizationID)
	if err != nil {
		return SyncResult{}, err
	}
	members, err := s.contacts.ListMemberAddresses(ctx, application.ListMemberAddressesInput{OrganizationID: organizationID})
	if err != nil {
		return SyncResult{}, err
	}
	result := SyncResult{Members: len(members)}
	for _, member := range members {
		_, err := s.linkMember(ctx, token, connection, member)
		switch {
		case errors.Is(err, ErrNoSlackMatch):
			continue
		case errors.Is(err, domain.ErrConflict):
			s.logger.WarnContext(ctx, "slack account already linked to another member", "user_id", member.UserID)
			continue
		case err != nil:
			if errors.Is(err, application.ErrSlackRevoked) {
				_, _ = s.disconnectRevoked(ctx, connection)
			}
			return result, err
		}
		result.Linked++
	}
	return result, nil
}

// Workspace tells any member whether their organization has a Slack
// workspace connected, and its name, so the Notifications page can say
// whether linking is possible yet or an admin has to install withHuman
// first. Only the name leaves here; the rest of the connection stays
// behind connection.read. An unconfigured deployment has nothing to
// connect, which reads the same as not connected.
func (s *Service) Workspace(ctx context.Context, principal domain.Principal) (string, bool, error) {
	if principal.ActorType != domain.ActorHuman || principal.MembershipID == "" {
		return "", false, domain.ErrForbidden
	}
	if !s.Configured() {
		return "", false, nil
	}
	connection, err := s.connections.GetSlackConnection(ctx, application.GetSlackConnectionInput{OrganizationID: principal.OrganizationID})
	if errors.Is(err, domain.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if connection.Status != "connected" {
		return "", false, nil
	}
	return connection.TeamName, true, nil
}

// LinkSelf matches the calling person against the workspace by their own
// verified addresses, so joining or confirming an address after the
// install does not leave them waiting for an admin to sync members. Any
// member may do it: the match is by their own confirmed addresses, so the
// only account it can link is theirs.
func (s *Service) LinkSelf(ctx context.Context, principal domain.Principal) (domain.ContactMethod, error) {
	if err := s.requireMember(principal); err != nil {
		return domain.ContactMethod{}, err
	}
	connection, token, err := s.activeConnection(ctx, principal.OrganizationID)
	if err != nil {
		return domain.ContactMethod{}, err
	}
	members, err := s.contacts.ListMemberAddresses(ctx, application.ListMemberAddressesInput{OrganizationID: principal.OrganizationID, UserID: principal.ActorID})
	if err != nil {
		return domain.ContactMethod{}, err
	}
	if len(members) == 0 {
		return domain.ContactMethod{}, domain.ErrForbidden
	}
	method, err := s.linkMember(ctx, token, connection, members[0])
	if errors.Is(err, application.ErrSlackRevoked) {
		_, _ = s.disconnectRevoked(ctx, connection)
	}
	return method, err
}

// linkMember looks one member up by each of their addresses and links the
// first Slack account found. ErrNoSlackMatch when none matches; the
// repository's ErrConflict when the account already belongs to another
// member.
func (s *Service) linkMember(ctx context.Context, token string, connection domain.SlackConnection, member application.MemberAddresses) (domain.ContactMethod, error) {
	for _, address := range member.Emails {
		user, err := s.messenger.LookupUserByEmail(ctx, token, address)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return domain.ContactMethod{}, err
		}
		label := "@" + user.Handle
		if user.DisplayName != "" {
			label = user.DisplayName + " (@" + user.Handle + ")"
		}
		return s.contacts.LinkOrganizationContactMethod(ctx, application.LinkOrganizationContactMethodInput{
			MethodID: domain.ID(uuid.NewString()), UserID: member.UserID, OrganizationID: connection.OrganizationID, SlackInstallationID: connection.InstallationID,
			Kind: domain.ContactSlack, ExternalID: user.ID, Label: label, Now: s.clock.Now(),
		})
	}
	return domain.ContactMethod{}, ErrNoSlackMatch
}

func (s *Service) Configured() bool { return s.connector != nil && s.sealer != nil }

func (s *Service) requireManager(principal domain.Principal) error {
	if principal.MembershipID == "" {
		return domain.ErrForbidden
	}
	return s.require(principal, domain.PermissionConnectionWrite, domain.OrganizationResource())
}

// requireMember admits any signed-in member of the organization: the
// self-link touches nothing but the caller's own contact methods.
func (s *Service) requireMember(principal domain.Principal) error {
	if principal.ActorType != domain.ActorHuman || principal.MembershipID == "" || principal.ActorID == "" {
		return domain.ErrForbidden
	}
	if !s.Configured() {
		return ErrNotConfigured
	}
	return nil
}

func (s *Service) require(principal domain.Principal, permission domain.Permission, resource domain.Resource) error {
	if principal.ActorType != domain.ActorHuman {
		return domain.ErrForbidden
	}
	if err := authorization.Check(principal, permission, resource); err != nil {
		return err
	}
	if !s.Configured() {
		return ErrNotConfigured
	}
	return nil
}

func (s *Service) callbackURL() string {
	return strings.TrimRight(s.config.PublicOrigin, "/") + callbackPath
}

// Get reports the organization's connection, or ErrNotFound when none.
// Reading needs no configuration: a deployment that later loses its Slack
// credentials should still show what was connected.
func (s *Service) Get(ctx context.Context, principal domain.Principal) (domain.SlackConnection, error) {
	if err := s.require(principal, domain.PermissionConnectionRead, domain.OrganizationResource()); err != nil {
		return domain.SlackConnection{}, err
	}
	return s.connections.GetSlackConnection(ctx, application.GetSlackConnectionInput{OrganizationID: principal.OrganizationID})
}

// Begin mints a state bound to this organization and the admin starting
// the install, and returns where to send them. Only the hash is stored:
// the state itself travels through Slack and back in the browser.
func (s *Service) Begin(ctx context.Context, principal domain.Principal) (string, error) {
	if err := s.requireManager(principal); err != nil {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	state := base64.RawURLEncoding.EncodeToString(raw)
	now := s.clock.Now()
	if err := s.connections.CreateSlackInstallState(ctx, application.CreateSlackInstallStateInput{
		StateHash: hashState(state), OrganizationID: principal.OrganizationID,
		MembershipID: principal.MembershipID, ExpiresAt: now.Add(StateTTL), Now: now,
	}); err != nil {
		return "", err
	}
	return s.connector.AuthorizeURL(state, s.callbackURL()), nil
}

// Complete finishes an install from the callback. The browser arrives
// from slack.com, which is a cross-site navigation, so the strict session
// cookie is not sent and no principal is available. The state decides:
// it was minted for one organization by one admin who held
// connection.manage at that moment, it is redeemed exactly once, and it
// expires. A callback URL that leaks cannot attach a workspace to any
// organization but the one whose admin began the install.
func (s *Service) Complete(ctx context.Context, state, code string) (domain.SlackConnection, error) {
	if !s.Configured() {
		return domain.SlackConnection{}, ErrNotConfigured
	}
	if state == "" || code == "" {
		return domain.SlackConnection{}, fmt.Errorf("the Slack callback is missing its state or code: %w", domain.ErrInvalid)
	}
	now := s.clock.Now()
	began, err := s.connections.ConsumeSlackInstallState(ctx, application.ConsumeSlackInstallStateInput{StateHash: hashState(state), Now: now})
	if errors.Is(err, domain.ErrNotFound) {
		return domain.SlackConnection{}, ErrInstallExpired
	}
	if err != nil {
		return domain.SlackConnection{}, err
	}
	installation, err := s.connector.Exchange(ctx, application.SlackExchangeInput{Code: code, RedirectURL: s.callbackURL()})
	if err != nil {
		return domain.SlackConnection{}, err
	}
	var connection domain.SlackConnection
	err = s.connections.WithSlackLock(ctx, application.SlackLockInput{OrganizationID: began.OrganizationID, TeamID: installation.TeamID, Work: func(ctx context.Context) error {
		var previous *application.SlackConnectionVersion
		owner, err := s.connections.FindSlackWorkspace(ctx, application.FindSlackWorkspaceInput{TeamID: installation.TeamID})
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		if err == nil && owner.OrganizationID != began.OrganizationID {
			oldToken, err := s.connections.OpenSlackBotToken(ctx, application.GetSlackConnectionInput{OrganizationID: owner.OrganizationID, InstallationID: owner.InstallationID})
			if err != nil {
				return ErrCheckUnavailable
			}
			_, err = s.connector.CheckToken(ctx, application.SlackTokenInput{BotToken: oldToken})
			if err == nil {
				return domain.ErrConflict
			}
			if !errors.Is(err, application.ErrSlackRevoked) {
				return ErrCheckUnavailable
			}
			previous = &application.SlackConnectionVersion{OrganizationID: owner.OrganizationID, InstallationID: owner.InstallationID}
		}
		identity, err := s.connector.CheckToken(ctx, application.SlackTokenInput{BotToken: installation.BotToken})
		if err != nil {
			return ErrCheckUnavailable
		}
		if identity.TeamID != installation.TeamID || identity.BotUserID != installation.BotUserID {
			return ErrCheckUnavailable
		}
		sealed, err := s.sealer.Seal(tokenLabel, installation.BotToken)
		if err != nil {
			return err
		}
		connection, err = s.connections.UpsertSlackConnection(ctx, application.UpsertSlackConnectionInput{
			OrganizationID: began.OrganizationID, Installation: installation, SealedBotToken: sealed,
			InstalledByMembershipID: began.MembershipID, Now: s.clock.Now(), InstallationID: domain.ID(uuid.NewString()), PreviousOwner: previous,
		})
		return err
	}})
	// OAuth can return the token already used by another installation. A failed
	// local save must never revoke that shared provider authorization.
	if err != nil {
		return domain.SlackConnection{}, err
	}
	// Pre-populate members' Slack identities so most people find theirs
	// already there. Matching is best effort: the connection stands even
	// if Slack is slow to answer, and Sync members can be run again.
	s.dispatchEvents(ctx, "installation/"+string(connection.InstallationID))
	if result, err := s.syncOrganization(ctx, began.OrganizationID); err != nil {
		s.logger.WarnContext(ctx, "slack member match incomplete after install", "team_id", connection.TeamID, "linked", result.Linked, "error", err)
	}
	return connection, nil
}

// Disconnect removes the connection and revokes the token at Slack. The
// local record goes first: a revoke that fails leaves a token that is
// unusable here regardless, and is logged for an operator to finish.
func (s *Service) Disconnect(ctx context.Context, principal domain.Principal) error {
	if err := s.requireManager(principal); err != nil {
		return err
	}
	connection, err := s.connections.GetSlackConnection(ctx, application.GetSlackConnectionInput{OrganizationID: principal.OrganizationID})
	if errors.Is(err, domain.ErrNotFound) || (err == nil && connection.Status == "disconnected") {
		return nil
	}
	if err != nil {
		return err
	}
	return s.connections.WithSlackLock(ctx, application.SlackLockInput{OrganizationID: principal.OrganizationID, TeamID: connection.TeamID, Work: func(ctx context.Context) error {
		token, err := s.connections.DeleteSlackConnection(ctx, application.DeleteSlackConnectionInput{
			OrganizationID: principal.OrganizationID, InstallationID: connection.InstallationID, ActorMembershipID: principal.MembershipID, Now: s.clock.Now(),
		})
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ErrConflict
		}
		if err != nil {
			return err
		}
		s.dispatchEvents(ctx, "disconnection/"+string(connection.InstallationID))
		if token != "" {
			if err := s.connector.Revoke(ctx, token); err != nil {
				s.logger.WarnContext(ctx, "Slack token not revoked after disconnect", "team_id", connection.TeamID, "error", err)
			}
		}
		return nil
	}})
}

func hashState(state string) []byte {
	sum := sha256.Sum256([]byte(state))
	return sum[:]
}
