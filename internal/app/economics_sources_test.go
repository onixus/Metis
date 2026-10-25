package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onixus/metis/internal/economics"
	modeling "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/economics/sources"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
	"github.com/shopspring/decimal"
)

type formulaCRM struct {
	deals []ports.Deal
	err   error
	calls int
}

func (c *formulaCRM) Accounts(context.Context) ([]ports.Account, error) { return nil, nil }
func (c *formulaCRM) Deals(context.Context) ([]ports.Deal, error)       { c.calls++; return c.deals, c.err }

type sourceTracker struct {
	*financeTracker
	calls int
}

func (t *sourceTracker) Worklogs(ctx context.Context, issues []string, since time.Time) ([]ports.Worklog, error) {
	t.calls++
	logs, err := t.financeTracker.Worklogs(ctx, issues, since)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, key := range issues {
		allowed[key] = true
	}
	out := []ports.Worklog{}
	for _, log := range logs {
		if allowed[log.IssueKey] {
			out = append(out, log)
		}
	}
	return out, nil
}
func formulaSourceApp(t *testing.T) (*App, *financeTracker, economics.Snapshot, kernel.ID) {
	a, tracker, book, other := worklogApp(t)
	a.Tracker = &sourceTracker{financeTracker: tracker}
	return a, tracker, book, other
}

func TestEC09_EC10_EC13_RealFormulaSourceAdaptersAndClosedCapture(t *testing.T) {
	a, tracker, book, _ := formulaSourceApp(t)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("synthetic-formulas")
	a.Cfg.FormulaBookProductID = book.ProductID.String()
	product, err := a.Portfolio.Product(ctx, sc, book.ProductID)
	if err != nil {
		t.Fatal(err)
	}
	crm := &formulaCRM{deals: []ports.Deal{{ExternalID: "opaque-deal", ProductKey: product.Key, Outcome: ports.DealWon, ClosedDate: kernel.DateOf(2026, 9, 20), Amount: kernel.RUB(12000), Products: []string{product.Key, "extra-attach"}, Name: "not-in-provenance"}}}
	a.CRM = crm
	period, _ := modeling.ParsePeriod(book.Period)
	sl := modeling.Slice{ProductID: book.ProductID, Period: period}
	reader := economicsPlatformSources{app: a}
	for _, tc := range []struct {
		kind modeling.PlatformSourceKind
		want string
	}{{modeling.SourceWorklogs, "3600"}, {modeling.SourceCRMRevenue, "12000"}, {modeling.SourceHeadcount, "5"}} {
		v, err := reader.ReadFormulaSource(ctx, sc, tc.kind, sl)
		if err != nil || v.Value.String() != tc.want || v.SnapshotID == "" || v.AsOf.IsZero() {
			t.Fatalf("%s: %+v %v", tc.kind, v, err)
		}
		for _, p := range v.Provenance {
			if strings.Contains(p.RecordID, "opaque") || strings.Contains(p.RecordID, "not-in-provenance") {
				t.Fatalf("private record leaked: %+v", p)
			}
		}
	}
	key := sources.Key{ProductID: sl.ProductID, Period: book.Period, Kind: "crm_revenue"}
	hist, err := a.FormulaSources.History(ctx, sc, key)
	if err != nil || len(hist) != 1 {
		t.Fatalf("source history: %+v %v", hist, err)
	}
	if _, err := reader.ReadFormulaSource(ctx, sc, modeling.SourceCRMRevenue, sl); err != nil {
		t.Fatal(err)
	}
	hist, _ = a.FormulaSources.History(ctx, sc, key)
	if len(hist) != 1 {
		t.Fatalf("unchanged source produced duplicate revisions: %d", len(hist))
	}
	if _, err := a.Modeling.SaveMetric(ctx, sc, modeling.MetricInput{Key: "direct_sources", Name: "Synthetic sources", Expression: `worklogs() + crm_revenue() + headcount()`, EffectiveFrom: kernel.DateOf(2026, 1, 1)}); err != nil {
		t.Fatal(err)
	}
	ex, err := a.Modeling.Explain(ctx, sc, "direct_sources", sl)
	if err != nil || !ex.Value.Equal(decimal.NewFromInt(15605)) || len(ex.Inputs) != 3 {
		t.Fatalf("real formula: %+v %v", ex, err)
	}
	crm.deals[0].Amount = kernel.RUB(14000)
	updated, err := a.Modeling.Value(ctx, sc, "direct_sources", sl)
	if err != nil || !updated.Equal(decimal.NewFromInt(17605)) {
		t.Fatalf("live source update: %v %v", updated, err)
	}
	if err := a.Modeling.ClosePeriod(ctx, sc, period); err != nil {
		t.Fatal(err)
	}
	calls := crm.calls
	crm.err = kernel.ErrUnavailable
	tracker.err = kernel.ErrUnavailable
	frozen, err := a.Modeling.Value(ctx, sc, "direct_sources", sl)
	if err != nil || !frozen.Equal(updated) || crm.calls != calls {
		t.Fatalf("closed source drift/read: %v %v calls %d/%d", frozen, err, crm.calls, calls)
	}
	missing := sl
	missing.TeamID = kernel.NewID()
	if _, err := reader.ReadFormulaSource(ctx, sc, modeling.SourceWorklogs, missing); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("missing closed capture: %v", err)
	}
	for _, denied := range []authz.Scope{{}, authz.New(authz.Params{Subject: "aggregate", Roles: []authz.Role{authz.RoleFinance}, AllProducts: authz.AccessPrivate, Finance: authz.FinanceAggregates})} {
		if _, err := reader.ReadFormulaSource(ctx, denied, modeling.SourceCRMRevenue, sl); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("source boundary: %v", err)
		}
	}
}

