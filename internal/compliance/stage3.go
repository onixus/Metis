package compliance

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

// Severity — критичность уязвимости; от неё зависит регуляторный срок устранения (CM-08, CT-02).
type Severity string

// Значения критичности.
const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

// ValidSeverity сообщает, известна ли критичность.
func ValidSeverity(s Severity) bool {
	switch s {
	case SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow:
		return true
	}
	return false
}

// DeadlineRequest — запрос на запуск регуляторного срока устранения (CM-08 → CT-02).
type DeadlineRequest struct {
	ProductID kernel.ID
	// Subject — что нужно сделать.
	Subject string
	// Basis — основание; по нему обеспечивается идемпотентность повторного сообщения.
	Basis   string
	DueDate kernel.Date
}

// DeadlineRegistrar — порт модуля commitments: заводит регуляторное обязательство со сроком
// устранения уязвимости. Повторный запрос с тем же основанием не создаёт дубликата.
type DeadlineRegistrar interface {
	StartVulnerabilityDeadline(ctx context.Context, sc authz.Scope, req DeadlineRequest) (kernel.ID, error)
}

// Deadline — запущенный регуляторный срок (CT-02).
type Deadline struct {
	ProductID    kernel.ID   `json:"product_id"`
	BaselineID   kernel.ID   `json:"baseline_id"`
	Version      string      `json:"version"`
	CommitmentID kernel.ID   `json:"commitment_id"`
	DueDate      kernel.Date `json:"due_date"`
}

// VulnerabilityImpact — ответ на сообщение об уязвимом компоненте (CM-08).
type VulnerabilityImpact struct {
	Component  Component           `json:"component"`
	Severity   Severity            `json:"severity"`
	Baselines  []CertifiedBaseline `json:"baselines"`
	Deadlines  []Deadline          `json:"deadlines"`
	ReportedAt kernel.Date         `json:"reported_at"`
}

// Названия доменных событий этапа 3.
const (
	EventVulnerabilityReported = "compliance.vulnerability.reported"
	EventBaselineComponents    = "compliance.baseline.components_saved"
)

// WithDeadlines подключает реестр обязательств: без него CM-08 только показывает
// затронутые версии, не запуская сроки.
func (s *Service) WithDeadlines(r DeadlineRegistrar) *Service {
	s.deadlines = r
	return s
}

// WithPipeline подключает пайплайн безопасности для автосбора доказательств (CM-09).
func (s *Service) WithPipeline(p ports.SecurityPipeline) *Service {
	s.pipeline = p
	return s
}

// SetBaselineComponents записывает состав поставки сертифицированной версии (CM-08).
// Состав приходит из SBOM пайплайна безопасности или заводится вручную.
func (s *Service) SetBaselineComponents(ctx context.Context, sc authz.Scope, baselineID kernel.ID, components []Component) (CertifiedBaseline, error) {
	b, err := s.store.Baseline(ctx, baselineID)
	if err != nil {
		return CertifiedBaseline{}, err
	}
	if err := sc.Require(authz.ActionWriteCompliance, b.ProductID); err != nil {
		return CertifiedBaseline{}, err
	}
	clean := make([]Component, 0, len(components))
	seen := map[string]bool{}
	for _, c := range components {
		key := strings.TrimSpace(c.Key)
		if key == "" {
			return CertifiedBaseline{}, kernel.Invalid("components", "у компонента пустой ключ")
		}
		version := strings.TrimSpace(c.Version)
		if seen[key+"@"+version] {
			continue
		}
		seen[key+"@"+version] = true
		clean = append(clean, Component{Key: key, Version: version})
	}
	sort.Slice(clean, func(i, j int) bool {
		if clean[i].Key != clean[j].Key {
			return clean[i].Key < clean[j].Key
		}
		return clean[i].Version < clean[j].Version
	})
	b.Components = clean
	if err := s.store.SaveBaseline(ctx, b); err != nil {
		return CertifiedBaseline{}, fmt.Errorf("baseline %s: %w", b.ID, err)
	}
	if err := s.emit(ctx, EventBaselineComponents, b.ID, b.ProductID, sc.Subject(), b); err != nil {
		return CertifiedBaseline{}, err
	}
	return b, nil
}

