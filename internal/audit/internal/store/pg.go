// Package store — реализация audit.Store на PostgreSQL (таблица audit.records).
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/audit/internal/db"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
)

// PG — хранилище записей аудита. Таблица защищена триггером от UPDATE/DELETE (инвариант 8).
type PG struct {
	db       *pgdb.DB
	pageSize int32
}

// New создаёт хранилище.
func New(d *pgdb.DB) *PG { return &PG{db: d, pageSize: 1000} }

func (s *PG) q(ctx context.Context) *db.Queries { return db.New(pgdb.Querier(ctx, s.db)) }

// Last — последняя запись.
func (s *PG) Last(ctx context.Context) (audit.Record, error) {
	row, err := s.q(ctx).LastRecord(ctx)
	if err != nil {
		return audit.Record{}, fmt.Errorf("audit last: %w", pgdb.MapError(err))
	}
	return fromRow(row)
}

// Insert добавляет запись. Поле At должно иметь точность timestamptz (микросекунды), иначе
// хеш, посчитанный до записи, не совпадёт с прочитанным; используйте pgstore.Clock.
func (s *PG) Insert(ctx context.Context, r audit.Record) error {
	if !r.At.Equal(r.At.Truncate(time.Microsecond)) {
		return fmt.Errorf("%w: audit: поле at должно быть с точностью до микросекунды", kernel.ErrValidation)
	}
	var details []byte
	if r.Details != nil {
		b, err := json.Marshal(r.Details)
		if err != nil {
			return fmt.Errorf("audit insert: details: %w", err)
		}
		details = b
	}
	err := s.q(ctx).InsertRecord(ctx, db.InsertRecordParams{
		Seq: r.Seq, At: r.At.UTC(), Actor: r.Actor, Action: string(r.Action),
		ObjectType: r.ObjectType, ObjectID: r.ObjectID,
		ProductID: uuid.NullUUID{UUID: r.ProductID, Valid: r.ProductID != kernel.NilID},
		Details:   details, PrevHash: r.PrevHash, Hash: r.Hash,
	})
	if err != nil {
		return fmt.Errorf("audit insert: %w", pgdb.MapError(err))
	}
	return nil
}

// Walk перебирает записи по возрастанию seq постранично.
func (s *PG) Walk(ctx context.Context, fn func(audit.Record) error) error {
	var after int64
	for {
		rows, err := s.q(ctx).RecordsAfter(ctx, db.RecordsAfterParams{Seq: after, Limit: s.pageSize})
		if err != nil {
			return fmt.Errorf("audit walk: %w", err)
		}
		for _, row := range rows {
			r, err := fromRow(row)
			if err != nil {
				return err
			}
			if err := fn(r); err != nil {
				return err
			}
			after = r.Seq
		}
		if len(rows) < int(s.pageSize) {
			return nil
		}
	}
}

func fromRow(row db.AuditRecord) (audit.Record, error) {
	r := audit.Record{
		Seq: row.Seq, At: row.At.UTC(), Actor: row.Actor, Action: audit.Action(row.Action),
		ObjectType: row.ObjectType, ObjectID: row.ObjectID, ProductID: row.ProductID.UUID,
		PrevHash: row.PrevHash, Hash: row.Hash,
	}
	if len(row.Details) > 0 && string(row.Details) != "null" {
		if err := json.Unmarshal(row.Details, &r.Details); err != nil {
			return audit.Record{}, fmt.Errorf("audit record %d: details: %w", row.Seq, err)
		}
	}
	return r, nil
}

// IsImmutableViolation сообщает, что БД отклонила UPDATE/DELETE журнала (триггер или REVOKE).
func IsImmutableViolation(err error) bool {
	return errors.Is(pgdb.MapError(err), kernel.ErrForbidden)
}
