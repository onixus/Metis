package architecture_test

import (
	"context"
	"testing"

	"github.com/onixus/metis/internal/decisions"
	"github.com/onixus/metis/internal/discovery"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/prioritization"
)

func TestNFS01_ReadOnlyScopeCannotMutateStoredCollections(t *testing.T) {
	ctx := context.Background()
	product, id := kernel.NewID(), kernel.NewID()
	admin := authz.New(authz.Params{Subject: "admin", Roles: []authz.Role{authz.RoleAdmin}, AllProducts: authz.AccessPrivate})
	reader := authz.New(authz.Params{Subject: "reader", Products: map[kernel.ID]authz.Access{product: authz.AccessPrivate}})
	t.Run("scoring inputs", func(t *testing.T) {
		store := prioritization.NewMemStore()
		original := prioritization.ScoringModel{ID: id, ProductID: product, Inputs: []string{"safe"}}
		if err := store.SaveModel(ctx, admin, original); err != nil {
			t.Fatal(err)
		}
		original.Inputs[0] = "input mutation"
		got, err := store.Model(ctx, reader, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Inputs[0] != "safe" {
			t.Fatal("Save retained caller slice")
		}
		got.Inputs[0] = "read mutation"
		again, err := store.Model(ctx, admin, id)
		if err != nil || again.Inputs[0] != "safe" {
			t.Fatal("reader changed stored model", err)
		}
	})
	t.Run("decision snapshot", func(t *testing.T) {
		store := decisions.NewMemStore()
		original := decisions.DecisionRecord{ID: id, ProductID: product, Snapshot: map[string]any{"nested": map[string]any{"amount": int64(123)}}}
		if err := store.Save(ctx, admin, original); err != nil {
			t.Fatal(err)
		}
		original.Snapshot["nested"].(map[string]any)["amount"] = int64(999)
		got, err := store.Get(ctx, reader, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Snapshot["nested"].(map[string]any)["amount"] != int64(123) {
			t.Fatal("Save retained nested map or changed numeric type")
		}
		got.Snapshot["nested"].(map[string]any)["amount"] = int64(0)
		again, err := store.Get(ctx, admin, id)
		if err != nil || again.Snapshot["nested"].(map[string]any)["amount"] != int64(123) {
			t.Fatal("reader changed stored snapshot", err)
		}
	})
	t.Run("discovery custom fields", func(t *testing.T) {
		store := discovery.NewMemStore()
		h := discovery.Hypothesis{ID: id, ProductID: product, CustomFields: map[string]any{"x": []string{"safe"}}}
		if err := store.SaveHypothesis(ctx, admin, h); err != nil {
			t.Fatal(err)
		}
		h.CustomFields["x"].([]string)[0] = "input mutation"
		got, err := store.Hypothesis(ctx, reader, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.CustomFields["x"].([]string)[0] != "safe" {
			t.Fatal("Save retained custom fields")
		}
		got.CustomFields["x"].([]string)[0] = "read mutation"
		again, err := store.Hypothesis(ctx, admin, id)
		if err != nil || again.CustomFields["x"].([]string)[0] != "safe" {
			t.Fatal("reader changed custom fields", err)
		}
	})
}
