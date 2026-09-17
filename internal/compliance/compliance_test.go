package compliance_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/compliance"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	pg "github.com/onixus/metis/internal/portfoliograph"
)

type memPub struct{ events []kernel.Event }

func (m *memPub) Publish(_ context.Context, evs ...kernel.Event) error {
	m.events = append(m.events, evs...)
	return nil
}

func (m *memPub) count(typ string) int {
	n := 0
	for _, e := range m.events {
		if e.Type == typ {
			n++
		}
	}
	return n
}

const sha = "ab" + "cd" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789ab" // 64 hex

type fixture struct {
	t        *testing.T
	ctx      context.Context
	graph    *pg.Service
	svc      *compliance.Service
	evidence *compliance.EvidenceMemStore
	pub      *memPub
	cpo, cmp authz.Scope
	// server — дистрибутив; agent — компонент, входящий в поставку server; sdk — общий компонент.
	server, agent, sdk kernel.ID
	agentFeature       kernel.ID
	sdkFeature         kernel.ID
	serverFeature      kernel.ID
}

func cpoScope() authz.Scope {
	return authz.New(authz.Params{Subject: "cpo", Roles: []authz.Role{authz.RoleCPO}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func complianceScope() authz.Scope {
	return authz.New(authz.Params{Subject: "compliance-1", Roles: []authz.Role{authz.RoleCompliance}, AllProducts: authz.AccessPrivate, Audience: authz.AudienceInternal})
}

func pmScope(private ...kernel.ID) authz.Scope {
	products := map[kernel.ID]authz.Access{}
	for _, id := range private {
		products[id] = authz.AccessPrivate
	}
	return authz.New(authz.Params{Subject: "pm-1", Roles: []authz.Role{authz.RolePM}, Products: products, Audience: authz.AudienceInternal})
}

func presaleScope() authz.Scope {
	return authz.New(authz.Params{Subject: "presale-1", Roles: []authz.Role{authz.RolePresale}, AllProducts: authz.AccessStrategic})
}

func adminScope() authz.Scope {
	return authz.New(authz.Params{Subject: "admin", Roles: []authz.Role{authz.RoleAdmin}, AllProducts: authz.AccessPrivate})
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pub := &memPub{}
	clock := kernel.FixedClock{T: time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)}
	graph := pg.NewService(pg.NewMemStore(), pub, clock)
	f := &fixture{t: t, ctx: context.Background(), graph: graph, pub: pub, cpo: cpoScope(), cmp: complianceScope()}
	f.evidence = compliance.NewEvidenceMemStore()
	f.svc = compliance.NewService(compliance.NewMemStore(), f.evidence, graph, pub, clock)
	for _, tpl := range compliance.DefaultTemplates() {
		if _, err := f.svc.SaveTemplate(f.ctx, adminScope(), tpl); err != nil {
			t.Fatalf("seed template: %v", err)
		}
	}
	f.server = f.product("server", "Server", false)
	f.agent = f.product("agent", "Agent платформы управления", true)
	f.sdk = f.product("sdk", "SDK", false)
	// server (From) зависит от agent (To): агент входит в поставку сервера.
	f.link(pg.LinkBundled, f.server, f.agent)
	f.link(pg.LinkSharedComponent, f.server, f.sdk)
	f.agentFeature = f.feature(f.agent, "Новый протокол агента")
	f.sdkFeature = f.feature(f.sdk, "Новый API SDK")
	f.serverFeature = f.feature(f.server, "Отчёты")
	return f
}

func (f *fixture) product(key, name string, certified bool) kernel.ID {
	f.t.Helper()
	p, err := f.graph.CreateProduct(f.ctx, f.cpo, pg.ProductInput{Key: key, Name: name, Type: pg.ProductTypeSecurity, Owner: "pm-" + key, SSDLCCertified: certified})
	if err != nil {
		f.t.Fatalf("create product %s: %v", key, err)
	}
	return p.ID
}

func (f *fixture) link(typ pg.LinkType, from, to kernel.ID) {
	f.t.Helper()
	if _, err := f.graph.CreateLink(f.ctx, f.cpo, pg.LinkInput{Type: typ, FromProductID: from, ToProductID: to, Criticality: pg.CritBlocks}); err != nil {
		f.t.Fatalf("create link: %v", err)
	}
}

func (f *fixture) feature(productID kernel.ID, name string) kernel.ID {
	f.t.Helper()
	feat, err := f.graph.CreateFeature(f.ctx, f.cpo, productID, pg.FeatureInput{Name: name})
	if err != nil {
		f.t.Fatalf("create feature: %v", err)
	}
	return feat.ID
}

func (f *fixture) startTrack(productID kernel.ID) compliance.Track {
	f.t.Helper()
	tr, err := f.svc.StartTrack(f.ctx, f.cmp, compliance.TrackInput{ProductID: productID, ReleaseID: kernel.NewID(), Version: "3.1.0"})
	if err != nil {
		f.t.Fatalf("start track: %v", err)
	}
	return tr
}

func gateByKey(t *testing.T, tr compliance.Track, key string) compliance.Gate {
	t.Helper()
	for _, g := range tr.Gates {
		if g.Key == key {
			return g
		}
	}
	t.Fatalf("гейт %s не найден", key)
	return compliance.Gate{}
}

// closeChecklist добавляет доказательство на каждый пункт чек-листа гейта и закрывает его.
func (f *fixture) closeChecklist(tr compliance.Track, key string) compliance.Track {
	f.t.Helper()
	g := gateByKey(f.t, tr, key)
	for _, it := range g.Checklist {
		ev, err := f.svc.AppendEvidence(f.ctx, f.cmp, compliance.EvidenceInput{TrackID: tr.ID, GateID: g.ID, URL: "https://ci.example.test/" + it.Key, SHA256: sha})
		if err != nil {
			f.t.Fatalf("append evidence: %v", err)
		}
		tr, err = f.svc.CheckItem(f.ctx, f.cmp, tr.ID, g.ID, it.Key, ev.ID)
		if err != nil {
			f.t.Fatalf("check item %s/%s: %v", key, it.Key, err)
		}
	}
	return tr
}

func (f *fixture) pass(tr compliance.Track, key string) compliance.Track {
	f.t.Helper()
	tr = f.closeChecklist(tr, key)
	out, err := f.svc.PassGate(f.ctx, f.cmp, tr.ID, gateByKey(f.t, tr, key).ID)
	if err != nil {
		f.t.Fatalf("pass gate %s: %v", key, err)
	}
	return out
}

// certify проходит все гейты до сертификата.
func (f *fixture) certify(tr compliance.Track) compliance.Track {
	f.t.Helper()
	for _, key := range []string{"ssdlc", "fstec_application", "fstec_lab", "fstec_body", compliance.GateKeyCertificate} {
		tr = f.pass(tr, key)
	}
	return tr
}

// ---------- CM-01 ----------

func TestCM01_RequirementSetVersioning(t *testing.T) {
	f := newFixture(t)
	in := compliance.RequirementSetInput{Code: "reestr", ProductType: pg.ProductTypeSecurity, Items: []compliance.RequirementItem{{Key: "r1", Text: "Требование 1"}}}
	v1, err := f.svc.CreateRequirementSet(f.ctx, f.cmp, in)
	if err != nil {
		t.Fatalf("create v1: %v", err)
	}
	if v1.Code != "REESTR" || v1.Version != 1 || v1.Status != compliance.RequirementSetDraft {
		t.Fatalf("v1: %+v", v1)
	}
	if _, err := f.svc.SetRequirementSetStatus(f.ctx, f.cmp, v1.ID, compliance.RequirementSetPublished); err != nil {
		t.Fatalf("publish v1: %v", err)
	}
	in.Items = append(in.Items, compliance.RequirementItem{Key: "r2", Text: "Требование 2"})
	v2, err := f.svc.CreateRequirementSet(f.ctx, f.cmp, in)
	if err != nil {
		t.Fatalf("create v2: %v", err)
	}
	if v2.Version != 2 || v2.ID == v1.ID {
		t.Fatalf("v2: %+v", v2)
	}
	got, err := f.svc.RequirementSet(f.ctx, presaleScope(), v1.ID)
	if err != nil || len(got.Items) != 1 || got.Status != compliance.RequirementSetPublished {
		t.Fatalf("v1 изменился: %+v, %v", got, err)
	}
	// Привязка к типу продукта: другой тип под тем же кодом — конфликт.
	in.ProductType = pg.ProductTypePlatform
	if _, err := f.svc.CreateRequirementSet(f.ctx, f.cmp, in); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("ожидался конфликт типа продукта, получено %v", err)
	}
	// Переход draft → retired запрещён.
	if _, err := f.svc.SetRequirementSetStatus(f.ctx, f.cmp, v2.ID, compliance.RequirementSetRetired); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("ожидался конфликт перехода, получено %v", err)
	}
	// Каталог ведут admin и compliance; PM — нет.
	if _, err := f.svc.CreateRequirementSet(f.ctx, pmScope(f.server), in); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM не должен вести каталог: %v", err)
	}
	all, err := f.svc.RequirementSets(f.ctx, f.cmp, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("каталог: %d, %v", len(all), err)
	}
}

