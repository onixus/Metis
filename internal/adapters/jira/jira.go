// Package jira — адаптер порта ports.DeliveryTracker для Jira Data Center (REST API v2, Agile API 1.0).
// Реализован на net/http без сторонних клиентов. Содержимое из Jira считается недоверенным (NF-S17):
// строки обрезаются по длине и очищаются от управляющих символов, но не интерпретируются.
package jira

import (
	"bytes"
	"context"
	"encoding/json"
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

// Credentials отдаёт токен сервисной учётки из хранилища секретов (NF-S04). Токен не логируется.
type Credentials interface {
	Token(ctx context.Context) (string, error)
}

// StaticToken — учётные данные из заранее полученного токена (значение передаётся из хранилища секретов).
type StaticToken struct{ value string }

// NewStaticToken оборачивает токен.
func NewStaticToken(token string) StaticToken { return StaticToken{value: token} }

// Token возвращает токен.
func (s StaticToken) Token(context.Context) (string, error) {
	if s.value == "" {
		return "", fmt.Errorf("%w: токен Jira не задан", kernel.ErrForbidden)
	}
	return s.value, nil
}

// String скрывает токен при печати и логировании.
func (StaticToken) String() string { return "StaticToken{***}" }

// FieldConfig — имена полей Jira, настраиваемые администратором (AD-05, ТЗ 4.2).
type FieldConfig struct {
	EpicIssueType   string `json:"epic_issue_type"`
	EpicLinkField   string `json:"epic_link_field"`
	EpicNameField   string `json:"epic_name_field"`
	FeatureRefField string `json:"feature_ref_field"`
	FeatureLabelPfx string `json:"feature_label_prefix"`
	MaxResults      int    `json:"max_results"`
}

// DefaultFieldConfig — значения по умолчанию для Jira Data Center.
func DefaultFieldConfig() FieldConfig {
	return FieldConfig{
		EpicIssueType:   "Epic",
		EpicLinkField:   "Epic Link",
		EpicNameField:   "customfield_10011",
		FeatureLabelPfx: "metis-feature-",
		MaxResults:      100,
	}
}

// Client — адаптер Jira. Реализует ports.DeliveryTracker.
type Client struct {
	base   *url.URL
	http   *http.Client
	creds  Credentials
	fields FieldConfig
}

var _ ports.DeliveryTracker = (*Client)(nil)

// New создаёт клиент. httpClient может быть nil — тогда используется клиент с таймаутом 10 с.
func New(baseURL string, creds Credentials, fields FieldConfig, httpClient *http.Client) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, kernel.Invalid("base_url", "некорректный адрес Jira")
	}
	if creds == nil {
		return nil, kernel.Invalid("credentials", "учётные данные обязательны")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	if fields.MaxResults <= 0 {
		fields.MaxResults = DefaultFieldConfig().MaxResults
	}
	if fields.MaxResults > 1000 {
		return nil, kernel.Invalid("max_results", "must not exceed 1000")
	}
	if fields.EpicLinkField == "" {
		fields.EpicLinkField = DefaultFieldConfig().EpicLinkField
	}
	copyClient := *httpClient
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: u, http: &copyClient, creds: creds, fields: fields}, nil
}

// --- вспомогательные типы ответов Jira (только нужные поля) ---

type jiraIssue struct {
	ID     string                     `json:"id"`
	Key    string                     `json:"key"`
	Fields map[string]json.RawMessage `json:"fields"`
}

type jiraSearch struct {
	StartAt    int         `json:"startAt"`
	MaxResults int         `json:"maxResults"`
	Total      int         `json:"total"`
	Issues     []jiraIssue `json:"issues"`
}

type jiraNamed struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type jiraStatus struct {
	Name           string `json:"name"`
	StatusCategory struct {
		Key string `json:"key"`
	} `json:"statusCategory"`
}

