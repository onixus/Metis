package observability_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onixus/metis/internal/observability"
)

func TestNFO04_TracingInitialisesForEveryExporter(t *testing.T) {
	for _, exp := range []string{"none", "", "stdout"} {
		shutdown, err := observability.Tracing(context.Background(), "metis-test", "0", exp)
		if err != nil {
			t.Fatalf("экспортер %q: %v", exp, err)
		}
		if err := shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown %q: %v", exp, err)
		}
	}
	if _, err := observability.Tracing(context.Background(), "m", "0", "weird"); err == nil {
		t.Fatal("неизвестный экспортер должен давать ошибку")
	}
}

func TestNFO04_MetricsHandlerServes(t *testing.T) {
	m := observability.NewMetrics("metis")
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics: %d", rec.Code)
	}
}
