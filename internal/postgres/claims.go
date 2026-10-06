package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/authorization"
)

// serializationFailureCode is PostgreSQL's "could not serialize access"
// class, raised when a serializable transaction loses a race it can safely
// retry from a fresh snapshot.
const serializationFailureCode = "40001"

// claimWriteAttempts bounds the retries below. Two reviewers claiming the
// same instant, and a claim racing the decision or expiry that closes the
// request, both surface as transient PostgreSQL errors rather than
// answers. Retrying from a fresh snapshot turns them into the real
// outcome: the live claim, a conflict, or a terminal request.
const claimWriteAttempts = 3

func isClaimWriteRace(err error) bool {
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) {
		return false
	}
	return pgError.Code == serializationFailureCode || pgError.Code == uniqueViolationCode
}

// retryClaimWrite reruns a serializable claim transaction while it keeps
// losing races. A race that outlives the attempts is reported as a
// conflict, never as a raw driver error, so callers always see a mapped
// status.
func retryClaimWrite(attempt func() error) error {
	var err error
	for try := 0; try < claimWriteAttempts; try++ {
		if err = attempt(); !isClaimWriteRace(err) {
			return err
		}
	}
	return fmt.Errorf("someone else changed this request. Try again: %w", domain.ErrConflict)
}

// ClaimApproval claims a pending request for the principal. The whole claim
// is one serializable transaction mirroring ApplyDecision: the request row
// is locked, terminal state rejects, the claim kernel runs against the
// pinned routing snapshot, stale open claims are lapsed in place (which
// also backfills the lapse audit event when the workflow timer signal was
// lost), and the insert plus audit event commit together. Claiming an
// already-held request returns the live claim for the same claimant and
// ErrConflict for anyone else; there is no takeover.
func (s *Store) ClaimApproval(ctx context.Context, input application.ClaimApprovalInput) (domain.Claim, error) {
	if err := input.Validate(); err != nil {
		return domain.Claim{}, err
	}
	var claim domain.Claim
	err := retryClaimWrite(func() error {
		var attemptError error
		claim, attemptError = s.claimApproval(ctx, input)
		return attemptError
	})
	if err != nil {
		return domain.Claim{}, err
	}
	return claim, nil
}