// ---------- CM-02 ----------

func TestCM02_DefaultTemplatesAndValidation(t *testing.T) {
	f := newFixture(t)
	tpls := compliance.DefaultTemplates()
	if len(tpls) != 4 {
		t.Fatalf("шаблонов по умолчанию: %d", len(tpls))
	}
	keys := []string{}
	for _, g := range tpls[0].Gates {
		keys = append(keys, g.Key)
	}
	want := "ssdlc,registry,fstec_application,fstec_lab,fstec_body,certificate,support"
	if strings.Join(keys, ",") != want {
		t.Fatalf("порядок гейтов: %s", strings.Join(keys, ","))
	}
	if gateByKeyTpl(tpls[0], "registry").ParallelGroup == gateByKeyTpl(tpls[0], "fstec_lab").ParallelGroup {
		t.Fatal("реестр и ФСТЭК должны быть в разных параллельных ветках")
	}
	saved, err := f.svc.Templates(f.ctx, presaleScope(), pg.ProductTypeSecurity)
	if err != nil || len(saved) != 1 {
		t.Fatalf("шаблоны security: %d, %v", len(saved), err)
	}
	bad := tpls[0]
	bad.Gates = append([]compliance.GateTemplate(nil), bad.Gates...)
	bad.Gates[1].Key = bad.Gates[0].Key
	if _, err := f.svc.SaveTemplate(f.ctx, f.cmp, bad); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("повтор ключа должен отклоняться: %v", err)
	}
	if _, err := f.svc.SaveTemplate(f.ctx, pmScope(f.server), tpls[1]); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM не должен вести шаблоны: %v", err)
	}
}

