package e2e

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onixus/metis/internal/app"
	"github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
	"github.com/onixus/metis/tests/e2e/client"
)

func financeClient(t *testing.T, base string, roles []string, level string) *client.ClientWithResponses {
	t.Helper()
	tok, err := identityaccess.MintHS256([]byte(secret), issuer, "synthetic-finance", roles, nil, level, time.Hour, kernel.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.NewClientWithResponses(base+"/api/v1", client.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "Bearer "+tok)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEC01_EC03_EC07_EC11_EC13_NFS02_FinanceHTTPWorkflow(t *testing.T) {
	ctx := context.Background()
	a, err := app.Build(ctx, app.Config{Storage: "memory", AuthMode: "hmac", HMACSecret: secret, HMACIssuer: issuer, OTelExport: "none"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close(ctx) }()
	srv := httptest.NewServer(a.Handler)
	defer srv.Close()
	full := financeClient(t, srv.URL, []string{"finance", "cpo"}, "full")
	p := must(full.CreateProductWithResponse(ctx, client.CreateProductJSONRequestBody{Key: "finance-product", Name: "Synthetic Financial Product", Type: "security"}))
	if p.JSON201 == nil {
		t.Fatalf("create: %s", p.Body)
	}
	id := p.JSON201.Id
	period := "2026-09"
	input := client.FinanceFileInput{Filename: "synthetic.csv", ContentBase64: base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("product_id,period,category,amount,currency,team_id,headcount\n%s,%s,revenue,10000.00,RUB,,\n%s,%s,payroll,3000.00,RUB,team-small,3\n", id, period, id, period))), Template: ports.FinanceTemplate{}, ExpectedVersion: 0}
	for _, c := range []*client.ClientWithResponses{financeClient(t, srv.URL, []string{"admin"}, "full"), financeClient(t, srv.URL, []string{"finance"}, "none"), financeClient(t, srv.URL, []string{"finance"}, "aggregates")} {
		denied := must(c.PreviewFinanceImportWithResponse(ctx, id, period, input))
		if denied.StatusCode() != 403 {
			t.Fatalf("finance boundary: %d", denied.StatusCode())
		}
	}
	preview := must(full.PreviewFinanceImportWithResponse(ctx, id, period, input))
	if preview.JSON200 == nil || len(preview.JSON200.Errors) > 0 || len(preview.JSON200.Rows) != 2 {
		t.Fatalf("preview: %s", preview.Body)
	}
	saved := must(full.ImportFinanceFileWithResponse(ctx, id, period, input))
	if saved.JSON201 == nil || saved.JSON201.Version != 1 {
		t.Fatalf("import: %s", saved.Body)
	}
	report := must(full.GetEconomicsReportWithResponse(ctx, id, period, nil))
	if report.JSON200 == nil || report.JSON200.Total.Revenue != 1000000 || report.JSON200.Total.LoadedProfit != 700000 {
		t.Fatalf("P&L: %s", report.Body)
	}
	manual := client.EconomicsConfiguration{ExpectedVersion: 1, Fields: []economics.Field{{Key: "margin", Name: "Margin", Type: "percent", Source: "calculated", Formula: "ifgt(revenue,0,loaded_profit/revenue*100,0)"}}, Rows: []client.EconomicsRowRule{}}
	rules := must(full.ConfigureEconomicsWithResponse(ctx, id, period, manual))
	if rules.JSON200 == nil || rules.JSON200.Version != 2 {
		t.Fatalf("rules: %s", rules.Body)
	}
	scenario := must(full.CalculateEconomicsScenarioWithResponse(ctx, id, period, client.EconomicsScenario{Version: 2, FilterProductId: &id, Overrides: map[string]string{"revenue": "2000000"}}))
	if scenario.JSON200 == nil || scenario.JSON200.Total.LoadedProfit != 1700000 || !scenario.JSON200.Scenario {
		t.Fatalf("scenario: %s", scenario.Body)
	}
	snapshot := must(full.GetEconomicsSnapshotWithResponse(ctx, id, period, nil))
	if snapshot.JSON200 == nil || snapshot.JSON200.Rows[0].Amount.Amount != 1000000 {
		t.Fatal("scenario changed fact")
	}
	closed := must(full.CloseEconomicsWithResponse(ctx, id, period, client.EconomicsVersionInput{ExpectedVersion: 2}))
	if closed.JSON200 == nil || !closed.JSON200.Closed || closed.JSON200.Version != 3 {
		t.Fatalf("close: %s", closed.Body)
	}
	input.ExpectedVersion = 3
	blocked := must(full.ImportFinanceFileWithResponse(ctx, id, period, input))
	if blocked.StatusCode() != 409 {
		t.Fatalf("closed import: %s", blocked.Body)
	}
	input.Recalculate = pilotPtr(true)
	recalculated := must(full.ImportFinanceFileWithResponse(ctx, id, period, input))
	if recalculated.JSON201 == nil || recalculated.JSON201.Version != 4 || !recalculated.JSON201.Closed {
		t.Fatalf("recalculate: %s", recalculated.Body)
	}
	history := must(full.ListEconomicsVersionsWithResponse(ctx, id, period))
	if history.JSON200 == nil || len(*history.JSON200) != 4 {
		t.Fatalf("history: %s", history.Body)
	}
	csv := must(full.ExportEconomicsWithResponse(ctx, id, period, nil))
	if csv.StatusCode() != 200 || !strings.Contains(string(csv.Body), "1000000,300000,0,700000,700000") {
		t.Fatalf("export: %s", csv.Body)
	}
	stale := must(full.ConfigureEconomicsWithResponse(ctx, id, period, manual))
	if stale.StatusCode() != 409 {
		t.Fatalf("CAS: %s", stale.Body)
	}
	template := economics.ImportTemplate{ProductID: id, Name: "Synthetic", Template: ports.FinanceTemplate{Columns: map[string]string{}}}
	if r := must(full.SaveFinanceTemplateWithResponse(ctx, id, template)); r.StatusCode() != 200 {
		t.Fatalf("template: %s", r.Body)
	}
	if r := must(full.ListFinanceTemplatesWithResponse(ctx, id)); r.JSON200 == nil || len(*r.JSON200) != 1 {
		t.Fatalf("templates: %s", r.Body)
	}
	input.ExpectedVersion = 4
	input.ContentBase64 = base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("product_id,period,category,amount,currency\n%s,%s,revenue,1.123,RUB\n", id, period)))
	bad := must(full.ImportFinanceFileWithResponse(ctx, id, period, input))
	if bad.StatusCode() != 400 {
		t.Fatalf("partial invalid import accepted: %s", bad.Body)
	}
	if r := must(full.ListEconomicsVersionsWithResponse(ctx, id, period)); r.JSON200 == nil || len(*r.JSON200) != 4 {
		t.Fatal("failed import wrote a version")
	}
}

