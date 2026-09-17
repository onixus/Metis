package compliance_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onixus/metis/internal/adapters/securityfile"
	"github.com/onixus/metis/internal/commitments"
	"github.com/onixus/metis/internal/compliance"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// deadlineAdapter — порт реестра обязательств, как он собран в internal/app (CM-08 → CT-02).
type deadlineAdapter struct{ svc *commitments.Service }

func (d deadlineAdapter) StartVulnerabilityDeadline(ctx context.Context, sc authz.Scope, req compliance.DeadlineRequest) (kernel.ID, error) {
	c, err := d.svc.StartVulnerabilityDeadline(ctx, sc, req.ProductID, req.Subject, req.Basis, req.DueDate)
	if err != nil {
		return kernel.NilID, err
	}
	return c.ID, nil
}

// certifiedWithComponents доводит трек до сертификата и записывает состав baseline.
func (f *fixture) certifiedWithComponents(productID kernel.ID, components ...compliance.Component) compliance.CertifiedBaseline {
	f.t.Helper()
	tr := f.certify(f.startTrack(productID))
	if tr.BaselineID == kernel.NilID {
		f.t.Fatal("после гейта сертификата не создан baseline")
	}
	b, err := f.svc.SetBaselineComponents(f.ctx, f.cmp, tr.BaselineID, components)
	if err != nil {
		f.t.Fatalf("состав baseline: %v", err)
	}
	return b
}

// TestCM08_VulnerableComponentListsBaselinesAndStartsDeadlines: по идентификатору уязвимого
// компонента платформа отвечает сертифицированными версиями и запускает регуляторные сроки (CT-02).
func TestCM08_VulnerableComponentListsBaselinesAndStartsDeadlines(t *testing.T) {
	f := newFixture(t)
	comms := commitments.NewService(commitments.NewMemStore(), f.pub, kernel.FixedClock{T: time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)})
	svc := f.svc.WithDeadlines(deadlineAdapter{svc: comms})

	openssl := compliance.Component{Key: "pkg:generic/openssl", Version: "3.0.12"}
	f.certifiedWithComponents(f.agent, openssl, compliance.Component{Key: "pkg:generic/zlib", Version: "1.3"})
	f.certifiedWithComponents(f.sdk, openssl)
	f.certifiedWithComponents(f.server, compliance.Component{Key: "pkg:generic/openssl", Version: "3.5.0"})

	impact, err := svc.ReportVulnerableComponent(f.ctx, f.cmp, openssl, compliance.SeverityCritical)
	if err != nil {
		t.Fatalf("сообщение об уязвимости: %v", err)
	}
	if len(impact.Baselines) != 2 {
		t.Fatalf("затронутых версий %d, ожидалось 2: %+v", len(impact.Baselines), impact.Baselines)
	}
	if len(impact.Deadlines) != 2 {
		t.Fatalf("запущено сроков %d, ожидалось 2", len(impact.Deadlines))
	}
	want := kernel.DateOf(2026, time.October, 17) // 30 дней от 17 сентября 2026 для critical
	for _, d := range impact.Deadlines {
		if d.DueDate != want {
			t.Fatalf("срок устранения %s, ожидался %s", d.DueDate, want)
		}
	}
	list, err := comms.List(f.ctx, f.cmp, commitments.Filter{Kind: commitments.KindRegulatory, Subtype: commitments.SubtypeVulnFixDeadline})
	if err != nil {
		t.Fatalf("обязательства: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("создано обязательств %d, ожидалось 2", len(list))
	}
	// Повторное сообщение не плодит дубликаты.
	if _, err := svc.ReportVulnerableComponent(f.ctx, f.cmp, openssl, compliance.SeverityCritical); err != nil {
		t.Fatalf("повторное сообщение: %v", err)
	}
	list, err = comms.List(f.ctx, f.cmp, commitments.Filter{Kind: commitments.KindRegulatory, Subtype: commitments.SubtypeVulnFixDeadline})
	if err != nil {
		t.Fatalf("обязательства: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("после повтора обязательств %d", len(list))
	}
}

// TestCM08_ForbiddenWithoutAccess: нулевой Scope отклоняется, а PM чужого продукта
// не видит затронутые версии и не заводит регуляторные сроки.
func TestCM08_ForbiddenWithoutAccess(t *testing.T) {
	f := newFixture(t)
	comms := commitments.NewService(commitments.NewMemStore(), f.pub, kernel.FixedClock{T: time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)})
	svc := f.svc.WithDeadlines(deadlineAdapter{svc: comms})
	openssl := compliance.Component{Key: "pkg:generic/openssl", Version: "3.0.12"}
	f.certifiedWithComponents(f.agent, openssl)

	var zero authz.Scope
	if _, err := svc.ReportVulnerableComponent(f.ctx, zero, openssl, compliance.SeverityHigh); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatalf("нулевой Scope: ожидался отказ, получено %v", err)
	}
	impact, err := svc.ReportVulnerableComponent(f.ctx, pmScope(f.server), openssl, compliance.SeverityHigh)
	if err != nil {
		t.Fatalf("PM чужого продукта: %v", err)
	}
	if len(impact.Baselines) != 0 || len(impact.Deadlines) != 0 {
		t.Fatalf("PM чужого продукта увидел чужие версии: %+v", impact)
	}
}

