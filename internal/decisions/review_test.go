package decisions_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/decisions"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// metricStub — источник фактических значений показателей экономики (порт DA-06).
type metricStub struct{ value decimal.Decimal }

func (m metricStub) MetricValue(_ context.Context, _ authz.Scope, _ string, _ kernel.ID, _ string) (decimal.Decimal, error) {
	return m.value, nil
}

func dec(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	v, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("число %q: %v", s, err)
	}
	return v
}

// reviewFixture заводит принятое решение с измеримым эффектом и наступившей датой ревизии.
func reviewFixture(t *testing.T, expected, actual string) (*decisions.Service, decisions.DecisionRecord, authz.Scope, context.Context) {
	t.Helper()
	clock := kernel.FixedClock{T: time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)}
	svc := decisions.NewService(decisions.NewMemStore(), &memPub{}, clock).WithMetrics(metricStub{value: dec(t, actual)})
	sc := cpoScope()
	ctx := context.Background()
	product := kernel.NewID()
	rec, err := svc.Create(ctx, sc, decisions.Input{
		ProductID: product, Title: "Вложиться в сертификацию 4.0",
		Context:        "Сделки уходят из-за отсутствия сертификата",
		Options:        []decisions.Option{{Key: "certify", Title: "Сертифицировать"}, {Key: "wait", Title: "Отложить"}},
		ChosenKey:      "certify",
		ExpectedEffect: "выручка с сертификатом вырастет",
		Effect:         decisions.MeasurableEffect{MetricKey: "certified_revenue", Value: dec(t, expected), Period: "2026-09"},
		ReviewDate:     kernel.DateOf(2026, time.September, 1),
	})
	if err != nil {
		t.Fatalf("решение: %v", err)
	}
	accepted, err := svc.Accept(ctx, sc, rec.ID)
	if err != nil {
		t.Fatalf("принятие решения: %v", err)
	}
	return svc, accepted, sc, ctx
}

// TestDA06_ReviewComparesExpectedToFact: ревизия сравнивает ожидаемый эффект с фактом на дату ревизии.
func TestDA06_ReviewComparesExpectedToFact(t *testing.T) {
	cases := []struct {
		name     string
		expected string
		actual   string
		verdict  decisions.Verdict
	}{
		{"факт выше ожидания", "5000000", "6000000", decisions.VerdictConfirmed},
		{"факт вдвое ниже", "5000000", "2600000", decisions.VerdictPartial},
		{"эффекта нет", "5000000", "100000", decisions.VerdictMissed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, rec, sc, ctx := reviewFixture(t, c.expected, c.actual)
			reviewed, err := svc.ReviewDecision(ctx, sc, rec.ID, "плановая ревизия")
			if err != nil {
				t.Fatalf("ревизия: %v", err)
			}
			if reviewed.Review == nil {
				t.Fatal("ревизия не записана")
			}
			if reviewed.Review.Verdict != c.verdict {
				t.Fatalf("итог %q, ожидался %q (ожидание %s, факт %s)",
					reviewed.Review.Verdict, c.verdict, reviewed.Review.Expected, reviewed.Review.Actual)
			}
			if !reviewed.Review.Delta.Equal(reviewed.Review.Actual.Sub(reviewed.Review.Expected)) {
				t.Fatalf("отклонение посчитано неверно: %s", reviewed.Review.Delta)
			}
		})
	}
}

// TestDA06_DueForReviewListsPendingDecisions: решения с наступившей датой ревизии попадают в список
// и уходят из него после ревизии.
func TestDA06_DueForReviewListsPendingDecisions(t *testing.T) {
	svc, rec, sc, ctx := reviewFixture(t, "5000000", "6000000")
	due, err := svc.DueForReview(ctx, sc, kernel.NilID)
	if err != nil {
		t.Fatalf("список: %v", err)
	}
	if len(due) != 1 || due[0].ID != rec.ID {
		t.Fatalf("к ревизии: %+v", due)
	}
	if _, err := svc.ReviewDecision(ctx, sc, rec.ID, ""); err != nil {
		t.Fatalf("ревизия: %v", err)
	}
	due, err = svc.DueForReview(ctx, sc, kernel.NilID)
	if err != nil {
		t.Fatalf("список: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("после ревизии в списке %d решений", len(due))
	}
}

// TestDA06_ReviewRequiresMeasurableEffect: без измеримого эффекта сравнивать нечего.
func TestDA06_ReviewRequiresMeasurableEffect(t *testing.T) {
	clock := kernel.FixedClock{T: time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)}
	svc := decisions.NewService(decisions.NewMemStore(), &memPub{}, clock).WithMetrics(metricStub{value: decimal.Zero})
	ctx, sc := context.Background(), cpoScope()
	rec, err := svc.Create(ctx, sc, decisions.Input{
		ProductID: kernel.NewID(), Title: "Решение без метрики", Context: "контекст",
		Options: []decisions.Option{{Key: "a", Title: "A"}}, ChosenKey: "a",
		ReviewDate: kernel.DateOf(2026, time.September, 1),
	})
	if err != nil {
		t.Fatalf("решение: %v", err)
	}
	if _, err := svc.ReviewDecision(ctx, sc, rec.ID, ""); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("ожидалась ошибка валидации, получено %v", err)
	}
}

// TestDA06_ForbiddenWithoutWriteAccess: ревизию проводит тот, кто вправе менять решение.
func TestDA06_ForbiddenWithoutWriteAccess(t *testing.T) {
	svc, rec, _, ctx := reviewFixture(t, "5000000", "6000000")
	var zero authz.Scope
	if _, err := svc.ReviewDecision(ctx, zero, rec.ID, ""); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope: ожидался отказ, получено %v", err)
	}
	presale := authz.New(authz.Params{Subject: "presale", Roles: []authz.Role{authz.RolePresale},
		AllProducts: authz.AccessStrategic, Audience: authz.AudienceSalesSafe})
	if _, err := svc.ReviewDecision(ctx, presale, rec.ID, ""); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("presale провёл ревизию: %v", err)
	}
}
