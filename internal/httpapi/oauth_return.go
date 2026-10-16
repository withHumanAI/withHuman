package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/withHumanAI/withHuman/internal/oauthreturn"
)

// bindReturnTo reads the optional body of a request that begins an OAuth
// round trip: {"return_to": "settings" | "onboarding"}. No body means
// settings, the page the flow was started from.
func bindReturnTo(c *gin.Context) (oauthreturn.Page, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<10))
	if err != nil {
		writeBindError(c, err)
		return "", false
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return oauthreturn.Settings, true
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	var request struct {
		ReturnTo string `json:"return_to"`
	}
	if !bindStrict(c, &request) {
		return "", false
	}
	page, err := oauthreturn.Parse(request.ReturnTo)
	if err != nil {
		writeAPIError(c, http.StatusBadRequest, "invalid_request", "invalid_request", "return_to must be settings or onboarding.", nil)
		return "", false
	}
	return page, true
}

// redirectOAuthOutcome sends the browser back from a provider's callback
// to the page its state names, with the outcome as one query parameter.
// settingsPage is where the flow lives outside the guided setup.
func redirectOAuthOutcome(c *gin.Context, state, settingsPage, key, value string) {
	page := settingsPage
	if oauthreturn.FromState(state) == oauthreturn.Onboarding {
		page = "/welcome"
	}
	c.Redirect(http.StatusFound, page+"?"+key+"="+url.QueryEscape(value))
}
