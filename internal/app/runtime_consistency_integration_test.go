//go:build integration

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/migrate"
	"github.com/onixus/metis/internal/kernel/pgdb"
)

const runtimeTestSecret = "metis-runtime-test-secret-32-bytes-only"

func runtimeApps(t *testing.T) (*App, *App, *pgdb.DB) {
	t.Helper()
	raw := os.Getenv("METIS_RUNTIME_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("METIS_RUNTIME_TEST_DATABASE_URL missing (docs/questions.md #05)")
	}
	owner, err := pgdb.Open(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	if err := migrate.Up(context.Background(), owner.Pool(), slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	// Exercise the same non-owner login used by Compose. The isolated database
	// must provision metis_app; migration ownership stays with the test fixture.
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	u.User = url.UserPassword("metis_app", password)
	build := func() *App {
		a, err := Build(context.Background(), Config{Storage: "postgres", DatabaseURL: u.String(), AuthMode: "hmac", HMACSecret: runtimeTestSecret, HMACIssuer: "runtime-test", OTelExport: "none"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = a.Close(context.Background()) })
		return a
	}
	return build(), build(), owner
}

func runtimeRequest(a *App, method, path, body, actor string, roles, products []string) *httptest.ResponseRecorder {
	token, _ := identityaccess.MintHS256([]byte(runtimeTestSecret), "runtime-test", actor, roles, products, "", time.Hour, kernel.SystemClock{})
	req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Handler.ServeHTTP(w, req)
	return w
}

func runtimeProduct(t *testing.T, a *App, key, actor string) string {
	t.Helper()
	w := runtimeRequest(a, http.MethodPost, "/products", fmt.Sprintf(`{"key":%q,"name":"Синтетический продукт ИБ","type":"security","owner":"pilot-pm"}`, key), actor, []string{"cpo"}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("create product: %d %s", w.Code, w.Body.String())
	}
	var product struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &product); err != nil {
		t.Fatal(err)
	}
	return product.ID
}

func TestNFR06_NFS05_RuntimeRollsBackStateOutboxAuditAndGraph(t *testing.T) {
	a, b, owner := runtimeApps(t)
	ctx := context.Background()
	// Inject a real PostgreSQL failure after SaveProduct, without replacing the
	// production Publisher or bypassing HTTP middleware.
	_, err := owner.Pool().Exec(ctx, `CREATE OR REPLACE FUNCTION kernel.reject_runtime_test_event() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN IF NEW.actor = 'reject-outbox' THEN RAISE EXCEPTION 'synthetic outbox failure'; END IF; RETURN NEW; END $$;
	DROP TRIGGER IF EXISTS reject_runtime_test_event ON kernel.outbox;
	CREATE TRIGGER reject_runtime_test_event BEFORE INSERT ON kernel.outbox FOR EACH ROW EXECUTE FUNCTION kernel.reject_runtime_test_event()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = owner.Pool().Exec(ctx, "DROP TRIGGER IF EXISTS reject_runtime_test_event ON kernel.outbox; DROP FUNCTION IF EXISTS kernel.reject_runtime_test_event()")
	})
	key := "rollback-" + kernel.NewID().String()
	w := runtimeRequest(a, "POST", "/products", fmt.Sprintf(`{"key":%q,"name":"Rollback","type":"security"}`, key), "reject-outbox", []string{"cpo"}, nil)
	if w.Code < 500 {
		t.Fatalf("failed publication returned success: %d %s", w.Code, w.Body.String())
	}
	for _, instance := range []*App{a, b} {
		list := runtimeRequest(instance, "GET", "/products", "", "reader", []string{"cpo"}, nil)
		if list.Code != 200 || strings.Contains(list.Body.String(), key) {
			t.Fatalf("uncommitted product survived rollback: %d %s", list.Code, list.Body.String())
		}
	}
	var count int
	if err := owner.Pool().QueryRow(ctx, "SELECT count(*) FROM kernel.outbox WHERE actor = 'reject-outbox'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed outbox state: %d %v", count, err)
	}
	// The same key must be immediately reusable after rollback, including on
	// the process whose in-memory graph had been mutated.
	runtimeProduct(t, a, key, "retry-success")

	_, err = owner.Pool().Exec(ctx, `CREATE OR REPLACE FUNCTION audit.reject_runtime_test_record() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN IF NEW.actor = 'reject-audit' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$;
	DROP TRIGGER IF EXISTS reject_runtime_test_record ON audit.records;
	CREATE TRIGGER reject_runtime_test_record BEFORE INSERT ON audit.records FOR EACH ROW EXECUTE FUNCTION audit.reject_runtime_test_record()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = owner.Pool().Exec(ctx, "DROP TRIGGER IF EXISTS reject_runtime_test_record ON audit.records; DROP FUNCTION IF EXISTS audit.reject_runtime_test_record()")
	})
	key = "audit-rollback-" + kernel.NewID().String()
	w = runtimeRequest(a, "POST", "/products", fmt.Sprintf(`{"key":%q,"name":"Audit rollback","type":"security"}`, key), "reject-audit", []string{"cpo"}, nil)
	if w.Code < 500 {
		t.Fatalf("failed audit returned success: %d %s", w.Code, w.Body.String())
	}
	if err := owner.Pool().QueryRow(ctx, "SELECT count(*) FROM kernel.outbox WHERE actor = 'reject-audit'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("audit rollback left domain event: %d %v", count, err)
	}
	runtimeProduct(t, b, key, "audit-retry-success")
}

func TestPG05_NFS01_RuntimeRefreshesGraphBeforeAuthorizationAndConcurrentWrites(t *testing.T) {
	a, b, _ := runtimeApps(t)
	key := "visibility-" + kernel.NewID().String()
	id := runtimeProduct(t, a, key, "writer")
	// The PM token resolves a product key created after replica b started.
	w := runtimeRequest(b, "POST", "/products/"+id+"/features", `{"name":"Изоляция узла","status":"planned"}`, "pilot-pm", []string{"pm"}, []string{key})
	if w.Code != 201 {
		t.Fatalf("replica resolves stale product scope: %d %s", w.Code, w.Body.String())
	}
	w = runtimeRequest(a, "GET", "/products/"+id+"/features", "", "pilot-pm", []string{"pm"}, []string{key})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Изоляция узла") {
		t.Fatalf("replica did not refresh features: %d %s", w.Code, w.Body.String())
	}
	w = runtimeRequest(b, "GET", "/products/"+id+"/features", "", "foreign-pm", []string{"pm"}, nil)
	if w.Code != 403 {
		t.Fatalf("foreign PM reads private features: %d %s", w.Code, w.Body.String())
	}
	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			instance := a
			if i%2 != 0 {
				instance = b
			}
			body := fmt.Sprintf(`{"key":%q,"name":"Concurrent","type":"security"}`, fmt.Sprintf("%s-%d", key, i))
			w := runtimeRequest(instance, "POST", "/products", body, "concurrent-writer", []string{"cpo"}, nil)
			if w.Code != 201 {
				errs <- fmt.Sprintf("%d %s", w.Code, w.Body.String())
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	result, err := audit.Verify(context.Background(), a.AuditStore)
	if err != nil || !result.OK {
		t.Fatalf("concurrent audit chain: %+v %v", result, err)
	}
}

func TestAD01_NFS05_RuntimeRetainsAuthenticationDenialAfterRollback(t *testing.T) {
	a, _, owner := runtimeApps(t)
	var before, after int
	if err := owner.Pool().QueryRow(context.Background(), "SELECT count(*) FROM audit.records WHERE action = 'auth.denied'").Scan(&before); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/v1/products", nil)
	req.Header.Set("Authorization", "Bearer invalid-synthetic-token")
	w := httptest.NewRecorder()
	a.Handler.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("denied login: %d %s", w.Code, w.Body.String())
	}
	if err := owner.Pool().QueryRow(context.Background(), "SELECT count(*) FROM audit.records WHERE action = 'auth.denied'").Scan(&after); err != nil || after != before+1 {
		t.Fatalf("denial audit lost or duplicated: before=%d after=%d err=%v", before, after, err)
	}
}

