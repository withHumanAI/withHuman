package httpapi

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/slackconnect"
)

// The Slack workspace connection is enterprise surface: gated by
// connection.manage in the service, product API only. The callback lives
// under /auth because the browser returns from slack.com cross-site,
// where the strict session cookie is not sent; the install state, not
// the session, identifies the organization there.
func (a *API) installSlackRoutes(auth, app *gin.RouterGroup) {
	a.handle(auth, http.MethodGet, "/slack/callback", AccessPublic, a.slackCallback)
	a.handle(auth, http.MethodPost, "/slack/events", AccessPublic, a.slackEvents)
	a.handle(auth, http.MethodPost, "/slack/interactions", AccessPublic, a.slackInteractions)
	a.handle(app, http.MethodGet, "/enterprise/slack", string(domain.PermissionConnectionRead), a.getSlackConnection)
	a.handle(app, http.MethodPost, "/enterprise/slack/install", string(domain.PermissionConnectionWrite), a.beginSlackInstall)
	a.handle(app, http.MethodDelete, "/enterprise/slack", string(domain.PermissionConnectionWrite), a.disconnectSlack)
	a.handle(app, http.MethodPost, "/enterprise/slack/sync", string(domain.PermissionConnectionWrite), a.syncSlackMembers)
	a.handle(app, http.MethodPost, "/me/slack/link", AccessSelf, a.linkSlackSelf)
}

// linkSlackSelf matches the caller against the workspace by their own
// confirmed addresses, so a person can link Slack without an admin
// re-running the member sync.
func (a *API) linkSlackSelf(c *gin.Context) {
	method, err := a.slack.LinkSelf(c.Request.Context(), currentPrincipal(c))
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"contact_method": newContactMethodResponse(method)})
}

type slackConnectionResponse struct {
	Status                  string     `json:"status"`
	DisconnectedAt          *time.Time `json:"disconnected_at,omitempty"`
	DisconnectReason        string     `json:"disconnect_reason,omitempty"`
	TeamID                  string     `json:"team_id"`
	TeamName                string     `json:"team_name"`
	BotUserID               string     `json:"bot_user_id"`
	Scopes                  []string   `json:"scopes"`
	InstalledByMembershipID domain.ID  `json:"installed_by_membership_id"`
	InstalledAt             time.Time  `json:"installed_at"`
	LinkedMembers           int        `json:"linked_members"`
}

func newSlackConnectionResponse(connection domain.SlackConnection) slackConnectionResponse {
	scopes := connection.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return slackConnectionResponse{
		Status: connection.Status, DisconnectedAt: connection.DisconnectedAt, DisconnectReason: connection.DisconnectReason,
		TeamID: connection.TeamID, TeamName: connection.TeamName, BotUserID: connection.BotUserID,
		Scopes: scopes, InstalledByMembershipID: connection.InstalledByMembershipID, InstalledAt: connection.InstalledAt,
		LinkedMembers: connection.LinkedMembers,
	}
}

// syncSlackMembers re-runs the email match against the workspace, for
// members who joined or confirmed an address after the install.
func (a *API) syncSlackMembers(c *gin.Context) {
	result, err := a.slack.SyncMembers(c.Request.Context(), currentPrincipal(c))
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"members": result.Members, "linked": result.Linked})
}

func (a *API) getSlackConnection(c *gin.Context) {
	connection, err := a.slack.Get(c.Request.Context(), currentPrincipal(c))
	if errors.Is(err, domain.ErrNotFound) {
		// Not connected is a state the page renders, not an error.
		c.JSON(http.StatusOK, gin.H{"connection": nil})
		return
	}
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"connection": newSlackConnectionResponse(connection)})
}

func (a *API) beginSlackInstall(c *gin.Context) {
	returnTo, ok := bindReturnTo(c)
	if !ok {
		return
	}
	authorizeURL, err := a.slack.Begin(c.Request.Context(), currentPrincipal(c), returnTo)
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"authorize_url": authorizeURL})
}

func (a *API) disconnectSlack(c *gin.Context) {
	if err := a.slack.Disconnect(c.Request.Context(), currentPrincipal(c)); err != nil {
		a.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// slackCallback lands the admin back where the install began: the
// Integrations page or the guided setup, as the state names. Slack's own
// error detail stays in the log; the browser gets a stable code the page
// can explain.
func (a *API) slackCallback(c *gin.Context) {
	if declined := c.Query("error"); declined != "" {
		redirectOAuthOutcome(c, c.Query("state"), slackSettingsPage, "slack_error", "access_denied")
		return
	}
	_, err := a.slack.Complete(c.Request.Context(), c.Query("state"), c.Query("code"))
	if err != nil {
		code := "slack_rejected"
		switch {
		case errors.Is(err, slackconnect.ErrCheckUnavailable):
			code = "connection_check_unavailable"
		case errors.Is(err, slackconnect.ErrNotConfigured):
			code = "slack_not_configured"
		case errors.Is(err, domain.ErrConflict):
			code = "workspace_taken"
		case errors.Is(err, slackconnect.ErrInstallExpired):
			code = "install_expired"
		}
		a.logger.WarnContext(c.Request.Context(), "slack install failed", "error", err)
		redirectOAuthOutcome(c, c.Query("state"), slackSettingsPage, "slack_error", code)
		return
	}
	redirectOAuthOutcome(c, c.Query("state"), slackSettingsPage, "slack", "connected")
}

const slackSettingsPage = "/settings/integrations"

func (a *API) slackEvents(c *gin.Context) {
	if a.slack == nil {
		a.writeError(c, slackconnect.ErrNotConfigured)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
	if err != nil {
		a.writeError(c, domain.ErrInvalid)
		return
	}
	challenge, err := a.slack.ReceiveEvent(c.Request.Context(), c.GetHeader("X-Slack-Request-Timestamp"), c.GetHeader("X-Slack-Signature"), body)
	if err != nil {
		a.writeError(c, err)
		return
	}
	if challenge != "" {
		c.JSON(http.StatusOK, gin.H{"challenge": challenge})
		return
	}
	c.Status(http.StatusOK)
}

func (a *API) slackInteractions(c *gin.Context) {
	if a.slack == nil {
		a.writeError(c, slackconnect.ErrNotConfigured)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
	if err != nil {
		a.writeError(c, domain.ErrInvalid)
		return
	}
	if err := a.slack.ReceiveInteraction(c.GetHeader("X-Slack-Request-Timestamp"), c.GetHeader("X-Slack-Signature"), body); err != nil {
		a.writeError(c, err)
		return
	}
	c.Status(http.StatusOK)
}
