package httpapi

// Guided onboarding surfaces (design_doc/09-onboarding.md). Two trust
// boundaries share this file: the human routes under /api/v1/onboarding
// (session cookie, the onboarding permission set), and the machine
// surface under /api/onboarding/v1 that the withhuman CLI drives with the
// whb_ bearer it received for one setup code. Agent credentials cross
// exactly once, in the enroll response, and are never stored raw.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/onboarding"
)

const onboardingSessionKey = "withhuman.onboarding_session"

// assistantTurnTimeout bounds one setup assistant message: the model
// may read, write, and preview a few pipelines, each a round trip.
const assistantTurnTimeout = 4 * time.Minute

func (a *API) installOnboardingRoutes(appAPI *gin.RouterGroup, router *gin.Engine) {
	// The service requires every domain.OnboardingPermissions key; the
	// routes carry the first of them and the service enforces the set.
	setup := string(domain.OnboardingPermissions[0])
	a.handle(appAPI, http.MethodPost, "/onboarding/sessions", setup, a.beginOnboarding)
	a.handle(appAPI, http.MethodGet, "/onboarding/session", setup, a.getOnboardingSession)
	a.handle(appAPI, http.MethodGet, "/onboarding/session/events", setup, a.onboardingSessionEvents)
	a.handle(appAPI, http.MethodPut, "/onboarding/session/agents", setup, a.selectOnboardingAgents)
	a.handle(appAPI, http.MethodPost, "/onboarding/session/channels", setup, a.confirmOnboardingChannels)
	a.handle(appAPI, http.MethodPost, "/onboarding/session/provision", setup, a.provisionOnboarding)
	a.handle(appAPI, http.MethodPost, "/onboarding/session/assistant", setup, a.askOnboardingAssistant)
	a.handle(appAPI, http.MethodDelete, "/onboarding/session/assistant", setup, a.clearOnboardingAssistant)
	a.handle(appAPI, http.MethodPut, "/onboarding/session/gated-tools", setup, a.setOnboardingGatedTools)
	a.handle(appAPI, http.MethodPost, "/onboarding/session/rules", setup, a.recordOnboardingRules)
	a.handle(appAPI, http.MethodPost, "/onboarding/session/test", setup, a.recordOnboardingTest)
	a.handle(appAPI, http.MethodPost, "/onboarding/completion", setup, a.completeOnboarding)

	// The CLI surface acts on one session: the exchange is public (the
	// setup code is the authorization) and the rest is the session's own.
	machine := router.Group("/api/onboarding/v1")
	a.handle(machine, http.MethodPost, "/sessions/exchange", AccessPublic, a.exchangeOnboardingSetupCode)
	session := machine.Group("", a.onboardingAuth())
	a.handle(session, http.MethodGet, "/session", AccessSelf, a.waitOnboardingSession)
	a.handle(session, http.MethodPut, "/session/detections", AccessSelf, a.reportOnboardingDetections)
	a.handle(session, http.MethodPut, "/session/tools", AccessSelf, a.reportOnboardingTools)
	a.handle(session, http.MethodPost, "/session/enroll", AccessSelf, a.enrollOnboarding)
	a.handle(session, http.MethodPut, "/session/install-results", AccessSelf, a.reportOnboardingInstall)
	a.handle(session, http.MethodPut, "/session/error", AccessSelf, a.reportOnboardingError)
}

// onboardingAuth resolves the whb_ bearer to its session. The bearer is
// a grant, not an actor, so no principal is set: handlers act on the
// session and the service decides what the grant may do.
func (a *API) onboardingAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		authorization := c.GetHeader("Authorization")
		if !strings.HasPrefix(authorization, "Bearer ") {
			a.writeError(c, domain.ErrUnauthenticated)
			c.Abort()
			return
		}
		session, err := a.auth.ResolveOnboardingCredential(c.Request.Context(), strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer ")))
		if err != nil {
			a.writeError(c, err)
			c.Abort()
			return
		}
		c.Set(onboardingSessionKey, session)
		c.Next()
	}
}

func currentOnboardingSession(c *gin.Context) domain.OnboardingSession {
	value, _ := c.Get(onboardingSessionKey)
	session, _ := value.(domain.OnboardingSession)
	return session
}

// writeOnboardingError maps the flow's own failures onto the envelope and
// defers everything else to the shared mapping.
func (a *API) writeOnboardingError(c *gin.Context, err error) {
	var step *onboarding.StepError
	switch {
	case errors.As(err, &step):
		writeAPIError(c, http.StatusBadRequest, "invalid_request", step.Code, err.Error(), map[string]any{"param": step.Param})
	case errors.Is(err, onboarding.ErrProvisionFailed):
		writeAPIError(c, http.StatusBadGateway, "dependency", "onboarding_provision_failed", err.Error(), nil)
	case errors.Is(err, onboarding.ErrAssistantUnavailable):
		writeAPIError(c, http.StatusNotImplemented, "dependency", "setup_assistant_unconfigured", "The setup assistant needs a model: set WITHHUMAN_LLM_MODELS on this deployment.", nil)
	case errors.Is(err, onboarding.ErrAssistantBusy):
		writeAPIError(c, http.StatusConflict, "conflict", "onboarding_assistant_busy", err.Error(), nil)
	case errors.Is(err, onboarding.ErrAssistantFailed):
		writeAPIError(c, http.StatusBadGateway, "dependency", "onboarding_assistant_failed", err.Error(), nil)
	case errors.Is(err, onboarding.ErrEntryPipelineChanged):
		writeAPIError(c, http.StatusConflict, "conflict", "entry_pipeline_changed", "The entry pipeline changed while the rules were being written. Try again.", nil)
	default:
		a.writeError(c, err)
	}
}

