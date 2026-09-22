package economics_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"

	"github.com/onixus/metis/internal/adapters/financexlsx"
	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/economics/modeling/formula"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

type memPub struct{ events []kernel.Event }

func (m *memPub) Publish(_ context.Context, evs ...kernel.Event) error {
	m.events = append(m.events, evs...)
	return nil
}

// dirStub — справочник продуктов по ключу.
type dirStub struct{ byKey map[string]kernel.ID }

func (d dirStub) ProductIDByKey(_ context.Context, key string) (kernel.ID, error) {
	id, ok := d.byKey[key]
	if !ok {
		return kernel.NilID, kernel.NotFound("product", kernel.NilID)
	}
	return id, nil
}

type fixture struct {
	t     *testing.T
	ctx   context.Context
	svc   *economics.Service
	pub   *memPub
	clock kernel.FixedClock
	fin   authz.Scope
	hub   kernel.ID
	edr   kernel.ID
	vm    kernel.ID
	dir   dirStub
}

func financeScope() authz.Scope {
	return authz.New(authz.Params{Subject: "cfo", Roles: []authz.Role{authz.RoleFinance},
		AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal, Finance: authz.FinanceFull})
}

func aggregatesScope() authz.Scope {
	return authz.New(authz.Params{Subject: "cpo", Roles: []authz.Role{authz.RoleCPO},
		AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal, Finance: authz.FinanceAggregates})
}

