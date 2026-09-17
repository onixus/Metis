package pgstore

import (
	"context"
	"fmt"
	"time"

	"github.com/onixus/metis/internal/compliance"
	"github.com/onixus/metis/internal/compliance/internal/db"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
)

// EvidenceStore — журнал доказательств (CM-04) на таблице compliance.evidence_log: только INSERT,
// UPDATE и DELETE отклоняет триггер; роль приложения этих прав не имеет (инвариант 8).
type EvidenceStore struct {
	db       *pgdb.DB
	pageSize int64
}

var _ compliance.EvidenceStore = (*EvidenceStore)(nil)

// NewEvidenceStore создаёт журнал доказательств.
func NewEvidenceStore(d *pgdb.DB) *EvidenceStore { return &EvidenceStore{db: d, pageSize: 1000} }

func (s *EvidenceStore) q(ctx context.Context) *db.Queries { return db.New(pgdb.Querier(ctx, s.db)) }

// Last — последняя запись; kernel.ErrNotFound, если журнал пуст.
func (s *EvidenceStore) Last(ctx context.Context) (compliance.EvidenceItem, error) {
	row, err := s.q(ctx).LastEvidence(ctx)
	if err != nil {
		return compliance.EvidenceItem{}, fmt.Errorf("compliance evidence last: %w", pgdb.MapError(err))
	}
	return evidenceFromRow(row), nil
}

// Insert добавляет запись. Поле At должно иметь точность timestamptz (микросекунды), иначе хеш,
// посчитанный до записи, не совпадёт с прочитанным (вопрос №10); используйте pgstore.Clock.
func (s *EvidenceStore) Insert(ctx context.Context, e compliance.EvidenceItem) error {
	if !e.At.Equal(e.At.Truncate(time.Microsecond)) {
		return fmt.Errorf("%w: compliance evidence: поле at должно быть с точностью до микросекунды", kernel.ErrValidation)
	}
	err := s.q(ctx).InsertEvidence(ctx, db.InsertEvidenceParams{
		Seq: e.Seq, ID: e.ID, ProductID: e.ProductID, TrackID: e.TrackID, GateID: e.GateID, Url: e.URL, Sha256: e.SHA256,
		Status: string(e.Status), Comment: e.Comment, Supersedes: e.Supersedes, Actor: e.Actor, At: e.At.UTC(),
		PrevHash: e.PrevHash, Hash: e.Hash,
	})
	if err != nil {
		return fmt.Errorf("compliance evidence insert: %w", pgdb.MapError(err))
	}
	return nil
}

// Walk перебирает записи по возрастанию seq постранично.
func (s *EvidenceStore) Walk(ctx context.Context, fn func(compliance.EvidenceItem) error) error {
	var after int64
	for {
		rows, err := s.q(ctx).EvidenceAfter(ctx, db.EvidenceAfterParams{Seq: after, Lim: s.pageSize})
		if err != nil {
			return fmt.Errorf("compliance evidence walk: %w", pgdb.MapError(err))
		}
		for _, row := range rows {
			e := evidenceFromRow(row)
			if err := fn(e); err != nil {
				return err
			}
			after = e.Seq
		}
		if int64(len(rows)) < s.pageSize {
			return nil
		}
	}
}

func evidenceFromRow(r db.ComplianceEvidenceLog) compliance.EvidenceItem {
	return compliance.EvidenceItem{
		Seq: r.Seq, ID: r.ID, ProductID: r.ProductID, TrackID: r.TrackID, GateID: r.GateID, URL: r.Url, SHA256: r.Sha256,
		Status: compliance.EvidenceStatus(r.Status), Comment: r.Comment, Supersedes: r.Supersedes, Actor: r.Actor, At: r.At.UTC(),
		PrevHash: r.PrevHash, Hash: r.Hash,
	}
}

// Clock округляет время до микросекунд — точности timestamptz, чтобы хеш записи журнала
// доказательств (At входит в canonical_json) совпадал после чтения из БД. Передавайте его в
// compliance.NewService при работе на PostgreSQL (по образцу audit/pgstore.Clock, вопрос №10).
type Clock struct{ Inner kernel.Clock }

// Now — время Inner (или системное), усечённое до микросекунд, в UTC.
func (c Clock) Now() time.Time {
	inner := c.Inner
	if inner == nil {
		inner = kernel.SystemClock{}
	}
	return inner.Now().UTC().Truncate(time.Microsecond)
}