type jiraSprint struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Goal      string `json:"goal"`
	State     string `json:"state"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
}

type jiraVersion struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Released    bool   `json:"released"`
	ReleaseDate string `json:"releaseDate"`
}

// --- запросы ---

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	token, err := c.creds.Token(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("jira credentials: %w", ctx.Err())
		}
		return fmt.Errorf("%w: Jira credentials unavailable", kernel.ErrForbidden)
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
			return fmt.Errorf("jira %s %s: %w", method, path, err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return fmt.Errorf("jira %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// Сообщение транспорта не содержит токена: заголовки в ошибку не попадают.
		if ctx.Err() != nil {
			return fmt.Errorf("%w: jira request: %w", kernel.ErrUnavailable, ctx.Err())
		}
		return fmt.Errorf("%w: jira transport failed", kernel.ErrUnavailable)
	}
	defer func() { _ = resp.Body.Close() }()
	limited := io.LimitReader(resp.Body, 8<<20)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, limited)
		return statusError(resp.StatusCode, method, path)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, limited)
		return nil
	}
	if err := json.NewDecoder(limited).Decode(out); err != nil {
		return fmt.Errorf("%w: jira %s %s: ответ не разобран: %w", kernel.ErrUnavailable, method, path, err)
	}
	return nil
}

func statusError(code int, method, path string) error {
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: jira %s %s: код %d", kernel.ErrForbidden, method, path, code)
	case http.StatusNotFound:
		return fmt.Errorf("%w: jira %s %s", kernel.ErrNotFound, method, path)
	case http.StatusBadRequest, http.StatusConflict:
		return fmt.Errorf("%w: jira %s %s: код %d", kernel.ErrValidation, method, path, code)
	default:
		return fmt.Errorf("%w: jira %s %s: код %d", kernel.ErrUnavailable, method, path, code)
	}
}

func (c *Client) search(ctx context.Context, jql string, fields []string) ([]jiraIssue, error) {
	var all []jiraIssue
	for start, pages := 0, 0; ; pages++ {
		if pages >= maxPages {
			return nil, kernel.Invalid("pagination", "Jira page limit exceeded")
		}
		q := url.Values{}
		q.Set("jql", jql)
		q.Set("startAt", strconv.Itoa(start))
		q.Set("maxResults", strconv.Itoa(c.fields.MaxResults))
		q.Set("fields", strings.Join(fields, ","))
		var page jiraSearch
		if err := c.do(ctx, http.MethodGet, "/rest/api/2/search", q, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Issues...)
		if len(all) > maxRecords {
			return nil, kernel.Invalid("pagination", "Jira record limit exceeded")
		}
		start += len(page.Issues)
		if len(page.Issues) == 0 || start >= page.Total {
			break
		}
	}
	return all, nil
}

func (c *Client) epicFields() []string {
	f := []string{"summary", "description", "status", "priority", "duedate", "fixVersions", "labels", "created"}
	if c.fields.FeatureRefField != "" {
		f = append(f, c.fields.FeatureRefField)
	}
	return f
}

// Epics возвращает эпики проекта по JQL.
func (c *Client) Epics(ctx context.Context, project string) ([]ports.Epic, error) {
	if err := validKey(project); err != nil {
		return nil, err
	}
	jql := fmt.Sprintf("project = %s AND issuetype = %q ORDER BY created ASC", project, c.fields.EpicIssueType)
	issues, err := c.search(ctx, jql, c.epicFields())
	if err != nil {
		return nil, err
	}
	out := make([]ports.Epic, 0, len(issues))
	for _, is := range issues {
		out = append(out, c.toEpic(is))
	}
	return out, nil
}

// Epic возвращает эпик по ключу.
func (c *Client) Epic(ctx context.Context, key string) (ports.Epic, error) {
	if err := validKey(key); err != nil {
		return ports.Epic{}, err
	}
	q := url.Values{}
	q.Set("fields", strings.Join(c.epicFields(), ","))
	var is jiraIssue
	if err := c.do(ctx, http.MethodGet, "/rest/api/2/issue/"+url.PathEscape(key), q, nil, &is); err != nil {
		return ports.Epic{}, err
	}
	return c.toEpic(is), nil
}

// EpicIssues возвращает задачи эпика.
func (c *Client) EpicIssues(ctx context.Context, epicKey string) ([]ports.Issue, error) {
	if err := validKey(epicKey); err != nil {
		return nil, err
	}
	field := strconv.Quote(c.fields.EpicLinkField)
	if suffix, ok := strings.CutPrefix(c.fields.EpicLinkField, "customfield_"); ok {
		if _, err := strconv.ParseUint(suffix, 10, 64); err != nil {
			return nil, kernel.Invalid("epic_link_field", "invalid custom field")
		}
		field = "cf[" + suffix + "]"
	}
	jql := fmt.Sprintf("%s = %s ORDER BY created ASC", field, epicKey)
	issues, err := c.search(ctx, jql, []string{"summary", "status", "created", "closedSprints", "sprint"})
	if err != nil {
		return nil, err
	}
	out := make([]ports.Issue, 0, len(issues))
	for _, is := range issues {
		it := toIssue(is)
		it.EpicKey = epicKey
		out = append(out, it)
	}
	return out, nil
}

// Sprints возвращает спринты доски вместе с составом (Agile API).
func (c *Client) Sprints(ctx context.Context, board string) ([]ports.Sprint, error) {
	if err := validKey(board); err != nil {
		return nil, err
	}
	sprints, err := c.allSprints(ctx, board)
	if err != nil {
		return nil, err
	}
	out := make([]ports.Sprint, 0, len(sprints))
	for _, s := range sprints {
		sp := ports.Sprint{
			ID: strconv.Itoa(s.ID), Name: Sanitize(s.Name, MaxSummary), Goal: Sanitize(s.Goal, MaxDescription),
			State: sprintState(s.State), StartDate: dateOf(s.StartDate), EndDate: dateOf(s.EndDate),
		}
		issues, err := c.sprintIssues(ctx, s.ID)
		if err != nil {
			return nil, err
		}
		for _, is := range issues {
			sp.Issues = append(sp.Issues, toIssue(is))
		}
		out = append(out, sp)
	}
	return out, nil
}

// Versions возвращает версии проекта.
func (c *Client) Versions(ctx context.Context, project string) ([]ports.Version, error) {
	if err := validKey(project); err != nil {
		return nil, err
	}
	var vs []jiraVersion
	if err := c.do(ctx, http.MethodGet, "/rest/api/2/project/"+url.PathEscape(project)+"/versions", nil, nil, &vs); err != nil {
		return nil, err
	}
	out := make([]ports.Version, 0, len(vs))
	for _, v := range vs {
		out = append(out, ports.Version{ID: v.ID, Name: Sanitize(v.Name, MaxSummary), Released: v.Released, ReleaseDate: dateOf(v.ReleaseDate)})
	}
	return out, nil
}

// CreateEpic создаёт эпик и возвращает его ключ. Вызывается только из обработчика outbox.
func (c *Client) CreateEpic(ctx context.Context, project, summary, description, featureRef string) (string, error) {
	if err := validKey(project); err != nil {
		return "", err
	}
	if summary == "" {
		return "", kernel.Invalid("summary", "обязателен")
	}
	fields := map[string]any{
		"project":   map[string]string{"key": project},
		"issuetype": map[string]string{"name": c.fields.EpicIssueType},
		"summary":   summary,
	}
	if description != "" {
		fields["description"] = description
	}
	if c.fields.EpicNameField != "" {
		fields[c.fields.EpicNameField] = summary
	}
	if featureRef != "" {
		if c.fields.FeatureRefField != "" {
			fields[c.fields.FeatureRefField] = featureRef
		} else {
			fields["labels"] = []string{c.fields.FeatureLabelPfx + featureRef}
		}
	}
	var created struct {
		Key string `json:"key"`
	}
	if err := c.do(ctx, http.MethodPost, "/rest/api/2/issue", nil, map[string]any{"fields": fields}, &created); err != nil {
		return "", err
	}
	if created.Key == "" {
		return "", fmt.Errorf("%w: jira не вернула ключ эпика", kernel.ErrUnavailable)
	}
	return created.Key, nil
}

// SetEpicPriority задаёт приоритет эпика.
func (c *Client) SetEpicPriority(ctx context.Context, key, priority string) error {
	if err := validKey(key); err != nil {
		return err
	}
	if priority == "" {
		return kernel.Invalid("priority", "обязателен")
	}
	body := map[string]any{"fields": map[string]any{"priority": map[string]string{"name": priority}}}
	return c.do(ctx, http.MethodPut, "/rest/api/2/issue/"+url.PathEscape(key), nil, body, nil)
}

// LinkEpicToFeature записывает ссылку на фичу в настроенное поле или метку.
func (c *Client) LinkEpicToFeature(ctx context.Context, key, featureRef string) error {
	if err := validKey(key); err != nil {
		return err
	}
	if featureRef == "" {
		return kernel.Invalid("feature_ref", "обязательна")
	}
	var body map[string]any
	if c.fields.FeatureRefField != "" {
		body = map[string]any{"fields": map[string]any{c.fields.FeatureRefField: featureRef}}
	} else {
		body = map[string]any{"update": map[string]any{"labels": []map[string]string{{"add": c.fields.FeatureLabelPfx + featureRef}}}}
	}
	return c.do(ctx, http.MethodPut, "/rest/api/2/issue/"+url.PathEscape(key), nil, body, nil)
}

// --- преобразования ---

func (c *Client) toEpic(is jiraIssue) ports.Epic {
	e := ports.Epic{
		Key:         Sanitize(is.Key, MaxKey),
		Summary:     Sanitize(str(is.Fields["summary"]), MaxSummary),
		Description: Sanitize(str(is.Fields["description"]), MaxDescription),
		Status:      Sanitize(status(is.Fields["status"]).Name, MaxKey),
		Priority:    Sanitize(named(is.Fields["priority"]).Name, MaxKey),
		DueDate:     dateOf(str(is.Fields["duedate"])),
		CreatedAt:   timeOf(str(is.Fields["created"])),
	}
	var fv []jiraNamed
	_ = json.Unmarshal(is.Fields["fixVersions"], &fv)
	for _, v := range fv {
		e.FixVersions = append(e.FixVersions, Sanitize(v.Name, MaxSummary))
	}
	if c.fields.FeatureRefField != "" {
		e.FeatureRef = Sanitize(str(is.Fields[c.fields.FeatureRefField]), MaxKey)
	} else {
		var labels []string
		_ = json.Unmarshal(is.Fields["labels"], &labels)
		for _, l := range labels {
			if strings.HasPrefix(l, c.fields.FeatureLabelPfx) {
				e.FeatureRef = Sanitize(strings.TrimPrefix(l, c.fields.FeatureLabelPfx), MaxKey)
				break
			}
		}
	}
	return e
}

func toIssue(is jiraIssue) ports.Issue {
	st := status(is.Fields["status"])
	it := ports.Issue{
		Key:       Sanitize(is.Key, MaxKey),
		Summary:   Sanitize(str(is.Fields["summary"]), MaxSummary),
		Status:    Sanitize(st.Name, MaxKey),
		Done:      st.StatusCategory.Key == "done",
		CreatedAt: timeOf(str(is.Fields["created"])),
	}
	var closed []jiraSprint
	_ = json.Unmarshal(is.Fields["closedSprints"], &closed)
	for _, s := range closed {
		it.SprintIDs = append(it.SprintIDs, strconv.Itoa(s.ID))
	}
	var cur jiraSprint
	if err := json.Unmarshal(is.Fields["sprint"], &cur); err == nil && cur.ID != 0 {
		it.SprintIDs = append(it.SprintIDs, strconv.Itoa(cur.ID))
	}
	return it
}

func str(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

func named(raw json.RawMessage) jiraNamed {
	var n jiraNamed
	_ = json.Unmarshal(raw, &n)
	return n
}

func status(raw json.RawMessage) jiraStatus {
	var s jiraStatus
	_ = json.Unmarshal(raw, &s)
	return s
}

func dateOf(s string) kernel.Date {
	if s == "" {
		return kernel.Date{}
	}
	if len(s) >= 10 {
		if d, err := kernel.ParseDate(s[:10]); err == nil {
			return d
		}
	}
	return kernel.Date{}
}

func timeOf(s string) time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05.000-0700", time.RFC3339, time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func sprintState(s string) ports.SprintState {
	switch strings.ToLower(s) {
	case "active":
		return ports.SprintActive
	case "closed":
		return ports.SprintClosed
	default:
		return ports.SprintFuture
	}
}

// validKey защищает JQL и пути от инъекции: ключи проекта, эпика и доски — только буквы, цифры, «-», «_».
func validKey(k string) error {
	if k == "" || len(k) > MaxKey {
		return kernel.Invalid("key", "ключ пуст или слишком длинный")
	}
	for _, r := range k {
		ok := r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_'
		if !ok {
			return kernel.Invalid("key", "недопустимый символ в ключе")
		}
	}
	return nil
}