func noFinanceScope() authz.Scope {
	return authz.New(authz.Params{Subject: "pm", Roles: []authz.Role{authz.RolePM},
		AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func jan() economics.Period { return economics.PeriodOf(2026, time.January) }

func dec(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	v, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("число %q: %v", s, err)
	}
	return v
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pub := &memPub{}
	clock := kernel.FixedClock{T: time.Date(2026, 2, 10, 9, 0, 0, 0, time.UTC)}
	f := &fixture{t: t, ctx: context.Background(), pub: pub, clock: clock, fin: financeScope(),
		hub: kernel.NewID(), edr: kernel.NewID(), vm: kernel.NewID()}
	f.dir = dirStub{byKey: map[string]kernel.ID{"platform": f.hub, "edr": f.edr, "vm": f.vm}}
	svc, err := economics.NewService(economics.NewMemStore(), pub, clock, economics.DefaultConfig())
	if err != nil {
		t.Fatalf("сервис: %v", err)
	}
	f.svc = svc.WithImport(financexlsx.New(), f.dir)
	f.declareFields()
	return f
}

func (f *fixture) declareFields() {
	f.t.Helper()
	fields := []economics.FieldInput{
		{Key: "revenue", Name: "Выручка", Type: economics.FieldMoney, Source: economics.SourceImport,
			Dimensions: []formula.Dimension{formula.DimProduct, formula.DimPeriod}},
		{Key: "bundle_revenue", Name: "Выручка бандлов", Type: economics.FieldMoney, Source: economics.SourceImport,
			Dimensions: []formula.Dimension{formula.DimPeriod, formula.DimItem}},
		{Key: "certified_revenue", Name: "Выручка с сертификатом", Type: economics.FieldMoney, Source: economics.SourceImport},
		{Key: "feature_revenue", Name: "Выручка фичи", Type: economics.FieldMoney, Source: economics.SourceImport},
		{Key: "payroll", Name: "ФОТ", Type: economics.FieldMoney, Source: economics.SourceImport,
			Dimensions: []formula.Dimension{formula.DimProduct, formula.DimTeam, formula.DimPeriod}},
		{Key: "direct_costs", Name: "Прямые затраты", Type: economics.FieldMoney, Source: economics.SourceImport},
		{Key: "marketing", Name: "Маркетинг", Type: economics.FieldMoney, Source: economics.SourceImport},
		{Key: "feature_cost", Name: "Затраты на фичу", Type: economics.FieldMoney, Source: economics.SourceImport},
		{Key: "branch_cost", Name: "Поддержка ветки", Type: economics.FieldMoney, Source: economics.SourceImport},
		{Key: "track_cost", Name: "Затраты трека", Type: economics.FieldMoney, Source: economics.SourceImport},
		{Key: "budget", Name: "Бюджет", Type: economics.FieldMoney, Source: economics.SourceImport},
		{Key: "headcount", Name: "Численность", Type: economics.FieldNumber, Source: economics.SourceManual},
	}
	for _, in := range fields {
		if _, err := f.svc.SaveField(f.ctx, f.fin, in); err != nil {
			f.t.Fatalf("поле %s: %v", in.Key, err)
		}
	}
}

// book строит книгу XLSX с листом «Финансы» по строкам {продукт, период, статья, значения полей}.
type bookRow struct {
	product string
	team    string
	period  string
	item    string
	values  map[string]string
}

func (f *fixture) book(columns []string, rows []bookRow) []byte {
	f.t.Helper()
	x := excelize.NewFile()
	sheet := "Финансы"
	if _, err := x.NewSheet(sheet); err != nil {
		f.t.Fatalf("лист: %v", err)
	}
	header := append([]string{"Продукт", "Команда", "Период", "Статья"}, columns...)
	for i, h := range header {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := x.SetCellValue(sheet, cell, h); err != nil {
			f.t.Fatalf("заголовок: %v", err)
		}
	}
	for r, row := range rows {
		vals := append([]string{row.product, row.team, row.period, row.item}, make([]string, len(columns))...)
		for i, c := range columns {
			vals[4+i] = row.values[c]
		}
		for i, v := range vals {
			cell, _ := excelize.CoordinatesToCellName(i+1, r+2)
			if err := x.SetCellValue(sheet, cell, v); err != nil {
				f.t.Fatalf("ячейка: %v", err)
			}
		}
	}
	var buf bytes.Buffer
	if err := x.Write(&buf); err != nil {
		f.t.Fatalf("книга: %v", err)
	}
	return buf.Bytes()
}

func (f *fixture) template(columns []string) economics.Template {
	f.t.Helper()
	cols := make([]economics.ColumnMap, 0, len(columns))
	for _, c := range columns {
		cols = append(cols, economics.ColumnMap{Column: c, FieldKey: c})
	}
	tpl, err := f.svc.SaveTemplate(f.ctx, f.fin, economics.TemplateInput{
		Name: "Финансы месяца",
		Sheets: []economics.SheetMap{{
			Sheet: "Финансы", HeaderRow: 1, ProductColumn: "Продукт", TeamColumn: "Команда",
			PeriodColumn: "Период", ItemColumn: "Статья", Columns: cols,
		}},
	})
	if err != nil {
		f.t.Fatalf("шаблон: %v", err)
	}
	return tpl
}

// loadControlData загружает контрольный набор данных за январь 2026.
func (f *fixture) loadControlData() economics.ImportBatch {
	f.t.Helper()
	columns := []string{"revenue", "payroll", "direct_costs", "marketing"}
	tpl := f.template(columns)
	data := f.book(columns, []bookRow{
		{product: "edr", period: "2026-01", values: map[string]string{
			"revenue": "10000000", "payroll": "4000000", "direct_costs": "1000000", "marketing": "500000"}},
		{product: "vm", period: "2026-01", values: map[string]string{
			"revenue": "5000000", "payroll": "2000000"}},
		{product: "platform", period: "2026-01", values: map[string]string{"payroll": "3000000"}},
	})
	res, err := f.svc.ApplyImport(f.ctx, f.fin, economics.ImportInput{
		TemplateID: tpl.ID, Period: jan(), FileName: "jan.xlsx", Data: data})
	if err != nil {
		f.t.Fatalf("загрузка: %v", err)
	}
	return res.Batch
}

func (f *fixture) rub(t *testing.T, rubles int64) kernel.Money {
	t.Helper()
	return kernel.Money{Amount: rubles * 100, Currency: "RUB"}
}

// TestEC01_ImportFromXLSX: финансовые данные загружаются из книги XLSX.
func TestEC01_ImportFromXLSX(t *testing.T) {
	f := newFixture(t)
	batch := f.loadControlData()
	if batch.Status != economics.BatchApplied {
		t.Fatalf("статус загрузки %q", batch.Status)
	}
	if batch.Rows != 7 {
		t.Fatalf("загружено строк %d, ожидалось 7", batch.Rows)
	}
	if batch.SHA256 == "" {
		t.Fatal("не посчитан хеш файла")
	}
	rows, err := f.svc.Facts(f.ctx, f.fin, economics.FactFilter{FieldKey: "revenue"})
	if err != nil {
		t.Fatalf("строки: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("строк выручки %d, ожидалось 2", len(rows))
	}
	for _, r := range rows {
		if r.ProductID == f.edr && !r.Value.Equal(dec(t, "1000000000")) {
			t.Fatalf("выручка EDR в копейках: %s", r.Value)
		}
	}
}

// TestEC01_ScheduledImportRecorded: загрузка по расписанию отмечается в истории.
func TestEC01_ScheduledImportRecorded(t *testing.T) {
	f := newFixture(t)
	columns := []string{"revenue"}
	tpl := f.template(columns)
	data := f.book(columns, []bookRow{{product: "edr", period: "2026-01", values: map[string]string{"revenue": "100"}}})
	res, err := f.svc.ApplyImport(f.ctx, f.fin, economics.ImportInput{TemplateID: tpl.ID, Period: jan(),
		FileName: "auto.xlsx", Data: data, Scheduled: true})
	if err != nil {
		t.Fatalf("загрузка: %v", err)
	}
	if !res.Batch.Scheduled {
		t.Fatal("загрузка не отмечена как выполненная по расписанию")
	}
}

// TestEC07_PreviewReportsRowErrors: предпросмотр показывает ошибки по строкам и ничего не сохраняет.
func TestEC07_PreviewReportsRowErrors(t *testing.T) {
	f := newFixture(t)
	columns := []string{"revenue"}
	tpl := f.template(columns)
	data := f.book(columns, []bookRow{
		{product: "edr", period: "2026-01", values: map[string]string{"revenue": "100"}},
		{product: "неизвестный", period: "2026-01", values: map[string]string{"revenue": "200"}},
		{product: "vm", period: "2026-01", values: map[string]string{"revenue": "не число"}},
		{product: "vm", period: "январь", values: map[string]string{"revenue": "300"}},
	})
	res, err := f.svc.PreviewImport(f.ctx, f.fin, economics.ImportInput{TemplateID: tpl.ID, Period: jan(),
		FileName: "jan.xlsx", Data: data})
	if err != nil {
		t.Fatalf("предпросмотр: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("к загрузке готово строк %d, ожидалась 1", len(res.Rows))
	}
	if len(res.Batch.Errors) != 3 {
		t.Fatalf("ошибок по строкам %d, ожидалось 3: %+v", len(res.Batch.Errors), res.Batch.Errors)
	}
	facts, err := f.svc.Facts(f.ctx, f.fin, economics.FactFilter{})
	if err != nil {
		t.Fatalf("строки: %v", err)
	}
	if len(facts) != 0 {
		t.Fatalf("предпросмотр сохранил %d строк", len(facts))
	}
	batches, err := f.svc.Batches(f.ctx, f.fin, economics.Period{})
	if err != nil {
		t.Fatalf("история: %v", err)
	}
	if len(batches) != 0 {
		t.Fatalf("предпросмотр записал историю: %d", len(batches))
	}
}

// TestEC07_ReimportCreatesNewDataVersion: повторная загрузка периода создаёт новую версию данных,
// история загрузок сохраняется, действующими становятся значения новой версии.
func TestEC07_ReimportCreatesNewDataVersion(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	columns := []string{"revenue"}
	tpl := f.template(columns)
	data := f.book(columns, []bookRow{{product: "edr", period: "2026-01", values: map[string]string{"revenue": "12000000"}}})
	res, err := f.svc.ApplyImport(f.ctx, f.fin, economics.ImportInput{TemplateID: tpl.ID, Period: jan(),
		FileName: "jan-v2.xlsx", Data: data})
	if err != nil {
		t.Fatalf("повторная загрузка: %v", err)
	}
	if res.Batch.DataVersion != 2 {
		t.Fatalf("версия данных %d, ожидалась 2", res.Batch.DataVersion)
	}
	batches, err := f.svc.Batches(f.ctx, f.fin, jan())
	if err != nil {
		t.Fatalf("история: %v", err)
	}
	if len(batches) != 2 {
		t.Fatalf("в истории %d загрузок, ожидалось 2", len(batches))
	}
	pnl, err := f.svc.ProductPnL(f.ctx, f.fin, f.edr, jan())
	if err != nil {
		t.Fatalf("P&L: %v", err)
	}
	if pnl.Revenue != f.rub(t, 12_000_000) {
		t.Fatalf("действует старая версия данных: %s", pnl.Revenue)
	}
	// Затраты из первой загрузки не потеряны: их полей во второй книге нет.
	if pnl.DirectCosts != f.rub(t, 5_500_000) {
		t.Fatalf("затраты после повторной загрузки: %s", pnl.DirectCosts)
	}
	old := 1
	rows, err := f.svc.Facts(f.ctx, f.fin, economics.FactFilter{FieldKey: "revenue", DataVersion: &old})
	if err != nil {
		t.Fatalf("строки версии 1: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("версия 1 потеряна: строк %d", len(rows))
	}
}

// TestEC03_PnLDirectAndLoaded: P&L продукта в двух видах сходится с контрольным расчётом.
func TestEC03_PnLDirectAndLoaded(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	if _, err := f.svc.SaveAllocationRule(f.ctx, f.fin, economics.AllocationInput{
		HubProductID: f.hub, Basis: economics.BasisManual,
		Shares: map[kernel.ID]decimal.Decimal{f.edr: dec(t, "0.6"), f.vm: dec(t, "0.4")},
	}); err != nil {
		t.Fatalf("правило аллокации: %v", err)
	}
	pnl, err := f.svc.ProductPnL(f.ctx, f.fin, f.edr, jan())
	if err != nil {
		t.Fatalf("P&L: %v", err)
	}
	if pnl.Revenue != f.rub(t, 10_000_000) {
		t.Fatalf("выручка %s", pnl.Revenue)
	}
	if pnl.DirectCosts != f.rub(t, 5_500_000) {
		t.Fatalf("прямые затраты %s", pnl.DirectCosts)
	}
	if pnl.HubLoad != f.rub(t, 1_800_000) {
		t.Fatalf("нагрузка хаба %s", pnl.HubLoad)
	}
	if pnl.DirectProfit != f.rub(t, 4_500_000) {
		t.Fatalf("прибыль прямого вида %s", pnl.DirectProfit)
	}
	if pnl.LoadedProfit != f.rub(t, 2_700_000) {
		t.Fatalf("прибыль с нагрузкой %s", pnl.LoadedProfit)
	}
}

// TestEC03_PortfolioPnL: свод по портфелю собирает продукты периода.
func TestEC03_PortfolioPnL(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	port, err := f.svc.PortfolioPnL(f.ctx, f.fin, jan())
	if err != nil {
		t.Fatalf("портфель: %v", err)
	}
	if len(port.Products) != 3 {
		t.Fatalf("продуктов в своде %d, ожидалось 3", len(port.Products))
	}
	if port.Revenue != f.rub(t, 15_000_000) {
		t.Fatalf("выручка портфеля %s", port.Revenue)
	}
	if port.Costs != f.rub(t, 10_500_000) {
		t.Fatalf("затраты портфеля %s", port.Costs)
	}
	if port.Profit != f.rub(t, 4_500_000) {
		t.Fatalf("прибыль портфеля %s", port.Profit)
	}
}

// TestEC03_ForbiddenWithoutFinanceLevel: без уровня доступа к финансам P&L недоступен (NF-S02).
func TestEC03_ForbiddenWithoutFinanceLevel(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	if _, err := f.svc.ProductPnL(f.ctx, noFinanceScope(), f.edr, jan()); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("ожидался отказ, получено %v", err)
	}
	var zero authz.Scope
	if _, err := f.svc.ProductPnL(f.ctx, zero, f.edr, jan()); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope: ожидался отказ, получено %v", err)
	}
	// Уровень «агрегаты» видит P&L, но не отдельные строки.
	if _, err := f.svc.ProductPnL(f.ctx, aggregatesScope(), f.edr, jan()); err != nil {
		t.Fatalf("уровень агрегатов: %v", err)
	}
	if _, err := f.svc.Facts(f.ctx, aggregatesScope(), economics.FactFilter{}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("строки на уровне агрегатов: ожидался отказ, получено %v", err)
	}
}

// TestEC02_AllocationRuleVersioned: новая версия правила действует с указанной даты,
// прежние версии не изменяются.
func TestEC02_AllocationRuleVersioned(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	if _, err := f.svc.SaveAllocationRule(f.ctx, f.fin, economics.AllocationInput{
		HubProductID: f.hub, Basis: economics.BasisManual, EffectiveFrom: kernel.DateOf(2026, time.January, 1),
		Shares: map[kernel.ID]decimal.Decimal{f.edr: dec(t, "0.6"), f.vm: dec(t, "0.4")},
	}); err != nil {
		t.Fatalf("версия 1: %v", err)
	}
	v2, err := f.svc.SaveAllocationRule(f.ctx, f.fin, economics.AllocationInput{
		HubProductID: f.hub, Basis: economics.BasisRevenue, EffectiveFrom: kernel.DateOf(2026, time.February, 1),
	})
	if err != nil {
		t.Fatalf("версия 2: %v", err)
	}
	if v2.Version != 2 {
		t.Fatalf("версия правила %d", v2.Version)
	}
	// Январь считается по версии 1 (ручные доли).
	pnl, err := f.svc.ProductPnL(f.ctx, f.fin, f.edr, jan())
	if err != nil {
		t.Fatalf("P&L: %v", err)
	}
	if pnl.HubLoad != f.rub(t, 1_800_000) {
		t.Fatalf("январь посчитан не по версии 1: %s", pnl.HubLoad)
	}
	rules, err := f.svc.AllocationRules(f.ctx, f.fin)
	if err != nil {
		t.Fatalf("правила: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("правил %d, ожидалось 2", len(rules))
	}
}

// TestEC02_RevenueBasisSplitsByRevenue: база «выручка» распределяет затраты хаба пропорционально выручке.
func TestEC02_RevenueBasisSplitsByRevenue(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	if _, err := f.svc.SaveAllocationRule(f.ctx, f.fin, economics.AllocationInput{
		HubProductID: f.hub, Basis: economics.BasisRevenue, Consumers: []kernel.ID{f.edr, f.vm},
	}); err != nil {
		t.Fatalf("правило: %v", err)
	}
	pnl, err := f.svc.ProductPnL(f.ctx, f.fin, f.edr, jan())
	if err != nil {
		t.Fatalf("P&L: %v", err)
	}
	// 10 из 15 млн выручки → две трети затрат хаба (3 млн) = 2 млн.
	if pnl.HubLoad != f.rub(t, 2_000_000) {
		t.Fatalf("нагрузка хаба %s, ожидалось 2 000 000 ₽", pnl.HubLoad)
	}
}

// TestEC02_ManualSharesMustSumToOne: сумма ручных долей проверяется.
func TestEC02_ManualSharesMustSumToOne(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.SaveAllocationRule(f.ctx, f.fin, economics.AllocationInput{
		HubProductID: f.hub, Basis: economics.BasisManual,
		Shares: map[kernel.ID]decimal.Decimal{f.edr: dec(t, "0.6")},
	})
	if !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("ожидалась ошибка валидации, получено %v", err)
	}
}

// TestEC04_BundleRevenueAttributed: выручка бандла делится между продуктами по правилу.
func TestEC04_BundleRevenueAttributed(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	columns := []string{"bundle_revenue"}
	tpl := f.template(columns)
	data := f.book(columns, []bookRow{{period: "2026-01", item: "security-suite",
		values: map[string]string{"bundle_revenue": "3000000"}}})
	if _, err := f.svc.ApplyImport(f.ctx, f.fin, economics.ImportInput{TemplateID: tpl.ID, Period: jan(),
		FileName: "bundle.xlsx", Data: data}); err != nil {
		t.Fatalf("загрузка бандла: %v", err)
	}
	if _, err := f.svc.SaveBundleRule(f.ctx, f.fin, economics.BundleInput{BundleKey: "security-suite",
		Shares: map[kernel.ID]decimal.Decimal{f.edr: dec(t, "0.7"), f.vm: dec(t, "0.3")}}); err != nil {
		t.Fatalf("правило бандла: %v", err)
	}
	pnl, err := f.svc.ProductPnL(f.ctx, f.fin, f.edr, jan())
	if err != nil {
		t.Fatalf("P&L: %v", err)
	}
	if pnl.BundleRevenue != f.rub(t, 2_100_000) {
		t.Fatalf("выручка бандла %s, ожидалось 2 100 000 ₽", pnl.BundleRevenue)
	}
	if pnl.DirectProfit != f.rub(t, 6_600_000) {
		t.Fatalf("прибыль с учётом бандла %s", pnl.DirectProfit)
	}
}

// TestEC05_FeatureInvestmentVsRevenue: инвестиции в фичу сравниваются с привязанной выручкой.
func TestEC05_FeatureInvestmentVsRevenue(t *testing.T) {
	f := newFixture(t)
	feature := kernel.NewID()
	columns := []string{"feature_cost", "feature_revenue"}
	tpl := f.template(columns)
	data := f.book(columns, []bookRow{{product: "edr", period: "2026-01", item: economics.FeatureItem(feature),
		values: map[string]string{"feature_cost": "800000", "feature_revenue": "500000"}}})
	if _, err := f.svc.ApplyImport(f.ctx, f.fin, economics.ImportInput{TemplateID: tpl.ID, Period: jan(),
		FileName: "feature.xlsx", Data: data}); err != nil {
		t.Fatalf("загрузка: %v", err)
	}
	fe, err := f.svc.FeatureEconomics(f.ctx, f.fin, f.edr, feature, jan())
	if err != nil {
		t.Fatalf("экономика фичи: %v", err)
	}
	if fe.Investment != f.rub(t, 800_000) || fe.Revenue != f.rub(t, 500_000) {
		t.Fatalf("инвестиции %s, выручка %s", fe.Investment, fe.Revenue)
	}
	if fe.Balance != (kernel.Money{Amount: -30_000_000, Currency: "RUB"}) {
		t.Fatalf("баланс %s", fe.Balance)
	}
}

// TestEC05_BranchSupportCost: стоимость поддержки старых веток считается по веткам.
func TestEC05_BranchSupportCost(t *testing.T) {
	f := newFixture(t)
	columns := []string{"branch_cost"}
	tpl := f.template(columns)
	data := f.book(columns, []bookRow{
		{product: "edr", period: "2026-01", item: economics.BranchItem("3.1-certified"),
			values: map[string]string{"branch_cost": "700000"}},
		{product: "edr", period: "2026-01", item: economics.BranchItem("4.0"),
			values: map[string]string{"branch_cost": "300000"}},
	})
	if _, err := f.svc.ApplyImport(f.ctx, f.fin, economics.ImportInput{TemplateID: tpl.ID, Period: jan(),
		FileName: "branches.xlsx", Data: data}); err != nil {
		t.Fatalf("загрузка: %v", err)
	}
	costs, err := f.svc.BranchCosts(f.ctx, f.fin, f.edr, jan())
	if err != nil {
		t.Fatalf("ветки: %v", err)
	}
	if len(costs) != 2 || costs[0].Branch != "3.1-certified" || costs[0].Cost != f.rub(t, 700_000) {
		t.Fatalf("ветки: %+v", costs)
	}
}

// trackCostStub — затраты трека сертификации из модуля compliance.
type trackCostStub struct{ cost kernel.Money }

func (s trackCostStub) TrackCost(_ context.Context, _ authz.Scope, _ kernel.ID) (kernel.Money, error) {
	return s.cost, nil
}

// TestEC06_CertificationEconomics: затраты трека сравниваются с выручкой, доступной только с сертификатом.
func TestEC06_CertificationEconomics(t *testing.T) {
	f := newFixture(t)
	columns := []string{"certified_revenue"}
	tpl := f.template(columns)
	data := f.book(columns, []bookRow{{product: "edr", period: "2026-01",
		values: map[string]string{"certified_revenue": "9000000"}}})
	if _, err := f.svc.ApplyImport(f.ctx, f.fin, economics.ImportInput{TemplateID: tpl.ID, Period: jan(),
		FileName: "cert.xlsx", Data: data}); err != nil {
		t.Fatalf("загрузка: %v", err)
	}
	svc := f.svc.WithTrackCosts(trackCostStub{cost: kernel.RUB(400_000_00)})
	ce, err := svc.CertificationEconomics(f.ctx, f.fin, f.edr, jan())
	if err != nil {
		t.Fatalf("экономика сертификации: %v", err)
	}
	if ce.TrackCost != f.rub(t, 400_000) {
		t.Fatalf("затраты трека %s", ce.TrackCost)
	}
	if ce.Balance != f.rub(t, 8_600_000) {
		t.Fatalf("баланс %s", ce.Balance)
	}
}

// TestEC08_CustomFieldVersioned: описание поля версионируется с датой действия.
func TestEC08_CustomFieldVersioned(t *testing.T) {
	f := newFixture(t)
	fld, err := f.svc.SaveField(f.ctx, f.fin, economics.FieldInput{Key: "revenue", Name: "Выручка, нетто",
		Type: economics.FieldMoney, Source: economics.SourceImport, EffectiveFrom: kernel.DateOf(2026, time.March, 1)})
	if err != nil {
		t.Fatalf("новая версия поля: %v", err)
	}
	if len(fld.Versions) != 2 {
		t.Fatalf("версий поля %d", len(fld.Versions))
	}
	v, ok := fld.At(kernel.DateOf(2026, time.January, 31))
	if !ok || v.Name != "Выручка" {
		t.Fatalf("на январь действует версия %+v", v)
	}
	if _, err := f.svc.SaveField(f.ctx, f.fin, economics.FieldInput{Key: "Выручка!", Name: "x",
		Type: economics.FieldMoney, Source: economics.SourceImport}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("недопустимый ключ принят: %v", err)
	}
}

// TestEC08_ManualFieldValue: поле ручного ввода принимает значение, импортное — нет.
func TestEC08_ManualFieldValue(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.SetManualValue(f.ctx, f.fin, "headcount",
		economics.Slice{ProductID: f.edr, Period: jan()}, dec(t, "42")); err != nil {
		t.Fatalf("ручной ввод: %v", err)
	}
	if _, err := f.svc.SetManualValue(f.ctx, f.fin, "revenue",
		economics.Slice{ProductID: f.edr, Period: jan()}, dec(t, "1")); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("импортное поле приняло ручной ввод: %v", err)
	}
}

// TestEC09_MetricUsesPlatformData: формула показателя считает по данным платформы.
func TestEC09_MetricUsesPlatformData(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	if _, err := f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: "gross_profit", Name: "Валовая прибыль",
		Expression: `field("revenue") - field("payroll") - field("direct_costs") - field("marketing")`}); err != nil {
		t.Fatalf("показатель: %v", err)
	}
	if _, err := f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: "margin", Name: "Маржа",
		Expression: `metric("gross_profit") / field("revenue") * 100`}); err != nil {
		t.Fatalf("показатель маржи: %v", err)
	}
	v, err := f.svc.Value(f.ctx, f.fin, "margin", economics.Slice{ProductID: f.edr, Period: jan()})
	if err != nil {
		t.Fatalf("значение: %v", err)
	}
	if !v.Round(4).Equal(dec(t, "45")) {
		t.Fatalf("маржа %s, ожидалось 45", v)
	}
}

