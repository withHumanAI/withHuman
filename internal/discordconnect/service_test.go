package discordconnect

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/oauthreturn"
)

type fakeConnector struct {
	exchangeErr error
	identifyErr error
	guild       application.DiscordInstallation
	user        application.DiscordUser
	members     map[string]application.DiscordUser
	left        []string
	sent        []application.DiscordDirectMessageInput
}

func (f *fakeConnector) AuthorizeInstallURL(state, redirect string) string {
	return "https://discord.example/oauth2/authorize?scope=bot&state=" + state + "&redirect_uri=" + redirect
}

func (f *fakeConnector) AuthorizeLinkURL(state, redirect string) string {
	return "https://discord.example/oauth2/authorize?scope=identify&state=" + state + "&redirect_uri=" + redirect
}

func (f *fakeConnector) ExchangeInstall(context.Context, application.DiscordExchangeInput) (application.DiscordInstallation, error) {
	if f.exchangeErr != nil {
		return application.DiscordInstallation{}, f.exchangeErr
	}
	return f.guild, nil
}

func (f *fakeConnector) IdentifyUser(context.Context, application.DiscordExchangeInput) (application.DiscordUser, error) {
	if f.identifyErr != nil {
		return application.DiscordUser{}, f.identifyErr
	}
	return f.user, nil
}

func (f *fakeConnector) LeaveGuild(_ context.Context, guildID string) error {
	f.left = append(f.left, guildID)
	return nil
}

func (f *fakeConnector) GetGuildMember(_ context.Context, input application.DiscordGuildMemberInput) (application.DiscordUser, error) {
	if member, ok := f.members[input.GuildID+"/"+input.UserID]; ok {
		return member, nil
	}
	return application.DiscordUser{}, domain.ErrNotFound
}

func (f *fakeConnector) SendDirectMessage(_ context.Context, input application.DiscordDirectMessageInput) error {
	f.sent = append(f.sent, input)
	return nil
}

type storedState struct {
	purpose application.DiscordOAuthPurpose
	state   application.DiscordOAuthState
	expires time.Time
}

type memoryRepository struct {
	states     map[string]storedState
	connection *domain.DiscordConnection
	upsertErr  error
	methods    []domain.ContactMethod
	links      []application.LinkOrganizationContactMethodInput
	deleted    []domain.ID
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{states: map[string]storedState{}}
}

func (m *memoryRepository) ListContactMethods(_ context.Context, input application.ListContactMethodsInput) ([]domain.ContactMethod, error) {
	var out []domain.ContactMethod
	for _, method := range m.methods {
		if method.UserID == input.UserID && (method.OrganizationID == "" || method.OrganizationID == input.OrganizationID) {
			out = append(out, method)
		}
	}
	return out, nil
}
func (m *memoryRepository) CreateContactMethod(context.Context, application.CreateContactMethodInput) (domain.ContactMethod, error) {
	return domain.ContactMethod{}, nil
}
func (m *memoryRepository) SetContactMethodChallenge(context.Context, application.SetContactMethodChallengeInput) (domain.ContactMethod, error) {
	return domain.ContactMethod{}, nil
}
func (m *memoryRepository) VerifyContactMethod(context.Context, application.VerifyContactMethodInput) (domain.ContactMethod, error) {
	return domain.ContactMethod{}, nil
}
func (m *memoryRepository) DeleteContactMethod(_ context.Context, input application.DeleteContactMethodInput) error {
	m.deleted = append(m.deleted, input.MethodID)
	return nil
}
func (m *memoryRepository) ListMemberAddresses(context.Context, application.ListMemberAddressesInput) ([]application.MemberAddresses, error) {
	return nil, nil
}
func (m *memoryRepository) LinkOrganizationContactMethod(_ context.Context, input application.LinkOrganizationContactMethodInput) (domain.ContactMethod, error) {
	if err := input.Validate(); err != nil {
		return domain.ContactMethod{}, err
	}
	for _, existing := range m.links {
		if existing.Kind == input.Kind && existing.OrganizationID == input.OrganizationID && existing.ExternalID == input.ExternalID && existing.UserID != input.UserID {
			return domain.ContactMethod{}, fmt.Errorf("taken: %w", domain.ErrConflict)
		}
	}
	m.links = append(m.links, input)
	return domain.ContactMethod{ID: input.MethodID, UserID: input.UserID, Kind: input.Kind, Value: input.ExternalID, OrganizationID: input.OrganizationID, Label: input.Label}, nil
}

