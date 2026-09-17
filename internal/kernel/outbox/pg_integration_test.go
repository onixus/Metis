//go:build integration

package outbox_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/migrate"
	"github.com/onixus/metis/internal/kernel/outbox"
	"github.com/onixus/metis/internal/kernel/pgdb"
)

func openTestDB(t *testing.T) *pgdb.DB {
	t.Helper()
	url := os.Getenv("METIS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("METIS_TEST_DATABASE_URL не задан: интеграционный тест пропущен (docs/questions.md №05)")
	}
	ctx := context.Background()
	db, err := pgdb.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrate.Up(ctx, db.Pool(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, "TRUNCATE kernel.outbox, kernel.outbox_dlq"); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestNFR06_OutboxDeliversAndRetriesToDLQ(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	clock := &stepClock{t: time.Now().UTC().Truncate(time.Microsecond)}
	store := outbox.NewPGStore(db)
	pub := outbox.NewPublisher(store, clock)

	ok, _ := kernel.NewEvent(clock, "it.ok", kernel.NewID(), kernel.NewID(), "tester", map[string]string{"k": "v"})
	bad, _ := kernel.NewEvent(clock, "it.bad", kernel.NewID(), kernel.NilID, "tester", nil)

	// Публикация в транзакции доменной записи: откат транзакции не оставляет события.
	rolled, _ := kernel.NewEvent(clock, "it.ok", kernel.NewID(), kernel.NewID(), "tester", nil)
	errRollback := errors.New("откат")
	err := db.Transact(ctx, func(ctx context.Context) error {
		if err := pub.Publish(ctx, rolled); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("Transact: %v", err)
	}
	if err := db.Transact(ctx, func(ctx context.Context) error { return pub.Publish(ctx, ok, bad) }); err != nil {
		t.Fatal(err)
	}

	var okGot kernel.Event
	badCalls := 0
	w := outbox.NewWorker(store, clock, outbox.Config{MaxAttempts: 2, BaseBackoff: time.Second}, nil)
	w.Register("it.ok", kernel.HandlerFunc(func(_ context.Context, ev kernel.Event) error { okGot = ev; return nil }))
	w.Register("it.bad", kernel.HandlerFunc(func(ctx context.Context, ev kernel.Event) error {
		badCalls++
		// Обработчик пишет в БД в транзакции пачки; сбой не должен ломать транзакцию (savepoint).
		if _, err := pgdb.Querier(ctx, db).Exec(ctx, "SELECT 1/0"); err != nil {
			return err
		}
		return nil
	}))

	if n, err := w.RunOnce(ctx); err != nil || n != 2 {
		t.Fatalf("RunOnce 1: n=%d err=%v", n, err)
	}
	if okGot.ID != ok.ID || string(okGot.Payload) != `{"k": "v"}` && string(okGot.Payload) != `{"k":"v"}` {
		t.Fatalf("ok не доставлено корректно: %+v", okGot)
	}
	if n, _ := w.RunOnce(ctx); n != 0 {
		t.Fatalf("до next_attempt_at выбрано %d", n)
	}
	clock.t = clock.t.Add(2 * time.Second)
	if n, err := w.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("RunOnce 2: n=%d err=%v", n, err)
	}
	if badCalls != 2 {
		t.Fatalf("вызовов bad: %d", badCalls)
	}
	cnt, err := w.DLQCount(ctx)
	if err != nil || cnt != 1 {
		t.Fatalf("DLQCount=%d err=%v", cnt, err)
	}
	dead, err := w.DLQList(ctx, 10)
	if err != nil || len(dead) != 1 || dead[0].Event.ID != bad.ID || dead[0].Attempts != 2 || dead[0].Error == "" {
		t.Fatalf("DLQList: %+v err=%v", dead, err)
	}
	var pending int
	if err := db.Pool().QueryRow(ctx, "SELECT count(*) FROM kernel.outbox").Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("outbox не пуст: %d err=%v", pending, err)
	}
	if err := w.Requeue(ctx, bad.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.Requeue(ctx, bad.ID); !kernel.IsNotFound(err) {
		t.Fatalf("повторный Requeue: %v", err)
	}
	if n, _ := w.RunOnce(ctx); n != 1 || badCalls != 3 {
		t.Fatalf("после Requeue: n=%d calls=%d", n, badCalls)
	}
}
