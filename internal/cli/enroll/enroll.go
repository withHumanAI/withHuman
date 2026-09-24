// Package enroll implements browser-authorized instance enrollment.
package enroll

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/cli/credential"
)

type Options struct {
	BaseURL, Runtime, RequestedAgentSlug, InstanceName string
	HTTP                                               *http.Client
	Stdout                                             io.Writer
	OpenBrowser                                        func(context.Context, string) error
	PollWait                                           time.Duration
}

func HTTPClient(given *http.Client) *http.Client {
	var client http.Client
	if given != nil {
		client = *given
	}
	if client.Timeout == 0 {
		client.Timeout = 30 * time.Second
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}

// Accepts reports whether the saved credential still authenticates, by
// reading the instance's own identity document. Nothing about which
// calls to hold comes from the provider: every intercepted call is sent
// and the provider's pipeline decides.
func Accepts(ctx context.Context, client *http.Client, cred credential.Credential) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cred.ProviderURL+"/api/v1/agent_instances/self", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+cred.Token)
	resp, err := client.Do(req)
	if err != nil {
		return false, errors.New("could not check the saved credential: provider unavailable")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return false, nil
	default:
		return false, fmt.Errorf("could not check the saved credential: HTTP %d", resp.StatusCode)
	}
}
func openBrowser(ctx context.Context, url string) error {
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(bounded, command, url).Run()
}
func Enroll(ctx context.Context, opts Options) (credential.Credential, error) {
	var empty credential.Credential
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	opts.HTTP = HTTPClient(opts.HTTP)
	if opts.PollWait == 0 {
		opts.PollWait = 3 * time.Second
	}
	if opts.OpenBrowser == nil {
		opts.OpenBrowser = openBrowser
	}
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "agent"
	}
	if opts.InstanceName == "" {
		opts.InstanceName = hostname + "-" + uuid.NewString()[:8]
	}
	raw, err := postJSON(ctx, opts.HTTP, opts.BaseURL+"/auth/agent_enrollments", map[string]any{"runtime": opts.Runtime, "requested_agent_slug": opts.RequestedAgentSlug, "instance_name": opts.InstanceName, "instance_metadata": map[string]any{"hostname": hostname, "os": runtime.GOOS}})
	if err != nil {
		return empty, fmt.Errorf("begin enrollment: %w", err)
	}
	var start struct {
		EnrollmentCode  string    `json:"enrollment_code"`
		UserCode        string    `json:"user_code"`
		VerificationURI string    `json:"verification_uri"`
		ExpiresAt       time.Time `json:"expires_at"`
	}
	if json.Unmarshal(raw, &start) != nil || start.EnrollmentCode == "" || start.UserCode == "" || !start.ExpiresAt.After(time.Now()) || start.VerificationURI != opts.BaseURL+"/connect/authorize" {
		return empty, errors.New("provider returned an invalid enrollment")
	}
	fmt.Fprintf(opts.Stdout, "Authorize %s at %s with code %s.\n", opts.Runtime, start.VerificationURI, start.UserCode)
	if err := opts.OpenBrowser(ctx, start.VerificationURI+"?user_code="+url.QueryEscape(start.UserCode)); err != nil {
		fmt.Fprintln(opts.Stdout, "Open that address in your browser to continue.")
	}
	bounded, cancel := context.WithDeadline(ctx, start.ExpiresAt)
	defer cancel()
	for {
		timer := time.NewTimer(opts.PollWait)
		select {
		case <-bounded.Done():
			timer.Stop()
			return empty, fmt.Errorf("enrollment ended: %w", bounded.Err())
		case <-timer.C:
		}
		raw, err = postJSON(bounded, opts.HTTP, opts.BaseURL+"/auth/agent_enrollments/token", map[string]any{"enrollment_code": start.EnrollmentCode})
		if isPending(err) {
			continue
		}
		if err != nil {
			return empty, fmt.Errorf("exchange enrollment: %w", err)
		}
		var issued domain.AgentEnrollmentCredential
		if json.Unmarshal(raw, &issued) != nil || issued.Credential.Token == "" || issued.AgentInstance.ID == "" || !issued.Agent.AgentSlug.Valid() || issued.Agent.Name == "" || issued.Agent.AgentSlug != issued.AgentInstance.AgentSlug || (opts.RequestedAgentSlug != "" && string(issued.Agent.AgentSlug) != opts.RequestedAgentSlug) || !issued.Credential.ExpiresAt.After(time.Now()) {
			return empty, errors.New("provider returned an invalid instance credential")
		}
		return credential.Credential{ProviderURL: opts.BaseURL, Token: issued.Credential.Token, CredentialID: string(issued.Credential.ID), ExpiresAt: issued.Credential.ExpiresAt, AgentSlug: string(issued.Agent.AgentSlug), AgentName: issued.Agent.Name, InstanceName: issued.AgentInstance.Name, Runtime: opts.Runtime}, nil
	}
}

// statusError distinguishes "still pending" (the provider answers 404
// until a human authorizes the user code) from real failures.
type statusError struct{ code int }

func (e *statusError) Error() string { return fmt.Sprintf("HTTP %d", e.code) }

func isPending(err error) bool {
	var s *statusError
	return errors.As(err, &s) && s.code == http.StatusNotFound
}

func postJSON(ctx context.Context, client *http.Client, url string, body map[string]any) (json.RawMessage, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, &statusError{resp.StatusCode}
	}
	return payload, nil
}
