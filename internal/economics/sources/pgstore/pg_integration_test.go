//go:build integration

package pgstore_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/economics/sources"
	"github.com/onixus/metis/internal/economics/sources/pgstore"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/migrate"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/shopspring/decimal"
)

type brokenAudit struct{}

func (brokenAudit) Append(context.Context, audit.Entry) (audit.Record, error) {
	return audit.Record{}, kernel.ErrUnavailable
}
func TestEC09_EC11_NFS05_PostgresSourcesRestartRollbackAndImmutable(t *testing.T) {
	raw := os.Getenv("METIS_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("METIS_TEST_DATABASE_URL missing: docs/questions.md #05")
	}
	ctx := context.Background()
	database, err := pgdb.Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := migrate.Up(ctx, database.Pool(), nil); err != nil {
		t.Fatal(err)
	}
	sc := identityaccess.FinanceServiceScope("synthetic")
	clock := kernel.FixedClock{T: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)}
	auditlog := audit.NewLogger(audit.NewMemStore(), clock)
	svc := sources.NewService(pgstore.New(database), auditlog, clock)
	in := sources.Input{Key: sources.Key{ProductID: kernel.NewID(), Period: "2026-10", Kind: "worklogs"}, Value: decimal.NewFromInt(3600), AsOf: clock.Now(), Provenance: []sources.Provenance{{Source: "delivery.aggregate", RecordID: "synthetic-content-hash"}}}
	first, err := svc.Capture(ctx, sc, in)
	if err != nil {
		t.Fatal(err)
	}
	secondDB, err := pgdb.Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer secondDB.Close()
	reader := sources.NewService(pgstore.New(secondDB), auditlog, clock)
	read, err := reader.Latest(ctx, sc, in.Key)
	if err != nil || read.ID != first.ID {
		t.Fatalf("restart lost source: %+v %v", read, err)
	}
	failed := sources.NewService(pgstore.New(database), brokenAudit{}, clock)
	in.Value = decimal.NewFromInt(7200)
	if _, err := failed.Capture(ctx, sc, in); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("audit failure ignored: %v", err)
	}
	unchanged, err := reader.Latest(ctx, sc, in.Key)
	if err != nil || unchanged.ID != first.ID {
		t.Fatalf("partial revision: %+v %v", unchanged, err)
	}
	for _, query := range []string{"UPDATE economics.formula_sources SET period='2026-11' WHERE id=$1", "DELETE FROM economics.formula_sources WHERE id=$1"} {
		if _, err := database.Pool().Exec(ctx, query, first.ID); err == nil {
			t.Fatalf("mutation allowed: %s", query)
		}
	}
}
