package slackconnect

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
	"github.com/withHumanAI/withHuman/internal/secrets"
)

type fakeConnector struct {
	tokenErrors map[string]error
	exchangeErr error
	revoked     []string
	directory   map[string]application.SlackUser
	posted      []application.SlackDirectMessageInput
}

func (f *fakeConnector) LookupUserByEmail(_ context.Context, _ string, email string) (application.SlackUser, error) {
	if user, ok := f.directory[email]; ok {
		return user, nil
	}
	return application.SlackUser{}, domain.ErrNotFound
}

func (f *fakeConnector) PostDirectMessage(_ context.Context, input application.SlackDirectMessageInput) error {
	f.posted = append(f.posted, input)
	return nil
}

func (f *fakeConnector) AuthorizeURL(state, redirect string) string {
	return "https://slack.example/authorize?state=" + state + "&redirect_uri=" + redirect
}

func (f *fakeConnector) Exchange(context.Context, application.SlackExchangeInput) (application.SlackInstallation, error) {
	if f.exchangeErr != nil {
		return application.SlackInstallation{}, f.exchangeErr
	}
	return application.SlackInstallation{TeamID: "T0ACME", TeamName: "Acme", BotUserID: "U0BOT", Scopes: []string{"chat:write"}, BotToken: "xoxb-token"}, nil
}

func (f *fakeConnector) Revoke(_ context.Context, token string) error {
	f.revoked = append(f.revoked, token)
	return nil
}

type memoryRepository struct {
	application.SlackLifecycleRepository
	events      []application.SlackEvent
	completed   int
	owner       *domain.SlackConnection
	ownerToken  string
	states      map[string]application.SlackInstallState
	expiries    map[string]time.Time
	connection  *domain.SlackConnection
	sealedToken []byte
	upsertErr   error
	members     []application.MemberAddresses
	links       []application.LinkOrganizationContactMethodInput
}

