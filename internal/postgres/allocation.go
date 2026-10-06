package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
)

// Allocation quotas are enforced here, inside the transaction that
// allocates, never in the service that resolved the limit: a count made
// outside the insert's transaction is a race, and two concurrent
// allocations that each see one seat free would both succeed.
//
// The lock is a session-level advisory lock per organization and
// dimension, taken on the connection before the serializable transaction
// begins. Taking it inside the transaction would not do: a serializable
// snapshot is fixed by the first statement, so a transaction that waited
// for the lock would still count with a snapshot from before the holder
// committed, and PostgreSQL would abort it. Held before the snapshot, the
// count after the lock sees every committed allocation.

const serializableAttempts = 5

func isSerializationFailure(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == serializationFailureCode
}

// retrySerializable reruns a serializable transaction while it loses
// snapshot races with unrelated writers. A race that outlives the attempts
// is a conflict, never a raw driver error.
func retrySerializable(attempt func() error) error {
	var err error
	for try := 0; try < serializableAttempts; try++ {
		if err = attempt(); !isSerializationFailure(err) {
			return err
		}
	}
	return fmt.Errorf("something else changed this at the same time. Try again: %w", domain.ErrConflict)
}

// allocation is one allocating transaction with its lock. Close releases
// everything whether or not Commit ran.
type allocation struct {
	conn      *pgxpool.Conn
	tx        pgx.Tx
	lockKey   string
	committed bool
}

// beginAllocation takes the organization's lock for the dimension when the
// quota is limited, then opens the serializable transaction. An unlimited
// quota takes no lock: nothing will be counted.
func (s *Store) beginAllocation(ctx context.Context, dimension application.Dimension, organizationID string, quota application.AllocationQuota) (*allocation, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	a := &allocation{conn: conn}
	if !quota.Unlimited() {
		a.lockKey = string(dimension) + ":" + organizationID
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, a.lockKey); err != nil {
			conn.Release()
			return nil, err
		}
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		a.unlock(ctx)
		conn.Release()
		return nil, err
	}
	a.tx = tx
	return a, nil
}

func (a *allocation) Tx() pgx.Tx { return a.tx }

func (a *allocation) Commit(ctx context.Context) error {
	if err := a.tx.Commit(ctx); err != nil {
		return err
	}
	a.committed = true
	return nil
}

// Close rolls back an uncommitted transaction, releases the lock, and
// returns the connection to the pool. The unlock runs even when the
// context is done: a session lock that leaks with its connection would
// block every later allocation for the organization.
func (a *allocation) Close(ctx context.Context) {
	if !a.committed {
		_ = a.tx.Rollback(ctx)
	}
	a.unlock(context.WithoutCancel(ctx))
	a.conn.Release()
}

func (a *allocation) unlock(ctx context.Context) {
	if a.lockKey == "" {
		return
	}
	if _, err := a.conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, a.lockKey); err != nil {
		// The connection is unusable; destroying it releases the session
		// lock server-side.
		a.conn.Conn().Close(ctx)
	}
}

// countSeats is what a seat quota counts: active memberships plus pending,
// unexpired invitations, each of which reserves a seat. One invitation
// can be excluded so the transaction accepting or renewing it does not
// count it against itself.
func countSeats(ctx context.Context, tx pgx.Tx, organizationID string, now time.Time, excludeInvitationID string) (int64, error) {
	var count int64
	err := tx.QueryRow(ctx, `SELECT
        (SELECT count(*) FROM memberships WHERE organization_id = $1 AND status = 'active')
      + (SELECT count(*) FROM invitations WHERE organization_id = $1 AND status = 'pending'
           AND expires_at > $2 AND id::text <> $3)`,
		organizationID, now, excludeInvitationID).Scan(&count)
	return count, err
}

// enforceSeatQuota refuses the allocation when the organization already
// holds every seat its quota allows. The caller holds the seat lock
// through beginAllocation.
func enforceSeatQuota(ctx context.Context, tx pgx.Tx, organizationID string, quota application.AllocationQuota, now time.Time, excludeInvitationID string) error {
	if quota.Unlimited() {
		return nil
	}
	used, err := countSeats(ctx, tx, organizationID, now, excludeInvitationID)
	if err != nil {
		return err
	}
	return quota.Check(application.DimensionSeats, used)
}

// enforceAgentQuota is the agent counterpart: active agents only.
func enforceAgentQuota(ctx context.Context, tx pgx.Tx, organizationID string, quota application.AllocationQuota) error {
	if quota.Unlimited() {
		return nil
	}
	var used int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM agents WHERE organization_id = $1 AND status = 'active'`,
		organizationID).Scan(&used); err != nil {
		return err
	}
	return quota.Check(application.DimensionAgents, used)
}