func (a *API) beginOnboarding(c *gin.Context) {
	result, err := a.onboarding.Begin(c.Request.Context(), currentPrincipal(c))
	if err != nil {
		a.writeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"session": result.Session, "setup_code": result.SetupCode,
		"setup_command": result.SetupCommand,
	})
}

func (a *API) getOnboardingSession(c *gin.Context) {
	session, err := a.onboarding.Get(c.Request.Context(), currentPrincipal(c))
	if errors.Is(err, domain.ErrNotFound) {
		writeAPIError(c, http.StatusNotFound, "not_found", "onboarding_session_not_found", "No setup session is open. Start one to get a setup code.", nil)
		return
	}
	if err != nil {
		a.writeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, session)
}

// onboardingSessionEvents streams the person's open session whenever its
// version changes, the same poll-over-SSE shape as the review queue.
func (a *API) onboardingSessionEvents(c *gin.Context) {
	principal := currentPrincipal(c)
	if !onboarding.Authorized(principal) {
		a.writeError(c, domain.ErrForbidden)
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	lastFingerprint := ""
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		session, err := a.onboarding.Get(c.Request.Context(), principal)
		var payload []byte
		switch {
		case err == nil:
			payload, _ = json.Marshal(session)
		case errors.Is(err, domain.ErrNotFound):
			payload = []byte("null")
		default:
			return
		}
		if fingerprint := string(payload); fingerprint != lastFingerprint {
			fmt.Fprintf(c.Writer, "event: session\ndata: %s\n\n", payload)
			c.Writer.Flush()
			lastFingerprint = fingerprint
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-a.drain.streamsEnded():
			return
		case <-ticker.C:
			fmt.Fprint(c.Writer, ": keepalive\n\n")
			c.Writer.Flush()
		}
		// A session that expired or was revoked since the last tick ends
		// the stream; the browser's reconnect then meets the 401.
		resolved, err := resolvePrincipal(c, a.auth)
		if err != nil || !onboarding.Authorized(resolved) {
			return
		}
		principal = resolved
	}
}

func (a *API) selectOnboardingAgents(c *gin.Context) {
	var request struct {
		Runtimes []string `json:"runtimes"`
	}
	if !bind(c, &request) {
		return
	}
	session, err := a.onboarding.SelectAgents(c.Request.Context(), currentPrincipal(c), request.Runtimes)
	if err != nil {
		a.writeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, session)
}

// askOnboardingAssistant runs one assistant turn and answers with the
// session once it is done; the page follows the work in between over
// the events stream. A failed turn is recorded on the session too, so
// the error answer and the transcript agree.
func (a *API) askOnboardingAssistant(c *gin.Context) {
	var request struct {
		Message string `json:"message" binding:"required"`
	}
	if !bind(c, &request) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), assistantTurnTimeout)
	defer cancel()
	session, err := a.onboarding.Ask(ctx, currentPrincipal(c), request.Message)
	if err != nil {
		a.writeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, session)
}

func (a *API) clearOnboardingAssistant(c *gin.Context) {
	session, err := a.onboarding.ClearAssistant(c.Request.Context(), currentPrincipal(c))
	if err != nil {
		a.writeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, session)
}

// setOnboardingGatedTools is the by-hand alternative to the assistant:
// one runtime's rules become "these tools wait, the rest runs".
func (a *API) setOnboardingGatedTools(c *gin.Context) {
	var request struct {
		Runtime string   `json:"runtime" binding:"required"`
		Tools   []string `json:"tools"`
	}
	if !bind(c, &request) {
		return
	}
	session, err := a.onboarding.SetGatedTools(c.Request.Context(), currentPrincipal(c), request.Runtime, request.Tools)
	if err != nil {
		a.writeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, session)
}

func (a *API) recordOnboardingRules(c *gin.Context) {
	session, err := a.onboarding.RecordRules(c.Request.Context(), currentPrincipal(c))
	if err != nil {
		a.writeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, session)
}

func (a *API) provisionOnboarding(c *gin.Context) {
	session, err := a.onboarding.Provision(c.Request.Context(), currentPrincipal(c))
	if err != nil {
		a.writeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, session)
}

func (a *API) recordOnboardingTest(c *gin.Context) {
	var request struct {
		RequestID domain.ID `json:"request_id" binding:"required"`
	}
	if !bind(c, &request) {
		return
	}
	session, err := a.onboarding.RecordTest(c.Request.Context(), currentPrincipal(c), request.RequestID)
	if err != nil {
		a.writeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, session)
}

