package economics

import (
	"context"
	"errors"
	"testing"

	book "github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

func TestEC05_NFS01_NFS02_CanonicalFeatureAndBranchPreserveCallerScope(t *testing.T) {
	s, _, sc, owner, p := historyFixture(t)
	ctx := context.Background()
	feature := kernel.NewID()
	aggregate := authz.New(authz.Params{Subject: "synthetic-aggregate", Roles: []authz.Role{authz.RoleFinance}, AllProducts: authz.AccessPrivate, Finance: authz.FinanceAggregates})
	for _, caller := range []authz.Scope{sc, aggregate} {
		if _, err := s.FeatureEconomics(ctx, caller, owner, feature, p); err != nil {
			t.Fatalf("legacy feature: %v", err)
		}
		if _, err := s.BranchCosts(ctx, caller, owner, p); err != nil {
			t.Fatalf("legacy branches: %v", err)
		}
	}
	in := HistoryUnificationInput{BookProductID: owner, Period: p, ExpectedBookVersion: 2, ExpectedModelVersion: 2}
	preview, err := s.PreviewUnification(ctx, sc, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Digest = preview.Digest
	if _, err := s.ApplyUnification(ctx, sc, in); err != nil {
		t.Fatal(err)
	}
	for _, period := range []Period{p, {}} {
		value, err := s.FeatureEconomics(ctx, sc, owner, feature, period)
		if err != nil || value.Investment.Amount != 0 || value.Revenue.Amount != 0 {
			t.Fatalf("default-config canonical feature: %+v %v", value, err)
		}
		branches, err := s.BranchCosts(ctx, sc, owner, period)
		if err != nil || len(branches) != 0 {
			t.Fatalf("default-config canonical branches: %+v %v", branches, err)
		}
	}
	other := authz.New(authz.Params{Subject: "synthetic-other", Roles: []authz.Role{authz.RoleFinance}, Products: map[kernel.ID]authz.Access{kernel.NewID(): authz.AccessPrivate}, Finance: authz.FinanceFull})
	for name, caller := range map[string]authz.Scope{"zero": {}, "aggregates": aggregate, "other-product": other} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.FeatureEconomics(ctx, caller, owner, feature, p); !errors.Is(err, kernel.ErrForbidden) {
				t.Fatalf("feature should deny: %v", err)
			}
			if _, err := s.BranchCosts(ctx, caller, owner, p); !errors.Is(err, kernel.ErrForbidden) {
				t.Fatalf("branches should deny: %v", err)
			}
		})
	}
}

func TestEC05_NFS02_CanonicalFeatureAndBranchConfiguredInputsKeepExactValues(t *testing.T) {
	s, books, sc, owner, p := historyFixture(t)
	ctx := context.Background()
	feature := kernel.NewID()
	snapshot, err := books.Snapshot(ctx, sc, owner, p.String(), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"feature_cost", "feature_revenue", "branch_cost"} {
		snapshot.Fields = append(snapshot.Fields, book.Field{Key: key, Name: key, Type: "money", Source: "manual"})
	}
	snapshot.Rows = append(snapshot.Rows,
		book.Row{ProductID: owner, Category: book.DirectCost, Amount: kernel.RUB(0), ModelItem: FeatureItem(feature), Values: map[string]string{"feature_cost": "1001", "feature_revenue": "2502"}},
		book.Row{ProductID: owner, Category: book.DirectCost, Amount: kernel.RUB(0), ModelItem: BranchItem("synthetic/1"), Values: map[string]string{"branch_cost": "703"}},
	)
	if _, err := books.Save(ctx, sc, book.SaveInput{ProductID: owner, Period: p.String(), Currency: snapshot.Currency, ExpectedVersion: snapshot.Version, Rows: snapshot.Rows, Fields: snapshot.Fields}); err != nil {
		t.Fatal(err)
	}
	in := HistoryUnificationInput{BookProductID: owner, Period: p, ExpectedBookVersion: 3, ExpectedModelVersion: 2}
	preview, err := s.PreviewUnification(ctx, sc, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Digest = preview.Digest
	if _, err := s.ApplyUnification(ctx, sc, in); err != nil {
		t.Fatal(err)
	}
	// Config explicitly selects the migrated custom inputs. This test does not
	// assume an automatic mapping of default feature fields to book metadata.
	cfg := DefaultConfig()
	cfg.FeatureCostField = preview.DefinitionMappings["feature_cost"]
	cfg.FeatureRevenueField = preview.DefinitionMappings["feature_revenue"]
	cfg.BranchCostField = preview.DefinitionMappings["branch_cost"]
	configured, err := NewService(s.store, nil, kernel.SystemClock{}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	configured.WithAuditor(historyAuditor{})
	for _, period := range []Period{p, {}} {
		value, err := configured.FeatureEconomics(ctx, sc, owner, feature, period)
		if err != nil || value.Investment.Amount != 1001 || value.Revenue.Amount != 2502 || value.Balance.Amount != 1501 || value.Balance.Currency != "RUB" {
			t.Fatalf("canonical feature values: %+v %v", value, err)
		}
		branches, err := configured.BranchCosts(ctx, sc, owner, period)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, branch := range branches {
			if branch.Branch == "synthetic/1" {
				found = branch.Cost.Amount == 703 && branch.Cost.Currency == "RUB"
			} else if branch.Cost.Amount != 0 {
				t.Fatalf("unrelated canonical item received branch costs: %+v", branch)
			}
		}
		if !found {
			t.Fatalf("canonical branch amount missing: %+v", branches)
		}
	}
}