func (s *Store) claimApproval(ctx context.Context, input application.ClaimApprovalInput) (domain.Claim, error) {
	organizationID, requestID, principal, now := input.OrganizationID, input.RequestID, input.Principal, input.Now
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return domain.Claim{}, err
	}
	defer tx.Rollback(ctx)

	var requestStatus string
	var deadlineAt time.Time
	err = tx.QueryRow(ctx, `SELECT r.status,r.deadline_at
		FROM approval_requests r WHERE r.organization_id=$1 AND r.id=$2 FOR UPDATE`, organizationID, requestID).Scan(
		&requestStatus, &deadlineAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Claim{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Claim{}, err
	}
	if domain.ApprovalStatus(requestStatus).Terminal() {
		return domain.Claim{}, domain.ErrAlreadyTerminal
	}

	var membershipStatus, displayName string
	err = tx.QueryRow(ctx, `SELECT m.status,u.display_name
      FROM memberships m JOIN users u ON u.id=m.user_id
     WHERE m.organization_id=$1 AND m.id=$2`, organizationID, principal.MembershipID).Scan(
		&membershipStatus, &displayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Claim{}, domain.ErrForbidden
	}
	if err != nil {
		return domain.Claim{}, err
	}

	var retriggerSeconds int64
	var unroutedRequests string
	if err := tx.QueryRow(ctx, `SELECT claim_retrigger_seconds, unrouted_requests FROM organization_policies
        WHERE organization_id=$1`, organizationID).Scan(&retriggerSeconds, &unroutedRequests); err != nil {
		return domain.Claim{}, err
	}
	routingTargets, err := decisionRoutingTargets(ctx, tx, organizationID, requestID, principal.MembershipID)
	if err != nil {
		return domain.Claim{}, err
	}
	// Grants are read inside the transaction so the check sees the same
	// assignments the claim is written against.
	grants, err := loadGrants(ctx, tx, organizationID, domain.PrincipalMembership, principal.MembershipID)
	if err != nil {
		return domain.Claim{}, err
	}
	if rejection := authorization.EvaluateClaim(authorization.ClaimInput{
		MembershipStatus:     domain.MembershipStatus(membershipStatus),
		Grants:               grants,
		RequestID:            requestID,
		ReviewerMembershipID: principal.MembershipID,
		HasRoutingTargets:    routingTargets.hasTargets,
		RoutedUserIDs:        routingTargets.userIDs,
		RoutedTeamIDs:        routingTargets.teamIDs,
		AddedReviewerIDs:     routingTargets.addedReviewerIDs,
		ReviewerTeamIDs:      routingTargets.reviewerTeamIDs,
		UnroutedRequests:     domain.UnroutedRequestsPolicy(unroutedRequests),
	}); rejection != nil {
		return domain.Claim{}, rejection.Err
	}

	if err := s.lapseStaleClaims(ctx, tx, organizationID, requestID, now); err != nil {
		return domain.Claim{}, err
	}

	var openID, openMembershipID string
	var openClaimedAt, openExpiresAt time.Time
	err = tx.QueryRow(ctx, `SELECT id::text,membership_id::text,claimed_at,expires_at
        FROM approval_request_claims
       WHERE organization_id=$1 AND request_id=$2 AND released_at IS NULL FOR UPDATE`,
		organizationID, requestID).Scan(&openID, &openMembershipID, &openClaimedAt, &openExpiresAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.Claim{}, err
	}
	if err == nil {
		if domain.ID(openMembershipID) != principal.MembershipID {
			return domain.Claim{}, fmt.Errorf("another reviewer already claimed this request: %w", domain.ErrConflict)
		}
		// Idempotent re-claim: return the live claim unchanged, without a
		// fresh audit event. Extending expiry would turn the claim into an
		// unbounded keep-alive lease and defeat the retrigger timeout.
		if err := tx.Commit(ctx); err != nil {
			return domain.Claim{}, err
		}
		return domain.Claim{
			ID: domain.ID(openID), RequestID: requestID, OrganizationID: organizationID,
			MembershipID: principal.MembershipID, ClaimantDisplayName: displayName,
			ClaimedAt: openClaimedAt, ExpiresAt: openExpiresAt,
		}, nil
	}

	expiresAt := now.Add(time.Duration(retriggerSeconds) * time.Second)
	if deadlineAt.Before(expiresAt) {
		expiresAt = deadlineAt
	}
	if !expiresAt.After(now) {
		return domain.Claim{}, fmt.Errorf("%w: it expired", domain.ErrAlreadyTerminal)
	}
	claim := domain.Claim{
		ID: domain.ID(uuid.NewString()), RequestID: requestID, OrganizationID: organizationID,
		MembershipID: principal.MembershipID, ClaimantDisplayName: displayName,
		ClaimedAt: now, ExpiresAt: expiresAt,
	}
	if _, err := tx.Exec(ctx, `INSERT INTO approval_request_claims
		(id,request_id,organization_id,membership_id,claimed_at,expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		claim.ID, requestID, organizationID, principal.MembershipID, now, expiresAt); err != nil {
		return domain.Claim{}, err
	}
	actorID, err := membershipActorID(ctx, tx, organizationID, principal.MembershipID)
	if err != nil {
		return domain.Claim{}, err
	}
	data, _ := json.Marshal(map[string]any{
		"claim_id": claim.ID, "membership_id": principal.MembershipID, "expires_at": expiresAt,
	})
	if err := s.appendAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: organizationID, EventType: "routing.claimed",
		ActorType: domain.ActorHuman, ActorID: actorID,
		SubjectType: "approval_request", SubjectID: requestID,
		Data: data, OccurredAt: now,
	}); err != nil {
		return domain.Claim{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Claim{}, err
	}
	return claim, nil
}

// ReleaseClaim manually releases the request's open claim. Only the
// claimant may release; there is no takeover of a live claim.
func (s *Store) ReleaseClaim(ctx context.Context, input application.ReleaseClaimInput) (domain.ID, error) {
	if err := input.Validate(); err != nil {
		return "", err
	}
	var claimID domain.ID
	err := retryClaimWrite(func() error {
		var err error
		claimID, err = s.releaseClaim(ctx, input)
		return err
	})
	return claimID, err
}

func (s *Store) releaseClaim(ctx context.Context, input application.ReleaseClaimInput) (domain.ID, error) {
	organizationID, requestID, now := input.OrganizationID, input.RequestID, input.Now
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var claimID, membershipID string
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `SELECT id::text,membership_id::text,expires_at FROM approval_request_claims
       WHERE organization_id=$1 AND request_id=$2 AND released_at IS NULL FOR UPDATE`,
		organizationID, requestID).Scan(&claimID, &membershipID, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if !expiresAt.After(now) {
		// The claim already lapsed; release it as such and report no live
		// claim to the caller.
		if err := s.lapseStaleClaims(ctx, tx, organizationID, requestID, now); err != nil {
			return "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		return "", domain.ErrNotFound
	}
	if domain.ID(membershipID) != input.MembershipID {
		return "", fmt.Errorf("only the reviewer who claimed this request can release it: %w", domain.ErrForbidden)
	}
	if _, err := tx.Exec(ctx, `UPDATE approval_request_claims
        SET released_at=$3,release_reason='released' WHERE organization_id=$1 AND id=$2`,
		organizationID, claimID, now); err != nil {
		return "", err
	}
	data, _ := json.Marshal(map[string]any{"claim_id": claimID, "membership_id": membershipID})
	if err := s.appendAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: organizationID, EventType: "routing.claim_released",
		ActorType: domain.ActorHuman, ActorID: input.ActorID,
		SubjectType: "approval_request", SubjectID: requestID,
		Data: data, OccurredAt: now,
	}); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return domain.ID(claimID), nil
}

// LapseClaim lapses one specific claim when its retrigger timer fires. The
// compare-and-set is keyed by claim id, so a claim already released for any
// reason, a duplicate workflow timer, or a retried activity is a clean
// no-op that can never touch a newer claim on the same request.
func (s *Store) LapseClaim(ctx context.Context, input application.LapseClaimInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `UPDATE approval_request_claims
        SET released_at=$3,release_reason='lapsed'
      WHERE organization_id=$1 AND id=$2 AND released_at IS NULL`,
		input.OrganizationID, input.ClaimID, input.Now)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	data, _ := json.Marshal(map[string]any{"claim_id": input.ClaimID})
	if err := s.appendAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "routing.claim_lapsed",
		ActorType:   domain.ActorSystem,
		SubjectType: "approval_request", SubjectID: input.RequestID,
		Data: data, OccurredAt: input.Now,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// lapseStaleClaims releases every open claim on the request whose expiry
// already passed, emitting the routing.claim_lapsed audit event for each.
// This is both the unique-index unblocker before a fresh claim insert and
// the audit backfill for the case where the workflow's lapse timer signal
// never arrived.
func (s *Store) lapseStaleClaims(ctx context.Context, tx pgx.Tx, organizationID, requestID domain.ID, now time.Time) error {
	rows, err := tx.Query(ctx, `UPDATE approval_request_claims
        SET released_at=$3,release_reason='lapsed'
      WHERE organization_id=$1 AND request_id=$2 AND released_at IS NULL AND expires_at <= $3
      RETURNING id::text`, organizationID, requestID, now)
	if err != nil {
		return err
	}
	var lapsedIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		lapsedIDs = append(lapsedIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range lapsedIDs {
		data, _ := json.Marshal(map[string]any{"claim_id": id})
		if err := s.appendAuditEvent(ctx, tx, domain.AuditEvent{
			OrganizationID: organizationID, EventType: "routing.claim_lapsed",
			ActorType:   domain.ActorSystem,
			SubjectType: "approval_request", SubjectID: requestID,
			Data: data, OccurredAt: now,
		}); err != nil {
			return err
		}
	}
	return nil
}

// closeOpenClaims closes the request's open claims inside a decision or
// expiry transaction. It writes no audit event of its own: the adjacent
// approval.decided or approval.expired event records the cause.
func closeOpenClaims(ctx context.Context, tx execer, organizationID, requestID domain.ID, reason domain.ClaimReleaseReason, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE approval_request_claims
        SET released_at=$3,release_reason=$4
      WHERE organization_id=$1 AND request_id=$2 AND released_at IS NULL`,
		organizationID, requestID, now, reason)
	return err
}
