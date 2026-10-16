// Package discordconnect owns the organization-level Discord install and
// the per-person identity link: opening each OAuth door, completing it,
// and closing it. Delivery to Discord builds on the connection and the
// links it produces.
package discordconnect

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/authorization"
	"github.com/withHumanAI/withHuman/internal/oauthreturn"
)

// ErrNotConfigured means this deployment has no Discord application
// credentials, so no organization can connect a server here. The HTTP
// layer maps it to its own code so the page can say so.
var ErrNotConfigured = errors.New("discord is not configured on this deployment")

// ErrNotConnected means this organization must install the bot in a server
// before a member can link their personal account.
var ErrNotConnected = fmt.Errorf("connect your organization's Discord server first: %w", domain.ErrNotFound)

// ErrStateExpired means a callback carried a state that was never issued,
// was minted for the other flow, was already redeemed, or is past its
// window. It wraps ErrInvalid so generic handling treats it as a bad
// request, while the callback can tell it apart from Discord refusing the
// exchange.
var ErrStateExpired = fmt.Errorf("this Discord flow has expired or was already used. Start again: %w", domain.ErrInvalid)

// ErrNotGuildMember means the account a person authorized is not in the
// organization's connected server. A bot can only message people it
// shares a server with, so the link would be a dead end.
var ErrNotGuildMember = fmt.Errorf("that Discord account is not in your organization's server: %w", domain.ErrForbidden)

// StateTTL bounds how long a round trip may sit between opening Discord
// and coming back.
const StateTTL = 10 * time.Minute

const (
	installCallbackPath = "/auth/discord/callback"
	linkCallbackPath    = "/auth/discord/link/callback"
)

type Config struct {
	PublicOrigin string
}

type Service struct {
	connector   application.DiscordConnector
	messenger   application.DiscordMessenger
	connections application.DiscordConnectionRepository
	contacts    application.ContactMethodRepository
	clock       application.Clock
	logger      *slog.Logger
	config      Config
}

// NewService returns a service that refuses every operation with
// ErrNotConfigured when the connector is nil, so composition roots can
// always construct one and the pages can explain the absence.
func NewService(connector application.DiscordConnector, messenger application.DiscordMessenger, connections application.DiscordConnectionRepository, contacts application.ContactMethodRepository, clock application.Clock, logger *slog.Logger, config Config) *Service {
	return &Service{connector: connector, messenger: messenger, connections: connections, contacts: contacts, clock: clock, logger: logger, config: config}
}

func (s *Service) Configured() bool { return s.connector != nil && s.messenger != nil }

