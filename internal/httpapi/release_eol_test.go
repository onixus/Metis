package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/roadmap"
)

// TestRM05_ReleaseEOLHTTPNullableClear exercises the actual strict JSON date
// decoder as well as the handler, so clearing support dates cannot regress to 400.
func TestRM05_ReleaseEOLHTTPNullableClear(t *testing.T) {
	ctx, sc := context.Background(), cpoScope()
	svc := roadmap.NewService(roadmap.NewMemStore(), nopPub{}, kernel.SystemClock{})
	release, err := svc.CreateRelease(ctx, sc, kernel.NewID(), roadmap.ReleaseInput{
		Name: "Synthetic release", Version: "1.0", PlannedDate: kernel.DateOf(2026, 11, 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(Deps{Roadmap: svc, Instrument: withScope(sc)}).Handler()
	for _, tc := range []struct {
		name, body string
		status     int
		want       kernel.Date
	}{
		{"set", `{"eol":"2028-11-01"}`, http.StatusOK, kernel.DateOf(2028, 11, 1)},
		{"reject-empty", `{"eol":""}`, http.StatusBadRequest, kernel.DateOf(2028, 11, 1)},
		{"reject-omitted", `{}`, http.StatusBadRequest, kernel.DateOf(2028, 11, 1)},
		{"reject-null-body", `null`, http.StatusBadRequest, kernel.DateOf(2028, 11, 1)},
		{"clear", `{"eol":null}`, http.StatusOK, kernel.Date{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/api/v1/releases/"+release.ID.String()+"/eol", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("HTTP %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			got, err := svc.Release(ctx, sc, release.ID)
			if err != nil || got.EOL != tc.want {
				t.Fatalf("persisted EOL: %v, want %v; err=%v", got.EOL, tc.want, err)
			}
		})
	}
}
