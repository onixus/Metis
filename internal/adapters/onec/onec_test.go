package onec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onixus/metis/internal/kernel"
)

const syntheticProduct = "11111111-1111-4111-8111-111111111111"

func config(base string) Config {
	return Config{BaseURL: base + "/odata/standard.odata", Collection: "PublishedTeamFinance", SourceName: "zup-aggregate", Fields: map[string]string{"product_id": "ProductRef", "period": "Month", "amount": "Total", "team_id": "Team", "headcount": "Headcount", "description": "Note"}, ProductIDs: map[string]kernel.ID{"external-product": mustID()}, Currency: "RUB", Category: "payroll", PeriodFormat: "date"}
}

func mustID() kernel.ID { id, _ := kernel.ParseID(syntheticProduct); return id }

func TestEC01_OneCExplicitMappingPagingAndTeamPayroll(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		user, pass, ok := r.BasicAuth()
		if !ok || user != "synthetic-service" || pass != "synthetic-password" {
			t.Error("missing Basic auth")
		}
		if r.Method != http.MethodGet || r.URL.Path != "/odata/standard.odata/PublishedTeamFinance" {
			t.Error("unexpected mutation or path")
		}
		if r.URL.Query().Get("$skiptoken") == "next" {
			writeJSON(t, w, map[string]any{"d": map[string]any{"results": []any{map[string]any{"ProductRef": "external-product", "Month": "2026-09-02T00:00:00", "Total": json.Number("100.50"), "Team": "team-b", "Headcount": 6, "Note": "second"}}}})
			return
		}
		if !strings.Contains(r.URL.Query().Get("$select"), "Total") || r.URL.Query().Get("$format") != "json" {
			t.Error("missing explicit projection")
		}
		writeJSON(t, w, map[string]any{"d": map[string]any{"results": []any{map[string]any{"ProductRef": "external-product", "Month": "2026-09-01T00:00:00", "Total": "1200.25", "Team": "team-a", "Headcount": 5, "Note": "line1\nline2"}}, "__next": "?$skiptoken=next"}})
	}))
	defer server.Close()
	client, err := New(config(server.URL), NewStaticCredentials("synthetic-service", "synthetic-password"), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	p, err := client.Read(context.Background())
	if err != nil || len(p.Rows) != 2 || len(p.Errors) != 0 {
		t.Fatalf("preview: %+v %v", p, err)
	}
	if requests.Load() != 2 || p.Rows[0].Amount != kernel.RUB(120025) || p.Rows[1].Amount != kernel.RUB(10050) || p.Rows[0].Period != "2026-09" || p.Rows[0].Headcount != 5 {
		t.Fatalf("rows: %+v", p.Rows)
	}
	if p.Rows[0].Source.File != "zup-aggregate" || len(p.SourceHashes) != 1 || p.Rows[0].Source.Hash != p.SourceHashes[0] || p.Rows[1].Source.Row != 2 || len(p.SourceHash) != 64 {
		t.Fatalf("lineage: %+v", p.Rows)
	}
}

func TestEC07_OneCUnknownProductAndIndividualPayrollRowErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"value": []any{map[string]any{"ProductRef": "unmapped-private-reference", "Month": "2026-09-01", "Total": "1", "Headcount": 1}}})
	}))
	defer server.Close()
	client, err := New(config(server.URL), NewStaticCredentials("synthetic", "synthetic"), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	p, err := client.Read(context.Background())
	if err != nil || len(p.Rows) != 0 || len(p.Errors) < 2 {
		t.Fatalf("invalid aggregate accepted: %+v %v", p, err)
	}
	for _, e := range p.Errors {
		if strings.Contains(e.Message, "unmapped-private-reference") || e.Row != 1 {
			t.Fatalf("unsafe diagnostics: %+v", e)
		}
	}
}

func TestEC07_OneCMappingFingerprintChangesWithoutLosingSourceHash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"value": []any{map[string]any{"ProductRef": "external-product", "Month": "2026-09-01", "Total": "100", "Headcount": 6, "Team": "synthetic-team"}}})
	}))
	defer server.Close()
	c1 := config(server.URL)
	first, err := New(c1, NewStaticCredentials("synthetic", "synthetic"), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	c2 := config(server.URL)
	c2.Category = "revenue"
	second, err := New(c2, NewStaticCredentials("synthetic", "synthetic"), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	p1, err := first.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p2, err := second.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p1.SourceHash == p2.SourceHash || p1.Rows[0].Source.Hash != p2.Rows[0].Source.Hash || p1.SourceHashes[0] != p2.SourceHashes[0] {
		t.Fatal("mapping change ignored or source lineage altered")
	}
}

func TestNFS14_OneCContinuationNeverLeaksCredentials(t *testing.T) {
	var leaked atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer other.Close()
	for _, next := range []string{other.URL + "/capture", "/different-collection", "//invalid.example/capture", "?x=1#fragment"} {
		t.Run(next, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, map[string]any{"d": map[string]any{"results": []any{}, "__next": next}})
			}))
			defer server.Close()
			client, err := New(config(server.URL), NewStaticCredentials("synthetic", "must-not-leak"), server.Client())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Read(context.Background()); !errors.Is(err, kernel.ErrValidation) {
				t.Fatalf("unsafe pagination accepted: %v", err)
			}
		})
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/capture", http.StatusFound)
	}))
	defer redirect.Close()
	client, err := New(config(redirect.URL), NewStaticCredentials("synthetic", "must-not-leak"), redirect.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Read(context.Background()); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("redirect followed: %v", err)
	}
	if leaked.Load() != 0 {
		t.Fatalf("credentials reached another origin: %d", leaked.Load())
	}
}

func TestNFR06_OneCPaginationCycleAndMalformedContinuation(t *testing.T) {
	for _, next := range []any{"?$skiptoken=repeat", 123} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, map[string]any{"d": map[string]any{"results": []any{}, "__next": next}})
		}))
		client, err := New(config(server.URL), NewStaticCredentials("synthetic", "synthetic"), server.Client())
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Read(context.Background())
		server.Close()
		if !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("invalid pagination accepted: %v", err)
		}
	}
}

func TestNFS14_OneCBoundedResponsesAndSafeErrors(t *testing.T) {
	for _, response := range []string{strings.Repeat("x", maxResponseBytes+1), `{"d":{"results":[]}} {"secret":"do-not-echo"}`, `{"error":"synthetic-secret"}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(response)) }))
		client, err := New(config(server.URL), NewStaticCredentials("synthetic", "synthetic-secret"), server.Client())
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Read(context.Background())
		server.Close()
		if !errors.Is(err, kernel.ErrValidation) || strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
	c := NewStaticCredentials("synthetic", "synthetic-secret")
	if strings.Contains(fmt.Sprintf("%v %#v", c, c), "synthetic-secret") {
		t.Fatal("printed credentials")
	}
}

func TestEC01_OneCRequiresExplicitConfiguration(t *testing.T) {
	for _, change := range []func(*Config){
		func(c *Config) { c.BaseURL = "http://business.example.invalid" },
		func(c *Config) { c.Collection = "" }, func(c *Config) { c.Collection = "Register()?$filter=1" }, func(c *Config) { c.Fields["amount"] = "Value/Execute()" }, func(c *Config) { delete(c.Fields, "period") }, func(c *Config) { c.BaseURL = "https://user:password@invalid.example" }, func(c *Config) { c.PeriodFormat = "guess" },
	} {
		c := config("https://invalid.example")
		change(&c)
		if _, err := New(c, NewStaticCredentials("synthetic", "synthetic"), nil); !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("ambiguous configuration accepted: %v", err)
		}
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}
