package portfoliograph

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

func TestAD02_NFS01_ContractBindingRejectsNewForeignAndPreservesLegacy(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	a, b, c, d := kernel.NewID(), kernel.NewID(), kernel.NewID(), kernel.NewID()
	for i, id := range []kernel.ID{a, b, c, d} {
		store.snap.Products = append(store.snap.Products, Product{ID: id, Key: []string{"a", "b", "c", "d"}[i], Name: "Synthetic", Type: ProductTypeOther, Lifecycle: LifecycleActive})
	}
	contract := IntegrationContract{ID: kernel.NewID(), Name: "AB", ProviderProductID: a, ConsumerProductID: b, Criticality: CritBlocks, Status: ContractDraft}
	owned := Link{ID: kernel.NewID(), Type: LinkIntegration, FromProductID: b, ToProductID: a, ContractID: contract.ID, Criticality: CritBlocks}
	legacy := Link{ID: kernel.NewID(), Type: LinkIntegration, FromProductID: c, ToProductID: d, ContractID: contract.ID, Criticality: CritBlocks}
	store.snap.Contracts = []IntegrationContract{contract}
	store.snap.Links = []Link{owned, legacy}
	all := authz.New(authz.Params{Subject: "test-admin", Roles: []authz.Role{authz.RoleAdmin}, AllProducts: authz.AccessPrivate})
	pm := authz.New(authz.Params{Subject: "pm-ab", Roles: []authz.Role{authz.RolePM}, Products: map[kernel.ID]authz.Access{a: authz.AccessPrivate, b: authz.AccessStrategic}})
	svc := NewService(store, nil, kernel.SystemClock{})
	for _, bad := range []Link{legacy, {Type: LinkIntegration, FromProductID: a, ToProductID: b, ContractID: contract.ID, Criticality: CritBlocks}, {Type: LinkCommercial, FromProductID: b, ToProductID: a, ContractID: contract.ID, Criticality: CritBlocks}} {
		bad.ID = kernel.NewID()
		if err := store.SaveLink(ctx, all, bad); err == nil {
			t.Fatal("raw store accepted invalid contract binding")
		}
		if _, err := svc.CreateLink(ctx, all, LinkInput{Type: bad.Type, FromProductID: bad.FromProductID, ToProductID: bad.ToProductID, ContractID: bad.ContractID, Criticality: bad.Criticality}); err == nil {
			t.Fatal("service accepted invalid contract binding")
		}
	}
	if err := store.SaveLink(ctx, authz.Scope{}, owned); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("zero scope=%v", err)
	}
	if err := store.SaveLink(ctx, pm, owned); err != nil {
		t.Fatal(err)
	}
	manual := legacy
	manual.ID = kernel.NewID()
	manual.ContractID = kernel.NilID
	if err := store.SaveLink(ctx, all, manual); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveContract(ctx, pm, contract.ID, ContractInput{Name: "Updated", ProviderProductID: a, ConsumerProductID: b, Criticality: CritAccelerates}); err != nil {
		t.Fatal(err)
	}
	for _, reload := range []bool{false, true} {
		if reload {
			if err := svc.Load(ctx); err != nil {
				t.Fatal(err)
			}
		}
		links, err := svc.Links(ctx, all)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, link := range links {
			if link.ID == legacy.ID {
				found = true
				if !reflect.DeepEqual(link, legacy) {
					t.Fatalf("legacy changed=%+v", link)
				}
			}
		}
		if !found {
			t.Fatal("scoped AB editor deleted legacy CD link")
		}
	}
	snapshot, err := store.Load(ctx, all)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, link := range snapshot.Links {
		if link.ID == legacy.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("durable legacy link deleted")
	}
}

