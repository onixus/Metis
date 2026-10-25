package economics

import (
	"github.com/onixus/metis/internal/economics/modeling/formula"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

type fieldReadKey struct {
	key        string
	product    kernel.ID
	productSet bool
	team       kernel.ID
	teamSet    bool
	period     Period
	periodSet  bool
	item       string
	itemSet    bool
	version    int
	versionSet bool
	override   string
}
type fieldRead struct {
	rows   []FactRow
	values []decimal.Decimal
	sum    decimal.Decimal
}
type explanationBudget struct{ rows, nodes, provenance int }

func fieldKey(filter FactFilter) fieldReadKey {
	key := fieldReadKey{key: filter.FieldKey}
	if filter.Product != nil {
		key.product, key.productSet = *filter.Product, true
	}
	if filter.Team != nil {
		key.team, key.teamSet = *filter.Team, true
	}
	if filter.Period != nil {
		key.period, key.periodSet = *filter.Period, true
	}
	if filter.Item != nil {
		key.item, key.itemSet = *filter.Item, true
	}
	if filter.DataVersion != nil {
		key.version, key.versionSet = *filter.DataVersion, true
	}
	return key
}

func (f *factSource) traceField(key fieldReadKey, value Explanation) {
	if f.active == "" {
		return
	}
	seen := f.fieldTraces[f.active]
	if seen == nil {
		seen = map[fieldReadKey]bool{}
		f.fieldTraces[f.active] = seen
	}
	if seen[key] {
		return
	}
	seen[key] = true
	f.traces[f.active] = append(f.traces[f.active], evaluatedInput{field: &value})
}

func (f *factSource) fieldValues(key string, filter FactFilter) ([]decimal.Decimal, error) {
	readKey := fieldKey(filter)
	if override, ok := f.override(key, filter); ok {
		if err := formula.ValidateDecimal(override); err != nil {
			return nil, kernel.Invalid("facts", err.Error())
		}
		readKey.override = override.String()
		f.traceField(readKey, Explanation{Key: key, Kind: formula.RefField, Value: override})
		return []decimal.Decimal{override}, nil
	}
	read, ok := f.fields[readKey]
	if !ok {
		rows, err := f.svc.store.Facts(f.ctx, f.sc, filter)
		if err != nil {
			return nil, err
		}
		if len(rows) > 500000-f.fieldRows {
			return nil, kernel.Invalid("facts", "превышен общий предел 500000 строк полей за расчёт")
		}
		f.fieldRows += len(rows)
		read = fieldRead{rows: rows, values: make([]decimal.Decimal, 0, len(rows)), sum: decimal.Zero}
		for _, row := range rows {
			if err := formula.ValidateDecimal(row.Value); err != nil {
				return nil, kernel.Invalid("facts", err.Error())
			}
			read.values = append(read.values, row.Value)
			read.sum = read.sum.Add(row.Value)
			if err := formula.ValidateDecimal(read.sum); err != nil {
				return nil, kernel.Invalid("facts", err.Error())
			}
		}
		f.fields[readKey] = read
	}
	f.traceField(readKey, Explanation{Key: key, Kind: formula.RefField, Value: read.sum, Rows: read.rows})
	return read.values, nil
}

func (f *factSource) reserveExplanation(rows, nodes, provenance int) error {
	f.platformLock.Lock()
	defer f.platformLock.Unlock()
	budget := f.explainBudget
	if rows > 50000-budget.rows || nodes > 4096-budget.nodes || provenance > 16384-budget.provenance {
		return kernel.Invalid("explanation", "превышен бюджет объяснения: 50000 строк, 4096 узлов или 16384 элементов происхождения; рассчитайте меньший срез")
	}
	budget.rows += rows
	budget.nodes += nodes
	budget.provenance += provenance
	return nil
}