func (s *Service) requireManager(principal domain.Principal) error {
	if principal.MembershipID == "" {
		return domain.ErrForbidden
	}
	return s.require(principal, domain.PermissionConnectionWrite, domain.OrganizationResource())
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

func (s *Service) requireMember(principal domain.Principal) error {
	if principal.ActorType != domain.ActorHuman || principal.MembershipID == "" || principal.ActorID == "" {
		return domain.ErrForbidden
	}
	if !s.Configured() {
		return ErrNotConfigured
	}
	return nil
}

func (s *Service) callbackURL(path string) string {
	return strings.TrimRight(s.config.PublicOrigin, "/") + path
}

// Get reports the organization's connection, or ErrNotFound when none.
func (s *Service) Get(ctx context.Context, principal domain.Principal) (domain.DiscordConnection, error) {
	if err := s.require(principal, domain.PermissionConnectionRead, domain.OrganizationResource()); err != nil {
		return domain.DiscordConnection{}, err
	}
	return s.connections.GetDiscordConnection(ctx, application.GetDiscordConnectionInput{OrganizationID: principal.OrganizationID})
}

// Server exposes only the connected server's name to members, without
// granting access to the administrative connection details.
func (s *Service) Server(ctx context.Context, principal domain.Principal) (string, bool, error) {
	if principal.ActorType != domain.ActorHuman || principal.MembershipID == "" || principal.ActorID == "" {
		return "", false, domain.ErrForbidden
	}
	if !s.Configured() {
		return "", false, nil
	}
	connection, err := s.connections.GetDiscordConnection(ctx, application.GetDiscordConnectionInput{OrganizationID: principal.OrganizationID})
	if errors.Is(err, domain.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return connection.GuildName, true, nil
}

// Begin mints an install state bound to this organization and the admin
// starting it, and returns where to send them. Only the hash is stored:
// the state itself travels through Discord and back in the browser.
func (s *Service) Begin(ctx context.Context, principal domain.Principal, returnTo oauthreturn.Page) (string, error) {
	if err := s.requireManager(principal); err != nil {
		return "", err
	}
	state, err := s.mintState(ctx, application.DiscordPurposeInstall, principal, returnTo)
	if err != nil {
		return "", err
	}
	return s.connector.AuthorizeInstallURL(state, s.callbackURL(installCallbackPath)), nil
}

// Complete finishes an install from the callback. The browser arrives
// from discord.com, a cross-site navigation without the strict session
// cookie, so the state decides: minted for one organization by one admin
// who held connection.manage, redeemed exactly once, and expiring. The
// guild comes from the token exchange rather than the callback query,
// because only the exchange proves where the bot actually went.
func (s *Service) Complete(ctx context.Context, state, code, guildID string) (domain.DiscordConnection, error) {
	if !s.Configured() {
		return domain.DiscordConnection{}, ErrNotConfigured
	}
	if state == "" || code == "" {
		return domain.DiscordConnection{}, fmt.Errorf("the Discord callback is missing its state or code: %w", domain.ErrInvalid)
	}
	began, err := s.consumeState(ctx, application.DiscordPurposeInstall, state)
	if err != nil {
		return domain.DiscordConnection{}, err
	}
	installation, err := s.connector.ExchangeInstall(ctx, application.DiscordExchangeInput{Code: code, RedirectURL: s.callbackURL(installCallbackPath)})
	if err != nil {
		return domain.DiscordConnection{}, err
	}
	if guildID != "" && guildID != installation.GuildID {
		s.logger.WarnContext(ctx, "discord callback guild differs from the exchanged guild", "callback_guild_id", guildID, "guild_id", installation.GuildID)
	}
	connection, err := s.connections.UpsertDiscordConnection(ctx, application.UpsertDiscordConnectionInput{
		OrganizationID: began.OrganizationID, Installation: installation,
		InstalledByMembershipID: began.MembershipID, Now: s.clock.Now(),
	})
	if err != nil {
		// The bot is in a guild nobody here owns; leave rather than sit in
		// a server that another organization may hold.
		if leaveErr := s.connector.LeaveGuild(ctx, installation.GuildID); leaveErr != nil {
			s.logger.WarnContext(ctx, "bot could not leave an unclaimed Discord guild", "guild_id", installation.GuildID, "error", leaveErr)
		}
		return domain.DiscordConnection{}, err
	}
	return connection, nil
}

// Disconnect removes the connection and every member's link, then makes
// the bot leave the guild. The local record goes first: a leave that
// fails leaves a bot that no longer delivers anything here, and is logged
// for an operator to finish.
func (s *Service) Disconnect(ctx context.Context, principal domain.Principal) error {
	if err := s.requireManager(principal); err != nil {
		return err
	}
	guildID, err := s.connections.DeleteDiscordConnection(ctx, application.DeleteDiscordConnectionInput{
		OrganizationID: principal.OrganizationID, ActorMembershipID: principal.MembershipID, Now: s.clock.Now(),
	})
	if err != nil {
		return err
	}
	if err := s.connector.LeaveGuild(ctx, guildID); err != nil {
		s.logger.WarnContext(ctx, "bot did not leave the Discord guild after disconnect", "guild_id", guildID, "error", err)
	}
	return nil
}

// BeginLink mints a link state bound to the calling person in this
// organization. It needs a connected server to check membership against,
// so without one it answers ErrNotConnected and the page explains.
func (s *Service) BeginLink(ctx context.Context, principal domain.Principal, returnTo oauthreturn.Page) (string, error) {
	if err := s.requireMember(principal); err != nil {
		return "", err
	}
	if _, err := s.connections.GetDiscordConnection(ctx, application.GetDiscordConnectionInput{OrganizationID: principal.OrganizationID}); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", ErrNotConnected
		}
		return "", err
	}
	state, err := s.mintState(ctx, application.DiscordPurposeLink, principal, returnTo)
	if err != nil {
		return "", err
	}
	return s.connector.AuthorizeLinkURL(state, s.callbackURL(linkCallbackPath)), nil
}

