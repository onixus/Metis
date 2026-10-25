package economics

import (
	"context"
	"fmt"

	book "github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

// historyDefinitionOverlay is a read-only candidate view; no migration writes are
// needed to validate the exact shared report that will become active after apply.
type historyDefinitionOverlay struct {
	Store
	history HistoryStore
	archive HistoryArchive
	fields  map[string]Field
	metrics map[string]Metric
}

func (v *historyDefinitionOverlay) Field(ctx context.Context, sc authz.Scope, key string) (Field, error) {
	if f, ok := v.fields[key]; ok {
		return f, nil
	}
	return v.Store.Field(ctx, sc, key)
}
func (v *historyDefinitionOverlay) Metric(ctx context.Context, sc authz.Scope, key string) (Metric, error) {
	if m, ok := v.metrics[key]; ok {
		return m, nil
	}
	return v.Store.Metric(ctx, sc, key)
}
func (v *historyDefinitionOverlay) Unification(ctx context.Context, sc authz.Scope, p Period) (HistoryArchive, error) {
	if p == v.archive.Period {
		return v.archive, nil
	}
	return v.history.Unification(ctx, sc, p)
}
func (v *historyDefinitionOverlay) Unifications(ctx context.Context, sc authz.Scope) ([]HistoryArchive, error) {
	out, err := v.history.Unifications(ctx, sc)
	if err != nil {
		return nil, err
	}
	return append(out, v.archive), nil
}
func (v *historyDefinitionOverlay) UnificationBindings(ctx context.Context, sc authz.Scope) ([]HistoryBinding, error) {
	out, err := v.history.UnificationBindings(ctx, sc)
	if err != nil {
		return nil, err
	}
	return append(out, HistoryBinding{OriginalBookVersion: v.archive.BookVersions[len(v.archive.BookVersions)-1].Version, BookProductID: v.archive.BookProductID, Period: v.archive.Period, FieldKeys: v.archive.FieldKeys, MetricKeys: v.archive.MetricKeys}), nil
}
func (v *historyDefinitionOverlay) SaveUnification(context.Context, authz.Scope, HistoryArchive) error {
	return kernel.ErrForbidden
}
func (s *Service) preflightUnification(ctx context.Context, sc authz.Scope, a HistoryArchive, fields []Field, metrics []Metric, expected book.Report) error {
	c, err := s.canonicalStore()
	if err != nil {
		return err
	}
	view := &historyDefinitionOverlay{Store: c.Store, history: c.Store.(HistoryStore), archive: a, fields: map[string]Field{}, metrics: map[string]Metric{}}
	for _, f := range fields {
		view.fields[f.Key] = f
	}
	for _, m := range metrics {
		view.metrics[m.Key] = m
	}
	candidate, err := NewService(view, nil, s.clock, s.cfg)
	if err != nil {
		return err
	}
	candidate.WithAuditor(s.audit).WithCanonicalBooks(c.books)
	values, err := candidate.UnifiedBookMetrics(ctx, sc, a.BookProductID, a.Period, expected.Version, kernel.NilID, "")
	if err != nil {
		return fmt.Errorf("%w: translated common report is not ready: %w", kernel.ErrValidation, err)
	}
	compare := func(product kernel.ID, row book.ProductReport) error {
		for key, metric := range values[product] {
			old, ok := row.Metrics[key]
			if !ok {
				return kernel.Invalid("unification", "common metric is absent from original report: "+key)
			}
			baseline, err := decimal.NewFromString(old)
			if err != nil {
				return fmt.Errorf("parse historical metric: %w", err)
			}
			if !metric.Explanation.Value.Equal(baseline) {
				return kernel.Invalid("unification", "translated common metric differs from original report: "+key)
			}
		}
		return nil
	}
	if err := compare(kernel.NilID, expected.Total); err != nil {
		return err
	}
	for _, row := range expected.Products {
		if err := compare(row.ProductID, row); err != nil {
			return err
		}
	}
	return nil
}
