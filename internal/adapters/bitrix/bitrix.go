// Package bitrix implements the read-only CRM port using Bitrix24 REST list methods.
// OAuth credentials are sent in the request body, never in URLs or returned errors.
package bitrix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

const (
	MaxResponseBytes = 4 << 20
	MaxPages         = 200
	MaxRecords       = 10000
)

type TokenSource interface {
	Token(context.Context) (string, error)
}
type StaticToken struct{ value string }

func NewStaticToken(value string) StaticToken { return StaticToken{value: value} }
func (StaticToken) String() string            { return "StaticToken{***}" }
func (t StaticToken) Token(context.Context) (string, error) {
	if t.value == "" {
		return "", fmt.Errorf("%w: Bitrix24 token is required", kernel.ErrForbidden)
	}
	return t.value, nil
}

// Fields maps neutral port fields to the installation's REST field names.
// ProductKey is required; no customer-specific UF_CRM field is guessed.
type Fields struct {
	Accounts      map[string]string `json:"accounts"`
	Deals         map[string]string `json:"deals"`
	CurrencyScale map[string]int32  `json:"currency_scale"`
}

func DefaultFields() Fields {
	return Fields{
		Accounts:      map[string]string{"id": "ID", "name": "TITLE", "segment": "INDUSTRY"},
		Deals:         map[string]string{"id": "ID", "account_id": "COMPANY_ID", "name": "TITLE", "stage": "STAGE_ID", "amount": "OPPORTUNITY", "currency": "CURRENCY_ID", "expected_date": "CLOSEDATE", "notes": "COMMENTS"},
		CurrencyScale: map[string]int32{"RUB": 2, "USD": 2, "EUR": 2},
	}
}

type Client struct {
	base   *url.URL
	http   *http.Client
	token  TokenSource
	fields Fields
}

var _ ports.CRM = (*Client)(nil)

func New(baseURL string, token TokenSource, fields Fields, client *http.Client) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, kernel.Invalid("bitrix_base_url", "expected an HTTP(S) origin without credentials or query")
	}
	if path := strings.TrimRight(u.Path, "/"); path != "" && path != "/rest" {
		return nil, kernel.Invalid("bitrix_base_url", "use the portal origin or /rest; incoming webhook URLs are not accepted")
	}
	if token == nil {
		return nil, kernel.Invalid("bitrix_token", "required")
	}
	if err := validateFields(fields); err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	copyClient := *client
	// Deny all redirects, including same-origin ones: a POST body contains the token.
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: u, http: &copyClient, token: token, fields: cloneFields(fields)}, nil
}

func cloneFields(f Fields) Fields {
	out := Fields{Accounts: map[string]string{}, Deals: map[string]string{}, CurrencyScale: map[string]int32{}}
	for k, v := range f.Accounts {
		out.Accounts[k] = v
	}
	for k, v := range f.Deals {
		out.Deals[k] = v
	}
	for k, v := range f.CurrencyScale {
		out.CurrencyScale[k] = v
	}
	return out
}

func validateFields(f Fields) error {
	allowedAccounts := map[string]bool{"id": true, "name": true, "segment": true, "arr": true, "currency": true}
	allowedDeals := map[string]bool{"id": true, "account_id": true, "name": true, "stage": true, "amount": true, "currency": true, "product_key": true, "regulatory": true, "version": true, "expected_date": true, "requested_features": true, "notes": true, "blocks_on_features": true}
	valid := regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
	for _, group := range []struct {
		fields  map[string]string
		allowed map[string]bool
	}{{f.Accounts, allowedAccounts}, {f.Deals, allowedDeals}} {
		for k, v := range group.fields {
			if !group.allowed[k] || !valid.MatchString(v) {
				return kernel.Invalid("bitrix_fields", "unknown target or invalid REST field name")
			}
		}
	}
	for _, key := range []string{"id", "name"} {
		if f.Accounts[key] == "" {
			return kernel.Invalid("bitrix_accounts", "id and name mappings are required")
		}
	}
	for _, key := range []string{"id", "account_id", "product_key", "amount", "currency"} {
		if f.Deals[key] == "" {
			return kernel.Invalid("bitrix_deals", "id, account_id, product_key, amount and currency mappings are required")
		}
	}
	if f.Accounts["arr"] != "" && f.Accounts["currency"] == "" {
		return kernel.Invalid("bitrix_accounts", "ARR requires a currency mapping")
	}
	for currency, scale := range f.CurrencyScale {
		if len(currency) != 3 || strings.ToUpper(currency) != currency || scale < 0 || scale > 6 {
			return kernel.Invalid("currency_scale", "expected ISO currency and scale from 0 to 6")
		}
	}
	return nil
}

