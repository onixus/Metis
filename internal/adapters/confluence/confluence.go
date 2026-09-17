// Package confluence — адаптер порта ports.KnowledgeBase для Confluence Data Center (REST API /rest/api).
// Реализован на net/http без сторонних клиентов. Содержимое из Confluence считается недоверенным (NF-S17):
// строки обрезаются по длине и очищаются от управляющих символов, разметка страницы не интерпретируется —
// для индекса из неё лишь удаляются теги. Текст, отправляемый в storage-формате, экранируется вызывающим
// (internal/knowledgedocs); заголовок, метки и свойства адаптер передаёт как данные JSON.
package confluence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

// TokenSource отдаёт токен сервисной учётки из хранилища секретов (NF-S04). Токен не логируется.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// StaticToken — учётные данные из заранее полученного токена (значение передаётся из хранилища секретов).
type StaticToken struct{ value string }

// NewStaticToken оборачивает токен.
func NewStaticToken(token string) StaticToken { return StaticToken{value: token} }

// Token возвращает токен.
func (s StaticToken) Token(context.Context) (string, error) {
	if s.value == "" {
		return "", fmt.Errorf("%w: токен Confluence не задан", kernel.ErrForbidden)
	}
	return s.value, nil
}

// String скрывает токен при печати и логировании.
func (StaticToken) String() string { return "StaticToken{***}" }

// MaxResponseBytes — лимит размера ответа Confluence.
const MaxResponseBytes = 8 << 20

// pageLimit — размер страницы выборки при поиске.
const pageLimit = 50

// Client — адаптер Confluence. Реализует ports.KnowledgeBase.
type Client struct {
	base *url.URL
	http *http.Client
	tok  TokenSource
}

var _ ports.KnowledgeBase = (*Client)(nil)

// New создаёт клиент. client может быть nil — тогда используется клиент с таймаутом 10 с.
func New(baseURL string, token TokenSource, client *http.Client) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, kernel.Invalid("base_url", "некорректный адрес Confluence")
	}
	if token == nil {
		return nil, kernel.Invalid("token", "источник токена обязателен")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{base: u, http: client, tok: token}, nil
}

// --- типы ответов Confluence (только нужные поля) ---

type cfContent struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Space struct {
		Key string `json:"key"`
	} `json:"space"`
	Version struct {
		Number int    `json:"number"`
		When   string `json:"when"`
	} `json:"version"`
	Body struct {
		Storage struct {
			Value string `json:"value"`
		} `json:"storage"`
	} `json:"body"`
	Metadata struct {
		Labels struct {
			Results []cfLabel `json:"results"`
		} `json:"labels"`
	} `json:"metadata"`
	Links struct {
		WebUI string `json:"webui"`
		Base  string `json:"base"`
	} `json:"_links"`
}

type cfLabel struct {
	Prefix string `json:"prefix,omitempty"`
	Name   string `json:"name"`
}

type cfProperty struct {
	Key     string          `json:"key"`
	Value   json.RawMessage `json:"value"`
	Version struct {
		Number int `json:"number"`
	} `json:"version"`
}

type cfList[T any] struct {
	Results []T `json:"results"`
	Start   int `json:"start"`
	Limit   int `json:"limit"`
	Size    int `json:"size"`
}

const contentExpand = "space,version,body.storage,metadata.labels"

// --- запросы ---

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	token, err := c.tok.Token(ctx)
	if err != nil {
		return err
	}
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	if query != nil {
		u.RawQuery = query.Encode()
	}
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("confluence %s %s: %w", method, path, err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return fmt.Errorf("confluence %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// Сообщение транспорта не содержит токена: заголовки в ошибку не попадают.
		return fmt.Errorf("%w: confluence %s %s: %w", kernel.ErrUnavailable, method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	limited := io.LimitReader(resp.Body, MaxResponseBytes)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, limited)
		return statusError(resp.StatusCode, method, path)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, limited)
		return nil
	}
	if err := json.NewDecoder(limited).Decode(out); err != nil {
		return fmt.Errorf("%w: confluence %s %s: ответ не разобран: %w", kernel.ErrUnavailable, method, path, err)
	}
	return nil
}

