// Package onec reads explicitly configured OData v3 collections. It does not
// assume a 1C configuration, register, chart of accounts, or field ownership.
package onec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/onixus/metis/internal/adapters/financefile"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

const maxResponseBytes = 8 << 20
const maxTotalBytes = 32 << 20
const maxPages = 100
const maxRows = 10000

// Credentials obtains Basic credentials from the deployment's secret provider.
type Credentials interface {
	Basic(ctx context.Context) (username, password string, err error)
}

type StaticCredentials struct{ username, password string }

func NewStaticCredentials(username, password string) StaticCredentials {
	return StaticCredentials{username: username, password: password}
}
func (c StaticCredentials) Basic(ctx context.Context) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", fmt.Errorf("onec credentials: %w", err)
	}
	if c.username == "" || c.password == "" || strings.Contains(c.username, ":") {
		return "", "", fmt.Errorf("%w: onec credentials are missing or invalid", kernel.ErrForbidden)
	}
	return c.username, c.password, nil
}
func (StaticCredentials) String() string   { return "StaticCredentials{redacted}" }
func (StaticCredentials) GoString() string { return "StaticCredentials{redacted}" }

// Config has no secrets. Fields maps canonical finance fields to scalar OData
// properties. Collection and all fields must be explicitly configured. ProductIDs
// maps external product references to Metis UUIDs; without it source IDs must be
// Metis UUIDs. Currency and Category can supply explicit constant values.
// PeriodFormat is "month" (YYYY-MM, default) or "date" (ISO date/datetime).
type Config struct {
	BaseURL      string               `json:"base_url"`
	Collection   string               `json:"collection"`
	Fields       map[string]string    `json:"fields"`
	ProductIDs   map[string]kernel.ID `json:"product_ids,omitempty"`
	Currency     string               `json:"currency,omitempty"`
	Category     string               `json:"category,omitempty"`
	PeriodFormat string               `json:"period_format,omitempty"`
	SourceName   string               `json:"source_name,omitempty"`
	Filter       string               `json:"filter,omitempty"`
}

type Client struct {
	collection                                           *url.URL
	http                                                 *http.Client
	creds                                                Credentials
	fields                                               map[string]string
	products                                             map[string]kernel.ID
	currency, category, periodFormat, sourceName, filter string
}

var _ ports.FinanceSourceReader = (*Client)(nil)

