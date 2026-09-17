package outbox

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/onixus/metis/internal/kernel"
)

// MemStore — хранилище outbox в памяти для unit-тестов и стендов без БД.
type MemStore struct {
	mu   sync.Mutex
	msgs map[kernel.ID]Message
	dead map[kernel.ID]DeadMessage
}

// NewMemStore создаёт пустое хранилище.
func NewMemStore() *MemStore {
	return &MemStore{msgs: map[kernel.ID]Message{}, dead: map[kernel.ID]DeadMessage{}}
}

// Enqueue добавляет события.
func (m *MemStore) Enqueue(_ context.Context, now time.Time, events ...kernel.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ev := range events {
		if _, dup := m.msgs[ev.ID]; dup {
			return kernel.ErrConflict
		}
		m.msgs[ev.ID] = Message{Event: ev, NextAttemptAt: now, CreatedAt: now}
	}
	return nil
}

// Process обрабатывает готовые сообщения.
func (m *MemStore) Process(ctx context.Context, now time.Time, limit int, fn func(ctx context.Context, m Message) Outcome) (int, error) {
	m.mu.Lock()
	ready := make([]Message, 0, limit)
	for _, msg := range m.msgs {
		if !msg.NextAttemptAt.After(now) {
			ready = append(ready, msg)
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		if !ready[i].NextAttemptAt.Equal(ready[j].NextAttemptAt) {
			return ready[i].NextAttemptAt.Before(ready[j].NextAttemptAt)
		}
		return ready[i].CreatedAt.Before(ready[j].CreatedAt)
	})
	if len(ready) > limit {
		ready = ready[:limit]
	}
	m.mu.Unlock()

	for _, msg := range ready {
		out := fn(ctx, msg)
		m.mu.Lock()
		switch out.Kind {
		case Ack:
			delete(m.msgs, msg.Event.ID)
		case Retry:
			msg.Attempts++
			msg.NextAttemptAt = out.NextAttemptAt
			msg.LastError = out.Error
			m.msgs[msg.Event.ID] = msg
		case Dead:
			delete(m.msgs, msg.Event.ID)
			m.dead[msg.Event.ID] = DeadMessage{Event: msg.Event, Attempts: msg.Attempts + 1, Error: out.Error, CreatedAt: msg.CreatedAt, FailedAt: now}
		}
		m.mu.Unlock()
	}
	return len(ready), nil
}

// DLQCount — число сообщений в DLQ.
func (m *MemStore) DLQCount(context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.dead)), nil
}

// DLQList — сообщения DLQ, новые первыми.
func (m *MemStore) DLQList(_ context.Context, limit int) ([]DeadMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]DeadMessage, 0, len(m.dead))
	for _, d := range m.dead {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FailedAt.After(out[j].FailedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Requeue возвращает сообщение в очередь.
func (m *MemStore) Requeue(_ context.Context, id kernel.ID, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.dead[id]
	if !ok {
		return kernel.NotFound("outbox_dlq", id)
	}
	delete(m.dead, id)
	m.msgs[id] = Message{Event: d.Event, NextAttemptAt: now, LastError: d.Error, CreatedAt: d.CreatedAt}
	return nil
}

// Pending — число сообщений в очереди (для тестов).
func (m *MemStore) Pending() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.msgs)
}