func (m *memoryRepository) GetDiscordConnection(_ context.Context, input application.GetDiscordConnectionInput) (domain.DiscordConnection, error) {
	if m.connection == nil || m.connection.OrganizationID != input.OrganizationID {
		return domain.DiscordConnection{}, domain.ErrNotFound
	}
	return *m.connection, nil
}
func (m *memoryRepository) UpsertDiscordConnection(_ context.Context, input application.UpsertDiscordConnectionInput) (domain.DiscordConnection, error) {
	if err := input.Validate(); err != nil {
		return domain.DiscordConnection{}, err
	}
	if m.upsertErr != nil {
		return domain.DiscordConnection{}, m.upsertErr
	}
	m.connection = &domain.DiscordConnection{
		OrganizationID: input.OrganizationID, GuildID: input.Installation.GuildID, GuildName: input.Installation.GuildName,
		InstalledByMembershipID: input.InstalledByMembershipID, InstalledAt: input.Now,
	}
	return *m.connection, nil
}
func (m *memoryRepository) DeleteDiscordConnection(_ context.Context, input application.DeleteDiscordConnectionInput) (string, error) {
	if err := input.Validate(); err != nil {
		return "", err
	}
	if m.connection == nil {
		return "", domain.ErrNotFound
	}
	guild := m.connection.GuildID
	m.connection = nil
	return guild, nil
}
func (m *memoryRepository) CreateDiscordOAuthState(_ context.Context, input application.CreateDiscordOAuthStateInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	m.states[string(input.StateHash)] = storedState{
		purpose: input.Purpose,
		state:   application.DiscordOAuthState{OrganizationID: input.OrganizationID, MembershipID: input.MembershipID, UserID: input.UserID},
		expires: input.ExpiresAt,
	}
	return nil
}
func (m *memoryRepository) ConsumeDiscordOAuthState(_ context.Context, input application.ConsumeDiscordOAuthStateInput) (application.DiscordOAuthState, error) {
	stored, ok := m.states[string(input.StateHash)]
	if !ok || stored.purpose != input.Purpose || !input.Now.Before(stored.expires) {
		return application.DiscordOAuthState{}, domain.ErrNotFound
	}
	delete(m.states, string(input.StateHash))
	return stored.state, nil
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func admin() domain.Principal {
	return domain.Principal{
		ActorType: domain.ActorHuman, ActorID: "user-1", OrganizationID: "org-1", MembershipID: "membership-1",
	}.WithGrants(domain.OrganizationGrants([]domain.Permission{domain.PermissionConnectionRead, domain.PermissionConnectionWrite}))
}

func reviewer() domain.Principal {
	return domain.Principal{
		ActorType: domain.ActorHuman, ActorID: "user-2", OrganizationID: "org-1", MembershipID: "membership-2",
	}.WithGrants(domain.OrganizationGrants([]domain.Permission{domain.PermissionRequestDecide}))
}

func stateFrom(authorizeURL string) string {
	return strings.SplitN(strings.SplitN(authorizeURL, "state=", 2)[1], "&", 2)[0]
}

func newConnector() *fakeConnector {
	return &fakeConnector{
		guild: application.DiscordInstallation{GuildID: "G0ACME", GuildName: "Acme"},
		user:  application.DiscordUser{ID: "100200300", Username: "ana", DisplayName: "Ana S"},
		members: map[string]application.DiscordUser{
			"G0ACME/100200300": {ID: "100200300", Username: "ana", DisplayName: "Ana at Acme"},
		},
	}
}

func newTestService(connector *fakeConnector, repository *memoryRepository) *Service {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewService(connector, connector, repository, repository, fixedClock{now: time.Unix(1_700_000_000, 0).UTC()}, logger, Config{PublicOrigin: "https://approvals.example.com/"})
}

func connected(repository *memoryRepository) {
	repository.connection = &domain.DiscordConnection{OrganizationID: "org-1", GuildID: "G0ACME", GuildName: "Acme"}
}

// The state carried to Discord is stored only as a hash, expires, and is
// what binds the callback to the organization whose admin began the
// install. The guild comes from the exchange, not the callback query.
func TestBeginThenCompleteBindsTheGuildToTheOrganization(t *testing.T) {
	connector, repository := newConnector(), newMemoryRepository()
	service := newTestService(connector, repository)

	authorizeURL, err := service.Begin(context.Background(), admin(), oauthreturn.Settings)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if !strings.Contains(authorizeURL, "scope=bot") || !strings.Contains(authorizeURL, "redirect_uri=https://approvals.example.com/auth/discord/callback") {
		t.Fatalf("authorize URL is wrong: %s", authorizeURL)
	}
	state := stateFrom(authorizeURL)
	hash := sha256.Sum256([]byte(state))
	if _, stored := repository.states[string(hash[:])]; !stored {
		t.Fatal("state was not stored as its hash")
	}
	if _, clear := repository.states[state]; clear {
		t.Fatal("state was stored in the clear")
	}

	connection, err := service.Complete(context.Background(), state, "good-code", "G0SOMETHINGELSE")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if connection.OrganizationID != "org-1" || connection.InstalledByMembershipID != "membership-1" || connection.GuildID != "G0ACME" {
		t.Fatalf("connection = %+v, want the exchanged guild", connection)
	}
	if _, err := service.Complete(context.Background(), state, "good-code", ""); !errors.Is(err, ErrStateExpired) {
		t.Fatalf("replayed callback = %v, want an expired state", err)
	}
}

func TestCompleteRejectsUnknownExpiredAndMismatchedStates(t *testing.T) {
	connector, repository := newConnector(), newMemoryRepository()
	service := newTestService(connector, repository)
	if _, err := service.Complete(context.Background(), "never-issued", "code", ""); !errors.Is(err, ErrStateExpired) {
		t.Fatalf("unknown state = %v", err)
	}
	authorizeURL, _ := service.Begin(context.Background(), admin(), oauthreturn.Settings)
	state := stateFrom(authorizeURL)
	stored := repository.states[string(hashState(state))]
	stored.expires = time.Unix(1_600_000_000, 0)
	repository.states[string(hashState(state))] = stored
	if _, err := service.Complete(context.Background(), state, "code", ""); !errors.Is(err, ErrStateExpired) {
		t.Fatalf("expired state = %v", err)
	}
	// A link state, minted by any member, can never complete an install.
	connected(repository)
	linkURL, err := service.BeginLink(context.Background(), reviewer(), oauthreturn.Settings)
	if err != nil {
		t.Fatalf("BeginLink: %v", err)
	}
	if _, err := service.Complete(context.Background(), stateFrom(linkURL), "code", ""); !errors.Is(err, ErrStateExpired) {
		t.Fatalf("link state completing an install = %v, want refused", err)
	}
}

// A guild the bot joined but we could not record would otherwise keep the
// bot sitting in a server nobody here owns.
func TestCompleteLeavesAnUnclaimedGuild(t *testing.T) {
	connector, repository := newConnector(), newMemoryRepository()
	repository.upsertErr = domain.ErrConflict
	service := newTestService(connector, repository)
	authorizeURL, _ := service.Begin(context.Background(), admin(), oauthreturn.Settings)
	if _, err := service.Complete(context.Background(), stateFrom(authorizeURL), "code", ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Complete = %v, want the storage conflict", err)
	}
	if len(connector.left) != 1 || connector.left[0] != "G0ACME" {
		t.Fatalf("bot did not leave the unclaimed guild: %v", connector.left)
	}
}

func TestCompleteDistinguishesDiscordRefusalFromASpentState(t *testing.T) {
	connector, repository := newConnector(), newMemoryRepository()
	connector.exchangeErr = fmt.Errorf("discord refused the code: invalid_grant: %w", domain.ErrInvalid)
	service := newTestService(connector, repository)
	authorizeURL, _ := service.Begin(context.Background(), admin(), oauthreturn.Settings)
	_, err := service.Complete(context.Background(), stateFrom(authorizeURL), "bad", "")
	if !errors.Is(err, domain.ErrInvalid) || errors.Is(err, ErrStateExpired) {
		t.Fatalf("refusal = %v, want invalid but not an expired state", err)
	}
}

func TestDisconnectLeavesAndRequiresThePermission(t *testing.T) {
	connector, repository := newConnector(), newMemoryRepository()
	service := newTestService(connector, repository)
	connected(repository)
	if err := service.Disconnect(context.Background(), reviewer()); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("reviewer disconnect = %v, want forbidden", err)
	}
	if _, err := service.Begin(context.Background(), reviewer(), oauthreturn.Settings); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("reviewer begin = %v, want forbidden", err)
	}
	if err := service.Disconnect(context.Background(), admin()); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if repository.connection != nil || len(connector.left) != 1 || connector.left[0] != "G0ACME" {
		t.Fatalf("disconnect left connection=%v left=%v", repository.connection, connector.left)
	}
}

