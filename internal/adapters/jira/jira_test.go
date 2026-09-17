package jira_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onixus/metis/internal/adapters/jira"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

const fixtures = "../../../fixtures/jira"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtures, name))
	if err != nil {
		t.Fatalf("фикстура %s: %v", name, err)
	}
	return raw
}

// mockJira — httptest-сервер на записанных синтетических ответах.
type mockJira struct {
	t        *testing.T
	mu       sync.Mutex
	requests []recorded
	fail     int // код ответа для всех запросов, если > 0
}

type recorded struct {
	Method, Path, Query, Auth string
	Body                      []byte
}

func (m *mockJira) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	m.mu.Lock()
	m.requests = append(m.requests, recorded{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), body})
	fail := m.fail
	m.mu.Unlock()
	if fail > 0 {
		w.WriteHeader(fail)
		return
	}
	serve := func(name string, code int) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write(fixture(m.t, name))
	}
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/search" && strings.Contains(q.Get("jql"), "Epic Link"):
		serve("epic_issues.json", 200)
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/search":
		serve("search_epics.json", 200)
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/SOAR-42":
		serve("issue_soar42.json", 200)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/rest/api/2/issue/"):
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodPost && r.URL.Path == "/rest/api/2/issue":
		serve("create_epic.json", 201)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/rest/api/2/issue/"):
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && r.URL.Path == "/rest/agile/1.0/board/7/sprint":
		serve("sprints.json", 200)
	case r.Method == http.MethodGet && r.URL.Path == "/rest/agile/1.0/sprint/101/issue":
		serve("sprint_issues_101.json", 200)
	case r.Method == http.MethodGet && r.URL.Path == "/rest/agile/1.0/sprint/102/issue":
		serve("sprint_issues_102.json", 200)
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/project/SOAR/versions":
		serve("versions.json", 200)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newClient(t *testing.T) (*jira.Client, *mockJira) {
	t.Helper()
	m := &mockJira{t: t}
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	c, err := jira.New(srv.URL, jira.NewStaticToken("synthetic-token"), jira.DefaultFieldConfig(), srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return c, m
}

func TestJiraAdapter_EpicsByJQLAndSanitize(t *testing.T) {
	c, m := newClient(t)
	epics, err := c.Epics(context.Background(), "SOAR")
	if err != nil {
		t.Fatal(err)
	}
	if len(epics) != 2 {
		t.Fatalf("эпиков: %d", len(epics))
	}
	e := epics[0]
	if e.Key != "SOAR-42" || e.Summary != "Коннектор EDR v2" || e.Status != "In Progress" || e.Priority != "Medium" {
		t.Fatalf("эпик: %+v", e)
	}
	if e.DueDate != kernel.DateOf(2026, time.December, 1) || len(e.FixVersions) != 1 || e.FixVersions[0] != "SOAR 2.4" {
		t.Fatalf("даты/версии: %+v", e)
	}
	if e.FeatureRef != "0192f3a0-0000-7000-8000-000000000042" {
		t.Fatalf("ссылка на фичу из метки: %q", e.FeatureRef)
	}
	if e.CreatedAt != time.Date(2026, 8, 1, 6, 0, 0, 0, time.UTC) {
		t.Fatalf("created: %v", e.CreatedAt)
	}
	// NF-S17: управляющие символы удалены, содержимое не интерпретируется.
	if strings.ContainsAny(epics[1].Summary, "\x07\x00") || epics[1].Summary != "Плейбуки реагирования с управляющим символом" {
		t.Fatalf("summary не очищен: %q", epics[1].Summary)
	}
	if !epics[1].DueDate.IsZero() {
		t.Fatal("null duedate должен быть пустой датой")
	}
	req := m.requests[0]
	if !strings.Contains(req.Query, "jql=project+%3D+SOAR+AND+issuetype+%3D+%22Epic%22") {
		t.Fatalf("jql: %s", req.Query)
	}
	if req.Auth != "Bearer synthetic-token" {
		t.Fatalf("авторизация: %q", req.Auth)
	}
	// JQL-инъекция через ключ проекта отклоняется.
	if _, err := c.Epics(context.Background(), "SOAR OR 1=1"); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("ожидалась ошибка валидации: %v", err)
	}
}

