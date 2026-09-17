package seed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/onixus/metis/internal/commitments"
	"github.com/onixus/metis/internal/compliance"
	"github.com/onixus/metis/internal/decisions"
	"github.com/onixus/metis/internal/discovery"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	pg "github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/roadmap"
)

// Stage2Deps — сервисы, нужные seed этапа 2. Все обращения — через публичные интерфейсы модулей.
type Stage2Deps struct {
	Portfolio   *pg.Service
	Roadmap     *roadmap.Service
	Compliance  *compliance.Service
	Commitments *commitments.Service
	Discovery   *discovery.Service
	Decisions   *decisions.Service
}

// Синтетические коды наборов требований (ТЗ 8.2: реальные номера документов не указываются).
const (
	SetCodeRegistry = "REESTR"
	SetCodeFSTEC    = "FSTEC-UD4"

	// Версии релизов, которые заводит seed.
	ServerCertifiedVersion = "1.0"
	SOARCertifiedVersion   = "5.1"
)

// Stage2 дополняет референсные портфели данными этапа 2 идемпотентно: каталог наборов требований и
// шаблоны треков (CM-01, CM-02), сертифицированный baseline Server (CM-07) через полный трек
// сертификации, трек сертификации релиза SOAR ветки certified (RM-04, CM-03), класс влияния фичи
// агента платформы управления (CM-06), клиентское и регуляторное обязательства (CT-01, CT-02),
// гипотеза (DS-01) и решение (DA-01). Вызывается после Security и Infrastructure.
func Stage2(ctx context.Context, d Stage2Deps, sc authz.Scope) error {
	if d.Portfolio == nil || d.Roadmap == nil || d.Compliance == nil || d.Commitments == nil || d.Discovery == nil || d.Decisions == nil {
		return fmt.Errorf("%w: seed этапа 2: не все сервисы переданы", kernel.ErrValidation)
	}
	if err := ensureCatalog(ctx, d, sc); err != nil {
		return err
	}
	server, err := d.Portfolio.ProductIDByKey(ctx, "server")
	if err != nil {
		return fmt.Errorf("seed: продукт server: %w", err)
	}
	soar, err := d.Portfolio.ProductIDByKey(ctx, "soar")
	if err != nil {
		return fmt.Errorf("seed: продукт soar: %w", err)
	}
	mgmt, err := d.Portfolio.ProductIDByKey(ctx, "mgmt")
	if err != nil {
		return fmt.Errorf("seed: продукт mgmt: %w", err)
	}
	edr, err := d.Portfolio.ProductIDByKey(ctx, "edr")
	if err != nil {
		return fmt.Errorf("seed: продукт edr: %w", err)
	}

	// Baseline Server: релиз ветки certified и трек, пройденный до гейта «сертификат».
	serverRelease, err := ensureRelease(ctx, d, sc, server, "Server "+ServerCertifiedVersion, ServerCertifiedVersion, kernel.DateOf(2026, 3, 1))
	if err != nil {
		return err
	}
	if err := ensureCertifiedTrack(ctx, d, sc, server, serverRelease); err != nil {
		return err
	}
	// Трек SOAR: запущен; проходит его сценарий приёмки этапа 2.
	soarRelease, err := ensureRelease(ctx, d, sc, soar, "SOAR "+SOARCertifiedVersion, SOARCertifiedVersion, kernel.DateOf(2027, 3, 1))
	if err != nil {
		return err
	}
	if _, err := ensureTrack(ctx, d, sc, soar, soarRelease); err != nil {
		return err
	}

	// Связь bundled «агент платформы управления → Server» и класс влияния фичи агента.
	agent, err := featureByName(ctx, d, sc, mgmt, "Агент управления v4")
	if err != nil {
		return err
	}
	if err := ensureBundledLink(ctx, d, sc, server, mgmt, agent); err != nil {
		return err
	}
	if err := ensureImpact(ctx, d, sc, agent, mgmt); err != nil {
		return err
	}

	// Обязательства: клиентское по коннектору EDR (SOAR) и регуляторное по сертификату Server.
	connector, err := featureByName(ctx, d, sc, soar, "Коннектор EDR v2")
	if err != nil {
		return err
	}
	if err := ensureCommitment(ctx, d, sc, soar, commitments.Input{
		Kind: commitments.KindCustomer, Counterparty: "A-77", Subject: "Реагирование через SOAR на события EDR",
		DueDate: kernel.DateOf(2026, 12, 31), Basis: "Договор D-1001", Owner: "pm-soar", FeatureID: connector,
	}); err != nil {
		return err
	}
	if err := ensureCommitment(ctx, d, sc, server, commitments.Input{
		Kind: commitments.KindRegulatory, Subtype: commitments.SubtypeCertificateExpiry, Counterparty: "регулятор",
		Subject: "Срок действия сертификата Server " + ServerCertifiedVersion, DueDate: kernel.DateOf(2028, 3, 1),
		Basis: "Сертификат SYN-0001", Owner: "compliance-server", ReleaseID: serverRelease,
	}); err != nil {
		return err
	}

	// Гипотеза EDR и решение SOAR.
	hyp, err := ensureHypothesis(ctx, d, sc, edr)
	if err != nil {
		return err
	}
	return ensureDecision(ctx, d, sc, soar, connector, hyp)
}

