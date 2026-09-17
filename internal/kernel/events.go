package kernel

import (
	"context"
	"encoding/json"
	"time"
)

// Event — доменное событие. Публикуется через outbox в той же транзакции, что и изменение.
type Event struct {
	ID          ID              `json:"id"`
	Type        string          `json:"type"`
	AggregateID ID              `json:"aggregate_id"`
	ProductID   ID              `json:"product_id"`
	OccurredAt  time.Time       `json:"occurred_at"`
	Actor       string          `json:"actor"`
	Payload     json.RawMessage `json:"payload"`
}

// NewEvent собирает событие с новым идентификатором и временем в UTC.
func NewEvent(clock Clock, typ string, aggregate, product ID, actor string, payload any) (Event, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Event{}, Invalid("payload", err.Error())
	}
	return Event{
		ID:          NewID(),
		Type:        typ,
		AggregateID: aggregate,
		ProductID:   product,
		OccurredAt:  clock.Now(),
		Actor:       actor,
		Payload:     raw,
	}, nil
}

// Publisher записывает события в outbox.
type Publisher interface {
	Publish(ctx context.Context, events ...Event) error
}

// Handler обрабатывает событие из outbox. Обработка идемпотентна по Event.ID.
type Handler interface {
	Handle(ctx context.Context, ev Event) error
}

// HandlerFunc — адаптер функции к Handler.
type HandlerFunc func(ctx context.Context, ev Event) error

// Handle вызывает функцию.
func (f HandlerFunc) Handle(ctx context.Context, ev Event) error { return f(ctx, ev) }

// Clock — источник времени; в тестах подменяется.
type Clock interface {
	Now() time.Time
}

// SystemClock возвращает текущее время в UTC.
type SystemClock struct{}

// Now — текущее время UTC.
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// FixedClock — неподвижные часы для тестов.
type FixedClock struct{ T time.Time }

// Now возвращает фиксированное время.
func (c FixedClock) Now() time.Time { return c.T.UTC() }
