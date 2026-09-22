package pgdb

import (
	"context"
	"fmt"

	"github.com/onixus/metis/internal/kernel"
)

// LockApplication serializes pilot state transitions across API/worker processes.
// Acquire it before reading the graph, claiming outbox rows or appending audit.
// The lock belongs to the transaction and is released on commit/rollback.
// TODO(question-30): replace the coarse pilot lock with versioned graph snapshots
// and narrower aggregate locks after concurrent load testing.
func LockApplication(ctx context.Context, db *DB) error {
	if !InTx(ctx) {
		return fmt.Errorf("%w: application lock requires a transaction", kernel.ErrValidation)
	}
	if _, err := Querier(ctx, db).Exec(ctx, "SELECT pg_advisory_xact_lock(1296389193, 12)"); err != nil {
		return fmt.Errorf("application lock: %w", err)
	}
	return nil
}