func gateByKeyTpl(t compliance.TrackTemplate, key string) compliance.GateTemplate {
	for _, g := range t.Gates {
		if g.Key == key {
			return g
		}
	}
	return compliance.GateTemplate{}
}

// ---------- CM-03 ----------

func TestCM03_TrackGatesOrderAndParallel(t *testing.T) {
	f := newFixture(t)
	tr := f.startTrack(f.server)
	if tr.Status != compliance.TrackActive || len(tr.Gates) != 7 || f.pub.count(compliance.EventTrackStarted) != 1 {
		t.Fatalf("трек: %+v", tr)
	}
	// Второй трек на тот же релиз — конфликт.
	if _, err := f.svc.StartTrack(f.ctx, f.cmp, compliance.TrackInput{ProductID: f.server, ReleaseID: tr.ReleaseID, Version: "x"}); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("ожидался конфликт: %v", err)
	}
	// Гейт с незакрытым чек-листом не проходит.
	if _, err := f.svc.PassGate(f.ctx, f.cmp, tr.ID, gateByKey(t, tr, "ssdlc").ID); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("незакрытый чек-лист: %v", err)
	}
	// Заявка ФСТЭК не проходит раньше SSDLC (гейт без группы блокирует всех).
	tr = f.closeChecklist(tr, "fstec_application")
	if _, err := f.svc.PassGate(f.ctx, f.cmp, tr.ID, gateByKey(t, tr, "fstec_application").ID); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("заявка до SSDLC: %v", err)
	}
	tr = f.pass(tr, "ssdlc")
	// Заявка ФСТЭК проходит без реестра — параллельная ветка.
	tr, err := f.svc.PassGate(f.ctx, f.cmp, tr.ID, gateByKey(t, tr, "fstec_application").ID)
	if err != nil {
		t.Fatalf("заявка после SSDLC: %v", err)
	}
	// Внутри ветки порядок соблюдается: орган по сертификации раньше лаборатории — нельзя.
	tr = f.closeChecklist(tr, "fstec_body")
	if _, err := f.svc.PassGate(f.ctx, f.cmp, tr.ID, gateByKey(t, tr, "fstec_body").ID); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("орган раньше лаборатории: %v", err)
	}
	// Владелец, срок, затраты.
	tr, err = f.svc.UpdateGate(f.ctx, f.cmp, tr.ID, gateByKey(t, tr, "fstec_lab").ID, compliance.GateUpdate{Owner: "lab-owner", DueDate: kernel.DateOf(2026, 12, 1), Cost: kernel.RUB(1_000_000_00)})
	if err != nil {
		t.Fatalf("update gate: %v", err)
	}
	if g := gateByKey(t, tr, "fstec_lab"); g.Owner != "lab-owner" || g.DueDate.String() != "2026-12-01" || g.Cost.Amount != 1_000_000_00 {
		t.Fatalf("гейт после обновления: %+v", g)
	}
	tr = f.pass(tr, "fstec_lab")
	tr, err = f.svc.PassGate(f.ctx, f.cmp, tr.ID, gateByKey(t, tr, "fstec_body").ID)
	if err != nil {
		t.Fatalf("орган после лаборатории: %v", err)
	}
	tr = f.pass(tr, compliance.GateKeyCertificate)
	if tr.Status != compliance.TrackCertified || tr.BaselineID == kernel.NilID {
		t.Fatalf("трек после сертификата: %+v", tr)
	}
	if f.pub.count(compliance.EventBaselineCreated) != 1 || f.pub.count(compliance.EventGatePassed) != 5 {
		t.Fatalf("события: baseline %d, passed %d", f.pub.count(compliance.EventBaselineCreated), f.pub.count(compliance.EventGatePassed))
	}
	bls, err := f.svc.Baselines(f.ctx, presaleScope(), f.server)
	if err != nil || len(bls) != 1 || bls[0].Version != "3.1.0" || bls[0].EOL.Year != 2031 {
		t.Fatalf("baseline: %+v, %v", bls, err)
	}
	// Сертифицированный трек больше не изменяется.
	if _, err := f.svc.UpdateGate(f.ctx, f.cmp, tr.ID, gateByKey(t, tr, "support").ID, compliance.GateUpdate{Owner: "x"}); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("изменение завершённого трека: %v", err)
	}
}

