package isureal

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// commitWithReplicaBarrier releases the business transaction's row locks before
// waiting for replica replay. A barrier error cannot undo the business commit;
// callers must not retry or compensate the mutation automatically.
func commitWithReplicaBarrier(ctx context.Context, db *sqlx.DB, tx *sqlx.Tx) error {
	if os.Getenv("ISUREAL_COMMIT_BARRIER") != "1" {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL synchronous_commit=local`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	var barrierLSN string
	// The materialized volatile CTE sets remote_apply in this implicit
	// transaction, even when the pooled connection defaults to local.
	// Scan consumes/closes the result and includes autocommit completion.
	if err := db.QueryRowxContext(ctx, `
WITH sync_mode AS MATERIALIZED (
  SELECT set_config('synchronous_commit', 'remote_apply', true)
)
INSERT INTO public.replica_commit_barriers (id)
SELECT $1::uuid FROM sync_mode
RETURNING pg_current_wal_insert_lsn()::text`, uuid.NewString()).Scan(&barrierLSN); err != nil {
		return fmt.Errorf("business committed; replica barrier failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("business committed; replica barrier canceled: %w", err)
	}
	var applied bool
	// A SyncRep query cancellation can produce WARNING and a nil Scan error.
	// Independently require both fixed replicas to have replayed the business
	// commit (which precedes barrierLSN); missing/hidden stats fail closed.
	if err := db.GetContext(ctx, &applied, `
SELECT count(DISTINCT application_name) = 2
FROM pg_stat_replication
WHERE application_name IN ('europe1', 'america1')
  AND state = 'streaming' AND replay_lsn >= $1::pg_lsn`, barrierLSN); err != nil {
		return fmt.Errorf("business committed; replica replay verification failed: %w", err)
	}
	if !applied {
		return fmt.Errorf("business committed; replica replay not confirmed")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("business committed; replica replay verification canceled: %w", err)
	}
	return nil
}
