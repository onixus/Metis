package portfoliograph_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	pg "github.com/onixus/metis/internal/portfoliograph"
	"github.com/shopspring/decimal"
)

func TestPG02_ReadHierarchyAndRejectForeignCapability(t *testing.T) {
	f := newFixture(t)
	cap, err := f.svc.CreateCapability(f.ctx, f.cpo, f.edr, "Response")
	if err != nil {
		t.Fatal(err)
	}
	feature := f.feature(f.edr, "API", kernel.Date{})
	requirement, err := f.svc.CreateRequirement(f.ctx, f.cpo, feature, "Provide typed response")
	if err != nil {
		t.Fatal(err)
	}
	cs, err := f.svc.Capabilities(f.ctx, f.cpo, f.edr)
	if err != nil || len(cs) != 1 || cs[0].ID != cap.ID {
		t.Fatalf("capabilities=%v err=%v", cs, err)
	}
	rs, err := f.svc.Requirements(f.ctx, f.cpo, feature)
	if err != nil || len(rs) != 1 || rs[0].ID != requirement.ID {
		t.Fatalf("requirements=%v err=%v", rs, err)
	}
	strategic := pmScope("hub", map[kernel.ID]authz.Access{f.edr: authz.AccessStrategic})
	if _, err := f.svc.Capabilities(f.ctx, strategic, f.edr); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("capabilities disclosure: %v", err)
	}
	if _, err := f.svc.Requirements(f.ctx, strategic, feature); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("requirements disclosure: %v", err)
	}
	foreign, err := f.svc.CreateCapability(f.ctx, f.cpo, f.soar, "Other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.UpdateFeature(f.ctx, f.cpo, feature, pg.FeatureInput{CapabilityID: foreign.ID}); err == nil {
		t.Fatal("foreign capability accepted")
	}
	updated, err := f.svc.Feature(f.ctx, f.cpo, feature)
	if err != nil || updated.CapabilityID != kernel.NilID {
		t.Fatalf("feature modified: %+v %v", updated, err)
	}
}

