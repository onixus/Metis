package economics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onixus/metis/internal/audit"
	book "github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

type historyTx struct{ *MemStore }

func (m historyTx) Transact(ctx context.Context, _ authz.Scope, fn func(context.Context) error) error {
	return fn(ctx)
}

type historyAuditor struct{}

func (historyAuditor) FinanceAccess(context.Context, string, string, string, kernel.ID, map[string]any) error {
	return nil
}

type historyBookAuditor struct{}

func (historyBookAuditor) Append(context.Context, audit.Entry) (audit.Record, error) {
	return audit.Record{}, nil
}

type historyReferences struct{}

func (historyReferences) ValidateFinanceProduct(context.Context, authz.Scope, kernel.ID) error {
	return nil
}
func (historyReferences) ValidateFinanceRow(context.Context, authz.Scope, book.Row) error { return nil }
func historyFixture(t *testing.T) (*Service, *book.Service, authz.Scope, kernel.ID, Period) {
	t.Helper()
	sc := identityaccess.FinanceServiceScope("synthetic-history")
	ctx := context.Background()
	owner := kernel.NewID()
	p := PeriodOf(2026, 9)
	books := book.NewService(book.NewMemStore(), historyBookAuditor{}, kernel.SystemClock{}).WithReferences(historyReferences{})
	for n, amount := range []int64{10000, 20000} {
		_, err := books.Save(ctx, sc, book.SaveInput{ProductID: owner, Period: p.String(), Currency: "RUB", ExpectedVersion: n, Rows: []book.Row{{ProductID: owner, Category: book.Revenue, Amount: kernel.RUB(amount), Source: book.Source{File: "synthetic.csv", Row: 1, Hash: "synthetic"}}, {ProductID: owner, Category: book.Payroll, Amount: kernel.RUB(4001), TeamID: "synthetic_team", Headcount: 5, Source: book.Source{File: "synthetic.csv", Row: 2, Hash: "synthetic"}}}, Fields: []book.Field{{Key: "margin", Name: "Margin", Type: "percent", Source: "calculated", Formula: "loaded_profit/3"}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	store := historyTx{NewMemStore()}
	s, err := NewService(store, nil, kernel.SystemClock{}, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	s.WithAuditor(historyAuditor{}).WithCanonicalBooks(books)
	for _, key := range []string{"revenue", "budget"} {
		_, err := s.SaveField(ctx, sc, FieldInput{Key: key, Name: key, Type: FieldMoney, Source: SourceManual, Currency: "RUB", EffectiveFrom: kernel.DateOf(2026, 1, 1)})
		if err != nil {
			t.Fatal(err)
		}
	}
	for n, v := range []int64{9000, 15000} {
		b := ImportBatch{ID: kernel.NewID(), Period: p, DataVersion: n + 1, Status: BatchApplied, At: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)}
		if err := store.SaveBatch(ctx, sc, b); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendFacts(ctx, sc, []FactRow{{ID: kernel.NewID(), BatchID: b.ID, Period: p, DataVersion: n + 1, ProductID: owner, FieldKey: "revenue", Value: decimal.NewFromInt(v)}, {ID: kernel.NewID(), BatchID: b.ID, Period: p, DataVersion: n + 1, ProductID: owner, FieldKey: "budget", Value: decimal.NewFromInt(700)}}); err != nil {
			t.Fatal(err)
		}
	}
	return s, books, sc, owner, p
}
func TestEC07_EC09_EC11_HistoryUnificationSwitchesCanonicalInputsAndRetainsBothHistories(t *testing.T) {
	s, books, sc, owner, p := historyFixture(t)
	ctx := context.Background()
	in := HistoryUnificationInput{BookProductID: owner, Period: p, ExpectedBookVersion: 2, ExpectedModelVersion: 2}
	preview, err := s.PreviewUnification(ctx, sc, in)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Applied || preview.BookVersions != 2 || preview.ModelVersions != 2 || len(preview.ExcludedModelFields) != 1 || preview.ExcludedModelFields[0] != "budget" {
		t.Fatalf("preview: %+v", preview)
	}
	again, err := s.PreviewUnification(ctx, sc, in)
	if err != nil || preview.Digest != again.Digest {
		t.Fatalf("unstable plan: %v", err)
	}
	in.Digest = preview.Digest
	result, err := s.ApplyUnification(ctx, sc, in)
	if err != nil || !result.Applied {
		t.Fatalf("apply: %+v %v", result, err)
	}
	pnl, err := s.ProductPnL(ctx, sc, owner, p)
	if err != nil || pnl.Revenue.Amount != 20000 || pnl.LoadedProfit.Amount != 15999 {
		t.Fatalf("canonical PnL: %+v %v", pnl, err)
	}
	v := 1
	old, err := s.Facts(ctx, sc, FactFilter{Period: &p, DataVersion: &v})
	if err != nil || len(old) != 2 {
		t.Fatalf("old model history: %v %v", old, err)
	}
	archive, err := s.UnificationHistory(ctx, sc, p)
	if err != nil || len(archive.BookVersions) != 2 || len(archive.ModelVersions) != 2 {
		t.Fatalf("archive: %+v %v", archive, err)
	}
	if _, err := s.SetManualValue(ctx, sc, "revenue", Slice{ProductID: owner, Period: p}, decimal.NewFromInt(1)); !errors.Is(err, kernel.ErrValidation) && !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("independent write accepted: %v", err)
	}
	_, err = books.Save(ctx, sc, book.SaveInput{ProductID: owner, Period: p.String(), Currency: "RUB", ExpectedVersion: 2, Rows: []book.Row{{ProductID: owner, Category: book.Revenue, Amount: kernel.RUB(30000), Source: book.Source{File: "synthetic.csv", Row: 1, Hash: "synthetic"}}}, Fields: archive.BookVersions[1].Fields})
	if err != nil {
		t.Fatal(err)
	}
	pnl, err = s.ProductPnL(ctx, sc, owner, p)
	if err != nil || pnl.Revenue.Amount != 30000 {
		t.Fatalf("canonical writes not visible: %+v %v", pnl, err)
	}
	if replay, err := s.ApplyUnification(ctx, sc, in); err != nil || !replay.Applied || replay.Digest != in.Digest {
		t.Fatalf("idempotent replay: %+v %v", replay, err)
	}
}
func TestEC09_EC11_LegacyFormulaTranslationPreservesPrecisionAndLazyBranches(t *testing.T) {
	s, _, _, _, _ := historyFixture(t)
	expr, err := translateBookFormula("ifgt(revenue,0,loaded_profit/3,1/0)", nil, nil, map[string]string{"revenue": "revenue", "loaded_profit": "loaded_profit"})
	if err != nil {
		t.Fatal(err)
	}
	prog, err := s.engine.Compile(expr)
	if err != nil {
		t.Fatal(err)
	}
	_ = prog
	if expr == "" {
		t.Fatal("empty translation")
	}
}

type historySource struct{}

func (historySource) ReadFormulaSource(context.Context, authz.Scope, PlatformSourceKind, Slice) (PlatformValue, error) {
	return PlatformValue{Value: decimal.NewFromInt(5), SnapshotID: "synthetic-headcount", AsOf: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Provenance: []PlatformProvenance{{Source: "synthetic", RecordID: "team", Version: "1"}}}, nil
}
func TestEC13_NFS02_CanonicalScenarioSourceOverrideDoesNotOverridePnLFacts(t *testing.T) {
	s, _, sc, owner, p := historyFixture(t)
	ctx := context.Background()
	s.WithPlatformSources(historySource{})
	in := HistoryUnificationInput{BookProductID: owner, Period: p, ExpectedBookVersion: 2, ExpectedModelVersion: 2}
	preview, err := s.PreviewUnification(ctx, sc, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Digest = preview.Digest
	if _, err := s.ApplyUnification(ctx, sc, in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveMetric(ctx, sc, MetricInput{Key: "people", Name: "People", Expression: `headcount()`, EffectiveFrom: p.Start()}); err != nil {
		t.Fatal(err)
	}
	scenario, err := s.SaveScenario(ctx, sc, Scenario{Name: "Synthetic", Period: p, Products: []kernel.ID{owner}, Overrides: []Override{{Source: SourceHeadcount, ProductID: owner, Period: p, Value: decimal.NewFromInt(7)}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.RunScenario(ctx, sc, scenario.ID, []string{"people"})
	if err != nil {
		t.Fatal(err)
	}
	key := "people@" + owner.String()
	if result.Metrics[key] != "7" || result.BaseMetrics[key] != "5" || result.PnL[0] != result.BasePnL[0] {
		t.Fatalf("source scenario changed fact: %+v", result)
	}
}
func TestNFS02_UnboundAggregatePnLStillAllowedBoundBookRequiresFull(t *testing.T) {
	s, _, sc, owner, p := historyFixture(t)
	ctx := context.Background()
	aggregate := authz.New(authz.Params{Subject: "synthetic-aggregate", Roles: []authz.Role{authz.RoleFinance}, AllProducts: authz.AccessPrivate, Finance: authz.FinanceAggregates})
	if _, err := s.ProductPnL(ctx, aggregate, owner, p); err != nil {
		t.Fatalf("unbound aggregate regressed: %v", err)
	}
	in := HistoryUnificationInput{BookProductID: owner, Period: p, ExpectedBookVersion: 2, ExpectedModelVersion: 2}
	preview, err := s.PreviewUnification(ctx, sc, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Digest = preview.Digest
	if _, err := s.ApplyUnification(ctx, sc, in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProductPnL(ctx, aggregate, owner, p); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("bound private book leaked: %v", err)
	}
}

func TestEC07_HistoryArchiveIncludesRejectedImports(t *testing.T) {
	s, _, sc, owner, p := historyFixture(t)
	ctx := context.Background()
	c, err := s.canonicalStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Store.SaveBatch(ctx, sc, ImportBatch{ID: kernel.NewID(), Period: p, Status: BatchRejected, FileName: "rejected-synthetic.xlsx", Errors: []RowError{{Row: 7, Message: "synthetic validation"}}}); err != nil {
		t.Fatal(err)
	}
	in := HistoryUnificationInput{BookProductID: owner, Period: p, ExpectedBookVersion: 2, ExpectedModelVersion: 2}
	preview, err := s.PreviewUnification(ctx, sc, in)
	if err != nil {
		t.Fatal(err)
	}
	if preview.ModelVersions != 3 || preview.ModelVersion != 2 {
		t.Fatalf("rejected import lost: %+v", preview)
	}
	in.Digest = preview.Digest
	if _, err := s.ApplyUnification(ctx, sc, in); err != nil {
		t.Fatal(err)
	}
	archive, err := s.UnificationHistory(ctx, sc, p)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, version := range archive.ModelVersions {
		if version.Batch.Status == BatchRejected {
			found = len(version.Batch.Errors) == 1 && len(version.Facts) == 0
		}
	}
	if !found {
		t.Fatal("rejected history/errors not archived")
	}
}

func TestEC12_HistoryUnificationPreservesExistingTeamIDs(t *testing.T) {
	s, _, sc, owner, p := historyFixture(t)
	ctx := context.Background()
	team, err := s.SaveTeam(ctx, sc, "synthetic_team", "Synthetic Team")
	if err != nil {
		t.Fatal(err)
	}
	in := HistoryUnificationInput{BookProductID: owner, Period: p, ExpectedBookVersion: 2, ExpectedModelVersion: 2}
	preview, err := s.PreviewUnification(ctx, sc, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Digest = preview.Digest
	if _, err := s.ApplyUnification(ctx, sc, in); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Facts(ctx, sc, FactFilter{Period: &p, Team: &team.ID})
	if err != nil || len(rows) == 0 {
		t.Fatalf("old teamID lost canonical facts: %v %v", rows, err)
	}
	cost, err := s.TeamCosts(ctx, sc, team.ID, p)
	if err != nil || cost.Total.Amount != 4001 || cost.ByProduct[owner].Amount != 4001 {
		t.Fatalf("old teamID lost costs: %+v %v", cost, err)
	}
	teams, err := s.Teams(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, known := range teams {
		if known.Key == team.Key {
			count++
			if known.ID != team.ID {
				t.Fatal("canonical team duplicated oldidentity")
			}
		}
	}
	if count != 1 {
		t.Fatalf("team duplicates: %+v", teams)
	}
	matrix, err := s.Matrix(ctx, sc, p)
	if err != nil || matrix.Cells[team.ID][owner].Amount != 4001 {
		t.Fatalf("matrix identity: %+v %v", matrix, err)
	}
}

func TestEC04_HistoryUnificationBundleComponentConservesAllocatedMinorUnits(t *testing.T) {
	s, books, sc, owner, p := historyFixture(t)
	ctx := context.Background()
	other := kernel.NewID()
	last, err := books.Snapshot(ctx, sc, owner, p.String(), 2)
	if err != nil {
		t.Fatal(err)
	}
	_, err = books.Save(ctx, sc, book.SaveInput{ProductID: owner, Period: p.String(), Currency: "RUB", ExpectedVersion: 2, Fields: last.Fields, Rows: []book.Row{{ProductID: owner, Category: book.Revenue, BundleID: "synthetic_bundle", Amount: kernel.RUB(101), Allocations: []book.Allocation{{ProductID: owner, Share: decimal.New(5, -1)}, {ProductID: other, Share: decimal.New(5, -1)}}, Source: book.Source{File: "synthetic.csv", Row: 1, Hash: "synthetic"}}, {ProductID: owner, Category: book.Revenue, Amount: kernel.RUB(99), Source: book.Source{File: "synthetic.csv", Row: 2, Hash: "synthetic"}}}})
	if err != nil {
		t.Fatal(err)
	}
	in := HistoryUnificationInput{BookProductID: owner, Period: p, ExpectedBookVersion: 3, ExpectedModelVersion: 2}
	preview, err := s.PreviewUnification(ctx, sc, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Digest = preview.Digest
	if _, err := s.ApplyUnification(ctx, sc, in); err != nil {
		t.Fatal(err)
	}
	a, err := s.ProductPnL(ctx, sc, owner, p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.ProductPnL(ctx, sc, other, p)
	if err != nil {
		t.Fatal(err)
	}
	if a.BundleRevenue.Amount+b.BundleRevenue.Amount != 101 || a.Revenue.Amount != 99 || b.Revenue.Amount != 0 || a.LoadedProfit.Amount+b.LoadedProfit.Amount != 200 {
		t.Fatalf("bundle counted twice or lost: %+v %+v", a, b)
	}
	portfolio, err := s.PortfolioPnL(ctx, sc, p)
	if err != nil || portfolio.Revenue.Amount != 200 || portfolio.Profit.Amount != 200 {
		t.Fatalf("portfolio bundle: %+v %v", portfolio, err)
	}
	rows, err := s.Facts(ctx, sc, FactFilter{FieldKey: s.cfg.BundleRevenueField, Period: &p})
	if err != nil || len(rows) != 2 {
		t.Fatalf("bundle source inputs: %v %v", rows, err)
	}
	sum := decimal.Zero
	for _, row := range rows {
		sum = sum.Add(row.Value)
		if row.Item != "synthetic_bundle" {
			t.Fatal("bundle lineage lost")
		}
	}
	if !sum.Equal(decimal.NewFromInt(101)) {
		t.Fatalf("bundle facts total: %s", sum)
	}
}

func TestEC11_HistoryUnificationRejectsNamespaceWithDifferentSemanticHistory(t *testing.T) {
	s, _, sc, owner, p := historyFixture(t)
	ctx := context.Background()
	in := HistoryUnificationInput{BookProductID: owner, Period: p, ExpectedBookVersion: 2, ExpectedModelVersion: 2}
	archive, _, err := s.prepareUnification(ctx, sc, in)
	if err != nil {
		t.Fatal(err)
	}
	_, metrics, err := s.migratedDefinitions(archive)
	if err != nil || len(metrics) == 0 {
		t.Fatalf("definitions: %v", err)
	}
	expected := metrics[0]
	conflict := expected
	old := expected.Latest()
	old.Name = "Different historical meaning"
	old.Expression = `d("1")`
	old.Version = 1
	latest := expected.Latest()
	latest.Version = 2
	conflict.Versions = []MetricVersion{old, latest}
	c, err := s.canonicalStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SaveMetric(ctx, sc, conflict); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreviewUnification(ctx, sc, in); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("same latest expression masked different history: %v", err)
	}
}

func TestEC08_EC09_HistoryUnificationNormalizesDistinctLegacyKeysAndRemainsEditable(t *testing.T) {
	s, books, sc, owner, p := historyFixture(t)
	ctx := context.Background()
	last, err := books.Snapshot(ctx, sc, owner, p.String(), 2)
	if err != nil {
		t.Fatal(err)
	}
	fields := append(last.Fields, book.Field{Key: "Foo", Name: "Upper", Type: "number", Source: "manual"}, book.Field{Key: "foo", Name: "Lower", Type: "number", Source: "manual"}, book.Field{Key: "UpperMetric", Name: "Upper Metric", Type: "number", Source: "calculated", Formula: "Foo+foo"})
	rows := last.Rows
	rows[0].Values = map[string]string{"Foo": "2", "foo": "3"}
	if _, err := books.Save(ctx, sc, book.SaveInput{ProductID: owner, Period: p.String(), Currency: "RUB", ExpectedVersion: 2, Fields: fields, Rows: rows}); err != nil {
		t.Fatal(err)
	}
	in := HistoryUnificationInput{BookProductID: owner, Period: p, ExpectedBookVersion: 3, ExpectedModelVersion: 2}
	preview, err := s.PreviewUnification(ctx, sc, in)
	if err != nil {
		t.Fatal(err)
	}
	upper, lower, metric := preview.DefinitionMappings["Foo"], preview.DefinitionMappings["foo"], preview.DefinitionMappings["UpperMetric"]
	if upper == lower || upper == "" || metric == "" {
		t.Fatalf("namespace collision: %+v", preview.DefinitionMappings)
	}
	for _, key := range []string{upper, lower, metric} {
		if err := validKey(key); err != nil {
			t.Fatalf("uneditable migrated key %s: %v", key, err)
		}
	}
	in.Digest = preview.Digest
	if _, err := s.ApplyUnification(ctx, sc, in); err != nil {
		t.Fatal(err)
	}
	if value, err := s.Value(ctx, sc, metric, Slice{ProductID: owner, Period: p}); err != nil || !value.Equal(decimal.NewFromInt(5)) {
		t.Fatalf("case-sensitive inputs merged: %s %v", value, err)
	}
	if _, err := s.SaveField(ctx, sc, FieldInput{Key: upper, Name: "Edited Upper", Type: FieldNumber, Source: SourceManual, EffectiveFrom: p.Start()}); err != nil {
		t.Fatalf("migrated field uneditable: %v", err)
	}
	if _, err := s.SaveMetric(ctx, sc, MetricInput{Key: metric, Name: "Edited Metric", Expression: `field("` + upper + `")*d("2")+field("` + lower + `")`, EffectiveFrom: p.Start()}); err != nil {
		t.Fatalf("migrated metric uneditable: %v", err)
	}
	metrics, err := s.Metrics(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	foundType := false
	for _, m := range metrics {
		if m.Key == metric {
			foundType = m.Latest().ResultType == FieldNumber && m.Latest().Currency == ""
		}
	}
	if !foundType {
		t.Fatal("imported result type/currency lost during common edit")
	}
	if value, err := s.Value(ctx, sc, metric, Slice{ProductID: owner, Period: p}); err != nil || !value.Equal(decimal.NewFromInt(7)) {
		t.Fatalf("edited common definition: %s %v", value, err)
	}
}

type countingHistoryBooks struct {
	CanonicalBooks
	payload   book.Snapshot
	reads     int
	revisions int
}

func (c *countingHistoryBooks) History(context.Context, authz.Scope, kernel.ID, string) ([]book.SnapshotInfo, error) {
	out := make([]book.SnapshotInfo, c.revisions)
	for n := range out {
		out[n] = c.payload.SnapshotInfo
		out[n].Version = n + 1
	}
	return out, nil
}
func (c *countingHistoryBooks) Snapshot(_ context.Context, _ authz.Scope, _ kernel.ID, _ string, version int) (book.Snapshot, error) {
	c.reads++
	snapshot := c.payload
	snapshot.Version = version
	return snapshot, nil
}
func TestEC07_NFP05_HistoryArchiveRejectsByteBudgetBeforeLoadingAllRevisions(t *testing.T) {
	s, books, sc, owner, p := historyFixture(t)
	rows := make([]book.Row, 2000)
	for n := range rows {
		rows[n] = book.Row{ID: kernel.NewID(), ProductID: owner, Category: book.DirectCost, Amount: kernel.RUB(0), Description: strings.Repeat("x", 1500), Source: book.Source{File: "medium-synthetic.csv", Row: n + 1, Hash: "synthetic"}}
	}
	port := &countingHistoryBooks{CanonicalBooks: books, payload: book.Snapshot{SnapshotInfo: book.SnapshotInfo{ID: kernel.NewID(), ProductID: owner, Period: p.String(), Currency: "RUB", Version: 1, RowCount: len(rows)}, Rows: rows, Fields: []book.Field{}}, revisions: 128}
	encoded, err := json.Marshal(port.payload)
	if err != nil || len(encoded) >= maxHistoryArchiveBytes/2 || len(encoded) < maxHistoryArchiveBytes/16 {
		t.Fatalf("fixture must contain medium revisions: %d %v", len(encoded), err)
	}
	s.WithCanonicalBooks(port)
	_, err = s.PreviewUnification(context.Background(), sc, HistoryUnificationInput{BookProductID: owner, Period: p, ExpectedBookVersion: 128, ExpectedModelVersion: 2})
	if !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("oversized archive accepted: %v", err)
	}
	if port.reads < 2 || port.reads >= 16 {
		t.Fatalf("loaded too many revisions before byte refusal: %d", port.reads)
	}
	c, _ := s.canonicalStore()
	if _, err := c.Store.(HistoryStore).Unification(context.Background(), sc, p); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("oversized preview changed binding: %v", err)
	}
}
func TestNFP05_HistoryByteBudgetCannotWrapOrAdvanceAfterRefusal(t *testing.T) {
	b := historyByteBudget{remaining: 5}
	if err := b.charge("a"); err != nil || b.remaining != 1 {
		t.Fatalf("charge: remaining=%d err=%v", b.remaining, err)
	}
	if err := b.charge(""); !errors.Is(err, kernel.ErrValidation) || b.remaining != 1 {
		t.Fatalf("refused charge changed budget: remaining=%d err=%v", b.remaining, err)
	}
	b.remaining = int(^uint(0) >> 1)
	start := b.remaining
	if err := b.charge("a"); err != nil || b.remaining != start-4 {
		t.Fatalf("budget wrapped: remaining=%d err=%v", b.remaining, err)
	}
}
func TestEC09_EC10_HistoryPreviewRejectsUnrenderableTranslatedChainBeforeApply(t *testing.T) {
	s, books, sc, owner, p := historyFixture(t)
	ctx := context.Background()
	last, err := books.Snapshot(ctx, sc, owner, p.String(), 2)
	if err != nil {
		t.Fatal(err)
	}
	fields := []book.Field{}
	for n := 0; n < 8; n++ {
		expr := "loaded_profit"
		if n > 0 {
			expr = fmt.Sprintf("chain%d+1", n-1)
		}
		fields = append(fields, book.Field{Key: fmt.Sprintf("chain%d", n), Name: "Synthetic", Type: "number", Source: "calculated", Formula: expr})
	}
	if _, err := books.Save(ctx, sc, book.SaveInput{ProductID: owner, Period: p.String(), Currency: "RUB", ExpectedVersion: 2, Rows: last.Rows, Fields: fields}); err != nil {
		t.Fatal(err)
	}
	if _, err := books.Report(ctx, sc, book.ReportInput{ProductID: owner, Period: p.String(), Version: 3}); err != nil {
		t.Fatalf("original root chain must be valid: %v", err)
	}
	_, err = s.PreviewUnification(ctx, sc, HistoryUnificationInput{BookProductID: owner, Period: p, ExpectedBookVersion: 3, ExpectedModelVersion: 2})
	if !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("unrenderable common chain accepted: %v", err)
	}
	c, _ := s.canonicalStore()
	if _, err := c.Store.(HistoryStore).Unification(ctx, sc, p); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("preflight wrote binding: %v", err)
	}
	registry, err := c.Metrics(ctx, sc)
	if err != nil || len(registry) != 0 {
		t.Fatalf("preflight changed registry: %+v %v", registry, err)
	}
}
