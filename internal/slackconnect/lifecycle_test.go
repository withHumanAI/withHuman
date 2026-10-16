package slackconnect

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/oauthreturn"
)

func signedEvent(body string, at time.Time) (string, string) {
	timestamp := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte("signing-secret"))
	mac.Write([]byte("v0:" + timestamp + ":" + body))
	return timestamp, "v0=" + hex.EncodeToString(mac.Sum(nil))
}

func TestSlackEventBoundary(t *testing.T) {
	repo := newMemoryRepository()
	service := newTestService(t, &fakeConnector{}, repo)
	now := service.clock.Now()
	event := fmt.Sprintf(`{"type":"event_callback","api_app_id":"A123","event_id":"Ev1","team_id":"T0ACME","event_time":%d,"event":{"type":"app_uninstalled"}}`, now.Unix())
	timestamp, signature := signedEvent(event, now)
	for i := 0; i < 2; i++ {
		if _, err := service.ReceiveEvent(context.Background(), timestamp, signature, []byte(event)); err != nil {
			t.Fatal(err)
		}
	}
	if len(repo.events) != 1 {
		t.Fatalf("duplicate event persisted: %d", len(repo.events))
	}
	for _, test := range []struct{ name, body, signature, timestamp string }{
		{"bad signature", event, "v0=bad", timestamp},
		{"changed body", event + " ", signature, timestamp},
		{"stale", event, signature, strconv.FormatInt(now.Add(-6*time.Minute).Unix(), 10)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.ReceiveEvent(context.Background(), test.timestamp, test.signature, []byte(test.body)); !errors.Is(err, domain.ErrUnauthenticated) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	for _, body := range []string{
		`{"type":"url_verification","challenge":"challenge-1"}`,
		fmt.Sprintf(`{"type":"event_callback","api_app_id":"A123","event_id":"Ev2","team_id":"T0ACME","event_time":%d,"event":{"type":"tokens_revoked","tokens":{"oauth":["U1"]}}}`, now.Unix()),
	} {
		ts, sig := signedEvent(body, now)
		challenge, err := service.ReceiveEvent(context.Background(), ts, sig, []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(body, "url_verification") && challenge != "challenge-1" {
			t.Fatalf("challenge=%q", challenge)
		}
	}
	if len(repo.events) != 1 {
		t.Fatal("unrelated user revocation persisted")
	}
}

func TestConflictChecksOldGrantAndNeverRevokesOnFailure(t *testing.T) {
	for _, test := range []struct {
		name  string
		check error
		want  error
	}{
		{"healthy", nil, domain.ErrConflict},
		{"revoked", application.ErrSlackRevoked, nil},
		{"inconclusive", errors.New("invalid_auth"), ErrCheckUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := newMemoryRepository()
			repo.owner = &domain.SlackConnection{OrganizationID: "old-org", InstallationID: "old-generation", TeamID: "T0ACME", Status: "connected"}
			repo.ownerToken = "old-token"
			connector := &fakeConnector{tokenErrors: map[string]error{"old-token": test.check}}
			service := newTestService(t, connector, repo)
			url, err := service.Begin(context.Background(), admin(), oauthreturn.Settings)
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Complete(context.Background(), stateFrom(url), "code")
			if !errors.Is(err, test.want) {
				t.Fatalf("complete=%v want=%v", err, test.want)
			}
			if len(connector.revoked) != 0 {
				t.Fatal("existing authorization was revoked")
			}
			if test.want == nil && repo.owner.Status != "disconnected" {
				t.Fatal("old claim retained")
			}
			if test.want != nil && repo.owner.Status != "connected" {
				t.Fatal("old claim changed")
			}
		})
	}
}

func TestIncomingGrantMustWorkBeforeReclaimingWorkspace(t *testing.T) {
	repo := newMemoryRepository()
	repo.owner = &domain.SlackConnection{OrganizationID: "old-org", InstallationID: "old-generation", TeamID: "T0ACME", Status: "connected"}
	repo.ownerToken = "old-token"
	connector := &fakeConnector{tokenErrors: map[string]error{"old-token": application.ErrSlackRevoked, "xoxb-token": errors.New("invalid_auth")}}
	service := newTestService(t, connector, repo)
	url, err := service.Begin(context.Background(), admin(), oauthreturn.Settings)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Complete(context.Background(), stateFrom(url), "code")
	if !errors.Is(err, ErrCheckUnavailable) || repo.owner.Status != "connected" || repo.connection != nil || len(connector.revoked) != 0 {
		t.Fatalf("unverified grant changed ownership: %v", err)
	}
}

func TestDelayedEventsCannotRemoveANewInstallation(t *testing.T) {
	for _, test := range []struct {
		name         string
		eventOffset  time.Duration
		check        error
		bot          string
		disconnected bool
		pending      bool
	}{
		{"older uninstall", -time.Minute, application.ErrSlackRevoked, "", false, false},
		{"healthy same second", 0, nil, "", false, false},
		{"healthy newer connection", time.Second, nil, "", false, false},
		{"ambiguous same second", 0, errors.New("timeout"), "", false, true},
		{"later dispatch with inconclusive token", time.Second, errors.New("invalid_auth"), "", false, true},
		{"revoked", time.Second, application.ErrSlackRevoked, "", true, false},
		{"different bot", time.Second, application.ErrSlackRevoked, "U_OTHER", false, false},
		{"matching bot", time.Second, application.ErrSlackRevoked, "U0BOT", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := newMemoryRepository()
			connector := &fakeConnector{tokenErrors: map[string]error{"xoxb-token": test.check}}
			service := newTestService(t, connector, repo)
			now := service.clock.Now()
			repo.connection = &domain.SlackConnection{OrganizationID: "org-1", InstallationID: "new", TeamID: "T0ACME", BotUserID: "U0BOT", Status: "connected", InstalledAt: now}
			event := application.SlackEvent{ID: "event", AppID: "A123", ExternalEventID: "Ev", TeamID: "T0ACME", Type: "app_uninstalled", OccurredAt: now.Add(test.eventOffset), ReceivedAt: now}
			if test.bot != "" {
				event.Type = "tokens_revoked"
				event.BotUserIDs = []string{test.bot}
			}
			repo.events = []application.SlackEvent{event}
			err := service.DrainEvents(context.Background())
			if (err != nil) != test.pending {
				t.Fatalf("drain=%v", err)
			}
			if (repo.connection.Status == "disconnected") != test.disconnected {
				t.Fatalf("state=%s", repo.connection.Status)
			}
			if test.pending && repo.completed != 0 {
				t.Fatal("inconclusive event lost")
			}
		})
	}
}

type unavailableEventDispatcher struct {
	repo  *memoryRepository
	calls int
}

func (d *unavailableEventDispatcher) DispatchSlackEvents(context.Context, application.DispatchSlackEventsInput) error {
	if len(d.repo.events) != 1 {
		return errors.New("event was not persisted before dispatch")
	}
	d.calls++
	return errors.New("Temporal unavailable")
}

func TestAcceptedEventSurvivesImmediateDispatchFailure(t *testing.T) {
	repo := newMemoryRepository()
	dispatcher := &unavailableEventDispatcher{repo: repo}
	service := newTestService(t, &fakeConnector{}, repo).WithEventDispatcher(dispatcher)
	now := service.clock.Now()
	body := fmt.Sprintf(`{"type":"event_callback","api_app_id":"A123","event_id":"Ev-start","team_id":"T0ACME","event_time":%d,"event":{"type":"app_uninstalled"}}`, now.Unix())
	ts, signature := signedEvent(body, now)
	if _, err := service.ReceiveEvent(context.Background(), ts, signature, []byte(body)); err != nil {
		t.Fatal(err)
	}
	if len(repo.events) != 1 || dispatcher.calls != 1 || repo.completed != 0 {
		t.Fatal("accepted event was not queued before the failed dispatch")
	}
	if err := service.DrainEvents(context.Background()); err != nil || repo.completed != 1 {
		t.Fatalf("minute drain=%v completed=%d", err, repo.completed)
	}
}

type eventRecoveryRepository struct {
	*memoryRepository
	completeErr error
}

func (r *eventRecoveryRepository) CompleteSlackEvent(ctx context.Context, in application.CompleteSlackEventInput) error {
	if r.completeErr != nil {
		return r.completeErr
	}
	return r.memoryRepository.CompleteSlackEvent(ctx, in)
}

func TestPersistedSlackEventRecoversAfterProcessingFailure(t *testing.T) {
	repo := newMemoryRepository()
	connector := &fakeConnector{tokenErrors: map[string]error{"xoxb-token": errors.New("timeout")}}
	service := newTestService(t, connector, repo)
	now := service.clock.Now()
	repo.connection = &domain.SlackConnection{OrganizationID: "org-1", InstallationID: "generation", TeamID: "T0ACME", BotUserID: "U0BOT", Status: "connected", InstalledAt: now}
	body := fmt.Sprintf(`{"type":"event_callback","api_app_id":"A123","event_id":"Ev-retry","team_id":"T0ACME","event_time":%d,"event":{"type":"app_uninstalled"}}`, now.Unix())
	ts, sig := signedEvent(body, now)
	if _, err := service.ReceiveEvent(context.Background(), ts, sig, []byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := service.DrainEvents(context.Background()); err == nil || repo.completed != 0 {
		t.Fatal("inconclusive event was lost")
	}
	connector.tokenErrors["xoxb-token"] = application.ErrSlackRevoked
	recovery := &eventRecoveryRepository{memoryRepository: repo, completeErr: errors.New("failed after disconnect")}
	service.connections = recovery
	if err := service.DrainEvents(context.Background()); err == nil || repo.completed != 0 || repo.connection.Status != "disconnected" {
		t.Fatal("expected persisted disconnection with pending inbox entry")
	}
	recovery.completeErr = nil
	if err := service.DrainEvents(context.Background()); err != nil || repo.completed != 1 {
		t.Fatalf("retry=%v completed=%d", err, repo.completed)
	}
}
