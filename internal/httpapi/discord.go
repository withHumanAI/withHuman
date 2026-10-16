package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/discordconnect"
)

// The Discord server connection is enterprise surface gated by
// connection.manage in the service; linking your own account is
// self-service under /me. Both callbacks live under /auth because the
// browser returns from discord.com cross-site, where the strict session
// cookie is not sent; the OAuth state, not the session, identifies who
// began the flow.
func (a *API) installDiscordRoutes(auth, app *gin.RouterGroup) {
	a.handle(auth, http.MethodGet, "/discord/callback", AccessPublic, a.discordCallback)
	a.handle(auth, http.MethodGet, "/discord/link/callback", AccessPublic, a.discordLinkCallback)
	a.handle(app, http.MethodGet, "/enterprise/discord", string(domain.PermissionConnectionRead), a.getDiscordConnection)
	a.handle(app, http.MethodPost, "/enterprise/discord/install", string(domain.PermissionConnectionWrite), a.beginDiscordInstall)
	a.handle(app, http.MethodDelete, "/enterprise/discord", string(domain.PermissionConnectionWrite), a.disconnectDiscord)
	a.handle(app, http.MethodPost, "/me/discord/link", AccessSelf, a.beginDiscordLink)
	a.handle(app, http.MethodDelete, "/me/discord", AccessSelf, a.unlinkDiscord)
}

type discordConnectionResponse struct {
	GuildID                 string    `json:"guild_id"`
	GuildName               string    `json:"guild_name"`
	InstalledByMembershipID domain.ID `json:"installed_by_membership_id"`
	InstalledAt             time.Time `json:"installed_at"`
	LinkedMembers           int       `json:"linked_members"`
}

func newDiscordConnectionResponse(connection domain.DiscordConnection) discordConnectionResponse {
	return discordConnectionResponse{
		GuildID: connection.GuildID, GuildName: connection.GuildName,
		InstalledByMembershipID: connection.InstalledByMembershipID, InstalledAt: connection.InstalledAt,
		LinkedMembers: connection.LinkedMembers,
	}
}

func (a *API) getDiscordConnection(c *gin.Context) {
	connection, err := a.discord.Get(c.Request.Context(), currentPrincipal(c))
	if errors.Is(err, domain.ErrNotFound) {
		// Not connected is a state the page renders, not an error.
		c.JSON(http.StatusOK, gin.H{"connection": nil})
		return
	}
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"connection": newDiscordConnectionResponse(connection)})
}

func (a *API) beginDiscordInstall(c *gin.Context) {
	returnTo, ok := bindReturnTo(c)
	if !ok {
		return
	}
	authorizeURL, err := a.discord.Begin(c.Request.Context(), currentPrincipal(c), returnTo)
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"authorize_url": authorizeURL})
}

func (a *API) disconnectDiscord(c *gin.Context) {
	if err := a.discord.Disconnect(c.Request.Context(), currentPrincipal(c)); err != nil {
		a.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *API) beginDiscordLink(c *gin.Context) {
	returnTo, ok := bindReturnTo(c)
	if !ok {
		return
	}
	authorizeURL, err := a.discord.BeginLink(c.Request.Context(), currentPrincipal(c), returnTo)
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"authorize_url": authorizeURL})
}

func (a *API) unlinkDiscord(c *gin.Context) {
	if err := a.discord.Unlink(c.Request.Context(), currentPrincipal(c)); err != nil {
		a.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// discordCallback lands the admin back where the install began: the
// Integrations page or the guided setup, as the state names. Discord's
// own error detail stays in the log; the browser gets a stable code the
// page can explain.
func (a *API) discordCallback(c *gin.Context) {
	if declined := c.Query("error"); declined != "" {
		redirectOAuthOutcome(c, c.Query("state"), "/settings/integrations", "discord_error", "access_denied")
		return
	}
	_, err := a.discord.Complete(c.Request.Context(), c.Query("state"), c.Query("code"), c.Query("guild_id"))
	if err != nil {
		code := "discord_rejected"
		switch {
		case errors.Is(err, discordconnect.ErrNotConfigured):
			code = "discord_not_configured"
		case errors.Is(err, domain.ErrConflict):
			code = "guild_taken"
		case errors.Is(err, discordconnect.ErrStateExpired):
			code = "install_expired"
		}
		a.logger.WarnContext(c.Request.Context(), "discord install failed", "error", err)
		redirectOAuthOutcome(c, c.Query("state"), "/settings/integrations", "discord_error", code)
		return
	}
	redirectOAuthOutcome(c, c.Query("state"), "/settings/integrations", "discord", "connected")
}

// discordLinkCallback lands the person back where the link began: their
// Notifications page or the guided setup. Its refusals use their own key,
// discord_link_error, so a page can tell a failed link from a failed
// install.
func (a *API) discordLinkCallback(c *gin.Context) {
	if declined := c.Query("error"); declined != "" {
		redirectOAuthOutcome(c, c.Query("state"), "/settings/notifications", "discord_link_error", "access_denied")
		return
	}
	_, err := a.discord.CompleteLink(c.Request.Context(), c.Query("state"), c.Query("code"))
	if err != nil {
		code := "discord_rejected"
		switch {
		case errors.Is(err, discordconnect.ErrNotConfigured):
			code = "discord_not_configured"
		case errors.Is(err, discordconnect.ErrNotGuildMember):
			code = "not_a_member"
		case errors.Is(err, discordconnect.ErrStateExpired):
			code = "link_expired"
		case errors.Is(err, domain.ErrConflict):
			code = "account_taken"
		case errors.Is(err, domain.ErrNotFound):
			code = "no_connection"
		}
		a.logger.WarnContext(c.Request.Context(), "discord link failed", "error", err)
		redirectOAuthOutcome(c, c.Query("state"), "/settings/notifications", "discord_link_error", code)
		return
	}
	redirectOAuthOutcome(c, c.Query("state"), "/settings/notifications", "discord", "linked")
}
