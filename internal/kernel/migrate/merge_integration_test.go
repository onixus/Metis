//go:build integration

package migrate_test

import (
	"context"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	metisdb "github.com/onixus/metis/db"
	"github.com/onixus/metis/internal/kernel/migrate"
)

// TestCM03_CM08_MergeDatabaseLineages exercises actual goose histories from both branches.
func TestCM03_CM08_MergeDatabaseLineages(t *testing.T) {
	url := os.Getenv("METIS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("METIS_TEST_DATABASE_URL не задан: docs/questions.md №05")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	for _, lineage := range []string{"pilot", "main"} {
		t.Run(lineage, func(t *testing.T) {
			name := "metis_merge_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
					t.Error(err)
				}
			}()
			cfg, err := pgxpool.ParseConfig(url)
			if err != nil {
				t.Fatal(err)
			}
			cfg.ConnConfig.Database = name
			pool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			historical := fstest.MapFS{}
			for _, file := range []string{"00001_compliance.sql", "00002_requirement_set_version_unique.sql"} {
				body, err := fs.ReadFile(metisdb.Migrations, "compliance/migrations/"+file)
				if err != nil {
					t.Fatal(err)
				}
				historical[file] = &fstest.MapFile{Data: body}
			}
			files := []string{"00003_settings.sql"}
			if lineage == "main" {
				files = []string{"00003_baseline_components.sql", "00004_settings.sql"}
			}
			for _, file := range files {
				var body []byte
				if lineage == "main" {
					body, err = os.ReadFile("../../../db/compliance/history/main/" + file)
				} else {
					body, err = fs.ReadFile(metisdb.Migrations, "compliance/migrations/"+file)
				}
				if err != nil {
					t.Fatal(err)
				}
				historical[file] = &fstest.MapFile{Data: body}
			}
			sqlDB := stdlib.OpenDBFromPool(pool)
			defer sqlDB.Close()
			provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, historical, goose.WithTableName("goose_compliance"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = provider.Up(ctx); err != nil {
				t.Fatal(err)
			}
			insert := `INSERT INTO compliance.settings(singleton,value) VALUES(true,'{"preserved":"pilot"}')`
			if lineage == "main" {
				insert = `INSERT INTO compliance.settings(id,value) VALUES(1,'{"preserved":"main"}')`
			}
			if _, err = pool.Exec(ctx, insert); err != nil {
				t.Fatal(err)
			}
			if err = migrate.Up(ctx, pool, nil); err != nil {
				t.Fatal(err)
			}
			if err = migrate.Up(ctx, pool, nil); err != nil {
				t.Fatal("repeat migration:", err)
			}
			var got string
			if err = pool.QueryRow(ctx, `SELECT value->>'preserved' FROM compliance.settings WHERE singleton`).Scan(&got); err != nil || got != lineage {
				t.Fatalf("settings lost: %q %v", got, err)
			}
			if _, err = pool.Exec(ctx, `INSERT INTO compliance.settings(singleton,value) VALUES(true,'{"updated":true}') ON CONFLICT(singleton) DO UPDATE SET value=EXCLUDED.value`); err != nil {
				t.Fatal(err)
			}
			var present bool
			if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='compliance' AND table_name='baselines' AND column_name='components')`).Scan(&present); err != nil || !present {
				t.Fatalf("components missing: %v", err)
			}
		})
	}
}
