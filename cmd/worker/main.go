// Команда worker собирает домены платформы, доставляет outbox и выполняет сверку.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/onixus/metis/internal/app"
	"github.com/onixus/metis/internal/observability"
)

func main() {
	log := observability.Logger(slog.LevelInfo)
	if err := run(log); err != nil {
		log.Error("worker остановлен с ошибкой", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := app.FromEnv()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	a, err := app.BuildWorker(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.Close(closeCtx); err != nil {
			log.Error("закрытие worker", "err", err)
		}
	}()
	log.Info("воркер запущен: outbox, сверка delivery, продление сертификатов", "storage", cfg.Storage)
	if err := a.RunWorker(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