func TestEC09_NFS02_FormulaSourcesRejectAmbiguityAndPreserveTrueZero(t *testing.T) {
	a, tracker, book, _ := formulaSourceApp(t)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("source-zero")
	p, _ := modeling.ParsePeriod(book.Period)
	sl := modeling.Slice{ProductID: book.ProductID, Period: p}
	reader := economicsPlatformSources{app: a}
	a.Cfg.FormulaBookProductID = book.ProductID.String()
	crm := &formulaCRM{}
	a.CRM = crm
	zero, err := reader.ReadFormulaSource(ctx, sc, modeling.SourceCRMRevenue, sl)
	if err != nil || !zero.Value.IsZero() || zero.SnapshotID == "" {
		t.Fatalf("true zero: %+v %v", zero, err)
	}
	crm.err = kernel.ErrUnavailable
	if _, err := reader.ReadFormulaSource(ctx, sc, modeling.SourceCRMRevenue, sl); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("source failure fell back tozero: %v", err)
	}
	crm.err = nil
	product, _ := a.Portfolio.Product(ctx, sc, book.ProductID)
	deal := ports.Deal{ExternalID: "one", ProductKey: product.Key, Outcome: ports.DealWon, ClosedDate: kernel.DateOf(2026, 9, 1), Amount: kernel.RUB(1)}
	crm.deals = []ports.Deal{deal, deal}
	once, err := reader.ReadFormulaSource(ctx, sc, modeling.SourceCRMRevenue, sl)
	if err != nil || once.Value.String() != "1" {
		t.Fatalf("duplicate id counted: %v %v", once, err)
	}
	crm.deals[1].Amount = kernel.RUB(2)
	if _, err := reader.ReadFormulaSource(ctx, sc, modeling.SourceCRMRevenue, sl); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("conflicting source duplicate: %v", err)
	}
	tracker.logs[0].Author = "unmapped"
	if _, err := reader.ReadFormulaSource(ctx, sc, modeling.SourceWorklogs, sl); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("unknown team author: %v", err)
	}
	tracker.logs = nil
	empty, err := reader.ReadFormulaSource(ctx, sc, modeling.SourceWorklogs, sl)
	if err != nil || !empty.Value.IsZero() {
		t.Fatalf("complete empty worklogs: %+v %v", empty, err)
	}
	team, _ := bookTeamID("team-platform")
	sl.TeamID = team
	hc, err := reader.ReadFormulaSource(ctx, sc, modeling.SourceHeadcount, sl)
	if err != nil || hc.Value.String() != "5" {
		t.Fatalf("book team UUID resolution: %+v %v", hc, err)
	}
	sl.TeamID = kernel.NewID()
	if _, err := reader.ReadFormulaSource(ctx, sc, modeling.SourceHeadcount, sl); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("unknown team: %v", err)
	}
}

