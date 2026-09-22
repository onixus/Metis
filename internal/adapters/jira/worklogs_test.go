package jira_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onixus/metis/internal/adapters/jira"
	"github.com/onixus/metis/internal/kernel"
)

func TestDL05_JiraWorklogSnapshotPaginationAndUTC(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/2/issue/MET-1/worklog" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Error("missing token")
		}
		if r.URL.Query().Get("startAt") == "0" {
			_, _ = fmt.Fprint(w, `{"total":2,"worklogs":[{"id":"11","author":{"key":"opaque-key","displayName":"MUST NOT BE USED","emailAddress":"private@example.test"},"started":"2026-08-31T23:30:00.000+0000","updated":"2026-09-02T09:30:00.000+0000","timeSpentSeconds":60}]}`)
			return
		}
		if r.URL.Query().Get("startAt") != "1" {
			t.Error("incorrect cursor")
		}
		_, _ = fmt.Fprint(w, `{"total":2,"worklogs":[{"id":"12","author":{"key":"opaque-key"},"started":"2026-09-01T03:00:00.000+0300","updated":"2026-09-02T12:30:00.000+0300","timeSpentSeconds":3601}]}`)
	}))
	defer srv.Close()
	c, err := jira.New(srv.URL, jira.NewStaticToken("synthetic"), jira.DefaultFieldConfig(), srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	logs, err := c.Worklogs(context.Background(), []string{"MET-1", "MET-1"}, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].ExternalID != "12" || logs[0].Spent != 3601*time.Second || logs[0].Author != "opaque-key" || logs[0].Started.Location() != time.UTC || requests != 2 {
		t.Fatalf("snapshot: %+v requests=%d", logs, requests)
	}
	if !logs[0].UpdatedAt.Equal(time.Date(2026, 9, 2, 9, 30, 0, 0, time.UTC)) {
		t.Fatalf("updated=%v", logs[0].UpdatedAt)
	}
}

func TestDL05_NFS17_JiraRejectsInvalidWorklogWithoutPartialSnapshot(t *testing.T) {
	for name, patch := range map[string]string{
		"missing-id":          `"id":""`,
		"duration-overflow":   `"timeSpentSeconds":9223372036854775807`,
		"negative-duration":   `"timeSpentSeconds":-1`,
		"invalid-time":        `"started":"invalid"`,
		"display-only-author": `"author":{"displayName":"sensitive"}`,
	} {
		t.Run(name, func(t *testing.T) {
			values := map[string]any{"id": "1", "author": map[string]string{"key": "opaque"}, "started": "2026-09-01T00:00:00Z", "updated": "2026-09-01T00:00:00Z", "timeSpentSeconds": 1}
			var change map[string]any
			if err := json.Unmarshal([]byte("{"+patch+"}"), &change); err != nil {
				t.Fatal(err)
			}
			for k, v := range change {
				values[k] = v
			}
			// Keep the overflow case as an exact JSON integer, not a decoded float.
			if name == "duration-overflow" {
				values["timeSpentSeconds"] = json.Number("9223372036854775807")
			}
			body, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"total":1,"worklogs":[%s]}`, body)
			}))
			defer srv.Close()
			c, err := jira.New(srv.URL, jira.NewStaticToken("synthetic"), jira.DefaultFieldConfig(), nil)
			if err != nil {
				t.Fatal(err)
			}
			logs, err := c.Worklogs(context.Background(), []string{"MET-1"}, time.Time{})
			if !errors.Is(err, kernel.ErrValidation) || logs != nil {
				t.Fatalf("invalid accepted: %+v %v", logs, err)
			}
			if strings.Contains(err.Error(), "sensitive") {
				t.Fatal("author leaked")
			}
		})
	}
}

func TestDL02_AD05_JiraPaginatesSprintsIssuesAndUsesConfiguredEpicField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := r.URL.Query().Get("startAt")
		switch r.URL.Path {
		case "/rest/agile/1.0/board/7/sprint":
			if start == "0" {
				_, _ = fmt.Fprint(w, `{"isLast":false,"values":[{"id":1,"name":"First"}]}`)
			} else {
				_, _ = fmt.Fprint(w, `{"isLast":true,"values":[{"id":2,"name":"Second"}]}`)
			}
		case "/rest/agile/1.0/sprint/1/issue":
			if start == "0" {
				_, _ = fmt.Fprint(w, `{"total":2,"issues":[{"key":"MET-1","fields":{"summary":"A"}}]}`)
			} else {
				_, _ = fmt.Fprint(w, `{"total":2,"issues":[{"key":"MET-2","fields":{"summary":"B"}}]}`)
			}
		case "/rest/agile/1.0/sprint/2/issue":
			_, _ = fmt.Fprint(w, `{"total":0,"issues":[]}`)
		case "/rest/api/2/search":
			if !strings.Contains(r.URL.Query().Get("jql"), "cf[12000] = MET-9") {
				t.Errorf("configured JQL=%s", r.URL.Query().Get("jql"))
			}
			_, _ = fmt.Fprint(w, `{"total":0,"issues":[]}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	f := jira.DefaultFieldConfig()
	f.EpicLinkField = "customfield_12000"
	c, err := jira.New(srv.URL, jira.NewStaticToken("synthetic"), f, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	sprints, err := c.Sprints(context.Background(), "7")
	if err != nil || len(sprints) != 2 || len(sprints[0].Issues) != 2 {
		t.Fatalf("sprints %+v %v", sprints, err)
	}
	if _, err := c.EpicIssues(context.Background(), "MET-9"); err != nil {
		t.Fatal(err)
	}
}
