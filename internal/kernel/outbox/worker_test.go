package outbox_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/outbox"
)

type stepClock struct{ t time.Time }

func (c *stepClock) Now() time.Time { return c.t }

func TestNFR06_WorkerRetriesThenDLQ(t *testing.T) {
	ctx := context.Background()
	clock := &stepClock{t: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)}
	store := outbox.NewMemStore()
	pub := outbox.NewPublisher(store, clock)

	ok, err := kernel.NewEvent(clock, "test.ok", kernel.NewID(), kernel.NewID(), "tester", map[string]int{"n": 1})
	if err != nil {
		t.Fatal(err)
	}
	bad, err := kernel.NewEvent(clock, "test.bad", kernel.NewID(), kernel.NewID(), "tester", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(ctx, ok, bad); err != nil {
		t.Fatal(err)
	}

	var okCalls, badCalls atomic.Int32
	w := outbox.NewWorker(store, clock, outbox.Config{MaxAttempts: 3, BaseBackoff: time.Second, MaxBackoff: time.Minute}, nil)
	w.Register("test.ok", kernel.HandlerFunc(func(_ context.Context, ev kernel.Event) error {
		okCalls.Add(1)
		return nil
	}))
	// Два обработчика на тип: первый успешен, второй падает — событие повторяется целиком.
	w.Register("test.bad", kernel.HandlerFunc(func(context.Context, kernel.Event) error { return nil }))
	w.Register("test.bad", kernel.HandlerFunc(func(context.Context, kernel.Event) error {
		badCalls.Add(1)
		return errors.New("внешняя система недоступна")
	}))

	// Попытка 1: ok доставлено, bad -> retry через 1 с.
	if n, err := w.RunOnce(ctx); err != nil || n != 2 {
		t.Fatalf("RunOnce: n=%d err=%v", n, err)
	}
	if okCalls.Load() != 1 || badCalls.Load() != 1 {
		t.Fatalf("вызовы: ok=%d bad=%d", okCalls.Load(), badCalls.Load())
	}
	// Ещё не время повтора.
	if n, _ := w.RunOnce(ctx); n != 0 {
		t.Fatalf("до next_attempt_at выбрано %d сообщений", n)
	}
	// Попытка 2 через 1 с; следующая задержка 2 с (экспонента).
	clock.t = clock.t.Add(time.Second)
	if n, _ := w.RunOnce(ctx); n != 1 {
		t.Fatalf("попытка 2: выбрано %d", n)
	}
	clock.t = clock.t.Add(time.Second)
	if n, _ := w.RunOnce(ctx); n != 0 {
		t.Fatalf("задержка должна быть 2 с, выбрано %d", n)
	}
	clock.t = clock.t.Add(time.Second)
	// Попытка 3 = MaxAttempts -> DLQ.
	if n, _ := w.RunOnce(ctx); n != 1 {
		t.Fatalf("попытка 3: выбрано %d", n)
	}
	if badCalls.Load() != 3 {
		t.Fatalf("ожидалось 3 вызова, было %d", badCalls.Load())
	}
	cnt, err := w.DLQCount(ctx)
	if err != nil || cnt != 1 {
		t.Fatalf("DLQCount=%d err=%v", cnt, err)
	}
	dead, err := w.DLQList(ctx, 10)
	if err != nil || len(dead) != 1 || dead[0].Event.ID != bad.ID || dead[0].Attempts != 3 || dead[0].Error == "" {
		t.Fatalf("DLQList: %+v err=%v", dead, err)
	}
	if store.Pending() != 0 {
		t.Fatalf("очередь должна быть пуста, осталось %d", store.Pending())
	}

	// Requeue возвращает событие в очередь с нулевым счётчиком.
	if err := w.Requeue(ctx, bad.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.Requeue(ctx, bad.ID); !kernel.IsNotFound(err) {
		t.Fatalf("повторный Requeue должен вернуть ErrNotFound, получено %v", err)
	}
	if cnt, _ := w.DLQCount(ctx); cnt != 0 {
		t.Fatalf("DLQ после Requeue: %d", cnt)
	}
	if n, _ := w.RunOnce(ctx); n != 1 || badCalls.Load() != 4 {
		t.Fatalf("после Requeue: n=%d calls=%d", n, badCalls.Load())
	}
}

func TestNFR06_WorkerRunStopsOnCancel(t *testing.T) {
	store := outbox.NewMemStore()
	w := outbox.NewWorker(store, kernel.SystemClock{}, outbox.Config{PollInterval: 10 * time.Millisecond}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := w.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestNFR06_PublisherRejectsInvalidEvent(t *testing.T) {
	pub := outbox.NewPublisher(outbox.NewMemStore(), kernel.SystemClock{})
	err := pub.Publish(context.Background(), kernel.Event{Type: "x"})
	if !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("ожидалась ошибка валидации, получено %v", err)
	}
}
