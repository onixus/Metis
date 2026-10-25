package economics

import (
	"context"
	"sort"

	"github.com/shopspring/decimal"

	book "github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

type UnifiedMetricValue struct {
	Value       string      `json:"value"`
	Expression  string      `json:"expression"`
	Explanation Explanation `json:"explanation"`
}

// UnifiedBookMetrics evaluates migrated common definitions on an explicitly pinned book input.
// The nil product key contains the report total (or selected product total).
func (s *Service) UnifiedBookMetrics(ctx context.Context, sc authz.Scope, owner kernel.ID, p Period, version int, filter kernel.ID, team string) (map[kernel.ID]map[string]UnifiedMetricValue, error) {
	return s.UnifiedBookMetricsWithOverrides(ctx, sc, owner, p, version, filter, team, nil)
}
func (s *Service) UnifiedBookMetricsWithOverrides(ctx context.Context, sc authz.Scope, owner kernel.ID, p Period, version int, filter kernel.ID, team string, overrides map[string]string) (map[kernel.ID]map[string]UnifiedMetricValue, error) {
	if err := s.requireRead(sc, owner, authz.FinanceFull); err != nil {
		return nil, err
	}
	ctx = WithFormulaSnapshot(ctx)
	c, err := s.canonicalStore()
	if err != nil {
		return nil, err
	}
	a, bound, err := c.binding(ctx, sc, p)
	if err != nil {
		return nil, err
	}
	if !bound || a.BookProductID != owner {
		return nil, kernel.ErrNotFound
	}
	report, err := c.books.Report(ctx, sc, book.ReportInput{ProductID: owner, Period: p.String(), Version: version, FilterProductID: filter, Team: team, Overrides: overrides})
	if err != nil {
		return nil, err
	}
	facts, err := c.canonicalFacts(ctx, sc, a, report.Version)
	if err != nil {
		return nil, err
	}
	projection, err := c.books.Project(ctx, sc, owner, p.String(), report.Version)
	if err != nil {
		return nil, err
	}
	types := map[string]FieldType{}
	for _, f := range projection.Facts {
		types[f.FieldKey] = FieldType(f.Type)
		if mapped := a.FieldKeys[f.FieldKey]; mapped != "" {
			types[mapped] = FieldType(f.Type)
		}
	}
	for _, key := range []string{s.cfg.RevenueField, s.cfg.BundleRevenueField, s.cfg.PayrollField, s.cfg.DirectCostField, s.cfg.MarketingField, book.HubCost, book.CertificationCost, book.MaintenanceCost, "direct_total", "direct_profit", "loaded_profit"} {
		types[key] = FieldMoney
	}
	teamID, err := c.canonicalTeamID(ctx, sc, team)
	if err != nil {
		return nil, err
	}
	// Derive standard totals for the selected team with the same canonical report calculator.
	if team != "" || len(overrides) > 0 {
		kept := facts[:0]
		for _, r := range facts {
			if r.FieldKey != "direct_total" && r.FieldKey != "direct_profit" && r.FieldKey != "loaded_profit" {
				kept = append(kept, r)
			}
		}
		facts = kept
		for _, pr := range report.Products {
			for key, value := range map[string]int64{"direct_total": pr.DirectCost, "direct_profit": pr.DirectProfit, "loaded_profit": pr.LoadedProfit} {
				facts = append(facts, FactRow{ID: projection.Snapshot.ID, BatchID: projection.Snapshot.ID, ProductID: pr.ProductID, TeamID: teamID, Period: p, FieldKey: key, Value: decimal.NewFromInt(value), DataVersion: report.Version})
			}
		}
	}
	if len(overrides) > 0 {
		aliases := map[string]string{book.Revenue: s.cfg.RevenueField, book.Payroll: s.cfg.PayrollField, book.DirectCost: s.cfg.DirectCostField, book.Marketing: s.cfg.MarketingField}
		for raw, value := range overrides {
			v, err := decimal.NewFromString(value)
			if err != nil {
				return nil, err
			}
			keys := map[string]bool{raw: true}
			if alias := aliases[raw]; alias != "" {
				keys[alias] = true
			}
			if alias := a.FieldKeys[raw]; alias != "" {
				keys[alias] = true
			}
			kept := facts[:0]
			for _, f := range facts {
				if !keys[f.FieldKey] || (filter != kernel.NilID && f.ProductID != filter) || (team != "" && f.TeamID != teamID) {
					kept = append(kept, f)
				}
			}
			facts = kept
			pid := filter
			if pid == kernel.NilID && len(report.Products) == 1 {
				pid = report.Products[0].ProductID
			}
			for key := range keys {
				facts = append(facts, FactRow{ID: projection.Snapshot.ID, BatchID: projection.Snapshot.ID, ProductID: pid, TeamID: teamID, Period: p, FieldKey: key, Value: v, DataVersion: report.Version})
			}
		}
	}
	products := []kernel.ID{kernel.NilID}
	for _, pr := range report.Products {
		products = append(products, pr.ProductID)
	}
	snapshot, err := c.books.Snapshot(ctx, sc, owner, p.String(), report.Version)
	if err != nil {
		return nil, err
	}
	active := map[string]bool{}
	for _, field := range snapshot.Fields {
		if field.Source == "calculated" {
			active[field.Key] = true
		}
	}
	keys := []string{}
	for key := range a.MetricKeys {
		if !active[key] {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := map[kernel.ID]map[string]UnifiedMetricValue{}
	for _, pid := range products {
		out[pid] = map[string]UnifiedMetricValue{}
		selection := pid
		if pid == kernel.NilID && filter != kernel.NilID {
			selection = filter
		}
		for _, key := range keys {
			ex, err := s.EvaluateExternal(ctx, sc, a.MetricKeys[key], Slice{ProductID: selection, TeamID: teamID, Period: p}, facts, types)
			if err != nil {
				return nil, err
			}
			out[pid][key] = UnifiedMetricValue{Value: ex.Value.String(), Expression: ex.Expression, Explanation: ex}
		}
	}
	return out, nil
}

// CanonicalInputAliases extends a pinned book projection with its migrated input namespace.
func (s *Service) CanonicalInputAliases(ctx context.Context, sc authz.Scope, owner kernel.ID, p Period, facts []FactRow, types map[string]FieldType) ([]FactRow, map[string]FieldType, error) {
	c, err := s.canonicalStore()
	if err != nil {
		return facts, types, nil
	}
	a, bound, err := c.binding(ctx, sc, p)
	if err != nil {
		return nil, nil, err
	}
	if !bound || a.BookProductID != owner {
		return facts, types, nil
	}
	out := append([]FactRow{}, facts...)
	fieldTypes := kernel.CloneValue(types)
	for _, f := range facts {
		if mapped := a.FieldKeys[f.FieldKey]; mapped != "" {
			fieldTypes[mapped] = types[f.FieldKey]
			f.FieldKey = mapped
			out = append(out, f)
		}
	}
	return out, fieldTypes, nil
}

// EvaluateCanonicalBookMetric uses the common registry for any metric on a pinned canonical book.
func (s *Service) EvaluateCanonicalBookMetric(ctx context.Context, sc authz.Scope, owner kernel.ID, p Period, version int, key string, filter kernel.ID, team string) (book.SnapshotInfo, Explanation, error) {
	if err := s.requireRead(sc, owner, authz.FinanceFull); err != nil {
		return book.SnapshotInfo{}, Explanation{}, err
	}
	c, err := s.canonicalStore()
	if err != nil {
		return book.SnapshotInfo{}, Explanation{}, err
	}
	a, bound, err := c.binding(ctx, sc, p)
	if err != nil {
		return book.SnapshotInfo{}, Explanation{}, err
	}
	if !bound || a.BookProductID != owner {
		return book.SnapshotInfo{}, Explanation{}, kernel.ErrNotFound
	}
	snapshot, err := c.books.Snapshot(ctx, sc, owner, p.String(), version)
	if err != nil {
		return book.SnapshotInfo{}, Explanation{}, err
	}
	facts, err := c.canonicalFacts(ctx, sc, a, snapshot.Version)
	if err != nil {
		return book.SnapshotInfo{}, Explanation{}, err
	}
	projection, err := c.books.Project(ctx, sc, owner, p.String(), snapshot.Version)
	if err != nil {
		return book.SnapshotInfo{}, Explanation{}, err
	}
	types := map[string]FieldType{}
	for _, f := range projection.Facts {
		types[f.FieldKey] = FieldType(f.Type)
		if mapped := a.FieldKeys[f.FieldKey]; mapped != "" {
			types[mapped] = FieldType(f.Type)
		}
	}
	for _, key := range []string{s.cfg.RevenueField, s.cfg.BundleRevenueField, s.cfg.PayrollField, s.cfg.DirectCostField, s.cfg.MarketingField, book.HubCost, book.CertificationCost, book.MaintenanceCost, "direct_total", "direct_profit", "loaded_profit"} {
		types[key] = FieldMoney
	}
	teamID, err := c.canonicalTeamID(ctx, sc, team)
	if err != nil {
		return book.SnapshotInfo{}, Explanation{}, err
	}
	metric, err := s.EvaluateExternal(ctx, sc, key, Slice{ProductID: filter, TeamID: teamID, Period: p}, facts, types)
	return snapshot.SnapshotInfo, metric, err
}
