package identityaccess_test

import (
	"errors"
	"testing"

	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

func TestAD02_NFS01_PnLCalculationScopeIsAuthorizedAndReadOnly(t *testing.T) {
	own, foreign := kernel.NewID(), kernel.NewID()
	caller := authz.New(authz.Params{Subject: "pm", Roles: []authz.Role{authz.RolePM}, Products: map[kernel.ID]authz.Access{own: authz.AccessPrivate}, Finance: authz.FinanceAggregates})
	for _, product := range []kernel.ID{kernel.NilID, foreign} {
		if sc, err := identityaccess.ProductPnLCalculationScope(caller, product); !errors.Is(err, kernel.ErrForbidden) || sc.Valid() {
			t.Fatalf("forbidden target: %+v %v", sc, err)
		}
	}
	if sc, err := identityaccess.ProductPnLCalculationScope(authz.Scope{}, own); !errors.Is(err, kernel.ErrForbidden) || sc.Valid() {
		t.Fatalf("zero caller: %+v %v", sc, err)
	}
	calculated, err := identityaccess.ProductPnLCalculationScope(caller, own)
	if err != nil {
		t.Fatal(err)
	}
	if !calculated.Allows(authz.ActionReadModelFinance, foreign) {
		t.Fatal("fixed P&L lacks cross-product inputs")
	}
	if len(calculated.Roles()) != 0 || calculated.Allows(authz.ActionWriteModelFinance, own) || calculated.Allows(authz.ActionWriteModelFinance, kernel.NilID) {
		t.Fatal("calculation gained write permission")
	}
	if caller.Allows(authz.ActionReadModelFinance, foreign) || caller.Finance() != authz.FinanceAggregates {
		t.Fatal("caller permissions changed")
	}
	formula := identityaccess.ModelCalculationScope(caller)
	if formula.Allows(authz.ActionReadModelFinance, foreign) {
		t.Fatal("arbitrary formula gained cross-product access")
	}
}
