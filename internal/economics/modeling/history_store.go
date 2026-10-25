package economics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	book "github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

type canonicalStore struct {
	Store
	books CanonicalBooks
	cfg   Config
}

func (c *canonicalStore) Transact(ctx context.Context, sc authz.Scope, fn func(context.Context) error) error {
	if tx, ok := c.Store.(TransactionalStore); ok {
		return tx.Transact(ctx, sc, fn)
	}
	return fn(ctx)
}
func (c *canonicalStore) binding(ctx context.Context, sc authz.Scope, p Period) (HistoryBinding, bool, error) {
	hs, ok := c.Store.(HistoryStore)
	if !ok {
		return HistoryBinding{}, false, nil
	}
	bindings, err := hs.UnificationBindings(ctx, sc)
	if err != nil {
		return HistoryBinding{}, false, err
	}
	for _, a := range bindings {
		if a.Period == p {
			return a, true, nil
		}
	}
	return HistoryBinding{}, false, nil
}
func (c *canonicalStore) SaveBatch(ctx context.Context, sc authz.Scope, b ImportBatch) error {
	if _, bound, err := c.binding(ctx, sc, b.Period); err != nil {
		return err
	} else if bound && b.Status == BatchApplied {
		return kernel.Invalid("period", "unified period accepts inputs only through its canonical book")
	}
	return c.Store.SaveBatch(ctx, sc, b)
}
func (c *canonicalStore) AppendFacts(ctx context.Context, sc authz.Scope, rows []FactRow) error {
	for _, r := range rows {
		if _, bound, err := c.binding(ctx, sc, r.Period); err != nil {
			return err
		} else if bound {
			return kernel.Invalid("period", "unified period accepts inputs only through its canonical book")
		}
	}
	return c.Store.AppendFacts(ctx, sc, rows)
}
func (c *canonicalStore) SaveTeamShares(ctx context.Context, sc authz.Scope, shares []TeamShare) error {
	for _, r := range shares {
		if _, bound, err := c.binding(ctx, sc, r.Period); err != nil {
			return err
		} else if bound {
			return kernel.Invalid("period", "unified team allocation belongs to canonical book")
		}
	}
	return c.Store.SaveTeamShares(ctx, sc, shares)
}
func (c *canonicalStore) Facts(ctx context.Context, sc authz.Scope, f FactFilter) ([]FactRow, error) {
	if f.DataVersion != nil {
		return c.Store.Facts(ctx, sc, f)
	}
	raw, err := c.Store.Facts(ctx, sc, f)
	if err != nil {
		return nil, err
	}
	hs, ok := c.Store.(HistoryStore)
	if !ok {
		return raw, nil
	}
	bindings, err := hs.UnificationBindings(ctx, sc)
	if err != nil {
		return nil, err
	}
	for _, a := range bindings {
		if f.Period != nil && a.Period != *f.Period {
			continue
		}
		rows, err := c.canonicalFacts(ctx, sc, a, 0)
		if err != nil {
			return nil, err
		}
		kept := raw[:0]
		for _, r := range raw {
			if r.Period != a.Period {
				kept = append(kept, r)
			}
		}
		raw = kept
		for _, r := range rows {
			if f.FieldKey != "" && r.FieldKey != f.FieldKey || f.Product != nil && r.ProductID != *f.Product || f.Team != nil && r.TeamID != *f.Team || f.Item != nil && r.Item != *f.Item {
				continue
			}
			raw = append(raw, r)
		}
	}
	return raw, nil
}
func CanonicalTeamID(key string) (kernel.ID, error) {
	if key == "" {
		return kernel.NilID, nil
	}
	sum := sha256.Sum256([]byte("metis:economics:team:" + key))
	return kernel.ParseID(hex.EncodeToString(sum[:16]))
}
func (c *canonicalStore) canonicalFacts(ctx context.Context, sc authz.Scope, a HistoryBinding, version int) ([]FactRow, error) {
	sc = canonicalReadScope(ctx, sc)
	projection, err := c.books.Project(ctx, sc, a.BookProductID, a.Period.String(), version)
	if err != nil {
		return nil, err
	}
	if projection.Snapshot.Currency != c.cfg.Currency {
		return nil, kernel.Invalid("currency", "canonical book currency changed")
	}
	known, err := c.canonicalTeamMap(ctx, sc)
	if err != nil {
		return nil, err
	}
	out := []FactRow{}
	aliases := map[string]string{book.Revenue: c.cfg.RevenueField, book.Payroll: c.cfg.PayrollField, book.Marketing: c.cfg.MarketingField, book.DirectCost: c.cfg.DirectCostField}
	for _, f := range projection.Facts {
		tid, err := resolveCanonicalTeam(f.TeamID, known)
		if err != nil {
			return nil, err
		}
		r := FactRow{BookProductID: a.BookProductID, ID: f.RowID, BatchID: projection.Snapshot.ID, FieldKey: f.FieldKey, Item: f.Item, ProductID: f.ProductID, TeamID: tid, Period: a.Period, Value: f.Value, DataVersion: projection.Snapshot.Version, Sheet: f.Source.Sheet, Row: f.Source.Row}
		if mapped := a.FieldKeys[f.FieldKey]; mapped != "" {
			r.FieldKey = mapped
			out = append(out, r)
			continue
		}
		out = append(out, r)
		if f.FieldKey == book.Revenue && f.BundleID != "" {
			extra := r
			extra.FieldKey = c.cfg.BundleRevenueField
			extra.Item = f.BundleID
			out = append(out, extra)
		}
		if alias := aliases[f.FieldKey]; alias != "" && alias != f.FieldKey {
			r.FieldKey = alias
			out = append(out, r)
		}

	}
	out = appendDerivedBookFacts(out, projection.Snapshot.ID, projection.Snapshot.Version, a.Period)
	return out, nil
}

