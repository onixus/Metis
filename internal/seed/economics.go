package seed

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/economics/modeling/formula"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// Stage3Result — то, что завёл seed этапа 3.
type Stage3Result struct {
	TemplateID kernel.ID
	Fields     []string
	Metrics    []string
}

// Economics заводит финансовый контур референсных портфелей (этап 3): поля, шаблон импорта,
// показатели и правило аллокации затрат хаба. Сами финансовые данные приходят импортом XLSX:
// синтетических выгрузок в репозитории нет (правило «реальные данные не использовать»).
// Идемпотентно: повторный вызов не создаёт дубликатов.
func Economics(ctx context.Context, svc *economics.Service, sc authz.Scope, hubProductID kernel.ID, shares map[kernel.ID]string) (Stage3Result, error) {
	res := Stage3Result{}
	fields := []economics.FieldInput{
		{Key: "revenue", Name: "Выручка", Type: economics.FieldMoney, Source: economics.SourceImport,
			Dimensions: []formula.Dimension{formula.DimProduct, formula.DimPeriod}},
		{Key: "bundle_revenue", Name: "Выручка бандлов", Type: economics.FieldMoney, Source: economics.SourceImport,
			Dimensions: []formula.Dimension{formula.DimPeriod, formula.DimItem}},
		{Key: "certified_revenue", Name: "Выручка, доступная только с сертификатом", Type: economics.FieldMoney, Source: economics.SourceImport,
			Dimensions: []formula.Dimension{formula.DimProduct, formula.DimPeriod}},
		{Key: "payroll", Name: "ФОТ", Type: economics.FieldMoney, Source: economics.SourceImport,
			Dimensions: []formula.Dimension{formula.DimProduct, formula.DimTeam, formula.DimPeriod}},
		{Key: "direct_costs", Name: "Прямые затраты", Type: economics.FieldMoney, Source: economics.SourceImport,
			Dimensions: []formula.Dimension{formula.DimProduct, formula.DimPeriod}},
		{Key: "marketing", Name: "Маркетинговый бюджет", Type: economics.FieldMoney, Source: economics.SourceImport,
			Dimensions: []formula.Dimension{formula.DimProduct, formula.DimPeriod}},
		{Key: "budget", Name: "Бюджет продукта", Type: economics.FieldMoney, Source: economics.SourceImport,
			Dimensions: []formula.Dimension{formula.DimProduct, formula.DimPeriod}},
		{Key: "headcount", Name: "Численность", Type: economics.FieldNumber, Source: economics.SourceManual,
			Dimensions: []formula.Dimension{formula.DimProduct, formula.DimTeam, formula.DimPeriod}},
	}
	existing, err := svc.Fields(ctx, sc)
	if err != nil {
		return res, fmt.Errorf("seed economics: %w", err)
	}
	known := map[string]bool{}
	for _, f := range existing {
		known[f.Key] = true
	}
	for _, in := range fields {
		res.Fields = append(res.Fields, in.Key)
		if known[in.Key] {
			continue
		}
		if _, err := svc.SaveField(ctx, sc, in); err != nil {
			return res, fmt.Errorf("seed поле %s: %w", in.Key, err)
		}
	}

	metrics := []economics.MetricInput{
		{Key: "gross_profit", Name: "Валовая прибыль",
			Expression: `field("revenue") - field("payroll") - field("direct_costs") - field("marketing")`},
		{Key: "margin", Name: "Маржа, %", Expression: `metric("gross_profit") / field("revenue") * 100`},
	}
	knownMetrics, err := svc.Metrics(ctx, sc)
	if err != nil {
		return res, fmt.Errorf("seed economics: %w", err)
	}
	haveMetric := map[string]bool{}
	for _, m := range knownMetrics {
		haveMetric[m.Key] = true
	}
	for _, in := range metrics {
		res.Metrics = append(res.Metrics, in.Key)
		if haveMetric[in.Key] {
			continue
		}
		if _, err := svc.SaveMetric(ctx, sc, in); err != nil {
			return res, fmt.Errorf("seed показатель %s: %w", in.Key, err)
		}
	}

	templates, err := svc.Templates(ctx, sc)
	if err != nil {
		return res, fmt.Errorf("seed economics: %w", err)
	}
	const templateName = "Финансы месяца"
	for _, t := range templates {
		if t.Name == templateName {
			res.TemplateID = t.ID
		}
	}
	if res.TemplateID == kernel.NilID {
		columns := []economics.ColumnMap{}
		for _, key := range []string{"revenue", "payroll", "direct_costs", "marketing", "budget", "certified_revenue"} {
			columns = append(columns, economics.ColumnMap{Column: key, FieldKey: key})
		}
		t, err := svc.SaveTemplate(ctx, sc, economics.TemplateInput{Name: templateName, Sheets: []economics.SheetMap{{
			Sheet: "Финансы", HeaderRow: 1, ProductColumn: "Продукт", TeamColumn: "Команда",
			PeriodColumn: "Период", ItemColumn: "Статья", Columns: columns,
		}}})
		if err != nil {
			return res, fmt.Errorf("seed шаблон импорта: %w", err)
		}
		res.TemplateID = t.ID
	}

	if hubProductID == kernel.NilID || len(shares) == 0 {
		return res, nil
	}
	rules, err := svc.AllocationRules(ctx, sc)
	if err != nil {
		return res, fmt.Errorf("seed economics: %w", err)
	}
	for _, r := range rules {
		if r.HubProductID == hubProductID {
			return res, nil
		}
	}
	parsed, err := parseShares(shares)
	if err != nil {
		return res, err
	}
	if _, err := svc.SaveAllocationRule(ctx, sc, economics.AllocationInput{
		HubProductID: hubProductID, Basis: economics.BasisManual, Shares: parsed}); err != nil {
		return res, fmt.Errorf("seed правило аллокации: %w", err)
	}
	return res, nil
}

func parseShares(in map[kernel.ID]string) (map[kernel.ID]decimal.Decimal, error) {
	out := make(map[kernel.ID]decimal.Decimal, len(in))
	for id, raw := range in {
		v, err := decimal.NewFromString(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: доля продукта %s: %q", kernel.ErrValidation, id, raw)
		}
		out[id] = v
	}
	return out, nil
}
