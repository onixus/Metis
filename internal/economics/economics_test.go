package economics_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

type references struct{ denied kernel.ID }

func (r references) ValidateFinanceProduct(_ context.Context, _ authz.Scope, id kernel.ID) error {
	if id == r.denied {
		return kernel.ErrNotFound
	}
	return nil
}
func (r references) ValidateFinanceRow(ctx context.Context, sc authz.Scope, row economics.Row) error {
	return r.ValidateFinanceProduct(ctx, sc, row.ProductID)
}

type auditor struct {
	entries []audit.Entry
	err     error
}

func (a *auditor) Append(_ context.Context, e audit.Entry) (audit.Record, error) {
	a.entries = append(a.entries, e)
	return audit.Record{}, a.err
}

func fixture(t *testing.T) (*economics.Service, *economics.MemStore, *auditor, authz.Scope, economics.SaveInput, kernel.ID) {
	t.Helper()
	p1, err := kernel.ParseID("00000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := kernel.ParseID("00000000-0000-0000-0000-000000000002")
	if err != nil {
		t.Fatal(err)
	}
	track, feature := kernel.NewID(), kernel.NewID()
	alloc := []economics.Allocation{{ProductID: p2, Share: decimal.RequireFromString("0.5")}, {ProductID: p1, Share: decimal.RequireFromString("0.5")}}
	source := economics.Source{File: "synthetic.xlsx", Sheet: "Actual", Row: 2, Hash: "synthetic"}
	rows := []economics.Row{
		{ProductID: p1, Category: economics.Revenue, Amount: kernel.RUB(10000), Source: source},
		{ProductID: p1, Category: economics.Revenue, Amount: kernel.RUB(1000), CertificationTrackID: track, Source: source},
		{ProductID: p1, Category: economics.Payroll, Amount: kernel.RUB(2001), TeamID: "team-platform", Headcount: 3, Allocations: alloc, Source: source, Values: map[string]string{"worklog_hours": "80"}},
		{ProductID: p1, Category: economics.HubCost, Amount: kernel.RUB(101), Allocations: alloc, Source: source},
		{ProductID: p2, Category: economics.Revenue, Amount: kernel.RUB(5000), Source: source},
		{ProductID: p1, Category: economics.DirectCost, Amount: kernel.RUB(300), FeatureID: feature, Source: source},
		{ProductID: p1, Category: economics.CertificationCost, Amount: kernel.RUB(50), CertificationTrackID: track, Source: source},
		{ProductID: p1, Category: economics.MaintenanceCost, Amount: kernel.RUB(49), Branch: "1.x", Source: source},
		{ProductID: p2, Category: economics.Marketing, Amount: kernel.RUB(100), Source: source},
	}
	fields := []economics.Field{{Key: "worklog_hours", Type: "number", Source: "manual"}, {Key: "margin", Type: "percent", Source: "calculated", Formula: "ifgt(revenue, 0, loaded_profit / revenue * 100, 0)"},
		{Key: "profit_copy", Type: "money", Source: "calculated", Formula: "loaded_profit"}, {Key: "profit_twice", Type: "money", Source: "calculated", Formula: "profit_copy * 2"}}
	sc := authz.New(authz.Params{Subject: "synthetic-finance", Roles: []authz.Role{authz.RoleFinance}, AllProducts: authz.AccessPrivate, Finance: authz.FinanceFull})
	store, a := economics.NewMemStore(), &auditor{}
	service := economics.NewService(store, a, kernel.SystemClock{}).WithReferences(references{})
	return service, store, a, sc, economics.SaveInput{ProductID: p1, Period: "2026-09", Currency: "RUB", Rows: rows, Fields: fields}, p2
}

func TestEC02_EC03_EC05_EC06_EC12_ControlPLExactAllocationAndInvestments(t *testing.T) {
	svc, _, _, sc, input, p2 := fixture(t)
	ctx := context.Background()
	snapshot, err := svc.Save(ctx, sc, input)
	if err != nil {
		t.Fatal(err)
	}
	report, err := svc.Report(ctx, sc, economics.ReportInput{ProductID: input.ProductID, Period: input.Period})
	if err != nil {
		t.Fatal(err)
	}
	if report.Version != 1 || report.Total.Revenue != 16000 || report.Total.DirectCost != 2500 || report.Total.HubCost != 101 || report.Total.DirectProfit != 13500 || report.Total.LoadedProfit != 13399 {
		t.Fatalf("wrong control totals: %+v", report.Total)
	}
	if len(report.Products) != 2 || report.Products[0].LoadedProfit != 9549 || report.Products[1].LoadedProfit != 3850 {
		t.Fatalf("wrong products: %+v", report.Products)
	}
	if len(report.Teams) != 2 || report.Teams[0].Cost != 1001 || report.Teams[1].Cost != 1000 {
		t.Fatalf("lost allocation cent: %+v", report.Teams)
	}
	if len(report.Investments) != 3 {
		t.Fatalf("investments: %+v", report.Investments)
	}
	for _, item := range report.Investments {
		if item.Kind == "certification" && item.Balance != 950 {
			t.Fatalf("certification: %+v", item)
		}
	}
	if report.Products[1].ProductID != p2 || report.Products[0].Metrics["worklog_hours"] != "40" {
		t.Fatalf("dimension values: %+v", report.Products)
	}
	if snapshot.Rows[0].ID == kernel.NilID {
		t.Fatal("source row IDs missing")
	}
}

func TestEC04_BundleRevenueConservesEveryMinorUnit(t *testing.T) {
	svc, _, _, sc, input, p2 := fixture(t)
	input.Fields = nil
	input.Rows = []economics.Row{{ProductID: input.ProductID, Category: economics.Revenue, Amount: kernel.RUB(1), BundleID: "bundle", Allocations: []economics.Allocation{{ProductID: p2, Share: decimal.RequireFromString("0.5")}, {ProductID: input.ProductID, Share: decimal.RequireFromString("0.5")}}}}
	if _, err := svc.Save(context.Background(), sc, input); err != nil {
		t.Fatal(err)
	}
	report, err := svc.Report(context.Background(), sc, economics.ReportInput{ProductID: input.ProductID, Period: input.Period})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total.Revenue != 1 || report.Products[0].Revenue != 1 || report.Products[1].Revenue != 0 {
		t.Fatalf("nonconserved bundle: %+v", report)
	}
}

func TestEC08_EC09_EC10_FormulaDAGAndSourceLineage(t *testing.T) {
	svc, _, _, sc, input, _ := fixture(t)
	ctx := context.Background()
	if _, err := svc.Save(ctx, sc, input); err != nil {
		t.Fatal(err)
	}
	report, err := svc.Report(ctx, sc, economics.ReportInput{ProductID: input.ProductID, Period: input.Period})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total.Metrics["profit_twice"] != "26798" || len(report.Total.Lineage["profit_twice"].RowIDs) != len(input.Rows) {
		t.Fatalf("metric lineage: %+v", report.Total)
	}
	if report.Total.Lineage["profit_twice"].Formula != "profit_copy * 2" || len(report.Total.Lineage["profit_twice"].Sources) != len(input.Rows) {
		t.Fatalf("lineage: %+v", report.Total.Lineage)
	}
	for _, fields := range [][]economics.Field{
		{{Key: "a", Type: "number", Source: "calculated", Formula: "b+1"}, {Key: "b", Type: "number", Source: "calculated", Formula: "a+1"}},
		{{Key: "a", Type: "number", Source: "calculated", Formula: "secret_salary+1"}},
		{{Key: "a", Type: "number", Source: "calculated", Formula: "os.Getenv(1)"}},
	} {
		input.ExpectedVersion = 1
		input.Fields = fields
		input.Rows[2].Values = nil
		if _, err := svc.Save(ctx, sc, input); !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("accepted unsafe formula: %v", err)
		}
	}
}