// TestEC10_RejectsFormulaCycle: формула, создающая цикл, не сохраняется; путь цикла показан.
func TestEC10_RejectsFormulaCycle(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: "a", Name: "A",
		Expression: `field("revenue")`}); err != nil {
		t.Fatalf("a: %v", err)
	}
	if _, err := f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: "b", Name: "B",
		Expression: `metric("a") + d("1")`}); err != nil {
		t.Fatalf("b: %v", err)
	}
	_, err := f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: "a", Name: "A",
		Expression: `metric("b") + d("1")`})
	if !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("цикл принят: %v", err)
	}
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("a → b → a")) {
		t.Fatalf("путь цикла не показан: %v", err)
	}
}

// TestEC10_AffectedMetricsRecalculated: пересчитываются только затронутые показатели.
func TestEC10_AffectedMetricsRecalculated(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	for _, m := range []economics.MetricInput{
		{Key: "gross_profit", Name: "Валовая прибыль", Expression: `field("revenue") - field("payroll")`},
		{Key: "margin", Name: "Маржа", Expression: `metric("gross_profit") / field("revenue")`},
		{Key: "marketing_share", Name: "Доля маркетинга", Expression: `field("marketing") / field("revenue")`},
	} {
		if _, err := f.svc.SaveMetric(f.ctx, f.fin, m); err != nil {
			t.Fatalf("%s: %v", m.Key, err)
		}
	}
	affected, err := f.svc.Affected(f.ctx, formula.Ref{Kind: formula.RefField, Key: "payroll"})
	if err != nil {
		t.Fatalf("затронутые: %v", err)
	}
	if len(affected) != 2 || affected[0] != "gross_profit" || affected[1] != "margin" {
		t.Fatalf("затронутые показатели %v", affected)
	}
	values, err := f.svc.Recalculate(f.ctx, f.fin, formula.Ref{Kind: formula.RefField, Key: "payroll"},
		economics.Slice{ProductID: f.edr, Period: jan()}, false)
	if err != nil {
		t.Fatalf("пересчёт: %v", err)
	}
	if len(values) != 2 {
		t.Fatalf("пересчитано показателей %d", len(values))
	}
}

