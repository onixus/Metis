// Сценарий приёмки этапа 3 (ТЗ 6): P&L продукта в двух видах сходится с контрольным расчётом,
// сценарий показывает влияние на обязательства и треки сертификации.
package e2e

import (
	"bytes"
	"context"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/xuri/excelize/v2"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/tests/e2e/client"
)

// financeBook собирает книгу XLSX с контрольными финансовыми данными за январь 2026.
func financeBook(t *testing.T, rows [][]string) []byte {
	t.Helper()
	x := excelize.NewFile()
	sheet := "Финансы"
	if _, err := x.NewSheet(sheet); err != nil {
		t.Fatalf("лист: %v", err)
	}
	header := []string{"Продукт", "Период", "Статья", "revenue", "payroll", "direct_costs", "marketing", "budget"}
	all := append([][]string{header}, rows...)
	for r, row := range all {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("ячейка: %v", err)
			}
			if err := x.SetCellValue(sheet, cell, v); err != nil {
				t.Fatalf("запись ячейки: %v", err)
			}
		}
	}
	var buf bytes.Buffer
	if err := x.Write(&buf); err != nil {
		t.Fatalf("книга: %v", err)
	}
	return buf.Bytes()
}

func TestAcceptanceStage3(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	cpo := e.client("cpo", []string{"cpo"}, nil)
	fin := e.clientFinance("cfo", []string{"finance"}, nil, "aggregates")
	finFull := e.clientFinance("cfo", []string{"finance"}, nil, "full")
	compliance := e.client("compliance", []string{"compliance"}, nil)

	products := must(cpo.ListProductsWithResponse(ctx))
	byKey := map[string]client.Product{}
	for _, p := range *products.JSON200 {
		byKey[p.Key] = p
	}

	// 1. Заведены финансовые поля (EC-08) и шаблон импорта (EC-07).
	fields := []client.FinancialFieldInput{
		{Key: "revenue", Name: "Выручка", Type: "money", Source: "import"},
		{Key: "payroll", Name: "ФОТ", Type: "money", Source: "import"},
		{Key: "direct_costs", Name: "Прямые затраты", Type: "money", Source: "import"},
		{Key: "marketing", Name: "Маркетинг", Type: "money", Source: "import"},
		{Key: "budget", Name: "Бюджет", Type: "money", Source: "import"},
	}
	for _, f := range fields {
		res := must(finFull.SaveFinancialFieldWithResponse(ctx, client.SaveFinancialFieldJSONRequestBody(f)))
		if res.StatusCode() != 200 {
			t.Fatalf("поле %s: %s %s", f.Key, res.Status(), res.Body)
		}
	}
	columns := []client.ImportColumn{}
	for _, f := range fields {
		columns = append(columns, client.ImportColumn{Column: f.Key, FieldKey: f.Key})
	}
	headerRow := 1
	productColumn, periodColumn, itemColumn := "Продукт", "Период", "Статья"
	tpl := must(finFull.SaveImportTemplateWithResponse(ctx, client.SaveImportTemplateJSONRequestBody{
		Name: "Финансы месяца",
		Sheets: []client.ImportSheet{{Sheet: "Финансы", HeaderRow: &headerRow, ProductColumn: &productColumn,
			PeriodColumn: &periodColumn, ItemColumn: &itemColumn, Columns: columns}},
	}))
	if tpl.StatusCode() != 200 {
		t.Fatalf("шаблон: %s %s", tpl.Status(), tpl.Body)
	}

	// 2. Загружены контрольные данные января 2026 (EC-01).
	book := financeBook(t, [][]string{
		{"edr", "2026-01", "", "10000000", "4000000", "1000000", "500000", "2000000"},
		{"vm", "2026-01", "", "5000000", "2000000", "", "", ""},
		{"soar", "2026-01", "", "", "3000000", "", "", ""},
	})
	period := "2026-01"
	fileName := "january.xlsx"
	imp := must(finFull.ImportFinanceFileWithBodyWithResponse(ctx, &client.ImportFinanceFileParams{
		TemplateId: tpl.JSON200.Id, Period: &period, FileName: &fileName},
		"application/octet-stream", bytes.NewReader(book)))
	if imp.StatusCode() != 200 {
		t.Fatalf("импорт: %s %s", imp.Status(), imp.Body)
	}
	if imp.JSON200.Batch.Errors != nil && len(*imp.JSON200.Batch.Errors) > 0 {
		t.Fatalf("ошибки импорта: %+v", *imp.JSON200.Batch.Errors)
	}

	// 3. Правило аллокации затрат хаба SOAR (EC-02).
	rule := must(finFull.SaveAllocationRuleWithResponse(ctx, client.SaveAllocationRuleJSONRequestBody{
		HubProductId: byKey["soar"].Id, Basis: "manual",
		EffectiveFrom: date(2026, time.January, 1),
		Shares:        &map[string]string{byKey["edr"].Id.String(): "0.6", byKey["vm"].Id.String(): "0.4"},
	}))
	if rule.StatusCode() != 201 {
		t.Fatalf("правило аллокации: %s %s", rule.Status(), rule.Body)
	}

	// 4. P&L продукта в двух видах сходится с контрольным расчётом финансов (критерий этапа 3).
	pnl := must(fin.GetProductPnLWithResponse(ctx, byKey["edr"].Id, &client.GetProductPnLParams{Period: &period}))
	if pnl.StatusCode() != 200 {
		t.Fatalf("P&L: %s %s", pnl.Status(), pnl.Body)
	}
	if pnl.JSON200.Revenue.Amount != 10_000_000_00 {
		t.Fatalf("выручка: %d", pnl.JSON200.Revenue.Amount)
	}
	if pnl.JSON200.DirectCosts.Amount != 5_500_000_00 {
		t.Fatalf("прямые затраты: %d", pnl.JSON200.DirectCosts.Amount)
	}
	if pnl.JSON200.HubLoad.Amount != 1_800_000_00 {
		t.Fatalf("нагрузка хаба: %d", pnl.JSON200.HubLoad.Amount)
	}
	if pnl.JSON200.DirectProfit.Amount != 4_500_000_00 {
		t.Fatalf("прибыль прямого вида: %d", pnl.JSON200.DirectProfit.Amount)
	}
	if pnl.JSON200.LoadedProfit.Amount != 2_700_000_00 {
		t.Fatalf("прибыль с нагрузкой хаба: %d", pnl.JSON200.LoadedProfit.Amount)
	}
	portfolio := must(fin.GetPortfolioPnLWithResponse(ctx, &client.GetPortfolioPnLParams{Period: &period}))
	if portfolio.StatusCode() != 200 || portfolio.JSON200.Profit.Amount != 4_500_000_00 {
		t.Fatalf("P&L портфеля: %s %s", portfolio.Status(), portfolio.Body)
	}

	// Финансовые данные закрыты от субъекта без уровня доступа (NF-S02).
	pm := e.client("pm-edr", []string{"pm"}, []string{"edr"})
	denied := must(pm.GetProductPnLWithResponse(ctx, byKey["edr"].Id, &client.GetProductPnLParams{Period: &period}))
	if denied.StatusCode() != 403 {
		t.Fatalf("P&L без финансового доступа: %s", denied.Status())
	}

	// 5. Трек сертификации с затратами гейта и обязательство со сроком внутри окна сдвига.
	release := must(cpo.CreateReleaseWithResponse(ctx, byKey["edr"].Id, client.CreateReleaseJSONRequestBody{
		Name: "EDR 4.0", Version: "4.0", PlannedDate: date(2027, time.January, 31)}))
	if release.StatusCode() != 201 {
		t.Fatalf("релиз: %s %s", release.Status(), release.Body)
	}
	track := must(compliance.StartTrackWithResponse(ctx, byKey["edr"].Id, client.StartTrackJSONRequestBody{
		ReleaseId: release.JSON201.Id, Version: "4.0"}))
	if track.StatusCode() != 201 {
		t.Fatalf("трек: %s %s", track.Status(), track.Body)
	}
	gateID := track.JSON201.Gates[0].Id
	gate := must(compliance.UpdateGateWithResponse(ctx, track.JSON201.Id, gateID, client.UpdateGateJSONRequestBody{
		Cost: &client.Money{Amount: 1_800_000_00, Currency: "RUB"}}))
	if gate.StatusCode() != 200 {
		t.Fatalf("затраты гейта: %s %s", gate.Status(), gate.Body)
	}
	due := kernel.DateFromTime(time.Now().UTC()).AddDays(10)
	commitment := must(compliance.CreateCommitmentWithResponse(ctx, byKey["edr"].Id, client.CreateCommitmentJSONRequestBody{
		Kind: "customer", Counterparty: "Банк", Subject: "Поставить ГОСТ-криптографию",
		Basis: "договор 42", Owner: "pm-edr", DueDate: openapi_types.Date{Time: due.Time()}}))
	if commitment.StatusCode() != 201 {
		t.Fatalf("обязательство: %s %s", commitment.Status(), commitment.Body)
	}

	// 6. Сценарий «что если»: сдвиг поставки на 30 дней и урезание бюджета на 500 000 ₽.
	shift := 30
	scenario := must(finFull.SaveScenarioWithResponse(ctx, client.SaveScenarioJSONRequestBody{
		Name: "Сдвиг поставки и урезание бюджета", Period: period,
		Products:          &[]openapi_types.UUID{byKey["edr"].Id},
		CapacityShiftDays: &shift,
		BudgetDelta:       &client.Money{Amount: -500_000_00, Currency: "RUB"},
	}))
	if scenario.StatusCode() != 200 {
		t.Fatalf("сценарий: %s %s", scenario.Status(), scenario.Body)
	}
	run := must(finFull.RunScenarioWithResponse(ctx, scenario.JSON200.Id, client.RunScenarioJSONRequestBody{}))
	if run.StatusCode() != 200 {
		t.Fatalf("расчёт сценария: %s %s", run.Status(), run.Body)
	}
	breached := 0
	for _, c := range *run.JSON200.Commitments {
		if c.Breached {
			breached++
		}
	}
	if breached != 1 {
		t.Fatalf("сценарий не показал нарушенных обязательств: %+v", *run.JSON200.Commitments)
	}
	// Плановые затраты гейта трека 1 800 000 ₽ против бюджета января 2 000 000 ₽,
	// урезанного сценарием на 500 000 ₽: нехватка 300 000 ₽.
	atRisk := 0
	for _, tr := range *run.JSON200.Tracks {
		if tr.AtRisk {
			atRisk++
			if tr.Shortage == nil || tr.Shortage.Amount != 300_000_00 {
				t.Fatalf("нехватка бюджета трека: %+v", tr.Shortage)
			}
		}
	}
	if atRisk != 1 {
		t.Fatalf("сценарий показал треков под угрозой: %d, ожидался 1", atRisk)
	}
	// Фактические данные сценарий не изменил (EC-13).
	after := must(fin.GetProductPnLWithResponse(ctx, byKey["edr"].Id, &client.GetProductPnLParams{Period: &period}))
	if after.JSON200.LoadedProfit.Amount != 2_700_000_00 {
		t.Fatalf("сценарий изменил фактический P&L: %d", after.JSON200.LoadedProfit.Amount)
	}

	// 7. Маркетинговая аналитика по данным CRM (DA-04).
	marketing := e.client("cmo", []string{"marketing"}, nil)
	winLoss := must(marketing.GetWinLossWithResponse(ctx, &client.GetWinLossParams{}))
	if winLoss.StatusCode() != 200 {
		t.Fatalf("win/loss: %s %s", winLoss.Status(), winLoss.Body)
	}
	if winLoss.JSON200.Won != 1 || winLoss.JSON200.Lost != 1 {
		t.Fatalf("win/loss: выиграно %d, проиграно %d", winLoss.JSON200.Won, winLoss.JSON200.Lost)
	}
}