func TestEC07_EC11_ImportVersionsCloseAndExplicitRecalculation(t *testing.T) {
	svc, _, _, sc, input, _ := fixture(t)
	ctx := context.Background()
	preview := ports.FinancePreview{Rows: []ports.FinanceRow{{ProductID: input.ProductID, Period: input.Period, Category: economics.Revenue, Amount: kernel.RUB(100), Source: ports.FinanceSource{File: "actual.csv", Row: 2, Hash: "hash"}}}, SourceHash: "hash"}
	first, err := svc.Import(ctx, sc, input.ProductID, input.Period, preview, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	closed, err := svc.Close(ctx, sc, input.ProductID, input.Period, 1)
	if err != nil || !closed.Closed || closed.Version != 2 {
		t.Fatalf("close: %+v %v", closed, err)
	}
	preview.Rows[0].Amount = kernel.RUB(200)
	if _, err := svc.Import(ctx, sc, input.ProductID, input.Period, preview, 2, false); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("closed overwrite: %v", err)
	}
	third, err := svc.Import(ctx, sc, input.ProductID, input.Period, preview, 2, true)
	if err != nil || !third.Closed || third.Version != 3 {
		t.Fatalf("explicit recalc: %+v %v", third, err)
	}
	old, err := svc.Snapshot(ctx, sc, input.ProductID, input.Period, 1)
	if err != nil || !reflect.DeepEqual(old, first) {
		t.Fatalf("history was mutated: %+v %v", old, err)
	}
	history, err := svc.History(ctx, sc, input.ProductID, input.Period)
	if err != nil || len(history) != 3 {
		t.Fatalf("history: %+v %v", history, err)
	}
	preview.Errors = []ports.FinanceRowError{{Row: 3, Field: "amount", Message: "invalid"}}
	if _, err := svc.Import(ctx, sc, input.ProductID, input.Period, preview, 3, true); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("partial import: %v", err)
	}
}

