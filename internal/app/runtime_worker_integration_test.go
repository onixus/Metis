//go:build integration

package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onixus/metis/internal/commitments"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/roadmap"
)

func postgresWorker(t *testing.T, cfg Config) *App {
	t.Helper()
	a, err := BuildWorker(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(context.Background()) })
	return a
}

func TestNFR06_PG08_CT03_PostgresWorkerSeesNewAPIStateAndDispatches(t *testing.T) {
	a, reader, _ := runtimeApps(t)
	worker := postgresWorker(t, a.Cfg)
	ctx, sc := context.Background(), identityaccess.ServiceScope("runtime-worker-test")
	var feature portfoliograph.Feature
	var item roadmap.RoadmapItem
	var obligation commitments.Commitment
	if err := a.runOperation(ctx, func(ctx context.Context) error {
		p, err := a.Portfolio.CreateProduct(ctx, sc, portfoliograph.ProductInput{Key: "worker-" + kernel.NewID().String(), Name: "Synthetic worker portfolio", Type: portfoliograph.ProductTypeSecurity})
		if err != nil {
			return err
		}
		feature, err = a.Portfolio.CreateFeature(ctx, sc, p.ID, portfoliograph.FeatureInput{Name: "Synthetic worker feature", PlannedDate: kernel.DateOf(2026, 10, 1)})
		if err != nil {
			return err
		}
		item, err = a.Roadmap.CreateItem(ctx, sc, p.ID, roadmap.ItemInput{FeatureID: feature.ID, Title: "Synthetic roadmap", Bucket: roadmap.BucketNow, EndDate: feature.PlannedDate})
		if err != nil {
			return err
		}
		obligation, err = a.Commitments.Create(ctx, sc, p.ID, commitments.Input{Kind: commitments.KindCustomer, Counterparty: "synthetic-account", Subject: "Synthetic availability", DueDate: kernel.DateOf(2026, 10, 15), Basis: "Acceptance", Owner: "synthetic-owner", FeatureID: feature.ID})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	newDate := kernel.DateOf(2026, 11, 1)
	if err := a.runOperation(ctx, func(ctx context.Context) error {
		_, err := a.Portfolio.ShiftFeatureDate(ctx, sc, feature.ID, newDate, "Synthetic PG delay")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	drainWorker(t, worker)
	if err := reader.runOperation(ctx, func(ctx context.Context) error {
		current, err := reader.Portfolio.Feature(ctx, sc, feature.ID)
		if err != nil {
			return err
		}
		if current.PlannedDate != newDate {
			t.Errorf("stale graph date %s", current.PlannedDate)
		}
		items, err := reader.Roadmap.Items(ctx, sc, feature.ProductID)
		if err != nil {
			return err
		}
		if len(items) != 1 || items[0].ID != item.ID || items[0].EndDate != newDate {
			t.Errorf("worker failed roadmap: %v", items)
		}
		alerts, err := reader.Commitments.Alerts(ctx, sc, feature.ProductID, false)
		if err != nil {
			return err
		}
		if len(alerts) == 0 || alerts[0].CommitmentID != obligation.ID {
			t.Errorf("worker failed commitments: %v", alerts)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNFR06_PostgresWorkerReloadsGraphAfterFailedHandlerSavepoint(t *testing.T) {
	a, _, _ := runtimeApps(t)
	cfg := a.Cfg
	cfg.OutboxConfig.MaxAttempts = 1
	cfg.OutboxConfig.BatchSize = 2
	worker := postgresWorker(t, cfg)
	ctx, sc := context.Background(), identityaccess.ServiceScope("runtime-worker-rollback")
	var feature portfoliograph.Feature
	if err := a.runOperation(ctx, func(ctx context.Context) error {
		p, err := a.Portfolio.CreateProduct(ctx, sc, portfoliograph.ProductInput{Key: "worker-rollback-" + kernel.NewID().String(), Name: "Synthetic rollback", Type: portfoliograph.ProductTypeSecurity})
		if err != nil {
			return err
		}
		feature, err = a.Portfolio.CreateFeature(ctx, sc, p.ID, portfoliograph.FeatureInput{Name: "Synthetic feature", PlannedDate: kernel.DateOf(2026, 10, 1)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	failureType, observerType := "test.worker.fail."+feature.ID.String(), "test.worker.observe."+feature.ID.String()
	worker.Worker.Register(failureType, kernel.HandlerFunc(func(ctx context.Context, _ kernel.Event) error {
		if _, err := worker.Portfolio.UpdateFeature(ctx, sc, feature.ID, portfoliograph.FeatureInput{Name: "Rolled back change"}); err != nil {
			return err
		}
		return errors.New("synthetic failure after graph mutation")
	}))
	observed := false
	worker.Worker.Register(observerType, kernel.HandlerFunc(func(ctx context.Context, _ kernel.Event) error {
		current, err := worker.Portfolio.Feature(ctx, sc, feature.ID)
		if err != nil {
			return err
		}
		observed = true
		if current.Name != feature.Name {
			t.Errorf("next handler saw rolled-back graph: %s", current.Name)
		}
		return nil
	}))
	// Claim both fixture messages in one batch, regardless of notifications left
	// by previous runs. The application lock also prevents another worker from
	// consuming them before this worker's test handlers run.
	if err := worker.runOperation(ctx, func(ctx context.Context) error {
		var earliest time.Time
		if err := pgdb.Querier(ctx, worker.db).QueryRow(ctx, "SELECT COALESCE(min(next_attempt_at), now()) FROM kernel.outbox").Scan(&earliest); err != nil {
			return err
		}
		first := earliest.Add(-time.Second)
		for i, typ := range []string{failureType, observerType} {
			ev, err := kernel.NewEvent(kernel.SystemClock{}, typ, feature.ID, feature.ProductID, sc.Subject(), nil)
			if err != nil {
				return err
			}
			if err := worker.Outbox.Enqueue(ctx, first.Add(time.Duration(i)*time.Microsecond), ev); err != nil {
				return err
			}
		}
		count, err := worker.Worker.RunOnce(ctx)
		if err != nil {
			return err
		}
		if count != 2 {
			t.Errorf("fixture batch count=%d, want 2", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !observed {
		t.Fatal("observer event was not delivered")
	}
	current, err := worker.Portfolio.Feature(ctx, sc, feature.ID)
	if err != nil || current.Name != feature.Name {
		t.Fatalf("worker retained rolled-back graph: %+v %v", current, err)
	}
}
