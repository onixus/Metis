package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onixus/metis/internal/delivery"
	"github.com/onixus/metis/internal/httpapi/gen"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/ports"
)

func TestDL04_HTTPForecastExposesHistoryAndStaleness(t *testing.T) {
	ctx := context.Background()
	clock := kernel.FixedClock{T: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	sc := identityaccess.ServiceScope("forecast-fixture")
	portfolio := portfoliograph.NewService(portfoliograph.NewMemStore(), nopPub{}, clock)
	product, err := portfolio.CreateProduct(ctx, sc, portfoliograph.ProductInput{Key: "forecast", Name: "Synthetic forecast", Type: portfoliograph.ProductTypeSecurity})
	if err != nil {
		t.Fatal(err)
	}
	feature, err := portfolio.CreateFeature(ctx, sc, product.ID, portfoliograph.FeatureInput{Name: "Synthetic delivery"})
	if err != nil {
		t.Fatal(err)
	}
	store := delivery.NewMemStore()
	if err := store.SaveEpic(ctx, sc, delivery.EpicProjection{FeatureID: feature.ID, ProductID: product.ID, EpicKey: "SYN-1", Issues: []delivery.IssueSnapshot{{Key: "SYN-2"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSprints(ctx, sc, product.ID, []delivery.SprintStatus{{ProductID: product.ID, State: ports.SprintClosed, Done: 1, Total: 1}}); err != nil {
		t.Fatal(err)
	}
	lastSuccess := clock.T.Add(-time.Hour)
	if err := store.SaveSyncState(ctx, sc, delivery.SyncState{LastSuccessAt: lastSuccess, LastAttemptAt: clock.T, LastError: "Synthetic sync unavailable"}); err != nil {
		t.Fatal(err)
	}
	svc := delivery.NewService(store, nil, portfolio, nopPub{}, clock, delivery.Config{})
	srv := NewServer(Deps{Delivery: svc, Instrument: withScope(cpoScope())})
	request := func(query string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		srv.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/features/"+feature.ID.String()+"/forecast"+query, nil))
		return response
	}
	response := request("?samples=100&seed=42")
	if response.Code != http.StatusOK {
		t.Fatalf("forecast %d: %s", response.Code, response.Body.String())
	}
	var forecast gen.Forecast
	if err := json.Unmarshal(response.Body.Bytes(), &forecast); err != nil {
		t.Fatal(err)
	}
	if forecast.CalculatedAt == nil || !forecast.CalculatedAt.Equal(clock.T) {
		t.Fatalf("missing calculation timestamp: %+v", forecast.CalculatedAt)
	}
	if forecast.Sync == nil || !forecast.Sync.Stale || forecast.Sync.LastSuccessAt != lastSuccess || forecast.Sync.LastError == "" || forecast.Throughput == nil || len(*forecast.Throughput) != 1 || forecast.P85 == nil {
		t.Fatalf("missing provenance or estimate: %+v", forecast)
	}
	for _, query := range []string{"?samples=-1", "?samples=100001"} {
		if response := request(query); response.Code != http.StatusBadRequest {
			t.Fatalf("unbounded forecast %d: %s", response.Code, response.Body.String())
		}
	}
}