func TestJiraAdapter_EpicAndIssues(t *testing.T) {
	c, _ := newClient(t)
	e, err := c.Epic(context.Background(), "SOAR-42")
	if err != nil || e.Key != "SOAR-42" || e.DueDate.String() != "2026-12-01" {
		t.Fatalf("эпик: %+v %v", e, err)
	}
	if _, err := c.Epic(context.Background(), "SOAR-404"); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("ожидался ErrNotFound: %v", err)
	}
	issues, err := c.EpicIssues(context.Background(), "SOAR-42")
	if err != nil || len(issues) != 4 {
		t.Fatalf("задачи: %d %v", len(issues), err)
	}
	if !issues[0].Done || issues[2].Done || issues[0].EpicKey != "SOAR-42" {
		t.Fatalf("статусы: %+v", issues)
	}
	if got := issues[1].SprintIDs; len(got) != 2 || got[0] != "101" || got[1] != "102" {
		t.Fatalf("спринты задачи: %v", got)
	}
}

func TestJiraAdapter_SprintsWithComposition(t *testing.T) {
	c, _ := newClient(t)
	sprints, err := c.Sprints(context.Background(), "7")
	if err != nil || len(sprints) != 2 {
		t.Fatalf("спринты: %d %v", len(sprints), err)
	}
	s := sprints[1]
	if s.ID != "102" || s.State != ports.SprintActive || s.Goal != "Изоляция хоста и завершение клиента" {
		t.Fatalf("спринт: %+v", s)
	}
	if s.StartDate.String() != "2026-08-18" || s.EndDate.String() != "2026-09-01" {
		t.Fatalf("даты: %s %s", s.StartDate, s.EndDate)
	}
	if len(s.Issues) != 2 || s.Issues[0].Key != "SOAR-102" || s.Issues[1].Key != "SOAR-103" {
		t.Fatalf("состав: %+v", s.Issues)
	}
	if sprints[0].State != ports.SprintClosed {
		t.Fatal("первый спринт закрыт")
	}
}

func TestJiraAdapter_Versions(t *testing.T) {
	c, _ := newClient(t)
	vs, err := c.Versions(context.Background(), "SOAR")
	if err != nil || len(vs) != 2 {
		t.Fatalf("версии: %v %v", vs, err)
	}
	if vs[0].Name != "SOAR 2.4" || vs[0].Released || vs[0].ReleaseDate.String() != "2026-12-15" || !vs[1].Released {
		t.Fatalf("версии: %+v", vs)
	}
}

func TestJiraAdapter_CreateEpicAndWrites(t *testing.T) {
	c, m := newClient(t)
	key, err := c.CreateEpic(context.Background(), "SOAR", "Коннектор EDR v2", "описание", "feature-uuid")
	if err != nil || key != "SOAR-99" {
		t.Fatalf("создание: %q %v", key, err)
	}
	var body struct {
		Fields map[string]any `json:"fields"`
	}
	if err := json.Unmarshal(m.requests[0].Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Fields["summary"] != "Коннектор EDR v2" || body.Fields["customfield_10011"] != "Коннектор EDR v2" {
		t.Fatalf("тело: %s", m.requests[0].Body)
	}
	if it, _ := body.Fields["issuetype"].(map[string]any); it["name"] != "Epic" {
		t.Fatalf("тип: %v", body.Fields["issuetype"])
	}
	if labels, _ := body.Fields["labels"].([]any); len(labels) != 1 || labels[0] != "metis-feature-feature-uuid" {
		t.Fatalf("метка: %v", body.Fields["labels"])
	}
	if err := c.SetEpicPriority(context.Background(), "SOAR-99", "High"); err != nil {
		t.Fatal(err)
	}
	if err := c.LinkEpicToFeature(context.Background(), "SOAR-99", "feature-uuid"); err != nil {
		t.Fatal(err)
	}
	if len(m.requests) != 3 || m.requests[1].Method != http.MethodPut || m.requests[2].Path != "/rest/api/2/issue/SOAR-99" {
		t.Fatalf("запросы: %+v", m.requests)
	}
	if !strings.Contains(string(m.requests[2].Body), `"add":"metis-feature-feature-uuid"`) {
		t.Fatalf("тело ссылки: %s", m.requests[2].Body)
	}
	if _, err := c.CreateEpic(context.Background(), "SOAR", "", "", ""); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("пустой summary: %v", err)
	}
}

