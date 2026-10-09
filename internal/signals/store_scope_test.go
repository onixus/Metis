package signals_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/signals"
)

func TestAD02_NFS01_StoreRejectsDirectUnauthorizedAccess(t *testing.T) {
	ctx := context.Background()
	store := signals.NewMemStore()
	own, foreign := kernel.NewID(), kernel.NewID()
	scoped := pmScope("owner", map[kernel.ID]authz.Access{own: authz.AccessPrivate})
	a := signals.Signal{ID: kernel.NewID(), ProductID: own, ExternalKey: "synthetic-own"}
	b := signals.Signal{ID: kernel.NewID(), ProductID: foreign, ExternalKey: "synthetic-foreign"}
	for _, row := range []signals.Signal{a, b} {
		if err := store.Save(ctx, cpoScope(), row); err != nil {
			t.Fatal(err)
		}
	}
	for name, sc := range map[string]authz.Scope{"zero": {}, "foreign": scoped} {
		t.Run(name, func(t *testing.T) {
			if err := store.Save(ctx, sc, b); !errors.Is(err, kernel.ErrForbidden) {
				t.Fatalf("save: %v", err)
			}
			if _, err := store.Get(ctx, sc, b.ID); !errors.Is(err, kernel.ErrForbidden) {
				t.Fatalf("get: %v", err)
			}
			if _, err := store.GetByExternalKey(ctx, sc, b.ExternalKey); !errors.Is(err, kernel.ErrForbidden) {
				t.Fatalf("external key: %v", err)
			}
			if _, err := store.List(ctx, sc, signals.Filter{ProductID: foreign}); !errors.Is(err, kernel.ErrForbidden) {
				t.Fatalf("list: %v", err)
			}
		})
	}
	hijack := b
	hijack.ProductID = own
	if err := store.Save(ctx, scoped, hijack); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("owner overwrite: %v", err)
	}
	got, err := store.Get(ctx, cpoScope(), b.ID)
	if err != nil || got.ProductID != foreign {
		t.Fatalf("unauthorized mutation: %+v %v", got, err)
	}
	rows, err := store.List(ctx, scoped, signals.Filter{})
	if err != nil || len(rows) != 1 || rows[0].ID != a.ID {
		t.Fatalf("filtered list: %+v %v", rows, err)
	}
	if _, err := store.List(ctx, authz.Scope{}, signals.Filter{}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("zero list: %v", err)
	}
	a.Text = "allowed update"
	if err := store.Save(ctx, scoped, a); err != nil {
		t.Fatal(err)
	}
}
