package commitments_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onixus/metis/internal/commitments"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

func TestCT03_RiskOwnerHandlingAndEscalation(t *testing.T) {
	f := newFixture(t)
	feature := kernel.NewID()
	c := f.create(f.cpo, vm, customerInput(kernel.DateOf(2026, 11, 1), feature))
	h := commitments.NewShiftHandler(f.svc, serviceScope())
	if err := h.Handle(f.ctx, shiftEvent(t, feature, kernel.DateOf(2026, 12, 1), nil)); err != nil {
		t.Fatal(err)
	}
	alerts, err := f.svc.Alerts(f.ctx, f.cpo, vm, false)
	if err != nil || len(alerts) != 1 {
		t.Fatalf("alerts: %v %v", alerts, err)
	}
	a := alerts[0]
	if a.RiskOwner != c.Owner {
		t.Fatalf("default owner %q, want %q", a.RiskOwner, c.Owner)
	}
	for _, in := range []commitments.AlertActionInput{
		{Kind: "assigned", Recipient: "pm-backup", Note: "Переназначение во время отпуска"},
		{Kind: "escalated", Recipient: "portfolio-owner", Note: "Нужно согласовать новую дату с заказчиком"},
		{Kind: "noted", Note: "Владелец согласует план восстановления"},
	} {
		a, err = f.svc.HandleAlert(f.ctx, f.cpo, a.ID, in)
		if err != nil {
			t.Fatal(err)
		}
	}
	if a.RiskOwner != "portfolio-owner" || len(a.Actions) != 3 || a.Actions[0].Recipient != "pm-backup" || a.Actions[2].Actor != "cpo" {
		t.Fatalf("risk history: %+v", a)
	}
	if a.Acknowledged {
		t.Fatal("handling implicitly acknowledged alert")
	}
	a, err = f.svc.AcknowledgeAlert(f.ctx, f.cpo, a.ID)
	if err != nil || !a.Acknowledged || a.RiskOwner != "portfolio-owner" || len(a.Actions) != 3 {
		t.Fatalf("ack lost history: %+v %v", a, err)
	}
	if f.pub.count(commitments.EventAlertHandled) != 3 {
		t.Fatal("missing audit events")
	}
	// Returned history must not provide a mutable reference to stored records.
	a.Actions[0].Recipient = "tampered"
	alerts, err = f.svc.Alerts(f.ctx, f.cpo, vm, false)
	if err != nil || alerts[0].Actions[0].Recipient != "pm-backup" {
		t.Fatalf("mutable action history: %v %v", alerts, err)
	}
}

func TestCT03_RiskActionsRejectInvalidAndDenied(t *testing.T) {
	f := newFixture(t)
	feature := kernel.NewID()
	f.create(f.cpo, vm, customerInput(kernel.DateOf(2026, 11, 1), feature))
	if err := commitments.NewShiftHandler(f.svc, serviceScope()).Handle(f.ctx, shiftEvent(t, feature, kernel.DateOf(2026, 12, 1), nil)); err != nil {
		t.Fatal(err)
	}
	alerts, err := f.svc.Alerts(f.ctx, f.cpo, vm, false)
	if err != nil {
		t.Fatal(err)
	}
	id := alerts[0].ID
	for _, in := range []commitments.AlertActionInput{
		{Kind: "escalated", Note: "reason"}, {Kind: "assigned", Recipient: " ", Note: "reason"},
		{Kind: "noted", Note: " "}, {Kind: "noted", Recipient: "owner", Note: "reason"},
		{Kind: "unknown", Note: "reason"},
	} {
		if _, err := f.svc.HandleAlert(f.ctx, f.cpo, id, in); !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("accepted invalid %+v: %v", in, err)
		}
	}
	for _, sc := range []authz.Scope{{}, presaleScope(), pmScope("foreign-pm", map[kernel.ID]authz.Access{edr: authz.AccessPrivate})} {
		if _, err := f.svc.HandleAlert(f.ctx, sc, id, commitments.AlertActionInput{Kind: "escalated", Recipient: "owner", Note: "reason"}); !errors.Is(err, kernel.ErrForbidden) {
			t.Fatalf("scope accepted: %v", err)
		}
	}
	alerts, err = f.svc.Alerts(f.ctx, f.cpo, vm, false)
	if err != nil || len(alerts[0].Actions) != 0 || f.pub.count(commitments.EventAlertHandled) != 0 {
		t.Fatalf("failed writes changed history: %+v %v", alerts, err)
	}
}

