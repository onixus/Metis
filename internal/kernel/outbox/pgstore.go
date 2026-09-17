package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/outbox/internal/db"
	"github.com/onixus/metis/internal/kernel/pgdb"
)

// PGStore — хранилище outbox в таблицах kernel.outbox и kernel.outbox_dlq.
type PGStore struct {
	db *pgdb.DB
}

// NewPGStore создаёт хранилище.
func NewPGStore(d *pgdb.DB) *PGStore { return &PGStore{db: d} }

func (s *PGStore) q(ctx context.Context) *db.Queries { return db.New(pgdb.Querier(ctx, s.db)) }

// Enqueue пишет события в текущую транзакцию (или напрямую в пул, если транзакции нет).
func (s *PGStore) Enqueue(ctx context.Context, now time.Time, events ...kernel.Event) error {
	q := s.q(ctx)
	for _, ev := range events {
		payload := ev.Payload
		if len(payload) == 0 {
			payload = json.RawMessage("null")
		}
		err := q.InsertOutbox(ctx, db.InsertOutboxParams{
			ID: ev.ID, EventType: ev.Type, AggregateID: ev.AggregateID, ProductID: nullID(ev.ProductID),
			OccurredAt: ev.OccurredAt.UTC(), Actor: ev.Actor, Payload: payload, NextAttemptAt: now.UTC(),
		})
		if err != nil {
			return fmt.Errorf("outbox enqueue %s: %w", ev.ID, pgdb.MapError(err))
		}
	}
	return nil
}

// Process захватывает пачку под FOR UPDATE SKIP LOCKED и обрабатывает её в одной транзакции.
// Каждый обработчик выполняется в savepoint: его сбой не ломает транзакцию пачки.
func (s *PGStore) Process(ctx context.Context, now time.Time, limit int, fn func(ctx context.Context, m Message) Outcome) (int, error) {
	n := 0
	err := s.db.Transact(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		rows, err := q.ClaimOutbox(ctx, db.ClaimOutboxParams{NextAttemptAt: now.UTC(), Limit: clampInt32(limit)})
		if err != nil {
			return fmt.Errorf("outbox claim: %w", err)
		}
		n = len(rows)
		for _, r := range rows {
			msg := Message{
				Event:         eventFromRow(r.ID, r.EventType, r.AggregateID, r.ProductID, r.OccurredAt, r.Actor, r.Payload),
				Attempts:      int(r.Attempts),
				NextAttemptAt: r.NextAttemptAt,
				LastError:     r.LastError.String,
				CreatedAt:     r.CreatedAt,
			}
			var out Outcome
			spErr := s.db.Transact(ctx, func(spCtx context.Context) error {
				out = fn(spCtx, msg)
				if out.Kind != Ack {
					return errHandlerFailed
				}
				return nil
			})
			if spErr != nil && !errors.Is(spErr, errHandlerFailed) {
				return fmt.Errorf("outbox savepoint %s: %w", msg.Event.ID, spErr)
			}
			if err := s.apply(ctx, q, msg, out, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

var errHandlerFailed = errors.New("обработчик вернул ошибку")

// clampInt32 ограничивает значение диапазоном int32 (размеры пачек и счётчики попыток малы).
func clampInt32(v int) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < 0 {
		return 0
	}
	return int32(v)
}

func (s *PGStore) apply(ctx context.Context, q *db.Queries, msg Message, out Outcome, now time.Time) error {
	id := msg.Event.ID
	switch out.Kind {
	case Ack:
		if err := q.DeleteOutbox(ctx, id); err != nil {
			return fmt.Errorf("outbox ack %s: %w", id, err)
		}
	case Retry:
		err := q.RetryOutbox(ctx, db.RetryOutboxParams{
			ID: id, Attempts: clampInt32(msg.Attempts + 1), NextAttemptAt: out.NextAttemptAt.UTC(),
			LastError: pgtype.Text{String: out.Error, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("outbox retry %s: %w", id, err)
		}
	case Dead:
		err := q.MoveOutboxToDLQ(ctx, db.MoveOutboxToDLQParams{
			ID: id, Attempts: clampInt32(msg.Attempts + 1), Error: out.Error, FailedAt: now.UTC(),
		})
		if err != nil {
			return fmt.Errorf("outbox dlq %s: %w", id, err)
		}
		if err := q.DeleteOutbox(ctx, id); err != nil {
			return fmt.Errorf("outbox dlq delete %s: %w", id, err)
		}
	}
	return nil
}

// DLQCount — число сообщений в DLQ.
func (s *PGStore) DLQCount(ctx context.Context) (int64, error) {
	n, err := s.q(ctx).CountDLQ(ctx)
	if err != nil {
		return 0, fmt.Errorf("outbox dlq count: %w", err)
	}
	return n, nil
}

// DLQList — последние сообщения DLQ.
func (s *PGStore) DLQList(ctx context.Context, limit int) ([]DeadMessage, error) {
	rows, err := s.q(ctx).ListDLQ(ctx, clampInt32(limit))
	if err != nil {
		return nil, fmt.Errorf("outbox dlq list: %w", err)
	}
	out := make([]DeadMessage, 0, len(rows))
	for _, r := range rows {
		out = append(out, DeadMessage{
			Event:     eventFromRow(r.ID, r.EventType, r.AggregateID, r.ProductID, r.OccurredAt, r.Actor, r.Payload),
			Attempts:  int(r.Attempts),
			Error:     r.Error,
			CreatedAt: r.CreatedAt,
			FailedAt:  r.FailedAt,
		})
	}
	return out, nil
}

// Requeue переносит сообщение из DLQ в очередь.
func (s *PGStore) Requeue(ctx context.Context, id kernel.ID, now time.Time) error {
	return s.db.Transact(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		n, err := q.RequeueFromDLQ(ctx, db.RequeueFromDLQParams{ID: id, NextAttemptAt: now.UTC()})
		if err != nil {
			return fmt.Errorf("outbox requeue %s: %w", id, pgdb.MapError(err))
		}
		if n == 0 {
			return kernel.NotFound("outbox_dlq", id)
		}
		if err := q.DeleteDLQ(ctx, id); err != nil {
			return fmt.Errorf("outbox requeue delete %s: %w", id, err)
		}
		return nil
	})
}

func nullID(id kernel.ID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: id != kernel.NilID}
}

func eventFromRow(id kernel.ID, typ string, aggregate kernel.ID, product uuid.NullUUID, at time.Time, actor string, payload []byte) kernel.Event {
	return kernel.Event{
		ID: id, Type: typ, AggregateID: aggregate, ProductID: product.UUID,
		OccurredAt: at.UTC(), Actor: actor, Payload: json.RawMessage(payload),
	}
}