type canonicalReadScopeKey struct{}

// WithCanonicalReadScope preserves the original financial authorization across internal calculations.
func WithCanonicalReadScope(ctx context.Context, sc authz.Scope) context.Context {
	return context.WithValue(ctx, canonicalReadScopeKey{}, sc)
}
func canonicalReadScope(ctx context.Context, fallback authz.Scope) authz.Scope {
	if sc, ok := ctx.Value(canonicalReadScopeKey{}).(authz.Scope); ok {
		return sc
	}
	return fallback
}

func (c *canonicalStore) SaveAllocationRule(ctx context.Context, sc authz.Scope, r AllocationRule) error {
	if err := c.rejectLegacyRule(ctx, sc, r.EffectiveFrom); err != nil {
		return err
	}
	return c.Store.SaveAllocationRule(ctx, sc, r)
}
func (c *canonicalStore) SaveBundleRule(ctx context.Context, sc authz.Scope, r BundleRule) error {
	if err := c.rejectLegacyRule(ctx, sc, r.EffectiveFrom); err != nil {
		return err
	}
	return c.Store.SaveBundleRule(ctx, sc, r)
}
func (c *canonicalStore) rejectLegacyRule(ctx context.Context, sc authz.Scope, from kernel.Date) error {
	hs, ok := c.Store.(HistoryStore)
	if !ok {
		return nil
	}
	bindings, err := hs.UnificationBindings(ctx, sc)
	if err != nil {
		return err
	}
	for _, a := range bindings {
		if !from.After(a.Period.End()) {
			return kernel.Invalid("rules", "unified periods use the canonical book allocation; choose a later effective date")
		}
	}
	return nil
}
func (c *canonicalStore) ClosedPeriods(ctx context.Context, sc authz.Scope) ([]Period, error) {
	out, err := c.Store.ClosedPeriods(ctx, sc)
	if err != nil {
		return nil, err
	}
	hs, ok := c.Store.(HistoryStore)
	if !ok {
		return out, nil
	}
	bindings, err := hs.UnificationBindings(ctx, sc)
	if err != nil {
		return nil, err
	}
	seen := map[Period]bool{}
	for _, p := range out {
		seen[p] = true
	}
	for _, a := range bindings {
		snap, err := c.books.Snapshot(ctx, canonicalReadScope(ctx, sc), a.BookProductID, a.Period.String(), 0)
		if err != nil {
			return nil, err
		}
		if snap.Closed && !seen[a.Period] {
			out = append(out, a.Period)
			seen[a.Period] = true
		}
	}
	return out, nil
}

func (c *canonicalStore) ClosePeriod(ctx context.Context, sc authz.Scope, p Period, actor string) error {
	if _, bound, err := c.binding(ctx, sc, p); err != nil {
		return err
	} else if bound {
		return kernel.Invalid("period", "close the canonical book; this model period has no independent close state")
	}
	return c.Store.ClosePeriod(ctx, sc, p, actor)
}
func (c *canonicalStore) SaveField(ctx context.Context, sc authz.Scope, f Field) error {
	hs, ok := c.Store.(HistoryStore)
	if ok {
		bindings, err := hs.UnificationBindings(ctx, sc)
		if err != nil {
			return err
		}
		for _, a := range bindings {
			snapshot, err := c.books.Snapshot(ctx, sc, a.BookProductID, a.Period.String(), 0)
			if err != nil {
				return err
			}
			for _, input := range snapshot.Fields {

				key := a.FieldKeys[input.Key]
				if key == "" {
					key = a.MetricKeys[input.Key]
				}
				if key == "" {
					key = input.Key
				}
				if key != f.Key {
					continue
				}
				typ := FieldType(input.Type)
				if typ == "catalog" {
					typ = FieldReference
				}
				next := f.Latest()
				currency := ""
				if typ == FieldMoney {
					currency = snapshot.Currency
				}
				if next.Type != typ || next.Source != FieldSource(input.Source) || next.Currency != currency {
					return kernel.Invalid("field_type", "canonical input type, currency and source are fixed; only display metadata can change")
				}
			}
		}
	}
	return c.Store.SaveField(ctx, sc, f)
}