// TestEC10_ExplainDownToImportRows: значение раскрывается до формулы и строк импорта.
func TestEC10_ExplainDownToImportRows(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	if _, err := f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: "gross_profit", Name: "Валовая прибыль",
		Expression: `field("revenue") - field("payroll")`}); err != nil {
		t.Fatalf("показатель: %v", err)
	}
	ex, err := f.svc.Explain(f.ctx, f.fin, "gross_profit", economics.Slice{ProductID: f.edr, Period: jan()})
	if err != nil {
		t.Fatalf("объяснение: %v", err)
	}
	if ex.Expression == "" || len(ex.Inputs) != 2 {
		t.Fatalf("объяснение: %+v", ex)
	}
	rows := 0
	for _, in := range ex.Inputs {
		rows += len(in.Rows)
		for _, r := range in.Rows {
			if r.Sheet == "" || r.Row == 0 {
				t.Fatalf("строка без координат импорта: %+v", r)
			}
		}
	}
	if rows != 2 {
		t.Fatalf("исходных строк %d, ожидалось 2", rows)
	}
	if _, err := f.svc.Explain(f.ctx, aggregatesScope(), "gross_profit",
		economics.Slice{ProductID: f.edr, Period: jan()}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("объяснение на уровне агрегатов: ожидался отказ, получено %v", err)
	}
}