func TestEC13_ScenarioUsesSameEngineAndDoesNotChangeFacts(t *testing.T) {
	svc, _, _, sc, input, _ := fixture(t)
	ctx := context.Background()
	first, err := svc.Save(ctx, sc, input)
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := svc.Report(ctx, sc, economics.ReportInput{ProductID: input.ProductID, Period: input.Period, FilterProductID: input.ProductID, Overrides: map[string]string{economics.Payroll: "0"}})
	if err != nil {
		t.Fatal(err)
	}
	if !scenario.Scenario || scenario.Total.LoadedProfit != 10550 || scenario.Total.Metrics["profit_copy"] != "10550" || len(scenario.Teams) != 0 {
		t.Fatalf("scenario: %+v", scenario)
	}
	actual, err := svc.Snapshot(ctx, sc, input.ProductID, input.Period, 0)
	if err != nil || !reflect.DeepEqual(first, actual) {
		t.Fatalf("scenario mutated facts: %+v %v", actual, err)
	}
	if _, err := svc.Report(ctx, sc, economics.ReportInput{ProductID: input.ProductID, Period: input.Period, Overrides: map[string]string{"direct_total": "1"}}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("derived override accepted: %v", err)
	}
}

func TestNFS02_NFS16_FinanceScopeAndAuditFailClosed(t *testing.T) {
	svc, store, a, sc, input, _ := fixture(t)
	ctx := context.Background()
	if _, err := svc.Save(ctx, sc, input); err != nil {
		t.Fatal(err)
	}
	scopes := []authz.Scope{{}, authz.New(authz.Params{Subject: "admin", Roles: []authz.Role{authz.RoleAdmin}, AllProducts: authz.AccessPrivate, Finance: authz.FinanceFull}),
		authz.New(authz.Params{Subject: "finance-no-claim", Roles: []authz.Role{authz.RoleFinance}, AllProducts: authz.AccessPrivate}),
		authz.New(authz.Params{Subject: "aggregate", Roles: []authz.Role{authz.RoleFinance}, AllProducts: authz.AccessPrivate, Finance: authz.FinanceAggregates}),
		authz.New(authz.Params{Subject: "one-product", Roles: []authz.Role{authz.RoleFinance}, Products: map[kernel.ID]authz.Access{input.ProductID: authz.AccessPrivate}, Finance: authz.FinanceFull}),
		identityaccess.ServiceScope("ordinary")}
	for _, denied := range scopes {
		if _, err := svc.Report(ctx, denied, economics.ReportInput{ProductID: input.ProductID, Period: input.Period}); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("scope exposed team payroll: %v", err)
		}
		if _, err := store.Snapshot(ctx, denied, input.ProductID, input.Period, 0); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("repository scope bypass: %v", err)
		}
	}
	a.err = kernel.ErrUnavailable
	if _, err := svc.Report(ctx, sc, economics.ReportInput{ProductID: input.ProductID, Period: input.Period}); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("audit failure disclosed report: %v", err)
	}
	input.ExpectedVersion = 1
	if _, err := svc.Save(ctx, sc, input); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("audit failure saved facts: %v", err)
	}
	a.err = nil
	if _, err := svc.Report(ctx, sc, economics.ReportInput{ProductID: input.ProductID, Period: input.Period, Export: true}); err != nil {
		t.Fatal(err)
	}
	if a.entries[len(a.entries)-1].Action != audit.ActionExport {
		t.Fatal("export was not audited")
	}
}

