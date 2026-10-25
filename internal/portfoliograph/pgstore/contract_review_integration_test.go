//go:build integration

package pgstore_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
	pg "github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/portfoliograph/pgstore"
)

type contractReviewFixture struct {
	db                                          *pgdb.DB
	store                                       *pgstore.Store
	ctx                                         context.Context
	contract                                    pg.IntegrationContract
	provider, replacement, consumer, downstream pg.Feature
	products                                    [4]kernel.ID
}

func newContractReviewFixture(t *testing.T) *contractReviewFixture {
	t.Helper()
	db := openTestDB(t)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	f := &contractReviewFixture{db: db, store: pgstore.NewStore(db, kernel.FixedClock{T: now}), ctx: context.Background()}
	for i := range f.products {
		f.products[i] = kernel.NewID()
		p := pg.Product{ID: f.products[i], Key: []string{"review-provider", "review-consumer", "review-downstream", "review-foreign"}[i], Name: "Synthetic", Type: pg.ProductTypeOther, Lifecycle: pg.LifecycleActive, CreatedAt: now, UpdatedAt: now}
		if err := f.store.SaveProduct(f.ctx, repositoryScope(), p); err != nil {
			t.Fatal(err)
		}
	}
	feature := func(product kernel.ID, name string, date kernel.Date) pg.Feature {
		ft := pg.Feature{ID: kernel.NewID(), ProductID: product, Name: name, Status: pg.FeaturePlanned, PlannedDate: date, CreatedAt: now, UpdatedAt: now}
		if err := f.store.SaveFeature(f.ctx, repositoryScope(), ft); err != nil {
			t.Fatal(err)
		}
		return ft
	}
	f.provider = feature(f.products[0], "Provider", kernel.DateOf(2027, 2, 1))
	f.replacement = feature(f.products[0], "Replacement", kernel.DateOf(2027, 3, 1))
	f.consumer = feature(f.products[1], "Consumer", kernel.DateOf(2027, 1, 1))
	f.downstream = feature(f.products[2], "Downstream", kernel.DateOf(2027, 1, 15))
	f.contract = pg.IntegrationContract{ID: kernel.NewID(), Name: "Original", ProviderProductID: f.products[0], ConsumerProductID: f.products[1], ProviderFeatureIDs: []kernel.ID{f.provider.ID}, ConsumerFeatureIDs: []kernel.ID{f.consumer.ID}, Status: pg.ContractActive, Criticality: pg.CritBlocks, CreatedAt: now, UpdatedAt: now}
	manual := pg.Link{ID: kernel.NewID(), Type: pg.LinkIntegration, FromProductID: f.downstream.ProductID, ToProductID: f.consumer.ProductID, FromFeatureID: f.downstream.ID, ToFeatureID: f.consumer.ID, Criticality: pg.CritBlocks, CreatedAt: now}
	if err := f.store.SaveLink(f.ctx, repositoryScope(), manual); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *contractReviewFixture) links(provider kernel.ID) []pg.Link {
	return []pg.Link{
		{ID: kernel.NewID(), Type: pg.LinkIntegration, FromProductID: f.products[1], ToProductID: f.products[0], FromFeatureID: f.consumer.ID, ToFeatureID: provider, ContractID: f.contract.ID, Criticality: pg.CritBlocks, CreatedAt: f.contract.CreatedAt},
		{ID: kernel.NewID(), Type: pg.LinkIntegration, FromProductID: f.products[1], ToProductID: f.products[0], ContractID: f.contract.ID, Criticality: pg.CritBlocks, CreatedAt: f.contract.CreatedAt},
	}
}

func (f *contractReviewFixture) assertDates(t *testing.T, implied kernel.Date, source kernel.ID) {
	t.Helper()
	// Reload through a fresh service to check persisted projections.
	svc := pg.NewService(f.store, nil, kernel.SystemClock{})
	for _, id := range []kernel.ID{f.consumer.ID, f.downstream.ID} {
		ft, err := svc.Feature(f.ctx, repositoryScope(), id)
		if err != nil {
			t.Fatal(err)
		}
		wantSource := source
		if id == f.downstream.ID && !implied.IsZero() {
			wantSource = f.consumer.ID
		}
		if ft.Affected != !implied.IsZero() || ft.ImpliedDate != implied || ft.AffectedBy != wantSource {
			t.Fatalf("feature %s: affected=%v implied=%v source=%s; want implied=%v source=%s", id, ft.Affected, ft.ImpliedDate, ft.AffectedBy, implied, wantSource)
		}
	}
}