// Linking needs a connected server to check membership against; without
// one the page explains rather than sending the person to Discord for
// nothing. Agents never link.
func TestBeginLinkRequiresAConnectionAndAPerson(t *testing.T) {
	connector, repository := newConnector(), newMemoryRepository()
	service := newTestService(connector, repository)
	if _, err := service.BeginLink(context.Background(), reviewer(), oauthreturn.Settings); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("link without a connection = %v, want not found", err)
	}
	connected(repository)
	agent := domain.Principal{ActorType: domain.ActorAgent, ActorID: "agent-1", OrganizationID: "org-1"}
	if _, err := service.BeginLink(context.Background(), agent, oauthreturn.Settings); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("agent link = %v, want forbidden", err)
	}
	authorizeURL, err := service.BeginLink(context.Background(), reviewer(), oauthreturn.Settings)
	if err != nil || !strings.Contains(authorizeURL, "scope=identify") || !strings.Contains(authorizeURL, "/auth/discord/link/callback") {
		t.Fatalf("BeginLink = %s, %v", authorizeURL, err)
	}
}

func TestServerIsVisibleToMembersOnlyInTheirOrganization(t *testing.T) {
	repository := newMemoryRepository()
	service := newTestService(newConnector(), repository)
	if name, found, err := service.Server(context.Background(), reviewer()); err != nil || found || name != "" {
		t.Fatalf("unconnected server = %q, %v, %v", name, found, err)
	}
	connected(repository)
	if name, found, err := service.Server(context.Background(), reviewer()); err != nil || !found || name != "Acme" {
		t.Fatalf("reviewer's server = %q, %v, %v", name, found, err)
	}
	other := reviewer()
	other.OrganizationID = "org-2"
	if name, found, err := service.Server(context.Background(), other); err != nil || found || name != "" {
		t.Fatalf("another organization's server = %q, %v, %v", name, found, err)
	}
	for _, principal := range []domain.Principal{
		{ActorType: domain.ActorAgent, ActorID: "agent-1", OrganizationID: "org-1"},
		{ActorType: domain.ActorHuman, ActorID: "user-1", OrganizationID: "org-1"},
	} {
		if _, _, err := service.Server(context.Background(), principal); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("non-member server = %v, want forbidden", err)
		}
	}
	service.connector = nil
	if name, found, err := service.Server(context.Background(), reviewer()); err != nil || found || name != "" {
		t.Fatalf("unconfigured server = %q, %v, %v", name, found, err)
	}
}

