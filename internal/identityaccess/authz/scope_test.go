package authz_test

import (
	"errors"
	"testing"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

var (
	edr = kernel.NewID()
	vm  = kernel.NewID()
)

func TestNFS01_ZeroScopeDeniesEverything(t *testing.T) {
	var s authz.Scope
	for _, a := range []authz.Action{authz.ActionReadStrategic, authz.ActionReadPrivate, authz.ActionWriteGraph,
		authz.ActionWriteSignals, authz.ActionAdminSettings, authz.ActionReadAudit} {
		if s.Allows(a, edr) {
			t.Errorf("нулевой Scope разрешил %s", a)
		}
	}
	if s.Product(edr) != authz.AccessNone || s.Finance() != authz.FinanceNone || s.Audience() != authz.AudienceSalesSafe {
		t.Fatal("нулевой Scope должен давать минимальные уровни")
	}
}

func TestAD02_PMSeesOnlyOwnProductPrivately(t *testing.T) {
	s := authz.New(authz.Params{Subject: "pm-vm", Roles: []authz.Role{authz.RolePM},
		Products: map[kernel.ID]authz.Access{vm: authz.AccessPrivate}, Audience: authz.AudienceInternal})
	if !s.Allows(authz.ActionReadPrivate, vm) {
		t.Fatal("PM должен видеть свой приватный контур")
	}
	if s.Allows(authz.ActionReadPrivate, edr) || s.Allows(authz.ActionReadStrategic, edr) {
		t.Fatal("PM VM не должен видеть EDR")
	}
	if err := s.Require(authz.ActionWriteGraph, edr); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("ожидался ErrForbidden, получено %v", err)
	}
}

func TestPG10_HubOwnerSeesStrategicSliceOnly(t *testing.T) {
	s := authz.New(authz.Params{Subject: "pm-soar", Roles: []authz.Role{authz.RolePM},
		Products: map[kernel.ID]authz.Access{edr: authz.AccessStrategic}, Audience: authz.AudienceInternal})
	if !s.Allows(authz.ActionReadStrategic, edr) {
		t.Fatal("владелец хаба видит стратегический срез")
	}
	if s.Allows(authz.ActionReadPrivate, edr) {
		t.Fatal("владелец хаба не видит сырые сигналы EDR")
	}
	if s.Allows(authz.ActionWriteGraph, edr) {
		t.Fatal("владелец хаба не пишет в чужой продукт")
	}
}

func TestRM02_PresaleIsSalesSafeAudience(t *testing.T) {
	s := authz.New(authz.Params{Subject: "presale", Roles: []authz.Role{authz.RolePresale},
		AllProducts: authz.AccessStrategic, Audience: authz.AudienceSalesSafe})
	if s.Audience() != authz.AudienceSalesSafe {
		t.Fatal("presale — только sales-safe")
	}
	if s.Allows(authz.ActionReadPrivate, edr) || s.Allows(authz.ActionWriteRoadmap, edr) {
		t.Fatal("presale только читает")
	}
}

func TestAD02_AdminOnlyForSettingsAndAccess(t *testing.T) {
	cpo := authz.New(authz.Params{Subject: "cpo", Roles: []authz.Role{authz.RoleCPO}, AllProducts: authz.AccessPrivate})
	if cpo.Allows(authz.ActionManageAccess, kernel.NilID) || cpo.Allows(authz.ActionManageConnects, kernel.NilID) {
		t.Fatal("CPO не управляет правами и коннекторами")
	}
	adm := authz.New(authz.Params{Subject: "adm", Roles: []authz.Role{authz.RoleAdmin}})
	if !adm.Allows(authz.ActionManageAccess, kernel.NilID) || !adm.Allows(authz.ActionReadAudit, kernel.NilID) {
		t.Fatal("admin управляет правами и читает аудит")
	}
}

func TestNFS02_FinanceLevelDefaultsToNone(t *testing.T) {
	s := authz.New(authz.Params{Subject: "cpo", Roles: []authz.Role{authz.RoleCPO}, AllProducts: authz.AccessPrivate})
	if s.Finance() != authz.FinanceNone {
		t.Fatal("финансовый уровень выдаётся явно")
	}
}
