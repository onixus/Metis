package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/onixus/metis/internal/app"
	"github.com/onixus/metis/internal/seed"
	"github.com/onixus/metis/tests/e2e/client"
)

// confluenceMock воспроизводит маппинги deploy/compose/confluence-mock без WireMock.
type confluenceMock struct{ files string }

func (m *confluenceMock) serve(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	file := ""
	switch {
	case r.Method == http.MethodGet && p == "/rest/api/content":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[],"limit":2}`))
		return
	case r.Method == http.MethodPost && p == "/rest/api/content":
		file = "create_page.json"
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/label"):
		file = "labels_added.json"
	case r.Method == http.MethodGet && strings.Contains(p, "/property/"):
		// Свойства новой страницы отсутствуют: адаптер создаёт их POST.
		http.NotFound(w, r)
		return
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/property"):
		file = "property_created.json"
	case r.Method == http.MethodGet && p == "/rest/api/content/1001":
		file = "page_1001.json"
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

func newStage2Env(t *testing.T) *env {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cm := &confluenceMock{files: filepath.Join(root, "deploy/compose/confluence-mock/__files")}
	csrv := httptest.NewServer(http.HandlerFunc(cm.serve))
	t.Cleanup(csrv.Close)
	cfg := app.Config{
		Storage: "memory", AuthMode: "hmac", HMACSecret: secret, HMACIssuer: issuer,
		ConfluenceBaseURL: csrv.URL, ConfluenceToken: "e2e-only", ConfluenceSpace: "METIS",
		Seed: true, OTelExport: "none", Version: "e2e",
	}
	a, err := app.Build(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler)
	t.Cleanup(srv.Close)
	return &env{t: t, app: a, srv: srv}
}

func date(y int, m time.Month, d int) *openapi_types.Date {
	return &openapi_types.Date{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

func gateByKey(tr client.Track, key string) client.Gate {
	for _, g := range tr.Gates {
		if g.Key == key {
			return g
		}
	}
	return client.Gate{}
}

// passGate закрывает чек-лист гейта доказательствами с SHA-256 и проходит гейт (CM-03, CM-04).
func passGate(t *testing.T, c *client.ClientWithResponses, tr client.Track, key string) client.Track {
	t.Helper()
	ctx := context.Background()
	g := gateByKey(tr, key)
	for _, item := range g.Checklist {
		name := tr.Version + "/" + key + "/" + item.Key
		ev := must(c.AppendTrackEvidenceWithResponse(ctx, tr.Id, client.AppendTrackEvidenceJSONRequestBody{
			GateId: g.Id, Url: "https://evidence.example.test/" + name, Sha256: seed.SyntheticSHA256(name),
		}))
		if ev.StatusCode() != 201 {
			t.Fatalf("evidence %s: %s %s", name, ev.Status(), ev.Body)
		}
		chk := must(c.CheckGateItemWithResponse(ctx, tr.Id, g.Id, client.CheckGateItemJSONRequestBody{Key: item.Key, EvidenceId: ev.JSON201.Id}))
		if chk.StatusCode() != 200 {
			t.Fatalf("check %s: %s %s", name, chk.Status(), chk.Body)
		}
	}
	passed := must(c.PassGateWithResponse(ctx, tr.Id, g.Id))
	if passed.StatusCode() != 200 {
		t.Fatalf("pass %s: %s %s", key, passed.Status(), passed.Body)
	}
	return *passed.JSON200
}

// TestAcceptanceStage2 — критерий готовности этапа 2 (ТЗ раздел 6) в памяти, адаптер Confluence на моке.
func TestAcceptanceStage2(t *testing.T) {
	e := newStage2Env(t)
	ctx := context.Background()
	cpo := e.client("cpo", []string{"cpo"}, nil)
	comp := e.client("compliance", []string{"compliance"}, nil)
	adm := e.client("admin", []string{"admin"}, nil)

	byKey := map[string]client.Product{}
	for _, p := range *must(cpo.ListProductsWithResponse(ctx)).JSON200 {
		byKey[p.Key] = p
	}
	soar, server, mgmt, edr, vm := byKey["soar"], byKey["server"], byKey["mgmt"], byKey["edr"], byKey["vm"]

	// (а) Трек сертификации версии SOAR ведётся от гейтов SSDLC до сертификата.
	tracks := must(comp.ListTracksWithResponse(ctx, soar.Id))
	if tracks.StatusCode() != 200 || len(*tracks.JSON200) != 1 {
		t.Fatalf("треки SOAR: %s %s", tracks.Status(), tracks.Body)
	}
	tr := (*tracks.JSON200)[0]
	if tr.Version != seed.SOARCertifiedVersion || tr.Status != "active" {
		t.Fatalf("трек SOAR: %+v", tr)
	}
	rel := must(cpo.GetReleaseWithResponse(ctx, tr.ReleaseId)).JSON200
	if rel.Branch != "certified" {
		t.Fatalf("релиз SOAR должен быть в сертифицированной ветке: %+v", rel)
	}
	// RM-04: в сертифицированную ветку нельзя привязать элемент вида feature.
	if r := must(cpo.CreateRoadmapItemWithResponse(ctx, soar.Id, client.CreateRoadmapItemJSONRequestBody{
		Title: ptr("Новая фича"), Bucket: ptr(client.RoadmapItemInputBucket("next")), ReleaseId: &rel.Id,
		Audience: ptr(client.RoadmapItemInputAudience("internal")), Kind: ptr(client.RoadmapItemInputKind("feature")),
	})); r.StatusCode() != 409 {
		t.Fatalf("RM-04 feature в certified: %s %s", r.Status(), r.Body)
	}
	// Гейт «сертификат» нельзя пройти раньше SSDLC: чек-лист не закрыт, предшествующие гейты не пройдены.
	if r := must(comp.PassGateWithResponse(ctx, tr.Id, gateByKey(tr, "certificate").Id)); r.StatusCode() != 409 {
		t.Fatalf("сертификат раньше SSDLC: %s %s", r.Status(), r.Body)
	}
	// Релиз получает ready_for_certification только после гейтов SSDLC.
	if r := must(cpo.MarkReleaseReadyWithResponse(ctx, rel.Id)); r.StatusCode() != 409 {
		t.Fatalf("mark-ready до SSDLC: %s %s", r.Status(), r.Body)
	}
	rd := must(cpo.GetReleaseReadinessWithResponse(ctx, rel.Id)).JSON200
	if rd.Ready || len(rd.OpenItems) == 0 {
		t.Fatalf("readiness до SSDLC: %+v", rd)
	}
	tr = passGate(t, comp, tr, "ssdlc")
	ready := must(cpo.MarkReleaseReadyWithResponse(ctx, rel.Id))
	if ready.StatusCode() != 200 || ready.JSON200.Status != "ready_for_certification" {
		t.Fatalf("mark-ready после SSDLC: %s %s", ready.Status(), ready.Body)
	}
	// Ветка ФСТЭК идёт параллельно реестру: заявка → лаборатория → орган → сертификат.
	for _, key := range []string{"fstec_application", "fstec_lab", "fstec_body", "certificate"} {
		tr = passGate(t, comp, tr, key)
	}
	if tr.Status != "certified" || tr.BaselineId == nil {
		t.Fatalf("после гейта «сертификат» ожидался baseline: %+v", tr)
	}
	soarBaselines := must(cpo.ListBaselinesWithResponse(ctx, soar.Id)).JSON200
	if len(*soarBaselines) != 1 || (*soarBaselines)[0].Id != *tr.BaselineId || (*soarBaselines)[0].Version != seed.SOARCertifiedVersion {
		t.Fatalf("baseline SOAR: %+v", soarBaselines)
	}

	// (б) Фича агента платформы управления показывает затронутый baseline продукта Server.
	var agent client.Feature
	for _, f := range *must(cpo.ListFeaturesWithResponse(ctx, mgmt.Id)).JSON200 {
		if f.Name == "Агент управления v4" {
			agent = f
		}
	}
	if agent.Id == (openapi_types.UUID{}) {
		t.Fatal("нет фичи агента платформы управления")
	}
	impact := must(cpo.GetFeatureImpactWithResponse(ctx, agent.Id))
	if impact.StatusCode() != 200 || impact.JSON200.Class != "security_functions" {
		t.Fatalf("класс влияния агента: %s %s", impact.Status(), impact.Body)
	}
	affected := must(cpo.GetAffectedBaselinesWithResponse(ctx, agent.Id))
	if affected.StatusCode() != 200 {
		t.Fatalf("affected-baselines: %s %s", affected.Status(), affected.Body)
	}
	foundServer := false
	for _, ab := range *affected.JSON200 {
		if ab.Baseline.ProductId == server.Id && ab.Baseline.Version == seed.ServerCertifiedVersion {
			foundServer = true
			// Server сертифицировал процессы РБПО — упрощённое подтверждение; путь mgmt → server.
			if ab.Procedure != "simplified_confirmation" || len(ab.Path) != 2 || ab.Path[0] != mgmt.Id || ab.Path[1] != server.Id {
				t.Fatalf("затронутый baseline Server: %+v", ab)
			}
		}
	}
	if !foundServer {
		t.Fatalf("baseline Server не найден среди затронутых: %s", affected.Body)
	}
	// PR-05: стоимость фичи агента включает подтверждение изменений класса security_functions.
	cost := must(cpo.GetFeatureCostWithResponse(ctx, agent.Id))
	if cost.StatusCode() != 200 || cost.JSON200.ConfirmationCost.Amount <= 0 {
		t.Fatalf("стоимость подтверждения: %s %s", cost.Status(), cost.Body)
	}

	// (в) Решение создано, страница ADR запрошена → обработчик outbox создаёт страницу через адаптер → page_id заполнен.
	decs := must(cpo.ListDecisionsWithResponse(ctx, &client.ListDecisionsParams{ProductId: &soar.Id}))
	if decs.StatusCode() != 200 || len(*decs.JSON200) != 1 || (*decs.JSON200)[0].Links == nil || len(*(*decs.JSON200)[0].Links) != 2 {
		t.Fatalf("решения SOAR: %s %s", decs.Status(), decs.Body)
	}
	dec := (*decs.JSON200)[0]
	if r := must(cpo.RequestDecisionPageWithResponse(ctx, dec.Id, client.RequestDecisionPageJSONRequestBody{})); r.StatusCode() != 202 {
		t.Fatalf("request-page: %s %s", r.Status(), r.Body)
	}
	if _, err := e.app.Worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got := must(cpo.GetDecisionWithResponse(ctx, dec.Id)).JSON200
	if got.PageId == nil || *got.PageId != "2001" {
		t.Fatalf("page_id решения: %+v", got.PageId)
	}
	// Повторный запрос страницы отклонён: страница уже создана.
	if r := must(cpo.RequestDecisionPageWithResponse(ctx, dec.Id, client.RequestDecisionPageJSONRequestBody{})); r.StatusCode() != 409 {
		t.Fatalf("повторный request-page: %s", r.Status())
	}
	// Трассировка от фичи коннектора доходит до решения (DS-04).
	var connector client.Feature
	for _, f := range *must(cpo.ListFeaturesWithResponse(ctx, soar.Id)).JSON200 {
		if f.Name == "Коннектор EDR v2" {
			connector = f
		}
	}
	trace := must(cpo.GetTraceWithResponse(ctx, "feature", connector.Id))
	if trace.StatusCode() != 200 {
		t.Fatalf("trace: %s %s", trace.Status(), trace.Body)
	}
	hasDecision := false
	for _, n := range trace.JSON200.Nodes {
		hasDecision = hasDecision || (n.Kind == "decision" && n.Id == dec.Id)
	}
	if !hasDecision {
		t.Fatalf("решение не найдено в трассировке: %s", trace.Body)
	}

	// (г) Обязательство нарушено сдвигом даты фичи → алерт.
	if r := must(cpo.ListCommitmentAlertsWithResponse(ctx, soar.Id, &client.ListCommitmentAlertsParams{Open: ptr(true)})); len(*r.JSON200) != 0 {
		t.Fatalf("алерты до сдвига: %s", r.Body)
	}
	shift := must(cpo.ShiftFeatureDateWithResponse(ctx, connector.Id, client.ShiftFeatureDateJSONRequestBody{PlannedDate: *date(2027, 2, 1), Reason: "перенос интеграции на следующий квартал"}))
	if shift.StatusCode() != 200 {
		t.Fatalf("shift-date: %s %s", shift.Status(), shift.Body)
	}
	if _, err := e.app.Worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	alerts := must(cpo.ListCommitmentAlertsWithResponse(ctx, soar.Id, &client.ListCommitmentAlertsParams{Open: ptr(true)}))
	if alerts.StatusCode() != 200 || len(*alerts.JSON200) != 1 || (*alerts.JSON200)[0].Kind != "roadmap_shift" {
		t.Fatalf("алерты после сдвига: %s %s", alerts.Status(), alerts.Body)
	}
	alert := (*alerts.JSON200)[0]
	if alert.NewDate == nil || alert.NewDate.Format(time.DateOnly) != "2027-02-01" || alert.DueDate == nil || alert.DueDate.Format(time.DateOnly) != "2026-12-31" {
		t.Fatalf("алерт: %+v", alert)
	}
	if r := must(cpo.AcknowledgeCommitmentAlertWithResponse(ctx, alert.Id)); r.StatusCode() != 200 || !r.JSON200.Acknowledged {
		t.Fatalf("ack: %s %s", r.Status(), r.Body)
	}
	// CT-04: элемент roadmap на продление сертификата Server появляется за 18 месяцев до срока.
	renew := must(cpo.EnsureRenewalsWithResponse(ctx, client.EnsureRenewalsJSONRequestBody{Now: date(2027, 1, 15)}))
	if renew.StatusCode() != 200 || len(*renew.JSON200) != 1 || (*renew.JSON200)[0].RenewalItemId == nil {
		t.Fatalf("ensure-renewals: %s %s", renew.Status(), renew.Body)
	}

	// (д) PM VM не видит гипотезы EDR; presale видит матрицу совместимости релиза и не видит release notes.
	pmVM := e.client("pm-vm", []string{"pm"}, []string{"vm"})
	if r := must(pmVM.ListHypothesesWithResponse(ctx, edr.Id, nil)); r.StatusCode() != 403 && r.StatusCode() != 404 {
		t.Fatalf("PM VM → гипотезы EDR: %s", r.Status())
	}
	hyps := must(cpo.ListHypothesesWithResponse(ctx, edr.Id, nil)).JSON200
	if len(*hyps) != 1 {
		t.Fatalf("гипотезы EDR: %d", len(*hyps))
	}
	if r := must(pmVM.GetHypothesisWithResponse(ctx, (*hyps)[0].Id)); r.StatusCode() != 403 && r.StatusCode() != 404 {
		t.Fatalf("PM VM → гипотеза EDR: %s", r.Status())
	}
	_ = vm
	// Матрица совместимости: контракт EDR ↔ SOAR получает пару версий релиза SOAR 5.1 ↔ EDR 3.x.
	var edrSoar client.Contract
	for _, c := range *must(cpo.ListContractsWithResponse(ctx)).JSON200 {
		if c.Name == "EDR ↔ SOAR" {
			edrSoar = c
		}
	}
	pairs := append(*edrSoar.Compatibility, client.VersionPair{ProviderVersion: seed.SOARCertifiedVersion, ConsumerVersion: "3.x", Compatible: true})
	upd := must(cpo.UpdateContractWithResponse(ctx, edrSoar.Id, client.UpdateContractJSONRequestBody{
		Name: edrSoar.Name, ProviderProductId: edrSoar.ProviderProductId, ConsumerProductId: edrSoar.ConsumerProductId,
		ProviderFeatureIds: edrSoar.ProviderFeatureIds, ConsumerFeatureIds: edrSoar.ConsumerFeatureIds, InterfaceVersion: edrSoar.InterfaceVersion,
		Owner: edrSoar.Owner, Status: (*client.ContractInputStatus)(edrSoar.Status), Criticality: client.ContractInputCriticality(edrSoar.Criticality), Compatibility: &pairs,
	}))
	if upd.StatusCode() != 200 {
		t.Fatalf("contract update: %s %s", upd.Status(), upd.Body)
	}
	if r := must(cpo.SetReleaseNotesWithResponse(ctx, rel.Id, client.SetReleaseNotesJSONRequestBody{ReleaseNotes: "Внутренние заметки: исправления коннектора"})); r.StatusCode() != 200 {
		t.Fatalf("release notes: %s %s", r.Status(), r.Body)
	}
	presale := e.client("presale", []string{"presale"}, nil)
	pr := must(presale.GetReleaseWithResponse(ctx, rel.Id))
	if pr.StatusCode() != 200 || pr.JSON200.ReleaseNotes != nil || pr.JSON200.FeatureIds != nil {
		t.Fatalf("presale → релиз: %s %s", pr.Status(), pr.Body)
	}
	if pr.JSON200.CompatibilityMatrix == nil || len(*pr.JSON200.CompatibilityMatrix) != 1 || (*pr.JSON200.CompatibilityMatrix)[0].ConsumerVersion != "3.x" {
		t.Fatalf("presale → матрица совместимости: %s", pr.Body)
	}
	internal := must(cpo.GetReleaseWithResponse(ctx, rel.Id)).JSON200
	if internal.ReleaseNotes == nil || *internal.ReleaseNotes == "" {
		t.Fatalf("cpo → release notes: %+v", internal)
	}
	byRel := must(presale.GetRoadmapByReleaseWithResponse(ctx, soar.Id)).JSON200
	for _, g := range byRel.Releases {
		if g.Release.Id == rel.Id && (g.SalesSafeRelease == nil || g.Release.ReleaseNotes != nil) {
			t.Fatalf("presale → by-release: %+v", g)
		}
	}
	if r := must(presale.ListTracksWithResponse(ctx, soar.Id)); r.StatusCode() != 200 {
		// Статус compliance — часть стратегического среза, доступного presale.
		t.Fatalf("presale → треки: %s", r.Status())
	}
	// PM Desktop (другой портфель) не читает журнал доказательств трека SOAR: нет даже стратегического доступа.
	pmDesktop := e.client("pm-desktop", []string{"pm"}, []string{"desktop"})
	if r := must(pmDesktop.ListTrackEvidenceWithResponse(ctx, tr.Id)); r.StatusCode() != 403 {
		t.Fatalf("PM Desktop → журнал доказательств SOAR: %s", r.Status())
	}

	// (е) Проверка целостности журнала доказательств проходит; presale её не запускает.
	ver := must(adm.VerifyEvidenceLogWithResponse(ctx))
	if ver.StatusCode() != 200 || !ver.JSON200.Ok || ver.JSON200.Checked < 10 {
		t.Fatalf("evidence verify: %s %s", ver.Status(), ver.Body)
	}
	if r := must(presale.VerifyEvidenceLogWithResponse(ctx)); r.StatusCode() != 403 {
		t.Fatalf("presale → evidence verify: %s", r.Status())
	}
}
