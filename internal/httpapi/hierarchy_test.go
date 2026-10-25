package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	pg "github.com/onixus/metis/internal/portfoliograph"
)

func TestPG02_HierarchyHTTPReadsAndPrivateBoundary(t *testing.T) {
	ctx, sc := context.Background(), cpoScope()
	svc := pg.NewService(pg.NewMemStore(), nil, kernel.SystemClock{})
	p, err := svc.CreateProduct(ctx, sc, pg.ProductInput{Key: "hierarchy", Name: "Synthetic hierarchy", Type: pg.ProductTypeOther})
	if err != nil {
		t.Fatal(err)
	}
	cap, err := svc.CreateCapability(ctx, sc, p.ID, "Capability")
	if err != nil {
		t.Fatal(err)
	}
	f, err := svc.CreateFeature(ctx, sc, p.ID, pg.FeatureInput{Name: "Feature", CapabilityID: cap.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateRequirement(ctx, sc, f.ID, "Requirement"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/products/" + p.ID.String() + "/capabilities", "/api/v1/features/" + f.ID.String() + "/requirements"} {
		for _, access := range []authz.Access{authz.AccessPrivate, authz.AccessStrategic, authz.AccessNone} {
			readScope := authz.New(authz.Params{Subject: "pm", Roles: []authz.Role{authz.RolePM}, Products: map[kernel.ID]authz.Access{p.ID: access}})
			handler := NewServer(Deps{Portfolio: svc, Instrument: withScope(readScope)}).Handler()
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if access == authz.AccessPrivate {
				if rec.Code != 200 {
					t.Fatalf("%s private status=%d %s", path, rec.Code, rec.Body.String())
				}
				var got []map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 1 {
					t.Fatalf("%s payload=%s err=%v", path, rec.Body.String(), err)
				}
			} else if rec.Code != 403 {
				t.Fatalf("%s denied access=%v status=%d %s", path, access, rec.Code, rec.Body.String())
			}
		}
	}
}

func TestPG10_StrategicSliceIncludesContractReadyDate(t *testing.T) {
	ctx, sc := context.Background(), cpoScope()
	svc := pg.NewService(pg.NewMemStore(), nil, kernel.SystemClock{})
	p, err := svc.CreateProduct(ctx, sc, pg.ProductInput{Key: "provider", Name: "Provider", Type: pg.ProductTypeOther})
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateProduct(ctx, sc, pg.ProductInput{Key: "consumer", Name: "Consumer", Type: pg.ProductTypeOther})
	if err != nil {
		t.Fatal(err)
	}
	f, err := svc.CreateFeature(ctx, sc, p.ID, pg.FeatureInput{Name: "API", PlannedDate: kernel.DateOf(2026, 12, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveContract(ctx, sc, kernel.NilID, pg.ContractInput{Name: "Response", ProviderProductID: p.ID, ConsumerProductID: c.ID, ProviderFeatureIDs: []kernel.ID{f.ID}, Criticality: pg.CritBlocks}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(Deps{Portfolio: svc, Instrument: withScope(sc)}).Handler()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/products/"+p.ID.String()+"/strategic", nil))
	if rec.Code != 200 {
		t.Fatalf("strategic HTTP %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Contracts []struct {
			ReadyDate string `json:"ready_date"`
		} `json:"contracts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Contracts) != 1 || body.Contracts[0].ReadyDate != "2026-12-01" {
		t.Fatalf("strategic contracts=%s err=%v", rec.Body.String(), err)
	}
}
