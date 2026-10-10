//go:build integration

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	"github.com/xuri/excelize/v2"
)

func modelingRequest(t *testing.T, a *App, method, path string, body []byte, finance string) *httptest.ResponseRecorder {
	t.Helper()
	token, err := identityaccess.MintHS256([]byte(runtimeTestSecret), "runtime-test", "synthetic-finance", []string{"finance"}, nil, finance, time.Hour, kernel.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Handler.ServeHTTP(w, req)
	return w
}

func TestEC07_EC08_EC09_EC11_EC12_NFS02_ModelingAPIWorkerRestart(t *testing.T) {
	api, reader, owner := runtimeApps(t)
	ctx := context.Background()
	key := "runtime_" + strings.ReplaceAll(kernel.NewID().String(), "-", "_")
	w := modelingRequest(t, api, "POST", "/economics/fields", []byte(fmt.Sprintf(`{"key":%q,"name":"Synthetic","type":"number","source":"import"}`, key)), "full")
	if w.Code != 200 {
		t.Fatalf("field: %d %s", w.Code, w.Body.String())
	}
	w = modelingRequest(t, reader, "GET", "/economics/fields", nil, "full")
	if w.Code != 200 || !strings.Contains(w.Body.String(), key) {
		t.Fatalf("second API: %d %s", w.Code, w.Body.String())
	}
	tplName := "Synthetic " + kernel.NewID().String()
	body := fmt.Sprintf(`{"name":%q,"sheets":[{"sheet":"Sheet1","header_row":1,"period_column":"period","columns":[{"column":"value","field_key":%q}]}]}`, tplName, key)
	w = modelingRequest(t, api, "POST", "/economics/templates", []byte(body), "full")
	if w.Code != 200 {
		t.Fatalf("template: %d %s", w.Code, w.Body.String())
	}
	var tpl economics.Template
	if err := json.Unmarshal(w.Body.Bytes(), &tpl); err != nil {
		t.Fatal(err)
	}
	file := excelize.NewFile()
	t.Cleanup(func() { _ = file.Close() })
	for cell, value := range map[string]any{"A1": "period", "B1": "value", "A2": "2094-01", "B2": "123.45"} {
		if err := file.SetCellValue("Sheet1", cell, value); err != nil {
			t.Fatal(err)
		}
	}
	data, err := file.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	w = modelingRequest(t, api, "POST", "/economics/imports?templateId="+tpl.ID.String(), data.Bytes(), "full")
	if w.Code != 200 {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	cfg := api.Cfg
	cfg.FinanceDir = t.TempDir()
	cfg.FinanceTemplate = tplName
	if err := file.SaveAs(filepath.Join(cfg.FinanceDir, "synthetic.xlsx")); err != nil {
		t.Fatal(err)
	}
	worker := postgresWorker(t, cfg)
	sc := identityaccess.FinanceServiceScope("runtime-test")
	templates, err := worker.Modeling.Templates(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range templates {
		if v.ID == tpl.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("worker cannot see API template")
	}
	batches, err := worker.RunFinanceImports(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) > 1 {
		t.Fatalf("scheduled batches: %v", batches)
	}
	batches, err = worker.RunFinanceImports(ctx)
	if err != nil || len(batches) != 0 {
		t.Fatalf("scheduled dedup: %+v %v", batches, err)
	}
	restarted := postgresWorker(t, api.Cfg)
	rows, err := restarted.Modeling.Facts(ctx, sc, economics.FactFilter{FieldKey: key})
	if err != nil || len(rows) != 1 || rows[0].Value.String() != "123.45" {
		t.Fatalf("restart facts: %+v %v", rows, err)
	}
	w = modelingRequest(t, api, "GET", "/economics/imports", nil, "aggregates")
	if w.Code != 403 {
		t.Fatalf("aggregate import history: %d %s", w.Code, w.Body.String())
	}
	// Test the actual restricted application role's SQL history permissions.
	var update, remove bool
	err = owner.Pool().QueryRow(ctx, "SELECT has_table_privilege('metis_app','modeling.documents','UPDATE'), has_table_privilege('metis_app','modeling.documents','DELETE')").Scan(&update, &remove)
	if err != nil || update || remove {
		t.Fatalf("mutable financial history: %t %t %v", update, remove, err)
	}
}