func New(config Config, creds Credentials, httpClient *http.Client) (*Client, error) {
	u, err := url.Parse(config.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, validation("invalid base URL")
	}
	if u.Scheme == "http" && !loopbackHost(u.Hostname()) {
		return nil, validation("HTTPS is required for non-loopback financial sources")
	}
	if !identifier(config.Collection) {
		return nil, validation("an explicit collection identifier is required")
	}
	if creds == nil {
		return nil, validation("credentials provider is required")
	}
	fields := make(map[string]string, len(config.Fields))
	allowed := map[string]bool{}
	for _, name := range canonicalFields() {
		allowed[name] = true
	}
	for name, field := range config.Fields {
		if !allowed[name] || !identifier(field) {
			return nil, validation("invalid field mapping")
		}
		fields[name] = field
	}
	for _, name := range []string{"product_id", "period"} {
		if fields[name] == "" {
			return nil, validation("product_id and period mappings are required")
		}
	}
	if (fields["amount"] == "") == (fields["amount_minor"] == "") {
		return nil, validation("map exactly one of amount and amount_minor")
	}
	if fields["currency"] == "" && config.Currency == "" {
		return nil, validation("currency mapping or constant is required")
	}
	if fields["category"] == "" && config.Category == "" {
		return nil, validation("category mapping or constant is required")
	}
	if config.PeriodFormat == "" {
		config.PeriodFormat = "month"
	}
	if config.PeriodFormat != "month" && config.PeriodFormat != "date" {
		return nil, validation("period format must be month or date")
	}
	if config.SourceName == "" {
		config.SourceName = "1c-odata"
	}
	if len(config.SourceName) > 128 || strings.IndexFunc(config.SourceName, unicode.IsControl) >= 0 || len(config.Filter) > 4096 {
		return nil, validation("invalid source name or filter")
	}
	products := make(map[string]kernel.ID, len(config.ProductIDs))
	for external, id := range config.ProductIDs {
		if external == "" || id == kernel.NilID {
			return nil, validation("product mapping contains an empty reference")
		}
		products[external] = id
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + config.Collection
	u.RawPath = ""
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	copyClient := *httpClient
	if copyClient.Timeout == 0 {
		copyClient.Timeout = 20 * time.Second
	}
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{collection: u, http: &copyClient, creds: creds, fields: fields, products: products, currency: config.Currency, category: config.Category, periodFormat: config.PeriodFormat, sourceName: config.SourceName, filter: config.Filter}, nil
}

func validation(message string) error {
	return fmt.Errorf("%w: onec: %s", kernel.ErrValidation, message)
}

func identifier(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	for i, c := range s {
		if !unicode.IsLetter(c) && c != '_' && (i == 0 || !unicode.IsDigit(c)) {
			return false
		}
	}
	return true
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func canonicalFields() []string {
	return []string{"product_id", "period", "category", "amount", "amount_minor", "currency", "team_id", "headcount", "feature_id", "certification_track_id", "branch", "bundle_id", "description"}
}

func (c *Client) Read(ctx context.Context) (ports.FinancePreview, error) {
	current := *c.collection
	query := current.Query()
	query.Set("$format", "json")
	query.Set("$top", "1000")
	if c.filter != "" {
		query.Set("$filter", c.filter)
	}
	selected := make([]string, 0, len(c.fields))
	seenFields := make(map[string]bool)
	for _, field := range c.fields {
		if !seenFields[field] {
			selected = append(selected, field)
			seenFields[field] = true
		}
	}
	sort.Strings(selected)
	query.Set("$select", strings.Join(selected, ","))
	current.RawQuery = query.Encode()
	visited := make(map[string]bool)
	var records []map[string]any
	hasher := sha256.New()
	total := 0
	for page := 0; ; page++ {
		if page >= maxPages || visited[current.String()] {
			return ports.FinancePreview{}, validation("pagination limit or cycle")
		}
		visited[current.String()] = true
		body, err := c.request(ctx, &current)
		if err != nil {
			return ports.FinancePreview{}, err
		}
		total += len(body)
		if total > maxTotalBytes {
			return ports.FinancePreview{}, validation("snapshot size limit exceeded")
		}
		_, _ = fmt.Fprintf(hasher, "%d:", len(body))
		_, _ = hasher.Write(body)
		rows, next, err := decodePage(body)
		if err != nil {
			return ports.FinancePreview{}, err
		}
		if len(records)+len(rows) > maxRows {
			return ports.FinancePreview{}, validation("row limit exceeded")
		}
		records = append(records, rows...)
		if next == "" {
			break
		}
		u, err := url.Parse(next)
		if err != nil {
			return ports.FinancePreview{}, validation("invalid continuation link")
		}
		u = current.ResolveReference(u)
		if !strings.EqualFold(u.Scheme, c.collection.Scheme) || !strings.EqualFold(u.Host, c.collection.Host) || u.User != nil || u.Fragment != "" || u.EscapedPath() != c.collection.EscapedPath() || u.Opaque != "" {
			return ports.FinancePreview{}, validation("continuation must remain on the configured collection and origin")
		}
		current = *u
	}
	rawHash := hex.EncodeToString(hasher.Sum(nil))
	preview, err := c.preview(ctx, records, rawHash)
	if err != nil {
		return preview, err
	}
	metadata, err := json.Marshal(struct {
		RawHash string
		Config  Config
	}{RawHash: rawHash, Config: Config{BaseURL: c.collection.String(), Fields: c.fields, ProductIDs: c.products, Currency: c.currency, Category: c.category, PeriodFormat: c.periodFormat, SourceName: c.sourceName, Filter: c.filter}})
	if err != nil {
		return ports.FinancePreview{}, fmt.Errorf("onec fingerprint: %w", err)
	}
	fingerprint := sha256.Sum256(metadata)
	preview.SourceHash = hex.EncodeToString(fingerprint[:])
	preview.SourceHashes = []string{rawHash}
	return preview, nil
}

func (c *Client) request(ctx context.Context, u *url.URL) ([]byte, error) {
	username, password, err := c.creds.Basic(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("onec credentials: %w", ctx.Err())
		}
		return nil, fmt.Errorf("%w: onec credentials unavailable", kernel.ErrForbidden)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, validation("request construction failed")
	}
	req.SetBasicAuth(username, password)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("DataServiceVersion", "3.0")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: onec request: %w", kernel.ErrUnavailable, ctx.Err())
		}
		return nil, fmt.Errorf("%w: onec transport failed", kernel.ErrUnavailable)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: onec HTTP status %d", kernel.ErrUnavailable, resp.StatusCode)
	}
	if resp.ContentLength > maxResponseBytes {
		return nil, validation("response size limit exceeded")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: onec response read failed", kernel.ErrUnavailable)
	}
	if len(body) > maxResponseBytes {
		return nil, validation("response size limit exceeded")
	}
	return body, nil
}