// TestEC11_ClosedPeriodNeedsExplicitRecalc: закрытый период пересчитывается только явным действием.
func TestEC11_ClosedPeriodNeedsExplicitRecalc(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	if _, err := f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: "gross_profit", Name: "Валовая прибыль",
		Expression: `field("revenue") - field("payroll")`}); err != nil {
		t.Fatalf("показатель: %v", err)
	}
	if err := f.svc.ClosePeriod(f.ctx, f.fin, jan()); err != nil {
		t.Fatalf("закрытие периода: %v", err)
	}
	ref := formula.Ref{Kind: formula.RefField, Key: "payroll"}
	sl := economics.Slice{ProductID: f.edr, Period: jan()}
	if _, err := f.svc.Recalculate(f.ctx, f.fin, ref, sl, false); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("закрытый период пересчитан без явного действия: %v", err)
	}
	if _, err := f.svc.Recalculate(f.ctx, f.fin, ref, sl, true); err != nil {
		t.Fatalf("явный пересчёт: %v", err)
	}
	columns := []string{"revenue"}
	tpl := f.template(columns)
	data := f.book(columns, []bookRow{{product: "edr", period: "2026-01", values: map[string]string{"revenue": "1"}}})
	if _, err := f.svc.ApplyImport(f.ctx, f.fin, economics.ImportInput{TemplateID: tpl.ID, Period: jan(),
		FileName: "late.xlsx", Data: data}); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("загрузка в закрытый период: %v", err)
	}
}

