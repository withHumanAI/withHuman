package postgres

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
)

// Onboarding sessions. One row is the shared
// state between a person's browser and the CLI on their machine. Every
// write goes through updateOnboardingSession, which checks the status
// precondition, applies the patch, and bumps the version inside one
// statement, so the two sides can never interleave a stale write.

const onboardingColumns = `id::text, organization_id::text, membership_id::text, status, version,
	machine, detections, selected_runtimes, discovered_tools, discovery_errors,
	generated, enrollments, install_results, test_commands, test_request_id::text, tested_at, channels_set_at, rules_set_at,
	last_error,
	created_at, updated_at, expires_at, completed_at`

func scanOnboardingSession(row rowScanner) (domain.OnboardingSession, error) {
	var session domain.OnboardingSession
	var id, organizationID, membershipID, status string
	var testRequestID *string
	var machine, detections, tools, discoveryErrors, generated, enrollments, installResults, testCommands, lastError []byte
	err := row.Scan(&id, &organizationID, &membershipID, &status, &session.Version,
		&machine, &detections, &session.SelectedRuntimes, &tools, &discoveryErrors,
		&generated, &enrollments, &installResults, &testCommands, &testRequestID, &session.TestedAt, &session.ChannelsSetAt, &session.RulesSetAt,
		&lastError,
		&session.CreatedAt, &session.UpdatedAt, &session.ExpiresAt, &session.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OnboardingSession{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.OnboardingSession{}, err
	}
	session.ID, session.OrganizationID, session.MembershipID = domain.ID(id), domain.ID(organizationID), domain.ID(membershipID)
	session.Status = domain.OnboardingStatus(status)
	if testRequestID != nil {
		requestID := domain.ID(*testRequestID)
		session.TestRequestID = &requestID
	}
	for _, pair := range []struct {
		raw    []byte
		target any
	}{
		{machine, &session.Machine}, {detections, &session.Detections}, {tools, &session.Tools},
		{discoveryErrors, &session.DiscoveryErrors},
		{generated, &session.Generated}, {enrollments, &session.Enrollments},
		{installResults, &session.InstallResults}, {testCommands, &session.TestCommands},
	} {
		if len(pair.raw) == 0 {
			continue
		}
		if err := json.Unmarshal(pair.raw, pair.target); err != nil {
			return domain.OnboardingSession{}, fmt.Errorf("decode onboarding session %s: %w", id, err)
		}
	}
	if len(lastError) > 0 && string(lastError) != "null" {
		var decoded domain.OnboardingError
		if err := json.Unmarshal(lastError, &decoded); err != nil {
			return domain.OnboardingSession{}, fmt.Errorf("decode onboarding session %s: %w", id, err)
		}
		session.LastError = &decoded
	}
	if session.SelectedRuntimes == nil {
		session.SelectedRuntimes = []string{}
	}
	return session, nil
}

func (s *Store) CreateOnboardingSession(ctx context.Context, input application.CreateOnboardingSessionInput) (domain.OnboardingSession, error) {
	if err := input.Validate(); err != nil {
		return domain.OnboardingSession{}, err
	}
	session := input.Session
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.OnboardingSession{}, err
	}
	defer tx.Rollback(ctx)
	// Supersede the member's open session, if any, so the partial unique
	// index admits the new one. The superseded row keeps its history.
	if _, err := tx.Exec(ctx, `UPDATE onboarding_sessions
		SET status = 'skipped', completed_at = $3, updated_at = $3, version = version + 1,
		    last_error = '{"source":"server","code":"superseded","message":"A newer setup session replaced this one."}'::jsonb
		WHERE organization_id = $1 AND membership_id = $2 AND status NOT IN ('completed', 'skipped')`,
		session.OrganizationID, session.MembershipID, session.CreatedAt); err != nil {
		return domain.OnboardingSession{}, err
	}
	machine, _ := json.Marshal(session.Machine)
	if _, err := tx.Exec(ctx, `INSERT INTO onboarding_sessions
		(id, organization_id, membership_id, status, version, setup_code_hash, setup_code_expires_at,
		 machine, created_at, updated_at, expires_at)
		VALUES ($1, $2, $3, 'awaiting_cli', 1, $4, $5, $6, $7, $7, $8)`,
		session.ID, session.OrganizationID, session.MembershipID, input.SetupCodeHash, input.SetupCodeExpiresAt,
		machine, session.CreatedAt, session.ExpiresAt); err != nil {
		if isForeignKeyViolation(err) {
			return domain.OnboardingSession{}, fmt.Errorf("membership does not belong to the organization: %w", domain.ErrNotFound)
		}
		return domain.OnboardingSession{}, err
	}
	data, _ := json.Marshal(map[string]any{"session_id": session.ID})
	if err := s.appendAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: session.OrganizationID, EventType: "onboarding.started",
		ActorType: domain.ActorHuman, ActorID: session.MembershipID,
		SubjectType: "onboarding_session", SubjectID: session.ID,
		Data: data, OccurredAt: session.CreatedAt,
	}); err != nil {
		return domain.OnboardingSession{}, err
	}
	created, err := scanOnboardingSession(tx.QueryRow(ctx, `SELECT `+onboardingColumns+` FROM onboarding_sessions WHERE id = $1`, session.ID))
	if err != nil {
		return domain.OnboardingSession{}, err
	}
	return created, tx.Commit(ctx)
}