// ensureCatalog заводит опубликованные наборы требований и шаблоны треков по умолчанию.
// Код набора привязан к одному типу продукта (CM-01), а шаблоны по умолчанию ссылаются на одни коды
// для всех типов; наборы заводятся для инфраструктурного типа (baseline Server), для остальных типов
// baseline создаётся без ссылки на набор. TODO(question-27).
func ensureCatalog(ctx context.Context, d Stage2Deps, sc authz.Scope) error {
	sets := []struct {
		code  string
		items []compliance.RequirementItem
	}{
		{SetCodeRegistry, []compliance.RequirementItem{{Key: "origin", Text: "Исключительные права на ПО принадлежат российскому лицу"}, {Key: "no_foreign_control", Text: "Отсутствие иностранного контроля"}}},
		{SetCodeFSTEC, []compliance.RequirementItem{{Key: "ud4", Text: "Уровень доверия 4"}, {Key: "ssdlc", Text: "Процессы безопасной разработки"}, {Key: "vuln_fix", Text: "Устранение уязвимостей в установленный срок"}}},
	}
	for _, set := range sets {
		existing, err := d.Compliance.RequirementSets(ctx, sc, set.code)
		if err != nil {
			return fmt.Errorf("seed requirement sets %s: %w", set.code, err)
		}
		published := false
		for _, rs := range existing {
			published = published || rs.Status == compliance.RequirementSetPublished
		}
		if published {
			continue
		}
		rs, err := d.Compliance.CreateRequirementSet(ctx, sc, compliance.RequirementSetInput{Code: set.code, ProductType: pg.ProductTypeInfrastructure, Items: set.items})
		if err != nil {
			return fmt.Errorf("seed requirement set %s: %w", set.code, err)
		}
		if _, err := d.Compliance.SetRequirementSetStatus(ctx, sc, rs.ID, compliance.RequirementSetPublished); err != nil {
			return fmt.Errorf("seed publish %s: %w", set.code, err)
		}
	}
	for _, tpl := range compliance.DefaultTemplates() {
		existing, err := d.Compliance.Templates(ctx, sc, tpl.ProductType)
		if err != nil {
			return fmt.Errorf("seed templates: %w", err)
		}
		if len(existing) > 0 {
			continue
		}
		if _, err := d.Compliance.SaveTemplate(ctx, sc, tpl); err != nil {
			return fmt.Errorf("seed template %s: %w", tpl.ProductType, err)
		}
	}
	return nil
}

func ensureRelease(ctx context.Context, d Stage2Deps, sc authz.Scope, productID kernel.ID, name, version string, date kernel.Date) (kernel.ID, error) {
	rels, err := d.Roadmap.Releases(ctx, sc, productID)
	if err != nil {
		return kernel.NilID, fmt.Errorf("seed releases: %w", err)
	}
	for _, r := range rels {
		if r.Version == version {
			return r.ID, nil
		}
	}
	r, err := d.Roadmap.CreateRelease(ctx, sc, productID, roadmap.ReleaseInput{Name: name, Version: version, PlannedDate: date, Branch: roadmap.BranchCertified, EOL: date.AddDays(5 * 365)})
	if err != nil {
		return kernel.NilID, fmt.Errorf("seed release %s: %w", version, err)
	}
	return r.ID, nil
}

