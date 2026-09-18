// Package e2e — сценарий приёмки этапа 1 (ТЗ 7.7). Запускается в процессе: приложение в режиме
// памяти, аутентификация hmac, мок Jira на фикстурах compose-стенда. Тот же сценарий выполняется
// на compose-стенде против WireMock через METIS_E2E_BASE_URL (см. README).
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/onixus/metis/internal/app"
	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/tests/e2e/client"
)

const (
	secret = "e2e-only-secret-0123456789abcdef0123"
	issuer = "metis-e2e"
)

// jiraMock воспроизводит маппинги deploy/compose/jira-mock без WireMock.
type jiraMock struct {
	files   string
	shifted atomic.Bool
}

func (m *jiraMock) serve(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	file := ""
	switch {
	case r.Method == http.MethodGet && p == "/rest/api/2/issue/SOAR-42":
		file = "issue_soar42.json"
		if m.shifted.Load() {
			file = "issue_soar42_shifted.json"
		}
	case r.Method == http.MethodGet && p == "/rest/api/2/search":
		file = "search_epics.json"
		if strings.Contains(r.URL.Query().Get("jql"), "Epic Link") || strings.Contains(r.URL.Query().Get("jql"), "parent") {
			file = "epic_issues.json"
		}
	case r.Method == http.MethodGet && strings.HasSuffix(p, "/sprint"):
		file = "sprints.json"
	case r.Method == http.MethodGet && strings.Contains(p, "/sprint/101/issue"):
		file = "sprint_issues_101.json"
	case r.Method == http.MethodGet && strings.Contains(p, "/sprint/102/issue"):
		file = "sprint_issues_102.json"
	case r.Method == http.MethodGet && strings.HasSuffix(p, "/versions"):
		file = "versions.json"
	case r.Method == http.MethodPost && p == "/rest/api/2/issue":
		file = "create_epic.json"
	case r.Method == http.MethodPut:
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		http.NotFound(w, r)
		return
	}
	b, err := os.ReadFile(filepath.Join(m.files, file))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

type env struct {
	t    *testing.T
	app  *app.App
	srv  *httptest.Server
	jira *jiraMock
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	jm := &jiraMock{files: filepath.Join(root, "deploy/compose/jira-mock/__files")}
	jsrv := httptest.NewServer(http.HandlerFunc(jm.serve))
	t.Cleanup(jsrv.Close)
	cfg := app.Config{
		Storage: "memory", AuthMode: "hmac", HMACSecret: secret, HMACIssuer: issuer,
		JiraBaseURL: jsrv.URL, JiraToken: "e2e-only", CRMDir: filepath.Join(root, "fixtures/crm"), Seed: true, OTelExport: "none", Version: "e2e",
	}
	a, err := app.Build(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler)
	t.Cleanup(srv.Close)
	return &env{t: t, app: a, srv: srv, jira: jm}
}

func (e *env) client(subject string, roles, products []string) *client.ClientWithResponses {
	return e.clientFinance(subject, roles, products, "")
}

// clientFinance — клиент с заданным уровнем доступа к финансовым данным (NF-S02).
func (e *env) clientFinance(subject string, roles, products []string, finance string) *client.ClientWithResponses {
	e.t.Helper()
	tok, err := identityaccess.MintHS256([]byte(secret), issuer, subject, roles, products, finance, time.Hour, kernel.SystemClock{})
	if err != nil {
		e.t.Fatal(err)
	}
	c, err := client.NewClientWithResponses(e.srv.URL+"/api/v1", client.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+tok)
		return nil
	}))
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