func statusError(code int, method, path string) error {
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: confluence %s %s: код %d", kernel.ErrForbidden, method, path, code)
	case http.StatusNotFound:
		return fmt.Errorf("%w: confluence %s %s", kernel.ErrNotFound, method, path)
	case http.StatusBadRequest, http.StatusConflict:
		return fmt.Errorf("%w: confluence %s %s: код %d", kernel.ErrValidation, method, path, code)
	default:
		return fmt.Errorf("%w: confluence %s %s: код %d", kernel.ErrUnavailable, method, path, code)
	}
}

// Page возвращает страницу с метками, свойствами и текстом для индекса.
func (c *Client) Page(ctx context.Context, id string) (ports.Page, error) {
	if err := validKey(id); err != nil {
		return ports.Page{}, err
	}
	q := url.Values{}
	q.Set("expand", contentExpand)
	var ct cfContent
	if err := c.do(ctx, http.MethodGet, "/rest/api/content/"+url.PathEscape(id), q, nil, &ct); err != nil {
		return ports.Page{}, err
	}
	p := c.toPage(ct)
	props, err := c.properties(ctx, id)
	if err != nil {
		return ports.Page{}, err
	}
	p.Properties = props
	return p, nil
}

// Search возвращает страницы пространства с меткой (CQL через /rest/api/content/search).
func (c *Client) Search(ctx context.Context, spaceKey, label string) ([]ports.Page, error) {
	if err := validKey(spaceKey); err != nil {
		return nil, err
	}
	cql := fmt.Sprintf("type = page AND space = %q", spaceKey)
	if label != "" {
		if err := validKey(label); err != nil {
			return nil, err
		}
		cql += fmt.Sprintf(" AND label = %q", label)
	}
	cql += " ORDER BY created ASC"
	var out []ports.Page
	for start := 0; ; {
		q := url.Values{}
		q.Set("cql", cql)
		q.Set("expand", contentExpand)
		q.Set("start", strconv.Itoa(start))
		q.Set("limit", strconv.Itoa(pageLimit))
		var page cfList[cfContent]
		if err := c.do(ctx, http.MethodGet, "/rest/api/content/search", q, nil, &page); err != nil {
			return nil, err
		}
		for _, ct := range page.Results {
			out = append(out, c.toPage(ct))
		}
		start += len(page.Results)
		if len(page.Results) == 0 || len(page.Results) < page.Limit || page.Limit == 0 {
			break
		}
	}
	return out, nil
}

// CreatePage создаёт страницу, затем добавляет метки и свойства. Вызывается только из обработчика outbox.
//
// Если страница создана, а оформление (метки, свойства) не удалось, возвращается созданная страница
// вместе с ошибкой: идентификатор не теряется, и вызывающий сохраняет его до повторной доставки
// события — иначе повтор создал бы вторую страницу ADR того же решения.
func (c *Client) CreatePage(ctx context.Context, in ports.CreatePageInput) (ports.Page, error) {
	if err := validKey(in.SpaceKey); err != nil {
		return ports.Page{}, err
	}
	if strings.TrimSpace(in.Title) == "" {
		return ports.Page{}, kernel.Invalid("title", "обязателен")
	}
	if in.ParentID != "" {
		if err := validKey(in.ParentID); err != nil {
			return ports.Page{}, err
		}
	}
	body := map[string]any{
		"type":  "page",
		"title": in.Title,
		"space": map[string]string{"key": in.SpaceKey},
		"body": map[string]any{
			"storage": map[string]string{"value": in.Body, "representation": "storage"},
		},
	}
	if in.ParentID != "" {
		body["ancestors"] = []map[string]string{{"id": in.ParentID}}
	}
	var ct cfContent
	if err := c.do(ctx, http.MethodPost, "/rest/api/content", nil, body, &ct); err != nil {
		return ports.Page{}, err
	}
	if ct.ID == "" {
		return ports.Page{}, fmt.Errorf("%w: confluence не вернула идентификатор страницы", kernel.ErrUnavailable)
	}
	created := c.toPage(ct)
	if len(in.Labels) > 0 {
		if err := c.AddLabels(ctx, ct.ID, in.Labels); err != nil {
			return created, fmt.Errorf("метки страницы %s: %w", created.ID, err)
		}
	}
	if len(in.Properties) > 0 {
		if err := c.SetProperties(ctx, ct.ID, in.Properties); err != nil {
			return created, fmt.Errorf("свойства страницы %s: %w", created.ID, err)
		}
	}
	p := created
	p.Labels = append(p.Labels, in.Labels...)
	p.Properties = copyProps(in.Properties)
	return p, nil
}

