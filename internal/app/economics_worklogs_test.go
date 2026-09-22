package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onixus/metis/internal/delivery"
	"github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/ports"
)

type financeTracker struct {
	ports.DeliveryTracker
	logs   []ports.Worklog
	err    error
	since  time.Time
	issues []string
}

func (f *financeTracker) Worklogs(_ context.Context, issues []string, since time.Time) ([]ports.Worklog, error) {
	f.issues = issues
	f.since = since
	return f.logs, f.err
}

func worklogApp(t *testing.T) (*App, *financeTracker, economics.Snapshot, kernel.ID) {
	t.Helper()
	a := runtimeApp(t, Config{}, false)
	ctx := context.Background()
	sc := identityaccess.ServiceScope("synthetic-setup")
	f1 := runtimeFeature(t, a)
	p2, err := a.Portfolio.CreateProduct(ctx, sc, portfoliograph.ProductInput{Key: "worklog-target", Name: "Synthetic target", Type: portfoliograph.ProductTypeSecurity})
	if err != nil {
		t.Fatal(err)
	}
	f2, err := a.Portfolio.CreateFeature(ctx, sc, p2.ID, portfoliograph.FeatureInput{Name: "Synthetic work"})
	if err != nil {
		t.Fatal(err)
	}
	store := delivery.NewMemStore()
	now := time.Now().UTC()
	for i, f := range []portfoliograph.Feature{f1, f2} {
		key := []string{"SRC-1", "DST-1"}[i]
		issue := []string{"SRC-2", "DST-2"}[i]
		if err := store.SaveMapping(ctx, delivery.Mapping{FeatureID: f.ID, ProductID: f.ProductID, EpicKey: key}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveEpic(ctx, delivery.EpicProjection{FeatureID: f.ID, ProductID: f.ProductID, EpicKey: key, SyncedAt: now, Issues: []delivery.IssueSnapshot{{Key: issue}}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveSyncState(ctx, delivery.SyncState{LastSuccessAt: now}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	tracker := &financeTracker{logs: []ports.Worklog{{ExternalID: "one", IssueKey: "SRC-2", Author: "opaque-1", Started: start, Spent: time.Hour}, {ExternalID: "two", IssueKey: "DST-2", Author: "opaque-1", Started: start.Add(time.Hour), Spent: 2 * time.Hour}}}
	a.Tracker = tracker
	a.Delivery = delivery.NewService(store, tracker, a.Portfolio, nil, kernel.SystemClock{}, delivery.Config{})
	file := filepath.Join(t.TempDir(), "worklog-teams.json")
	if err := os.WriteFile(file, []byte(`{"author_team":{"opaque-1":"team-platform"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	a.Cfg.WorklogTeamsFile = file
	snapshot, err := a.Economics.Save(ctx, identityaccess.FinanceServiceScope("synthetic-finance"), economics.SaveInput{ProductID: f1.ProductID, Period: "2026-09", Currency: "RUB", Source: "synthetic", SourceHash: "original",
		Rows: []economics.Row{{ProductID: f1.ProductID, Category: economics.Payroll, Amount: kernel.RUB(101), TeamID: "team-platform", Headcount: 5, Source: economics.Source{File: "payroll.csv", Row: 2, Hash: "raw"}},
			{ProductID: p2.ID, Category: economics.Revenue, Amount: kernel.RUB(1000)}}})
	if err != nil {
		t.Fatal(err)
	}
	return a, tracker, snapshot, p2.ID
}

func TestDL05_EC12_WorklogsCreateExactPeriodAllocationVersion(t *testing.T) {
	a, tracker, first, p2 := worklogApp(t)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("synthetic-finance")
	// Out-of-period records are neither allocated nor allowed to trigger irrelevant author failures.
	tracker.logs = append(tracker.logs, ports.Worklog{ExternalID: "before", IssueKey: "SRC-2", Author: "unmapped-before", Started: time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC), Spent: 100 * time.Hour},
		ports.Worklog{ExternalID: "after", IssueKey: "SRC-2", Author: "unmapped-after", Started: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Spent: 100 * time.Hour})
	second, err := a.ApplyFinanceWorklogs(ctx, sc, first.ProductID, first.Period, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Version != 2 || second.Rows[0].Amount.Amount != 101 || second.Rows[0].Source != first.Rows[0].Source || second.SourceHash != "original" || second.Rows[0].ID != first.Rows[0].ID || second.Rows[0].AllocationSource != "worklogs" {
		t.Fatalf("facts overwritten: %+v", second)
	}
	if len(second.Rows[0].Allocations) != 2 || tracker.since != time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) || len(tracker.issues) != 4 {
		t.Fatalf("wrong worklog query or allocations: %+v %+v", tracker, second.Rows[0])
	}
	report, err := a.Economics.Report(ctx, sc, economics.ReportInput{ProductID: first.ProductID, Period: first.Period, FilterProductID: p2})
	if err != nil || report.Total.DirectCost != 67 {
		t.Fatalf("target allocated cost: %+v %v", report.Total, err)
	}
	old, err := a.Economics.Snapshot(ctx, sc, first.ProductID, first.Period, 1)
	if err != nil || !reflect.DeepEqual(old, first) {
		t.Fatalf("old version changed: %+v %v", old, err)
	}
	// Changed/deleted authoritative worklogs replace the rule instead of accumulating old hours.
	tracker.logs = []ports.Worklog{{ExternalID: "replacement", IssueKey: "DST-2", Author: "opaque-1", Started: tracker.since, Spent: time.Hour}}
	third, err := a.ApplyFinanceWorklogs(ctx, sc, first.ProductID, first.Period, 2, false)
	if err != nil || len(third.Rows[0].Allocations) != 1 || third.Rows[0].Allocations[0].ProductID != p2 {
		t.Fatalf("replacement failed: %+v %v", third, err)
	}
}

func TestDL05_NFS16_UnknownAuthorsFailWithoutIdentityOrFactChanges(t *testing.T) {
	a, tracker, first, _ := worklogApp(t)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("synthetic-finance")
	tracker.logs[0].Author = "sensitive-author-do-not-expose"
	if _, err := a.ApplyFinanceWorklogs(ctx, sc, first.ProductID, first.Period, 1, false); !errors.Is(err, kernel.ErrValidation) || strings.Contains(err.Error(), tracker.logs[0].Author) {
		t.Fatalf("unknown author failure leaked identity: %v", err)
	}
	current, err := a.Economics.Snapshot(ctx, sc, first.ProductID, first.Period, 0)
	if err != nil || !reflect.DeepEqual(first, current) {
		t.Fatalf("failed worklogs changed facts: %+v %v", current, err)
	}
	if _, err := a.ApplyFinanceWorklogs(ctx, identityaccess.ServiceScope("ordinary"), first.ProductID, first.Period, 1, false); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("ordinary connector finance access: %v", err)
	}
}

func TestDL05_EC11_WorklogsRejectConflictsAndClosedPeriods(t *testing.T) {
	a, tracker, first, _ := worklogApp(t)
	ctx := context.Background()
	sc := identityaccess.FinanceServiceScope("synthetic-finance")
	duplicate := tracker.logs[0]
	duplicate.Spent++
	tracker.logs = append(tracker.logs, duplicate)
	if _, err := a.ApplyFinanceWorklogs(ctx, sc, first.ProductID, first.Period, 1, false); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("conflicting worklog accepted: %v", err)
	}
	tracker.logs = tracker.logs[:2]
	closed, err := a.Economics.Close(ctx, sc, first.ProductID, first.Period, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ApplyFinanceWorklogs(ctx, sc, first.ProductID, first.Period, closed.Version, false); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("closed period overwritten: %v", err)
	}
	if updated, err := a.ApplyFinanceWorklogs(ctx, sc, first.ProductID, first.Period, closed.Version, true); err != nil || !updated.Closed {
		t.Fatalf("explicit closed recalculation: %+v %v", updated, err)
	}
}