func (a *API) confirmOnboardingChannels(c *gin.Context) {
	session, err := a.onboarding.ConfirmChannels(c.Request.Context(), currentPrincipal(c))
	if err != nil {
		a.writeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, session)
}

func (a *API) completeOnboarding(c *gin.Context) {
	var request struct {
		Outcome domain.OnboardingOutcome `json:"outcome" binding:"required"`
	}
	if !bind(c, &request) {
		return
	}
	if err := a.onboarding.Complete(c.Request.Context(), currentPrincipal(c), request.Outcome); err != nil {
		a.writeOnboardingError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Machine surface.

func (a *API) exchangeOnboardingSetupCode(c *gin.Context) {
	var request struct {
		SetupCode string                   `json:"setup_code" binding:"required"`
		Machine   domain.OnboardingMachine `json:"machine"`
	}
	if !bind(c, &request) {
		return
	}
	result, err := a.onboarding.Exchange(c.Request.Context(), request.SetupCode, request.Machine)
	if errors.Is(err, domain.ErrUnauthenticated) {
		writeAPIError(c, http.StatusNotFound, "not_found", "setup_code_invalid", "That setup code is unknown, already used, or expired. Start again from the page.", nil)
		return
	}
	if err != nil {
		a.writeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"session_id": result.Session.ID, "token": result.Token, "expires_at": result.Session.ExpiresAt,
		"provider_url": result.ProviderURL, "session": result.Session,
	})
}

func (a *API) waitOnboardingSession(c *gin.Context) {
	session := currentOnboardingSession(c)
	wait := parseWait(c.Query("wait"), 30*time.Second)
	since := int64(queryInt(c, "since", 0))
	if wait == 0 || session.Version > since {
		c.JSON(http.StatusOK, session)
		return
	}
	updated, err := a.onboarding.Wait(c.Request.Context(), session, since, wait)
	if err != nil {
		a.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (a *API) reportOnboardingDetections(c *gin.Context) {
	var request struct {
		Runtimes []domain.OnboardingDetection `json:"runtimes"`
	}
	if !bind(c, &request) {
		return
	}
	session, err := a.onboarding.ReportDetections(c.Request.Context(), currentOnboardingSession(c), request.Runtimes)
	if err != nil {
		a.writeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, session)
}

func (a *API) reportOnboardingTools(c *gin.Context) {
	var request struct {
		Tools map[string]domain.OnboardingReportedTools `json:"tools"`
	}
	if !bind(c, &request) {
		return
	}
	session, err := a.onboarding.ReportTools(c.Request.Context(), currentOnboardingSession(c), request.Tools)
	if err != nil {
		a.writeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, session)
}

type onboardingEnrollmentView struct {
	Runtime       string                  `json:"runtime"`
	Agent         onboardingAgentView     `json:"agent"`
	AgentInstance onboardingInstanceView  `json:"agent_instance"`
	Credential    domain.IssuedCredential `json:"credential"`
}

type onboardingAgentView struct {
	Slug domain.AgentSlug `json:"slug"`
	Name string           `json:"name"`
}

type onboardingInstanceView struct {
	ID   domain.ID `json:"id"`
	Name string    `json:"name"`
}

func (a *API) enrollOnboarding(c *gin.Context) {
	result, err := a.onboarding.Enroll(c.Request.Context(), currentOnboardingSession(c))
	if err != nil {
		a.writeOnboardingError(c, err)
		return
	}
	enrollments := make([]onboardingEnrollmentView, 0, len(result.Enrollments))
	for _, enrollment := range result.Enrollments {
		enrollments = append(enrollments, onboardingEnrollmentView{
			Runtime:       enrollment.Runtime,
			Agent:         onboardingAgentView{Slug: enrollment.Agent.AgentSlug, Name: enrollment.Agent.Name},
			AgentInstance: onboardingInstanceView{ID: enrollment.AgentInstance.ID, Name: enrollment.AgentInstance.Name},
			Credential:    enrollment.Credential,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"provider_url": result.ProviderURL, "enrollments": enrollments,
		"hooks": result.Hooks, "session": result.Session,
	})
}

func (a *API) reportOnboardingInstall(c *gin.Context) {
	var request struct {
		Results []domain.OnboardingInstallResult `json:"results"`
	}
	if !bind(c, &request) {
		return
	}
	session, err := a.onboarding.ReportInstall(c.Request.Context(), currentOnboardingSession(c), request.Results)
	if err != nil {
		a.writeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, session)
}

func (a *API) reportOnboardingError(c *gin.Context) {
	var request struct {
		Code    string `json:"code"`
		Message string `json:"message" binding:"required"`
	}
	if !bind(c, &request) {
		return
	}
	session, err := a.onboarding.ReportError(c.Request.Context(), currentOnboardingSession(c), request.Code, request.Message)
	if err != nil {
		a.writeOnboardingError(c, err)
		return
	}
	c.JSON(http.StatusOK, session)
}
