package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onixus/metis/internal/httpapi"
	"github.com/onixus/metis/internal/kernel/pgdb"
)

type operationKey struct{}

// runOperation is the application transaction boundary. Domain services and their
// stores share its context, so state, audit and outbox commit together. The graph
// is reloaded while every cooperating process holds the same transaction lock.
func (a *App) runOperation(ctx context.Context, fn func(context.Context) error) error {
	if a.db == nil || ctx.Value(operationKey{}) == a {
		return fn(ctx)
	}
	select {
	case a.operationGate <- struct{}{}:
		defer func() { <-a.operationGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	err := a.db.Transact(ctx, func(txCtx context.Context) error {
		if err := pgdb.LockApplication(txCtx, a.db); err != nil {
			return err
		}
		if err := a.Portfolio.Load(txCtx); err != nil {
			return fmt.Errorf("refresh portfolio: %w", err)
		}
		return fn(context.WithValue(txCtx, operationKey{}, a))
	})
	if err != nil {
		// Do not leave a graph mutated by a rolled-back operation in this process.
		// A fresh context must not retain the now-closed transaction from ctx.
		reloadCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if loadErr := a.Portfolio.Load(reloadCtx); loadErr != nil {
			a.Log.Error("не удалось восстановить граф после отката", "err", loadErr)
		}
	}
	return err
}

var errHTTPRejected = errors.New("HTTP operation rejected")

// withConsistency delays the HTTP response until commit succeeds. Failed domain
// operations roll back even when the HTTP adapter has already rendered a problem.
func (a *App) withConsistency(next http.Handler) http.Handler {
	if a.db == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1" && !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		buf := &transactionResponse{header: make(http.Header)}
		// Includes waiting for both the local gate and PostgreSQL lock; the inner
		// HTTP timeout alone starts too late to bound either queue.
		requestCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		requestCtx, authResult := httpapi.WithAuthenticationResult(requestCtx)
		err := a.runOperation(requestCtx, func(ctx context.Context) error {
			next.ServeHTTP(buf, r.WithContext(ctx))
			if buf.status >= http.StatusBadRequest {
				return errHTTPRejected
			}
			return buf.err
		})
		if errors.Is(err, errHTTPRejected) && authResult.Denied != nil {
			// The rejected transaction was rolled back; retain its security event
			// in a separate transaction without retaining any domain mutation.
			auditErr := a.runOperation(r.Context(), func(ctx context.Context) error {
				_, appendErr := a.Audit.Append(ctx, *authResult.Denied)
				return appendErr
			})
			if auditErr != nil {
				err = auditErr
			}
		}
		if err != nil && !errors.Is(err, errHTTPRejected) {
			a.Log.ErrorContext(r.Context(), "транзакция API не завершена", "err", err)
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"type":"about:blank","title":"Операция не завершена. Повторите запрос.","status":503}`))
			return
		}
		for key, values := range buf.header {
			w.Header()[key] = values
		}
		status := buf.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write(buf.body.Bytes())
	})
}

type transactionResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
	err    error
}

func (w *transactionResponse) Header() http.Header { return w.header }

func (w *transactionResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *transactionResponse) Write(body []byte) (int, error) {
	// Bounded buffering, including audit exports, before the transaction commits.
	const maxResponseBytes = 32 << 20
	if w.body.Len()+len(body) > maxResponseBytes {
		w.err = fmt.Errorf("API response exceeds %d bytes", maxResponseBytes)
		return 0, w.err
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(body)
}
