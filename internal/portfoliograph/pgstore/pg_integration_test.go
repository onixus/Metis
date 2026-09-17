//go:build integration

package pgstore_test

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/migrate"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/portfoliograph/pgstore"
)

func openTestDB(t *testing.T) *pgdb.DB {
	t.Helper()
	url := os.Getenv("METIS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("METIS_TEST_DATABASE_URL не задан: интеграционный тест пропущен (docs/questions.md №05)")
	}
	ctx := context.Background()
	db, err := pgdb.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrate.Up(ctx, db.Pool(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `TRUNCATE portfoliograph.feature_values, portfoliograph.links, portfoliograph.contracts,
		portfoliograph.requirements, portfoliograph.features, portfoliograph.capabilities, portfoliograph.products, portfoliograph.settings`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPG_PGStoreRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 17, 10, 30, 0, 123456000, time.UTC)
	store := pgstore.NewStore(db, kernel.FixedClock{T: now})

	edr := portfoliograph.Product{ID: kernel.NewID(), Key: "edr", Name: "EDR", Type: portfoliograph.ProductTypeSecurity, Owner: "pm-edr", Lifecycle: portfoliograph.LifecycleActive, SSDLCCertified: true, CreatedAt: now, UpdatedAt: now}
	soar := portfoliograph.Product{ID: kernel.NewID(), Key: "soar", Name: "SOAR", Type: portfoliograph.ProductTypeSecurity, Lifecycle: portfoliograph.LifecycleActive, HubManual: true, CreatedAt: now, UpdatedAt: now}
	capEDR := portfoliograph.Capability{ID: kernel.NewID(), ProductID: edr.ID, Name: "Response"}
	fEDR := portfoliograph.Feature{ID: kernel.NewID(), ProductID: edr.ID, CapabilityID: capEDR.ID, Name: "Response API v2", Status: portfoliograph.FeaturePlanned,
		OwnValue: kernel.RUB(1_200_000_000), PlannedDate: kernel.DateOf(2026, 12, 1), ExternalKey: "EDR-1", CreatedAt: now, UpdatedAt: now}
	fSOAR := portfoliograph.Feature{ID: kernel.NewID(), ProductID: soar.ID, Name: "Коннектор EDR v2", Status: portfoliograph.FeatureInProgress,
		Affected: true, AffectedBy: fEDR.ID, ImpliedDate: kernel.DateOf(2026, 12, 22), CreatedAt: now, UpdatedAt: now}
	req := portfoliograph.Requirement{ID: kernel.NewID(), ProductID: edr.ID, FeatureID: fEDR.ID, Text: "Ответ ≤ 200 мс"}
	contract := portfoliograph.IntegrationContract{ID: kernel.NewID(), Name: "EDR ↔ SOAR", ProviderProductID: edr.ID, ConsumerProductID: soar.ID,
		ProviderFeatureIDs: []kernel.ID{fEDR.ID}, ConsumerFeatureIDs: []kernel.ID{fSOAR.ID}, InterfaceVersion: "2.0", Owner: "pm-soar",
		Status: portfoliograph.ContractActive, Criticality: portfoliograph.CritBlocks,
		Compatibility: []portfoliograph.VersionPair{{ProviderVersion: "2.0", ConsumerVersion: "1.4", Compatible: true}},
		SignalValue:   kernel.RUB(1_200_000_000), CreatedAt: now, UpdatedAt: now}
	link := portfoliograph.Link{ID: kernel.NewID(), Type: portfoliograph.LinkIntegration, FromProductID: soar.ID, ToProductID: edr.ID,
		FromFeatureID: fSOAR.ID, ToFeatureID: fEDR.ID, Criticality: portfoliograph.CritBlocks, ContractID: contract.ID, CreatedAt: now}
	productLink := portfoliograph.Link{ID: kernel.NewID(), Type: portfoliograph.LinkCommercial, FromProductID: soar.ID, ToProductID: edr.ID, Criticality: portfoliograph.CritDesirable, CreatedAt: now}
	settings := portfoliograph.Settings{Coefficients: map[portfoliograph.Criticality]decimal.Decimal{
		portfoliograph.CritBlocks: decimal.RequireFromString("1"), portfoliograph.CritAccelerates: decimal.RequireFromString("0.55"), portfoliograph.CritDesirable: decimal.RequireFromString("0.2"),
	}}

	err := db.Transact(ctx, func(ctx context.Context) error {
		for _, f := range []func() error{
			func() error { return store.SaveProduct(ctx, edr) },
			func() error { return store.SaveProduct(ctx, soar) },
			func() error { return store.SaveCapability(ctx, capEDR) },
			func() error { return store.SaveFeature(ctx, fEDR) },
			func() error { return store.SaveFeature(ctx, fSOAR) },
			func() error { return store.SaveRequirement(ctx, req) },
			func() error { return store.SaveContract(ctx, contract) },
			func() error { return store.SaveLink(ctx, link) },
			func() error { return store.SaveLink(ctx, productLink) },
			func() error { return store.SaveSettings(ctx, settings) },
		} {
			if err := f(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Повторное сохранение — upsert.
	edr.Name = "EDR v2"
	if err := store.SaveProduct(ctx, edr); err != nil {
		t.Fatal(err)
	}

	snap, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Products) != 2 || len(snap.Capabilities) != 1 || len(snap.Features) != 2 || len(snap.Requirements) != 1 || len(snap.Links) != 2 || len(snap.Contracts) != 1 {
		t.Fatalf("размеры снимка: %d %d %d %d %d %d", len(snap.Products), len(snap.Capabilities), len(snap.Features), len(snap.Requirements), len(snap.Links), len(snap.Contracts))
	}
	find := func(want any) any {
		switch w := want.(type) {
		case portfoliograph.Product:
			for _, p := range snap.Products {
				if p.ID == w.ID {
					return p
				}
			}
		case portfoliograph.Feature:
			for _, f := range snap.Features {
				if f.ID == w.ID {
					return f
				}
			}
		case portfoliograph.Link:
			for _, l := range snap.Links {
				if l.ID == w.ID {
					return l
				}
			}
		}
		return nil
	}
	for _, want := range []any{edr, soar, fEDR, fSOAR, link, productLink} {
		if got := find(want); !reflect.DeepEqual(got, want) {
			t.Errorf("не совпадает:\n got %+v\nwant %+v", got, want)
		}
	}
	if !reflect.DeepEqual(snap.Capabilities[0], capEDR) || !reflect.DeepEqual(snap.Requirements[0], req) || !reflect.DeepEqual(snap.Contracts[0], contract) {
		t.Errorf("capability/requirement/contract не совпадают: %+v %+v %+v", snap.Capabilities[0], snap.Requirements[0], snap.Contracts[0])
	}
	for c, want := range settings.Coefficients {
		if !snap.Settings.Coef(c).Equal(want) {
			t.Errorf("коэффициент %s: %s != %s", c, snap.Settings.Coef(c), want)
		}
	}

	values := []portfoliograph.FeatureValue{{FeatureID: fEDR.ID, ProductID: edr.ID, OwnValue: kernel.RUB(1), DerivedValue: kernel.RUB(2), TotalValue: kernel.RUB(3), ComputedAt: now}}
	if err := store.SaveRollup(ctx, values); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRollup(ctx, values); err != nil {
		t.Fatal(err)
	}
	got, err := store.Rollup(ctx)
	if err != nil || !reflect.DeepEqual(got, values) {
		t.Fatalf("rollup: %+v err=%v", got, err)
	}

	if err := store.DeleteLink(ctx, productLink.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteLink(ctx, productLink.ID); !kernel.IsNotFound(err) {
		t.Fatalf("повторный DeleteLink: %v", err)
	}

	// Пустая БД настроек — значения по умолчанию.
	if _, err := db.Pool().Exec(ctx, "TRUNCATE portfoliograph.settings"); err != nil {
		t.Fatal(err)
	}
	snap, err = store.Load(ctx)
	if err != nil || !snap.Settings.Coef(portfoliograph.CritAccelerates).Equal(decimal.RequireFromString("0.5")) {
		t.Fatalf("настройки по умолчанию: %+v err=%v", snap.Settings, err)
	}
}

func TestPG01_PGStoreDeleteProductCascades(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 17, 10, 30, 0, 0, time.UTC)
	store := pgstore.NewStore(db, kernel.FixedClock{T: now})
	vm := portfoliograph.Product{ID: kernel.NewID(), Key: "vm", Name: "VM", Type: portfoliograph.ProductTypeSecurity, Lifecycle: portfoliograph.LifecycleActive, CreatedAt: now, UpdatedAt: now}
	edr := portfoliograph.Product{ID: kernel.NewID(), Key: "edr", Name: "EDR", Type: portfoliograph.ProductTypeSecurity, Lifecycle: portfoliograph.LifecycleActive, CreatedAt: now, UpdatedAt: now}
	capVM := portfoliograph.Capability{ID: kernel.NewID(), ProductID: vm.ID, Name: "Сканирование"}
	fVM := portfoliograph.Feature{ID: kernel.NewID(), ProductID: vm.ID, CapabilityID: capVM.ID, Name: "Экспорт", Status: portfoliograph.FeaturePlanned, CreatedAt: now, UpdatedAt: now}
	fEDR := portfoliograph.Feature{ID: kernel.NewID(), ProductID: edr.ID, Name: "API", Status: portfoliograph.FeaturePlanned, CreatedAt: now, UpdatedAt: now}
	req := portfoliograph.Requirement{ID: kernel.NewID(), ProductID: vm.ID, FeatureID: fVM.ID, Text: "CSV"}
	link := portfoliograph.Link{ID: kernel.NewID(), Type: portfoliograph.LinkIntegration, FromProductID: vm.ID, ToProductID: edr.ID, FromFeatureID: fVM.ID, ToFeatureID: fEDR.ID, Criticality: portfoliograph.CritBlocks, CreatedAt: now}
	for _, f := range []func() error{
		func() error { return store.SaveProduct(ctx, vm) }, func() error { return store.SaveProduct(ctx, edr) },
		func() error { return store.SaveCapability(ctx, capVM) }, func() error { return store.SaveFeature(ctx, fVM) },
		func() error { return store.SaveFeature(ctx, fEDR) }, func() error { return store.SaveRequirement(ctx, req) },
		func() error { return store.SaveLink(ctx, link) },
		func() error {
			return store.SaveRollup(ctx, []portfoliograph.FeatureValue{{FeatureID: fVM.ID, ProductID: vm.ID, ComputedAt: now}, {FeatureID: fEDR.ID, ProductID: edr.ID, ComputedAt: now}})
		},
	} {
		if err := f(); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeleteProduct(ctx, vm.ID); err != nil {
		t.Fatal(err)
	}
	snap, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Products) != 1 || snap.Products[0].ID != edr.ID || len(snap.Features) != 1 || len(snap.Links) != 0 || len(snap.Capabilities) != 0 || len(snap.Requirements) != 0 {
		t.Fatalf("каскад не сработал: %+v", snap)
	}
	if err := store.DeleteProduct(ctx, vm.ID); err == nil {
		t.Fatal("повторное удаление должно давать ErrNotFound")
	}
}
