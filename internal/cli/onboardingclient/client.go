// Package onboardingclient speaks the machine half of the guided setup,
// /api/onboarding/v1, exactly as design doc 09 lays it out: exchange the
// setup code for a whb_ bearer, then take turns with the person in the
// browser through one shared session.
package onboardingclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/withHumanAI/withHuman/domain"
)

// Error is a provider refusal carrying the {error:{code,message}} envelope.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("HTTP %d", e.Status)
	}
	return fmt.Sprintf("%s: %s (HTTP %d)", e.Code, e.Message, e.Status)
}

// IsCode reports whether err is a provider error with the given code.
func IsCode(err error, code string) bool {
	var perr *Error
	return errors.As(err, &perr) && perr.Code == code
}

// Client holds a whb_ bearer for one session.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// ExchangeResult is the answer to a setup code.
type ExchangeResult struct {
	SessionID   string                   `json:"session_id"`
	Token       string                   `json:"token"`
	ExpiresAt   time.Time                `json:"expires_at"`
	ProviderURL string                   `json:"provider_url"`
	Session     domain.OnboardingSession `json:"session"`
}

// Exchange redeems a setup code. The returned client carries the bearer.
func Exchange(ctx context.Context, httpClient *http.Client, baseURL, setupCode string, machine domain.OnboardingMachine) (*Client, *ExchangeResult, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	client := &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: httpClient}
	var result ExchangeResult
	if err := client.do(ctx, http.MethodPost, "/api/onboarding/v1/sessions/exchange", map[string]any{
		"setup_code": setupCode,
		"machine":    machine,
	}, &result); err != nil {
		return nil, nil, err
	}
	client.Token = result.Token
	if result.ProviderURL != "" {
		client.BaseURL = strings.TrimRight(result.ProviderURL, "/")
	}
	return client, &result, nil
}

// Wait long-polls the session until its version passes since or wait
// elapses, whichever comes first.
func (c *Client) Wait(ctx context.Context, since int64, wait time.Duration) (*domain.OnboardingSession, error) {
	query := url.Values{"wait": {wait.String()}, "since": {fmt.Sprint(since)}}
	var session domain.OnboardingSession
	if err := c.do(ctx, http.MethodGet, "/api/onboarding/v1/session?"+query.Encode(), nil, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

// Session fetches the session without waiting.
func (c *Client) Session(ctx context.Context) (*domain.OnboardingSession, error) {
	var session domain.OnboardingSession
	if err := c.do(ctx, http.MethodGet, "/api/onboarding/v1/session", nil, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

// PutDetections reports every runtime's detection.
func (c *Client) PutDetections(ctx context.Context, detections []domain.OnboardingDetection) (*domain.OnboardingSession, error) {
	var session domain.OnboardingSession
	if err := c.do(ctx, http.MethodPut, "/api/onboarding/v1/session/detections", map[string]any{"runtimes": detections}, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

// PutTools reports the discovered tools per selected runtime.
func (c *Client) PutTools(ctx context.Context, tools map[string]domain.OnboardingReportedTools) (*domain.OnboardingSession, error) {
	var session domain.OnboardingSession
	if err := c.do(ctx, http.MethodPut, "/api/onboarding/v1/session/tools", map[string]any{"tools": tools}, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

// HookPlan is the per-runtime hook registration the provider computed.
type HookPlan struct {
	Matcher string   `json:"matcher"`
	Tools   []string `json:"tools"`
}

// Enrollment is one runtime's minted agent, instance, and credential.
type Enrollment struct {
	Runtime string `json:"runtime"`
	Agent   struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	} `json:"agent"`
	AgentInstance struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"agent_instance"`
	Credential struct {
		ID        string    `json:"id"`
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	} `json:"credential"`
}

// EnrollResult is the one response that hands raw credentials over.
type EnrollResult struct {
	ProviderURL string                   `json:"provider_url"`
	Enrollments []Enrollment             `json:"enrollments"`
	Hooks       map[string]HookPlan      `json:"hooks"`
	Session     domain.OnboardingSession `json:"session"`
}

// Enroll mints one agent instance per selected runtime.
func (c *Client) Enroll(ctx context.Context) (*EnrollResult, error) {
	var result EnrollResult
	if err := c.do(ctx, http.MethodPost, "/api/onboarding/v1/session/enroll", map[string]any{}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// PutInstallResults reports how wiring each runtime went.
func (c *Client) PutInstallResults(ctx context.Context, results []domain.OnboardingInstallResult) (*domain.OnboardingSession, error) {
	var session domain.OnboardingSession
	if err := c.do(ctx, http.MethodPut, "/api/onboarding/v1/session/install-results", map[string]any{"results": results}, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

// ReportError records a CLI-side failure on the session.
func (c *Client) ReportError(ctx context.Context, code, message string) (*domain.OnboardingSession, error) {
	var session domain.OnboardingSession
	if err := c.do(ctx, http.MethodPut, "/api/onboarding/v1/session/error", map[string]any{"code": code, "message": message}, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		perr := &Error{Status: resp.StatusCode}
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(payload, &envelope) == nil {
			perr.Code, perr.Message = envelope.Error.Code, envelope.Error.Message
		}
		return perr
	}
	if out == nil || len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("%s %s: decode response: %w", method, path, err)
	}
	return nil
}
