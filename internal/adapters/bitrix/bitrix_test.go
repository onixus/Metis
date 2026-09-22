package bitrix_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onixus/metis/internal/adapters/bitrix"
	"github.com/onixus/metis/internal/kernel"
)

func fields() bitrix.Fields {
	f := bitrix.DefaultFields()
	f.Deals["product_key"] = "UF_CRM_METIS_PRODUCT"
	f.Deals["requested_features"] = "UF_CRM_METIS_REQUESTS"
	f.Deals["blocks_on_features"] = "UF_CRM_METIS_BLOCKS"
	return f
}

func TestSG01_SG03_Bitrix24BoxReadOnlyContract(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected credentials/method: %s %s", r.Method, r.URL.Path)
		}
		var in struct {
			Auth   string   `json:"auth"`
			Start  int      `json:"start"`
			Select []string `json:"select"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		if in.Auth != "synthetic-oauth" {
			t.Error("missing OAuth auth body")
		}
		if r.URL.Path == "/rest/crm.company.list.json" {
			_, _ = fmt.Fprint(w, `{"result":[{"ID":"1","TITLE":"Synthetic company","INDUSTRY":"IT"}]}`)
			return
		}
		if r.URL.Path != "/rest/crm.deal.list.json" {
			t.Errorf("write or unknown method: %s", r.URL.Path)
		}
		if !strings.Contains(strings.Join(in.Select, ","), "UF_CRM_METIS_PRODUCT") {
			t.Error("custom field not selected")
		}
		if in.Start == 0 {
			_, _ = fmt.Fprint(w, `{"result":[{"ID":"11","COMPANY_ID":"1","TITLE":"Synthetic deal","STAGE_ID":"NEW","OPPORTUNITY":"1234567890123.45","CURRENCY_ID":"RUB","CLOSEDATE":"2026-09-30T00:00:00+03:00","UF_CRM_METIS_PRODUCT":"EDR","UF_CRM_METIS_REQUESTS":["Feature A","Feature B"],"UF_CRM_METIS_BLOCKS":"Y"}],"next":50}`)
			return
		}
		if in.Start != 50 {
			t.Errorf("cursor: %d", in.Start)
		}
		_, _ = fmt.Fprint(w, `{"result":[{"ID":"12","COMPANY_ID":"1","OPPORTUNITY":"0.01","CURRENCY_ID":"RUB","UF_CRM_METIS_PRODUCT":"SOAR","UF_CRM_METIS_REQUESTS":"A; B"}]}`)
	}))
	defer srv.Close()
	c, err := bitrix.New(srv.URL, bitrix.NewStaticToken("synthetic-oauth"), fields(), srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := c.Accounts(context.Background())
	if err != nil || len(accounts) != 1 || accounts[0].ExternalID != "1" {
		t.Fatalf("accounts: %+v %v", accounts, err)
	}
	deals, err := c.Deals(context.Background())
	if err != nil || len(deals) != 2 {
		t.Fatalf("deals: %+v %v", deals, err)
	}
	if deals[0].Amount.Amount != 123456789012345 || deals[1].Amount.Amount != 1 || deals[0].ProductKey != "EDR" || !deals[0].BlocksOnFeatures || len(deals[0].RequestedFeatures) != 2 || deals[0].ExpectedDate.String() != "2026-09-30" {
		t.Fatalf("mapping: %+v", deals)
	}
	if requests != 3 {
		t.Fatalf("requests=%d", requests)
	}
}

func TestSG01_NFS17_BitrixRejectsMalformedOrPartialPages(t *testing.T) {
	for name, body := range map[string]string{
		"cursor":        `{"result":[{"ID":"1"}],"next":0}`,
		"missing-id":    `{"result":[{"TITLE":"x"}]}`,
		"duplicate-id":  `{"result":[{"ID":"1"},{"ID":"1"}]}`,
		"API-error":     `{"error":"TOKEN","error_description":"synthetic-oauth"}`,
		"precision":     `{"result":[{"ID":"1","OPPORTUNITY":"0.001","CURRENCY_ID":"RUB"}]}`,
		"overflow":      `{"result":[{"ID":"1","OPPORTUNITY":"9223372036854775808","CURRENCY_ID":"RUB"}]}`,
		"unknown-scale": `{"result":[{"ID":"1","OPPORTUNITY":"1","CURRENCY_ID":"JPY"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body) }))
			defer srv.Close()
			c, err := bitrix.New(srv.URL, bitrix.NewStaticToken("synthetic-oauth"), fields(), srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.Deals(context.Background())
			if err == nil || got != nil {
				t.Fatalf("accepted partial input: %+v %v", got, err)
			}
			if strings.Contains(err.Error(), "synthetic-oauth") || strings.Contains(err.Error(), "922337") {
				t.Fatal("remote sensitive value leaked")
			}
		})
	}
}

func TestNFS04_NFS17_BitrixDoesNotForwardCredentialsOrRemoteErrors(t *testing.T) {
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c, err := bitrix.New(srv.URL, bitrix.NewStaticToken("synthetic-oauth"), fields(), srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Deals(context.Background()); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("redirect error: %v", err)
	}
	if called {
		t.Fatal("credentials forwarded")
	}
	for _, base := range []string{"https://host.test/rest/1/secret/", "https://user:secret@host.test", "https://host.test?auth=secret"} {
		if _, err := bitrix.New(base, bitrix.NewStaticToken("x"), fields(), nil); !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("unsafe base accepted: %v", err)
		}
	}
	if _, err := bitrix.New(srv.URL, bitrix.NewStaticToken("x"), bitrix.DefaultFields(), nil); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("missing product mapping accepted: %v", err)
	}
	if strings.Contains(fmt.Sprint(bitrix.NewStaticToken("synthetic-oauth")), "synthetic-oauth") {
		t.Fatal("token printed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Deals(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("context: %v", err)
	}
}

func TestNFS17_BitrixResponseLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, strings.Repeat(" ", bitrix.MaxResponseBytes+1))
	}))
	defer srv.Close()
	c, err := bitrix.New(srv.URL, bitrix.NewStaticToken("x"), fields(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Accounts(context.Background()); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("oversized response: %v", err)
	}
}