// CompleteLink finishes a person's link from the callback. Discord vouches
// for the account; the bot then confirms the account is in the
// organization's server, since otherwise no message could ever reach it.
func (s *Service) CompleteLink(ctx context.Context, state, code string) (domain.ContactMethod, error) {
	if !s.Configured() {
		return domain.ContactMethod{}, ErrNotConfigured
	}
	if state == "" || code == "" {
		return domain.ContactMethod{}, fmt.Errorf("the Discord callback is missing its state or code: %w", domain.ErrInvalid)
	}
	began, err := s.consumeState(ctx, application.DiscordPurposeLink, state)
	if err != nil {
		return domain.ContactMethod{}, err
	}
	user, err := s.connector.IdentifyUser(ctx, application.DiscordExchangeInput{Code: code, RedirectURL: s.callbackURL(linkCallbackPath)})
	if err != nil {
		return domain.ContactMethod{}, err
	}
	connection, err := s.connections.GetDiscordConnection(ctx, application.GetDiscordConnectionInput{OrganizationID: began.OrganizationID})
	if err != nil {
		return domain.ContactMethod{}, err
	}
	member, err := s.messenger.GetGuildMember(ctx, application.DiscordGuildMemberInput{GuildID: connection.GuildID, UserID: user.ID})
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ContactMethod{}, ErrNotGuildMember
	}
	if err != nil {
		return domain.ContactMethod{}, err
	}
	return s.contacts.LinkOrganizationContactMethod(ctx, application.LinkOrganizationContactMethodInput{
		MethodID: domain.ID(uuid.NewString()), UserID: began.UserID, OrganizationID: began.OrganizationID,
		Kind: domain.ContactDiscord, ExternalID: user.ID, Label: label(member, user), Now: s.clock.Now(),
	})
}

// Unlink removes the caller's Discord link in this organization. It is
// self-service: the person is acting on their own account.
func (s *Service) Unlink(ctx context.Context, principal domain.Principal) error {
	if principal.ActorType != domain.ActorHuman || principal.ActorID == "" {
		return domain.ErrForbidden
	}
	methods, err := s.contacts.ListContactMethods(ctx, application.ListContactMethodsInput{UserID: principal.ActorID, OrganizationID: principal.OrganizationID})
	if err != nil {
		return err
	}
	for _, method := range methods {
		if method.Kind == domain.ContactDiscord && method.OrganizationID == principal.OrganizationID {
			return s.contacts.DeleteContactMethod(ctx, application.DeleteContactMethodInput{MethodID: method.ID, UserID: principal.ActorID})
		}
	}
	return domain.ErrNotFound
}

// mintState stores the state's hash for one purpose. The state itself
// names the page the callback returns to.
func (s *Service) mintState(ctx context.Context, purpose application.DiscordOAuthPurpose, principal domain.Principal, returnTo oauthreturn.Page) (string, error) {
	state, err := oauthreturn.NewState(returnTo)
	if err != nil {
		return "", err
	}
	now := s.clock.Now()
	if err := s.connections.CreateDiscordOAuthState(ctx, application.CreateDiscordOAuthStateInput{
		StateHash: hashState(state), Purpose: purpose, OrganizationID: principal.OrganizationID,
		MembershipID: principal.MembershipID, UserID: principal.ActorID, ExpiresAt: now.Add(StateTTL), Now: now,
	}); err != nil {
		return "", err
	}
	return state, nil
}

func (s *Service) consumeState(ctx context.Context, purpose application.DiscordOAuthPurpose, state string) (application.DiscordOAuthState, error) {
	began, err := s.connections.ConsumeDiscordOAuthState(ctx, application.ConsumeDiscordOAuthStateInput{
		StateHash: hashState(state), Purpose: purpose, Now: s.clock.Now(),
	})
	if errors.Is(err, domain.ErrNotFound) {
		return application.DiscordOAuthState{}, ErrStateExpired
	}
	return began, err
}

// label is what the person sees next to their link: the name they go by
// in the server, with the handle for disambiguation.
func label(member, user application.DiscordUser) string {
	handle := "@" + user.Username
	display := member.DisplayName
	if display == "" {
		display = user.DisplayName
	}
	if display == "" || display == user.Username {
		return handle
	}
	return display + " (" + handle + ")"
}

func hashState(state string) []byte {
	sum := sha256.Sum256([]byte(state))
	return sum[:]
}