func TestCM03_FailGate(t *testing.T) {
	f := newFixture(t)
	tr := f.startTrack(f.server)
	g := gateByKey(t, tr, "ssdlc")
	if _, err := f.svc.FailGate(f.ctx, f.cmp, tr.ID, g.ID, ""); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("причина обязательна: %v", err)
	}
	tr, err := f.svc.FailGate(f.ctx, f.cmp, tr.ID, g.ID, "SAST не пройден")
	if err != nil {
		t.Fatalf("fail gate: %v", err)
	}
	if tr.Status != compliance.TrackFailed || gateByKey(t, tr, "ssdlc").Status != compliance.GateFailed {
		t.Fatalf("после провала: %+v", tr)
	}
	if f.pub.count(compliance.EventGateFailed) != 1 {
		t.Fatal("событие провала не опубликовано")
	}
}

func TestCM03_ABAC_PMCannotPassGate_PresaleSeesTrack(t *testing.T) {
	f := newFixture(t)
	tr := f.startTrack(f.server)
	pm := pmScope(f.server)
	// PM продукта: не запускает трек, не проходит гейт, не добавляет доказательства.
	if _, err := f.svc.StartTrack(f.ctx, pm, compliance.TrackInput{ProductID: f.agent, ReleaseID: kernel.NewID(), Version: "1"}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM запустил трек: %v", err)
	}
	if _, err := f.svc.PassGate(f.ctx, pm, tr.ID, gateByKey(t, tr, "ssdlc").ID); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM прошёл гейт: %v", err)
	}
	if _, err := f.svc.AppendEvidence(f.ctx, pm, compliance.EvidenceInput{TrackID: tr.ID, GateID: tr.Gates[0].ID, URL: "https://x.test", SHA256: sha}); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM добавил доказательство: %v", err)
	}
	// Presale видит статус трека и доказательства (стратегический срез), но не пишет.
	presale := presaleScope()
	got, err := f.svc.Track(f.ctx, presale, tr.ID)
	if err != nil || got.ID != tr.ID {
		t.Fatalf("presale не видит трек: %v", err)
	}
	if _, err := f.svc.Evidence(f.ctx, presale, tr.ID); err != nil {
		t.Fatalf("presale не видит доказательства: %v", err)
	}
	if _, err := f.svc.PassGate(f.ctx, presale, tr.ID, gateByKey(t, tr, "ssdlc").ID); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("presale прошёл гейт: %v", err)
	}
	// Нулевой Scope запрещает всё.
	if _, err := f.svc.Track(f.ctx, authz.Scope{}, tr.ID); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой scope: %v", err)
	}
	if _, err := f.svc.ReleaseReadiness(f.ctx, authz.Scope{}, tr.ReleaseID); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой scope readiness: %v", err)
	}
}

