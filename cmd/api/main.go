// Команда api — HTTP API платформы Метида. Конфигурация только из окружения (см. internal/app).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/onixus/metis/internal/app"
	"github.com/onixus/metis/internal/observability"
	"github.com/onixus/metis/internal/webui"
)

func main() {
	log := observability.Logger(slog.LevelInfo)
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("api остановлен с ошибкой", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := app.FromEnv()
	if err != nil {
		return err
	}
	a, err := app.Build(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer func() {
		if err := a.Close(context.Background()); err != nil {
			log.Error("закрытие", "err", err)
		}
	}()

	handler := a.Handler
	if dir := os.Getenv("METIS_WEB_DIR"); dir != "" {
		mode := "oidc"
		if cfg.AuthMode == "hmac" {
			mode = "token"
		}
		handler, err = webui.New(os.DirFS(dir), handler, webui.Config{
			AuthMode: mode, OIDCIssuer: cfg.OIDCIssuer, OIDCClientID: cfg.OIDCClient,
			OIDCScope: "openid profile email",
		})
		if err != nil {
			return err
		}
	}
	srv := &http.Server{
		Addr: cfg.HTTPAddr, Handler: handler,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("api слушает", "addr", cfg.HTTPAddr, "storage", cfg.Storage, "auth", cfg.AuthMode)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	// Воркер outbox внутри api нужен только для стенда в памяти; в поставке события обрабатывает cmd/worker.
	if cfg.Storage == "memory" {
		go func() {
			if err := a.RunWorker(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.Error("воркер outbox", "err", err)
			}
		}()
	}
	// Загрузка финансовых данных по расписанию (EC-01); без METIS_FINANCE_DIR ничего не делает.
	go a.FinanceImportLoop(ctx)
	select {
	case <-ctx.Done():
	case err := <-errCh:
		return err
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