func TestEC03_NFS15_RejectsOverflowInvalidSharesAndNumericBombs(t *testing.T) {
	for _, kind := range []string{"overflow", "currency", "negative", "shares", "exponent", "unknown_product"} {
		t.Run(kind, func(t *testing.T) {
			svc, _, _, sc, input, p2 := fixture(t)
			input.Fields = nil
			input.Rows = []economics.Row{{ProductID: input.ProductID, Category: economics.Revenue, Amount: kernel.RUB(1)}}
			switch kind {
			case "overflow":
				input.Rows[0].Amount = kernel.RUB(math.MaxInt64)
				input.Rows = append(input.Rows, economics.Row{ProductID: p2, Category: economics.Revenue, Amount: kernel.RUB(1)})
			case "currency":
				input.Rows[0].Amount.Currency = "USD"
			case "negative":
				input.Rows[0].Amount.Amount = -1
			case "shares":
				input.Rows[0].Allocations = []economics.Allocation{{ProductID: p2, Share: decimal.RequireFromString("0.9")}}
			case "exponent":
				input.Fields = []economics.Field{{Key: "hours", Type: "number", Source: "manual"}}
				input.Rows[0].Values = map[string]string{"hours": "1e999999999"}
			case "unknown_product":
				svc.WithReferences(references{denied: p2})
				input.Rows[0].Allocations = []economics.Allocation{{ProductID: p2, Share: decimal.NewFromInt(1)}}
			}
			if _, err := svc.Save(context.Background(), sc, input); err == nil {
				t.Fatal("invalid financial state accepted")
			}
		})
	}
}

func TestEC11_RepositoryCASRejectsConcurrentVersionsAndDetachesRows(t *testing.T) {
	svc, store, _, sc, input, _ := fixture(t)
	ctx := context.Background()
	snapshot, err := svc.Save(ctx, sc, input)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Rows[0].Amount.Amount = 1
	read, err := store.Snapshot(ctx, sc, input.ProductID, input.Period, 0)
	if err != nil || read.Rows[0].Amount.Amount != 10000 {
		t.Fatal("caller mutated stored snapshot")
	}
	read.Version = 2
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- store.Append(ctx, sc, read, 1) }()
	}
	wg.Wait()
	close(results)
	conflicts, success := 0, 0
	for err := range results {
		if errors.Is(err, kernel.ErrConflict) {
			conflicts++
		} else if err == nil {
			success++
		} else {
			t.Fatal(err)
		}
	}
	if conflicts != 1 || success != 1 {
		t.Fatalf("CAS success=%d conflicts=%d", success, conflicts)
	}
}

func TestEC07_ImportTemplatesAreScopedAndDetached(t *testing.T) {
	svc, _, _, sc, input, _ := fixture(t)
	ctx := context.Background()
	template := economics.ImportTemplate{ProductID: input.ProductID, Name: "Actual", Template: ports.FinanceTemplate{Sheet: "Finance", Columns: map[string]string{"amount_minor": "Minor amount"}}}
	if _, err := svc.SaveTemplate(ctx, sc, template); err != nil {
		t.Fatal(err)
	}
	template.Template.Columns["amount_minor"] = "mutated"
	got, err := svc.Templates(ctx, sc, input.ProductID)
	if err != nil || len(got) != 1 || got[0].Template.Columns["amount_minor"] != "Minor amount" {
		t.Fatalf("templates: %+v %v", got, err)
	}
	if _, err := svc.Templates(ctx, authz.Scope{}, input.ProductID); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("template scope bypass: %v", err)
	}
}