// ---------- CM-04 ----------

func TestCM04_EvidenceChainAndTamper(t *testing.T) {
	f := newFixture(t)
	tr := f.startTrack(f.server)
	g := gateByKey(t, tr, "ssdlc")
	if _, err := f.svc.AppendEvidence(f.ctx, f.cmp, compliance.EvidenceInput{TrackID: tr.ID, GateID: g.ID, URL: "https://x.test", SHA256: "zz"}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("некорректный SHA-256 принят: %v", err)
	}
	e1, err := f.svc.AppendEvidence(f.ctx, f.cmp, compliance.EvidenceInput{TrackID: tr.ID, GateID: g.ID, URL: "https://x.test/sast", SHA256: strings.ToUpper(sha)})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if e1.Seq != 1 || e1.PrevHash != compliance.EvidenceGenesisHash || e1.SHA256 != sha || e1.Status != compliance.EvidenceSubmitted || e1.Actor != "compliance-1" {
		t.Fatalf("e1: %+v", e1)
	}
	e2, err := f.svc.AppendEvidence(f.ctx, f.cmp, compliance.EvidenceInput{TrackID: tr.ID, GateID: g.ID, URL: "https://x.test/sca", SHA256: sha})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if e2.Seq != 2 || e2.PrevHash != e1.Hash {
		t.Fatalf("e2 не сцеплена: %+v", e2)
	}
	// Смена статуса — новая запись, старая не меняется.
	e3, err := f.svc.SetEvidenceStatus(f.ctx, f.cmp, e1.ID, compliance.EvidenceRejected, "хеш не совпал")
	if err != nil {
		t.Fatalf("set status: %v", err)
	}
	if e3.Seq != 3 || e3.Supersedes != 1 || e3.ID != e1.ID || e3.PrevHash != e2.Hash {
		t.Fatalf("e3: %+v", e3)
	}
	list, err := f.svc.Evidence(f.ctx, f.cmp, tr.ID)
	if err != nil || len(list) != 2 || list[1].Status != compliance.EvidenceRejected {
		t.Fatalf("актуальные записи: %+v, %v", list, err)
	}
	if f.pub.count(compliance.EventEvidenceAppended) != 3 {
		t.Fatalf("событий: %d", f.pub.count(compliance.EventEvidenceAppended))
	}
	// Отклонённым доказательством пункт не закрыть; принятым — можно.
	if _, err := f.svc.CheckItem(f.ctx, f.cmp, tr.ID, g.ID, "sast", e1.ID); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("отклонённое доказательство: %v", err)
	}
	if _, err := f.svc.SetEvidenceStatus(f.ctx, f.cmp, e2.ID, compliance.EvidenceAccepted, ""); err != nil {
		t.Fatalf("accept: %v", err)
	}
	tr, err = f.svc.CheckItem(f.ctx, f.cmp, tr.ID, g.ID, "sast", e2.ID)
	if err != nil {
		t.Fatalf("check item: %v", err)
	}
	if g := gateByKey(t, tr, "ssdlc"); !g.Checklist[0].Done || g.Checklist[0].EvidenceID != e2.ID || g.Status != compliance.GateInProgress {
		t.Fatalf("пункт не закрыт: %+v", g)
	}
	// Проверка целостности: цела; после подмены — нарушена с указанием записи.
	res, err := f.svc.VerifyEvidence(f.ctx, f.cmp)
	if err != nil || !res.OK || res.Checked != 4 {
		t.Fatalf("verify: %+v, %v", res, err)
	}
	if _, err := f.svc.VerifyEvidence(f.ctx, pmScope(f.server)); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM проверил журнал: %v", err)
	}
	f.evidence.Tamper(2, func(e *compliance.EvidenceItem) { e.URL = "https://evil.test" })
	res, err = f.svc.VerifyEvidence(f.ctx, f.cmp)
	if err != nil || res.OK || res.BrokenSeq != 2 {
		t.Fatalf("подмена не обнаружена: %+v, %v", res, err)
	}
}

