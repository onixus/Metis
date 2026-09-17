//go:build integration

package pgstore_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/audit/pgstore"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/migrate"
	"github.com/onixus/metis/internal/kernel/pgdb"
)

func openTestDB(t *testing.T) *pgdb.DB {
	t.Helper()
	url := os.Getenv("METIS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("METIS_TEST_DATABASE_URL не задан: интеграционный тест пропущен (docs/questions.md №05)")
	}
	ctx := context.Background()
	db, err := pgdb.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrate.Up(ctx, db.Pool(), nil); err != nil {
		t.Fatal(err)
	}
	// TRUNCATE не проходит через триггер строк, но журнал тестовой БД чистится от владельца.
	if _, err := db.Pool().Exec(ctx, "TRUNCATE audit.records"); err != nil {
		t.Fatal(err)
	}
	return db
}

func fill(t *testing.T, db *pgdb.DB, n int) *audit.Logger {
	t.Helper()
	logger := audit.NewLogger(pgstore.New(db), pgstore.Clock{Inner: kernel.SystemClock{}})
	for i := 0; i < n; i++ {
		_, err := logger.Append(context.Background(), audit.Entry{
			Actor: "tester", Action: audit.ActionGraphChange, ObjectType: "feature", ObjectID: kernel.NewID().String(),
			ProductID: kernel.NewID(), Details: map[string]any{"i": i, "note": "тест <b>", "nested": map[string]any{"x": 1.5}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return logger
}

func TestNFS05_AuditTableRejectsUpdateDelete(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	fill(t, db, 2)
	if _, err := db.Pool().Exec(ctx, "UPDATE audit.records SET actor = 'x' WHERE seq = 1"); err == nil {
		t.Fatal("UPDATE audit.records должен быть отклонён триггером")
	}
	if _, err := db.Pool().Exec(ctx, "DELETE FROM audit.records WHERE seq = 1"); err == nil {
		t.Fatal("DELETE FROM audit.records должен быть отклонён триггером")
	}
	res, err := audit.Verify(ctx, pgstore.New(db))
	if err != nil || !res.OK || res.Checked != 2 {
		t.Fatalf("Verify после отклонённых изменений: %+v err=%v", res, err)
	}
}

func TestAD04_PGAuditChainVerify(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	fill(t, db, 5)
	store := pgstore.New(db)
	res, err := audit.Verify(ctx, store)
	if err != nil || !res.OK || res.Checked != 5 {
		t.Fatalf("Verify: %+v err=%v", res, err)
	}
	// Сценарий приёмки 7.7 п.8: ручное изменение записи суперпользователем (в обход триггера).
	if _, err := db.Pool().Exec(ctx, "ALTER TABLE audit.records DISABLE TRIGGER records_immutable"); err != nil {
		t.Skipf("нет прав отключить триггер (нужен владелец таблицы): %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool().Exec(context.Background(), "ALTER TABLE audit.records ENABLE TRIGGER records_immutable")
	})
	if _, err := db.Pool().Exec(ctx, "UPDATE audit.records SET details = details || '{\"i\": 99}' WHERE seq = 3"); err != nil {
		t.Fatal(err)
	}
	res, err = audit.Verify(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.BrokenSeq != 3 {
		t.Fatalf("Verify должен указать на запись 3: %+v", res)
	}
}

func TestAD04_PGStoreRejectsSubMicrosecondTime(t *testing.T) {
	db := openTestDB(t)
	r := audit.Record{Seq: 1, At: time.Date(2026, 9, 17, 0, 0, 0, 1, time.UTC), Actor: "a", Action: "b", PrevHash: audit.GenesisHash, Hash: audit.GenesisHash}
	if err := pgstore.New(db).Insert(context.Background(), r); err == nil {
		t.Fatal("Insert с наносекундами должен быть отклонён")
	}
}
