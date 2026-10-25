// Package pgstore persists modeling documents as immutable PostgreSQL revisions.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/economics/modeling/internal/db"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
)

type Store struct{ database *pgdb.DB }

var _ economics.Store = (*Store)(nil)

func New(database *pgdb.DB) *Store { return &Store{database: database} }
func (s *Store) queries(ctx context.Context) *db.Queries {
	return db.New(pgdb.Querier(ctx, s.database))
}

func requireRead(sc authz.Scope, full bool) error {
	if !sc.Valid() || sc.Finance() < authz.FinanceAggregates || (full && (sc.Finance() < authz.FinanceFull || !sc.SeesAllProducts())) {
		return kernel.ErrForbidden
	}
	return nil
}
func requireWrite(sc authz.Scope) error {
	if !sc.Allows(authz.ActionWriteModelFinance, kernel.NilID) || !sc.SeesAllProducts() {
		return kernel.ErrForbidden
	}
	return nil
}

// Transact serializes model changes, including callers outside the HTTP boundary.
func (s *Store) Transact(ctx context.Context, sc authz.Scope, fn func(context.Context) error) error {
	if err := requireWrite(sc); err != nil {
		return err
	}
	return s.database.Transact(ctx, func(ctx context.Context) error {
		if err := pgdb.LockApplication(ctx, s.database); err != nil {
			return err
		}
		return fn(ctx)
	})
}
func get[T any](ctx context.Context, s *Store, kind, key string) (T, error) {
	var out T
	raw, err := s.queries(ctx).GetDocument(ctx, db.GetDocumentParams{Kind: kind, Key: key})
	if err != nil {
		return out, fmt.Errorf("modeling %s: %w", kind, pgdb.MapError(err))
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("decode %s: %w", kind, err)
	}
	return out, nil
}
func list[T any](ctx context.Context, s *Store, kind string) ([]T, error) {
	rows, err := s.queries(ctx).ListDocuments(ctx, kind)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", kind, pgdb.MapError(err))
	}
	out := make([]T, 0, len(rows))
	for _, r := range rows {
		var v T
		if err := json.Unmarshal(r.Body, &v); err != nil {
			return nil, fmt.Errorf("decode %s: %w", kind, err)
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Store) append(ctx context.Context, kind, key string, product kernel.ID, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode %s: %w", kind, err)
	}
	if err := s.queries(ctx).AppendDocument(ctx, db.AppendDocumentParams{Kind: kind, Key: key, ProductID: product, Body: raw}); err != nil {
		return fmt.Errorf("append %s: %w", kind, pgdb.MapError(err))
	}
	return nil
}
func (s *Store) save(ctx context.Context, sc authz.Scope, kind, key string, v any, immutable bool) error {
	return s.Transact(ctx, sc, func(ctx context.Context) error {
		if immutable {
			_, err := get[json.RawMessage](ctx, s, kind, key)
			if err == nil {
				return kernel.ErrConflict
			}
			if !errors.Is(err, kernel.ErrNotFound) {
				return err
			}
		}
		product := kernel.NilID
		if fact, ok := v.(economics.FactRow); ok {
			product = fact.ProductID
		}
		return s.append(ctx, kind, key, product, v)
	})
}
func (s *Store) SaveField(ctx context.Context, sc authz.Scope, v economics.Field) error {
	return s.Transact(ctx, sc, func(ctx context.Context) error {
		prev, err := get[economics.Field](ctx, s, "field", v.Key)
		if err != nil && !errors.Is(err, kernel.ErrNotFound) {
			return err
		}
		if err == nil && (prev.ID != v.ID || len(v.Versions) != len(prev.Versions)+1 || !reflect.DeepEqual(prev.Versions, v.Versions[:len(prev.Versions)])) {
			return kernel.ErrConflict
		}
		if len(v.Versions) == 0 || (errors.Is(err, kernel.ErrNotFound) && len(v.Versions) != 1) || v.Latest().Version != len(v.Versions) {
			return kernel.ErrConflict
		}
		return s.append(ctx, "field", v.Key, kernel.NilID, v)
	})
}
func (s *Store) Field(ctx context.Context, sc authz.Scope, key string) (economics.Field, error) {
	if err := requireRead(sc, false); err != nil {
		return economics.Field{}, err
	}
	v, err := get[economics.Field](ctx, s, "field", key)
	return v, err
}
func (s *Store) Fields(ctx context.Context, sc authz.Scope) ([]economics.Field, error) {
	if err := requireRead(sc, false); err != nil {
		return nil, err
	}
	out, err := list[economics.Field](ctx, s, "field")
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
func (s *Store) SaveMetric(ctx context.Context, sc authz.Scope, v economics.Metric) error {
	return s.Transact(ctx, sc, func(ctx context.Context) error {
		prev, err := get[economics.Metric](ctx, s, "metric", v.Key)
		if err != nil && !errors.Is(err, kernel.ErrNotFound) {
			return err
		}
		if err == nil && (prev.ID != v.ID || len(v.Versions) != len(prev.Versions)+1 || !reflect.DeepEqual(prev.Versions, v.Versions[:len(prev.Versions)])) {
			return kernel.ErrConflict
		}
		if len(v.Versions) == 0 || (errors.Is(err, kernel.ErrNotFound) && len(v.Versions) != 1) || v.Latest().Version != len(v.Versions) {
			return kernel.ErrConflict
		}
		return s.append(ctx, "metric", v.Key, kernel.NilID, v)
	})
}
func (s *Store) Metric(ctx context.Context, sc authz.Scope, key string) (economics.Metric, error) {
	if err := requireRead(sc, false); err != nil {
		return economics.Metric{}, err
	}
	v, err := get[economics.Metric](ctx, s, "metric", key)
	return v, err
}
func (s *Store) Metrics(ctx context.Context, sc authz.Scope) ([]economics.Metric, error) {
	if err := requireRead(sc, false); err != nil {
		return nil, err
	}
	out, err := list[economics.Metric](ctx, s, "metric")
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
func (s *Store) SaveTemplate(ctx context.Context, sc authz.Scope, v economics.Template) error {
	return s.save(ctx, sc, "template", v.ID.String(), v, false)
}
func (s *Store) Template(ctx context.Context, sc authz.Scope, key kernel.ID) (economics.Template, error) {
	if err := requireRead(sc, false); err != nil {
		return economics.Template{}, err
	}
	v, err := get[economics.Template](ctx, s, "template", key.String())
	return v, err
}
func (s *Store) Templates(ctx context.Context, sc authz.Scope) ([]economics.Template, error) {
	if err := requireRead(sc, false); err != nil {
		return nil, err
	}
	out, err := list[economics.Template](ctx, s, "template")
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (s *Store) SaveTeam(ctx context.Context, sc authz.Scope, v economics.Team) error {
	return s.save(ctx, sc, "team", v.ID.String(), v, false)
}
func (s *Store) Teams(ctx context.Context, sc authz.Scope) ([]economics.Team, error) {
	if err := requireRead(sc, false); err != nil {
		return nil, err
	}
	out, err := list[economics.Team](ctx, s, "team")
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
func (s *Store) SaveScenario(ctx context.Context, sc authz.Scope, v economics.Scenario) error {
	return s.save(ctx, sc, "scenario", v.ID.String(), v, false)
}
func (s *Store) Scenario(ctx context.Context, sc authz.Scope, key kernel.ID) (economics.Scenario, error) {
	if err := requireRead(sc, false); err != nil {
		return economics.Scenario{}, err
	}
	v, err := get[economics.Scenario](ctx, s, "scenario", key.String())
	if err == nil && !scenarioVisible(sc, v) {
		return economics.Scenario{}, kernel.ErrForbidden
	}
	return v, err
}
func (s *Store) Scenarios(ctx context.Context, sc authz.Scope) ([]economics.Scenario, error) {
	if err := requireRead(sc, false); err != nil {
		return nil, err
	}
	out, err := list[economics.Scenario](ctx, s, "scenario")
	if err != nil {
		return nil, err
	}
	kept := out[:0]
	for _, v := range out {
		if scenarioVisible(sc, v) {
			kept = append(kept, v)
		}
	}
	out = kept
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func scenarioVisible(sc authz.Scope, v economics.Scenario) bool {
	if len(v.Products) == 0 && !sc.SeesAllProducts() {
		return false
	}
	for _, p := range v.Products {
		if !sc.Allows(authz.ActionReadModelFinance, p) {
			return false
		}
	}
	for _, o := range v.Overrides {
		if !sc.Allows(authz.ActionReadModelFinance, o.ProductID) {
			return false
		}
	}
	return true
}
func (s *Store) SaveBatch(ctx context.Context, sc authz.Scope, v economics.ImportBatch) error {
	return s.save(ctx, sc, "batch", v.ID.String(), v, true)
}
func (s *Store) Batch(ctx context.Context, sc authz.Scope, id kernel.ID) (economics.ImportBatch, error) {
	if err := requireRead(sc, true); err != nil {
		return economics.ImportBatch{}, err
	}
	return get[economics.ImportBatch](ctx, s, "batch", id.String())
}
func (s *Store) Batches(ctx context.Context, sc authz.Scope, p economics.Period) ([]economics.ImportBatch, error) {
	if err := requireRead(sc, true); err != nil {
		return nil, err
	}
	out, err := list[economics.ImportBatch](ctx, s, "batch")
	if err != nil {
		return nil, err
	}
	kept := out[:0]
	for _, v := range out {
		if p.IsZero() || p == v.Period {
			kept = append(kept, v)
		}
	}
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].Period != kept[j].Period {
			return kept[i].Period.Before(kept[j].Period)
		}
		return kept[i].DataVersion < kept[j].DataVersion
	})
	return kept, nil
}
func (s *Store) AppendFacts(ctx context.Context, sc authz.Scope, rows []economics.FactRow) error {
	if err := requireWrite(sc); err != nil {
		return err
	}
	for _, v := range rows {
		if !sc.Allows(authz.ActionReadModelFinance, v.ProductID) {
			return kernel.ErrForbidden
		}
	}
	return s.Transact(ctx, sc, func(ctx context.Context) error {
		for _, v := range rows {
			batch, err := get[economics.ImportBatch](ctx, s, "batch", v.BatchID.String())
			if err != nil {
				return err
			}
			if batch.Status != economics.BatchApplied || batch.Period != v.Period || batch.DataVersion != v.DataVersion {
				return kernel.ErrConflict
			}
			if err := s.save(ctx, sc, "fact", v.ID.String(), v, true); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Store) Facts(ctx context.Context, sc authz.Scope, f economics.FactFilter) ([]economics.FactRow, error) {
	if !sc.Valid() || sc.Finance() < authz.FinanceFull {
		return nil, kernel.ErrForbidden
	}
	if f.Product != nil && !sc.Allows(authz.ActionReadModelFinance, *f.Product) {
		return nil, kernel.ErrForbidden
	}
	version := int64(0)
	if f.DataVersion != nil {
		version = int64(*f.DataVersion)
	}
	raw, err := s.queries(ctx).ListFacts(ctx, db.ListFactsParams{AllProducts: sc.SeesAllProducts(), Products: sc.ProductIDs(authz.AccessStrategic), ExplicitVersion: f.DataVersion != nil, DataVersion: version})
	if err != nil {
		return nil, fmt.Errorf("facts: %w", pgdb.MapError(err))
	}
	out := make([]economics.FactRow, 0, len(raw))
	for _, r := range raw {
		var v economics.FactRow
		if err := json.Unmarshal(r, &v); err != nil {
			return nil, fmt.Errorf("decode fact: %w", err)
		}
		if !sc.Allows(authz.ActionReadModelFinance, v.ProductID) {
			continue
		}
		if f.FieldKey != "" && f.FieldKey != v.FieldKey || f.Product != nil && *f.Product != v.ProductID || f.Team != nil && *f.Team != v.TeamID || f.Period != nil && *f.Period != v.Period || f.Item != nil && *f.Item != v.Item {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Store) SaveAllocationRule(ctx context.Context, sc authz.Scope, v economics.AllocationRule) error {
	return s.save(ctx, sc, "allocation", v.HubProductID.String()+":"+strconv.Itoa(v.Version), v, true)
}
func (s *Store) AllocationRules(ctx context.Context, sc authz.Scope) ([]economics.AllocationRule, error) {
	if err := requireRead(sc, false); err != nil {
		return nil, err
	}
	out, err := list[economics.AllocationRule](ctx, s, "allocation")
	if err != nil {
		return nil, err
	}
	kept := out[:0]
	for _, v := range out {
		visible := true
		for p := range v.Shares {
			if !sc.Allows(authz.ActionReadModelFinance, p) {
				visible = false
			}
		}
		if !sc.Allows(authz.ActionReadModelFinance, v.HubProductID) {
			visible = false
		}
		if visible {
			kept = append(kept, v)
		}
	}
	out = kept
	sort.Slice(out, func(i, j int) bool { return out[i].EffectiveFrom.Before(out[j].EffectiveFrom) })
	return out, nil
}
func (s *Store) SaveBundleRule(ctx context.Context, sc authz.Scope, v economics.BundleRule) error {
	return s.save(ctx, sc, "bundle", v.BundleKey+":"+strconv.Itoa(v.Version), v, true)
}
func (s *Store) BundleRules(ctx context.Context, sc authz.Scope) ([]economics.BundleRule, error) {
	if err := requireRead(sc, false); err != nil {
		return nil, err
	}
	out, err := list[economics.BundleRule](ctx, s, "bundle")
	if err != nil {
		return nil, err
	}
	kept := out[:0]
	for _, v := range out {
		visible := true
		for p := range v.Shares {
			if !sc.Allows(authz.ActionReadModelFinance, p) {
				visible = false
			}
		}
		if visible {
			kept = append(kept, v)
		}
	}
	out = kept
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}
func (s *Store) SaveTeamShares(ctx context.Context, sc authz.Scope, shares []economics.TeamShare) error {
	grouped := map[string][]economics.TeamShare{}
	for _, v := range shares {
		key := v.TeamID.String() + ":" + v.Period.String()
		grouped[key] = append(grouped[key], v)
	}
	return s.Transact(ctx, sc, func(ctx context.Context) error {
		for key, v := range grouped {
			if err := s.append(ctx, "shares", key, kernel.NilID, v); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Store) TeamShares(ctx context.Context, sc authz.Scope, p economics.Period) ([]economics.TeamShare, error) {
	if err := requireRead(sc, false); err != nil {
		return nil, err
	}
	groups, err := list[[]economics.TeamShare](ctx, s, "shares")
	if err != nil {
		return nil, err
	}
	out := make([]economics.TeamShare, 0)
	for _, group := range groups {
		for _, v := range group {
			if sc.Allows(authz.ActionReadModelFinance, v.ProductID) && (p.IsZero() || p == v.Period) {
				out = append(out, v)
			}
		}
	}
	return out, nil
}

type closedPeriod struct {
	Period economics.Period `json:"period"`
	Actor  string           `json:"actor"`
}

func (s *Store) ClosePeriod(ctx context.Context, sc authz.Scope, p economics.Period, actor string) error {
	return s.Transact(ctx, sc, func(ctx context.Context) error {
		_, err := get[closedPeriod](ctx, s, "closed", p.String())
		if err == nil {
			return nil
		}
		if !errors.Is(err, kernel.ErrNotFound) {
			return err
		}
		return s.append(ctx, "closed", p.String(), kernel.NilID, closedPeriod{p, actor})
	})
}
func (s *Store) ClosedPeriods(ctx context.Context, sc authz.Scope) ([]economics.Period, error) {
	if err := requireRead(sc, false); err != nil {
		return nil, err
	}
	rows, err := list[closedPeriod](ctx, s, "closed")
	if err != nil {
		return nil, err
	}
	out := make([]economics.Period, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Period)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out, nil
}