func (s *Store) GetOnboardingSession(ctx context.Context, input application.GetOnboardingSessionInput) (domain.OnboardingSession, error) {
	if err := input.Validate(); err != nil {
		return domain.OnboardingSession{}, err
	}
	return scanOnboardingSession(s.pool.QueryRow(ctx, `SELECT `+onboardingColumns+`
		FROM onboarding_sessions WHERE organization_id = $1 AND id = $2`, input.OrganizationID, input.SessionID))
}

func (s *Store) GetOpenOnboardingSession(ctx context.Context, input application.GetOpenOnboardingSessionInput) (domain.OnboardingSession, error) {
	if err := input.Validate(); err != nil {
		return domain.OnboardingSession{}, err
	}
	return scanOnboardingSession(s.pool.QueryRow(ctx, `SELECT `+onboardingColumns+`
		FROM onboarding_sessions
		WHERE organization_id = $1 AND membership_id = $2 AND status NOT IN ('completed', 'skipped')`,
		input.OrganizationID, input.MembershipID))
}

// ExchangeOnboardingSetupCode is the CLI's first call. The code is spent
// in the same statement that stores the bearer's hash, so a replayed code
// finds no row.
func (s *Store) ExchangeOnboardingSetupCode(ctx context.Context, input application.ExchangeOnboardingSetupCodeInput) (domain.OnboardingSession, error) {
	if err := input.Validate(); err != nil {
		return domain.OnboardingSession{}, err
	}
	machine, _ := json.Marshal(input.Machine)
	session, err := scanOnboardingSession(s.pool.QueryRow(ctx, `UPDATE onboarding_sessions
		SET status = 'connected', version = version + 1, updated_at = $2,
		    setup_code_hash = NULL, credential_secret_hash = $3, machine = $4
		WHERE setup_code_hash = $1 AND status = 'awaiting_cli'
		  AND setup_code_expires_at > $2 AND expires_at > $2
		RETURNING `+onboardingColumns, input.SetupCodeHash, input.Now, input.CredentialSecretHash, machine))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.OnboardingSession{}, domain.ErrUnauthenticated
	}
	return session, err
}