// TestEC11_CompareFormulaVersions: доступно сравнение результатов двух версий формулы.
func TestEC11_CompareFormulaVersions(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	if _, err := f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: "gross_profit", Name: "Валовая прибыль",
		Expression: `field("revenue") - field("payroll")`, EffectiveFrom: kernel.DateOf(2026, time.January, 1)}); err != nil {
		t.Fatalf("версия 1: %v", err)
	}
	if _, err := f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: "gross_profit", Name: "Валовая прибыль",
		Expression:    `field("revenue") - field("payroll") - field("marketing")`,
		EffectiveFrom: kernel.DateOf(2026, time.February, 1)}); err != nil {
		t.Fatalf("версия 2: %v", err)
	}
	a, b, err := f.svc.CompareVersions(f.ctx, f.fin, "gross_profit",
		economics.Slice{ProductID: f.edr, Period: jan()}, 1, 2)
	if err != nil {
		t.Fatalf("сравнение: %v", err)
	}
	if !a.Equal(dec(t, "600000000")) || !b.Equal(dec(t, "550000000")) {
		t.Fatalf("версии дали %s и %s", a, b)
	}
	// На январь действует первая версия формулы.
	v, err := f.svc.Value(f.ctx, f.fin, "gross_profit", economics.Slice{ProductID: f.edr, Period: jan()})
	if err != nil {
		t.Fatalf("значение: %v", err)
	}
	if !v.Equal(a) {
		t.Fatalf("на январь применена не та версия: %s", v)
	}
}

