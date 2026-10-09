//go:build integration

package architecture_test

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	commitmentpg "github.com/onixus/metis/internal/commitments/pgstore"
	compliancepg "github.com/onixus/metis/internal/compliance/pgstore"
	decisionpg "github.com/onixus/metis/internal/decisions/pgstore"
	deliverypg "github.com/onixus/metis/internal/delivery/pgstore"
	discoverypg "github.com/onixus/metis/internal/discovery/pgstore"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/migrate"
	"github.com/onixus/metis/internal/kernel/pgdb"
	prioritypg "github.com/onixus/metis/internal/prioritization/pgstore"
	roadmappg "github.com/onixus/metis/internal/roadmap/pgstore"
	signalpg "github.com/onixus/metis/internal/signals/pgstore"
)

// A separate synthetic database prevents other packages' TRUNCATE fixtures from
// erasing this cross-module test while go test runs packages concurrently.
func TestAD02_NFS01_PGStoresRejectForeignProductAndOwnerTakeover(t *testing.T) {
	raw := os.Getenv("METIS_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("METIS_TEST_DATABASE_URL not set (docs/questions.md №05)")
	}
	ctx := context.Background()
	admin, err := pgdb.Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := "metis_scope_" + strings.ReplaceAll(kernel.NewID().String(), "-", "")
	if _, err := admin.Pool().Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Pool().Exec(ctx, "DROP DATABASE "+name); err != nil {
			t.Error(err)
		}
	}()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := pgdb.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate.Up(ctx, db.Pool(), nil); err != nil {
		t.Fatal(err)
	}
	testStoreOwnership(t, map[string]any{
		"signals": signalpg.New(db), "roadmap": roadmappg.New(db),
		"discovery": discoverypg.New(db), "decisions": decisionpg.New(db),
		"commitments": commitmentpg.New(db, nil), "delivery": deliverypg.New(db, nil),
		"compliance": compliancepg.New(db), "prioritization": prioritypg.New(db),
	})
}