func TestCT03_MemAlertUniquePerEvent(t *testing.T) {
	ctx := context.Background()
	store := commitments.NewMemStore()
	c := commitments.Commitment{ID: kernel.NewID(), ProductID: vm, Owner: "pm", Status: commitments.StatusActive}
	if err := store.Save(ctx, serviceScope(), c); err != nil {
		t.Fatal(err)
	}
	a := commitments.Alert{ID: kernel.NewID(), CommitmentID: c.ID, ProductID: vm, EventID: kernel.NewID()}
	if err := store.AppendAlert(ctx, serviceScope(), a); err != nil {
		t.Fatal(err)
	}
	a.ID = kernel.NewID()
	if err := store.AppendAlert(ctx, serviceScope(), a); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("duplicate event accepted: %v", err)
	}
	a.EventID = kernel.NilID
	if err := store.AppendAlert(ctx, serviceScope(), a); err != nil {
		t.Fatal(err)
	}
	a.ID = kernel.NewID()
	if err := store.AppendAlert(ctx, serviceScope(), a); err != nil {
		t.Fatalf("manual alerts must not dedup nil event: %v", err)
	}
}

func TestCT04_RenewalHorizonClampsMonthEnd(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		today, boundary, outside kernel.Date
	}{
		{"non-leap", kernel.DateOf(2027, 1, 31), kernel.DateOf(2027, 2, 28), kernel.DateOf(2027, 3, 1)},
		{"leap", kernel.DateOf(2028, 1, 31), kernel.DateOf(2028, 2, 29), kernel.DateOf(2028, 3, 1)},
		{"thirty-day-month", kernel.DateOf(2026, 3, 31), kernel.DateOf(2026, 4, 30), kernel.DateOf(2026, 5, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if err := f.svc.UpdateSettings(f.ctx, adminScope(), commitments.Settings{LeadMonths: 1}); err != nil {
				t.Fatal(err)
			}
			wanted := f.create(f.cpo, vm, certInput(tc.boundary))
			f.create(f.cpo, vm, certInput(tc.outside))
			created, err := f.svc.EnsureRenewals(f.ctx, f.cpo, tc.today)
			if err != nil || len(created) != 1 || created[0].ID != wanted.ID {
				t.Fatalf("horizon: %+v %v", created, err)
			}
		})
	}
}

type featureOwner struct {
	product kernel.ID
	err     error
}

func (o featureOwner) FeatureProduct(context.Context, authz.Scope, kernel.ID) (kernel.ID, error) {
	return o.product, o.err
}

type releaseOwner struct {
	product kernel.ID
	err     error
}

func (o releaseOwner) ReleaseProduct(context.Context, authz.Scope, kernel.ID) (kernel.ID, error) {
	return o.product, o.err
}

func TestCT01_CommitmentLinksMustBelongToProduct(t *testing.T) {
	f := newFixture(t)
	f.svc.WithLinkOwners(featureOwner{product: edr}, releaseOwner{product: edr})
	for _, field := range []string{"feature", "release"} {
		in := customerInput(kernel.DateOf(2026, 12, 1), kernel.NilID)
		if field == "feature" {
			in.FeatureID = kernel.NewID()
		} else {
			in.ReleaseID = kernel.NewID()
		}
		if _, err := f.svc.Create(f.ctx, f.cpo, vm, in); !errors.Is(err, kernel.ErrValidation) {
			t.Fatalf("foreign %s accepted: %v", field, err)
		}
	}
	f.svc.WithLinkOwners(featureOwner{product: vm}, releaseOwner{product: vm})
	in := customerInput(kernel.DateOf(2026, 12, 1), kernel.NewID())
	in.ReleaseID = kernel.NewID()
	c := f.create(f.cpo, vm, in)
	f.svc.WithLinkOwners(featureOwner{err: kernel.ErrForbidden}, releaseOwner{product: vm})
	if _, err := f.svc.Update(f.ctx, f.cpo, c.ID, in); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("denied link accepted: %v", err)
	}
}