func ensureTrack(ctx context.Context, d Stage2Deps, sc authz.Scope, productID, releaseID kernel.ID) (compliance.Track, error) {
	tracks, err := d.Compliance.Tracks(ctx, sc, productID)
	if err != nil {
		return compliance.Track{}, fmt.Errorf("seed tracks: %w", err)
	}
	for _, t := range tracks {
		if t.ReleaseID == releaseID {
			return t, nil
		}
	}
	rel, err := d.Roadmap.Release(ctx, sc, releaseID)
	if err != nil {
		return compliance.Track{}, fmt.Errorf("seed release: %w", err)
	}
	t, err := d.Compliance.StartTrack(ctx, sc, compliance.TrackInput{ProductID: productID, ReleaseID: releaseID, Version: rel.Version})
	if err != nil {
		return compliance.Track{}, fmt.Errorf("seed track: %w", err)
	}
	return t, nil
}

// SyntheticSHA256 — hex SHA-256 синтетического артефакта по его имени (для фикстур и сценария приёмки).
func SyntheticSHA256(name string) string {
	sum := sha256.Sum256([]byte("metis-synthetic:" + name))
	return hex.EncodeToString(sum[:])
}

// ensureCertifiedTrack проходит трек до гейта «сертификат» (SSDLC → ветка ФСТЭК), создавая baseline (CM-07).
func ensureCertifiedTrack(ctx context.Context, d Stage2Deps, sc authz.Scope, productID, releaseID kernel.ID) error {
	t, err := ensureTrack(ctx, d, sc, productID, releaseID)
	if err != nil {
		return err
	}
	if t.Status == compliance.TrackCertified {
		return nil
	}
	for _, key := range []string{"ssdlc", "fstec_application", "fstec_lab", "fstec_body", compliance.GateKeyCertificate} {
		if t, err = passGate(ctx, d, sc, t, key); err != nil {
			return err
		}
	}
	return nil
}

// passGate закрывает чек-лист гейта доказательствами и проходит его; уже пройденный гейт пропускается.
func passGate(ctx context.Context, d Stage2Deps, sc authz.Scope, t compliance.Track, key string) (compliance.Track, error) {
	var gate compliance.Gate
	for _, g := range t.Gates {
		if g.Key == key {
			gate = g
		}
	}
	if gate.ID == kernel.NilID {
		return t, fmt.Errorf("%w: seed: гейт %s не найден в треке", kernel.ErrNotFound, key)
	}
	if gate.Status == compliance.GatePassed {
		return t, nil
	}
	var err error
	for _, item := range gate.Checklist {
		if item.Done {
			continue
		}
		name := t.Version + "/" + key + "/" + item.Key
		ev, err := d.Compliance.AppendEvidence(ctx, sc, compliance.EvidenceInput{TrackID: t.ID, GateID: gate.ID, URL: "https://evidence.example.test/" + name, SHA256: SyntheticSHA256(name)})
		if err != nil {
			return t, fmt.Errorf("seed evidence %s: %w", name, err)
		}
		if t, err = d.Compliance.CheckItem(ctx, sc, t.ID, gate.ID, item.Key, ev.ID); err != nil {
			return t, fmt.Errorf("seed check %s: %w", name, err)
		}
	}
	if t, err = d.Compliance.PassGate(ctx, sc, t.ID, gate.ID); err != nil {
		return t, fmt.Errorf("seed pass gate %s: %w", key, err)
	}
	return t, nil
}

func featureByName(ctx context.Context, d Stage2Deps, sc authz.Scope, productID kernel.ID, name string) (kernel.ID, error) {
	fs, err := d.Portfolio.Features(ctx, sc, productID)
	if err != nil {
		return kernel.NilID, fmt.Errorf("seed features: %w", err)
	}
	for _, f := range fs {
		if f.Name == name {
			return f.ID, nil
		}
	}
	return kernel.NilID, fmt.Errorf("%w: seed: фича %q", kernel.ErrNotFound, name)
}

// ensureBundledLink гарантирует связь bundled «фича Server → агент платформы управления» (Infrastructure её заводит при создании).
func ensureBundledLink(ctx context.Context, d Stage2Deps, sc authz.Scope, server, mgmt, agent kernel.ID) error {
	links, err := d.Portfolio.Links(ctx, sc)
	if err != nil {
		return fmt.Errorf("seed links: %w", err)
	}
	for _, l := range links {
		if l.Type == pg.LinkBundled && l.FromProductID == server && l.ToProductID == mgmt {
			return nil
		}
	}
	support, err := featureByName(ctx, d, sc, server, "Поддержка агента v4")
	if err != nil {
		return err
	}
	if _, err := d.Portfolio.CreateLink(ctx, sc, pg.LinkInput{Type: pg.LinkBundled, FromFeatureID: support, ToFeatureID: agent, Criticality: pg.CritBlocks}); err != nil {
		return fmt.Errorf("seed bundled link: %w", err)
	}
	return nil
}