// ResolveOnboardingCredential authenticates a who_ bearer with a live
// lookup: the session must be open and unexpired, and its organization and
// membership both active, so suspending either cuts the CLI off at once.
func (s *Store) ResolveOnboardingCredential(ctx context.Context, input application.ResolveOnboardingCredentialInput) (domain.OnboardingSession, error) {
	if err := input.Validate(); err != nil {
		return domain.OnboardingSession{}, err
	}
	var storedHash []byte
	err := s.pool.QueryRow(ctx, `SELECT s.credential_secret_hash
		FROM onboarding_sessions s
		JOIN organizations o ON o.id = s.organization_id AND o.status = 'active'
		JOIN memberships m ON m.id = s.membership_id AND m.organization_id = s.organization_id AND m.status = 'active'
		WHERE s.id = $1 AND s.status NOT IN ('completed', 'skipped') AND s.expires_at > $2
		  AND s.credential_secret_hash IS NOT NULL`, input.SessionID, input.Now).Scan(&storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OnboardingSession{}, domain.ErrUnauthenticated
	}
	if err != nil {
		return domain.OnboardingSession{}, err
	}
	if subtle.ConstantTimeCompare(storedHash, input.SecretHash) != 1 {
		return domain.OnboardingSession{}, domain.ErrUnauthenticated
	}
	return scanOnboardingSession(s.pool.QueryRow(ctx, `SELECT `+onboardingColumns+` FROM onboarding_sessions WHERE id = $1`, input.SessionID))
}

// UpdateOnboardingSession is the one write path for open sessions. The
// status precondition, the patch, the status change, and the version bump
// happen in one statement; zero rows affected is then disambiguated into
// not found, expired, or a status conflict.
func (s *Store) UpdateOnboardingSession(ctx context.Context, input application.UpdateOnboardingSessionInput) (domain.OnboardingSession, error) {
	if err := input.Validate(); err != nil {
		return domain.OnboardingSession{}, err
	}
	sets := []string{"version = version + 1", "updated_at = $3"}
	args := []any{input.OrganizationID, input.SessionID, input.Now}
	bind := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	if input.NextStatus != "" {
		sets = append(sets, "status = "+bind(string(input.NextStatus)))
	}
	patch := input.Patch
	if patch.Detections != nil {
		sets = append(sets, "detections = "+bind(mustJSON(*patch.Detections, "[]")))
	}
	if patch.SelectedRuntimes != nil {
		runtimes := *patch.SelectedRuntimes
		if runtimes == nil {
			runtimes = []string{}
		}
		sets = append(sets, "selected_runtimes = "+bind(runtimes))
	}
	if patch.Tools != nil {
		sets = append(sets, "discovered_tools = "+bind(mustJSON(*patch.Tools, "{}")))
	}
	if patch.DiscoveryErrors != nil {
		sets = append(sets, "discovery_errors = "+bind(mustJSON(*patch.DiscoveryErrors, "{}")))
	}
	if patch.Generated != nil {
		sets = append(sets, "generated = "+bind(mustJSON(*patch.Generated, "{}")))
	}
	if patch.Enrollments != nil {
		sets = append(sets, "enrollments = "+bind(mustJSON(*patch.Enrollments, "[]")))
	}
	if patch.InstallResults != nil {
		sets = append(sets, "install_results = "+bind(mustJSON(*patch.InstallResults, "[]")))
	}
	if patch.TestCommands != nil {
		sets = append(sets, "test_commands = "+bind(mustJSON(*patch.TestCommands, "{}")))
	}
	if patch.TestRequestID != nil {
		sets = append(sets, "test_request_id = "+bind(string(*patch.TestRequestID)))
	}
	if patch.TestedAt != nil {
		sets = append(sets, "tested_at = "+bind(*patch.TestedAt))
	}
	if patch.ChannelsSetAt != nil {
		sets = append(sets, "channels_set_at = "+bind(*patch.ChannelsSetAt))
	}
	if patch.RulesSetAt != nil {
		sets = append(sets, "rules_set_at = "+bind(*patch.RulesSetAt))
	}
	switch {
	case patch.LastError != nil:
		sets = append(sets, "last_error = "+bind(mustJSON(*patch.LastError, "null")))
	case patch.ClearLastError:
		sets = append(sets, "last_error = NULL")
	}
	where := "organization_id = $1 AND id = $2 AND status NOT IN ('completed', 'skipped') AND expires_at > $3"
	if len(input.ExpectedStatuses) > 0 {
		expected := make([]string, len(input.ExpectedStatuses))
		for index, status := range input.ExpectedStatuses {
			expected[index] = string(status)
		}
		where += " AND status = ANY(" + bind(expected) + ")"
	}
	session, err := scanOnboardingSession(s.pool.QueryRow(ctx, `UPDATE onboarding_sessions SET `+strings.Join(sets, ", ")+`
		WHERE `+where+` RETURNING `+onboardingColumns, args...))
	if !errors.Is(err, domain.ErrNotFound) {
		return session, err
	}
	// Zero rows: tell the caller which precondition failed.
	current, lookupErr := s.GetOnboardingSession(ctx, application.GetOnboardingSessionInput{OrganizationID: input.OrganizationID, SessionID: input.SessionID})
	if lookupErr != nil {
		return domain.OnboardingSession{}, lookupErr
	}
	if current.Expired(input.Now) {
		return domain.OnboardingSession{}, domain.ErrOnboardingExpired
	}
	return domain.OnboardingSession{}, domain.ErrOnboardingState
}

