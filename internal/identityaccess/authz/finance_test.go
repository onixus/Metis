package authz_test

import (
	"testing"

	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

func TestNFS02_NFS16_ExplicitFinancialRoleAndClaimAreBothRequired(t *testing.T) {
	product := kernel.NewID()
	for _, action := range []authz.Action{authz.ActionReadFinance, authz.ActionWriteFinance} {
		for _, sc := range []authz.Scope{{}, identityaccess.ServiceScope("ordinary"),
			authz.New(authz.Params{Subject: "admin", Roles: []authz.Role{authz.RoleAdmin}, AllProducts: authz.AccessPrivate, Finance: authz.FinanceFull}),
			authz.New(authz.Params{Subject: "finance", Roles: []authz.Role{authz.RoleFinance}, AllProducts: authz.AccessPrivate, Finance: authz.FinanceAggregates})} {
			if sc.Allows(action, product) {
				t.Fatalf("financial action %s permitted to missing role/claim", action)
			}
		}
		if !identityaccess.FinanceServiceScope("configured-import").Allows(action, product) {
			t.Fatalf("explicit import service denied %s", action)
		}
		if identityaccess.FinanceServiceScope("configured-import").Allows(action, kernel.NilID) {
			t.Fatal("nil product allowed")
		}
	}
}
