// Команда worker — воркер outbox: миграции (METIS_MIGRATE=true) и доставка событий.
// Конфигурация только из окружения: METIS_DATABASE_URL, METIS_MIGRATE, METIS_OTEL_EXPORTER,
// METIS_OUTBOX_BATCH, METIS_OUTBOX_MAX_ATTEMPTS.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/migrate"
	"github.com/onixus/metis/internal/kernel/outbox"
	"github.com/onixus/metis/internal/kernel/pgdb"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("worker остановлен с ошибкой", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

type config struct {
	databaseURL string
	migrate     bool
	otel        string
	batch       int
	maxAttempts int
}

func loadConfig() (config, error) {
	c := config{
		databaseURL: os.Getenv("METIS_DATABASE_URL"),
		otel:        os.Getenv("METIS_OTEL_EXPORTER"),
	}
	if c.databaseURL == "" {
		return config{}, fmt.Errorf("%w: METIS_DATABASE_URL не задан", kernel.ErrValidation)
	}
	if v := os.Getenv("METIS_MIGRATE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return config{}, fmt.Errorf("%w: METIS_MIGRATE: %w", kernel.ErrValidation, err)
		}
		c.migrate = b
	}
	var err error
	if c.batch, err = envInt("METIS_OUTBOX_BATCH", 100); err != nil {
		return config{}, err
	}
	if c.maxAttempts, err = envInt("METIS_OUTBOX_MAX_ATTEMPTS", 10); err != nil {
		return config{}, err
	}
	return c, nil
}

func envInt(name string, def int) (int, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%w: %s должен быть положительным числом", kernel.ErrValidation, name)
	}
	return n, nil
}

func run(log *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	// TODO: OpenTelemetry подключается отдельным шагом; пока экспортёр только логируется.
	log.Info("worker запускается", slog.String("otel_exporter", cfg.otel), slog.Bool("migrate", cfg.migrate))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	db, err := pgdb.Open(ctx, cfg.databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	if cfg.migrate {
		if err := migrate.Up(ctx, db.Pool(), log); err != nil {
			return err
		}
		log.Info("миграции применены")
	}

	worker := outbox.NewWorker(outbox.NewPGStore(db), kernel.SystemClock{},
		outbox.Config{BatchSize: cfg.batch, MaxAttempts: cfg.maxAttempts}, log)
	// Обработчики доменных событий регистрируются здесь по мере появления модулей (delivery, roadmap, …).

	log.Info("воркер outbox запущен")
	// Run завершает текущую пачку и возвращает nil после SIGTERM/SIGINT.
	if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	log.Info("воркер остановлен")
	return nil
}
