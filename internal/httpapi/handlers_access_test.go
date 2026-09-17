package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onixus/metis/internal/commitments"
	"github.com/onixus/metis/internal/compliance"
	"github.com/onixus/metis/internal/decisions"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/roadmap"
)

// nopPub — публикатор-заглушка: события в тестах доступа не проверяются.
type nopPub struct{}

func (nopPub) Publish(context.Context, ...kernel.Event) error { return nil }

// withScope подменяет аутентификацию: кладёт готовый Scope в контекст запроса.
func withScope(sc authz.Scope) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(authz.WithScope(r.Context(), sc)))
		})
	}
}

func cpoScope() authz.Scope {
	return authz.New(authz.Params{Subject: "cpo", Roles: []authz.Role{authz.RoleCPO}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func pmScope(subject string, product kernel.ID) authz.Scope {
	return authz.New(authz.Params{Subject: subject, Roles: []authz.Role{authz.RolePM},
		Products: map[kernel.ID]authz.Access{product: authz.AccessPrivate}, Audience: authz.AudienceInternal})
}

// TestDA01_RequestPageUnavailableWithoutKnowledgeAdapter — без настроенного адаптера базы знаний
// (пустое KnowledgeSpace) обработчик публикации страницы не зарегистрирован: событие ушло бы в
// outbox и было бы удалено воркером без обработчиков. Поэтому 503 возвращается и при непустом
// space_key в теле запроса (дефект 7).
func TestDA01_RequestPageUnavailableWithoutKnowledgeAdapter(t *testing.T) {
	product := kernel.NewID()
	svc := decisions.NewService(decisions.NewMemStore(), nopPub{}, kernel.SystemClock{})
	rec, err := svc.Create(context.Background(), cpoScope(), decisions.Input{
		ProductID: product, Title: "Коннектор", Context: "нужен", ChosenKey: "A",
		Options: []decisions.Option{{Key: "A", Title: "Свой"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(Deps{Decisions: svc, KnowledgeSpace: "", Instrument: withScope(cpoScope())})
	h := srv.Handler()

	for name, body := range map[string]string{
		"с пространством в теле": `{"space_key":"ADR"}`,
		"пустое тело":            `{}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/decisions/"+rec.ID.String()+"/request-page", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: ожидался 503, получено %d: %s", name, w.Code, w.Body.String())
		}
	}
}

// TestCM05_ReleaseReadinessChecksAccessBeforeTrack — готовность релиза отвечает 403 субъекту без
// доступа к продукту релиза, даже если у релиза нет трека сертификации: иначе существующий чужой
// релиз без трека (200) отличался бы от релиза с треком (403) (дефект 15).
func TestCM05_ReleaseReadinessChecksAccessBeforeTrack(t *testing.T) {
	ctx := context.Background()
	product, foreign := kernel.NewID(), kernel.NewID()
	rm := roadmap.NewService(roadmap.NewMemStore(), nopPub{}, kernel.SystemClock{})
	rel, err := rm.CreateRelease(ctx, cpoScope(), product, roadmap.ReleaseInput{Name: "EDR 3.0", Version: "3.0"})
	if err != nil {
		t.Fatal(err)
	}
	cmpl := compliance.NewService(compliance.NewMemStore(), nil, nil, nopPub{}, kernel.SystemClock{})

	get := func(sc authz.Scope, id kernel.ID) int {
		srv := NewServer(Deps{Roadmap: rm, Compliance: cmpl, Commitments: commitments.NewService(commitments.NewMemStore(), nopPub{}, kernel.SystemClock{}), Instrument: withScope(sc)})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/releases/"+id.String()+"/readiness", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		return w.Code
	}

	if code := get(pmScope("pm-foreign", foreign), rel.ID); code != http.StatusForbidden {
		t.Fatalf("субъект без доступа к продукту релиза: ожидался 403, получено %d", code)
	}
	if code := get(authz.Scope{}, rel.ID); code != http.StatusForbidden {
		t.Fatalf("нулевой Scope: ожидался 403, получено %d", code)
	}
	// Владелец продукта получает ответ «не готов»: трека у релиза нет.
	if code := get(cpoScope(), rel.ID); code != http.StatusOK {
		t.Fatalf("владелец продукта: ожидался 200, получено %d", code)
	}
	// Несуществующий релиз — 404 для того, у кого есть доступ.
	if code := get(cpoScope(), kernel.NewID()); code != http.StatusNotFound {
		t.Fatalf("несуществующий релиз: ожидался 404, получено %d", code)
	}
}