func TestEC07_EC08_EC11_NFS02_ReconciliationHTTP(t *testing.T) {
	ctx := context.Background()
	a, err := app.Build(ctx, app.Config{Storage: "memory", AuthMode: "hmac", HMACSecret: secret, HMACIssuer: issuer, OTelExport: "none"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close(ctx) }()
	srv := httptest.NewServer(a.Handler)
	defer srv.Close()
	full := financeClient(t, srv.URL, []string{"finance", "cpo"}, "full")
	p := must(full.CreateProductWithResponse(ctx, client.CreateProductJSONRequestBody{Key: "reconcile-http", Name: "Synthetic", Type: "security"}))
	if p.JSON201 == nil {
		t.Fatalf("product: %s", p.Body)
	}
	id, period := p.JSON201.Id, "2026-10"
	input := client.FinanceFileInput{Filename: "source.csv", ContentBase64: base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("product_id,period,category,amount_minor,currency\n%s,%s,revenue,10000,RUB\n", id, period))), Template: ports.FinanceTemplate{}}
	first := must(full.ImportFinanceFileWithResponse(ctx, id, period, input))
	if first.JSON201 == nil {
		t.Fatalf("import: %s", first.Body)
	}
	rowID := first.JSON201.Rows[0].ID
	vals := map[string]string{"custom": "17"}
	config := client.EconomicsConfiguration{ExpectedVersion: 1, Fields: []economics.Field{{Key: "custom", Name: "Manual", Type: "number", Source: "manual"}}, Rows: []client.EconomicsRowRule{{RowId: rowID, Allocations: []economics.Allocation{}, Values: &vals}}}
	saved := must(full.ConfigureEconomicsWithResponse(ctx, id, period, config))
	if saved.JSON200 == nil {
		t.Fatalf("configure: %s", saved.Body)
	}
	input.ExpectedVersion = 2
	input.PreserveRules = pilotPtr(true)
	input.ContentBase64 = base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("product_id,period,category,amount_minor,currency\n%s,%s,revenue,12000,RUB\n", id, period)))
	preview := must(full.PreviewFinanceReconciliationWithResponse(ctx, id, period, input))
	if preview.JSON200 == nil || preview.JSON200.Matched != 1 || preview.JSON200.Changed != 1 || len(preview.JSON200.Totals) != 1 || preview.JSON200.Totals[0].Delta != "2000" {
		t.Fatalf("reconciliation: %s", preview.Body)
	}
	unchanged := must(full.ListEconomicsVersionsWithResponse(ctx, id, period))
	if unchanged.JSON200 == nil || len(*unchanged.JSON200) != 2 {
		t.Fatalf("preview wrote data: %s", unchanged.Body)
	}
	denied := must(financeClient(t, srv.URL, []string{"finance"}, "aggregates").PreviewFinanceReconciliationWithResponse(ctx, id, period, input))
	if denied.StatusCode() != 403 {
		t.Fatalf("aggregate read leaked: %s", denied.Body)
	}
	imported := must(full.ImportFinanceFileWithResponse(ctx, id, period, input))
	if imported.JSON201 == nil || imported.JSON201.Rows[0].ID != rowID || imported.JSON201.Rows[0].Values["custom"] != "17" || imported.JSON201.Rows[0].Amount.Amount != 12000 {
		t.Fatalf("reimport: %s", imported.Body)
	}
	stale := must(full.ImportFinanceFileWithResponse(ctx, id, period, input))
	if stale.StatusCode() != 409 {
		t.Fatalf("stale: %s", stale.Body)
	}
}

