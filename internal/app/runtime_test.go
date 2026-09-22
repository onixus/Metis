package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onixus/metis/internal/commitments"
	"github.com/onixus/metis/internal/delivery"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/outbox"
	"github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/roadmap"
)

func runtimeApp(t *testing.T, cfg Config, withHTTP bool) *App {
	t.Helper()
	cfg.Storage, cfg.OTelExport = "memory", "none"
	if cfg.JiraBaseURL != "" {
		cfg.JiraToken = "synthetic-test-token"
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var a *App
	var err error
	if withHTTP {
		cfg.AuthMode, cfg.HMACSecret = "hmac", "synthetic-test-secret-01234567890123456789"
		a, err = Build(context.Background(), cfg, log)
	} else {
		a, err = BuildWorker(context.Background(), cfg, log)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return a
}

func runtimeFeature(t *testing.T, a *App) portfoliograph.Feature {
	t.Helper()
	ctx, sc := context.Background(), identityaccess.ServiceScope("runtime-test")
	p, err := a.Portfolio.CreateProduct(ctx, sc, portfoliograph.ProductInput{Key: "runtime", Name: "Synthetic Security", Type: portfoliograph.ProductTypeSecurity})
	if err != nil {
		t.Fatal(err)
	}
	f, err := a.Portfolio.CreateFeature(ctx, sc, p.ID, portfoliograph.FeatureInput{Name: "Synthetic detection", PlannedDate: kernel.DateOf(2026, 10, 1)})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func drainWorker(t *testing.T, a *App) {
	t.Helper()
	for range 10 {
		n, err := a.Worker.RunOnce(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("outbox did not drain")
}

func TestNFR06_BuildWorkerDispatchesPG08RM03CT03(t *testing.T) {
	a := runtimeApp(t, Config{AuthMode: "oidc", OIDCIssuer: "http://unreachable.invalid", Seed: true}, false)
	if a.Handler != nil || a.Cfg.Seed {
		t.Fatal("worker must not build public HTTP or seed")
	}
	ctx, sc := context.Background(), identityaccess.ServiceScope("runtime-test")
	products, err := a.Portfolio.Products(ctx, sc)
	if err != nil || len(products) != 0 {
		t.Fatalf("worker seeded products: %v %v", products, err)
	}
	f := runtimeFeature(t, a)
	item, err := a.Roadmap.CreateItem(ctx, sc, f.ProductID, roadmap.ItemInput{FeatureID: f.ID, Title: "Detection release", Bucket: roadmap.BucketNow, EndDate: f.PlannedDate})
	if err != nil {
		t.Fatal(err)
	}
	c, err := a.Commitments.Create(ctx, sc, f.ProductID, commitments.Input{Kind: commitments.KindCustomer, Counterparty: "synthetic-account", Subject: "Detection availability", DueDate: kernel.DateOf(2026, 10, 15), Basis: "Synthetic acceptance", Owner: "test-owner", FeatureID: f.ID})
	if err != nil {
		t.Fatal(err)
	}
	newDate := kernel.DateOf(2026, 11, 1)
	if _, err := a.Portfolio.ShiftFeatureDate(ctx, sc, f.ID, newDate, "Synthetic delay"); err != nil {
		t.Fatal(err)
	}
	drainWorker(t, a)
	items, err := a.Roadmap.Items(ctx, sc, f.ProductID)
	if err != nil || len(items) != 1 || items[0].EndDate != newDate {
		t.Fatalf("roadmap shift not delivered: %v %v", items, err)
	}
	history, err := a.Roadmap.DateHistory(ctx, sc, item.ID)
	if err != nil || len(history) != 1 {
		t.Fatalf("history not delivered: %v %v", history, err)
	}
	alerts, err := a.Commitments.Alerts(ctx, sc, f.ProductID, false)
	if err != nil || len(alerts) == 0 || alerts[0].CommitmentID != c.ID {
		t.Fatalf("commitment alert not delivered: %v %v", alerts, err)
	}
	if dead, err := a.Worker.DLQCount(ctx); err != nil || dead != 0 {
		t.Fatalf("known notifications reached DLQ: %d %v", dead, err)
	}
}

func TestNFR06_DisabledIntegrationCommandIsRetainedInDLQ(t *testing.T) {
	a := runtimeApp(t, Config{OutboxConfig: outbox.Config{MaxAttempts: 1}}, false)
	ev, err := kernel.NewEvent(kernel.SystemClock{}, delivery.EventEpicCreateRequested, kernel.NewID(), kernel.NewID(), "synthetic", delivery.EpicCreateRequest{FeatureID: kernel.NewID(), Project: "TEST"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Outbox.Enqueue(context.Background(), time.Now(), ev); err != nil {
		t.Fatal(err)
	}
	drainWorker(t, a)
	dead, err := a.Worker.DLQList(context.Background(), 10)
	if err != nil || len(dead) != 1 || dead[0].Event.ID != ev.ID {
		t.Fatalf("disabled adapter silently discarded command: %v %v", dead, err)
	}
}

func TestDL01_NFR06_WorkerRetriesEpicCreationFromOutbox(t *testing.T) {
	var requests atomic.Int32
	js := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/2/issue" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if requests.Add(1) == 1 {
			http.Error(w, "synthetic transient failure", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"key":"TEST-1"}`)
	}))
	defer js.Close()
	a := runtimeApp(t, Config{JiraBaseURL: js.URL, OutboxConfig: outbox.Config{BaseBackoff: time.Nanosecond}}, false)
	f := runtimeFeature(t, a)
	ctx, sc := context.Background(), identityaccess.ServiceScope("runtime-test")
	if err := a.Delivery.CreateEpicForFeature(ctx, sc, f.ID, "TEST"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatal("command bypassed outbox")
	}
	drainWorker(t, a)
	updated, err := a.Portfolio.Feature(ctx, sc, f.ID)
	if err != nil || updated.ExternalKey != "TEST-1" || requests.Load() != 2 {
		t.Fatalf("epic not created and linked: %+v requests=%d err=%v", updated, requests.Load(), err)
	}
}

func TestDL03_WebhookUsesSeparateMachineCredential(t *testing.T) {
	a := runtimeApp(t, Config{JiraBaseURL: "http://synthetic.invalid", WebhookToken: "synthetic-webhook-token"}, true)
	f := runtimeFeature(t, a)
	if _, err := a.Delivery.MapFeature(context.Background(), identityaccess.ServiceScope("runtime-test"), f.ID, "TEST-1", "TEST"); err != nil {
		t.Fatal(err)
	}
	body := `{"webhookEvent":"jira:issue_updated","issue":{"id":"1","key":"TEST-1","fields":{"issuetype":{"name":"Epic"}}},"changelog":{"id":"2","items":[{"field":"duedate","toString":"2026-11-01"}]}}`
	for _, tc := range []struct {
		token string
		want  int
	}{{"", 403}, {"wrong", 403}, {"synthetic-webhook-token", 202}} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/jira", strings.NewReader(body))
		req.Header.Set("X-Metis-Webhook-Token", tc.token)
		rw := httptest.NewRecorder()
		a.Handler.ServeHTTP(rw, req)
		if rw.Code != tc.want {
			t.Fatalf("machine credential result=%d want=%d body=%s", rw.Code, tc.want, rw.Body.String())
		}
	}
	updated, err := a.Portfolio.Feature(context.Background(), identityaccess.ServiceScope("runtime-test"), f.ID)
	if err != nil || updated.PlannedDate != kernel.DateOf(2026, 11, 1) {
		t.Fatalf("webhook did not apply: %+v %v", updated, err)
	}
	rw := httptest.NewRecorder()
	a.Handler.ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/api/v1/products", nil))
	if rw.Code != 401 {
		t.Fatalf("user API lost authentication: %d", rw.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/jira", strings.NewReader(strings.Repeat("x", (1<<20)+1)))
	req.Header.Set("X-Metis-Webhook-Token", "synthetic-webhook-token")
	rw = httptest.NewRecorder()
	a.Handler.ServeHTTP(rw, req)
	if rw.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized webhook status=%d", rw.Code)
	}
}

func TestNFP05_CT04_BackgroundJobsRunOnStartupAndRepeat(t *testing.T) {
	var reads atomic.Int32
	js := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/2/issue/TEST-1":
			if reads.Add(1) == 1 {
				http.Error(w, "synthetic transient failure", http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, `{"key":"TEST-1","fields":{"summary":"Synthetic","status":{"name":"Open"},"duedate":"2026-12-01"}}`)
		case "/rest/api/2/search":
			_, _ = io.WriteString(w, `{"total":0,"issues":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer js.Close()
	a := runtimeApp(t, Config{JiraBaseURL: js.URL, DeliverySyncInterval: 10 * time.Millisecond, RenewalInterval: 10 * time.Millisecond, OutboxConfig: outbox.Config{PollInterval: 10 * time.Millisecond}}, false)
	f := runtimeFeature(t, a)
	ctx, sc := context.Background(), identityaccess.ServiceScope("runtime-test")
	if _, err := a.Delivery.MapFeature(ctx, sc, f.ID, "TEST-1", "TEST"); err != nil {
		t.Fatal(err)
	}
	c, err := a.Commitments.Create(ctx, sc, f.ProductID, commitments.Input{Kind: commitments.KindRegulatory, Subtype: commitments.SubtypeCertificateExpiry, Counterparty: "synthetic-regulator", Subject: "Renew certificate", DueDate: kernel.DateFromTime(time.Now()).AddDays(30), Basis: "Synthetic certificate", Owner: "test-owner"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.RunWorker(ctx) }()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	complete := false
	for !complete {
		select {
		case <-ctx.Done():
			t.Fatal("background jobs did not run on startup and retry")
		case <-ticker.C:
			state, syncErr := a.Delivery.SyncState(ctx)
			renewal, renewalErr := a.Commitments.Get(ctx, sc, c.ID)
			complete = syncErr == nil && !state.LastSuccessAt.IsZero() && renewalErr == nil && renewal.RenewalItemID != kernel.NilID && reads.Load() >= 2
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	items, err := a.Roadmap.Items(context.Background(), sc, f.ProductID)
	if err != nil || len(items) != 1 {
		t.Fatalf("renewal duplicated or missing: %v %v", items, err)
	}
}

func TestNFR06_RuntimeConfigRejectsInvalidIntervals(t *testing.T) {
	t.Setenv("METIS_STORAGE", "memory")
	for _, name := range []string{"METIS_OUTBOX_BATCH", "METIS_OUTBOX_MAX_ATTEMPTS", "METIS_DELIVERY_SYNC_INTERVAL", "METIS_RENEWAL_INTERVAL"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "0")
			if _, err := FromEnv(); err == nil {
				t.Fatalf("%s=0 accepted", name)
			}
		})
	}
}
