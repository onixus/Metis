package architecture_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onixus/metis/internal/analytics"
	"github.com/onixus/metis/internal/audit"
	auditpg "github.com/onixus/metis/internal/audit/pgstore"
	"github.com/onixus/metis/internal/commitments"
	commitmentpg "github.com/onixus/metis/internal/commitments/pgstore"
	"github.com/onixus/metis/internal/compliance"
	compliancepg "github.com/onixus/metis/internal/compliance/pgstore"
	"github.com/onixus/metis/internal/decisions"
	decisionpg "github.com/onixus/metis/internal/decisions/pgstore"
	"github.com/onixus/metis/internal/delivery"
	deliverypg "github.com/onixus/metis/internal/delivery/pgstore"
	"github.com/onixus/metis/internal/discovery"
	discoverypg "github.com/onixus/metis/internal/discovery/pgstore"
	"github.com/onixus/metis/internal/economics"
	modeling "github.com/onixus/metis/internal/economics/modeling"
	economicspg "github.com/onixus/metis/internal/economics/pgstore"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/outbox"
	"github.com/onixus/metis/internal/portfoliograph"
	graphpg "github.com/onixus/metis/internal/portfoliograph/pgstore"
	"github.com/onixus/metis/internal/prioritization"
	prioritypg "github.com/onixus/metis/internal/prioritization/pgstore"
	"github.com/onixus/metis/internal/roadmap"
	roadmappg "github.com/onixus/metis/internal/roadmap/pgstore"
	"github.com/onixus/metis/internal/signals"
	signalpg "github.com/onixus/metis/internal/signals/pgstore"
)

// Calling PG stores without a DB also proves that invalid scopes fail before I/O.
func TestNFS01_AllProductStoresRejectZeroScopeBeforeIO(t *testing.T) {
	stores := []any{analytics.NewMemStore(), commitments.NewMemStore(), compliance.NewMemStore(), decisions.NewMemStore(), delivery.NewMemStore(), discovery.NewMemStore(), economics.NewMemStore(), modeling.NewMemStore(), portfoliograph.NewMemStore(), prioritization.NewMemStore(), roadmap.NewMemStore(), signals.NewMemStore(),
		commitmentpg.New(nil, nil), compliancepg.New(nil), decisionpg.New(nil), deliverypg.New(nil, nil), discoverypg.New(nil), economicspg.New(nil), graphpg.NewStore(nil, nil), prioritypg.New(nil), roadmappg.New(nil), signalpg.New(nil)}
	stores = append(stores, infrastructureStores()...)
	assertStoresRejectScope(t, stores, authz.Scope{})
}

func infrastructureStores() []any {
	return []any{audit.NewMemStore(), auditpg.New(nil), compliance.NewEvidenceMemStore(), compliancepg.NewEvidenceStore(nil), outbox.NewMemStore(), outbox.NewPGStore(nil)}
}

func TestAD02_NFS01_InfrastructureRejectsLimitedAndUserScopes(t *testing.T) {
	scopes := []authz.Scope{
		authz.New(authz.Params{Subject: "admin", Roles: []authz.Role{authz.RoleAdmin}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal}),
		authz.New(authz.Params{Subject: "limited-service", Roles: []authz.Role{authz.RoleService}, Products: map[kernel.ID]authz.Access{kernel.NewID(): authz.AccessPrivate}, Audience: authz.AudienceInternal}),
		authz.New(authz.Params{Subject: "strategic-service", Roles: []authz.Role{authz.RoleService}, AllProducts: authz.AccessStrategic, Audience: authz.AudienceInternal}),
		authz.New(authz.Params{Subject: "external-service", Roles: []authz.Role{authz.RoleService}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceSalesSafe}),
	}
	for _, sc := range scopes {
		t.Run(sc.Subject(), func(t *testing.T) { assertStoresRejectScope(t, infrastructureStores(), sc) })
	}
}

func assertStoresRejectScope(t *testing.T, stores []any, sc authz.Scope) {
	t.Helper()
	ctxType := reflect.TypeFor[context.Context]()
	scopeType := reflect.TypeFor[authz.Scope]()
	for _, store := range stores {
		v := reflect.ValueOf(store)
		for i := 0; i < v.NumMethod(); i++ {
			method := v.Type().Method(i)
			fn := v.Method(i)
			if fn.Type().NumIn() == 0 || fn.Type().In(0) != ctxType {
				continue
			}
			t.Run(v.Type().Elem().PkgPath()+"/"+v.Type().Elem().Name()+"/"+method.Name, func(t *testing.T) {
				if fn.Type().NumIn() < 2 || fn.Type().In(1) != scopeType {
					t.Fatal("repository method lacks explicit Scope")
				}
				args := []reflect.Value{reflect.ValueOf(context.Background())}
				for j := 1; j < fn.Type().NumIn(); j++ {
					args = append(args, reflect.Zero(fn.Type().In(j)))
				}
				args[1] = reflect.ValueOf(sc)
				var result []reflect.Value
				if fn.Type().IsVariadic() {
					result = fn.CallSlice(args)
				} else {
					result = fn.Call(args)
				}
				if len(result) == 0 {
					t.Fatal("repository operation lacks error result")
				}
				err, ok := result[len(result)-1].Interface().(error)
				if !ok || !errors.Is(err, kernel.ErrForbidden) {
					t.Fatalf("zero scope: %v", result[len(result)-1].Interface())
				}
			})
		}
	}
}

func TestNFM01_StoreInterfacesRequireExplicitScope(t *testing.T) {
	root := filepath.Join("..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok || !strings.HasSuffix(spec.Name.Name, "Store") {
				return true
			}
			iface, ok := spec.Type.(*ast.InterfaceType)
			if !ok {
				return true
			}
			for _, field := range iface.Methods.List {
				fn, ok := field.Type.(*ast.FuncType)
				if !ok {
					t.Errorf("%s: embedded Store interface requires explicit review", rel)
					continue
				}
				if len(fn.Params.List) < 2 || !selector(fn.Params.List[0].Type, "context", "Context") || !selector(fn.Params.List[1].Type, "authz", "Scope") {
					t.Errorf("%s: %s requires (context.Context, authz.Scope, ...)", rel, field.Names[0].Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func selector(expr ast.Expr, pkg, name string) bool {
	s, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	p, ok := s.X.(*ast.Ident)
	return ok && p.Name == pkg && s.Sel.Name == name
}