// ---------- CM-05 ----------

func TestCM05_ReleaseReadiness(t *testing.T) {
	f := newFixture(t)
	r, err := f.svc.ReleaseReadiness(f.ctx, f.cmp, kernel.NewID())
	if err != nil || r.Ready || len(r.OpenItems) != 1 {
		t.Fatalf("релиз без трека: %+v, %v", r, err)
	}
	tr := f.startTrack(f.server)
	r, err = f.svc.ReleaseReadiness(f.ctx, presaleScope(), tr.ReleaseID)
	if err != nil || r.Ready || len(r.OpenItems) != 6 { // 5 пунктов + гейт не пройден
		t.Fatalf("новый трек: %+v, %v", r, err)
	}
	tr = f.closeChecklist(tr, "ssdlc")
	r, _ = f.svc.ReleaseReadiness(f.ctx, f.cmp, tr.ReleaseID)
	if r.Ready || len(r.OpenItems) != 1 {
		t.Fatalf("чек-лист закрыт, гейт не пройден: %+v", r)
	}
	if _, err := f.svc.PassGate(f.ctx, f.cmp, tr.ID, gateByKey(t, tr, "ssdlc").ID); err != nil {
		t.Fatalf("pass: %v", err)
	}
	r, err = f.svc.ReleaseReadiness(f.ctx, f.cmp, tr.ReleaseID)
	if err != nil || !r.Ready || len(r.OpenItems) != 0 {
		t.Fatalf("готовность: %+v, %v", r, err)
	}
}

// ---------- CM-06 ----------

func TestCM06_ImpactClassWithJustificationAndHistory(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.SetImpactClass(f.ctx, f.cmp, f.agentFeature, f.agent, compliance.ImpactSecurityFunctions, ""); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("без обоснования: %v", err)
	}
	if _, err := f.svc.SetImpactClass(f.ctx, f.cmp, f.agentFeature, f.server, compliance.ImpactNone, "x"); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("чужой продукт: %v", err)
	}
	if _, err := f.svc.SetImpactClass(f.ctx, pmScope(f.agent), f.agentFeature, f.agent, compliance.ImpactNone, "x"); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("PM выставил класс: %v", err)
	}
	a1, err := f.svc.SetImpactClass(f.ctx, f.cmp, f.agentFeature, f.agent, compliance.ImpactAnalysisRequired, "меняет протокол")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if a1.Author != "compliance-1" || a1.Class != compliance.ImpactAnalysisRequired {
		t.Fatalf("a1: %+v", a1)
	}
	if _, err := f.svc.SetImpactClass(f.ctx, f.cmp, f.agentFeature, f.agent, compliance.ImpactSecurityFunctions, "затрагивает шифрование"); err != nil {
		t.Fatalf("set 2: %v", err)
	}
	cur, err := f.svc.ImpactClass(f.ctx, presaleScope(), f.agentFeature)
	if err != nil || cur.Class != compliance.ImpactSecurityFunctions {
		t.Fatalf("действующая оценка: %+v, %v", cur, err)
	}
	hist, err := f.svc.ImpactHistory(f.ctx, f.cmp, f.agentFeature)
	if err != nil || len(hist) != 2 || hist[0].ID != a1.ID {
		t.Fatalf("история: %d, %v", len(hist), err)
	}
	if f.pub.count(compliance.EventImpactSet) != 2 {
		t.Fatal("события impact.set")
	}
	if _, err := f.svc.ImpactClass(f.ctx, f.cmp, f.sdkFeature); !errors.Is(err, kernel.ErrNotFound) {
		t.Fatalf("без оценки: %v", err)
	}
}