func (c *Client) list(ctx context.Context, method string, fields map[string]string) ([]map[string]json.RawMessage, error) {
	token, err := c.token.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: Bitrix24 credentials unavailable", kernel.ErrForbidden)
	}
	if token == "" {
		return nil, fmt.Errorf("%w: Bitrix24 token is required", kernel.ErrForbidden)
	}
	selectFields := make([]string, 0, len(fields))
	for _, f := range fields {
		selectFields = append(selectFields, f)
	}
	sort.Strings(selectFields)
	u := *c.base
	u.Path = "/rest/" + method + ".json"
	var out []map[string]json.RawMessage
	seen := map[string]bool{}
	readBytes := 0
	for start, pages := 0, 0; ; pages++ {
		if pages >= MaxPages {
			return nil, kernel.Invalid("bitrix_pagination", "page limit exceeded")
		}
		raw, err := json.Marshal(map[string]any{"auth": token, "start": start, "order": map[string]string{"ID": "ASC"}, "select": selectFields})
		if err != nil {
			return nil, fmt.Errorf("bitrix request: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("%w: Bitrix24 request failed", kernel.ErrUnavailable)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("bitrix request: %w", ctx.Err())
			}
			return nil, fmt.Errorf("%w: Bitrix24 transport failed", kernel.ErrUnavailable)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
		_ = resp.Body.Close()
		readBytes += len(body)
		if readBytes > 32<<20 {
			return nil, kernel.Invalid("bitrix_response", "combined response limit exceeded")
		}
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return nil, fmt.Errorf("%w: Bitrix24 authorization failed", kernel.ErrForbidden)
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, fmt.Errorf("%w: Bitrix24 HTTP %d", kernel.ErrUnavailable, resp.StatusCode)
		}
		if readErr != nil || len(body) > MaxResponseBytes {
			return nil, fmt.Errorf("%w: Bitrix24 response exceeds limit or is unreadable", kernel.ErrUnavailable)
		}
		var page struct {
			Result []map[string]json.RawMessage `json:"result"`
			Next   *int                         `json:"next"`
			Error  string                       `json:"error"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("%w: invalid Bitrix24 response", kernel.ErrUnavailable)
		}
		if page.Error != "" {
			return nil, fmt.Errorf("%w: Bitrix24 rejected list request", kernel.ErrUnavailable)
		}
		for _, row := range page.Result {
			id, err := stringField(row, fields["id"])
			if err != nil || id == "" || seen[id] {
				return nil, kernel.Invalid("bitrix_pagination", "missing or repeated record identifier")
			}
			seen[id] = true
			out = append(out, row)
			if len(out) > MaxRecords {
				return nil, kernel.Invalid("bitrix_records", "record limit exceeded")
			}
		}
		if page.Next == nil {
			return out, nil
		}
		if *page.Next <= start || len(page.Result) == 0 {
			return nil, kernel.Invalid("bitrix_pagination", "non-advancing cursor")
		}
		start = *page.Next
	}
}

func stringField(row map[string]json.RawMessage, key string) (string, error) {
	if key == "" || len(row[key]) == 0 || string(row[key]) == "null" {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(row[key], &s); err != nil {
		var n json.Number
		if err := json.Unmarshal(row[key], &n); err != nil {
			return "", kernel.Invalid("bitrix_field", "expected scalar string or number")
		}
		s = n.String()
	}
	if len(s) > 16384 {
		return "", kernel.Invalid("bitrix_field", "value exceeds limit")
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s), nil
}

func values(row map[string]json.RawMessage, fields map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for k, f := range fields {
		if k == "requested_features" || k == "blocks_on_features" {
			continue
		}
		s, err := stringField(row, f)
		if err != nil {
			return nil, err
		}
		out[k] = s
	}
	return out, nil
}

func (c *Client) money(amount, currency string) (kernel.Money, error) {
	if amount == "" {
		return kernel.Money{}, nil
	}
	scale, ok := c.fields.CurrencyScale[currency]
	if !ok {
		return kernel.Money{}, kernel.Invalid("currency", "currency scale is not configured")
	}
	if len(amount) > 40 || !regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`).MatchString(amount) {
		return kernel.Money{}, kernel.Invalid("amount", "invalid decimal amount")
	}
	d, err := decimal.NewFromString(amount)
	if err != nil {
		return kernel.Money{}, kernel.Invalid("amount", "invalid decimal amount")
	}
	minor := d.Shift(scale)
	if !minor.IsInteger() || !minor.BigInt().IsInt64() {
		return kernel.Money{}, kernel.Invalid("amount", "precision or range exceeds currency minor units")
	}
	return kernel.Money{Amount: minor.IntPart(), Currency: currency}, nil
}