func decodePage(body []byte) ([]map[string]any, string, error) {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var envelope map[string]any
	if err := d.Decode(&envelope); err != nil {
		return nil, "", validation("invalid OData JSON")
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, "", validation("trailing OData JSON")
	}
	var raw any
	var next string
	if payload, ok := envelope["d"]; ok {
		if object, ok := payload.(map[string]any); ok {
			raw = object["results"]
			var err error
			next, err = continuation(object, "__next")
			if err != nil {
				return nil, "", err
			}
		} else {
			raw = payload
		}
	} else {
		raw = envelope["value"]
		var err error
		next, err = continuation(envelope, "odata.nextLink")
		if err != nil {
			return nil, "", err
		}
		if next == "" {
			next, err = continuation(envelope, "@odata.nextLink")
			if err != nil {
				return nil, "", err
			}
		}
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, "", validation("expected an OData collection")
	}
	if len(items) > maxRows {
		return nil, "", validation("row limit exceeded")
	}
	rows := make([]map[string]any, 0, len(items))
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			return nil, "", validation("expected an OData record")
		}
		rows = append(rows, row)
	}
	return rows, next, nil
}

func continuation(object map[string]any, key string) (string, error) {
	raw, ok := object[key]
	if !ok || raw == nil {
		return "", nil
	}
	next, ok := raw.(string)
	if !ok || len(next) > 8192 {
		return "", validation("invalid continuation link")
	}
	return next, nil
}

func (c *Client) preview(ctx context.Context, records []map[string]any, hash string) (ports.FinancePreview, error) {
	base := ports.FinancePreview{Rows: []ports.FinanceRow{}, Errors: []ports.FinanceRowError{}, SourceHash: hash, Sheet: c.collection.Path[strings.LastIndex(c.collection.Path, "/")+1:]}
	if len(records) == 0 {
		return base, nil
	}
	fields := []string{}
	for _, field := range canonicalFields() {
		if c.fields[field] != "" || (field == "currency" && c.currency != "") || (field == "category" && c.category != "") {
			fields = append(fields, field)
		}
	}
	var buffer bytes.Buffer
	w := csv.NewWriter(&buffer)
	if err := w.Write(fields); err != nil {
		return base, validation("cannot normalize source records")
	}
	physicalToSource := make(map[int]int, len(records))
	physicalRow := 2
	for i, record := range records {
		if err := ctx.Err(); err != nil {
			return base, fmt.Errorf("onec: %w", err)
		}
		values := make([]string, len(fields))
		for j, field := range fields {
			var value string
			switch raw := record[c.fields[field]].(type) {
			case nil:
			case string:
				value = raw
			case json.Number:
				value = raw.String()
			default:
				base.Errors = append(base.Errors, ports.FinanceRowError{Row: i + 1, Field: field, Message: "expected a scalar string or number"})
			}
			if c.fields[field] == "" && field == "currency" {
				value = c.currency
			}
			if c.fields[field] == "" && field == "category" {
				value = c.category
			}
			if field == "product_id" && len(c.products) > 0 {
				id, ok := c.products[value]
				if !ok {
					base.Errors = append(base.Errors, ports.FinanceRowError{Row: i + 1, Field: field, Message: "external product reference has no configured mapping"})
					value = ""
				} else {
					value = id.String()
				}
			}
			if field == "period" && c.periodFormat == "date" {
				var err error
				value, err = dateMonth(value)
				if err != nil {
					base.Errors = append(base.Errors, ports.FinanceRowError{Row: i + 1, Field: field, Message: "expected an ISO date or datetime"})
				}
			}
			values[j] = value
		}
		if err := w.Write(values); err != nil {
			return base, validation("cannot normalize source record")
		}
		physicalToSource[physicalRow] = i + 1
		physicalRow++
		for _, value := range values {
			physicalRow += strings.Count(value, "\n")
		}
		if buffer.Len() > financefile.MaxFileSize {
			return base, validation("normalized source size limit exceeded")
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return base, validation("cannot normalize source records")
	}
	p, err := financefile.New().Parse(ctx, "1c-odata.csv", buffer.Bytes(), ports.FinanceTemplate{})
	if err != nil {
		return base, err
	}
	for _, rowError := range p.Errors {
		rowError.Row = physicalToSource[rowError.Row]
		base.Errors = append(base.Errors, rowError)
	}
	for _, row := range p.Rows {
		row.Source = ports.FinanceSource{File: c.sourceName, Sheet: base.Sheet, Row: physicalToSource[row.Source.Row], Hash: hash}
		base.Rows = append(base.Rows, row)
	}
	return base, nil
}

func dateMonth(value string) (string, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02"} {
		if date, err := time.Parse(layout, value); err == nil && date.Year() > 0 {
			return date.Format("2006-01"), nil
		}
	}
	return "", validation("invalid date")
}