func TestPG04_PG08_ContractReplacementPersistsReboundAndClearedDates(t *testing.T) {
	f := newContractReviewFixture(t)
	if _, err := f.store.ReplaceContract(f.ctx, repositoryScope(), f.contract, f.links(f.provider.ID)); err != nil {
		t.Fatal(err)
	}
	f.assertDates(t, kernel.DateOf(2027, 2, 1), f.provider.ID)
	f.contract.ProviderFeatureIDs = []kernel.ID{f.replacement.ID}
	if _, err := f.store.ReplaceContract(f.ctx, repositoryScope(), f.contract, f.links(f.replacement.ID)); err != nil {
		t.Fatal(err)
	}
	f.assertDates(t, kernel.DateOf(2027, 3, 1), f.replacement.ID)
	f.contract.ProviderFeatureIDs = nil
	if _, err := f.store.ReplaceContract(f.ctx, repositoryScope(), f.contract, f.links(f.replacement.ID)[1:]); err != nil {
		t.Fatal(err)
	}
	f.assertDates(t, kernel.Date{}, kernel.NilID)
}

func TestPG04_PG08_ContractProjectionFailureRollsBackContractEdgesAndDates(t *testing.T) {
	f := newContractReviewFixture(t)
	if _, err := f.store.ReplaceContract(f.ctx, repositoryScope(), f.contract, f.links(f.provider.ID)); err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Load(f.ctx, repositoryScope())
	if err != nil {
		t.Fatal(err)
	}
	// Fail the final projection write, after contract and edge writes ran.
	if _, err := f.db.Pool().Exec(f.ctx, `CREATE FUNCTION portfoliograph.review_reject_projection() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic projection failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.db.Pool().Exec(context.Background(), `DROP TRIGGER IF EXISTS review_projection_failure ON portfoliograph.features; DROP FUNCTION IF EXISTS portfoliograph.review_reject_projection()`); err != nil {
			t.Errorf("cleanup projection trigger: %v", err)
		}
	})
	if _, err := f.db.Pool().Exec(f.ctx, `CREATE TRIGGER review_projection_failure BEFORE UPDATE ON portfoliograph.features FOR EACH ROW EXECUTE FUNCTION portfoliograph.review_reject_projection()`); err != nil {
		t.Fatal(err)
	}
	f.contract.Name = "Must roll back"
	f.contract.ProviderFeatureIDs = []kernel.ID{f.replacement.ID}
	if _, err := f.store.ReplaceContract(f.ctx, repositoryScope(), f.contract, f.links(f.replacement.ID)); err == nil || !strings.Contains(err.Error(), "synthetic projection failure") {
		t.Fatalf("expected injected projection failure, got %v", err)
	}
	after, err := f.store.Load(f.ctx, repositoryScope())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("projection failure partially persisted: before=%+v after=%+v", before, after)
	}
	f.assertDates(t, kernel.DateOf(2027, 2, 1), f.provider.ID)
}

func TestAD02_NFS01_PGContractAssociationsRejectInvalidOwnershipAndPreserveLegacy(t *testing.T) {
	f := newContractReviewFixture(t)
	if _, err := f.store.ReplaceContract(f.ctx, repositoryScope(), f.contract, f.links(f.provider.ID)); err != nil {
		t.Fatal(err)
	}
	foreign := pg.Feature{ID: kernel.NewID(), ProductID: f.products[3], Name: "Foreign projection sentinel", Status: pg.FeaturePlanned, PlannedDate: kernel.DateOf(2027, 1, 1), Affected: true, ImpliedDate: kernel.DateOf(2027, 5, 1), AffectedBy: f.downstream.ID, CreatedAt: f.contract.CreatedAt, UpdatedAt: f.contract.UpdatedAt}
	if err := f.store.SaveFeature(f.ctx, repositoryScope(), foreign); err != nil {
		t.Fatal(err)
	}
	valid := f.links(f.provider.ID)[1]
	for _, kind := range []string{"unrelated", "reversed", "nonintegration", "spoofed-feature-products"} {
		t.Run(kind, func(t *testing.T) {
			bad := valid
			bad.ID = kernel.NewID()
			switch kind {
			case "unrelated":
				bad.FromProductID = f.products[2]
				bad.ToProductID = f.products[3]
			case "reversed":
				bad.FromProductID, bad.ToProductID = bad.ToProductID, bad.FromProductID
			case "nonintegration":
				bad.Type = pg.LinkCommercial
			case "spoofed-feature-products":
				bad.FromFeatureID, bad.ToFeatureID = f.downstream.ID, foreign.ID
			}
			if err := f.store.SaveLink(f.ctx, repositoryScope(), bad); !errors.Is(err, kernel.ErrValidation) {
				t.Fatalf("invalid association: %v", err)
			}
		})
	}
	if err := f.store.SaveLink(f.ctx, repositoryScope(), valid); err != nil {
		t.Fatalf("matching association: %v", err)
	}
	manual := valid
	manual.ID = kernel.NewID()
	manual.ContractID = kernel.NilID
	manual.Type = pg.LinkCommercial
	if err := f.store.SaveLink(f.ctx, repositoryScope(), manual); err != nil {
		t.Fatalf("manual link: %v", err)
	}
	legacyID := kernel.NewID()
	if _, err := f.db.Pool().Exec(f.ctx, `INSERT INTO portfoliograph.links(id,type,from_product_id,to_product_id,criticality,contract_id,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, legacyID, string(pg.LinkIntegration), f.products[2], f.products[3], string(pg.CritBlocks), f.contract.ID, f.contract.CreatedAt); err != nil {
		t.Fatal(err)
	}
	spoofedID := kernel.NewID()
	if _, err := f.db.Pool().Exec(f.ctx, `INSERT INTO portfoliograph.links(id,type,from_product_id,to_product_id,from_feature_id,to_feature_id,criticality,contract_id,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, spoofedID, string(pg.LinkIntegration), f.products[1], f.products[0], f.downstream.ID, foreign.ID, string(pg.CritBlocks), f.contract.ID, f.contract.CreatedAt); err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Load(f.ctx, repositoryScope())
	if err != nil {
		t.Fatal(err)
	}
	protected := map[kernel.ID]pg.Feature{}
	for _, ft := range before.Features {
		if ft.ID == foreign.ID || ft.ID == f.downstream.ID {
			protected[ft.ID] = ft
		}
	}
	restricted := authz.New(authz.Params{Subject: "contract-pm", Roles: []authz.Role{authz.RolePM}, Products: map[kernel.ID]authz.Access{f.products[0]: authz.AccessPrivate, f.products[1]: authz.AccessStrategic}})
	f.contract.Name = "Restricted editor update"
	if _, err := f.store.ReplaceContract(f.ctx, restricted, f.contract, f.links(f.provider.ID)); err != nil {
		t.Fatalf("legacy association blocked authorized editor: %v", err)
	}
	snap, err := f.store.Load(f.ctx, repositoryScope())
	if err != nil {
		t.Fatal(err)
	}
	foundLegacy, foundManual, foundSpoofed := false, false, false
	for _, l := range snap.Links {
		if l.ID == legacyID {
			foundLegacy = true
		}
		if l.ID == spoofedID {
			foundSpoofed = true
		}
		if l.ID == manual.ID {
			foundManual = true
		}
	}
	if !foundLegacy || !foundManual || !foundSpoofed {
		t.Fatalf("unowned links removed: legacy=%v manual=%v spoofed=%v", foundLegacy, foundManual, foundSpoofed)
	}
	for _, ft := range snap.Features {
		if want, ok := protected[ft.ID]; ok && !reflect.DeepEqual(ft, want) {
			t.Fatalf("foreign projection changed: got=%+v want=%+v", ft, want)
		}
	}
	svc := pg.NewService(f.store, nil, kernel.SystemClock{})
	if err := svc.Load(f.ctx); err != nil {
		t.Fatal(err)
	}
	cached, err := svc.Links(f.ctx, repositoryScope())
	if err != nil {
		t.Fatal(err)
	}
	cachedLegacy, cachedSpoofed := false, false
	for _, l := range cached {
		if l.ID == legacyID {
			cachedLegacy = true
		}
		if l.ID == spoofedID {
			cachedSpoofed = true
		}
	}
	if !cachedLegacy || !cachedSpoofed {
		t.Fatalf("service reload lost legacy links: foreign=%v spoofed=%v", cachedLegacy, cachedSpoofed)
	}

	if len(snap.Contracts) != 1 || snap.Contracts[0].Name != f.contract.Name {
		t.Fatal("authorized metadata update not persisted")
	}
}