func TestJiraAdapter_ErrorMapping(t *testing.T) {
	c, m := newClient(t)
	for code, want := range map[int]error{401: kernel.ErrForbidden, 403: kernel.ErrForbidden, 400: kernel.ErrValidation, 500: kernel.ErrUnavailable, 503: kernel.ErrUnavailable} {
		m.mu.Lock()
		m.fail = code
		m.mu.Unlock()
		_, err := c.Epics(context.Background(), "SOAR")
		if !errors.Is(err, want) {
			t.Fatalf("код %d: ожидалось %v, получено %v", code, want, err)
		}
		if strings.Contains(err.Error(), "synthetic-token") {
			t.Fatalf("токен попал в ошибку: %v", err)
		}
	}
	// Недоступный сервер.
	down, err := jira.New("http://127.0.0.1:1", jira.NewStaticToken("x"), jira.DefaultFieldConfig(), &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := down.Epics(context.Background(), "SOAR"); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("недоступность: %v", err)
	}
	// Пустой токен — запрет без обращения к сети.
	if _, err := jira.NewStaticToken("").Token(context.Background()); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("пустой токен: %v", err)
	}
	if fmt.Sprint(jira.NewStaticToken("secret")) == "secret" || strings.Contains(fmt.Sprintf("%v", jira.NewStaticToken("secret")), "secret") {
		t.Fatal("токен печатается")
	}
}

func TestJiraAdapter_ParseWebhookDueDateShift(t *testing.T) {
	c, _ := newClient(t)
	ev, err := c.ParseWebhook(fixture(t, "webhook_issue_updated.json"))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != ports.WebhookEpicUpdated || ev.EpicKey != "SOAR-42" || ev.ExternalID != "10042:50001" {
		t.Fatalf("событие: %+v", ev)
	}
	if ev.NewDueDate != kernel.DateOf(2026, time.December, 22) {
		t.Fatalf("новая дата: %s", ev.NewDueDate)
	}
	if len(ev.ChangedFields) != 2 || ev.ChangedFields[0] != "due_date" || ev.ChangedFields[1] != "fix_versions" {
		t.Fatalf("поля: %v", ev.ChangedFields)
	}
	if ev.OccurredAt != time.UnixMilli(1766400000000).UTC() {
		t.Fatalf("время: %v", ev.OccurredAt)
	}
	// Событие по обычной задаче игнорируется.
	if _, err := c.ParseWebhook(fixture(t, "webhook_task_updated.json")); !errors.Is(err, jira.ErrNotEpicEvent) {
		t.Fatalf("ожидался ErrNotEpicEvent: %v", err)
	}
	if _, err := c.ParseWebhook([]byte("{not json")); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("мусор: %v", err)
	}
}

func TestJiraAdapter_SanitizeTruncatesAndStripsControls(t *testing.T) {
	in := "a\x00b\x1fc\r\nd\te" + strings.Repeat("ё", 100)
	got := jira.Sanitize(in, 10)
	if got != "abc\nd\teёёё" {
		t.Fatalf("sanitize: %q", got)
	}
	if jira.Sanitize("чистая строка", 100) != "чистая строка" {
		t.Fatal("чистая строка не должна меняться")
	}
}