func TestEC08_NFS15_CustomMoneyUsesIntegerAllocationAndComputationsAreBounded(t *testing.T) {
	svc, _, _, sc, input, p2 := fixture(t)
	ctx := context.Background()
	input.Fields = []economics.Field{{Key: "custom_cost", Type: "money", Source: "manual"}}
	input.Rows = []economics.Row{{ProductID: input.ProductID, Category: economics.Revenue, Amount: kernel.RUB(1), Values: map[string]string{"custom_cost": "-101"},
		Allocations: []economics.Allocation{{ProductID: input.ProductID, Share: decimal.RequireFromString("0.5")}, {ProductID: p2, Share: decimal.RequireFromString("0.5")}}}}
	if _, err := svc.Save(ctx, sc, input); err != nil {
		t.Fatal(err)
	}
	report, err := svc.Report(ctx, sc, economics.ReportInput{ProductID: input.ProductID, Period: input.Period})
	if err != nil {
		t.Fatal(err)
	}
	if report.Total.Metrics["custom_cost"] != "-101" || report.Products[0].Metrics["custom_cost"] != "-50" || report.Products[1].Metrics["custom_cost"] != "-51" {
		t.Fatalf("fractional monetary metric: %+v", report.Products)
	}
	input.ExpectedVersion = 1
	input.Rows[0].Values = nil
	input.Fields = []economics.Field{{Key: "a", Type: "number", Source: "calculated", Formula: strings.Repeat("9", 128)}, {Key: "b", Type: "number", Source: "calculated", Formula: "a * a"}}
	if _, err := svc.Save(ctx, sc, input); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("unbounded decimal DAG accepted: %v", err)
	}
	input.Fields = nil
	input.Rows[0].Allocations = nil
	for i := 0; i < 100; i++ {
		input.Rows[0].Allocations = append(input.Rows[0].Allocations, economics.Allocation{ProductID: kernel.NewID(), Share: decimal.RequireFromString("0.01")})
	}
	for i := 0; i < 8; i++ {
		input.Fields = append(input.Fields, economics.Field{Key: fmt.Sprintf("metric%d", i), Type: "number", Source: "calculated", Formula: strings.Repeat("1+", 499) + "1"})
	}
	if _, err := svc.Save(ctx, sc, input); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("calculation expansion budget ignored: %v", err)
	}
}

func TestEC07_CompositeImportPreservesPerSourceLineage(t *testing.T) {
	svc, _, _, sc, input, _ := fixture(t)
	ctx := context.Background()
	preview := ports.FinancePreview{SourceHash: "combined", SourceHashes: []string{"payroll-source", "business-source"}, Rows: []ports.FinanceRow{
		{ProductID: input.ProductID, Period: input.Period, Category: economics.Payroll, Amount: kernel.RUB(100), TeamID: "team", Headcount: 5, Source: ports.FinanceSource{File: "1c-zup", Row: 1, Hash: "payroll-source"}},
		{ProductID: input.ProductID, Period: input.Period, Category: economics.Revenue, Amount: kernel.RUB(200), Source: ports.FinanceSource{File: "1c-business", Row: 1, Hash: "business-source"}}}}
	snapshot, err := svc.Import(ctx, sc, input.ProductID, input.Period, preview, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SourceHash != "combined" || snapshot.Rows[0].Source.Hash != "payroll-source" || snapshot.Rows[1].Source.Hash != "business-source" {
		t.Fatalf("source lineage lost: %+v", snapshot)
	}
	preview.Rows[1].Source.Hash = "unlisted"
	if _, err := svc.Import(ctx, sc, input.ProductID, input.Period, preview, 1, false); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("untrusted lineage accepted: %v", err)
	}
}
