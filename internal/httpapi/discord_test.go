package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/internal/discordconnect"
)

func discordTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	unconfigured := discordconnect.NewService(nil, nil, nil, nil, application.RealClock{}, logger, discordconnect.Config{})
	return NewRouter(Dependencies{Logger: logger, Discord: unconfigured})
}

// The connection and link routes are product surface behind the session
// guard and never appear on the AAP wire.
func TestDiscordRoutes(t *testing.T) {
	router := discordTestRouter()
	for _, test := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/enterprise/discord"},
		{http.MethodPost, "/api/v1/enterprise/discord/install"},
		{http.MethodDelete, "/api/v1/enterprise/discord"},
		{http.MethodPost, "/api/v1/me/discord/link"},
		{http.MethodDelete, "/api/v1/me/discord"},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 from the session guard", recorder.Code)
			}
		})
	}
	for _, path := range []string{"/api/aap/v1/enterprise/discord", "/api/aap/v1/me/discord"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("AAP surface exposes %s: %d", path, recorder.Code)
		}
	}
}

// Both callbacks are reached cross-site from discord.com without a
// session, so they must be routable unauthenticated and land the browser
// where the flow began, as the state names, with a stable outcome code.
func TestDiscordCallbacksRedirectWithAnOutcome(t *testing.T) {
	router := discordTestRouter()
	for _, test := range []struct {
		name     string
		path     string
		expected string
	}{
		{"install declined", "/auth/discord/callback?error=access_denied&state=s", "/settings/integrations?discord_error=access_denied"},
		{"install on a deployment without Discord", "/auth/discord/callback?state=s&code=c&guild_id=g", "/settings/integrations?discord_error=discord_not_configured"},
		{"link declined", "/auth/discord/link/callback?error=access_denied&state=s", "/settings/notifications?discord_link_error=access_denied"},
		{"link on a deployment without Discord", "/auth/discord/link/callback?state=s&code=c", "/settings/notifications?discord_link_error=discord_not_configured"},
		{"install declined during the guided setup", "/auth/discord/callback?error=access_denied&state=onboarding.s", "/welcome?discord_error=access_denied"},
		{"link declined during the guided setup", "/auth/discord/link/callback?error=access_denied&state=onboarding.s", "/welcome?discord_link_error=access_denied"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
			if recorder.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302", recorder.Code)
			}
			if location := recorder.Header().Get("Location"); location != test.expected {
				t.Fatalf("redirect = %q, want %q", location, test.expected)
			}
		})
	}
}

// Discord's own error text must never reach the browser, only the code.
func TestDiscordCallbacksKeepProviderDetailOutOfTheRedirect(t *testing.T) {
	router := discordTestRouter()
	for _, path := range []string{"/auth/discord/callback", "/auth/discord/link/callback"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path+"?error=invalid_scope_leaked_detail", nil))
		if location := recorder.Header().Get("Location"); strings.Contains(location, "leaked") {
			t.Fatalf("provider detail leaked into the redirect: %s", location)
		}
	}
}