// TestEC12_TeamProductMatrix: затраты команды раскладываются по продуктам, матрица собирается.
func TestEC12_TeamProductMatrix(t *testing.T) {
	f := newFixture(t)
	team, err := f.svc.SaveTeam(f.ctx, f.fin, "core", "Ядро")
	if err != nil {
		t.Fatalf("команда: %v", err)
	}
	columns := []string{"payroll"}
	tpl := f.template(columns)
	data := f.book(columns, []bookRow{{team: "core", period: "2026-01", values: map[string]string{"payroll": "3000000"}}})
	if _, err := f.svc.ApplyImport(f.ctx, f.fin, economics.ImportInput{TemplateID: tpl.ID, Period: jan(),
		FileName: "team.xlsx", Data: data}); err != nil {
		t.Fatalf("загрузка: %v", err)
	}
	if err := f.svc.SetTeamShares(f.ctx, f.fin, team.ID, jan(),
		map[kernel.ID]decimal.Decimal{f.edr: dec(t, "0.75"), f.vm: dec(t, "0.25")}); err != nil {
		t.Fatalf("доли: %v", err)
	}
	cost, err := f.svc.TeamCosts(f.ctx, f.fin, team.ID, jan())
	if err != nil {
		t.Fatalf("затраты команды: %v", err)
	}
	if cost.Total != f.rub(t, 3_000_000) {
		t.Fatalf("затраты команды %s", cost.Total)
	}
	if cost.ByProduct[f.edr] != f.rub(t, 2_250_000) {
		t.Fatalf("доля EDR %s", cost.ByProduct[f.edr])
	}
	m, err := f.svc.Matrix(f.ctx, f.fin, jan())
	if err != nil {
		t.Fatalf("матрица: %v", err)
	}
	if len(m.Teams) != 1 || len(m.Products) != 2 {
		t.Fatalf("матрица %dx%d", len(m.Teams), len(m.Products))
	}
	if m.Cells[team.ID][f.vm] != f.rub(t, 750_000) {
		t.Fatalf("ячейка матрицы %s", m.Cells[team.ID][f.vm])
	}
}

// commitmentsStub — источник обязательств для сценариев.
type commitmentsStub struct{ items []economics.CommitmentRef }

func (c commitmentsStub) Due(_ context.Context, _ authz.Scope, _ []kernel.ID) ([]economics.CommitmentRef, error) {
	return c.items, nil
}

// tracksStub — источник треков сертификации для сценариев.
type tracksStub struct{ items []economics.TrackRef }

func (t tracksStub) Active(_ context.Context, _ authz.Scope, _ []kernel.ID) ([]economics.TrackRef, error) {
	return t.items, nil
}

// TestEC13_ScenarioDoesNotChangeFacts: сценарий считается тем же движком формул
// с подменой входных значений; фактические данные не меняются.
func TestEC13_ScenarioDoesNotChangeFacts(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	if _, err := f.svc.SaveMetric(f.ctx, f.fin, economics.MetricInput{Key: "gross_profit", Name: "Валовая прибыль",
		Expression: `field("revenue") - field("payroll")`}); err != nil {
		t.Fatalf("показатель: %v", err)
	}
	sn, err := f.svc.SaveScenario(f.ctx, f.fin, economics.Scenario{
		Name: "Выручка EDR падает на 20 %", Period: jan(), Products: []kernel.ID{f.edr},
		Overrides: []economics.Override{{FieldKey: "revenue", ProductID: f.edr, Period: jan(),
			Value: dec(t, "800000000")}},
	})
	if err != nil {
		t.Fatalf("сценарий: %v", err)
	}
	res, err := f.svc.RunScenario(f.ctx, f.fin, sn.ID, []string{"gross_profit"})
	if err != nil {
		t.Fatalf("расчёт сценария: %v", err)
	}
	key := "gross_profit@" + f.edr.String()
	if res.BaseMetrics[key] != "600000000" || res.Metrics[key] != "400000000" {
		t.Fatalf("база %q, сценарий %q", res.BaseMetrics[key], res.Metrics[key])
	}
	if len(res.PnL) != 1 || res.PnL[0].Revenue != f.rub(t, 8_000_000) {
		t.Fatalf("P&L сценария: %+v", res.PnL)
	}
	if res.BasePnL[0].Revenue != f.rub(t, 10_000_000) {
		t.Fatalf("базовый P&L изменился: %+v", res.BasePnL)
	}
	// Факты остались прежними.
	pnl, err := f.svc.ProductPnL(f.ctx, f.fin, f.edr, jan())
	if err != nil {
		t.Fatalf("P&L: %v", err)
	}
	if pnl.Revenue != f.rub(t, 10_000_000) {
		t.Fatalf("сценарий изменил фактические данные: %s", pnl.Revenue)
	}
}

