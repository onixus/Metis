package confluence_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onixus/metis/internal/adapters/confluence"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

func TestDA01_NFR06_ConfluenceRecoversCreateAcrossClientRestart(t *testing.T) {
	for _, failure := range []string{"labels", "lost-response", "properties"} {
		t.Run(failure, func(t *testing.T) {
			var page map[string]any
			creates, labels, props := 0, 0, 0
			failed := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/rest/api/content":
					if r.URL.Query().Get("title") != "Metis request-001" {
						t.Error("mutable lookup title")
					}
					results := []map[string]any{}
					if page != nil {
						results = append(results, page)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
				case r.Method == http.MethodPost && r.URL.Path == "/rest/api/content":
					creates++
					if err := json.NewDecoder(r.Body).Decode(&page); err != nil {
						t.Error(err)
					}
					page["id"] = "2001"
					if failure == "lost-response" && !failed {
						failed = true
						w.WriteHeader(500)
						return
					}
					_ = json.NewEncoder(w).Encode(page)
				case strings.HasSuffix(r.URL.Path, "/label"):
					labels++
					if failure == "labels" && !failed {
						failed = true
						w.WriteHeader(503)
						return
					}
					_, _ = fmt.Fprint(w, `{}`)
				case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/property/"):
					w.WriteHeader(404)
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/property"):
					props++
					if failure == "properties" && !failed {
						failed = true
						w.WriteHeader(503)
						return
					}
					_, _ = fmt.Fprint(w, `{}`)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			input := ports.CreatePageInput{IdempotencyKey: "request-001", SpaceKey: "METIS", Title: "Readable title", Body: "<h1>Readable title</h1>", Labels: []string{"metis", "adr"}, Properties: map[string]string{"metis_decision_id": "request-001"}}
			c, err := confluence.New(srv.URL, confluence.NewStaticToken("synthetic"), srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.CreatePage(context.Background(), input)
			if failure != "lost-response" && !errors.Is(err, kernel.ErrUnavailable) {
				t.Fatalf("first error: %v", err)
			}
			// New client models a restart and a local transaction rollback: no PageID survives.
			c, err = confluence.New(srv.URL, confluence.NewStaticToken("synthetic"), srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			input.Title = "Changed while pending"
			got, err := c.CreatePage(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != "2001" || creates != 1 || labels < 2 || props < 1 || got.Properties["metis_decision_id"] != "request-001" {
				t.Fatalf("recovery: %+v creates=%d labels=%d props=%d", got, creates, labels, props)
			}
		})
	}
}

func TestDA01_NFS17_ConfluenceDoesNotAdoptForeignMatchingTitle(t *testing.T) {
	creates := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			creates++
			w.WriteHeader(500)
			return
		}
		_, _ = fmt.Fprint(w, `{"results":[{"id":"77","title":"Metis request-001","space":{"key":"METIS"},"body":{"storage":{"value":"<p>Someone else's page</p>"}}}]}`)
	}))
	defer srv.Close()
	c, err := confluence.New(srv.URL, confluence.NewStaticToken("synthetic"), srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreatePage(context.Background(), ports.CreatePageInput{IdempotencyKey: "request-001", SpaceKey: "METIS", Title: "title"})
	if !errors.Is(err, kernel.ErrConflict) || creates != 0 {
		t.Fatalf("foreign page adopted: %v writes=%d", err, creates)
	}
}
