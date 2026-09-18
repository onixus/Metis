package decisions

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// MeasurableEffect — измеримая часть ожидаемого эффекта решения (DA-06): показатель экономики,
// целевое значение и период, на котором эффект проверяется. Текстовый ExpectedEffect остаётся
// описанием для читателя ADR.
type MeasurableEffect struct {
	MetricKey string          `json:"metric_key,omitempty"`
	Value     decimal.Decimal `json:"value"`
	Period    string          `json:"period,omitempty"`
}

// IsZero сообщает, задан ли измеримый эффект.
func (e MeasurableEffect) IsZero() bool { return e.MetricKey == "" }

// Verdict — итог ревизии (DA-06).
type Verdict string

// Значения итога ревизии.
const (
	VerdictConfirmed Verdict = "confirmed" // факт не ниже ожидания
	VerdictPartial   Verdict = "partial"   // факт не ниже половины ожидания
	VerdictMissed    Verdict = "missed"    // эффект не достигнут
)

// Review — ревизия решения на дату ревизии: ожидание против факта (DA-06).
type Review struct {
	At       time.Time       `json:"at"`
	Actor    string          `json:"actor"`
	Expected decimal.Decimal `json:"expected"`
	Actual   decimal.Decimal `json:"actual"`
	Delta    decimal.Decimal `json:"delta"`
	Verdict  Verdict         `json:"verdict"`
	Comment  string          `json:"comment,omitempty"`
}

// MetricSource — порт модуля экономики: фактическое значение показателя в срезе (DA-06).
type MetricSource interface {
	MetricValue(ctx context.Context, sc authz.Scope, key string, product kernel.ID, period string) (decimal.Decimal, error)
}

// Названия доменных событий ревизии.
const EventRecordReviewed = "decisions.record.reviewed"

// WithMetrics подключает источник фактических значений показателей (DA-06).
func (s *Service) WithMetrics(m MetricSource) *Service {
	s.metrics = m
	return s
}

// DueForReview возвращает принятые решения, у которых наступила дата ревизии и ревизии ещё не было (DA-06).
func (s *Service) DueForReview(ctx context.Context, sc authz.Scope, productID kernel.ID) ([]DecisionRecord, error) {
	if !sc.Valid() {
		return nil, kernel.ErrForbidden
	}
	all, err := s.store.DueForReview(ctx, kernel.DateFromTime(s.clock.Now()))
	if err != nil {
		return nil, fmt.Errorf("decisions due for review: %w", err)
	}
	out := make([]DecisionRecord, 0, len(all))
	for _, rec := range all {
		if productID != kernel.NilID && rec.ProductID != productID {
			continue
		}
		if canRead(sc, rec.ProductID) != nil {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// ReviewDecision сравнивает ожидаемый эффект решения с фактом на дату ревизии (DA-06).
// Факт берётся у модуля экономики по показателю из измеримого эффекта.
func (s *Service) ReviewDecision(ctx context.Context, sc authz.Scope, id kernel.ID, comment string) (DecisionRecord, error) {
	rec, err := s.store.Get(ctx, id)
	if err != nil {
		return DecisionRecord{}, fmt.Errorf("decisions review %s: %w", id, err)
	}
	if err := canWrite(sc, rec.ProductID); err != nil {
		return DecisionRecord{}, err
	}
	if rec.Effect.IsZero() {
		return DecisionRecord{}, kernel.Invalid("effect", "у решения нет измеримого ожидаемого эффекта: нечего сравнивать")
	}
	if s.metrics == nil {
		return DecisionRecord{}, fmt.Errorf("%w: источник фактических значений не подключён", kernel.ErrUnavailable)
	}
	actual, err := s.metrics.MetricValue(ctx, sc, rec.Effect.MetricKey, rec.ProductID, rec.Effect.Period)
	if err != nil {
		return DecisionRecord{}, fmt.Errorf("факт по показателю %q: %w", rec.Effect.MetricKey, err)
	}
	r := Review{At: s.clock.Now().UTC(), Actor: sc.Subject(), Expected: rec.Effect.Value, Actual: actual,
		Delta: actual.Sub(rec.Effect.Value), Verdict: verdict(rec.Effect.Value, actual), Comment: comment}
	rec.Review = &r
	rec.UpdatedAt = r.At
	if err := s.store.Save(ctx, rec); err != nil {
		return DecisionRecord{}, fmt.Errorf("decisions review %s: %w", id, err)
	}
	if err := s.emit(ctx, EventRecordReviewed, rec, sc.Subject(), rec); err != nil {
		return DecisionRecord{}, err
	}
	return rec, nil
}

// verdict: факт не ниже ожидания — подтверждено; не ниже половины — частично; иначе — не достигнуто.
func verdict(expected, actual decimal.Decimal) Verdict {
	if expected.IsZero() {
		if actual.IsNegative() {
			return VerdictMissed
		}
		return VerdictConfirmed
	}
	ratio := actual.DivRound(expected, 6)
	switch {
	case ratio.GreaterThanOrEqual(decimal.NewFromInt(1)):
		return VerdictConfirmed
	case ratio.GreaterThanOrEqual(decimal.NewFromFloat(0.5)):
		return VerdictPartial
	default:
		return VerdictMissed
	}
}
