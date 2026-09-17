package outbox

import (
	"context"
	"fmt"

	"github.com/onixus/metis/internal/kernel"
)

// Publisher — kernel.Publisher поверх Store. С PGStore запись идёт в транзакцию из контекста,
// то есть атомарно с доменным изменением (ADR-0002).
type Publisher struct {
	store Store
	clock kernel.Clock
}

// NewPublisher создаёт Publisher.
func NewPublisher(store Store, clock kernel.Clock) *Publisher {
	return &Publisher{store: store, clock: clock}
}

// Publish кладёт события в outbox.
func (p *Publisher) Publish(ctx context.Context, events ...kernel.Event) error {
	if len(events) == 0 {
		return nil
	}
	for _, ev := range events {
		if ev.ID == kernel.NilID || ev.Type == "" {
			return kernel.Invalid("event", "id и type обязательны")
		}
	}
	if err := p.store.Enqueue(ctx, p.clock.Now(), events...); err != nil {
		return fmt.Errorf("outbox publish: %w", err)
	}
	return nil
}
