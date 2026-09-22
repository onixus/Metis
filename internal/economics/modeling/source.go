package economics

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/economics/modeling/formula"
	"github.com/onixus/metis/internal/kernel"
)

// maxExplainDepth ограничивает глубину раскрытия объяснения (EC-10).
const maxExplainDepth = 6

// factSource — источник данных формул поверх хранилища. Сценарии (EC-13, DA-02) подменяют
// значения через overrides, не изменяя факты.
type factSource struct {
	ctx       context.Context
	svc       *Service
	overrides []Override
	visiting  map[string]bool
	memo      map[string]decimal.Decimal
}

func (s *Service) source(ctx context.Context, overrides []Override) *factSource {
	return &factSource{ctx: ctx, svc: s, overrides: overrides,
		visiting: map[string]bool{}, memo: map[string]decimal.Decimal{}}
}

var _ formula.Source = (*factSource)(nil)

// Values возвращает значения поля по фильтру формулы.
func (f *factSource) Values(key string, flt formula.Filter) ([]decimal.Decimal, error) {
	filter, err := factFilter(key, flt)
	if err != nil {
		return nil, err
	}
	if ov, ok := f.override(key, filter); ok {
		return []decimal.Decimal{ov}, nil
	}
	rows, err := f.svc.store.Facts(f.ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	out := make([]decimal.Decimal, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Value)
	}
	return out, nil
}

// rows возвращает исходные строки поля — для объяснения значения (EC-10).
func (f *factSource) rows(key string, flt formula.Filter) ([]FactRow, error) {
	filter, err := factFilter(key, flt)
	if err != nil {
		return nil, err
	}
	rows, err := f.svc.store.Facts(f.ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("facts: %w", err)
	}
	return rows, nil
}

// override возвращает подменённое значение сценария, если оно задано для этого среза.
func (f *factSource) override(key string, filter FactFilter) (decimal.Decimal, bool) {
	for _, o := range f.overrides {
		if o.FieldKey != key {
			continue
		}
		if o.ProductID != kernel.NilID && (filter.Product == nil || *filter.Product != o.ProductID) {
			continue
		}
		if !o.Period.IsZero() && (filter.Period == nil || *filter.Period != o.Period) {
			continue
		}
		return o.Value, true
	}
	return decimal.Zero, false
}

func factFilter(key string, flt formula.Filter) (FactFilter, error) {
	out := FactFilter{FieldKey: key}
	set := func(dim formula.Dimension, raw string) error {
		if raw == "" {
			return nil
		}
		switch dim {
		case formula.DimProduct:
			id, err := kernel.ParseID(raw)
			if err != nil {
				return err
			}
			out.Product = &id
		case formula.DimTeam:
			id, err := kernel.ParseID(raw)
			if err != nil {
				return err
			}
			out.Team = &id
		case formula.DimPeriod:
			p, err := ParsePeriod(raw)
			if err != nil {
				return err
			}
			out.Period = &p
		case formula.DimItem:
			item := raw
			out.Item = &item
		}
		return nil
	}
	for dim, raw := range map[formula.Dimension]string{
		formula.DimProduct: flt.Slice.Product,
		formula.DimTeam:    flt.Slice.Team,
		formula.DimPeriod:  flt.Slice.Period,
		formula.DimItem:    flt.Slice.Item,
	} {
		if err := set(dim, raw); err != nil {
			return FactFilter{}, err
		}
	}
	if flt.Dim != "" {
		switch flt.Dim {
		case formula.DimProduct:
			out.Product = nil
		case formula.DimTeam:
			out.Team = nil
		case formula.DimPeriod:
			out.Period = nil
		case formula.DimItem:
			out.Item = nil
		}
		if flt.Value != formula.Any {
			if err := set(flt.Dim, flt.Value); err != nil {
				return FactFilter{}, err
			}
		}
	}
	return out, nil
}

// Metric вычисляет другой показатель в срезе; цикл во время вычисления — ошибка.
func (f *factSource) Metric(key string, sl formula.Slice) (decimal.Decimal, error) {
	cacheKey := key + "|" + sl.Product + "|" + sl.Team + "|" + sl.Period + "|" + sl.Item
	if v, ok := f.memo[cacheKey]; ok {
		return v, nil
	}
	if f.visiting[cacheKey] {
		return decimal.Zero, fmt.Errorf("%w: показатель %q ссылается сам на себя", kernel.ErrConflict, key)
	}
	mv, err := f.version(key, sl)
	if err != nil {
		return decimal.Zero, err
	}
	f.visiting[cacheKey] = true
	defer delete(f.visiting, cacheKey)
	v, err := f.evalExpression(mv.Expression, sl)
	if err != nil {
		return decimal.Zero, err
	}
	f.memo[cacheKey] = v
	return v, nil
}

// version выбирает версию формулы, действующую на период среза (EC-11).
func (f *factSource) version(key string, sl formula.Slice) (MetricVersion, error) {
	m, err := f.svc.store.Metric(f.ctx, key)
	if err != nil {
		return MetricVersion{}, err
	}
	at := kernel.Date{}
	if sl.Period != "" {
		p, err := ParsePeriod(sl.Period)
		if err != nil {
			return MetricVersion{}, err
		}
		at = p.End()
	}
	mv, ok := m.At(at)
	if !ok {
		return MetricVersion{}, fmt.Errorf("%w: формула показателя %q на период %s", kernel.ErrNotFound, key, sl.Period)
	}
	return mv, nil
}

func (f *factSource) evalExpression(expr string, sl formula.Slice) (decimal.Decimal, error) {
	prog, err := f.svc.program(expr)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%w: %w", kernel.ErrValidation, err)
	}
	v, err := prog.Eval(f, sl)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%w: показатель: %w", kernel.ErrValidation, err)
	}
	return v, nil
}

// explainMetric раскрывает значение показателя до формулы, вкладов и строк импорта (EC-10).
func (f *factSource) explainMetric(key string, sl formula.Slice, depth int) (Explanation, error) {
	mv, err := f.version(key, sl)
	if err != nil {
		return Explanation{}, err
	}
	value, err := f.Metric(key, sl)
	if err != nil {
		return Explanation{}, err
	}
	ex := Explanation{Key: key, Kind: formula.RefMetric, Expression: mv.Expression, Value: value}
	if depth >= maxExplainDepth {
		return ex, nil
	}
	prog, err := f.svc.program(mv.Expression)
	if err != nil {
		return Explanation{}, fmt.Errorf("%w: %w", kernel.ErrValidation, err)
	}
	for _, ref := range prog.Refs() {
		switch ref.Kind {
		case formula.RefField:
			rows, err := f.rows(ref.Key, formula.Filter{Slice: sl})
			if err != nil {
				return Explanation{}, err
			}
			sum := decimal.Zero
			for _, r := range rows {
				sum = sum.Add(r.Value)
			}
			ex.Inputs = append(ex.Inputs, Explanation{Key: ref.Key, Kind: formula.RefField, Value: sum, Rows: rows})
		case formula.RefMetric:
			sub, err := f.explainMetric(ref.Key, sl, depth+1)
			if err != nil {
				return Explanation{}, err
			}
			ex.Inputs = append(ex.Inputs, sub)
		}
	}
	return ex, nil
}