// Discord vouches for the account; the bot then confirms it is in the
// organization's server, since otherwise no message could reach it.
func TestCompleteLinkRecordsAVerifiedGuildMember(t *testing.T) {
	connector, repository := newConnector(), newMemoryRepository()
	service := newTestService(connector, repository)
	connected(repository)
	authorizeURL, _ := service.BeginLink(context.Background(), reviewer(), oauthreturn.Settings)
	method, err := service.CompleteLink(context.Background(), stateFrom(authorizeURL), "link-code")
	if err != nil {
		t.Fatalf("CompleteLink: %v", err)
	}
	if method.Kind != domain.ContactDiscord || method.Value != "100200300" || method.UserID != "user-2" || method.OrganizationID != "org-1" {
		t.Fatalf("method = %+v", method)
	}
	if method.Label != "Ana at Acme (@ana)" {
		t.Fatalf("label = %q", method.Label)
	}
	if _, err := service.CompleteLink(context.Background(), stateFrom(authorizeURL), "link-code"); !errors.Is(err, ErrStateExpired) {
		t.Fatalf("replayed link = %v, want an expired state", err)
	}
}

func TestCompleteLinkRefusesANonMemberAndATakenAccount(t *testing.T) {
	connector, repository := newConnector(), newMemoryRepository()
	service := newTestService(connector, repository)
	connected(repository)

	connector.user = application.DiscordUser{ID: "555", Username: "stranger"}
	authorizeURL, _ := service.BeginLink(context.Background(), reviewer(), oauthreturn.Settings)
	if _, err := service.CompleteLink(context.Background(), stateFrom(authorizeURL), "link-code"); !errors.Is(err, ErrNotGuildMember) || !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-member = %v, want not a guild member", err)
	}
	if len(repository.links) != 0 {
		t.Fatalf("a non-member was linked: %+v", repository.links)
	}

	connector.user = application.DiscordUser{ID: "100200300", Username: "ana"}
	repository.links = append(repository.links, application.LinkOrganizationContactMethodInput{
		Kind: domain.ContactDiscord, OrganizationID: "org-1", UserID: "user-9", ExternalID: "100200300",
	})
	authorizeURL, _ = service.BeginLink(context.Background(), reviewer(), oauthreturn.Settings)
	if _, err := service.CompleteLink(context.Background(), stateFrom(authorizeURL), "link-code"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("taken account = %v, want conflict", err)
	}

	// The organization disconnected between begin and complete.
	repository.connection = nil
	authorizeURL, _ = service.BeginLink(context.Background(), reviewer(), oauthreturn.Settings)
	if authorizeURL != "" {
		t.Fatal("BeginLink succeeded without a connection")
	}
}

