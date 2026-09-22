//go:build integration

package pgstore_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/economics/pgstore"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/migrate"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/ports"
)

func openDatabase(t *testing.T) *pgdb.DB {
	t.Helper()
	url := os.Getenv("METIS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("METIS_TEST_DATABASE_URL not set; integration environment documented in docs/questions.md question-05")
	}
	database, err := pgdb.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	if err := migrate.Up(context.Background(), database.Pool(), nil); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestEC07_EC11_NFS02_PGImmutableVersionsCASAndScope(t *testing.T) {
	database := openDatabase(t)
	ctx := context.Background()
	store := pgstore.New(database)
	product, other := kernel.NewID(), kernel.NewID()
	sc := authz.New(authz.Params{Subject: "synthetic-finance", Roles: []authz.Role{authz.RoleFinance}, AllProducts: authz.AccessPrivate, Finance: authz.FinanceFull})
	snapshot := economics.Snapshot{SnapshotInfo: economics.SnapshotInfo{ID: kernel.NewID(), ProductID: product, Period: "2026-09", Currency: "RUB", Version: 1, CreatedAt: time.Now().UTC(), CreatedBy: sc.Subject(), RowCount: 1},
		Rows: []economics.Row{{ID: kernel.NewID(), ProductID: other, Category: economics.Revenue, Amount: kernel.RUB(101)}}}
	if err := store.Append(ctx, sc, snapshot, 0); err != nil {
		t.Fatal(err)
	}
	secondPool, err := pgdb.Open(ctx, os.Getenv("METIS_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer secondPool.Close()
	otherStore := pgstore.New(secondPool)
	read, err := otherStore.Snapshot(ctx, sc, product, "2026-09", 0)
	if err != nil || read.Rows[0].Amount.Amount != 101 {
		t.Fatalf("restart: %+v %v", read, err)
	}
	narrow := authz.New(authz.Params{Subject: "narrow", Roles: []authz.Role{authz.RoleFinance}, Products: map[kernel.ID]authz.Access{product: authz.AccessPrivate}, Finance: authz.FinanceFull})
	for _, denied := range []authz.Scope{{}, narrow} {
		if _, err := otherStore.Snapshot(ctx, denied, product, "2026-09", 0); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("scope bypass: %v", err)
		}
		if _, err := otherStore.History(ctx, denied, product, "2026-09"); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("history bypass: %v", err)
		}
		if err := otherStore.Append(ctx, denied, snapshot, 1); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("append bypass: %v", err)
		}
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, destination := range []*pgstore.Store{store, otherStore} {
		wg.Add(1)
		go func(destination *pgstore.Store) {
			defer wg.Done()
			next := snapshot
			next.ID = kernel.NewID()
			next.Version = 2
			results <- destination.Append(ctx, sc, next, 1)
		}(destination)
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, kernel.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("CAS success=%d conflicts=%d", success, conflicts)
	}
	if _, err := database.Pool().Exec(ctx, "UPDATE economics.snapshots SET body='{}'::jsonb WHERE id=$1", snapshot.ID); err == nil {
		t.Fatal("immutable financial version allowed UPDATE")
	}
	if _, err := database.Pool().Exec(ctx, "DELETE FROM economics.snapshots WHERE id=$1", snapshot.ID); err == nil {
		t.Fatal("immutable financial version allowed DELETE")
	}
	rollback := errors.New("synthetic rollback")
	err = database.Transact(ctx, func(txctx context.Context) error {
		next := snapshot
		next.ID = kernel.NewID()
		next.Version = 3
		if err := store.Append(txctx, sc, next, 2); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	history, err := store.History(ctx, sc, product, "2026-09")
	if err != nil || len(history) != 2 {
		t.Fatalf("rollback history: %+v %v", history, err)
	}
	template := economics.ImportTemplate{ProductID: product, Name: "synthetic", Template: ports.FinanceTemplate{Columns: map[string]string{"amount_minor": "amount"}}, UpdatedAt: time.Now().UTC()}
	if err := store.UpsertTemplate(ctx, sc, template); err != nil {
		t.Fatal(err)
	}
	got, err := otherStore.ListTemplates(ctx, sc, product)
	if err != nil || len(got) != 1 || got[0].Template.Columns["amount_minor"] != "amount" {
		t.Fatalf("template reload: %+v %v", got, err)
	}
	if _, err := otherStore.ListTemplates(ctx, authz.Scope{}, product); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("template scope: %v", err)
	}
}
