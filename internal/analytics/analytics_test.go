package analytics_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onixus/metis/internal/analytics"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

func cpoScope() authz.Scope {
	return authz.New(authz.Params{Subject: "cpo", Roles: []authz.Role{authz.RoleCPO},
		AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal, Finance: authz.FinanceFull})
}

func pmScope(subject string, product kernel.ID) authz.Scope {
	return authz.New(authz.Params{Subject: subject, Roles: []authz.Role{authz.RolePM},
		Products: map[kernel.ID]authz.Access{product: authz.AccessPrivate}, Audience: authz.AudienceInternal})
}

func presaleScope() authz.Scope {
	return authz.New(authz.Params{Subject: "presale", Roles: []authz.Role{authz.RolePresale},
		AllProducts: authz.AccessStrategic, Audience: authz.AudienceSalesSafe})
}

func service() *analytics.Service {
	return analytics.NewService(analytics.NewMemStore(),
		kernel.FixedClock{T: time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)})
}

func panels() []analytics.Panel {
	return []analytics.Panel{
		{Key: "pnl", Title: "P&L портфеля", Source: analytics.SourcePortfolioPnL, Kind: analytics.KindTable, Width: 12},
		{Key: "winloss", Title: "Win/loss", Source: analytics.SourceWinLoss, Kind: analytics.KindBar},
	}
}

// TestDA05_DashboardBuiltFromKnownSources: дашборд собирается из известных срезов платформы.
func TestDA05_DashboardBuiltFromKnownSources(t *testing.T) {
	svc := service()
	ctx := context.Background()
	d, err := svc.Save(ctx, cpoScope(), analytics.Input{Name: "Портфель", Panels: panels(), Shared: true})
	if err != nil {
		t.Fatalf("дашборд: %v", err)
	}
	if d.Panels[1].Width != 6 {
		t.Fatalf("ширина по умолчанию %d", d.Panels[1].Width)
	}
	got, err := svc.Get(ctx, cpoScope(), d.ID)
	if err != nil || got.Name != "Портфель" {
		t.Fatalf("чтение: %+v %v", got, err)
	}
	list, err := svc.List(ctx, cpoScope())
	if err != nil || len(list) != 1 {
		t.Fatalf("список: %+v %v", list, err)
	}
}

// TestDA05_UnknownSourceRejected: панель не может ссылаться на произвольный адрес.
func TestDA05_UnknownSourceRejected(t *testing.T) {
	svc := service()
	_, err := svc.Save(context.Background(), cpoScope(), analytics.Input{Name: "Свой срез",
		Panels: []analytics.Panel{{Key: "x", Title: "x", Source: "http://внутренний/секрет", Kind: analytics.KindTable}}})
	if !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("неизвестный срез принят: %v", err)
	}
}

// TestDA05_ForbiddenForPresaleAndForeignProduct: presale не собирает дашборды,
// а PM не собирает дашборд чужого продукта.
func TestDA05_ForbiddenForPresaleAndForeignProduct(t *testing.T) {
	svc := service()
	ctx := context.Background()
	if _, err := svc.Save(ctx, presaleScope(), analytics.Input{Name: "Внешний", Panels: panels()}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("presale собрал дашборд: %v", err)
	}
	edr, vm := kernel.NewID(), kernel.NewID()
	if _, err := svc.Save(ctx, pmScope("pm-edr", edr), analytics.Input{ProductID: vm, Name: "Чужой", Panels: panels()}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM собрал дашборд чужого продукта: %v", err)
	}
	if _, err := svc.Save(ctx, pmScope("pm-edr", edr), analytics.Input{Name: "Портфельный", Panels: panels()}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM собрал портфельный дашборд: %v", err)
	}
	var zero authz.Scope
	if _, err := svc.Save(ctx, zero, analytics.Input{Name: "x", Panels: panels()}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope: ожидался отказ, получено %v", err)
	}
}

// TestDA05_PrivateDashboardHiddenFromOthers: непубличный дашборд виден только автору.
func TestDA05_PrivateDashboardHiddenFromOthers(t *testing.T) {
	svc := service()
	ctx := context.Background()
	edr := kernel.NewID()
	own, err := svc.Save(ctx, pmScope("pm-edr", edr), analytics.Input{ProductID: edr, Name: "Мой", Panels: panels()})
	if err != nil {
		t.Fatalf("дашборд: %v", err)
	}
	other := pmScope("pm-other", edr)
	if _, err := svc.Get(ctx, other, own.ID); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("чужой непубличный дашборд виден: %v", err)
	}
	if err := svc.Delete(ctx, other, own.ID); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("чужой дашборд удалён: %v", err)
	}
	if err := svc.Delete(ctx, pmScope("pm-edr", edr), own.ID); err != nil {
		t.Fatalf("удаление автором: %v", err)
	}
}
