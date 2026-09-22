package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/onixus/metis/internal/kernel"
)

// Config — параметры воркера.
type Config struct {
	BatchSize    int           // размер пачки; по умолчанию 100
	PollInterval time.Duration // пауза, когда очередь пуста; по умолчанию 1 с
	BaseBackoff  time.Duration // задержка первого повтора; по умолчанию 1 с
	MaxBackoff   time.Duration // потолок задержки; по умолчанию 10 мин
	MaxAttempts  int           // после этого числа попыток — DLQ; по умолчанию 10
}

func (c Config) withDefaults() Config {
	if c.BatchSize <= 0 {
		c.BatchSize = 100
	}
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
	if c.BaseBackoff <= 0 {
		c.BaseBackoff = time.Second
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 10 * time.Minute
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 10
	}
	return c
}

// Worker доставляет события из outbox обработчикам по Event.Type (несколько на тип).
// Гарантия — at-least-once; обработчики идемпотентны по Event.ID.
type Worker struct {
	store    Store
	clock    kernel.Clock
	cfg      Config
	log      *slog.Logger
	mu       sync.RWMutex
	handlers map[string][]kernel.Handler
}

// NewWorker создаёт воркер. Нулевые поля cfg заменяются значениями по умолчанию.
func NewWorker(store Store, clock kernel.Clock, cfg Config, log *slog.Logger) *Worker {
	if log == nil {
		log = slog.Default()
	}
	return &Worker{store: store, clock: clock, cfg: cfg.withDefaults(), log: log, handlers: map[string][]kernel.Handler{}}
}

// Register добавляет обработчик типа события. Вызывается до Run.
func (w *Worker) Register(eventType string, h kernel.Handler) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.handlers[eventType] = append(w.handlers[eventType], h)
}

// Run крутит цикл выборки до отмены контекста. Отмена контекста — штатное завершение (nil).
func (w *Worker) Run(ctx context.Context) error {
	for {
		n, err := w.RunOnce(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			w.log.ErrorContext(ctx, "outbox: ошибка выборки", slog.String("error", err.Error()))
		}
		wait := w.cfg.PollInterval
		if n > 0 && err == nil {
			wait = 0
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

// RunOnce обрабатывает одну пачку. Возвращает число захваченных сообщений.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	now := w.clock.Now()
	n, err := w.store.Process(ctx, now, w.cfg.BatchSize, func(ctx context.Context, m Message) Outcome {
		return w.handle(ctx, m, now)
	})
	if err != nil {
		return 0, fmt.Errorf("outbox process: %w", err)
	}
	return n, nil
}

func (w *Worker) handle(ctx context.Context, m Message, now time.Time) Outcome {
	w.mu.RLock()
	hs := w.handlers[m.Event.Type]
	w.mu.RUnlock()
	var errs []error
	if len(hs) == 0 {
		errs = append(errs, fmt.Errorf("%w: обработчик события %q не зарегистрирован", kernel.ErrUnavailable, m.Event.Type))
	}
	for _, h := range hs {
		if err := h.Handle(ctx, m.Event); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return Outcome{Kind: Ack}
	}
	err := errors.Join(errs...)
	attempt := m.Attempts + 1
	if attempt >= w.cfg.MaxAttempts {
		w.log.ErrorContext(ctx, "outbox: событие перенесено в DLQ",
			slog.String("event_id", m.Event.ID.String()), slog.String("type", m.Event.Type),
			slog.Int("attempts", attempt), slog.String("error", err.Error()))
		return Outcome{Kind: Dead, Error: err.Error()}
	}
	next := now.Add(w.Backoff(attempt))
	w.log.WarnContext(ctx, "outbox: повтор доставки",
		slog.String("event_id", m.Event.ID.String()), slog.String("type", m.Event.Type),
		slog.Int("attempt", attempt), slog.Time("next_attempt_at", next), slog.String("error", err.Error()))
	return Outcome{Kind: Retry, NextAttemptAt: next, Error: err.Error()}
}

// Backoff — экспоненциальная задержка перед попыткой attempt+1: base·2^(attempt-1), не больше MaxBackoff.
func (w *Worker) Backoff(attempt int) time.Duration {
	d := w.cfg.BaseBackoff
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= w.cfg.MaxBackoff {
			return w.cfg.MaxBackoff
		}
	}
	return d
}

// DLQCount — число сообщений в DLQ (для администратора, AD-05).
func (w *Worker) DLQCount(ctx context.Context) (int64, error) { return w.store.DLQCount(ctx) }

// DLQList — последние сообщения DLQ.
func (w *Worker) DLQList(ctx context.Context, limit int) ([]DeadMessage, error) {
	return w.store.DLQList(ctx, limit)
}

// Requeue возвращает сообщение из DLQ в очередь с нулевым счётчиком попыток.
func (w *Worker) Requeue(ctx context.Context, id kernel.ID) error {
	return w.store.Requeue(ctx, id, w.clock.Now())
}