func TestPG04_EditContractReplacesDependenciesAndPreservesManualLinks(t *testing.T) {
	f := newFixture(t)
	p1 := f.feature(f.edr, "Provider1", kernel.Date{})
	p2 := f.feature(f.edr, "Provider2", kernel.Date{})
	consumer := f.feature(f.soar, "Consumer", kernel.Date{})
	manual, err := f.dep(consumer, p1, pg.CritDesirable)
	if err != nil {
		t.Fatal(err)
	}
	in := pg.ContractInput{Name: "Response", ProviderProductID: f.edr, ConsumerProductID: f.soar, ProviderFeatureIDs: []kernel.ID{p1}, ConsumerFeatureIDs: []kernel.ID{consumer}, Criticality: pg.CritBlocks}
	c, err := f.svc.SaveContract(f.ctx, f.cpo, kernel.NilID, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetFeatureOwnValue(f.ctx, f.cpo, consumer, kernel.Money{Amount: 100, Currency: "RUB"}); err != nil {
		t.Fatal(err)
	}
	v, err := f.svc.FeatureValue(f.ctx, f.cpo, p1)
	if err != nil || v.DerivedValue.Amount != 100 {
		t.Fatalf("parallel contract edge missing: %+v %v", v, err)
	}
	in.ProviderFeatureIDs = []kernel.ID{p2}
	in.Criticality = pg.CritAccelerates
	if _, err := f.svc.SaveContract(f.ctx, f.cpo, c.ID, in); err != nil {
		t.Fatal(err)
	}
	links, err := f.svc.Links(f.ctx, f.cpo)
	if err != nil {
		t.Fatal(err)
	}
	generated := 0
	manualFound := false
	for _, l := range links {
		if l.ContractID == c.ID {
			if err := f.svc.DeleteLink(f.ctx, f.cpo, l.ID); !errors.Is(err, kernel.ErrConflict) {
				t.Fatalf("deleted contract edge: %v", err)
			}
		}
	}
	for _, l := range links {
		if l.ID == manual.ID {
			manualFound = true
		}
		if l.ContractID == c.ID {
			generated++
			if l.Criticality != pg.CritAccelerates || l.ToFeatureID == p1 {
				t.Fatalf("obsolete edge: %+v", l)
			}
		}
	}
	if generated != 2 || !manualFound {
		t.Fatalf("generated=%d manual=%v", generated, manualFound)
	}
	v, err = f.svc.FeatureValue(f.ctx, f.cpo, p1)
	if err != nil || v.DerivedValue.Amount != 20 {
		t.Fatalf("manual rollup=%+v %v", v, err)
	}
	v, err = f.svc.FeatureValue(f.ctx, f.cpo, p2)
	if err != nil || v.DerivedValue.Amount != 50 {
		t.Fatalf("contract rollup=%+v %v", v, err)
	}
	if err := f.svc.Load(f.ctx); err != nil {
		t.Fatal(err)
	}
	links, err = f.svc.Links(f.ctx, f.cpo)
	if err != nil || len(links) != 3 {
		t.Fatalf("persisted links=%v err=%v", links, err)
	}
}

func TestPG05_ContractReplacementCycleLeavesOriginalGraph(t *testing.T) {
	f := newFixture(t)
	p := f.feature(f.edr, "P", kernel.Date{})
	c := f.feature(f.soar, "C", kernel.Date{})
	other := f.feature(f.edr, "Other", kernel.Date{})
	in := pg.ContractInput{Name: "Contract", ProviderProductID: f.edr, ConsumerProductID: f.soar, ProviderFeatureIDs: []kernel.ID{p}, ConsumerFeatureIDs: []kernel.ID{c}, Criticality: pg.CritBlocks}
	contract, err := f.svc.SaveContract(f.ctx, f.cpo, kernel.NilID, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.dep(other, c, pg.CritBlocks); err != nil {
		t.Fatal(err)
	}
	in.ProviderFeatureIDs = []kernel.ID{other}
	if _, err := f.svc.SaveContract(f.ctx, f.cpo, contract.ID, in); err == nil {
		t.Fatal("cycle accepted")
	}
	old, _, err := f.svc.Contract(f.ctx, f.cpo, contract.ID)
	if err != nil || len(old.ProviderFeatureIDs) != 1 || old.ProviderFeatureIDs[0] != p {
		t.Fatalf("contract mutated %+v %v", old, err)
	}
}

func TestPG07_SettingsReordersParallelCriticalityAndDoesNotAlias(t *testing.T) {
	f := newFixture(t)
	p := f.feature(f.edr, "P", kernel.Date{})
	c := f.feature(f.soar, "C", kernel.Date{})
	if _, err := f.dep(c, p, pg.CritBlocks); err != nil {
		t.Fatal(err)
	}
	if _, err := f.dep(c, p, pg.CritDesirable); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetFeatureOwnValue(f.ctx, f.cpo, c, kernel.Money{Amount: 100, Currency: "RUB"}); err != nil {
		t.Fatal(err)
	}
	admin := authz.New(authz.Params{Subject: "admin", Roles: []authz.Role{authz.RoleAdmin}, AllProducts: authz.AccessPrivate})
	st := pg.DefaultSettings()
	st.Coefficients[pg.CritBlocks] = decimal.RequireFromString("0.1")
	st.Coefficients[pg.CritDesirable] = decimal.RequireFromString("0.8")
	if err := f.svc.UpdateSettings(f.ctx, admin, st); err != nil {
		t.Fatal(err)
	}
	st.Coefficients[pg.CritDesirable] = decimal.Zero
	read, err := f.svc.Settings(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	read.Coefficients[pg.CritDesirable] = decimal.Zero
	v, err := f.svc.FeatureValue(f.ctx, f.cpo, p)
	if err != nil || v.DerivedValue.Amount != 80 {
		t.Fatalf("settings stale/aliased %+v %v", v, err)
	}
}

func TestPG08_PropagationUsesIntermediateAndOtherSupplierDates(t *testing.T) {
	f := newFixture(t)
	a := f.feature(f.edr, "A", d(2026, 10, 1))
	b := f.feature(f.soar, "B", d(2026, 12, 1))
	c := f.feature(f.vm, "C", d(2026, 10, 1))
	other := f.feature(f.deception, "Other", d(2027, 1, 1))
	for _, edge := range [][2]kernel.ID{{b, a}, {c, b}, {c, other}} {
		if _, err := f.dep(edge[0], edge[1], pg.CritBlocks); err != nil {
			t.Fatal(err)
		}
	}
	res, err := f.svc.ShiftFeatureDate(f.ctx, f.cpo, a, d(2026, 11, 1), "delay")
	if err != nil {
		t.Fatal(err)
	}
	for _, affected := range res.Affected {
		if affected.FeatureID == c && affected.ImpliedDate != d(2027, 1, 1) {
			t.Fatalf("lost supplier date: %+v", affected)
		}
	}
	res, err = f.svc.ShiftFeatureDate(f.ctx, f.cpo, a, d(2026, 11, 2), "another delay")
	if err != nil {
		t.Fatal(err)
	}
	for _, affected := range res.Affected {
		if affected.FeatureID == c && affected.ImpliedDate != d(2027, 1, 1) {
			t.Fatalf("lowered supplier date: %+v", affected)
		}
	}
}

type failingContractStore struct{ *pg.MemStore }

func (s failingContractStore) ReplaceContract(context.Context, authz.Scope, pg.IntegrationContract, []pg.Link) error {
	return kernel.ErrConflict
}

func TestPG04_FailedContractPersistenceLeavesMemoryUnchanged(t *testing.T) {
	f := newFixture(t)
	svc := pg.NewService(failingContractStore{f.store}, nil, kernel.SystemClock{})
	in := pg.ContractInput{Name: "Failed", ProviderProductID: f.edr, ConsumerProductID: f.soar, Criticality: pg.CritBlocks}
	if _, err := svc.SaveContract(f.ctx, f.cpo, kernel.NilID, in); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("failure=%v", err)
	}
	cs, err := svc.Contracts(f.ctx, f.cpo)
	if err != nil || len(cs) != 0 {
		t.Fatalf("partial contract %v %v", cs, err)
	}
	ls, err := svc.Links(f.ctx, f.cpo)
	if err != nil || len(ls) != 0 {
		t.Fatalf("partial links %v %v", ls, err)
	}
}

func TestPG04_ContractRejectsDuplicateFeaturesAndEndpointChange(t *testing.T) {
	f := newFixture(t)
	p := f.feature(f.edr, "P", kernel.Date{})
	in := pg.ContractInput{Name: "Contract", ProviderProductID: f.edr, ConsumerProductID: f.soar, ProviderFeatureIDs: []kernel.ID{p, p}, Criticality: pg.CritBlocks}
	if _, err := f.svc.SaveContract(f.ctx, f.cpo, kernel.NilID, in); err == nil {
		t.Fatal("duplicate features accepted")
	}
	in.ProviderFeatureIDs = []kernel.ID{p}
	c, err := f.svc.SaveContract(f.ctx, f.cpo, kernel.NilID, in)
	if err != nil {
		t.Fatal(err)
	}
	in.ConsumerProductID = f.vm
	if _, err := f.svc.SaveContract(f.ctx, f.cpo, c.ID, in); err == nil {
		t.Fatal("endpoint change accepted")
	}
	in.ConsumerProductID = f.soar
	in.Status = pg.ContractStatus("unknown")
	if _, err := f.svc.SaveContract(f.ctx, f.cpo, c.ID, in); err == nil {
		t.Fatal("unknown status accepted")
	}
}

func TestPG08_EarlierSourceRetainsUpstreamConstraintAndReleasesStaleDates(t *testing.T) {
	f := newFixture(t)
	upstream := f.feature(f.edr, "Upstream", d(2027, 1, 1))
	source := f.feature(f.soar, "Source", d(2026, 11, 1))
	consumer := f.feature(f.vm, "Consumer", d(2026, 12, 1))
	if _, err := f.dep(source, upstream, pg.CritBlocks); err != nil {
		t.Fatal(err)
	}
	if _, err := f.dep(consumer, source, pg.CritBlocks); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ShiftFeatureDate(f.ctx, f.cpo, source, d(2027, 2, 1), "late source"); err != nil {
		t.Fatal(err)
	}
	res, err := f.svc.ShiftFeatureDate(f.ctx, f.cpo, source, d(2026, 10, 1), "earlier source")
	if err != nil || len(res.Affected) != 1 || res.Affected[0].ImpliedDate != d(2027, 1, 1) {
		t.Fatalf("upstream constraint lost: %+v %v", res, err)
	}
	for _, id := range []kernel.ID{source, consumer} {
		ft, err := f.svc.Feature(f.ctx, f.cpo, id)
		if err != nil || !ft.Affected || ft.ImpliedDate != d(2027, 1, 1) {
			t.Fatalf("effective source/dependent: %+v %v", ft, err)
		}
	}
	res, err = f.svc.ShiftFeatureDate(f.ctx, f.cpo, upstream, d(2026, 9, 1), "upstream finished earlier")
	if err != nil || len(res.Affected) != 2 {
		t.Fatalf("earlier upstream result: %+v %v", res, err)
	}
	if err := f.svc.Load(f.ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []kernel.ID{source, consumer} {
		ft, err := f.svc.Feature(f.ctx, f.cpo, id)
		if err != nil || ft.Affected || !ft.ImpliedDate.IsZero() || ft.AffectedBy != kernel.NilID {
			t.Fatalf("persisted stale constraints: %+v %v", ft, err)
		}
	}
}
