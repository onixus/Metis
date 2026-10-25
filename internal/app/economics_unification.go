package app

import (
	"context"
	"math"
	"reflect"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/economics"
	modeling "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// canonicalBooks exposes the raw public book calculator, avoiding report-enrichment recursion.
type canonicalBooks struct{ *economics.Service }

func (c canonicalBooks) Report(ctx context.Context, sc authz.Scope, in economics.ReportInput) (economics.Report, error) {
	return c.ReportRaw(ctx, sc, in)
}

type unifiedEconomics struct {
	model *modeling.Service
	books *economics.Service
}

func (u unifiedEconomics) CommonFinanceDefinitions(ctx context.Context, sc authz.Scope, owner kernel.ID, period string, version int) (bool, error) {
	p, err := modeling.ParsePeriod(period)
	if err != nil {
		return false, err
	}
	return u.model.BookSnapshotUsesCommonDefinitions(ctx, sc, owner, p, version)
}

func (u unifiedEconomics) ValidateFinanceDefinitions(ctx context.Context, sc authz.Scope, owner kernel.ID, period string, before, after []economics.Field) error {
	p, err := modeling.ParsePeriod(period)
	if err != nil {
		return err
	}
	bound, err := u.model.BookDefinitionsUnified(ctx, sc, owner, p)
	if err != nil {
		return err
	}
	if bound && !reflect.DeepEqual(before, after) {
		if allowedManualProjection(ctx, owner, period, before, after) {
			return nil
		}
		return kernel.Invalid("fields", "unified financial definitions are edited only through the common model constructor")
	}
	return nil
}
func (u unifiedEconomics) EnrichFinanceReport(ctx context.Context, sc authz.Scope, in economics.ReportInput, r *economics.Report) error {
	p, err := modeling.ParsePeriod(in.Period)
	if err != nil {
		return err
	}
	bound, err := u.model.BookDefinitionsUnified(ctx, sc, in.ProductID, p)
	if err != nil {
		return err
	}
	if !bound {
		return nil
	}
	common, err := u.model.BookSnapshotUsesCommonDefinitions(ctx, sc, in.ProductID, p, r.Version)
	if err != nil {
		return err
	}
	if !common {
		return nil
	} // Superseded original archived versions retain their own DSL.
	values, err := u.model.UnifiedBookMetricsWithOverrides(ctx, sc, in.ProductID, p, r.Version, in.FilterProductID, in.Team, in.Overrides)
	if err != nil {
		return err
	}
	snapshot, err := u.books.Snapshot(ctx, sc, in.ProductID, in.Period, r.Version)
	if err != nil {
		return err
	}
	moneyKeys := map[string]bool{}
	for _, field := range snapshot.Fields {
		if field.Type == "money" {
			moneyKeys[field.Key] = true
		}
	}
	for _, metrics := range values {
		for key, value := range metrics {
			if moneyKeys[key] {
				v := value.Explanation.Value
				if !v.Equal(v.Truncate(0)) || v.LessThan(decimal.NewFromInt(math.MinInt64)) || v.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
					return kernel.Invalid("metric", "common monetary metric must return int64 minor units")
				}
			}
		}
	}
	apply := func(row *economics.ProductReport, metrics map[string]modeling.UnifiedMetricValue) {
		for key, v := range metrics {
			if row.Metrics == nil {
				row.Metrics = map[string]string{}
			}
			if row.Lineage == nil {
				row.Lineage = map[string]economics.Lineage{}
			}
			row.Metrics[key] = v.Value
			line := economics.Lineage{Formula: v.Expression, Dependencies: []string{}, RowIDs: []kernel.ID{}, Sources: []economics.Source{}, SourceSnapshots: []economics.SourceSnapshotRef{{Kind: "book", SnapshotID: snapshot.ID.String(), AsOf: snapshot.CreatedAt}}}
			seenRows := map[kernel.ID]bool{}
			seenDeps := map[string]bool{}
			var walk func(modeling.Explanation)
			walk = func(ex modeling.Explanation) {
				if ex.Key != "" && !seenDeps[ex.Key] && ex.Key != v.Explanation.Key {
					seenDeps[ex.Key] = true
					line.Dependencies = append(line.Dependencies, ex.Key)
				}
				for _, ob := range ex.Sources {
					line.SourceSnapshots = append(line.SourceSnapshots, economics.SourceSnapshotRef{Kind: string(ob.Kind), SnapshotID: ob.SnapshotID, AsOf: ob.AsOf, CapturedValue: ob.CapturedValue, Overridden: ob.Overridden, Currency: ob.Currency})
				}
				for _, fact := range ex.Rows {
					for _, source := range snapshot.Rows {
						matches := source.ID == fact.ID
						if fact.ID == snapshot.ID {
							matches = source.ProductID == fact.ProductID
							for _, allocation := range source.Allocations {
								matches = matches || allocation.ProductID == fact.ProductID
							}
							if in.Team != "" && source.TeamID != in.Team {
								matches = false
							}
						}
						if matches && !seenRows[source.ID] {
							seenRows[source.ID] = true
							line.RowIDs = append(line.RowIDs, source.ID)
							line.Sources = append(line.Sources, source.Source)
						}
					}
				}
				for _, child := range ex.Inputs {
					walk(child)
				}
			}
			walk(v.Explanation)
			row.Lineage[key] = line
		}
	}
	apply(&r.Total, values[kernel.NilID])
	for i := range r.Products {
		apply(&r.Products[i], values[r.Products[i].ProductID])
	}
	return nil
}
func (a *App) configureUnifiedEconomics() {
	a.Modeling.WithCanonicalBooks(canonicalBooks{a.Economics}).WithCanonicalManualWriter(canonicalManualWriter{app: a})
	hooks := unifiedEconomics{a.Modeling, a.Economics}
	a.Economics.WithDefinitionPolicy(hooks).WithReportEvaluator(hooks)
}
