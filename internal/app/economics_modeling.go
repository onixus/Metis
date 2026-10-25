package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/onixus/metis/internal/economics"
	modeling "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// bookTeamID maps an opaque book team key into a stable, namespaced UUID.
func bookTeamID(key string) (kernel.ID, error) {
	if key == "" {
		return kernel.NilID, nil
	}
	sum := sha256.Sum256([]byte("metis:economics:team:" + key))
	return kernel.ParseID(hex.EncodeToString(sum[:16]))
}

// EvaluateBookMetric shares modeling's saved formulas and engine with a pinned book version.
// It does not merge the two stores or apply the modeling hub/bundle allocations again.
func (a *App) EvaluateBookMetric(ctx context.Context, sc authz.Scope, bookProduct kernel.ID, period string, version int, key string, filterProduct kernel.ID, team string) (economics.SnapshotInfo, modeling.Explanation, error) {
	if a.Economics == nil || a.Modeling == nil {
		return economics.SnapshotInfo{}, modeling.Explanation{}, kernel.ErrUnavailable
	}
	if filterProduct != kernel.NilID {
		if err := sc.Require(authz.ActionReadFinance, filterProduct); err != nil {
			return economics.SnapshotInfo{}, modeling.Explanation{}, err
		}
	}
	periodValue, err := modeling.ParsePeriod(period)
	if err != nil {
		return economics.SnapshotInfo{}, modeling.Explanation{}, err
	}
	bound, err := a.Modeling.BookDefinitionsUnified(ctx, sc, bookProduct, periodValue)
	if err != nil {
		return economics.SnapshotInfo{}, modeling.Explanation{}, err
	}
	if bound {
		return a.Modeling.EvaluateCanonicalBookMetric(ctx, sc, bookProduct, periodValue, version, key, filterProduct, team)
	}
	projection, err := a.Economics.Project(ctx, sc, bookProduct, period, version)
	if err != nil {
		return economics.SnapshotInfo{}, modeling.Explanation{}, err
	}
	if projection.Snapshot.Currency != a.Modeling.Config().Currency {
		return economics.SnapshotInfo{}, modeling.Explanation{}, kernel.Invalid("currency", "book and modeling currencies must match")
	}
	p, err := modeling.ParsePeriod(period)
	if err != nil {
		return economics.SnapshotInfo{}, modeling.Explanation{}, err
	}
	teamID, err := bookTeamID(team)
	if err != nil {
		return economics.SnapshotInfo{}, modeling.Explanation{}, err
	}
	cfg := a.Modeling.Config()
	aliases := map[string]string{economics.Revenue: cfg.RevenueField, economics.Payroll: cfg.PayrollField, economics.DirectCost: cfg.DirectCostField, economics.Marketing: cfg.MarketingField}
	present := map[string]bool{}
	for _, f := range projection.Facts {
		present[f.FieldKey] = true
	}
	for from, to := range aliases {
		if to != "" && from != to && present[to] {
			return economics.SnapshotInfo{}, modeling.Explanation{}, kernel.Invalid("fields", "book field conflicts with modeling category alias")
		}
	}
	facts := make([]modeling.FactRow, 0, len(projection.Facts)*2)
	fieldTypes := map[string]modeling.FieldType{}
	for _, f := range projection.Facts {
		tid, err := bookTeamID(f.TeamID)
		if err != nil {
			return economics.SnapshotInfo{}, modeling.Explanation{}, err
		}
		fact := modeling.FactRow{BookProductID: projection.Snapshot.ProductID, ID: f.RowID, BatchID: projection.Snapshot.ID, ProductID: f.ProductID, TeamID: tid, Period: p, FieldKey: f.FieldKey, Value: f.Value, DataVersion: projection.Snapshot.Version, Sheet: f.Source.Sheet, Row: f.Source.Row}
		facts = append(facts, fact)
		fieldTypes[fact.FieldKey] = modeling.FieldType(f.Type)
		if alias := aliases[f.FieldKey]; alias != "" && alias != f.FieldKey {
			fact.FieldKey = alias
			fieldTypes[alias] = modeling.FieldType(f.Type)
			facts = append(facts, fact)
		}
	}
	metric, err := a.Modeling.EvaluateExternal(ctx, sc, key, modeling.Slice{ProductID: filterProduct, TeamID: teamID, Period: p}, facts, fieldTypes)
	if err != nil {
		return economics.SnapshotInfo{}, modeling.Explanation{}, err
	}
	return projection.Snapshot, metric, nil
}
