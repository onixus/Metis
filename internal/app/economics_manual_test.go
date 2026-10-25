package app

import (
	"context"
	"errors"
	"testing"

	"github.com/onixus/metis/internal/economics"
	modeling "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

func manualCanonicalApp(t *testing.T) (*App, *modeling.MemStore, economics.Snapshot, kernel.ID) {
	t.Helper()
	a, _, book, other := formulaSourceApp(t)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("manual-fixture")
	book.Rows[0].Values = map[string]string{"legacy": "10"}
	book.Rows[0].Allocations = []economics.Allocation{{ProductID: book.ProductID, Share: decimal.New(6, -1)}, {ProductID: other, Share: decimal.New(4, -1)}}
	saved, err := a.Economics.Save(ctx, sc, economics.SaveInput{ProductID: book.ProductID, Period: book.Period, Currency: book.Currency, ExpectedVersion: 1, Rows: book.Rows, Fields: []economics.Field{{Key: "legacy", Name: "Legacy manual", Type: "number", Source: "manual"}}})
	if err != nil {
		t.Fatal(err)
	}
	store := modeling.NewMemStore()
	model, err := modeling.NewService(store, nil, kernel.SystemClock{}, modeling.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	a.Modeling = model.WithAuditor(financeAuditor{a.Audit})
	a.configureUnifiedEconomics()
	a.buildFormulaSources()
	period, _ := modeling.ParsePeriod(book.Period)
	if _, err := a.Modeling.SaveField(ctx, sc, modeling.FieldInput{Key: "mapped_legacy", Name: "Legacy manual", Type: modeling.FieldNumber, Source: modeling.SourceManual, EffectiveFrom: period.Start()}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveUnification(ctx, sc, modeling.HistoryArchive{ID: kernel.NewID(), BookProductID: book.ProductID, Period: period, Digest: "synthetic-binding", FieldKeys: map[string]string{"legacy": "mapped_legacy"}, MetricKeys: map[string]string{}, BookVersions: []economics.Snapshot{saved}}); err != nil {
		t.Fatal(err)
	}
	return a, store, saved, other
}
func TestEC08_EC09_EC11_CanonicalManualWriterUsesOneBookAndPreservesAllocations(t *testing.T) {
	a, store, book, other := manualCanonicalApp(t)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("canonical-manual")
	period, _ := modeling.ParsePeriod(book.Period)
	teams, err := a.Modeling.Teams(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	var team kernel.ID
	for _, candidate := range teams {
		if candidate.Key == "team-platform" {
			team = candidate.ID
		}
	}
	if team == kernel.NilID {
		t.Fatal("missing canonical team")
	}
	for _, key := range []string{"new_manual", "imported"} {
		source := modeling.SourceManual
		if key == "imported" {
			source = modeling.SourceImport
		}
		if _, err := a.Modeling.SaveField(ctx, sc, modeling.FieldInput{Key: key, Name: key, Type: modeling.FieldNumber, Source: source, EffectiveFrom: period.Start()}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Modeling.SaveMetric(ctx, sc, modeling.MetricInput{Key: "manual_value", Name: "Manual value", Expression: `field("new_manual")`, EffectiveFrom: period.Start()}); err != nil {
		t.Fatal(err)
	}
	sl := modeling.Slice{ProductID: book.ProductID, Period: period}
	first, err := a.Modeling.SetManualValue(ctx, sc, "new_manual", sl, decimal.New(123456, -2))
	if err != nil || first.DataVersion != 3 {
		t.Fatalf("canonical new input: %+v %v", first, err)
	}
	value, err := a.Modeling.Value(ctx, sc, "manual_value", sl)
	if err != nil || value.String() != "1234.56" {
		t.Fatalf("common model value: %v %v", value, err)
	}
	report, err := a.Economics.Report(ctx, sc, economics.ReportInput{ProductID: book.ProductID, Period: book.Period})
	if err != nil || report.Total.Metrics["new_manual"] != "1234.56" {
		t.Fatalf("book input: %+v %v", report, err)
	}
	before, err := a.Economics.Snapshot(ctx, sc, book.ProductID, book.Period, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.Modeling.SetManualValue(ctx, sc, "new_manual", sl, decimal.NewFromInt(15))
	if err != nil {
		t.Fatal(err)
	}
	after, _ := a.Economics.Snapshot(ctx, sc, book.ProductID, book.Period, 0)
	if second.ID != first.ID || len(after.Rows) != len(before.Rows) {
		t.Fatalf("repeated write grew syntheticrows: %d/%d ids%s/%s", len(before.Rows), len(after.Rows), first.ID, second.ID)
	}
	if batches, err := store.Batches(ctx, sc, period); err != nil || len(batches) != 0 {
		t.Fatalf("second legacy input path: %+v %v", batches, err)
	}
	// Replacing an allocated manual input must not change the neighboring product.
	legacySlice := modeling.Slice{ProductID: book.ProductID, TeamID: team, Period: period}
	if _, err := a.Modeling.SetManualValue(ctx, sc, "mapped_legacy", legacySlice, decimal.NewFromInt(7)); err != nil {
		t.Fatal(err)
	}
	facts, err := a.Modeling.Facts(ctx, sc, modeling.FactFilter{FieldKey: "mapped_legacy", Period: &period})
	if err != nil {
		t.Fatal(err)
	}
	sums := map[kernel.ID]decimal.Decimal{}
	for _, fact := range facts {
		sums[fact.ProductID] = sums[fact.ProductID].Add(fact.Value)
	}
	if sums[book.ProductID].String() != "7" || sums[other].String() != "4" {
		t.Fatalf("allocated manual inputs drifted: %+v", sums)
	}
	updated, _ := a.Economics.Snapshot(ctx, sc, book.ProductID, book.Period, 0)
	if updated.Rows[0].Amount != book.Rows[0].Amount || len(updated.Rows[0].Allocations) != 2 {
		t.Fatal("external amount/allocation mutated")
	}
	itemSlice := sl
	itemSlice.Item = "support"
	if _, err := a.Modeling.SetManualValue(ctx, sc, "new_manual", itemSlice, decimal.NewFromInt(9)); err != nil {
		t.Fatal(err)
	}
	itemValue, err := a.Modeling.Value(ctx, sc, "manual_value", itemSlice)
	if err != nil || itemValue.String() != "9" {
		t.Fatalf("item slice lost: %v %v", itemValue, err)
	}
	if _, err := a.Modeling.SetManualValue(ctx, sc, "imported", sl, decimal.NewFromInt(1)); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("external source ownership bypass: %v", err)
	}
	if _, err := a.Modeling.SetManualValue(ctx, sc, "new_manual", modeling.Slice{ProductID: book.ProductID, Period: period, TeamID: kernel.NewID()}, decimal.NewFromInt(1)); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("unknown team accepted: %v", err)
	}
	latest, _ := a.Economics.Snapshot(ctx, sc, book.ProductID, book.Period, 0)
	if _, err := a.Economics.Close(ctx, sc, book.ProductID, book.Period, latest.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Modeling.SetManualValue(ctx, sc, "new_manual", sl, decimal.NewFromInt(1)); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("closed manual writer: %v", err)
	}
}
func TestEC08_NFS02_CanonicalManualWriterCannotGrantUserSchemaBypassOrRound(t *testing.T) {
	a, _, book, _ := manualCanonicalApp(t)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("canonical-manual")
	period, _ := modeling.ParsePeriod(book.Period)
	latest, _ := a.Economics.Snapshot(ctx, sc, book.ProductID, book.Period, 0)
	fields := append([]economics.Field{}, latest.Fields...)
	fields = append(fields, economics.Field{Key: "bypass", Name: "Bypass", Type: "number", Source: "manual"})
	if _, err := a.Economics.Save(ctx, sc, economics.SaveInput{ProductID: book.ProductID, Period: book.Period, Currency: book.Currency, ExpectedVersion: latest.Version, Rows: latest.Rows, Fields: fields}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("caller created trusted grant: %v", err)
	}
	if _, err := a.Modeling.SaveField(ctx, sc, modeling.FieldInput{Key: "decimal_input", Name: "Decimal", Type: modeling.FieldNumber, Source: modeling.SourceManual, EffectiveFrom: period.Start()}); err != nil {
		t.Fatal(err)
	}
	value, _ := decimal.NewFromString("0.1234567890123456")
	if _, err := a.Modeling.SetManualValue(ctx, sc, "decimal_input", modeling.Slice{ProductID: book.ProductID, Period: period}, value); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("unsupported canonical fraction rounded: %v", err)
	}
	after, _ := a.Economics.Snapshot(ctx, sc, book.ProductID, book.Period, 0)
	if after.Version != latest.Version {
		t.Fatal("failed write mutated canonical book")
	}
	limited := authz.New(authz.Params{Subject: "foreign", Roles: []authz.Role{authz.RoleFinance}, Finance: authz.FinanceFull})
	if _, err := a.Modeling.SetManualValue(ctx, limited, "decimal_input", modeling.Slice{ProductID: book.ProductID, Period: period}, decimal.NewFromInt(1)); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("foreign book manual leak: %v", err)
	}
}

func TestEC08_EC12_CanonicalManualSliceSurvivesWorklogAllocation(t *testing.T) {
	a, _, book, _ := manualCanonicalApp(t)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("manual-worklogs")
	period, _ := modeling.ParsePeriod(book.Period)
	teams, err := a.Modeling.Teams(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	var team kernel.ID
	for _, candidate := range teams {
		if candidate.Key == "team-platform" {
			team = candidate.ID
		}
	}
	if _, err := a.Modeling.SaveField(ctx, sc, modeling.FieldInput{Key: "keep_manual", Name: "Keep manual", Type: modeling.FieldNumber, Source: modeling.SourceManual, EffectiveFrom: period.Start()}); err != nil {
		t.Fatal(err)
	}
	written, err := a.Modeling.SetManualValue(ctx, sc, "keep_manual", modeling.Slice{ProductID: book.ProductID, TeamID: team, Period: period}, decimal.NewFromInt(15))
	if err != nil {
		t.Fatal(err)
	}
	allocated, err := a.ApplyFinanceWorklogs(ctx, sc, book.ProductID, book.Period, written.DataVersion, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range allocated.Rows {
		if row.ID == written.ID {
			found = true
			if row.ProductID != book.ProductID || row.TeamID != "team-platform" || len(row.Allocations) != 0 || row.Values["keep_manual"] != "15" {
				t.Fatalf("worklogs redistributed explicit manual input: %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("manual row removed")
	}
}
