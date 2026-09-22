package portfoliograph_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	pg "github.com/onixus/metis/internal/portfoliograph"
)

func TestPG01_DescriptionPreservedClearedAndBounded(t *testing.T) {
	f := newFixture(t)
	description := strings.Repeat("я", 12000)
	in := pg.ProductInput{Key: "described", Name: "Synthetic", Type: pg.ProductTypeSecurity, Description: &description}
	p, err := f.svc.CreateProduct(f.ctx, f.cpo, in)
	if err != nil || p.Description != description {
		t.Fatalf("create: %v", err)
	}
	in.Description = nil
	updated, err := f.svc.UpdateProduct(f.ctx, f.cpo, p.ID, in)
	if err != nil || updated.Description != description {
		t.Fatalf("legacy update erased description: %v", err)
	}
	if _, err := f.svc.UpdateProduct(f.ctx, authz.Scope{}, p.ID, in); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("zero scope: %v", err)
	}
	tooLong := description + "я"
	in.Description = &tooLong
	if _, err := f.svc.UpdateProduct(f.ctx, f.cpo, p.ID, in); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("length: %v", err)
	}
	empty := ""
	in.Description = &empty
	updated, err = f.svc.UpdateProduct(f.ctx, f.cpo, p.ID, in)
	if err != nil || updated.Description != "" {
		t.Fatalf("clear: %v", err)
	}
}