func (m *memoryRepository) ListContactMethods(context.Context, application.ListContactMethodsInput) ([]domain.ContactMethod, error) {
	return nil, nil
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
func (m *memoryRepository) DeleteContactMethod(context.Context, application.DeleteContactMethodInput) error {
	return nil
}
func (m *memoryRepository) ListMemberAddresses(_ context.Context, input application.ListMemberAddressesInput) ([]application.MemberAddresses, error) {
	if input.UserID == "" {
		return m.members, nil
	}
	var matching []application.MemberAddresses
	for _, member := range m.members {
		if member.UserID == input.UserID {
			matching = append(matching, member)
		}
	}
	return matching, nil
}
func (m *memoryRepository) LinkOrganizationContactMethod(_ context.Context, input application.LinkOrganizationContactMethodInput) (domain.ContactMethod, error) {
	if err := input.Validate(); err != nil {
		return domain.ContactMethod{}, err
	}
	m.links = append(m.links, input)
	return domain.ContactMethod{ID: input.MethodID, Kind: input.Kind, Value: input.ExternalID}, nil
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{states: map[string]application.SlackInstallState{}, expiries: map[string]time.Time{}}
}

func (m *memoryRepository) GetSlackConnection(context.Context, application.GetSlackConnectionInput) (domain.SlackConnection, error) {
	if m.connection == nil {
		return domain.SlackConnection{}, domain.ErrNotFound
	}
	return *m.connection, nil
}
func (m *memoryRepository) OpenSlackBotToken(_ context.Context, in application.GetSlackConnectionInput) (string, error) {
	if m.owner != nil && m.owner.OrganizationID == in.OrganizationID {
		return m.ownerToken, nil
	}
	if m.connection == nil || m.connection.Status != "connected" {
		return "", domain.ErrNotFound
	}
	return "xoxb-token", nil
}
func (m *memoryRepository) UpsertSlackConnection(_ context.Context, input application.UpsertSlackConnectionInput) (domain.SlackConnection, error) {
	if err := input.Validate(); err != nil {
		return domain.SlackConnection{}, err
	}
	if m.upsertErr != nil {
		return domain.SlackConnection{}, m.upsertErr
	}
	if input.PreviousOwner != nil {
		_, _ = m.DisconnectSlackConnection(context.Background(), application.DisconnectSlackConnectionInput{SlackConnectionVersion: *input.PreviousOwner})
	}
	m.sealedToken = input.SealedBotToken
	m.connection = &domain.SlackConnection{
		Status: "connected", InstallationID: input.InstallationID, OrganizationID: input.OrganizationID, TeamID: input.Installation.TeamID, TeamName: input.Installation.TeamName,
		BotUserID: input.Installation.BotUserID, Scopes: input.Installation.Scopes,
		InstalledByMembershipID: input.InstalledByMembershipID, InstalledAt: input.Now,
	}
	return *m.connection, nil
}
func (m *memoryRepository) DeleteSlackConnection(_ context.Context, input application.DeleteSlackConnectionInput) (string, error) {
	if err := input.Validate(); err != nil {
		return "", err
	}
	if m.connection == nil || m.connection.Status != "connected" {
		return "", domain.ErrNotFound
	}
	m.connection.Status = "disconnected"
	return "xoxb-token", nil
}
func (m *memoryRepository) CreateSlackInstallState(_ context.Context, input application.CreateSlackInstallStateInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	m.states[string(input.StateHash)] = application.SlackInstallState{OrganizationID: input.OrganizationID, MembershipID: input.MembershipID}
	m.expiries[string(input.StateHash)] = input.ExpiresAt
	return nil
}
func (m *memoryRepository) ConsumeSlackInstallState(_ context.Context, input application.ConsumeSlackInstallStateInput) (application.SlackInstallState, error) {
	state, ok := m.states[string(input.StateHash)]
	if !ok || !input.Now.Before(m.expiries[string(input.StateHash)]) {
		return application.SlackInstallState{}, domain.ErrNotFound
	}
	delete(m.states, string(input.StateHash))
	return state, nil
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func admin() domain.Principal {
	return domain.Principal{
		ActorType: domain.ActorHuman, ActorID: "user-1", OrganizationID: "org-1", MembershipID: "membership-1",
	}.WithGrants(domain.OrganizationGrants([]domain.Permission{domain.PermissionConnectionRead, domain.PermissionConnectionWrite}))
}

func stateFrom(authorizeURL string) string {
	return strings.SplitN(strings.SplitN(authorizeURL, "state=", 2)[1], "&", 2)[0]
}

func newTestService(t *testing.T, connector *fakeConnector, repository *memoryRepository) *Service {
	t.Helper()
	sealer, err := secrets.NewSealer("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewService(connector, connector, repository, repository, sealer, fixedClock{now: time.Unix(1_700_000_000, 0).UTC()}, logger, Config{PublicOrigin: "https://approvals.example.com/", AppID: "A123", SigningSecret: "signing-secret"})
}

// The state carried to Slack is stored only as a hash, expires, and is what
// binds the callback to the organization whose admin began the install.
func TestBeginThenCompleteBindsTheWorkspaceToTheOrganization(t *testing.T) {
	connector, repository := &fakeConnector{}, newMemoryRepository()
	service := newTestService(t, connector, repository)

	authorizeURL, err := service.Begin(context.Background(), admin(), oauthreturn.Settings)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if !strings.Contains(authorizeURL, "redirect_uri=https://approvals.example.com/auth/slack/callback") {
		t.Fatalf("authorize URL has the wrong callback: %s", authorizeURL)
	}
	state := stateFrom(authorizeURL)
	hash := sha256.Sum256([]byte(state))
	if _, stored := repository.states[string(hash[:])]; !stored {
		t.Fatal("state was not stored as its hash")
	}
	if _, clear := repository.states[state]; clear {
		t.Fatal("state was stored in the clear")
	}

	connection, err := service.Complete(context.Background(), state, "good-code")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if connection.OrganizationID != "org-1" || connection.InstalledByMembershipID != "membership-1" || connection.TeamID != "T0ACME" {
		t.Fatalf("connection = %+v", connection)
	}
	if string(repository.sealedToken) == "xoxb-token" || len(repository.sealedToken) == 0 {
		t.Fatalf("bot token reached storage unsealed: %q", repository.sealedToken)
	}

	// The state is spent: replaying the callback cannot install again.
	if _, err := service.Complete(context.Background(), state, "good-code"); !errors.Is(err, ErrInstallExpired) {
		t.Fatalf("replayed callback = %v, want an expired install", err)
	}
}

// An install begun from the guided setup names it in the state, and the
// state still redeems as a whole.
func TestAnInstallFromTheGuidedSetupReturnsThere(t *testing.T) {
	connector, repository := &fakeConnector{}, newMemoryRepository()
	service := newTestService(t, connector, repository)
	authorizeURL, err := service.Begin(context.Background(), admin(), oauthreturn.Onboarding)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	state := stateFrom(authorizeURL)
	if page := oauthreturn.FromState(state); page != oauthreturn.Onboarding {
		t.Fatalf("state returns to %q, want onboarding", page)
	}
	if _, err := service.Complete(context.Background(), state, "good-code"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func TestCompleteRejectsUnknownAndExpiredStates(t *testing.T) {
	connector, repository := &fakeConnector{}, newMemoryRepository()
	service := newTestService(t, connector, repository)
	if _, err := service.Complete(context.Background(), "never-issued", "good-code"); !errors.Is(err, ErrInstallExpired) {
		t.Fatalf("unknown state = %v, want an expired install", err)
	}
	authorizeURL, _ := service.Begin(context.Background(), admin(), oauthreturn.Settings)
	state := stateFrom(authorizeURL)
	repository.expiries[string(hashState(state))] = time.Unix(1_600_000_000, 0)
	if _, err := service.Complete(context.Background(), state, "good-code"); !errors.Is(err, ErrInstallExpired) {
		t.Fatalf("expired state = %v, want an expired install", err)
	}
}

// OAuth may return the token already used by a different organization.
func TestCompletePreservesProviderTokenAfterSaveFailure(t *testing.T) {
	connector, repository := &fakeConnector{}, newMemoryRepository()
	repository.upsertErr = domain.ErrConflict
	service := newTestService(t, connector, repository)
	authorizeURL, _ := service.Begin(context.Background(), admin(), oauthreturn.Settings)
	if _, err := service.Complete(context.Background(), stateFrom(authorizeURL), "good-code"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Complete = %v, want the storage conflict", err)
	}
	if len(connector.revoked) != 0 {
		t.Fatalf("shared token was revoked: %v", connector.revoked)
	}
}

// Slack refusing the code is a different failure from a spent state, and
// the page tells the admin something different for each.
func TestCompleteDistinguishesSlackRefusalFromASpentState(t *testing.T) {
	connector, repository := &fakeConnector{exchangeErr: fmt.Errorf("slack refused the install: invalid_code: %w", domain.ErrInvalid)}, newMemoryRepository()
	service := newTestService(t, connector, repository)
	authorizeURL, _ := service.Begin(context.Background(), admin(), oauthreturn.Settings)
	_, err := service.Complete(context.Background(), stateFrom(authorizeURL), "bad-code")
	if !errors.Is(err, domain.ErrInvalid) || errors.Is(err, ErrInstallExpired) {
		t.Fatalf("slack refusal = %v, want invalid but not an expired install", err)
	}
}

func TestDisconnectRevokesAndRequiresThePermission(t *testing.T) {
	connector, repository := &fakeConnector{}, newMemoryRepository()
	service := newTestService(t, connector, repository)
	repository.connection = &domain.SlackConnection{Status: "connected", InstallationID: "generation-1", OrganizationID: "org-1", TeamID: "T0ACME", TeamName: "Acme", BotUserID: "U0BOT"}

	reviewer := admin()
	reviewer = reviewer.WithGrants(domain.OrganizationGrants([]domain.Permission{domain.PermissionRequestDecide}))
	if err := service.Disconnect(context.Background(), reviewer); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("reviewer disconnect = %v, want forbidden", err)
	}
	if _, err := service.Begin(context.Background(), reviewer, oauthreturn.Settings); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("reviewer begin = %v, want forbidden", err)
	}
	if err := service.Disconnect(context.Background(), admin()); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if repository.connection.Status != "disconnected" || len(connector.revoked) != 1 {
		t.Fatalf("disconnect left connection=%v revoked=%v", repository.connection, connector.revoked)
	}
}

// A deployment without Slack credentials answers with its own error so
// the page can say so, rather than a nil pointer or a generic failure.
func TestUnconfiguredServiceRefusesEverything(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(nil, nil, newMemoryRepository(), newMemoryRepository(), nil, fixedClock{}, logger, Config{})
	if _, err := service.Get(context.Background(), admin()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Get = %v", err)
	}
	if _, err := service.Begin(context.Background(), admin(), oauthreturn.Settings); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Begin = %v", err)
	}
	if _, err := service.Complete(context.Background(), "state", "code"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Complete = %v", err)
	}
}

// Completing an install pre-links members whose verified addresses are in
// the workspace, and Sync members repeats it on demand. Unconfirmed
// addresses never take part, so a typo cannot link a stranger.
func TestInstallMatchesMembersByVerifiedAddress(t *testing.T) {
	connector, repository := &fakeConnector{directory: map[string]application.SlackUser{
		"ana@example.com":          {ID: "U0ANA", Handle: "ana", DisplayName: "Ana"},
		"ben.personal@example.com": {ID: "U0BEN", Handle: "ben"},
	}}, newMemoryRepository()
	repository.members = []application.MemberAddresses{
		{UserID: "user-ana", MembershipID: "m-ana", Emails: []string{"ana@example.com"}},
		{UserID: "user-ben", MembershipID: "m-ben", Emails: []string{"ben@example.com", "ben.personal@example.com"}},
		{UserID: "user-cid", MembershipID: "m-cid", Emails: []string{"cid@example.com"}},
	}
	service := newTestService(t, connector, repository)
	authorizeURL, _ := service.Begin(context.Background(), admin(), oauthreturn.Settings)
	if _, err := service.Complete(context.Background(), stateFrom(authorizeURL), "good-code"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(repository.links) != 2 {
		t.Fatalf("links after install = %+v, want Ana and Ben", repository.links)
	}
	for _, link := range repository.links {
		if link.SlackInstallationID == "" || link.SlackInstallationID != repository.connection.InstallationID {
			t.Fatalf("member match did not use the installed generation: %+v", link)
		}
	}
	if repository.links[0].Kind != domain.ContactSlack || repository.links[0].ExternalID != "U0ANA" || repository.links[0].Label != "Ana (@ana)" || repository.links[1].ExternalID != "U0BEN" || repository.links[1].Label != "@ben" {
		t.Fatalf("links = %+v", repository.links)
	}
	result, err := service.SyncMembers(context.Background(), admin())
	if err != nil || result.Members != 3 || result.Linked != 2 {
		t.Fatalf("SyncMembers = %+v, %v", result, err)
	}
}

// Any member may ask whether a workspace is connected, and learns only its
// name; agents get nothing.
func TestWorkspaceIsReadableByAnyMember(t *testing.T) {
	connector, repository := &fakeConnector{}, newMemoryRepository()
	service := newTestService(t, connector, repository)
	member := domain.Principal{ActorType: domain.ActorHuman, ActorID: "user-ben", OrganizationID: "org-1", MembershipID: "m-ben"}
	if name, connected, err := service.Workspace(context.Background(), member); err != nil || connected || name != "" {
		t.Fatalf("Workspace before install = %q, %v, %v", name, connected, err)
	}
	repository.connection = &domain.SlackConnection{Status: "connected", InstallationID: "generation-1", OrganizationID: "org-1", TeamID: "T1", TeamName: "Acme HQ"}
	if name, connected, err := service.Workspace(context.Background(), member); err != nil || !connected || name != "Acme HQ" {
		t.Fatalf("Workspace after install = %q, %v, %v", name, connected, err)
	}
	agent := domain.Principal{ActorType: domain.ActorAgent, ActorID: "agent-1", OrganizationID: "org-1"}
	if _, _, err := service.Workspace(context.Background(), agent); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Workspace as an agent = %v, want forbidden", err)
	}
}

// A member links themselves by their own confirmed addresses, without the
// connection permission and without touching anyone else's link.
func TestLinkSelfMatchesOnlyTheCaller(t *testing.T) {
	connector, repository := &fakeConnector{directory: map[string]application.SlackUser{
		"ana@example.com": {ID: "U0ANA", Handle: "ana", DisplayName: "Ana"},
		"ben@example.com": {ID: "U0BEN", Handle: "ben"},
	}}, newMemoryRepository()
	repository.members = []application.MemberAddresses{
		{UserID: "user-ana", MembershipID: "m-ana", Emails: []string{"ana@example.com"}},
		{UserID: "user-ben", MembershipID: "m-ben", Emails: []string{"ben@example.com"}},
		{UserID: "user-cid", MembershipID: "m-cid", Emails: []string{"cid@example.com"}},
	}
	service := newTestService(t, connector, repository)
	ben := domain.Principal{ActorType: domain.ActorHuman, ActorID: "user-ben", OrganizationID: "org-1", MembershipID: "m-ben"}

	if _, err := service.LinkSelf(context.Background(), ben); !errors.Is(err, domain.ErrNotFound) || errors.Is(err, ErrNoSlackMatch) {
		t.Fatalf("LinkSelf before any install = %v, want plain not found", err)
	}
	repository.connection = &domain.SlackConnection{Status: "connected", InstallationID: "generation-1", OrganizationID: "org-1", TeamID: "T1"}

	method, err := service.LinkSelf(context.Background(), ben)
	if err != nil || method.Value != "U0BEN" {
		t.Fatalf("LinkSelf = %+v, %v", method, err)
	}
	if len(repository.links) != 1 || repository.links[0].UserID != "user-ben" || repository.links[0].Label != "@ben" {
		t.Fatalf("links = %+v, want Ben's alone", repository.links)
	}

	cid := domain.Principal{ActorType: domain.ActorHuman, ActorID: "user-cid", OrganizationID: "org-1", MembershipID: "m-cid"}
	if _, err := service.LinkSelf(context.Background(), cid); !errors.Is(err, ErrNoSlackMatch) {
		t.Fatalf("LinkSelf without a matching address = %v, want ErrNoSlackMatch", err)
	}
	stranger := domain.Principal{ActorType: domain.ActorHuman, ActorID: "user-zed", OrganizationID: "org-1", MembershipID: "m-zed"}
	if _, err := service.LinkSelf(context.Background(), stranger); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("LinkSelf as a non-member = %v, want forbidden", err)
	}
	agent := domain.Principal{ActorType: domain.ActorAgent, ActorID: "agent-1", OrganizationID: "org-1"}
	if _, err := service.LinkSelf(context.Background(), agent); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("LinkSelf as an agent = %v, want forbidden", err)
	}
}