// SetProperties задаёт page properties: новое свойство создаётся (POST), существующее — обновляется (PUT)
// с увеличением номера версии. Вызывается только из обработчика outbox.
func (c *Client) SetProperties(ctx context.Context, id string, props map[string]string) error {
	if err := validKey(id); err != nil {
		return err
	}
	for _, key := range sortedKeys(props) {
		if err := validKey(key); err != nil {
			return fmt.Errorf("свойство %q: %w", key, err)
		}
		path := "/rest/api/content/" + url.PathEscape(id) + "/property/" + url.PathEscape(key)
		var cur cfProperty
		err := c.do(ctx, http.MethodGet, path, nil, nil, &cur)
		switch {
		case err == nil:
			body := map[string]any{"key": key, "value": props[key], "version": map[string]int{"number": cur.Version.Number + 1}}
			if err := c.do(ctx, http.MethodPut, path, nil, body, nil); err != nil {
				return err
			}
		case errors.Is(err, kernel.ErrNotFound):
			body := map[string]any{"key": key, "value": props[key]}
			if err := c.do(ctx, http.MethodPost, "/rest/api/content/"+url.PathEscape(id)+"/property", nil, body, nil); err != nil {
				return err
			}
		default:
			return err
		}
	}
	return nil
}

// AddLabels добавляет метки к странице. Вызывается только из обработчика outbox.
func (c *Client) AddLabels(ctx context.Context, id string, labels []string) error {
	if err := validKey(id); err != nil {
		return err
	}
	if len(labels) == 0 {
		return nil
	}
	body := make([]cfLabel, 0, len(labels))
	for _, l := range labels {
		if err := validKey(l); err != nil {
			return fmt.Errorf("метка %q: %w", l, err)
		}
		body = append(body, cfLabel{Prefix: "global", Name: l})
	}
	return c.do(ctx, http.MethodPost, "/rest/api/content/"+url.PathEscape(id)+"/label", nil, body, nil)
}

func (c *Client) properties(ctx context.Context, id string) (map[string]string, error) {
	var list cfList[cfProperty]
	if err := c.do(ctx, http.MethodGet, "/rest/api/content/"+url.PathEscape(id)+"/property", nil, nil, &list); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(list.Results))
	for _, p := range list.Results {
		out[Sanitize(p.Key, MaxKey)] = Sanitize(propertyString(p.Value), MaxSummary)
	}
	return out, nil
}

// --- преобразования ---

func (c *Client) toPage(ct cfContent) ports.Page {
	p := ports.Page{
		ID:        Sanitize(ct.ID, MaxKey),
		Title:     Sanitize(ct.Title, MaxSummary),
		SpaceKey:  Sanitize(ct.Space.Key, MaxKey),
		Version:   ct.Version.Number,
		UpdatedAt: timeOf(ct.Version.When),
		BodyText:  Sanitize(StripTags(ct.Body.Storage.Value), MaxBody),
	}
	for _, l := range ct.Metadata.Labels.Results {
		p.Labels = append(p.Labels, Sanitize(l.Name, MaxKey))
	}
	if ct.Links.WebUI != "" {
		base := ct.Links.Base
		if base == "" {
			base = strings.TrimRight(c.base.String(), "/")
		}
		p.URL = Sanitize(base+ct.Links.WebUI, MaxSummary)
	}
	return p
}

// propertyString приводит значение свойства к строке: строковые значения — как есть, прочие — компактным JSON.
// TODO(question-21): формат значений page properties не задан в ТЗ.
func propertyString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return ""
	}
	return buf.String()
}

func timeOf(s string) time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05.000-07:00", "2006-01-02T15:04:05.000Z", time.RFC3339, time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func copyProps(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Детерминированный порядок запросов упрощает контрактные тесты и повторы.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// validKey защищает CQL и пути от инъекции: идентификаторы, ключи пространств, метки и ключи свойств —
// только буквы, цифры, «-», «_», «.».
func validKey(k string) error {
	if k == "" || len(k) > MaxKey {
		return kernel.Invalid("key", "ключ пуст или слишком длинный")
	}
	for _, r := range k {
		ok := r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
		if !ok {
			return kernel.Invalid("key", "недопустимый символ в ключе")
		}
	}
	return nil
}
