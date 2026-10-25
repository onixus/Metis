//go:build integration

package pgstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onixus/metis/internal/audit"
	book "github.com/onixus/metis/internal/economics"
	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/economics/modeling/pgstore"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

type historyLedgerAudit struct{}

func (historyLedgerAudit) Append(context.Context, audit.Entry) (audit.Record, error) {
	return audit.Record{}, nil
}

type historyReferences struct{}

func (historyReferences) ValidateFinanceProduct(context.Context, authz.Scope, kernel.ID) error {
	return nil
}
func (historyReferences) ValidateFinanceRow(context.Context, authz.Scope, book.Row) error { return nil }

type historyAudit struct{}

func (historyAudit) FinanceAccess(context.Context, string, string, string, kernel.ID, map[string]any) error {
	return nil
}
func TestEC07_EC09_EC11_NFS02_PostgresUnificationRollbackRestartAndIdempotence(t *testing.T) {
	isolateDatabase(t)
	databaseOwner := database(t)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("synthetic-history")
	owner := kernel.NewID()
	period := economics.PeriodOf(2026, 9)
	ledger := book.NewService(book.NewMemStore(), historyLedgerAudit{}, kernel.SystemClock{}).WithReferences(historyReferences{})
	for version, amount := range []int64{10000, 20000} {
		if _, err := ledger.Save(ctx, sc, book.SaveInput{ProductID: owner, Period: period.String(), Currency: "RUB", ExpectedVersion: version, Rows: []book.Row{{ProductID: owner, Category: book.Revenue, Amount: kernel.RUB(amount), Source: book.Source{File: "synthetic.csv", Row: 1, Hash: "synthetic"}}}, Fields: []book.Field{{Key: "third", Name: "Third", Type: "number", Source: "calculated", Formula: "revenue/3"}}}); err != nil {
			t.Fatal(err)
		}
	}
	model := service(t, databaseOwner, nil).WithAuditor(historyAudit{}).WithCanonicalBooks(ledger)
	if _, err := model.SaveField(ctx, sc, economics.FieldInput{Key: "revenue", Name: "Revenue", Type: economics.FieldMoney, Source: economics.SourceManual, Currency: "RUB"}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []int64{7000, 9000} {
		if _, err := model.SetManualValue(ctx, sc, "revenue", economics.Slice{ProductID: owner, Period: period}, decimal.NewFromInt(value)); err != nil {
			t.Fatal(err)
		}
	}
	in := economics.HistoryUnificationInput{BookProductID: owner, Period: period, ExpectedBookVersion: 2, ExpectedModelVersion: 2}
	preview, err := model.PreviewUnification(ctx, sc, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Digest = preview.Digest
	model.WithAuditor(failAuditor{})
	if _, err := model.ApplyUnification(ctx, sc, in); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("audit failure: %v", err)
	}
	raw := pgstore.New(databaseOwner)
	if _, err := raw.Unification(ctx, sc, period); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("partial binding after audit: %v", err)
	}
	for _, key := range preview.DefinitionKeys {
		if _, err := raw.Field(ctx, sc, key); err == nil {
			t.Fatalf("partial migrated field %s", key)
		}
		if _, err := raw.Metric(ctx, sc, key); err == nil {
			t.Fatalf("partial migrated metric %s", key)
		}
	}
	failing := service(t, databaseOwner, failPublisher{}).WithAuditor(historyAudit{}).WithCanonicalBooks(ledger)
	if _, err := failing.ApplyUnification(ctx, sc, in); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("outbox failure: %v", err)
	}
	if _, err := raw.Unification(ctx, sc, period); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("partial binding after outbox: %v", err)
	}
	model.WithAuditor(historyAudit{})
	if _, err := model.ApplyUnification(ctx, sc, in); err != nil {
		t.Fatal(err)
	}
	if err := raw.SaveBatch(ctx, sc, economics.ImportBatch{ID: kernel.NewID(), Period: period, DataVersion: 3, Status: economics.BatchApplied}); err == nil {
		t.Fatal("raw legacy store bypassed canonical DB guard")
	}
	if err := raw.SaveTeamShares(ctx, sc, []economics.TeamShare{{TeamID: kernel.NewID(), ProductID: owner, Period: period, Share: decimal.NewFromInt(1), Source: "manual"}}); err == nil {
		t.Fatal("raw legacy shares bypassed DB guard")
	}
	restarted := service(t, database(t), nil).WithAuditor(historyAudit{}).WithCanonicalBooks(ledger)
	pnl, err := restarted.ProductPnL(ctx, sc, owner, period)
	if err != nil || pnl.Revenue.Amount != 20000 {
		t.Fatalf("restart canonical: %+v %v", pnl, err)
	}
	key := preview.DefinitionMappings["third"]
	definitions, err := restarted.Metrics(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	typed := false
	for _, m := range definitions {
		if m.Key == key {
			typed = m.Latest().ResultType == economics.FieldNumber && m.Latest().Currency == ""
		}
	}
	if !typed {
		t.Fatal("imported result type did not survive restart")
	}
	value, err := restarted.Value(ctx, sc, key, economics.Slice{ProductID: owner, Period: period})
	if err != nil || value.String() != "6666.6666666666666667" {
		t.Fatalf("common precise formula: %s %v", value, err)
	}
	archive, err := restarted.UnificationHistory(ctx, sc, period)
	if err != nil || len(archive.BookVersions) != 2 || len(archive.ModelVersions) != 2 {
		t.Fatalf("lost source histories: %+v %v", archive, err)
	}
	if _, err := restarted.ApplyUnification(ctx, sc, in); err != nil {
		t.Fatalf("idempotence: %v", err)
	}
	if _, err := restarted.SetManualValue(ctx, sc, "revenue", economics.Slice{ProductID: owner, Period: period}, decimal.NewFromInt(1)); !errors.Is(err, kernel.ErrValidation) && !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("second input path: %v", err)
	}
	denied := identityaccess.ServiceScope("ordinary")
	if _, err := restarted.UnificationHistory(ctx, denied, period); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("archive access: %v", err)
	}
	stale := in
	stale.Digest = "stale"
	if _, err := restarted.ApplyUnification(ctx, sc, stale); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("stale plan: %v", err)
	}
}
