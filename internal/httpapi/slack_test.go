package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/internal/secrets"
	"github.com/withHumanAI/withHuman/internal/slackconnect"
)

func slackTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	unconfigured := slackconnect.NewService(nil, nil, nil, nil, nil, application.RealClock{}, logger, slackconnect.Config{})
	return NewRouter(Dependencies{Logger: logger, Slack: unconfigured})
}

type eventRepository struct {
	application.SlackConnectionRepository
	err    error
	stored int
}

func (r *eventRepository) InsertSlackEvent(context.Context, application.SlackEvent) error {
	if r.err != nil {
		return r.err
	}
	r.stored++
	return nil
}

type eventConnector struct{ application.SlackConnector }

func TestSlackEventsUseSignaturesWithoutSessionOrOrigin(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sealer, err := secrets.NewSealer("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	repo := &eventRepository{}
	service := slackconnect.NewService(eventConnector{}, nil, repo, nil, sealer, application.RealClock{}, logger, slackconnect.Config{AppID: "A1", SigningSecret: "secret"})
	router := NewRouter(Dependencies{Logger: logger, Slack: service})
	request := func(body, signature string) *httptest.ResponseRecorder {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte("secret"))
		mac.Write([]byte("v0:" + timestamp + ":" + body))
		if signature == "" {
			signature = "v0=" + hex.EncodeToString(mac.Sum(nil))
		}
		req := httptest.NewRequest(http.MethodPost, "/auth/slack/events", strings.NewReader(body))
		req.Header.Set("Origin", "https://slack.com")
		req.Header.Set("X-Slack-Request-Timestamp", timestamp)
		req.Header.Set("X-Slack-Signature", signature)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder
	}
	challenge := `{"type":"url_verification","challenge":"verify-me"}`
	if result := request(challenge, ""); result.Code != http.StatusOK || !strings.Contains(result.Body.String(), "verify-me") {
		t.Fatalf("challenge=%d %s", result.Code, result.Body)
	}
	if result := request(challenge, "v0=invalid"); result.Code != http.StatusUnauthorized {
		t.Fatalf("invalid signature=%d", result.Code)
	}
	event := `{"type":"event_callback","api_app_id":"A1","event_id":"Ev1","team_id":"T1","event_time":100,"event":{"type":"app_uninstalled"}}`
	if result := request(event, ""); result.Code != http.StatusOK || repo.stored != 1 {
		t.Fatalf("event=%d persisted=%d", result.Code, repo.stored)
	}
	repo.err = errors.New("database unavailable")
	if result := request(event, ""); result.Code != http.StatusInternalServerError || repo.stored != 1 {
		t.Fatalf("unpersisted event acknowledged=%d", result.Code)
	}
}

func TestSlackInteractionsUseSignaturesWithoutSessionOrOrigin(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sealer, err := secrets.NewSealer("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	// No repositories, messenger, dispatcher or approval service are supplied:
	// link clicks must succeed without invoking any of them.
	service := slackconnect.NewService(eventConnector{}, nil, nil, nil, sealer, application.RealClock{}, logger, slackconnect.Config{AppID: "A1", SigningSecret: "secret"})
	router := NewRouter(Dependencies{Logger: logger, Slack: service})
	form := func(payload string) string { return url.Values{"payload": {payload}}.Encode() }
	body := form(`{"type":"block_actions","api_app_id":"A1","actions":[{"type":"button","url":"https://approvals.example.com/queue/request-1"}]}`)
	for _, test := range []struct {
		name   string
		body   string
		origin string
		status int
	}{
		{"no session or origin", body, "", http.StatusOK},
		{"repeated click", body, "", http.StatusOK},
		{"cross-site", body, "https://slack.com", http.StatusOK},
		{"wrong app", form(`{"type":"block_actions","api_app_id":"A_OTHER"}`), "", http.StatusUnauthorized},
		{"malformed JSON", form(`{"type":`), "", http.StatusBadRequest},
		{"malformed form", "payload=%zz", "", http.StatusBadRequest},
		{"missing payload", "other=value", "", http.StatusBadRequest},
		{"unsupported type", form(`{"type":"view_submission","api_app_id":"A1"}`), "", http.StatusBadRequest},
		{"body at limit", body + "&padding=" + strings.Repeat("x", (1<<20)-len(body)-len("&padding=")), "", http.StatusOK},
		{"oversized body", body + "&padding=" + strings.Repeat("x", 1<<20), "", http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := signedSlackInteractionRequest(test.body)
			if test.origin != "" {
				req.Header.Set("Origin", test.origin)
			}
			result := httptest.NewRecorder()
			router.ServeHTTP(result, req)
			if result.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", result.Code, test.status, result.Body)
			}
			if test.status == http.StatusOK {
				if result.Body.Len() != 0 {
					t.Fatalf("acknowledgment body = %q, want empty", result.Body.String())
				}
			} else {
				assertSlackErrorEnvelope(t, result)
			}
		})
	}
	for _, header := range []string{"X-Slack-Signature", "X-Slack-Request-Timestamp"} {
		t.Run("missing "+header, func(t *testing.T) {
			req := signedSlackInteractionRequest(body)
			req.Header.Del(header)
			result := httptest.NewRecorder()
			router.ServeHTTP(result, req)
			if result.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", result.Code)
			}
			assertSlackErrorEnvelope(t, result)
		})
	}
}

