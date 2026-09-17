// Package outbox — transactional outbox на PostgreSQL (ADR-0002, NF-R06, инвариант 5).
// Publisher пишет события в kernel.outbox в транзакции доменного изменения; Worker доставляет их
// зарегистрированным обработчикам не менее одного раза, с повторами и очередью недоставленных (DLQ).
package outbox

import (
	"context"
	"time"

	"github.com/onixus/metis/internal/kernel"
)

// Message — событие в outbox вместе с состоянием доставки.
type Message struct {
	Event         kernel.Event
	Attempts      int
	NextAttemptAt time.Time
	LastError     string
	CreatedAt     time.Time
}

// DeadMessage — событие в DLQ.
type DeadMessage struct {
	Event     kernel.Event
	Attempts  int
	Error     string
	CreatedAt time.Time
	FailedAt  time.Time
}

// OutcomeKind — решение воркера по сообщению.
type OutcomeKind int

const (
	// Ack — доставлено; запись удаляется.
	Ack OutcomeKind = iota
	// Retry — повторить после NextAttemptAt.
	Retry
	// Dead — перенести в DLQ.
	Dead
)

// Outcome — результат обработки одного сообщения.
type Outcome struct {
	Kind          OutcomeKind
	NextAttemptAt time.Time
	Error         string
}

// Store — хранилище outbox. PG-реализация держит пачку под FOR UPDATE SKIP LOCKED на время обработки.
type Store interface {
	// Enqueue добавляет события; в PG — в транзакцию из контекста.
	Enqueue(ctx context.Context, now time.Time, events ...kernel.Event) error
	// Process захватывает до limit сообщений с NextAttemptAt <= now, вызывает fn для каждого
	// и применяет результат. Возвращает число захваченных сообщений.
	Process(ctx context.Context, now time.Time, limit int, fn func(ctx context.Context, m Message) Outcome) (int, error)
	// DLQCount — число сообщений в DLQ.
	DLQCount(ctx context.Context) (int64, error)
	// DLQList — последние сообщения DLQ.
	DLQList(ctx context.Context, limit int) ([]DeadMessage, error)
	// Requeue возвращает сообщение из DLQ в очередь; kernel.ErrNotFound, если его нет.
	Requeue(ctx context.Context, id kernel.ID, now time.Time) error
}