func TestPG05_ConcurrentReplicasCannotCreateFeatureCycle(t *testing.T) {
	a, b, _ := runtimeApps(t)
	id := runtimeProduct(t, a, "cycle-"+kernel.NewID().String(), "cycle-writer")
	var features []string
	for _, name := range []string{"Изоляция узла", "Ответ на инцидент"} {
		w := runtimeRequest(a, "POST", "/products/"+id+"/features", fmt.Sprintf(`{"name":%q}`, name), "cycle-writer", []string{"cpo"}, nil)
		if w.Code != 201 {
			t.Fatalf("create feature: %d %s", w.Code, w.Body.String())
		}
		var f struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &f); err != nil {
			t.Fatal(err)
		}
		features = append(features, f.ID)
	}
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 2)
	for i, instance := range []*App{a, b} {
		go func(i int, instance *App) {
			<-start
			body := fmt.Sprintf(`{"type":"integration","from_feature_id":%q,"to_feature_id":%q,"criticality":"blocks"}`, features[i], features[1-i])
			results <- runtimeRequest(instance, "POST", "/links", body, "cycle-writer", []string{"cpo"}, nil)
		}(i, instance)
	}
	close(start)
	created, rejected := 0, 0
	for range 2 {
		w := <-results
		switch w.Code {
		case 201:
			created++
		case 409:
			rejected++
		default:
			t.Fatalf("unexpected link response: %d %s", w.Code, w.Body.String())
		}
	}
	if created != 1 || rejected != 1 {
		t.Fatalf("concurrent inverse edges: created=%d rejected=%d", created, rejected)
	}
}
