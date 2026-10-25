package economics

import (
	"context"

	"github.com/onixus/metis/internal/economics/modeling/formula"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// externalFactsStore keeps stored definitions while isolating the entire fact namespace.
// No unmatched field, product, team or period can fall back to separately imported data.
type externalFactsStore struct {
	Store
	rows []FactRow
}

func (s externalFactsStore) Facts(ctx context.Context, sc authz.Scope, f FactFilter) ([]FactRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !sc.Valid() || sc.Finance() < authz.FinanceFull {
		return nil, kernel.ErrForbidden
	}
	if f.Product != nil && !sc.Allows(authz.ActionReadModelFinance, *f.Product) {
		return nil, kernel.ErrForbidden
	}
	out := []FactRow{}
	for _, r := range s.rows {
		if !sc.Allows(authz.ActionReadModelFinance, r.ProductID) {
			return nil, kernel.ErrForbidden
		}
		if f.FieldKey != "" && f.FieldKey != r.FieldKey {
			continue
		}
		if f.Product != nil && *f.Product != r.ProductID {
			continue
		}
		if f.Team != nil && *f.Team != r.TeamID {
			continue
		}
		if f.Period != nil && *f.Period != r.Period {
			continue
		}
		if f.Item != nil && *f.Item != r.Item {
			continue
		}
		if f.DataVersion != nil && *f.DataVersion != r.DataVersion {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// EvaluateExternal evaluates saved, period-effective metric definitions against trusted
// application-projected facts. It performs no writes and never reads stored financial facts.
func (s *Service) EvaluateExternal(ctx context.Context, sc authz.Scope, key string, sl Slice, facts []FactRow, fieldTypes map[string]FieldType) (Explanation, error) {
	if err := s.requireRead(sc, sl.ProductID, authz.FinanceFull); err != nil {
		return Explanation{}, err
	}
	if s.audit == nil {
		return Explanation{}, kernel.ErrUnavailable
	}
	if sl.Period.IsZero() || len(facts) > 500000 || len(fieldTypes) > 512 {
		return Explanation{}, kernel.Invalid("external_facts", "period and bounded facts required")
	}
	for _, row := range facts {
		if typ := fieldTypes[row.FieldKey]; typ != FieldMoney && typ != FieldNumber && typ != FieldPercent {
			return Explanation{}, kernel.Invalid("external_facts", "numeric field type required")
		}
		if row.Period != sl.Period || row.ProductID == kernel.NilID {
			return Explanation{}, kernel.Invalid("external_facts", "facts must belong to the selected period and product")
		}
		if err := s.requireRead(sc, row.ProductID, authz.FinanceFull); err != nil {
			return Explanation{}, err
		}
	}
	if err := s.validateExternalDefinitions(ctx, sc, key, sl.Period, fieldTypes, map[string]bool{}); err != nil {
		return Explanation{}, err
	}
	external, err := NewService(externalFactsStore{Store: s.store, rows: kernel.CloneValue(facts)}, s.pub, s.clock, s.cfg)
	if err != nil {
		return Explanation{}, err
	}
	external.audit = s.audit
	external.platformSources = s.platformSources
	external.platformIdentity = s.platformIdentity
	return external.Explain(ctx, sc, key, sl)
}

func (s *Service) validateExternalDefinitions(ctx context.Context, sc authz.Scope, key string, period Period, fieldTypes map[string]FieldType, visited map[string]bool) error {
	if visited[key] {
		return nil
	}
	visited[key] = true
	metric, err := s.store.Metric(ctx, sc, key)
	if err != nil {
		return err
	}
	version, ok := metric.At(period.End())
	if !ok {
		return kernel.ErrNotFound
	}
	if version.Currency != "" && version.Currency != s.cfg.Currency {
		return kernel.Invalid("currency", "metric and book currencies must match")
	}
	for _, ref := range version.Refs {
		if ref.Kind == formula.RefMetric {
			if err := s.validateExternalDefinitions(ctx, sc, ref.Key, period, fieldTypes, visited); err != nil {
				return err
			}
			continue
		}
		if ref.Kind == formula.RefSource {
			continue
		}
		field, err := s.store.Field(ctx, sc, ref.Key)
		if err != nil {
			return err
		}
		fv, ok := field.At(period.End())
		if !ok {
			return kernel.ErrNotFound
		}
		if fv.Type != FieldMoney && fv.Type != FieldNumber && fv.Type != FieldPercent {
			return kernel.Invalid("field_type", "external formula inputs must be numeric")
		}
		if typ, present := fieldTypes[ref.Key]; present && typ != fv.Type {
			return kernel.Invalid("field_type", "book and model input field types must match")
		}
		if fv.Type == FieldMoney && fv.Currency != s.cfg.Currency {
			return kernel.Invalid("currency", "field and book currencies must match")
		}
	}
	return nil
}
