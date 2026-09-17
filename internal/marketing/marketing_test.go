package marketing_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/marketing"
	"github.com/onixus/metis/internal/ports"
)

type crmStub struct {
	accounts []ports.Account
	deals    []ports.Deal
}

func (c crmStub) Accounts(context.Context) ([]ports.Account, error) { return c.accounts, nil }
func (c crmStub) Deals(context.Context) ([]ports.Deal, error)       { return c.deals, nil }

type dirStub struct{ byKey map[string]kernel.ID }

func (d dirStub) ProductIDByKey(_ context.Context, key string) (kernel.ID, error) {
	id, ok := d.byKey[key]
	if !ok {
		return kernel.NilID, kernel.NotFound("product", kernel.NilID)
	}
	return id, nil
}

func marketingScope() authz.Scope {
	return authz.New(authz.Params{Subject: "cmo", Roles: []authz.Role{authz.RoleMarketing},
		AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func devScope() authz.Scope {
	return authz.New(authz.Params{Subject: "dev", Roles: []authz.Role{authz.RoleDevLead},
		AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func fixture() (*marketing.Service, kernel.ID) {
	edr := kernel.NewID()
	crm := crmStub{
		accounts: []ports.Account{
			{ExternalID: "a1", Name: "Банк", Segment: "финансы"},
			{ExternalID: "a2", Name: "Завод", Segment: "промышленность"},
			{ExternalID: "a3", Name: "Оператор", Segment: "финансы"},
		},
		deals: []ports.Deal{
			{ExternalID: "d1", AccountID: "a1", ProductKey: "edr", Outcome: ports.DealWon, Reason: "функциональность",
				Amount: kernel.RUB(3_000_000_00), ClosedDate: kernel.DateOf(2026, time.March, 5),
				Products: []string{"edr", "vm"}, Features: []string{"ГОСТ-криптография", "агент Astra"}},
			{ExternalID: "d2", AccountID: "a2", ProductKey: "edr", Outcome: ports.DealLost, Reason: "нет сертификата",
				Amount: kernel.RUB(2_000_000_00), ClosedDate: kernel.DateOf(2026, time.April, 1)},
			{ExternalID: "d3", AccountID: "a3", ProductKey: "edr", Outcome: ports.DealLost, Reason: "нет сертификата",
				Amount: kernel.RUB(1_000_000_00), ClosedDate: kernel.DateOf(2026, time.May, 1)},
			{ExternalID: "d4", AccountID: "a1", ProductKey: "edr", Outcome: ports.DealWon, Reason: "цена",
				Amount: kernel.RUB(1_500_000_00), ClosedDate: kernel.DateOf(2026, time.June, 1),
				Products: []string{"edr"}, Features: []string{"ГОСТ-криптография"}},
			{ExternalID: "d5", AccountID: "a2", ProductKey: "vm", Outcome: ports.DealOpen,
				Amount: kernel.RUB(900_000_00)},
		},
	}
	return marketing.NewService(crm).WithProducts(dirStub{byKey: map[string]kernel.ID{"edr": edr}}), edr
}

// TestDA04_WinLossByReasonAndSegment: win/loss собирается по причинам и сегментам.
func TestDA04_WinLossByReasonAndSegment(t *testing.T) {
	svc, _ := fixture()
	rep, err := svc.WinLoss(context.Background(), marketingScope(), marketing.Filter{ProductKey: "edr"})
	if err != nil {
		t.Fatalf("отчёт: %v", err)
	}
	if rep.Won != 2 || rep.Lost != 2 {
		t.Fatalf("выиграно %d, проиграно %d", rep.Won, rep.Lost)
	}
	if rep.WonAmount != kernel.RUB(4_500_000_00) {
		t.Fatalf("сумма выигранных %s", rep.WonAmount)
	}
	if len(rep.ByReason) == 0 || rep.ByReason[0].Key != "нет сертификата" || rep.ByReason[0].Lost != 2 {
		t.Fatalf("причины: %+v", rep.ByReason)
	}
	bySegment := map[string]int{}
	for _, c := range rep.BySegment {
		bySegment[c.Key] = c.Lost
	}
	if bySegment["финансы"] != 1 || bySegment["промышленность"] != 1 {
		t.Fatalf("сегменты: %+v", rep.BySegment)
	}
}

// TestDA04_FeaturesInWonDealsAndAttachRate: фичи выигранных сделок и attach rate.
func TestDA04_FeaturesInWonDealsAndAttachRate(t *testing.T) {
	svc, _ := fixture()
	rep, err := svc.WinLoss(context.Background(), marketingScope(), marketing.Filter{ProductKey: "edr"})
	if err != nil {
		t.Fatalf("отчёт: %v", err)
	}
	if len(rep.Features) != 2 || rep.Features[0].Feature != "ГОСТ-криптография" || rep.Features[0].Deals != 2 {
		t.Fatalf("фичи выигранных сделок: %+v", rep.Features)
	}
	attach := map[string]string{}
	for _, a := range rep.Attach {
		attach[a.ProductKey] = a.Rate
	}
	if attach["edr"] != "100.00" || attach["vm"] != "50.00" {
		t.Fatalf("attach rate: %+v", rep.Attach)
	}
}

// TestDA04_PeriodFilter: отчёт ограничивается периодом закрытия сделок.
func TestDA04_PeriodFilter(t *testing.T) {
	svc, _ := fixture()
	rep, err := svc.WinLoss(context.Background(), marketingScope(), marketing.Filter{
		ProductKey: "edr", From: kernel.DateOf(2026, time.April, 1), To: kernel.DateOf(2026, time.May, 31)})
	if err != nil {
		t.Fatalf("отчёт: %v", err)
	}
	if rep.Won != 0 || rep.Lost != 2 {
		t.Fatalf("в периоде выиграно %d, проиграно %d", rep.Won, rep.Lost)
	}
}

// TestDA04_ForbiddenWithoutMarketingAccess: без права на маркетинговые данные отчёт недоступен.
func TestDA04_ForbiddenWithoutMarketingAccess(t *testing.T) {
	svc, _ := fixture()
	if _, err := svc.WinLoss(context.Background(), devScope(), marketing.Filter{ProductKey: "edr"}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("ожидался отказ, получено %v", err)
	}
	var zero authz.Scope
	if _, err := svc.WinLoss(context.Background(), zero, marketing.Filter{}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope: ожидался отказ, получено %v", err)
	}
}