func TestEC08_EC09_EC11_NFS02_ManualModelHTTP(t *testing.T) {
	ctx := context.Background()
	a, err := app.Build(ctx, app.Config{Storage: "memory", AuthMode: "hmac", HMACSecret: secret, HMACIssuer: issuer, OTelExport: "none"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close(ctx) }()
	srv := httptest.NewServer(a.Handler)
	defer srv.Close()
	full := financeClient(t, srv.URL, []string{"finance", "cpo"}, "full")
	product := must(full.CreateProductWithResponse(ctx, client.CreateProductJSONRequestBody{Key: "manual-model", Name: "Synthetic", Type: "security"}))
	if product.JSON201 == nil {
		t.Fatalf("product: %s", product.Body)
	}
	field := must(full.SaveFinancialFieldWithResponse(ctx, client.FinancialFieldInput{Key: "manual_http", Name: "Manual", Type: client.FinancialFieldInputTypeNumber, Source: client.FinancialFieldInputSourceManual}))
	if field.JSON200 == nil {
		t.Fatalf("field: %s", field.Body)
	}
	body := client.ManualFinancialValue{ProductId: product.JSON201.Id, Period: "2026-10", Value: "123.4567890123456789"}
	denied := must(financeClient(t, srv.URL, []string{"finance"}, "aggregates").SetManualFinancialValueWithResponse(ctx, "manual_http", body))
	if denied.StatusCode() != 403 {
		t.Fatalf("denial: %s", denied.Body)
	}
	saved := must(full.SetManualFinancialValueWithResponse(ctx, "manual_http", body))
	if saved.JSON201 == nil || saved.JSON201.Value != body.Value {
		t.Fatalf("manual: %s", saved.Body)
	}

	revenue := must(full.SaveFinancialFieldWithResponse(ctx, client.FinancialFieldInput{Key: "revenue", Name: "Revenue", Type: client.FinancialFieldInputTypeMoney, Source: client.FinancialFieldInputSourceImport, Currency: pilotPtr("RUB")}))
	if revenue.JSON200 == nil {
		t.Fatalf("revenue field: %s", revenue.Body)
	}
	metric := must(full.SaveMetricWithResponse(ctx, client.MetricInput{Key: "book_http", Name: "Book formula", Expression: `field("revenue") / d("2")`, Currency: pilotPtr("RUB")}))
	if metric.JSON200 == nil {
		t.Fatalf("metric: %s", metric.Body)
	}
	input := client.FinanceFileInput{Filename: "source.csv", ContentBase64: base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("product_id,period,category,amount_minor,currency\n%s,2026-10,revenue,12500,RUB\n", product.JSON201.Id))), Template: ports.FinanceTemplate{}}
	imported := must(full.ImportFinanceFileWithResponse(ctx, product.JSON201.Id, "2026-10", input))
	if imported.JSON201 == nil {
		t.Fatalf("book: %s", imported.Body)
	}
	shared := must(full.EvaluateBookMetricWithResponse(ctx, product.JSON201.Id, "2026-10", "book_http", &client.EvaluateBookMetricParams{Version: 1}))
	if shared.JSON200 == nil || shared.JSON200.Metric.Value != "6250" || shared.JSON200.Snapshot.Version != 1 || len(*shared.JSON200.Metric.Inputs) != 1 {
		t.Fatalf("shared formula: %s", shared.Body)
	}
	deniedShared := must(financeClient(t, srv.URL, []string{"finance"}, "aggregates").EvaluateBookMetricWithResponse(ctx, product.JSON201.Id, "2026-10", "book_http", &client.EvaluateBookMetricParams{Version: 1}))
	if deniedShared.StatusCode() != 403 {
		t.Fatalf("book formula boundary: %s", deniedShared.Body)
	}
	closedModel := must(full.ClosePeriodWithResponse(ctx, "2026-10"))
	if closedModel.StatusCode() != 204 {
		t.Fatalf("close model: %s", closedModel.Body)
	}
	changedMetric := must(full.SaveMetricWithResponse(ctx, client.MetricInput{Key: "book_http", Name: "Changed", Expression: `field("revenue") * d("2")`, Currency: pilotPtr("RUB")}))
	if changedMetric.StatusCode() != 409 {
		t.Fatalf("retroactive formula changed closed model: %s", changedMetric.Body)
	}
	stillPinned := must(full.EvaluateBookMetricWithResponse(ctx, product.JSON201.Id, "2026-10", "book_http", &client.EvaluateBookMetricParams{Version: 1}))
	if stillPinned.JSON200 == nil || stillPinned.JSON200.Metric.Value != "6250" {
		t.Fatalf("closed shared formula drift: %s", stillPinned.Body)
	}
	body.Value = "1e100000000"
	oversized := must(full.SetManualFinancialValueWithResponse(ctx, "manual_http", body))
	if oversized.StatusCode() != 422 && oversized.StatusCode() != 400 {
		t.Fatalf("unbounded decimal: %s", oversized.Body)
	}
}