func TestSlackInteractionsWithoutConfiguration(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for name, router := range map[string]*gin.Engine{
		"unconfigured service": slackTestRouter(),
		"no service":           NewRouter(Dependencies{Logger: logger}),
	} {
		t.Run(name, func(t *testing.T) {
			result := httptest.NewRecorder()
			router.ServeHTTP(result, signedSlackInteractionRequest("payload=%7B%7D"))
			if result.Code != http.StatusNotImplemented || !strings.Contains(result.Body.String(), `"code":"slack_not_configured"`) {
				t.Fatalf("unconfigured = %d %s", result.Code, result.Body)
			}
			assertSlackErrorEnvelope(t, result)
		})
	}
}

func signedSlackInteractionRequest(body string) *http.Request {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write([]byte("v0:" + timestamp + ":" + body))
	req := httptest.NewRequest(http.MethodPost, "/auth/slack/interactions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Request-Timestamp", timestamp)
	req.Header.Set("X-Slack-Signature", "v0="+hex.EncodeToString(mac.Sum(nil)))
	return req
}

func assertSlackErrorEnvelope(t *testing.T, result *httptest.ResponseRecorder) {
	t.Helper()
	var body struct {
		Error struct {
			Type      string `json:"type"`
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Type == "" || body.Error.Code == "" || body.Error.Message == "" || body.Error.RequestID == "" || body.Error.RequestID != result.Header().Get("X-Request-ID") {
		t.Fatalf("invalid error envelope: %s", result.Body)
	}
}

// The connection routes are product surface behind the session guard and
// never appear on the AAP wire.
func TestSlackConnectionRoutes(t *testing.T) {
	router := slackTestRouter()
	for _, test := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/enterprise/slack"},
		{http.MethodPost, "/api/v1/enterprise/slack/install"},
		{http.MethodDelete, "/api/v1/enterprise/slack"},
		{http.MethodPost, "/api/v1/me/slack/link"},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 from the session guard", recorder.Code)
			}
		})
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/aap/v1/enterprise/slack", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("AAP surface exposes the Slack connection: %d", recorder.Code)
	}
}

// The callback is reached cross-site from slack.com without a session, so
// it must be routable unauthenticated and must land the admin back where
// the install began, as the state names, with a stable outcome code.
func TestSlackCallbackRedirectsWithAnOutcome(t *testing.T) {
	router := slackTestRouter()
	for _, test := range []struct {
		name     string
		query    string
		expected string
	}{
		{"declined in Slack", "?error=access_denied&state=s", "/settings/integrations?slack_error=access_denied"},
		{"deployment without Slack", "?state=s&code=c", "/settings/integrations?slack_error=slack_not_configured"},
		{"declined during the guided setup", "?error=access_denied&state=onboarding.s", "/welcome?slack_error=access_denied"},
		{"guided setup on a deployment without Slack", "?state=onboarding.s&code=c", "/welcome?slack_error=slack_not_configured"},
		{"a state naming another page", "?error=access_denied&state=https:%2F%2Fevil.example.s", "/settings/integrations?slack_error=access_denied"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/auth/slack/callback"+test.query, nil))
			if recorder.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302", recorder.Code)
			}
			if location := recorder.Header().Get("Location"); location != test.expected {
				t.Fatalf("redirect = %q, want %q", location, test.expected)
			}
		})
	}
}

// Slack's own error text must never reach the browser, only the code.
func TestSlackCallbackKeepsProviderDetailOutOfTheRedirect(t *testing.T) {
	router := slackTestRouter()
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/auth/slack/callback?error=invalid_scope_leaked_detail", nil))
	if location := recorder.Header().Get("Location"); strings.Contains(location, "leaked") {
		t.Fatalf("provider detail leaked into the redirect: %s", location)
	}
}