// TestDA02_ScenarioShowsCommitmentAndTrackImpact: сценарий показывает влияние
// на обязательства и треки сертификации.
func TestDA02_ScenarioShowsCommitmentAndTrackImpact(t *testing.T) {
	f := newFixture(t)
	f.loadControlData()
	columns := []string{"budget"}
	tpl := f.template(columns)
	data := f.book(columns, []bookRow{{product: "edr", period: "2026-01", values: map[string]string{"budget": "2000000"}}})
	if _, err := f.svc.ApplyImport(f.ctx, f.fin, economics.ImportInput{TemplateID: tpl.ID, Period: jan(),
		FileName: "budget.xlsx", Data: data}); err != nil {
		t.Fatalf("загрузка бюджета: %v", err)
	}
	soon := kernel.DateOf(2026, time.March, 1) // в окне сдвига 30 дней от 10 февраля
	later := kernel.DateOf(2026, time.June, 1) // вне окна
	svc := f.svc.
		WithCommitments(commitmentsStub{items: []economics.CommitmentRef{
			{ID: kernel.NewID(), ProductID: f.edr, Title: "Поставка ГОСТ-криптографии", DueDate: soon, Regulatory: false},
			{ID: kernel.NewID(), ProductID: f.edr, Title: "Сертификат 4.0", DueDate: later, Regulatory: true},
		}}).
		WithTracks(tracksStub{items: []economics.TrackRef{
			{ID: kernel.NewID(), ProductID: f.edr, Name: "Сертификация 4.0",
				PlannedCost: kernel.RUB(1_800_000_00), Deadline: kernel.DateOf(2027, time.January, 1)},
		}})
	sn, err := svc.SaveScenario(f.ctx, f.fin, economics.Scenario{
		Name: "Урезали бюджет и сдвинули поставку", Period: jan(), Products: []kernel.ID{f.edr},
		CapacityShiftDays: 30, BudgetDelta: kernel.Money{Amount: -50_000_000, Currency: "RUB"},
	})
	if err != nil {
		t.Fatalf("сценарий: %v", err)
	}
	res, err := svc.RunScenario(f.ctx, f.fin, sn.ID, nil)
	if err != nil {
		t.Fatalf("расчёт сценария: %v", err)
	}
	breached := 0
	for _, c := range res.Commitments {
		if c.Breached {
			breached++
			if c.Reason == "" {
				t.Fatal("нарушенное обязательство без причины")
			}
		}
	}
	if breached != 1 {
		t.Fatalf("нарушено обязательств %d, ожидалось 1: %+v", breached, res.Commitments)
	}
	if len(res.Tracks) != 1 || !res.Tracks[0].AtRisk {
		t.Fatalf("влияние на треки: %+v", res.Tracks)
	}
	// Бюджет 2 000 000 ₽ минус 500 000 ₽ против плановых затрат трека 1 800 000 ₽.
	if res.Tracks[0].Shortage != f.rub(t, 300_000) {
		t.Fatalf("нехватка бюджета %s, ожидалось 300 000 ₽", res.Tracks[0].Shortage)
	}
}

// TestDL05_WorklogSharesFeedCostAllocation: доли из worklogs становятся базой распределения затрат.
func TestDL05_WorklogSharesFeedCostAllocation(t *testing.T) {
	f := newFixture(t)
	team, err := f.svc.SaveTeam(f.ctx, f.fin, "core", "Ядро")
	if err != nil {
		t.Fatalf("команда: %v", err)
	}
	columns := []string{"payroll"}
	tpl := f.template(columns)
	data := f.book(columns, []bookRow{{team: "core", period: "2026-01", values: map[string]string{"payroll": "4000000"}}})
	if _, err := f.svc.ApplyImport(f.ctx, f.fin, economics.ImportInput{TemplateID: tpl.ID, Period: jan(),
		FileName: "team.xlsx", Data: data}); err != nil {
		t.Fatalf("загрузка: %v", err)
	}
	// Доли пришли из списаний времени трекера: 80 % SOAR, 20 % EDR.
	if err := f.svc.SetWorklogShares(f.ctx, f.fin, team.ID, jan(),
		map[kernel.ID]decimal.Decimal{f.edr: dec(t, "0.2"), f.vm: dec(t, "0.8")}); err != nil {
		t.Fatalf("доли из worklogs: %v", err)
	}
	shares, err := f.svc.TeamShares(f.ctx, f.fin, jan())
	if err != nil {
		t.Fatalf("доли: %v", err)
	}
	for _, sh := range shares {
		if sh.Source != "worklogs" {
			t.Fatalf("источник доли %q", sh.Source)
		}
	}
	cost, err := f.svc.TeamCosts(f.ctx, f.fin, team.ID, jan())
	if err != nil {
		t.Fatalf("затраты команды: %v", err)
	}
	if cost.ByProduct[f.vm] != f.rub(t, 3_200_000) {
		t.Fatalf("затраты на VM %s", cost.ByProduct[f.vm])
	}
}

// fileSourceStub — источник файлов загрузки по расписанию.
type fileSourceStub struct{ files []economics.ImportFile }

func (f fileSourceStub) Pending(context.Context) ([]economics.ImportFile, error) { return f.files, nil }

// TestEC01_ScheduledImportSkipsAlreadyLoadedFiles: загрузка по расписанию не повторяет
// уже загруженные файлы и отмечает загрузки как выполненные по расписанию.
func TestEC01_ScheduledImportSkipsAlreadyLoadedFiles(t *testing.T) {
	f := newFixture(t)
	columns := []string{"revenue"}
	tpl := f.template(columns)
	jan := f.book(columns, []bookRow{{product: "edr", period: "2026-01", values: map[string]string{"revenue": "100"}}})
	feb := f.book(columns, []bookRow{{product: "edr", period: "2026-02", values: map[string]string{"revenue": "200"}}})
	src := fileSourceStub{files: []economics.ImportFile{{Name: "jan.xlsx", Data: jan}, {Name: "feb.xlsx", Data: feb}}}

	batches, err := f.svc.RunScheduledImports(f.ctx, f.fin, tpl.ID, src)
	if err != nil {
		t.Fatalf("загрузка по расписанию: %v", err)
	}
	if len(batches) != 2 {
		t.Fatalf("загружено файлов %d, ожидалось 2", len(batches))
	}
	for _, b := range batches {
		if !b.Scheduled {
			t.Fatalf("загрузка %s не отмечена как выполненная по расписанию", b.FileName)
		}
	}
	again, err := f.svc.RunScheduledImports(f.ctx, f.fin, tpl.ID, src)
	if err != nil {
		t.Fatalf("повторный запуск: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("повторный запуск загрузил файлы снова: %d", len(again))
	}
	all, err := f.svc.Batches(f.ctx, f.fin, economics.Period{})
	if err != nil {
		t.Fatalf("история: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("в истории %d загрузок", len(all))
	}
}
