package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/signals"
)

func TestNFL03_AD05_ProviderSelectionAndValidation(t *testing.T) {
	a := runtimeApp(t, Config{DeliveryProvider: "none", JiraBaseURL: "http://invalid", KnowledgeProvider: "none", ConfluenceBaseURL: "http://invalid", CRMProvider: "none", CRMDir: "invalid"}, false)
	if a.Tracker != nil || a.Knowledge != nil || a.CRM != nil || a.knowledgeSpace() != "" {
		t.Fatal("explicit none did not disable adapters")
	}
	sc, err := identityaccess.NewResolver(a.Portfolio, a.Portfolio).ScopeFor(context.Background(), identityaccess.Claims{Subject: "test-admin", Roles: []string{"admin"}})
	if err != nil {
		t.Fatal(err)
	}
	status, err := a.Delivery.ConnectorStatus(context.Background(), sc)
	if err != nil || status.Name != "none" {
		t.Fatalf("status %+v %v", status, err)
	}
	for _, cfg := range []Config{{DeliveryProvider: "unknown"}, {KnowledgeProvider: "unknown"}, {CRMProvider: "unknown"}, {DeliveryProvider: "jira"}, {KnowledgeProvider: "confluence"}, {CRMProvider: "bitrix24"}, {CRMProvider: "csv"}, {CRMDir: "/tmp/csv", BitrixBaseURL: "http://localhost"}} {
		cfg.Storage = "memory"
		cfg.OTelExport = "none"
		built, err := BuildWorker(context.Background(), cfg, nil)
		if built != nil {
			_ = built.Close(context.Background())
		}
		if !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("invalid config accepted: %v", err)
		}
	}
}

func TestSG01_NFP05_BitrixWorkerImportRunsOnStartupAndRepeats(t *testing.T) {
	var reads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/crm.company.list.json" {
			_, _ = io.WriteString(w, `{"result":[{"ID":"1","TITLE":"Synthetic company"}]}`)
			return
		}
		reads.Add(1)
		_, _ = io.WriteString(w, `{"result":[{"ID":"10","COMPANY_ID":"1","OPPORTUNITY":"100.00","CURRENCY_ID":"RUB","UF_CRM_METIS_PRODUCT":"runtime","COMMENTS":"Synthetic security feature"}]}`)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "fields.json")
	if err := os.WriteFile(path, []byte(`{"deals":{"product_key":"UF_CRM_METIS_PRODUCT"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	a := runtimeApp(t, Config{CRMProvider: "bitrix24", BitrixBaseURL: srv.URL, BitrixToken: "synthetic", BitrixFieldsFile: path, CRMSyncInterval: 10 * time.Millisecond}, false)
	f := runtimeFeature(t, a)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.RunWorker(ctx) }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	complete := false
	for !complete {
		select {
		case <-ctx.Done():
			t.Fatal("scheduled CRM import did not complete")
		case <-ticker.C:
			list, err := a.Signals.Signals(ctx, identityaccess.ServiceScope("test"), f.ProductID, signals.Filter{})
			complete = err == nil && len(list) == 1 && reads.Load() >= 2
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAD05_ConnectorMappingFilesAreStrictAndBounded(t *testing.T) {
	for _, raw := range []string{`{"unknown":"value"}`, `{} {}`, `{"deals":{"product_key":"UF_CRM_METIS_PRODUCT"}} trailing`} {
		path := filepath.Join(t.TempDir(), "mapping.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		a := &App{Cfg: Config{CRMProvider: "bitrix24", BitrixBaseURL: "http://localhost", BitrixToken: "synthetic", BitrixFieldsFile: path}}
		if err := a.configureConnectors(); !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("malformed config accepted: %v", err)
		}
	}
}
