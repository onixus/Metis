package confluence_test

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

	"github.com/onixus/metis/internal/adapters/confluence"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

const fixtures = "../../../fixtures/confluence"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtures, name))
	if err != nil {
		t.Fatalf("фикстура %s: %v", name, err)
	}
	return raw
}

// mockConfluence — httptest-сервер на записанных синтетических ответах Confluence Data Center.
type mockConfluence struct {
	t        *testing.T
	mu       sync.Mutex
	requests []recorded
	fail     int  // код ответа для всех запросов, если > 0
	huge     bool // отдавать ответ больше лимита
}

type recorded struct {
	Method, Path, Query, Auth string
	Body                      []byte
}

func (m *mockConfluence) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	m.mu.Lock()
	m.requests = append(m.requests, recorded{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), body})
	fail, huge := m.fail, m.huge
	m.mu.Unlock()
	if fail > 0 {
		w.WriteHeader(fail)
		return
	}
	if huge {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"1001","title":"%s"}`, strings.Repeat("a", confluence.MaxResponseBytes+1))
		return
	}
	serve := func(name string, code int) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write(fixture(m.t, name))
	}
	p := r.URL.Path
	switch {
	case r.Method == http.MethodGet && p == "/rest/api/content/1001":
		serve("page_1001.json", 200)
	case r.Method == http.MethodGet && p == "/rest/api/content/1001/property":
		serve("page_1001_properties.json", 200)
	case r.Method == http.MethodGet && p == "/rest/api/content/1001/property/metis_decision_id":
		serve("property_metis_decision_id.json", 200)
	case r.Method == http.MethodGet && strings.Contains(p, "/property/"):
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodPut && strings.HasPrefix(p, "/rest/api/content/1001/property/"):
		serve("property_metis_decision_id.json", 200)
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/property"):
		serve("property_created.json", 200)
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/label"):
		serve("labels_added.json", 200)
	case r.Method == http.MethodGet && p == "/rest/api/content/search":
		serve("search_adr.json", 200)
	case r.Method == http.MethodPost && p == "/rest/api/content":
		serve("create_page.json", 200)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newClient(t *testing.T) (*confluence.Client, *mockConfluence) {
	t.Helper()
	m := &mockConfluence{t: t}
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	c, err := confluence.New(srv.URL, confluence.NewStaticToken("synthetic-token"), srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return c, m
}

func (m *mockConfluence) find(method, suffix string) (recorded, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.requests {
		if r.Method == method && strings.HasSuffix(r.Path, suffix) {
			return r, true
		}
	}
	return recorded{}, false
}

func TestKB_Contract_PageWithLabelsPropertiesAndIndexText(t *testing.T) {
	c, m := newClient(t)
	p, err := c.Page(context.Background(), "1001")
	if err != nil {
		t.Fatal(err)
	}
	// NF-S17: управляющий символ из заголовка удалён.
	if p.ID != "1001" || p.Title != "ADR-0001 Коннектор EDR v2" || p.SpaceKey != "METIS" || p.Version != 3 {
		t.Fatalf("страница: %+v", p)
	}
	if p.URL != "https://confluence.example.test/pages/viewpage.action?pageId=1001" {
		t.Fatalf("url: %q", p.URL)
	}
	if p.UpdatedAt != time.Date(2026, 9, 10, 9, 30, 0, 0, time.UTC) {
		t.Fatalf("updated_at: %v", p.UpdatedAt)
	}
	if len(p.Labels) != 2 || p.Labels[0] != "metis" || p.Labels[1] != "adr" {
		t.Fatalf("метки: %v", p.Labels)
	}
	// Текст для индекса: теги удалены, сущности раскрыты, разметка не интерпретируется.
	if p.BodyText != "ADR-0001 Контекст: сделки & обязательства блокируются без коннектора. Вид feature" {
		t.Fatalf("текст индекса: %q", p.BodyText)
	}
	if p.Properties["metis_decision_id"] != "0192f3a0-0000-7000-8000-0000000000d1" {
		t.Fatalf("свойства: %v", p.Properties)
	}
	if p.Properties["metis_links"] != `{"feature":"0192f3a0-0000-7000-8000-000000000042"}` {
		t.Fatalf("нестроковое свойство: %q", p.Properties["metis_links"])
	}
	if m.requests[0].Auth != "Bearer synthetic-token" || !strings.Contains(m.requests[0].Query, "expand=") {
		t.Fatalf("запрос: %+v", m.requests[0])
	}
	if _, err := c.Page(context.Background(), "../admin"); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("ожидалась ошибка валидации идентификатора: %v", err)
	}
}

func TestKB_Contract_SearchByLabelBuildsCQL(t *testing.T) {
	c, m := newClient(t)
	pages, err := c.Search(context.Background(), "METIS", "adr")
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || pages[0].ID != "1001" || pages[1].Title != "ADR-0002 Изоляция хоста" {
		t.Fatalf("результат поиска: %+v", pages)
	}
	if pages[1].UpdatedAt != time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC) || pages[1].BodyText != "Контекст решения 2" {
		t.Fatalf("страница 2: %+v", pages[1])
	}
	req, ok := m.find(http.MethodGet, "/rest/api/content/search")
	if !ok {
		t.Fatal("поиск не вызван")
	}
	if !strings.Contains(req.Query, "cql=type+%3D+page+AND+space+%3D+%22METIS%22+AND+label+%3D+%22adr%22") {
		t.Fatalf("cql: %s", req.Query)
	}
	// Инъекция в CQL через ключ пространства или метку отклоняется.
	if _, err := c.Search(context.Background(), `METIS" OR space != "`, ""); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("ожидалась ошибка валидации: %v", err)
	}
	if _, err := c.Search(context.Background(), "METIS", `adr" OR label != "`); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("ожидалась ошибка валидации метки: %v", err)
	}
}

func TestKB_Contract_CreatePageAddsLabelsAndProperties(t *testing.T) {
	c, m := newClient(t)
	p, err := c.CreatePage(context.Background(), ports.CreatePageInput{
		SpaceKey: "METIS", ParentID: "1000", Title: "ADR: Коннектор EDR v2",
		Body:       "<h1>ADR: Коннектор EDR v2</h1>",
		Labels:     []string{"metis", "adr"},
		Properties: map[string]string{"metis_decision_id": "0192f3a0-0000-7000-8000-0000000000d1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "2001" || p.URL != "https://confluence.example.test/pages/viewpage.action?pageId=2001" || p.Version != 1 {
		t.Fatalf("созданная страница: %+v", p)
	}
	if len(p.Labels) != 2 || p.Properties["metis_decision_id"] == "" {
		t.Fatalf("метки/свойства созданной страницы: %+v", p)
	}
	create, ok := m.find(http.MethodPost, "/rest/api/content")
	if !ok {
		t.Fatal("создание не вызвано")
	}
	var body map[string]any
	if err := json.Unmarshal(create.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["type"] != "page" || body["title"] != "ADR: Коннектор EDR v2" {
		t.Fatalf("тело создания: %s", create.Body)
	}
	if body["space"].(map[string]any)["key"] != "METIS" || body["ancestors"].([]any)[0].(map[string]any)["id"] != "1000" {
		t.Fatalf("пространство/родитель: %s", create.Body)
	}
	storage := body["body"].(map[string]any)["storage"].(map[string]any)
	if storage["representation"] != "storage" || storage["value"] != "<h1>ADR: Коннектор EDR v2</h1>" {
		t.Fatalf("storage: %v", storage)
	}
	labels, ok := m.find(http.MethodPost, "/rest/api/content/2001/label")
	if !ok || string(labels.Body) != `[{"prefix":"global","name":"metis"},{"prefix":"global","name":"adr"}]` {
		t.Fatalf("метки: %s", labels.Body)
	}
	prop, ok := m.find(http.MethodPost, "/rest/api/content/2001/property")
	if !ok || !strings.Contains(string(prop.Body), `"key":"metis_decision_id"`) {
		t.Fatalf("свойство: %s", prop.Body)
	}
	if _, err := c.CreatePage(context.Background(), ports.CreatePageInput{SpaceKey: "METIS"}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("пустой заголовок: %v", err)
	}
}

func TestKB_Contract_SetPropertiesUpdatesExistingWithVersion(t *testing.T) {
	c, m := newClient(t)
	err := c.SetProperties(context.Background(), "1001", map[string]string{
		"metis_status":      "accepted",
		"metis_decision_id": "0192f3a0-0000-7000-8000-0000000000d1",
	})
	if err != nil {
		t.Fatal(err)
	}
	put, ok := m.find(http.MethodPut, "/property/metis_decision_id")
	if !ok || !strings.Contains(string(put.Body), `"version":{"number":2}`) {
		t.Fatalf("существующее свойство обновляется PUT с версией +1: %s", put.Body)
	}
	post, ok := m.find(http.MethodPost, "/rest/api/content/1001/property")
	if !ok || !strings.Contains(string(post.Body), `"key":"metis_status"`) || !strings.Contains(string(post.Body), `"value":"accepted"`) {
		t.Fatalf("новое свойство создаётся POST: %s", post.Body)
	}
	if err := c.SetProperties(context.Background(), "1001", map[string]string{"bad key": "x"}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("ключ свойства с пробелом: %v", err)
	}
	if err := c.AddLabels(context.Background(), "1001", []string{"a b"}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("метка с пробелом: %v", err)
	}
	if err := c.AddLabels(context.Background(), "1001", nil); err != nil {
		t.Fatalf("пустой список меток — без запроса: %v", err)
	}
}

func TestKB_Contract_ErrorsMappedAndTokenNotLeaked(t *testing.T) {
	c, m := newClient(t)
	cases := map[int]error{
		http.StatusUnauthorized:        kernel.ErrForbidden,
		http.StatusForbidden:           kernel.ErrForbidden,
		http.StatusNotFound:            kernel.ErrNotFound,
		http.StatusBadRequest:          kernel.ErrValidation,
		http.StatusInternalServerError: kernel.ErrUnavailable,
	}
	for code, want := range cases {
		m.mu.Lock()
		m.fail = code
		m.mu.Unlock()
		_, err := c.Page(context.Background(), "1001")
		if !errors.Is(err, want) {
			t.Fatalf("код %d: ожидалась %v, получено %v", code, want, err)
		}
		if strings.Contains(err.Error(), "synthetic-token") {
			t.Fatalf("токен в тексте ошибки: %v", err)
		}
	}
	m.mu.Lock()
	m.fail, m.huge = 0, true
	m.mu.Unlock()
	if _, err := c.Page(context.Background(), "1001"); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("ответ больше лимита: %v", err)
	}
	// Пустой токен — отказ до сетевого запроса.
	c2, err := confluence.New("https://confluence.example.test", confluence.NewStaticToken(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Page(context.Background(), "1001"); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("пустой токен: %v", err)
	}
	if fmt.Sprint(confluence.NewStaticToken("secret")) != "StaticToken{***}" {
		t.Fatal("токен печатается")
	}
	if _, err := confluence.New("not a url", confluence.NewStaticToken("x"), nil); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("некорректный адрес: %v", err)
	}
}

func TestKB_Contract_ContextCancelled(t *testing.T) {
	c, _ := newClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Page(ctx, "1001"); !errors.Is(err, kernel.ErrUnavailable) || !errors.Is(err, context.Canceled) {
		t.Fatalf("отменённый контекст: %v", err)
	}
}