func TestPG05_NFS01_CandidatePreservesLegacyForeignDependency(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	ids := []kernel.ID{kernel.NewID(), kernel.NewID(), kernel.NewID(), kernel.NewID()}
	fs := []kernel.ID{kernel.NewID(), kernel.NewID(), kernel.NewID(), kernel.NewID()}
	for i, id := range ids {
		store.snap.Products = append(store.snap.Products, Product{ID: id})
		store.snap.Features = append(store.snap.Features, Feature{ID: fs[i], ProductID: id, Status: FeaturePlanned})
	}
	c := IntegrationContract{ID: kernel.NewID(), Name: "AB", ProviderProductID: ids[0], ConsumerProductID: ids[1], Criticality: CritBlocks}
	store.snap.Contracts = []IntegrationContract{c}
	for _, pair := range [][2]int{{0, 2}, {2, 3}, {3, 1}} {
		link := Link{ID: kernel.NewID(), Type: LinkIntegration, FromProductID: ids[pair[0]], ToProductID: ids[pair[1]], FromFeatureID: fs[pair[0]], ToFeatureID: fs[pair[1]], Criticality: CritBlocks}
		if pair[0] == 2 {
			link.ContractID = c.ID
		}
		store.snap.Links = append(store.snap.Links, link)
	}
	all := authz.New(authz.Params{Subject: "admin", Roles: []authz.Role{authz.RoleAdmin}, AllProducts: authz.AccessPrivate})
	svc := NewService(store, nil, kernel.SystemClock{})
	_, err := svc.SaveContract(ctx, all, c.ID, ContractInput{Name: "AB", ProviderProductID: ids[0], ConsumerProductID: ids[1], ProviderFeatureIDs: []kernel.ID{fs[0]}, ConsumerFeatureIDs: []kernel.ID{fs[1]}, Criticality: CritBlocks})
	var cycle *CycleError
	if !errors.As(err, &cycle) {
		t.Fatalf("legacy dependency lost during cycle validation: %v", err)
	}
}

func TestAD02_NFS01_ActualFeatureOwnersProtectLegacyProjections(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	a, b, c, d := kernel.NewID(), kernel.NewID(), kernel.NewID(), kernel.NewID()
	for _, id := range []kernel.ID{a, b, c, d} {
		store.snap.Products = append(store.snap.Products, Product{ID: id})
	}
	cf, df := Feature{ID: kernel.NewID(), ProductID: c, PlannedDate: kernel.DateOf(2027, 1, 1), Affected: true, ImpliedDate: kernel.DateOf(2027, 5, 1)}, Feature{ID: kernel.NewID(), ProductID: d, PlannedDate: kernel.DateOf(2027, 1, 1)}
	store.snap.Features = []Feature{cf, df}
	contract := IntegrationContract{ID: kernel.NewID(), ProviderProductID: a, ConsumerProductID: b, Criticality: CritBlocks}
	store.snap.Contracts = []IntegrationContract{contract}
	spoofed := Link{ID: kernel.NewID(), Type: LinkIntegration, FromProductID: b, ToProductID: a, FromFeatureID: cf.ID, ToFeatureID: df.ID, ContractID: contract.ID, Criticality: CritBlocks}
	pm := authz.New(authz.Params{Subject: "pm-ab", Roles: []authz.Role{authz.RolePM}, Products: map[kernel.ID]authz.Access{a: authz.AccessPrivate, b: authz.AccessStrategic}})
	if err := store.SaveLink(ctx, pm, spoofed); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("spoofed actual owners accepted: %v", err)
	}
	// A legacy representation is fixture state, never accepted through the repaired writer.
	store.snap.Links = append(store.snap.Links, spoofed)
	svc := NewService(store, nil, kernel.SystemClock{})
	if _, err := svc.SaveContract(ctx, pm, contract.ID, ContractInput{Name: "AB update", ProviderProductID: a, ConsumerProductID: b, Criticality: CritBlocks}); err != nil {
		t.Fatal(err)
	}
	all := authz.New(authz.Params{Subject: "admin", Roles: []authz.Role{authz.RoleAdmin}, AllProducts: authz.AccessPrivate})
	for _, reload := range []bool{false, true} {
		if reload {
			if err := svc.Load(ctx); err != nil {
				t.Fatal(err)
			}
		}
		links, err := svc.Links(ctx, all)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, l := range links {
			if l.ID == spoofed.ID {
				found = true
				if !reflect.DeepEqual(l, spoofed) {
					t.Fatal("legacy spoofed edge changed")
				}
			}
		}
		if !found {
			t.Fatal("AB editor removed foreign feature edge")
		}
		got, err := svc.Feature(ctx, all, cf.ID)
		if err != nil || !reflect.DeepEqual(got, cf) {
			t.Fatalf("AB editor altered foreign projection: %+v %v", got, err)
		}
	}
}