func TestUnlinkRemovesOnlyTheDiscordMethodInThisOrganization(t *testing.T) {
	connector, repository := newConnector(), newMemoryRepository()
	service := newTestService(connector, repository)
	repository.methods = []domain.ContactMethod{
		{ID: "email-1", UserID: "user-2", Kind: domain.ContactEmail, Value: "ana@home.example"},
		{ID: "slack-1", UserID: "user-2", Kind: domain.ContactSlack, Value: "U0ANA", OrganizationID: "org-1"},
		{ID: "discord-elsewhere", UserID: "user-2", Kind: domain.ContactDiscord, Value: "1", OrganizationID: "org-2"},
	}
	if err := service.Unlink(context.Background(), reviewer()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unlink with nothing linked = %v, want not found", err)
	}
	repository.methods = append(repository.methods, domain.ContactMethod{ID: "discord-1", UserID: "user-2", Kind: domain.ContactDiscord, Value: "100200300", OrganizationID: "org-1"})
	if err := service.Unlink(context.Background(), reviewer()); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	if len(repository.deleted) != 1 || repository.deleted[0] != "discord-1" {
		t.Fatalf("deleted = %v, want only the Discord link here", repository.deleted)
	}
}

// A deployment without Discord credentials answers with its own error so
// the pages can say so, rather than a nil pointer or a generic failure.
func TestUnconfiguredServiceRefusesEverything(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repository := newMemoryRepository()
	connected(repository)
	service := NewService(nil, nil, repository, repository, fixedClock{}, logger, Config{})
	if _, err := service.Get(context.Background(), admin()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Get = %v", err)
	}
	if _, err := service.Begin(context.Background(), admin(), oauthreturn.Settings); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Begin = %v", err)
	}
	if _, err := service.Complete(context.Background(), "state", "code", ""); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Complete = %v", err)
	}
	if _, err := service.BeginLink(context.Background(), reviewer(), oauthreturn.Settings); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("BeginLink = %v", err)
	}
	if _, err := service.CompleteLink(context.Background(), "state", "code"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("CompleteLink = %v", err)
	}
}

func (m *memoryRepository) VerifyContactMethodByValue(context.Context, application.VerifyContactMethodByValueInput) (domain.ContactMethod, error) {
	return domain.ContactMethod{}, domain.ErrNotFound
}

func (r *memoryRepository) CreateVouchedContactMethod(context.Context, application.CreateVouchedContactMethodInput) (domain.ContactMethod, error) {
	return domain.ContactMethod{}, nil
}