func (c *Client) Accounts(ctx context.Context) ([]ports.Account, error) {
	rows, err := c.list(ctx, "crm.company.list", c.fields.Accounts)
	if err != nil {
		return nil, err
	}
	out := make([]ports.Account, 0, len(rows))
	for _, row := range rows {
		v, err := values(row, c.fields.Accounts)
		if err != nil {
			return nil, err
		}
		money, err := c.money(v["arr"], v["currency"])
		if err != nil {
			return nil, err
		}
		out = append(out, ports.Account{ExternalID: v["id"], Name: v["name"], Segment: v["segment"], ARR: money})
	}
	return out, nil
}

func (c *Client) Deals(ctx context.Context) ([]ports.Deal, error) {
	rows, err := c.list(ctx, "crm.deal.list", c.fields.Deals)
	if err != nil {
		return nil, err
	}
	out := make([]ports.Deal, 0, len(rows))
	for _, row := range rows {
		v, err := values(row, c.fields.Deals)
		if err != nil {
			return nil, err
		}
		money, err := c.money(v["amount"], v["currency"])
		if err != nil {
			return nil, err
		}
		var date kernel.Date
		if s := v["expected_date"]; s != "" {
			if len(s) > 10 {
				parsed, e := time.Parse(time.RFC3339, s)
				if e != nil {
					return nil, kernel.Invalid("expected_date", "invalid date")
				}
				date = kernel.DateOf(parsed.Year(), parsed.Month(), parsed.Day())
			} else {
				date, err = kernel.ParseDate(s)
				if err != nil {
					return nil, kernel.Invalid("expected_date", "invalid date")
				}
			}
		}
		requests, err := featureList(row[c.fields.Deals["requested_features"]])
		if err != nil {
			return nil, err
		}
		blocks := false
		if raw := row[c.fields.Deals["blocks_on_features"]]; len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &blocks); err != nil {
				s, e := stringField(row, c.fields.Deals["blocks_on_features"])
				if e != nil {
					return nil, e
				}
				switch strings.ToUpper(s) {
				case "Y", "1", "TRUE":
					blocks = true
				case "N", "0", "FALSE", "":
				default:
					return nil, kernel.Invalid("blocks_on_features", "invalid boolean")
				}
			}
		}
		out = append(out, ports.Deal{ExternalID: v["id"], AccountID: v["account_id"], Name: v["name"], Stage: ports.DealStage(v["stage"]), Amount: money, ProductKey: v["product_key"], Regulatory: v["regulatory"], Version: v["version"], ExpectedDate: date, RequestedFeatures: requests, Notes: v["notes"], BlocksOnFeatures: blocks})
	}
	return out, nil
}

func featureList(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, kernel.Invalid("requested_features", "expected string or string array")
		}
		list = strings.Split(s, ";")
	}
	if len(list) > 1000 {
		return nil, kernel.Invalid("requested_features", "too many requests")
	}
	out := make([]string, 0, len(list))
	for _, s := range list {
		if len(s) > 16384 {
			return nil, kernel.Invalid("requested_features", "request exceeds limit")
		}
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}