func (m *memoryRepository) VerifyContactMethodByValue(context.Context, application.VerifyContactMethodByValueInput) (domain.ContactMethod, error) {
	return domain.ContactMethod{}, domain.ErrNotFound
}

func (f *fakeConnector) CheckToken(_ context.Context, in application.SlackTokenInput) (application.SlackTokenIdentity, error) {
	return application.SlackTokenIdentity{TeamID: "T0ACME", BotUserID: "U0BOT"}, f.tokenErrors[in.BotToken]
}
func (m *memoryRepository) WithSlackLock(ctx context.Context, in application.SlackLockInput) error {
	return in.Work(ctx)
}
func (m *memoryRepository) FindSlackWorkspace(_ context.Context, in application.FindSlackWorkspaceInput) (domain.SlackConnection, error) {
	for _, c := range []*domain.SlackConnection{m.owner, m.connection} {
		if c != nil && c.Status == "connected" && c.TeamID == in.TeamID {
			return *c, nil
		}
	}
	return domain.SlackConnection{}, domain.ErrNotFound
}
func (m *memoryRepository) DisconnectSlackConnection(_ context.Context, in application.DisconnectSlackConnectionInput) (bool, error) {
	for _, c := range []*domain.SlackConnection{m.owner, m.connection} {
		if c != nil && c.OrganizationID == in.OrganizationID && c.InstallationID == in.InstallationID && c.Status == "connected" {
			c.Status = "disconnected"
			return true, nil
		}
	}
	return false, nil
}
func (m *memoryRepository) InsertSlackEvent(_ context.Context, in application.SlackEvent) error {
	if err := in.Validate(); err != nil {
		return err
	}
	for _, e := range m.events {
		if e.ExternalEventID == in.ExternalEventID && e.AppID == in.AppID {
			return nil
		}
	}
	in.ID = domain.ID(in.ExternalEventID)
	m.events = append(m.events, in)
	return nil
}
func (m *memoryRepository) ListPendingSlackEvents(context.Context, application.ListPendingSlackEventsInput) ([]application.SlackEvent, error) {
	return m.events, nil
}
func (m *memoryRepository) CompleteSlackEvent(context.Context, application.CompleteSlackEventInput) error {
	m.completed++
	return nil
}

func (r *memoryRepository) CreateVouchedContactMethod(context.Context, application.CreateVouchedContactMethodInput) (domain.ContactMethod, error) {
	return domain.ContactMethod{}, nil
}
