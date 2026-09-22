package licensing_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/licensing"
)

func adminScope() authz.Scope {
	return authz.New(authz.Params{Subject: "admin", Roles: []authz.Role{authz.RoleAdmin},
		AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func cpoScope() authz.Scope {
	return authz.New(authz.Params{Subject: "cpo", Roles: []authz.Role{authz.RoleCPO},
		AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func license(notAfter kernel.Date) licensing.License {
	return licensing.License{ID: "lic-1", Customer: "Заказчик", Edition: "enterprise",
		IssuedAt:  kernel.DateOf(2026, time.January, 1),
		NotBefore: kernel.DateOf(2026, time.January, 1), NotAfter: notAfter,
		Limits: licensing.Limits{Products: 2, Users: 50}}
}

func setup(t *testing.T, now time.Time) (*licensing.Service, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("ключевая пара: %v", err)
	}
	v := licensing.NewVerifier(kernel.FixedClock{T: now}, pub)
	return licensing.NewService(v), priv
}

// TestAD06_ValidKeyInstalledOffline: подписанный ключ проверяется без обращения в сеть.
func TestAD06_ValidKeyInstalledOffline(t *testing.T) {
	svc, priv := setup(t, time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC))
	key, err := licensing.Sign(priv, license(kernel.DateOf(2027, time.January, 1)))
	if err != nil {
		t.Fatalf("выпуск ключа: %v", err)
	}
	st, err := svc.Install(context.Background(), adminScope(), key)
	if err != nil {
		t.Fatalf("установка: %v", err)
	}
	if !st.Valid || st.Expired {
		t.Fatalf("статус: %+v", st)
	}
	if st.License.Limits.Products != 2 {
		t.Fatalf("лимиты: %+v", st.License.Limits)
	}
	if st.DaysLeft != 105 {
		t.Fatalf("дней до истечения %d", st.DaysLeft)
	}
}

// TestAD06_TamperedKeyRejected: подмена полезной нагрузки ломает подпись.
func TestAD06_TamperedKeyRejected(t *testing.T) {
	svc, priv := setup(t, time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC))
	l := license(kernel.DateOf(2027, time.January, 1))
	key, err := licensing.Sign(priv, l)
	if err != nil {
		t.Fatalf("выпуск ключа: %v", err)
	}
	forged, err := licensing.Sign(priv, l)
	if err != nil {
		t.Fatalf("выпуск ключа: %v", err)
	}
	parts := strings.Split(forged, ".")
	// Подменяем тело, оставляя прежнюю подпись.
	bigger := license(kernel.DateOf(2030, time.January, 1))
	other, err := licensing.Sign(priv, bigger)
	if err != nil {
		t.Fatalf("выпуск ключа: %v", err)
	}
	tampered := parts[0] + "." + strings.Split(other, ".")[1] + "." + parts[2]
	if _, err := svc.Install(context.Background(), adminScope(), tampered); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("подделанный ключ принят: %v", err)
	}
	// Ключ, подписанный чужой парой, тоже отвергается.
	_, foreign, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("ключевая пара: %v", err)
	}
	foreignKey, err := licensing.Sign(foreign, l)
	if err != nil {
		t.Fatalf("выпуск ключа: %v", err)
	}
	if _, err := svc.Install(context.Background(), adminScope(), foreignKey); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("чужой ключ принят: %v", err)
	}
	if _, err := svc.Install(context.Background(), adminScope(), key); err != nil {
		t.Fatalf("настоящий ключ: %v", err)
	}
}

// TestAD06_ExpiredKeyRejected: после льготного периода запись под лимитами запрещена.
func TestAD06_ExpiredKeyRejected(t *testing.T) {
	ctx := context.Background()
	notAfter := kernel.DateOf(2026, time.August, 1)

	inGrace, priv := setup(t, time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC))
	key, err := licensing.Sign(priv, license(notAfter))
	if err != nil {
		t.Fatalf("выпуск ключа: %v", err)
	}
	st, err := inGrace.Install(ctx, adminScope(), key)
	if err != nil {
		t.Fatalf("установка: %v", err)
	}
	if !st.Expired || !st.InGrace || !st.Valid {
		t.Fatalf("в льготном периоде статус: %+v", st)
	}
	if err := inGrace.CheckLimit(ctx, "admin", licensing.ResourceProducts, 1); err != nil {
		t.Fatalf("в льготном периоде запись запрещена: %v", err)
	}

	after, priv2 := setup(t, time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC))
	key2, err := licensing.Sign(priv2, license(notAfter))
	if err != nil {
		t.Fatalf("выпуск ключа: %v", err)
	}
	st2, err := after.Install(ctx, adminScope(), key2)
	if err != nil {
		t.Fatalf("установка: %v", err)
	}
	if st2.Valid || !st2.Expired {
		t.Fatalf("после льготного периода статус: %+v", st2)
	}
	if err := after.CheckLimit(ctx, "admin", licensing.ResourceProducts, 0); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("истёкшая лицензия разрешила запись: %v", err)
	}
}

// TestAD06_LimitsEnforced: лимит по числу продуктов не превышается.
func TestAD06_LimitsEnforced(t *testing.T) {
	ctx := context.Background()
	svc, priv := setup(t, time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC))
	key, err := licensing.Sign(priv, license(kernel.DateOf(2027, time.January, 1)))
	if err != nil {
		t.Fatalf("выпуск ключа: %v", err)
	}
	if _, err := svc.Install(ctx, adminScope(), key); err != nil {
		t.Fatalf("установка: %v", err)
	}
	if err := svc.CheckLimit(ctx, "admin", licensing.ResourceProducts, 1); err != nil {
		t.Fatalf("в пределах лимита: %v", err)
	}
	if err := svc.CheckLimit(ctx, "admin", licensing.ResourceProducts, 2); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("лимит продуктов не сработал: %v", err)
	}
	if err := svc.CheckLimit(ctx, "admin", licensing.ResourceUsers, 49); err != nil {
		t.Fatalf("в пределах лимита пользователей: %v", err)
	}
	if err := svc.CheckLimit(ctx, "admin", licensing.ResourceUsers, 50); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("лимит пользователей не сработал: %v", err)
	}
}

// TestAD06_ForbiddenWithoutAdmin: ключом управляет только администратор.
func TestAD06_ForbiddenWithoutAdmin(t *testing.T) {
	ctx := context.Background()
	svc, priv := setup(t, time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC))
	key, err := licensing.Sign(priv, license(kernel.DateOf(2027, time.January, 1)))
	if err != nil {
		t.Fatalf("выпуск ключа: %v", err)
	}
	if _, err := svc.Install(ctx, cpoScope(), key); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("CPO установил лицензию: %v", err)
	}
	var zero authz.Scope
	if _, err := svc.Status(ctx, zero); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope: ожидался отказ, получено %v", err)
	}
}

// TestAD06_NotYetValidKey: ключ до даты начала действия не работает, допуск на часы — сутки.
func TestAD06_NotYetValidKey(t *testing.T) {
	svc, priv := setup(t, time.Date(2025, 12, 20, 10, 0, 0, 0, time.UTC))
	key, err := licensing.Sign(priv, license(kernel.DateOf(2027, time.January, 1)))
	if err != nil {
		t.Fatalf("выпуск ключа: %v", err)
	}
	st, err := svc.Install(context.Background(), adminScope(), key)
	if err != nil {
		t.Fatalf("установка: %v", err)
	}
	if st.Valid {
		t.Fatalf("ключ действует до даты начала: %+v", st)
	}
}