// ReportVulnerableComponent по идентификатору уязвимого компонента возвращает сертифицированные
// версии, в состав которых он входит, и запускает регуляторные сроки устранения (CM-08 → CT-02).
// Повторное сообщение о том же компоненте не создаёт вторых обязательств.
func (s *Service) ReportVulnerableComponent(ctx context.Context, sc authz.Scope, component Component, severity Severity) (VulnerabilityImpact, error) {
	if !sc.Valid() {
		return VulnerabilityImpact{}, kernel.ErrForbidden
	}
	key := strings.TrimSpace(component.Key)
	if key == "" {
		return VulnerabilityImpact{}, kernel.Invalid("component", "идентификатор компонента обязателен")
	}
	if !ValidSeverity(severity) {
		return VulnerabilityImpact{}, kernel.Invalid("severity", "допустимы critical, high, medium, low")
	}
	component.Key, component.Version = key, strings.TrimSpace(component.Version)

	found, err := s.store.BaselinesWithComponent(ctx, key)
	if err != nil {
		return VulnerabilityImpact{}, fmt.Errorf("baselines по компоненту %q: %w", key, err)
	}
	today := kernel.DateFromTime(s.clock.Now())
	impact := VulnerabilityImpact{Component: component, Severity: severity, ReportedAt: today,
		Baselines: make([]CertifiedBaseline, 0, len(found))}
	for _, b := range found {
		if !b.HasComponent(component) {
			continue
		}
		// Затронутые версии видит тот, кому доступен продукт: выдача не расширяет доступ.
		if !sc.Allows(authz.ActionReadStrategic, b.ProductID) {
			continue
		}
		impact.Baselines = append(impact.Baselines, b)
	}
	if s.deadlines == nil || len(impact.Baselines) == 0 {
		if err := s.emit(ctx, EventVulnerabilityReported, kernel.NewID(), kernel.NilID, sc.Subject(), impact); err != nil {
			return VulnerabilityImpact{}, err
		}
		return impact, nil
	}
	days, err := s.vulnerabilityDays(ctx, sc, severity)
	if err != nil {
		return VulnerabilityImpact{}, err
	}
	due := today.AddDays(days)
	for _, b := range impact.Baselines {
		if err := sc.Require(authz.ActionWriteCommitments, b.ProductID); err != nil {
			return VulnerabilityImpact{}, err
		}
		id, err := s.deadlines.StartVulnerabilityDeadline(ctx, sc, DeadlineRequest{
			ProductID: b.ProductID,
			Subject:   fmt.Sprintf("Устранить уязвимый компонент %s в сертифицированной версии %s", componentName(component), b.Version),
			Basis:     vulnerabilityBasis(component, b.ID),
			DueDate:   due,
		})
		if err != nil {
			return VulnerabilityImpact{}, fmt.Errorf("регуляторный срок: %w", err)
		}
		impact.Deadlines = append(impact.Deadlines, Deadline{ProductID: b.ProductID, BaselineID: b.ID,
			Version: b.Version, CommitmentID: id, DueDate: due})
	}
	if err := s.emit(ctx, EventVulnerabilityReported, kernel.NewID(), kernel.NilID, sc.Subject(), impact); err != nil {
		return VulnerabilityImpact{}, err
	}
	return impact, nil
}

func componentName(c Component) string {
	if c.Version == "" {
		return c.Key
	}
	return c.Key + "@" + c.Version
}

// vulnerabilityBasis — основание обязательства; по нему CM-08 идемпотентен.
func vulnerabilityBasis(c Component, baselineID kernel.ID) string {
	return fmt.Sprintf("уязвимость компонента %s, baseline %s", componentName(c), baselineID)
}

// vulnerabilityDays — регуляторный срок устранения по критичности (TODO(question-31):
// значения не заданы ТЗ, уточняются у compliance-офицера).
func (s *Service) vulnerabilityDays(ctx context.Context, sc authz.Scope, severity Severity) (int, error) {
	st, err := s.Settings(ctx, sc)
	if err != nil {
		// После персистирования Settings ошибка чтения означает, что мы не знаем
		// настроенный регуляторный SLA. Молчаливый fallback мог бы создать
		// обязательство с неверной юридически значимой датой.
		return 0, fmt.Errorf("настройки срока устранения уязвимости: %w", err)
	}
	if days, ok := st.VulnerabilityFixDays[severity]; ok && days > 0 {
		return days, nil
	}
	switch severity {
	case SeverityCritical:
		return 30, nil
	case SeverityHigh:
		return 60, nil
	case SeverityMedium:
		return 90, nil
	default:
		return 180, nil
	}
}

// CollectResult — итог автосбора доказательств из пайплайна безопасности (CM-09).
type CollectResult struct {
	TrackID   kernel.ID      `json:"track_id"`
	GateID    kernel.ID      `json:"gate_id"`
	Collected []EvidenceItem `json:"collected"`
	// Skipped — артефакты, уже лежащие в журнале (сравнение по SHA-256).
	Skipped int `json:"skipped"`
	// SBOM — состав поставки из артефактов SBOM. Если baseline трека уже создан, состав
	// записан в него; иначе состав отдаётся вызывающему и попадает в baseline явным
	// вызовом SetBaselineComponents после сертификации (TODO(question-39)).
	SBOM []Component `json:"sbom,omitempty"`
	// ComponentsSaved — число компонентов, записанных в baseline трека (CM-08).
	ComponentsSaved int `json:"components_saved"`
}

