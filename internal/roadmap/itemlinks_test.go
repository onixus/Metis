package roadmap_test

import (
	"testing"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/roadmap"
)

// TestCT03_ItemLinksReturnsFeatureAndRelease — порт RoadmapReader для commitments: привязки элемента.
func TestCT03_ItemLinksReturnsFeatureAndRelease(t *testing.T) {
	f := newFixture(t)
	feature := kernel.NewID()
	rel := f.release(f.edr, "1.0", d(2026, 12, 1))
	it, err := f.svc.CreateItem(f.ctx, f.cpo, f.edr, roadmap.ItemInput{Title: "x", Bucket: roadmap.BucketNow, FeatureID: feature, ReleaseID: rel.ID, Audience: authz.AudienceInternal})
	if err != nil {
		t.Fatal(err)
	}
	fid, rid, err := f.svc.ItemLinks(f.ctx, f.cpo, it.ID)
	if err != nil || fid != feature || rid != rel.ID {
		t.Fatalf("links: %v %v %v", fid, rid, err)
	}
	if _, _, err := f.svc.ItemLinks(f.ctx, authz.Scope{}, it.ID); err == nil {
		t.Fatal("нулевой Scope должен быть отклонён")
	}
	// PM VM не читает элементы EDR (ABAC-отказ).
	if _, _, err := f.svc.ItemLinks(f.ctx, pmScope("pm-vm", f.vm), it.ID); err == nil {
		t.Fatal("PM VM не должен читать привязки элемента EDR")
	}
}