func mustJSON(value any, empty string) []byte {
	raw, err := json.Marshal(value)
	if err != nil || string(raw) == "null" {
		return []byte(empty)
	}
	return raw
}

// WaitOnboardingSession polls once a second until the version moves past
// SinceVersion or the timeout elapses, then returns the current row. The
// same shape the review queue's SSE stream uses; the table is tiny.
func (s *Store) WaitOnboardingSession(ctx context.Context, input application.WaitOnboardingSessionInput) (domain.OnboardingSession, error) {
	if err := input.Validate(); err != nil {
		return domain.OnboardingSession{}, err
	}
	deadline := time.Now().Add(input.Timeout)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		session, err := s.GetOnboardingSession(ctx, application.GetOnboardingSessionInput{OrganizationID: input.OrganizationID, SessionID: input.SessionID})
		if err != nil {
			return domain.OnboardingSession{}, err
		}
		if session.Version > input.SinceVersion || !time.Now().Before(deadline) {
			return session, nil
		}
		select {
		case <-ctx.Done():
			return session, nil
		case <-ticker.C:
		}
	}
}

// CompleteOnboarding closes the member's open session with the outcome
// and marks the organization done the first time. Finished requires the
// open session, when one exists, to have reached tested.
func (s *Store) CompleteOnboarding(ctx context.Context, input application.CompleteOnboardingInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var sessionID, status string
	err = tx.QueryRow(ctx, `SELECT id::text, status FROM onboarding_sessions
		WHERE organization_id = $1 AND membership_id = $2 AND status NOT IN ('completed', 'skipped') FOR UPDATE`,
		input.OrganizationID, input.MembershipID).Scan(&sessionID, &status)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	terminal := domain.OnboardingSkipped
	if input.Outcome == domain.OnboardingFinished {
		terminal = domain.OnboardingCompleted
		if sessionID != "" && domain.OnboardingStatus(status) != domain.OnboardingTested {
			return fmt.Errorf("%w: send a test request first", domain.ErrOnboardingState)
		}
	}
	if sessionID != "" {
		if _, err := tx.Exec(ctx, `UPDATE onboarding_sessions
			SET status = $2, completed_at = $3, updated_at = $3, version = version + 1 WHERE id = $1`,
			sessionID, string(terminal), input.Now); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE organizations SET onboarding_completed_at = COALESCE(onboarding_completed_at, $2), updated_at = $2
		WHERE id = $1`, input.OrganizationID, input.Now); err != nil {
		return err
	}
	actor := input.ActorMembershipID
	if actor == "" {
		actor = input.MembershipID
	}
	data, _ := json.Marshal(map[string]any{"outcome": input.Outcome, "session_id": sessionID})
	if err := s.appendAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "onboarding.completed",
		ActorType: domain.ActorHuman, ActorID: actor,
		SubjectType: "organization", SubjectID: input.OrganizationID,
		Data: data, OccurredAt: input.Now,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