// CollectPipelineEvidence складывает артефакты пайплайна безопасности (SAST, SCA, DAST, фаззинг,
// SBOM) в журнал доказательств гейта (CM-09). Повторный сбор не дублирует записи. Состав из SBOM
// записывается в baseline трека, если он уже создан: по нему работает поиск CM-08.
func (s *Service) CollectPipelineEvidence(ctx context.Context, sc authz.Scope, trackID, gateID kernel.ID, project string, since time.Time) (CollectResult, error) {
	if s.pipeline == nil {
		return CollectResult{}, fmt.Errorf("%w: пайплайн безопасности не подключён", kernel.ErrUnavailable)
	}
	t, err := s.loadForWrite(ctx, sc, trackID)
	if err != nil {
		return CollectResult{}, err
	}
	if _, err := gateIndex(t, gateID); err != nil {
		return CollectResult{}, err
	}
	artifacts, err := s.pipeline.Artifacts(ctx, project, since)
	if err != nil {
		return CollectResult{}, fmt.Errorf("%w: пайплайн безопасности: %w", kernel.ErrUnavailable, err)
	}
	known, err := s.knownEvidence(ctx, sc, trackID)
	if err != nil {
		return CollectResult{}, err
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].ProducedAt.Before(artifacts[j].ProducedAt) })

	out := CollectResult{TrackID: trackID, GateID: gateID}
	var components []Component
	for _, a := range artifacts {
		sha := strings.ToLower(strings.TrimSpace(a.SHA256))
		if !ValidSHA256(sha) {
			return CollectResult{}, kernel.Invalid("sha256", fmt.Sprintf("артефакт %q: ожидается hex SHA-256 из 64 символов", a.Title))
		}
		for _, c := range a.Components {
			components = append(components, Component{Key: c.Key, Version: c.Version})
		}
		if known[sha] {
			out.Skipped++
			continue
		}
		item, err := s.AppendEvidence(ctx, sc, EvidenceInput{TrackID: trackID, GateID: gateID,
			URL: a.URI, SHA256: sha, Comment: pipelineComment(a)})
		if err != nil {
			return CollectResult{}, err
		}
		known[sha] = true
		out.Collected = append(out.Collected, item)
	}
	out.SBOM = components
	if len(components) > 0 && t.BaselineID != kernel.NilID {
		b, err := s.SetBaselineComponents(ctx, sc, t.BaselineID, components)
		if err != nil {
			return CollectResult{}, err
		}
		out.ComponentsSaved = len(b.Components)
	}
	return out, nil
}

// pipelineComment — что это за артефакт: вид проверки и инструмент.
func pipelineComment(a ports.SecurityArtifact) string {
	parts := make([]string, 0, 3)
	if a.Kind != "" {
		parts = append(parts, a.Kind)
	}
	if a.Tool != "" {
		parts = append(parts, a.Tool)
	}
	if a.Title != "" {
		parts = append(parts, a.Title)
	}
	return strings.Join(parts, ": ")
}

func (s *Service) knownEvidence(ctx context.Context, sc authz.Scope, trackID kernel.ID) (map[string]bool, error) {
	items, err := s.Evidence(ctx, sc, trackID)
	if err != nil {
		return nil, fmt.Errorf("журнал доказательств: %w", err)
	}
	out := make(map[string]bool, len(items))
	for _, e := range items {
		out[e.SHA256] = true
	}
	return out, nil
}

// TrackPlannedCost — плановые затраты гейтов трека (DA-02: сценарий под бюджет).
func (s *Service) TrackPlannedCost(ctx context.Context, sc authz.Scope, trackID kernel.ID) (kernel.Money, error) {
	t, err := s.Track(ctx, sc, trackID)
	if err != nil {
		return kernel.Money{}, err
	}
	return gatesCost(t.Gates)
}

// TrackCost — суммарные плановые затраты треков продукта (EC-06: экономика сертификации).
func (s *Service) TrackCost(ctx context.Context, sc authz.Scope, productID kernel.ID) (kernel.Money, error) {
	tracks, err := s.Tracks(ctx, sc, productID)
	if err != nil {
		return kernel.Money{}, err
	}
	out := kernel.Money{}
	for _, t := range tracks {
		cost, err := gatesCost(t.Gates)
		if err != nil {
			return kernel.Money{}, err
		}
		sum, err := out.Add(cost)
		if err != nil {
			return kernel.Money{}, err
		}
		out = sum
	}
	return out, nil
}

func gatesCost(gates []Gate) (kernel.Money, error) {
	out := kernel.Money{}
	for _, g := range gates {
		if g.Cost.IsZero() {
			continue
		}
		sum, err := out.Add(g.Cost)
		if err != nil {
			return kernel.Money{}, err
		}
		out = sum
	}
	return out, nil
}
