package architecture_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/onixus/metis/internal/commitments"
	"github.com/onixus/metis/internal/compliance"
	"github.com/onixus/metis/internal/decisions"
	"github.com/onixus/metis/internal/delivery"
	"github.com/onixus/metis/internal/discovery"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/prioritization"
	"github.com/onixus/metis/internal/roadmap"
	"github.com/onixus/metis/internal/signals"
)

func TestAD02_NFS01_DirectStoresRejectForeignProductAndOwnerTakeover(t *testing.T) {
	testStoreOwnership(t, map[string]any{
		"signals": signals.NewMemStore(), "roadmap": roadmap.NewMemStore(),
		"discovery": discovery.NewMemStore(), "decisions": decisions.NewMemStore(),
		"commitments": commitments.NewMemStore(), "delivery": delivery.NewMemStore(),
		"compliance": compliance.NewMemStore(), "prioritization": prioritization.NewMemStore(),
	})
}

func testStoreOwnership(t *testing.T, stores map[string]any) {
	t.Helper()
	ctx := context.Background()
	own, foreign, id, model := kernel.NewID(), kernel.NewID(), kernel.NewID(), kernel.NewID()
	privileged := authz.New(authz.Params{Subject: "fixture", Roles: []authz.Role{authz.RoleAdmin, authz.RoleService}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
	if err := stores["prioritization"].(prioritization.Store).SaveModel(ctx, privileged, prioritization.ScoringModel{ID: model}); err != nil {
		t.Fatal(err)
	}
	// Even privileged roles must not turn a product-limited scope into global access.
	limited := authz.New(authz.Params{Subject: "limited", Roles: []authz.Role{authz.RoleAdmin, authz.RoleService}, Products: map[kernel.ID]authz.Access{own: authz.AccessPrivate}, Audience: authz.AudienceInternal})
	cases := []struct {
		store      any
		save, read string
		record     any
		keys       []any
	}{
		{stores["signals"], "Save", "Get", signals.Signal{ID: id, ProductID: foreign}, []any{id}},
		{stores["roadmap"], "SaveItem", "Item", roadmap.RoadmapItem{ID: id, ProductID: foreign}, []any{id}},
		{stores["roadmap"], "SaveRelease", "Release", roadmap.Release{ID: id, ProductID: foreign}, []any{id}},
		{stores["discovery"], "SaveHypothesis", "Hypothesis", discovery.Hypothesis{ID: id, ProductID: foreign}, []any{id}},
		{stores["discovery"], "SaveInterview", "Interview", discovery.Interview{ID: id, ProductID: foreign}, []any{id}},
		{stores["discovery"], "SaveInsight", "Insight", discovery.Insight{ID: id, ProductID: foreign}, []any{id}},
		{stores["discovery"], "SaveEvidence", "Evidence", discovery.Evidence{ID: id, ProductID: foreign}, []any{id}},
		{stores["decisions"], "Save", "Get", decisions.DecisionRecord{ID: id, ProductID: foreign}, []any{id}},
		{stores["commitments"], "Save", "Get", commitments.Commitment{ID: id, ProductID: foreign}, []any{id}},
		{stores["delivery"], "SaveMapping", "MappingByFeature", delivery.Mapping{FeatureID: id, ProductID: foreign}, []any{id}},
		{stores["delivery"], "SaveEpic", "EpicByFeature", delivery.EpicProjection{FeatureID: id, ProductID: foreign}, []any{id}},
		{stores["compliance"], "SaveTrack", "Track", compliance.Track{ID: id, ProductID: foreign}, []any{id}},
		{stores["compliance"], "SaveBaseline", "Baseline", compliance.CertifiedBaseline{ID: id, ProductID: foreign}, []any{id}},
		{stores["prioritization"], "SaveModel", "Model", prioritization.ScoringModel{ID: id, ProductID: foreign}, []any{id}},
		{stores["prioritization"], "SaveInputs", "Inputs", prioritization.FeatureScoreInput{ModelID: model, FeatureID: id, ProductID: foreign}, []any{model, id}},
		{stores["prioritization"], "SaveFlags", "Flags", prioritization.FeatureFlags{FeatureID: id, ProductID: foreign}, []any{id}},
	}
	for _, tc := range cases {
		t.Run(reflect.TypeOf(tc.record).String(), func(t *testing.T) {
			v := reflect.ValueOf(tc.store)
			save := func(sc authz.Scope, record any) error {
				result := v.MethodByName(tc.save).Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(sc), reflect.ValueOf(record)})
				if result[0].IsNil() {
					return nil
				}
				return result[0].Interface().(error)
			}
			read := func(sc authz.Scope) (reflect.Value, error) {
				args := []reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(sc)}
				for _, key := range tc.keys {
					args = append(args, reflect.ValueOf(key))
				}
				out := v.MethodByName(tc.read).Call(args)
				if out[1].IsNil() {
					return out[0], nil
				}
				return out[0], out[1].Interface().(error)
			}
			if err := save(privileged, tc.record); err != nil {
				t.Fatal(err)
			}
			if _, err := read(limited); !errors.Is(err, kernel.ErrForbidden) {
				t.Fatalf("foreign read: %v", err)
			}
			if err := save(limited, tc.record); !errors.Is(err, kernel.ErrForbidden) {
				t.Fatalf("foreign write: %v", err)
			}
			stolen := reflect.New(reflect.TypeOf(tc.record)).Elem()
			stolen.Set(reflect.ValueOf(tc.record))
			stolen.FieldByName("ProductID").Set(reflect.ValueOf(own))
			if err := save(limited, stolen.Interface()); !errors.Is(err, kernel.ErrForbidden) {
				t.Fatalf("owner takeover: %v", err)
			}
			got, err := read(privileged)
			if err != nil || got.FieldByName("ProductID").Interface() != foreign {
				t.Fatalf("stored owner changed: %v", err)
			}
		})
	}
}

func TestAD02_NFS01_ContractRequiresBothVisibleSides(t *testing.T) {
	ctx := context.Background()
	store := portfoliograph.NewMemStore()
	a, b := kernel.NewID(), kernel.NewID()
	pm := func(other authz.Access) authz.Scope {
		return authz.New(authz.Params{Subject: "pm", Roles: []authz.Role{authz.RolePM}, Products: map[kernel.ID]authz.Access{a: authz.AccessPrivate, b: other}})
	}
	c := portfoliograph.IntegrationContract{ID: kernel.NewID(), ProviderProductID: a, ConsumerProductID: b}
	if err := store.SaveContract(ctx, pm(authz.AccessNone), c); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("hidden endpoint: %v", err)
	}
	if err := store.SaveContract(ctx, pm(authz.AccessStrategic), c); err != nil {
		t.Fatal(err)
	}
	c.ConsumerProductID = a
	if err := store.SaveContract(ctx, pm(authz.AccessStrategic), c); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("endpoint takeover: %v", err)
	}
}
