package economics

import (
	"context"
	"fmt"
	"sync"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/economics/modeling/formula"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// maxExplainDepth ограничивает глубину раскрытия объяснения (EC-10).
const maxExplainDepth = 6

// factSource — источник данных формул поверх хранилища. Сценарии (EC-13, DA-02) подменяют
// значения через overrides, не изменяя факты.
type factSource struct {
	sc            authz.Scope
	caller        authz.Scope
	platformMemo  map[platformCacheKey]PlatformValue
	platformCost  *int
	platformLock  *sync.Mutex
	cacheScope    string
	sourceErr     error
	currencyStack []string
	metricStack   []string
	crmMetrics    map[string]bool
	fields        map[fieldReadKey]fieldRead
	fieldRows     int
	fieldTraces   map[string]map[fieldReadKey]bool
	explainBudget *explanationBudget
	ctx           context.Context
	svc           *Service
	overrides     []Override
	visiting      map[string]bool
	memo          map[string]decimal.Decimal
	active        string
	traces        map[string][]evaluatedInput
}

type evaluatedInput struct {
	field  *Explanation
	metric string
	slice  formula.Slice
}

func (s *Service) source(ctx context.Context, sc authz.Scope, overrides []Override) *factSource {
	ctx = WithFormulaSnapshot(WithCanonicalReadScope(ctx, sc))
	cost := 0
	values := map[platformCacheKey]PlatformValue{}
	costPointer := &cost
	lock := &sync.Mutex{}
	explainBudget := &explanationBudget{}
	if session, ok := ctx.Value(formulaSnapshotKey{}).(*formulaSnapshot); ok {
		values = session.values
		costPointer = &session.cost
		lock = &session.mu
		explainBudget = &session.explain
	}
	scopeKey, err := formulaScopeKey(sc)
	return &factSource{ctx: ctx, sc: identityaccess.ModelCalculationScope(sc), caller: sc, platformMemo: values, platformCost: costPointer, platformLock: lock, cacheScope: scopeKey, sourceErr: err, crmMetrics: map[string]bool{}, fields: map[fieldReadKey]fieldRead{}, fieldTraces: map[string]map[fieldReadKey]bool{}, explainBudget: explainBudget, svc: s, overrides: overrides,
		visiting: map[string]bool{}, memo: map[string]decimal.Decimal{}, traces: map[string][]evaluatedInput{}}
}

var _ formula.Source = (*factSource)(nil)

// Values возвращает значения поля по фильтру формулы.
func (f *factSource) Values(key string, flt formula.Filter) ([]decimal.Decimal, error) {
	filter, err := factFilter(key, flt)
	if err != nil {
		return nil, err
	}
	values, err := f.fieldValues(key, filter)
	if err != nil {
		f.sourceErr = err
	}
	return values, err
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
	cacheKey := metricSliceKey(key, sl)
	if f.active != "" {
		f.traces[f.active] = append(f.traces[f.active], evaluatedInput{metric: key, slice: sl})
	}
	if v, ok := f.memo[cacheKey]; ok {
		if f.crmMetrics[cacheKey] {
			if err := f.markCRMRead(); err != nil {
				f.sourceErr = err
				return decimal.Zero, err
			}
		}
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
	previous := f.active
	f.active = cacheKey
	f.metricStack = append(f.metricStack, cacheKey)
	defer func() {
		f.active = previous
		f.metricStack = f.metricStack[:len(f.metricStack)-1]
		delete(f.visiting, cacheKey)
	}()
	v, err := f.evalVersionExpression(mv, sl)
	if err != nil {
		return decimal.Zero, err
	}
	f.memo[cacheKey] = v
	return v, nil
}

// version выбирает версию формулы, действующую на период среза (EC-11).
func (f *factSource) version(key string, sl formula.Slice) (MetricVersion, error) {
	m, err := f.svc.store.Metric(f.ctx, f.sc, key)
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

func (f *factSource) evalVersionExpression(version MetricVersion, sl formula.Slice) (decimal.Decimal, error) {
	f.currencyStack = append(f.currencyStack, version.Currency)
	defer func() { f.currencyStack = f.currencyStack[:len(f.currencyStack)-1] }()
	value, err := f.evalExpression(version.Expression, sl)
	if err != nil {
		return decimal.Zero, err
	}
	if version.ResultType == FieldMoney && (!value.Equal(value.Truncate(0)) || !value.BigInt().IsInt64()) {
		err = kernel.Invalid("value", "денежный результат формулы должен быть целым int64 в минорных единицах")
		f.sourceErr = err
		return decimal.Zero, err
	}
	return value, nil
}

func (f *factSource) markCRMRead() error {
	for _, currency := range f.currencyStack {
		if currency != "" && currency != f.svc.cfg.Currency {
			return kernel.Invalid("currency", "формула с выручкой CRM должна использовать валюту модели")
		}
	}
	for _, key := range f.metricStack {
		f.crmMetrics[key] = true
	}
	return nil
}

func (f *factSource) evalExpression(expr string, sl formula.Slice) (decimal.Decimal, error) {
	prog, err := f.svc.program(expr)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%w: %w", kernel.ErrValidation, err)
	}
	v, err := prog.Eval(f, sl)
	if f.sourceErr != nil {
		return decimal.Zero, f.sourceErr
	}
	if err != nil {
		return decimal.Zero, fmt.Errorf("%w: показатель: %w", kernel.ErrValidation, err)
	}
	return v, nil
}

// explainMetric раскрывает значение показателя до формулы, вкладов и строк импорта (EC-10).
func (f *factSource) explainMetric(key string, sl formula.Slice, depth int) (Explanation, error) {
	if err := f.reserveExplanation(0, 1, 0); err != nil {
		return Explanation{}, err
	}
	mv, err := f.version(key, sl)
	if err != nil {
		return Explanation{}, err
	}
	value, err := f.Metric(key, sl)
	if err != nil {
		return Explanation{}, err
	}
	ex := Explanation{Key: key, Kind: formula.RefMetric, Expression: mv.Expression, Value: value}
	if depth >= maxExplainDepth && len(f.traces[metricSliceKey(key, sl)]) > 0 {
		return Explanation{}, kernel.Invalid("explanation", "глубина объяснения превышает 6; раскройте вложенный показатель отдельно")
	}
	for _, input := range f.traces[metricSliceKey(key, sl)] {
		if input.field != nil {
			provenance := 0
			for _, source := range input.field.Sources {
				provenance += 1 + len(source.Provenance)
			}
			if err := f.reserveExplanation(len(input.field.Rows), 1, provenance); err != nil {
				return Explanation{}, err
			}
			ex.Inputs = append(ex.Inputs, *input.field)
			continue
		}
		sub, err := f.explainMetric(input.metric, input.slice, depth+1)
		if err != nil {
			return Explanation{}, err
		}
		ex.Inputs = append(ex.Inputs, sub)
	}
	return ex, nil
}

func metricSliceKey(key string, sl formula.Slice) string {
	return key + "|" + sl.Product + "|" + sl.Team + "|" + sl.Period + "|" + sl.Item
}