// must распаковывает результат вызова; в тестах panic допустим (инвариант 9).
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestAcceptanceStage1(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	cpo := e.client("cpo", []string{"cpo"}, nil)

	// 1. Seed: портфель ИБ заведён — четыре продукта и три контракта.
	products := must(cpo.ListProductsWithResponse(ctx))
	if products.StatusCode() != 200 {
		t.Fatalf("products: %s %s", products.Status(), products.Body)
	}
	byKey := map[string]client.Product{}
	for _, p := range *products.JSON200 {
		byKey[p.Key] = p
	}
	for _, k := range []string{"deception", "vm", "edr", "soar"} {
		if _, ok := byKey[k]; !ok {
			t.Fatalf("нет продукта %s", k)
		}
	}
	contracts := must(cpo.ListContractsWithResponse(ctx))
	var edrSoar client.Contract
	secCount := 0
	for _, c := range *contracts.JSON200 {
		if c.ProviderProductId == byKey["soar"].Id {
			secCount++
		}
		if c.Name == "EDR ↔ SOAR" {
			edrSoar = c
		}
	}
	if secCount != 3 || edrSoar.Id == (openapi_types.UUID{}) {
		t.Fatalf("контракты портфеля ИБ: %d", secCount)
	}

	// 2. В контракте фичи «Response API v2» (EDR) и «Коннектор EDR v2» (SOAR), критичность «блокирует».
	if edrSoar.Criticality != "blocks" {
		t.Fatalf("критичность: %s", edrSoar.Criticality)
	}
	conn := (*edrSoar.ProviderFeatureIds)[0] // SOAR — хаб-поставщик
	api := (*edrSoar.ConsumerFeatureIds)[0]  // EDR — потребитель
	apiF := must(cpo.GetFeatureWithResponse(ctx, api)).JSON200
	connF := must(cpo.GetFeatureWithResponse(ctx, conn)).JSON200
	if apiF.Name != "Response API v2" || connF.Name != "Коннектор EDR v2" {
		t.Fatalf("фичи контракта: %s / %s", apiF.Name, connF.Name)
	}

	// 3. Сигнал со сделкой на 12 млн ₽ привязан к контракту: ценность обеих фич выросла на 12 млн × 1,0 (сразу, лимит 60 с).
	before := must(cpo.GetFeatureValueWithResponse(ctx, api)).JSON200.TotalValue.Amount
	sig := must(cpo.IngestSignalWithResponse(ctx, byKey["soar"].Id, client.IngestSignalJSONRequestBody{
		Source: "manual", Text: "Заказчик требует реагирование через SOAR на события EDR",
		AccountId: ptr("A-77"), DealId: ptr("D-1001"), DealAmount: &client.Money{Amount: 12_000_000_00, Currency: "RUB"}, BlocksDeal: ptr(true),
	}))
	if sig.StatusCode() != 201 {
		t.Fatalf("ingest: %s %s", sig.Status(), sig.Body)
	}
	linked := must(cpo.LinkSignalWithResponse(ctx, sig.JSON201.Id, client.LinkSignalJSONRequestBody{ContractId: &edrSoar.Id}))
	if linked.StatusCode() != 200 {
		t.Fatalf("link: %s %s", linked.Status(), linked.Body)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		v := must(cpo.GetFeatureValueWithResponse(ctx, api)).JSON200
		c := must(cpo.GetFeatureValueWithResponse(ctx, conn)).JSON200
		if v.TotalValue.Amount-before >= 12_000_000_00 && c.TotalValue.Amount >= 12_000_000_00 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rollup не пересчитан за 60 с: api=%d conn=%d", v.TotalValue.Amount, c.TotalValue.Amount)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// 4. Попытка создать цикл отклонена с путём цикла.
	cyc := must(cpo.CreateLinkWithResponse(ctx, client.CreateLinkJSONRequestBody{Type: "integration", Criticality: "blocks", FromFeatureId: &conn, ToFeatureId: &api}))
	if cyc.StatusCode() != 409 || cyc.ApplicationproblemJSON409 == nil || len(cyc.ApplicationproblemJSON409.Cycle) < 3 {
		t.Fatalf("цикл: %s %s", cyc.Status(), cyc.Body)
	}
	if cyc.ApplicationproblemJSON409.Cycle[0] != conn || cyc.ApplicationproblemJSON409.Cycle[1] != api {
		t.Fatalf("путь цикла: %v", cyc.ApplicationproblemJSON409.Cycle)
	}

	// 5. Мок Jira сдвигает эпик коннектора на 21 день: не позже 10 с срок контракта сдвинут,
	// фича EDR помечена как затронутая, причина записана в историю дат.
	item := must(cpo.CreateRoadmapItemWithResponse(ctx, byKey["soar"].Id, client.CreateRoadmapItemJSONRequestBody{
		Title: ptr("Коннектор EDR v2"), FeatureId: &conn, Bucket: ptr(client.RoadmapItemInputBucket("next")),
		StartDate: &openapi_types.Date{Time: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}, EndDate: &openapi_types.Date{Time: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)},
		Audience: ptr(client.RoadmapItemInputAudience("internal")),
	}))
	if item.StatusCode() != 201 {
		t.Fatalf("roadmap item: %s %s", item.Status(), item.Body)
	}
	if _, err := e.app.Delivery.MapFeature(ctx, e.app.ServiceScope, conn, "SOAR-42", "SOAR"); err != nil {
		t.Fatal(err)
	}
	e.jira.shifted.Store(true)
	body := must(os.ReadFile(filepath.Join(e.jira.files, "webhook_epic_shifted.json")))
	rec := httptest.NewRecorder()
	e.app.WebhookHandler()(rec, httptest.NewRequest(http.MethodPost, "/webhooks/jira", bytes.NewReader(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("webhook: %d %s", rec.Code, rec.Body)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		c := must(cpo.GetContractWithResponse(ctx, edrSoar.Id)).JSON200
		f := must(cpo.GetFeatureWithResponse(ctx, api)).JSON200
		if c.ReadyDate != nil && c.ReadyDate.Format(time.DateOnly) == "2026-12-22" && f.Affected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("сдвиг не распространён за 10 с: ready=%v affected=%v", c.ReadyDate, f.Affected)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := e.app.Worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	hist := must(cpo.GetRoadmapItemHistoryWithResponse(ctx, item.JSON201.Id))
	if hist.StatusCode() != 200 || len(*hist.JSON200) == 0 || !strings.Contains((*hist.JSON200)[0].Reason, "SOAR-42") {
		t.Fatalf("история дат: %s %s", hist.Status(), hist.Body)
	}

	// 6. PM VM не видит приватный контур EDR; PM SOAR видит стратегический срез EDR, но не сырые сигналы.
	pmVM := e.client("pm-vm", []string{"pm"}, []string{"vm"})
	if r := must(pmVM.ListFeaturesWithResponse(ctx, byKey["edr"].Id)); r.StatusCode() != 403 {
		t.Fatalf("PM VM → фичи EDR: %s", r.Status())
	}
	if r := must(pmVM.GetStrategicSliceWithResponse(ctx, byKey["edr"].Id)); r.StatusCode() != 403 {
		t.Fatalf("PM VM → срез EDR: %s", r.Status())
	}
	pmSOAR := e.client("pm-soar", []string{"pm"}, []string{"soar"})
	slice := must(pmSOAR.GetStrategicSliceWithResponse(ctx, byKey["edr"].Id))
	if slice.StatusCode() != 200 || len(slice.JSON200.Features) == 0 {
		t.Fatalf("PM SOAR → срез EDR: %s %s", slice.Status(), slice.Body)
	}
	if r := must(pmSOAR.ListSignalsWithResponse(ctx, byKey["edr"].Id, nil)); r.StatusCode() != 403 {
		t.Fatalf("PM SOAR → сигналы EDR: %s", r.Status())
	}
	if r := must(pmSOAR.ListFeaturesWithResponse(ctx, byKey["edr"].Id)); r.StatusCode() != 403 {
		t.Fatalf("PM SOAR → бэклог EDR: %s", r.Status())
	}

	// 7. Sales-safe срез roadmap не отдаёт внутренние элементы при прямом запросе к API.
	presale := e.client("presale", []string{"presale"}, nil)
	tl := must(presale.GetRoadmapTimelineWithResponse(ctx, byKey["soar"].Id))
	if tl.StatusCode() != 200 || tl.JSON200.Audience != "sales_safe" || tl.JSON200.Items != nil {
		t.Fatalf("presale timeline: %s %s", tl.Status(), tl.Body)
	}
	if tl.JSON200.SalesSafe != nil && len(*tl.JSON200.SalesSafe) != 0 {
		t.Fatalf("внутренний элемент утёк в sales-safe: %s", tl.Body)
	}
	if r := must(presale.ListRoadmapItemsWithResponse(ctx, byKey["soar"].Id)); r.StatusCode() != 403 {
		t.Fatalf("presale → внутренние элементы: %s", r.Status())
	}

	// 8. Проверка целостности аудита проходит; после ручной подмены записи указывает на неё.
	// Вторая запись — сдвиг даты через API с причиной (RM-03).
	shift := must(cpo.ShiftFeatureDateWithResponse(ctx, api, client.ShiftFeatureDateJSONRequestBody{
		PlannedDate: openapi_types.Date{Time: time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC)}, Reason: "подтверждён перенос после сдвига коннектора",
	}))
	if shift.StatusCode() != 200 {
		t.Fatalf("shift-date: %s %s", shift.Status(), shift.Body)
	}
	adm := e.client("admin", []string{"admin"}, nil)
	ok := must(adm.VerifyAuditWithResponse(ctx))
	if ok.StatusCode() != 200 || !ok.JSON200.Ok || ok.JSON200.Checked < 2 {
		t.Fatalf("audit verify: %s %s", ok.Status(), ok.Body)
	}
	mem, isMem := e.app.AuditStore.(*audit.MemStore)
	if !isMem {
		t.Fatal("ожидалось хранилище аудита в памяти")
	}
	mem.Tamper(2, func(r *audit.Record) { r.Actor = "intruder" })
	broken := must(adm.VerifyAuditWithResponse(ctx))
	if broken.JSON200.Ok || broken.JSON200.BrokenSeq == nil || *broken.JSON200.BrokenSeq != 2 {
		t.Fatalf("audit verify после подмены: %s", broken.Body)
	}
	// Без токена — 401, presale не читает аудит — 403.
	resp := must(http.Get(e.srv.URL + "/api/v1/products"))
	if resp.StatusCode != 401 {
		t.Fatalf("без токена: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
	if r := must(presale.VerifyAuditWithResponse(ctx)); r.StatusCode() != 403 {
		t.Fatalf("presale → аудит: %s", r.Status())
	}
	var problem map[string]any
	_ = json.Unmarshal(cyc.Body, &problem)
	if problem["type"] != "urn:metis:problem:feature-cycle" {
		t.Fatalf("problem+json: %v", problem)
	}
}

func ptr[T any](v T) *T { return &v }
