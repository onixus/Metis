// Команда migrate применяет embedded-миграции ролью владельца схем и завершает процесс.
// API и worker после этого используют отдельную роль metis_app без DDL-привилегий.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/onixus/metis/internal/kernel/migrate"
	"github.com/onixus/metis/internal/kernel/pgdb"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Getenv("METIS_DATABASE_URL"), log); err != nil {
		log.Error("миграции не завершены", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, databaseURL string, log *slog.Logger) error {
	if databaseURL == "" {
		return fmt.Errorf("METIS_DATABASE_URL роли миграций обязателен")
	}
	db, err := pgdb.Open(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("подключение для миграций: %w", err)
	}
	defer db.Close()
	if err := migrate.Up(ctx, db.Pool(), log); err != nil {
		return fmt.Errorf("миграции: %w", err)
	}
	log.Info("все миграции применены")
	return nil
}
