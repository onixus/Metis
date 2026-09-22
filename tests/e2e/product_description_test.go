package e2e

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/onixus/metis/internal/app"
	"github.com/onixus/metis/tests/e2e/client"
)

func TestPG01_NFS01_ProductDescriptionHTTP(t *testing.T) {
	ctx := context.Background()
	a, err := app.Build(ctx, app.Config{Storage: "memory", AuthMode: "hmac", HMACSecret: secret, HMACIssuer: issuer, OTelExport: "none"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close(ctx) }()
	srv := httptest.NewServer(a.Handler)
	defer srv.Close()
	c := financeClient(t, srv.URL, []string{"cpo"}, "none")
	description := "Назначение\n<script>не исполняется</script>"
	body := client.CreateProductJSONRequestBody{Key: "description", Name: "Synthetic", Type: "security", Description: &description}
	p := must(c.CreateProductWithResponse(ctx, body))
	if p.JSON201 == nil || p.JSON201.Description == nil || *p.JSON201.Description != description {
		t.Fatalf("create: %s", p.Body)
	}
	id := p.JSON201.Id
	body.Description = nil
	u := must(c.UpdateProductWithResponse(ctx, id, body))
	if u.JSON200 == nil || u.JSON200.Description == nil || *u.JSON200.Description != description {
		t.Fatalf("legacy update: %s", u.Body)
	}
	read := must(c.GetProductWithResponse(ctx, id))
	if read.JSON200 == nil || read.JSON200.Description == nil || *read.JSON200.Description != description {
		t.Fatalf("read: %s", read.Body)
	}
	denied := must(financeClient(t, srv.URL, []string{"pm"}, "none").UpdateProductWithResponse(ctx, id, body))
	if denied.StatusCode() != 403 {
		t.Fatalf("unscoped PM: %s", denied.Body)
	}
}