func appendDerivedBookFacts(facts []FactRow, batch kernel.ID, version int, p Period) []FactRow {
	type group struct{ product, team kernel.ID }
	values := map[group]map[string]decimal.Decimal{}
	for _, r := range facts {
		if r.FieldKey != book.Revenue && r.FieldKey != book.Payroll && r.FieldKey != book.DirectCost && r.FieldKey != book.Marketing && r.FieldKey != book.HubCost && r.FieldKey != book.CertificationCost && r.FieldKey != book.MaintenanceCost {
			continue
		}
		g := group{r.ProductID, r.TeamID}
		if values[g] == nil {
			values[g] = map[string]decimal.Decimal{}
		}
		values[g][r.FieldKey] = values[g][r.FieldKey].Add(r.Value)
	}
	out := append([]FactRow{}, facts...)
	for g, categories := range values {
		cost := decimal.Zero
		for _, k := range []string{book.Payroll, book.DirectCost, book.Marketing, book.CertificationCost, book.MaintenanceCost} {
			cost = cost.Add(categories[k])
		}
		direct := categories[book.Revenue].Sub(cost)
		for key, value := range map[string]decimal.Decimal{"direct_total": cost, "direct_profit": direct, "loaded_profit": direct.Sub(categories[book.HubCost])} {
			out = append(out, FactRow{ID: batch, BatchID: batch, ProductID: g.product, TeamID: g.team, FieldKey: key, Period: p, Value: value, DataVersion: version})
		}
	}
	return out
}

func (c *canonicalStore) TeamShares(ctx context.Context, sc authz.Scope, p Period) ([]TeamShare, error) {
	a, bound, err := c.binding(ctx, sc, p)
	if err != nil {
		return nil, err
	}
	if !bound {
		return c.Store.TeamShares(ctx, sc, p)
	}
	r, err := c.books.Report(ctx, canonicalReadScope(ctx, sc), book.ReportInput{ProductID: a.BookProductID, Period: p.String()})
	if err != nil {
		return nil, err
	}
	known, err := c.canonicalTeamMap(ctx, sc)
	if err != nil {
		return nil, err
	}
	totals := map[string]decimal.Decimal{}
	for _, row := range r.Teams {
		totals[row.TeamID] = totals[row.TeamID].Add(decimal.NewFromInt(row.Cost))
	}
	out := []TeamShare{}
	for _, row := range r.Teams {
		if totals[row.TeamID].IsZero() {
			continue
		}
		id, err := resolveCanonicalTeam(row.TeamID, known)
		if err != nil {
			return nil, err
		}
		out = append(out, TeamShare{TeamID: id, ProductID: row.ProductID, Period: p, Share: decimal.NewFromInt(row.Cost).Div(totals[row.TeamID]), Source: "canonical_book"})
	}
	return out, nil
}
func (c *canonicalStore) Teams(ctx context.Context, sc authz.Scope) ([]Team, error) {
	out, err := c.Store.Teams(ctx, sc)
	if err != nil {
		return nil, err
	}
	hs, ok := c.Store.(HistoryStore)
	if !ok {
		return out, nil
	}
	bindings, err := hs.UnificationBindings(ctx, sc)
	if err != nil {
		return nil, err
	}
	known, err := c.canonicalTeamMap(ctx, sc)
	if err != nil {
		return nil, err
	}
	seen := map[kernel.ID]bool{}
	for _, t := range out {
		seen[t.ID] = true
	}
	for _, a := range bindings {
		report, err := c.books.Report(ctx, canonicalReadScope(ctx, sc), book.ReportInput{ProductID: a.BookProductID, Period: a.Period.String()})
		if err != nil {
			return nil, err
		}
		for _, row := range report.Teams {
			id, err := resolveCanonicalTeam(row.TeamID, known)
			if err != nil {
				return nil, err
			}
			if !seen[id] {
				seen[id] = true
				out = append(out, Team{ID: id, Key: row.TeamID, Name: row.TeamID})
			}
		}
	}
	return out, nil
}

func (c *canonicalStore) canonicalTeamMap(ctx context.Context, sc authz.Scope) (map[string]kernel.ID, error) {
	teams, err := c.Store.Teams(ctx, sc)
	if err != nil {
		return nil, err
	}
	out := map[string]kernel.ID{}
	for _, team := range teams {
		if prior, ok := out[team.Key]; ok && prior != team.ID {
			return nil, kernel.Invalid("teams", "multiple model team IDs share canonical team key")
		}
		out[team.Key] = team.ID
	}
	return out, nil
}
func resolveCanonicalTeam(key string, known map[string]kernel.ID) (kernel.ID, error) {
	if id, ok := known[key]; ok {
		return id, nil
	}
	return CanonicalTeamID(key)
}
func (c *canonicalStore) canonicalTeamID(ctx context.Context, sc authz.Scope, key string) (kernel.ID, error) {
	known, err := c.canonicalTeamMap(ctx, sc)
	if err != nil {
		return kernel.NilID, err
	}
	return resolveCanonicalTeam(key, known)
}
