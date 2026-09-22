package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onixus/metis/internal/kernel/pgdb"
)

func TestNFR06_CancelledRequestDoesNotWaitForBusyOperation(t *testing.T) {
	a := &App{db: &pgdb.DB{}, operationGate: make(chan struct{}, 1)}
	a.operationGate <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := a.runOperation(ctx, func(context.Context) error {
		t.Fatal("cancelled request entered a database operation")
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected cancellation while waiting: %v", err)
	}
}
