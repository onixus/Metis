package identityaccess_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

type fakeDir map[string]kernel.ID

func (d fakeDir) ProductIDByKey(_ context.Context, key string) (kernel.ID, error) {
	id, ok := d[key]
	if !ok {
		return kernel.NilID, kernel.ErrNotFound
	}
	return id, nil
}

type fakeGraph map[kernel.ID][]kernel.ID

func (g fakeGraph) LinkedProducts(_ context.Context, id kernel.ID) ([]kernel.ID, error) {
	return g[id], nil
}

func TestAD02_ResolverBuildsPMScopeWithStrategicNeighbors(t *testing.T) {
	edr, soar, vm := kernel.NewID(), kernel.NewID(), kernel.NewID()
	r := identityaccess.NewResolver(fakeDir{"edr": edr, "soar": soar, "vm": vm},
		fakeGraph{soar: {edr, vm}, edr: {soar}, vm: {soar}})
	s, err := r.ScopeFor(context.Background(), identityaccess.Claims{Subject: "pm-soar", Roles: []string{"pm"}, Products: []string{"soar"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Product(soar) != authz.AccessPrivate || s.Product(edr) != authz.AccessStrategic || s.Product(vm) != authz.AccessStrategic {
		t.Fatal("PM хаба: приватно свой, стратегически соседи")
	}
	if s.Audience() != authz.AudienceInternal {
		t.Fatal("PM — внутренняя аудитория")
	}
}

func TestAD02_UnknownRolesDenied(t *testing.T) {
	r := identityaccess.NewResolver(fakeDir{}, fakeGraph{})
	_, err := r.ScopeFor(context.Background(), identityaccess.Claims{Subject: "x", Roles: []string{"superuser"}})
	if !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("ожидался ErrForbidden, получено %v", err)
	}
}

func TestRM02_PresaleOnlyGetsSalesSafe(t *testing.T) {
	r := identityaccess.NewResolver(fakeDir{}, fakeGraph{})
	s, err := r.ScopeFor(context.Background(), identityaccess.Claims{Subject: "p", Roles: []string{"presale"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Audience() != authz.AudienceSalesSafe || s.Product(kernel.NewID()) != authz.AccessStrategic {
		t.Fatal("presale видит все продукты стратегически, аудитория sales-safe")
	}
}