func TestCM06_ConfirmationCostBySSDLCCertified(t *testing.T) {
	f := newFixture(t)
	// Без оценки — класс none, стоимость 0.
	c, err := f.svc.ConfirmationCost(f.ctx, f.cmp, f.serverFeature)
	if err != nil || !c.IsZero() {
		t.Fatalf("без оценки: %v, %v", c, err)
	}
	for _, id := range []kernel.ID{f.serverFeature, f.agentFeature} {
		p := f.server
		if id == f.agentFeature {
			p = f.agent
		}
		if _, err := f.svc.SetImpactClass(f.ctx, f.cmp, id, p, compliance.ImpactSecurityFunctions, "x"); err != nil {
			t.Fatalf("set: %v", err)
		}
	}
	full, err := f.svc.ConfirmationCost(f.ctx, f.cmp, f.serverFeature) // server: процессы не сертифицированы
	if err != nil || full.Amount != 300_000_00 {
		t.Fatalf("полная стоимость: %v, %v", full, err)
	}
	disc, err := f.svc.ConfirmationCost(f.ctx, f.cmp, f.agentFeature) // agent: SSDLCCertified
	if err != nil || disc.Amount != 150_000_00 {
		t.Fatalf("со скидкой: %v, %v", disc, err)
	}
	st := compliance.DefaultSettings()
	st.CertifiedProcessDiscount = decimal.RequireFromString("1.5")
	if err := f.svc.UpdateSettings(f.ctx, adminScope(), st); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("скидка > 1 принята: %v", err)
	}
	if err := f.svc.UpdateSettings(f.ctx, f.cmp, compliance.DefaultSettings()); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("compliance изменил настройки: %v", err)
	}
}

// ---------- CM-07 ----------

func TestCM07_AffectedBaselinesViaBundledAndShared(t *testing.T) {
	f := newFixture(t)
	serverTrack := f.certify(f.startTrack(f.server))
	agentTrack := f.certify(f.startTrack(f.agent))
	// Фича агента (компонент в поставке Server) затрагивает baseline агента и Server.
	got, err := f.svc.AffectedBaselines(f.ctx, f.cmp, f.agentFeature)
	if err != nil {
		t.Fatalf("affected: %v", err)
	}
	byProduct := map[kernel.ID]compliance.AffectedBaseline{}
	for _, a := range got {
		byProduct[a.Baseline.ProductID] = a
	}
	if len(got) != 2 {
		t.Fatalf("затронуто %d, ожидалось 2: %+v", len(got), got)
	}
	own := byProduct[f.agent]
	if own.Baseline.ID != agentTrack.BaselineID || len(own.Path) != 1 || own.Procedure != compliance.ProcedureSimplified {
		t.Fatalf("собственный baseline: %+v", own)
	}
	srv := byProduct[f.server]
	if srv.Baseline.ID != serverTrack.BaselineID || len(srv.Path) != 2 || srv.Path[0] != f.agent || srv.Path[1] != f.server || srv.Procedure != compliance.ProcedureFull {
		t.Fatalf("baseline Server: %+v", srv)
	}
	// Фича Server (дистрибутив) не затрагивает baseline агента, но затрагивает SDK (общий компонент — в обе стороны).
	got, err = f.svc.AffectedBaselines(f.ctx, f.cmp, f.serverFeature)
	if err != nil || len(got) != 1 || got[0].Baseline.ProductID != f.server {
		t.Fatalf("фича Server: %+v, %v", got, err)
	}
	f.certify(f.startTrack(f.sdk))
	got, err = f.svc.AffectedBaselines(f.ctx, f.cmp, f.serverFeature)
	if err != nil || len(got) != 2 {
		t.Fatalf("после baseline SDK: %+v, %v", got, err)
	}
	got, err = f.svc.AffectedBaselines(f.ctx, f.cmp, f.sdkFeature)
	if err != nil || len(got) != 2 {
		t.Fatalf("фича SDK: %+v, %v", got, err)
	}
	// Стратегический доступ ограничивает список: PM только с agent не увидит baseline Server.
	got, err = f.svc.AffectedBaselines(f.ctx, pmScope(f.agent), f.agentFeature)
	if err != nil || len(got) != 1 || got[0].Baseline.ProductID != f.agent {
		t.Fatalf("PM agent: %+v, %v", got, err)
	}
}