func ensureImpact(ctx context.Context, d Stage2Deps, sc authz.Scope, featureID, productID kernel.ID) error {
	_, err := d.Compliance.ImpactClass(ctx, sc, featureID)
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, kernel.ErrNotFound):
		return fmt.Errorf("seed impact: %w", err)
	}
	if _, err := d.Compliance.SetImpactClass(ctx, sc, featureID, productID, compliance.ImpactSecurityFunctions,
		"Агент выполняет функции безопасности в составе сертифицированных дистрибутивов"); err != nil {
		return fmt.Errorf("seed impact: %w", err)
	}
	return nil
}

func ensureCommitment(ctx context.Context, d Stage2Deps, sc authz.Scope, productID kernel.ID, in commitments.Input) error {
	list, err := d.Commitments.List(ctx, sc, commitments.Filter{ProductID: productID, Kind: in.Kind, Subtype: in.Subtype})
	if err != nil {
		return fmt.Errorf("seed commitments: %w", err)
	}
	for _, c := range list {
		if c.Subject == in.Subject {
			return nil
		}
	}
	if _, err := d.Commitments.Create(ctx, sc, productID, in); err != nil {
		return fmt.Errorf("seed commitment %q: %w", in.Subject, err)
	}
	return nil
}

func ensureHypothesis(ctx context.Context, d Stage2Deps, sc authz.Scope, productID kernel.ID) (kernel.ID, error) {
	const title = "Изоляция хоста из консоли SOAR сокращает время реагирования"
	list, err := d.Discovery.Hypotheses(ctx, sc, productID, discovery.HypothesisFilter{})
	if err != nil {
		return kernel.NilID, fmt.Errorf("seed hypotheses: %w", err)
	}
	for _, h := range list {
		if h.Title == title {
			return h.ID, nil
		}
	}
	h, err := d.Discovery.SaveHypothesis(ctx, sc, discovery.HypothesisInput{
		ProductID: productID, Title: title,
		Statement:             "Мы считаем, что аналитики SOC будут изолировать хосты из плейбуков SOAR, а не из консоли EDR",
		Assumptions:           []string{"SOC пользуется SOAR как основной консолью", "Изоляция обратима"},
		ConfirmationCriterion: "Не меньше трёх аккаунтов сегмента enterprise подтверждают сценарий на интервью",
	})
	if err != nil {
		return kernel.NilID, fmt.Errorf("seed hypothesis: %w", err)
	}
	return h.ID, nil
}

func ensureDecision(ctx context.Context, d Stage2Deps, sc authz.Scope, productID, featureID, hypothesisID kernel.ID) error {
	const title = "Коннектор EDR v2 через Response API, а не через агент"
	list, err := d.Decisions.List(ctx, sc, productID, "")
	if err != nil {
		return fmt.Errorf("seed decisions: %w", err)
	}
	for _, r := range list {
		if r.Title == title {
			return nil
		}
	}
	if _, err := d.Decisions.Create(ctx, sc, decisions.Input{
		ProductID: productID, Title: title,
		Context: "Интеграция SOAR с EDR требует выбора канала управления хостами",
		Options: []decisions.Option{
			{Key: "api", Title: "Response API v2", Description: "Публичный API EDR, независимый релизный цикл"},
			{Key: "agent", Title: "Через агент EDR", Description: "Прямое управление агентом, связывает релизы"},
		},
		ChosenKey: "api", Rationale: "Не связывает сертифицированные ветки продуктов", ExpectedEffect: "Сокращение времени реагирования на 30 %",
		ReviewDate: kernel.DateOf(2027, 3, 1),
		Links:      []decisions.Link{{Kind: decisions.LinkFeature, ID: featureID}, {Kind: decisions.LinkHypothesis, ID: hypothesisID}},
	}); err != nil {
		return fmt.Errorf("seed decision: %w", err)
	}
	return nil
}