func TestEC09_EC11_FormulaHeadcountPortfolioDeduplicatesTeamsAndFreezes(t *testing.T) {
	a, _, book, other := formulaSourceApp(t)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("portfolio-headcount")
	a.Cfg.FormulaBookProductID = book.ProductID.String()
	book.Rows = append(book.Rows, economics.Row{ProductID: other, Category: economics.Payroll, TeamID: "team-platform", Headcount: 5, Amount: kernel.RUB(10)})
	if _, err := a.Economics.Save(ctx, sc, economics.SaveInput{ProductID: book.ProductID, Period: book.Period, Currency: "RUB", ExpectedVersion: 1, Rows: book.Rows}); err != nil {
		t.Fatal(err)
	}
	period, _ := modeling.ParsePeriod(book.Period)
	sl := modeling.Slice{Period: period}
	reader := economicsPlatformSources{app: a}
	v, err := reader.ReadFormulaSource(ctx, sc, modeling.SourceHeadcount, sl)
	if err != nil || v.Value.String() != "5" {
		t.Fatalf("double counted sharedteam: %+v %v", v, err)
	}
	if err := a.Modeling.ClosePeriod(ctx, sc, period); err != nil {
		t.Fatal(err)
	}
	a.Cfg.FormulaBookProductID = book.ProductID.String()
	again, err := reader.ReadFormulaSource(ctx, sc, modeling.SourceHeadcount, sl)
	if err != nil || again.SnapshotID != v.SnapshotID {
		t.Fatalf("portfolio closed source: %+v %v", again, err)
	}
}

func TestEC09_EC10_PortfolioFormulaSourcesReadOneCoherentExternalSnapshot(t *testing.T) {
	a, _, book, other := formulaSourceApp(t)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("coherent")
	a.Cfg.FormulaBookProductID = book.ProductID.String()
	first, _ := a.Portfolio.Product(ctx, sc, book.ProductID)
	second, _ := a.Portfolio.Product(ctx, sc, other)
	crm := &formulaCRM{deals: []ports.Deal{{ExternalID: "d1", ProductKey: first.Key, Outcome: ports.DealWon, ClosedDate: kernel.DateOf(2026, 9, 1), Amount: kernel.RUB(10)}, {ExternalID: "d2", ProductKey: second.Key, Outcome: ports.DealWon, ClosedDate: kernel.DateOf(2026, 9, 1), Amount: kernel.RUB(20)}}}
	a.CRM = crm
	period, _ := modeling.ParsePeriod(book.Period)
	sl := modeling.Slice{Period: period}
	reader := economicsPlatformSources{app: a}
	revenue, err := reader.ReadFormulaSource(ctx, sc, modeling.SourceCRMRevenue, sl)
	if err != nil || revenue.Value.String() != "30" || crm.calls != 1 {
		t.Fatalf("CRM snapshot not coherent: %+v %v calls %d", revenue, err, crm.calls)
	}
	logs, err := reader.ReadFormulaSource(ctx, sc, modeling.SourceWorklogs, sl)
	if err != nil || logs.Value.String() != "10800" || a.Tracker.(*sourceTracker).calls != 1 {
		t.Fatalf("worklog snapshot not coherent: %+v %v calls %d", logs, err, a.Tracker.(*sourceTracker).calls)
	}
	if err := a.Modeling.ClosePeriod(ctx, sc, period); err != nil {
		t.Fatal(err)
	}
	crm.err = kernel.ErrUnavailable
	again, err := reader.ReadFormulaSource(ctx, sc, modeling.SourceCRMRevenue, sl)
	if err != nil || again.SnapshotID != revenue.SnapshotID || crm.calls != 1 {
		t.Fatalf("closed portfolio source changed: %+v %v", again, err)
	}
}

func TestEC09_EC12_WorklogSourceChecksIndividualSecondsWithoutNanosecondOverflow(t *testing.T) {
	a, tracker, book, _ := formulaSourceApp(t)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("source-duration")
	a.Cfg.FormulaBookProductID = book.ProductID.String()
	period, _ := modeling.ParsePeriod(book.Period)
	sl := modeling.Slice{ProductID: book.ProductID, Period: period}
	start := period.Start().Time()
	reader := economicsPlatformSources{app: a}
	tracker.logs = nil
	for i := 0; i < 10; i++ {
		tracker.logs = append(tracker.logs, ports.Worklog{ExternalID: fmt.Sprint(i), IssueKey: "SRC-2", Author: "opaque-1", Started: start, Spent: time.Duration(1000000000000000000)})
	}
	value, err := reader.ReadFormulaSource(ctx, sc, modeling.SourceWorklogs, sl)
	if err != nil || value.Value.String() != "10000000000" {
		t.Fatalf("seconds aggregate incorrectly bounded in nanos: %+v %v", value, err)
	}
	tracker.logs = []ports.Worklog{{ExternalID: "fraction-one", IssueKey: "SRC-2", Author: "opaque-1", Started: start, Spent: time.Nanosecond}, {ExternalID: "fraction-two", IssueKey: "SRC-2", Author: "opaque-1", Started: start, Spent: time.Second - time.Nanosecond}}
	if _, err := reader.ReadFormulaSource(ctx, sc, modeling.SourceWorklogs, sl); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("individual fractional seconds accepted: %v", err)
	}
}
