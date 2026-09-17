// Package migrate — программный запуск миграций goose для всех модулей (db/<module>/migrations).
// Каждый модуль ведёт собственную таблицу версий goose_<module> в схеме public, поэтому
// номера миграций разных модулей не пересекаются.
package migrate

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	metisdb "github.com/onixus/metis/db"
)

// migrateLockID — ключ advisory-блокировки миграций.
const migrateLockID int64 = 7_312_2026

// Modules возвращает имена модулей с миграциями в отсортированном порядке.
func Modules() ([]string, error) {
	entries, err := fs.ReadDir(metisdb.Migrations, ".")
	if err != nil {
		return nil, fmt.Errorf("migrate: чтение каталога миграций: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// Up применяет все ожидающие миграции каждого модуля. Логгер может быть nil.
func Up(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	modules, err := Modules()
	if err != nil {
		return err
	}
	// Сессионная advisory-блокировка: несколько реплик (или тестовых пакетов) не мигрируют одновременно.
	lockConn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrate: соединение для блокировки: %w", err)
	}
	defer lockConn.Release()
	if _, err := lockConn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrateLockID); err != nil {
		return fmt.Errorf("migrate: advisory lock: %w", err)
	}
	defer func() {
		if _, uerr := lockConn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrateLockID); uerr != nil {
			log.WarnContext(ctx, "migrate: advisory unlock", slog.String("error", uerr.Error()))
		}
	}()
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() {
		if cerr := sqlDB.Close(); cerr != nil {
			log.WarnContext(ctx, "migrate: закрытие database/sql", slog.String("error", cerr.Error()))
		}
	}()
	for _, m := range modules {
		sub, err := fs.Sub(metisdb.Migrations, m+"/migrations")
		if err != nil {
			return fmt.Errorf("migrate %s: %w", m, err)
		}
		p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, sub, goose.WithTableName("goose_"+m))
		if err != nil {
			return fmt.Errorf("migrate %s: provider: %w", m, err)
		}
		results, err := p.Up(ctx)
		if err != nil {
			return fmt.Errorf("migrate %s: up: %w", m, err)
		}
		for _, r := range results {
			log.InfoContext(ctx, "migrate: применена миграция",
				slog.String("module", m), slog.Int64("version", r.Source.Version), slog.String("path", r.Source.Path))
		}
	}
	return nil
}
