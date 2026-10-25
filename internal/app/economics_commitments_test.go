package app

import (
	"context"
	"errors"
	"testing"

	"github.com/onixus/metis/internal/commitments"
	modeling "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/roadmap"
)

type scenarioRoadmapStub struct {
	items     []roadmap.RoadmapItem
	release   roadmap.Release
	err       error
	seenScope authz.Scope
}

func (s *scenarioRoadmapStub) Items(_ context.Context, sc authz.Scope, _ kernel.ID) ([]roadmap.RoadmapItem, error) {
	s.seenScope = sc
	return s.items, s.err
}
func (s *scenarioRoadmapStub) Release(_ context.Context, sc authz.Scope, _ kernel.ID) (roadmap.Release, error) {
	s.seenScope = sc
	return s.release, s.err
}

type scenarioFeaturesStub struct {
	feature   portfoliograph.Feature
	err       error
	seenScope authz.Scope
}

func (s *scenarioFeaturesStub) Feature(_ context.Context, sc authz.Scope, _ kernel.ID) (portfoliograph.Feature, error) {
	s.seenScope = sc
	return s.feature, s.err
}

func TestDA02_EC13_LinkedPlanPrecedenceAndUnknownDates(t *testing.T) {
	product, featureID, releaseID, renewalID, commitmentID := kernel.NewID(), kernel.NewID(), kernel.NewID(), kernel.NewID(), kernel.NewID()
	planned := kernel.DateOf(2028, 1, 10)
	other := kernel.DateOf(2028, 2, 10)
	for _, tt := range []struct {
		name           string
		configure      func(*commitments.Commitment, *scenarioRoadmapStub, *scenarioFeaturesStub) []roadmap.RoadmapItem
		source, status string
		date           kernel.Date
		err            error
	}{
		{name: "renewal precedence", source: "renewal", status: "known", date: planned, configure: func(c *commitments.Commitment, r *scenarioRoadmapStub, _ *scenarioFeaturesStub) []roadmap.RoadmapItem {
			c.RenewalItemID = renewalID
			r.release.PlannedDate = other
			return []roadmap.RoadmapItem{{ID: renewalID, ProductID: product, CommitmentID: commitmentID, EndDate: planned, Status: roadmap.ItemPlanned}}
		}},
		{name: "missing renewal no fallback", source: "renewal", status: "missing", configure: func(c *commitments.Commitment, _ *scenarioRoadmapStub, _ *scenarioFeaturesStub) []roadmap.RoadmapItem {
			c.RenewalItemID = renewalID
			return nil
		}},
		{name: "wrong renewal commitment", source: "renewal", status: "ambiguous", configure: func(c *commitments.Commitment, _ *scenarioRoadmapStub, _ *scenarioFeaturesStub) []roadmap.RoadmapItem {
			c.RenewalItemID = renewalID
			return []roadmap.RoadmapItem{{ID: renewalID, ProductID: product, CommitmentID: kernel.NewID(), EndDate: planned}}
		}},
		{name: "completed renewal", source: "renewal", status: "completed", configure: func(c *commitments.Commitment, _ *scenarioRoadmapStub, _ *scenarioFeaturesStub) []roadmap.RoadmapItem {
			c.RenewalItemID = renewalID
			return []roadmap.RoadmapItem{{ID: renewalID, ProductID: product, CommitmentID: commitmentID, EndDate: planned, Status: roadmap.ItemDone}}
		}},
		{name: "release precedence", source: "release", status: "known", date: planned, configure: func(_ *commitments.Commitment, _ *scenarioRoadmapStub, _ *scenarioFeaturesStub) []roadmap.RoadmapItem {
			return []roadmap.RoadmapItem{{ProductID: product, FeatureID: featureID, EndDate: other, Status: roadmap.ItemPlanned}}
		}},
		{name: "missing release no fallback", source: "release", status: "missing", configure: func(_ *commitments.Commitment, r *scenarioRoadmapStub, _ *scenarioFeaturesStub) []roadmap.RoadmapItem {
			r.err = kernel.ErrNotFound
			return nil
		}},
		{name: "completed release", source: "release", status: "completed", configure: func(_ *commitments.Commitment, r *scenarioRoadmapStub, _ *scenarioFeaturesStub) []roadmap.RoadmapItem {
			r.release.Status = roadmap.ReleaseReleased
			return nil
		}},
		{name: "forbidden release", err: kernel.ErrForbidden, configure: func(_ *commitments.Commitment, r *scenarioRoadmapStub, _ *scenarioFeaturesStub) []roadmap.RoadmapItem {
			r.err = kernel.ErrForbidden
			return nil
		}},
		{name: "foreign release", err: kernel.ErrForbidden, configure: func(_ *commitments.Commitment, r *scenarioRoadmapStub, _ *scenarioFeaturesStub) []roadmap.RoadmapItem {
			r.release.ProductID = kernel.NewID()
			return nil
		}},
		{name: "feature roadmap", source: "feature_roadmap", status: "known", date: planned, configure: func(c *commitments.Commitment, _ *scenarioRoadmapStub, _ *scenarioFeaturesStub) []roadmap.RoadmapItem {
			c.ReleaseID = kernel.NilID
			return []roadmap.RoadmapItem{{ProductID: product, FeatureID: featureID, EndDate: planned, Status: roadmap.ItemPlanned}}
		}},
		{name: "conflicting feature dates", source: "feature_roadmap", status: "ambiguous", configure: func(c *commitments.Commitment, _ *scenarioRoadmapStub, _ *scenarioFeaturesStub) []roadmap.RoadmapItem {
			c.ReleaseID = kernel.NilID
			return []roadmap.RoadmapItem{{ProductID: product, FeatureID: featureID, EndDate: planned, Status: roadmap.ItemPlanned}, {ProductID: product, FeatureID: featureID, EndDate: other, Status: roadmap.ItemPlanned}}
		}},
		{name: "undated feature item", source: "feature_roadmap", status: "missing", configure: func(c *commitments.Commitment, _ *scenarioRoadmapStub, _ *scenarioFeaturesStub) []roadmap.RoadmapItem {
			c.ReleaseID = kernel.NilID
			return []roadmap.RoadmapItem{{ProductID: product, FeatureID: featureID, Status: roadmap.ItemPlanned}}
		}},
		{name: "missing versus dated ambiguous", source: "feature_roadmap", status: "ambiguous", configure: func(c *commitments.Commitment, _ *scenarioRoadmapStub, _ *scenarioFeaturesStub) []roadmap.RoadmapItem {
			c.ReleaseID = kernel.NilID
			return []roadmap.RoadmapItem{{ProductID: product, FeatureID: featureID, Status: roadmap.ItemPlanned}, {ProductID: product, FeatureID: featureID, EndDate: planned, Status: roadmap.ItemPlanned}}
		}},
		{name: "completed feature items", source: "feature", status: "completed", configure: func(c *commitments.Commitment, _ *scenarioRoadmapStub, _ *scenarioFeaturesStub) []roadmap.RoadmapItem {
			c.ReleaseID = kernel.NilID
			return []roadmap.RoadmapItem{{ProductID: product, FeatureID: featureID, EndDate: planned, Status: roadmap.ItemDone}}
		}},
		{name: "feature own planned date", source: "feature", status: "known", date: other, configure: func(c *commitments.Commitment, _ *scenarioRoadmapStub, _ *scenarioFeaturesStub) []roadmap.RoadmapItem {
			c.ReleaseID = kernel.NilID
			return nil
		}},
		{name: "missing feature", source: "feature", status: "missing", configure: func(c *commitments.Commitment, _ *scenarioRoadmapStub, f *scenarioFeaturesStub) []roadmap.RoadmapItem {
			c.ReleaseID = kernel.NilID
			f.err = kernel.ErrNotFound
			return nil
		}},
		{name: "forbidden feature", err: kernel.ErrForbidden, configure: func(c *commitments.Commitment, _ *scenarioRoadmapStub, f *scenarioFeaturesStub) []roadmap.RoadmapItem {
			c.ReleaseID = kernel.NilID
			f.err = kernel.ErrForbidden
			return nil
		}},
		{name: "foreign feature", err: kernel.ErrForbidden, configure: func(c *commitments.Commitment, _ *scenarioRoadmapStub, f *scenarioFeaturesStub) []roadmap.RoadmapItem {
			c.ReleaseID = kernel.NilID
			f.feature.ProductID = kernel.NewID()
			return nil
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := commitments.Commitment{ID: commitmentID, ProductID: product, FeatureID: featureID, ReleaseID: releaseID}
			r := &scenarioRoadmapStub{release: roadmap.Release{ID: releaseID, ProductID: product, PlannedDate: planned, Status: roadmap.ReleasePlanned}}
			f := &scenarioFeaturesStub{feature: portfoliograph.Feature{ID: featureID, ProductID: product, PlannedDate: other}}
			items := tt.configure(&c, r, f)
			ref := modeling.CommitmentRef{PlanningSource: "none", PlanningStatus: "missing"}
			sc := identityaccess.FinanceServiceScope("synthetic-plan-test")
			err := (commitmentsAdapter{roadmap: r, features: f}).commitmentPlan(context.Background(), sc, c, items, &ref)
			if tt.err != nil {
				if !errors.Is(err, tt.err) {
					t.Fatalf("expected %v, got %v", tt.err, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if ref.PlanningSource != tt.source || ref.PlanningStatus != tt.status || ref.PlannedDate != tt.date {
				t.Fatalf("wrong plan: %+v", ref)
			}
			if r.seenScope.Valid() && r.seenScope.Subject() != sc.Subject() {
				t.Fatal("roadmap scope replaced")
			}
			if f.seenScope.Valid() && f.seenScope.Subject() != sc.Subject() {
				t.Fatal("feature scope replaced")
			}
		})
	}
}

func TestDA02_NFS01_CommitmentPlansUseScopedPublicModules(t *testing.T) {
	a := runtimeApp(t, Config{}, false)
	feature := runtimeFeature(t, a)
	ctx, setup := context.Background(), identityaccess.ServiceScope("synthetic-scenario-setup")
	date := kernel.DateOf(2028, 1, 10)
	release, err := a.Roadmap.CreateRelease(ctx, setup, feature.ProductID, roadmap.ReleaseInput{Name: "Synthetic", Version: "1.0", PlannedDate: date, Status: roadmap.ReleasePlanned, Branch: roadmap.BranchEvolving})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Commitments.Create(ctx, setup, feature.ProductID, commitments.Input{Kind: commitments.KindCustomer, Counterparty: "Synthetic counterparty", Subject: "Synthetic delivery", DueDate: date.AddDays(5), Basis: "Synthetic contract", Owner: "Synthetic owner", FeatureID: feature.ID, ReleaseID: release.ID})
	if err != nil {
		t.Fatal(err)
	}
	adapter := commitmentsAdapter{svc: a.Commitments, roadmap: a.Roadmap, features: a.Portfolio}
	sc := identityaccess.FinanceServiceScope("synthetic-scenario-finance")
	refs, err := adapter.Due(ctx, sc, []kernel.ID{feature.ProductID, feature.ProductID})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].PlannedDate != date || refs[0].PlanningSource != "release" || refs[0].PlanningStatus != "known" {
		t.Fatalf("unexpected public projection: %+v", refs)
	}
	for _, denied := range []authz.Scope{{}, authz.New(authz.Params{Subject: "foreign", Roles: []authz.Role{authz.RoleFinance}, Finance: authz.FinanceFull, Products: map[kernel.ID]authz.Access{kernel.NewID(): authz.AccessPrivate}})} {
		if _, err := adapter.Due(ctx, denied, []kernel.ID{feature.ProductID}); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("foreign product read allowed: %v", err)
		}
	}
}