// TestCM08_SeverityDrivesDeadline: срок устранения зависит от критичности.
func TestCM08_SeverityDrivesDeadline(t *testing.T) {
	f := newFixture(t)
	comms := commitments.NewService(commitments.NewMemStore(), f.pub, kernel.FixedClock{T: time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)})
	svc := f.svc.WithDeadlines(deadlineAdapter{svc: comms})
	component := compliance.Component{Key: "pkg:generic/libxml2", Version: "2.12.0"}
	f.certifiedWithComponents(f.agent, component)

	impact, err := svc.ReportVulnerableComponent(f.ctx, f.cmp, component, compliance.SeverityMedium)
	if err != nil {
		t.Fatalf("сообщение об уязвимости: %v", err)
	}
	if len(impact.Deadlines) != 1 || impact.Deadlines[0].DueDate != kernel.DateOf(2026, time.December, 16) {
		t.Fatalf("срок для medium: %+v", impact.Deadlines)
	}
	if _, err := svc.ReportVulnerableComponent(f.ctx, f.cmp, component, "неизвестная"); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("неизвестная критичность принята: %v", err)
	}
}

// TestCM09_PipelineEvidenceCollectedAppendOnly: доказательства из пайплайна попадают в журнал,
// повторный сбор не дублирует записи, состав из SBOM записывается в baseline (CM-08).
func TestCM09_PipelineEvidenceCollectedAppendOnly(t *testing.T) {
	f := newFixture(t)
	tr := f.startTrack(f.agent)
	gate := gateByKey(t, tr, "ssdlc")

	dir := t.TempDir()
	writeManifest(t, dir, map[string]any{
		"project": "agent",
		"artifacts": []map[string]any{
			{"kind": "sast", "tool": "svace", "title": "Отчёт SAST", "uri": "https://ci.example.test/sast.sarif",
				"sha256": sha, "produced_at": "2026-09-01T10:00:00Z"},
			{"kind": "sbom", "tool": "cyclonedx", "title": "SBOM", "uri": "https://ci.example.test/sbom.json",
				"sha256": otherSHA, "produced_at": "2026-09-02T10:00:00Z",
				"components": []map[string]string{{"key": "pkg:generic/openssl", "version": "3.0.12"}}},
		},
	})
	svc := f.svc.WithPipeline(securityfile.New(dir))

	res, err := svc.CollectPipelineEvidence(f.ctx, f.cmp, tr.ID, gate.ID, "agent", time.Time{})
	if err != nil {
		t.Fatalf("сбор доказательств: %v", err)
	}
	if len(res.Collected) != 2 {
		t.Fatalf("собрано %d доказательств, ожидалось 2", len(res.Collected))
	}
	if len(res.SBOM) != 1 || res.SBOM[0].Key != "pkg:generic/openssl" {
		t.Fatalf("состав из SBOM: %+v", res.SBOM)
	}
	again, err := svc.CollectPipelineEvidence(f.ctx, f.cmp, tr.ID, gate.ID, "agent", time.Time{})
	if err != nil {
		t.Fatalf("повторный сбор: %v", err)
	}
	if len(again.Collected) != 0 || again.Skipped != 2 {
		t.Fatalf("повторный сбор продублировал записи: %+v", again)
	}
	verify, err := svc.VerifyEvidence(f.ctx, adminScope())
	if err != nil {
		t.Fatalf("проверка журнала: %v", err)
	}
	if !verify.OK {
		t.Fatalf("журнал доказательств нарушен: %+v", verify)
	}
	// После сертификации состав из SBOM записывается в baseline и виден поиску (CM-08).
	certified := f.certify(tr)
	if _, err := svc.SetBaselineComponents(f.ctx, f.cmp, certified.BaselineID, res.SBOM); err != nil {
		t.Fatalf("состав baseline: %v", err)
	}
	impact, err := svc.ReportVulnerableComponent(f.ctx, f.cmp,
		compliance.Component{Key: "pkg:generic/openssl", Version: "3.0.12"}, compliance.SeverityHigh)
	if err != nil {
		t.Fatalf("поиск по компоненту: %v", err)
	}
	if len(impact.Baselines) != 1 {
		t.Fatalf("состав из SBOM не попал в baseline: %+v", impact.Baselines)
	}
}

// TestCM09_PipelineNotConnected: без адаптера пайплайна автосбор отвечает «недоступно».
func TestCM09_PipelineNotConnected(t *testing.T) {
	f := newFixture(t)
	tr := f.startTrack(f.agent)
	gate := gateByKey(t, tr, "ssdlc")
	if _, err := f.svc.CollectPipelineEvidence(f.ctx, f.cmp, tr.ID, gate.ID, "agent", time.Time{}); !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("ожидалась недоступность, получено %v", err)
	}
}

// TestEC06_TrackCostFromGates: затраты треков продукта складываются из плановых затрат гейтов.
func TestEC06_TrackCostFromGates(t *testing.T) {
	f := newFixture(t)
	tr := f.startTrack(f.agent)
	cost := kernel.RUB(1_500_000_00)
	updated, err := f.svc.UpdateGate(f.ctx, f.cmp, tr.ID, gateByKey(t, tr, "ssdlc").ID, compliance.GateUpdate{Cost: cost})
	if err != nil {
		t.Fatalf("затраты гейта: %v", err)
	}
	planned, err := f.svc.TrackPlannedCost(f.ctx, f.cmp, updated.ID)
	if err != nil {
		t.Fatalf("затраты трека: %v", err)
	}
	if planned != cost {
		t.Fatalf("плановые затраты трека %s, ожидалось %s", planned, cost)
	}
	byProduct, err := f.svc.TrackCost(f.ctx, f.cmp, f.agent)
	if err != nil {
		t.Fatalf("затраты продукта: %v", err)
	}
	if byProduct != cost {
		t.Fatalf("затраты треков продукта %s", byProduct)
	}
}

const otherSHA = "ef" + "01" + "23456789abcdef0123456789abcdef0123456789abcdef0123456789abcd" // 64 hex

func writeManifest(t *testing.T, dir string, m map[string]any) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("манифест: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.json"), raw, 0o600); err != nil {
		t.Fatalf("файл манифеста: %v", err)
	}
}
