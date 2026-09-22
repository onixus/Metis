package app

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/onixus/metis/internal/commitments"
	"github.com/onixus/metis/internal/compliance"
	"github.com/onixus/metis/internal/decisions"
	"github.com/onixus/metis/internal/delivery"
	"github.com/onixus/metis/internal/discovery"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/outbox"
	"github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/prioritization"
	"github.com/onixus/metis/internal/roadmap"
	"github.com/onixus/metis/internal/signals"
)

func envPositiveInt(name string, fallback int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("%w: %s должен быть положительным целым числом", kernel.ErrValidation, name)
	}
	return v, nil
}

func envPositiveDuration(name string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	v, err := time.ParseDuration(raw)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("%w: %s должен быть положительным интервалом, например 1h", kernel.ErrValidation, name)
	}
	return v, nil
}

// registerNotifications перечисляет уведомления, для которых пока нет внутренних
// потребителей. Команды и события с побочными эффектами в этот список не входят.
// Новый неизвестный тип события остаётся в retry/DLQ до обновления сборки.
func (a *App) registerNotifications() {
	for _, typ := range []string{
		portfoliograph.EventProductCreated, portfoliograph.EventProductDeleted,
		portfoliograph.EventFeatureCreated, portfoliograph.EventLinkCreated,
		portfoliograph.EventLinkDeleted, portfoliograph.EventContractSaved,
		portfoliograph.EventRollupComputed, portfoliograph.EventFeatureValueSet,
		signals.EventSignalIngested, signals.EventSignalTriaged, signals.EventSignalLinked, signals.EventSignalMerged,
		prioritization.EventModelSaved, prioritization.EventInputsSet, prioritization.EventFlagsSet, prioritization.EventDevCostSet,
		roadmap.EventItemSaved, roadmap.EventReleaseSaved, roadmap.EventReleaseReadyForCertification,
		delivery.EventEpicLinked, delivery.EventEpicDueDateChanged,
		discovery.EventHypothesisSaved, discovery.EventInterviewSaved, discovery.EventInsightSaved, discovery.EventEvidenceSaved,
		commitments.EventCommitmentCreated, commitments.EventCommitmentUpdated, commitments.EventCommitmentFulfilled,
		commitments.EventCommitmentCancelled, commitments.EventAlertRaised, commitments.EventAlertAcknowledged, commitments.EventRenewalPlanned,
		compliance.EventTrackStarted, compliance.EventGatePassed, compliance.EventGateFailed,
		compliance.EventEvidenceAppended, compliance.EventImpactSet, compliance.EventBaselineCreated,
		decisions.EventRecordSaved, decisions.EventRecordAccepted, decisions.EventPageCreated,
	} {
		a.Worker.Register(typ, kernel.HandlerFunc(func(context.Context, kernel.Event) error { return nil }))
	}
}

func (a *App) refreshGraph(ctx context.Context) error {
	if a.db == nil {
		return nil
	}
	return a.Portfolio.Load(ctx)
}

// operationStore берёт блокировку приложения до захвата outbox. Все процессы
// используют одинаковый порядок блокировок: приложение, затем сообщения.
type operationStore struct {
	outbox.Store
	app *App
}

func (s operationStore) Process(ctx context.Context, now time.Time, limit int, fn func(context.Context, outbox.Message) outbox.Outcome) (int, error) {
	var count int
	err := s.app.runOperation(ctx, func(ctx context.Context) error {
		var err error
		count, err = s.Store.Process(ctx, now, limit, func(ctx context.Context, msg outbox.Message) outbox.Outcome {
			if err := s.app.refreshGraph(ctx); err != nil {
				return outbox.Outcome{Kind: outbox.Retry, NextAttemptAt: now.Add(time.Second), Error: err.Error()}
			}
			return fn(ctx, msg)
		})
		if err != nil {
			return err
		}
		// Не оставляем изменения графа из обработчика, чей savepoint откатился.
		return s.app.refreshGraph(ctx)
	})
	return count, err
}

// RunWorker исполняет outbox и периодическую сверку. Задания запускаются сразу
// после старта, затем по интервалу; ошибки сохраняются в логах и повторяются.
func (a *App) RunWorker(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Worker.Run(ctx) }()
	defer func() {
		cancel()
		<-done
	}()
	syncInterval := a.Cfg.DeliverySyncInterval
	if syncInterval <= 0 {
		syncInterval = time.Hour
	}
	renewalInterval := a.Cfg.RenewalInterval
	if renewalInterval <= 0 {
		renewalInterval = 24 * time.Hour
	}
	syncTimer := time.NewTimer(0)
	defer syncTimer.Stop()
	renewalTimer := time.NewTimer(0)
	defer renewalTimer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-syncTimer.C:
			if a.Jira != nil {
				a.syncDelivery(ctx)
			}
			syncTimer.Reset(syncInterval)
		case <-renewalTimer.C:
			a.runBackground(ctx, "commitments.renewals", func(ctx context.Context) error {
				_, err := a.Commitments.EnsureRenewals(ctx, identityaccess.ServiceScope("renewals"), kernel.DateFromTime(time.Now()))
				return err
			})
			renewalTimer.Reset(renewalInterval)
		}
	}
}

func (a *App) syncDelivery(ctx context.Context) {
	var attemptedAt time.Time
	if err := a.runOperation(ctx, func(ctx context.Context) error {
		attemptedAt = time.Now().UTC()
		return a.Delivery.Sync(ctx)
	}); err != nil && ctx.Err() == nil {
		a.Log.ErrorContext(ctx, "фоновая сверка delivery завершилась с ошибкой", "err", err)
		if attemptedAt.IsZero() {
			return
		}
		if saveErr := a.runOperation(ctx, func(ctx context.Context) error { return a.Delivery.RecordSyncFailure(ctx, attemptedAt, err) }); saveErr != nil {
			a.Log.ErrorContext(ctx, "не удалось сохранить состояние неудачной сверки", "err", saveErr)
		}
	}
}

func (a *App) runBackground(ctx context.Context, name string, fn func(context.Context) error) {
	if err := a.runOperation(ctx, fn); err != nil && ctx.Err() == nil {
		a.Log.ErrorContext(ctx, "фоновое задание завершилось с ошибкой", "job", name, "err", err)
	}
}
