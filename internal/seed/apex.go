package seed

import (
	"context"
	"fmt"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	pg "github.com/onixus/metis/internal/portfoliograph"
)

// APEXStatusWeight is the deterministic completion model used for the APEX
// portfolio baseline. The numbers are planning weights, not claims of release
// readiness: done=100, in_progress=60, planned=20, discovery=10, idea=0.
func APEXStatusWeight(s pg.FeatureStatus) int {
	switch s {
	case pg.FeatureDone:
		return 100
	case pg.FeatureInProgress:
		return 60
	case pg.FeaturePlanned:
		return 20
	case pg.FeatureDiscovery:
		return 10
	default:
		return 0
	}
}

// APEXFeatureTemplate is one evidence-backed workstream in a product template.
// EffortPoints are relative engineering points used only for portfolio planning.
type APEXFeatureTemplate struct {
	Name          string
	Status        pg.FeatureStatus
	PlannedDate   kernel.Date
	EffortPoints  int
	ConfidencePct int
	Evidence      string
}

// APEXProductTemplate is the seed template for one canonical APEX participant.
type APEXProductTemplate struct {
	Key      string
	Name     string
	Type     pg.ProductType
	Owner    string
	Repo     string
	Features []APEXFeatureTemplate
}

// CompletionPct returns the arithmetic mean of status weights.
func (t APEXProductTemplate) CompletionPct() int {
	if len(t.Features) == 0 {
		return 0
	}
	total := 0
	for _, f := range t.Features {
		total += APEXStatusWeight(f.Status)
	}
	return total / len(t.Features)
}

// RemainingEffortPoints sums effort for workstreams that are not done.
func (t APEXProductTemplate) RemainingEffortPoints() int {
	total := 0
	for _, f := range t.Features {
		if f.Status != pg.FeatureDone {
			total += f.EffortPoints
		}
	}
	return total
}

// ConfidencePct returns the effort-weighted confidence of unfinished work.
func (t APEXProductTemplate) ConfidencePct() int {
	var weighted, effort int
	for _, f := range t.Features {
		if f.Status == pg.FeatureDone {
			continue
		}
		weighted += f.ConfidencePct * f.EffortPoints
		effort += f.EffortPoints
	}
	if effort == 0 {
		return 100
	}
	return weighted / effort
}

// APEXProductTemplates returns the canonical portfolio baseline derived from
// repository state and published plans as of 2026-09-22.
//
// The baseline deliberately models workstreams rather than every issue: Metis
// is a portfolio tool, not a second issue tracker.
func APEXProductTemplates() []APEXProductTemplate {
	d := kernel.DateOf
	return []APEXProductTemplate{
		{
			Key: "apex-gateway", Name: "APEX Gateway", Type: pg.ProductTypePlatform, Owner: "platform",
			Repo: "onixus/unified-platform",
			Features: []APEXFeatureTemplate{
				{Name: "Architecture Contract v1", Status: pg.FeatureDone, PlannedDate: d(2026, 9, 21), EffortPoints: 8, ConfidencePct: 100, Evidence: "contracts/v1: registry, schemas, CI contract checks"},
				{Name: "Unified Gateway + Web Console", Status: pg.FeatureInProgress, PlannedDate: d(2026, 10, 15), EffortPoints: 13, ConfidencePct: 85, Evidence: "FastAPI gateway and Next.js console implemented; synthetic fallbacks remain"},
				{Name: "Cross-product contract conformance", Status: pg.FeatureInProgress, PlannedDate: d(2026, 11, 15), EffortPoints: 13, ConfidencePct: 75, Evidence: "registry is enforced centrally; cross-repository conformance rollout is next"},
				{Name: "Production identity and durable control state", Status: pg.FeaturePlanned, PlannedDate: d(2026, 12, 15), EffortPoints: 21, ConfidencePct: 65, Evidence: "README documents demo auth and in-memory control-plane state"},
			},
		},
		{
			Key: "shapoclyack", Name: "Shapoclyack", Type: pg.ProductTypeSecurity, Owner: "security-products",
			Repo: "onixus/Shapoclyack",
			Features: []APEXFeatureTemplate{
				{Name: "EASM / CAASM asset model", Status: pg.FeatureDone, PlannedDate: d(2026, 9, 1), EffortPoints: 13, ConfidencePct: 100, Evidence: "asset-centric discovery and inventory are present in the product"},
				{Name: "Risk-based vulnerability lifecycle", Status: pg.FeatureDone, PlannedDate: d(2026, 9, 1), EffortPoints: 13, ConfidencePct: 100, Evidence: "RBVM and remediation verification are implemented product capabilities"},
				{Name: "APEX contract boundary", Status: pg.FeatureInProgress, PlannedDate: d(2026, 10, 20), EffortPoints: 8, ConfidencePct: 85, Evidence: "canonical participant; contract adoption is being rolled out across repositories"},
				{Name: "Scale and reproducible performance baseline", Status: pg.FeaturePlanned, PlannedDate: d(2026, 11, 30), EffortPoints: 13, ConfidencePct: 70, Evidence: "portfolio plan calls for measurable performance and reproducible claims"},
			},
		},
		{
			Key: "lariska", Name: "Lariska", Type: pg.ProductTypeSecurity, Owner: "endpoint-security",
			Repo: "onixus/Lariska",
			Features: []APEXFeatureTemplate{
				{Name: "Endpoint inventory snapshots", Status: pg.FeatureInProgress, PlannedDate: d(2026, 10, 15), EffortPoints: 13, ConfidencePct: 80, Evidence: "canonical ownership: endpoint inventory snapshots and agent state"},
				{Name: "Local agent telemetry / Shadow IT", Status: pg.FeatureInProgress, PlannedDate: d(2026, 11, 1), EffortPoints: 13, ConfidencePct: 75, Evidence: "APEX integration surface includes fleet, packages and Shadow IT actions"},
				{Name: "APEX identity/event boundary", Status: pg.FeaturePlanned, PlannedDate: d(2026, 11, 30), EffortPoints: 8, ConfidencePct: 70, Evidence: "participant is declared; v1 boundary conformance remains rollout work"},
				{Name: "Fleet rollout and operational hardening", Status: pg.FeaturePlanned, PlannedDate: d(2027, 1, 15), EffortPoints: 21, ConfidencePct: 60, Evidence: "plan extrapolated from current agent-centric implementation toward managed fleet operation"},
			},
		},
		{
			Key: "ferrum", Name: "Ferrum", Type: pg.ProductTypeSecurity, Owner: "cloud-security",
			Repo: "onixus/Ferrum",
			Features: []APEXFeatureTemplate{
				{Name: "Admission/controller/agent MVP", Status: pg.FeatureDone, PlannedDate: d(2026, 9, 1), EffortPoints: 21, ConfidencePct: 100, Evidence: "MVP components and policy path are implemented and documented"},
				{Name: "First signed tagged release", Status: pg.FeaturePlanned, PlannedDate: d(2026, 10, 15), EffortPoints: 8, ConfidencePct: 90, Evidence: "ROADMAP/issues: publish images, cosign, SBOM, first tagged release"},
				{Name: "kind/k3d end-to-end acceptance", Status: pg.FeaturePlanned, PlannedDate: d(2026, 11, 1), EffortPoints: 13, ConfidencePct: 80, Evidence: "open plan: real apiserver acceptance for deny/kill/LKG scenarios"},
				{Name: "BPF LSM synchronous enforcement", Status: pg.FeaturePlanned, PlannedDate: d(2027, 1, 31), EffortPoints: 21, ConfidencePct: 65, Evidence: "roadmap phase 2: LSM mode and tracepoint fallback"},
			},
		},
		{
			Key: "bsdm-proxy", Name: "BSDM-Proxy", Type: pg.ProductTypeSecurity, Owner: "network-security",
			Repo: "onixus/bsdm-proxy",
			Features: []APEXFeatureTemplate{
				{Name: "HTTP/HTTPS forward proxy core", Status: pg.FeatureDone, PlannedDate: d(2026, 9, 1), EffortPoints: 21, ConfidencePct: 100, Evidence: "Rust forward proxy, TLS inspection, caching and policy enforcement are implemented"},
				{Name: "Policy path decomposition", Status: pg.FeatureInProgress, PlannedDate: d(2026, 10, 20), EffortPoints: 13, ConfidencePct: 85, Evidence: "current architecture work is extracting policy from the growing request path"},
				{Name: "Async analytics backpressure isolation", Status: pg.FeaturePlanned, PlannedDate: d(2026, 11, 20), EffortPoints: 13, ConfidencePct: 75, Evidence: "known scale concern around Kafka/ClickHouse must remain off the hot request path"},
				{Name: "Latency/throughput regression benchmark", Status: pg.FeaturePlanned, PlannedDate: d(2026, 12, 15), EffortPoints: 8, ConfidencePct: 85, Evidence: "planned verification after request-path refactoring"},
			},
		},
		{
			Key: "oko-ra", Name: "Oko-Ra", Type: pg.ProductTypeSecurity, Owner: "risk-analytics",
			Repo: "onixus/Oko-Ra",
			Features: []APEXFeatureTemplate{
				{Name: "World model / scenario graph", Status: pg.FeatureInProgress, PlannedDate: d(2026, 10, 31), EffortPoints: 21, ConfidencePct: 75, Evidence: "canonical ownership: world model and scenario graph"},
				{Name: "Causal risk projections", Status: pg.FeatureInProgress, PlannedDate: d(2026, 11, 30), EffortPoints: 21, ConfidencePct: 70, Evidence: "canonical ownership includes causal/risk projections"},
				{Name: "APEX evidence/event ingestion", Status: pg.FeaturePlanned, PlannedDate: d(2026, 12, 15), EffortPoints: 13, ConfidencePct: 65, Evidence: "integration requires canonical refs, event envelope and provenance"},
				{Name: "Calibration and scenario benchmark", Status: pg.FeaturePlanned, PlannedDate: d(2027, 2, 15), EffortPoints: 21, ConfidencePct: 55, Evidence: "planned measurable validation of risk projections"},
			},
		},
		{
			Key: "pulse", Name: "Pulse", Type: pg.ProductTypeSecurity, Owner: "attack-surface",
			Repo: "onixus/GenDec",
			Features: []APEXFeatureTemplate{
				{Name: "Async SYN/UDP scanner core", Status: pg.FeatureDone, PlannedDate: d(2026, 9, 1), EffortPoints: 21, ConfidencePct: 100, Evidence: "scanner core exists in GenDec/Pulse"},
				{Name: "Accuracy and hot-path refactor", Status: pg.FeatureInProgress, PlannedDate: d(2026, 10, 20), EffortPoints: 13, ConfidencePct: 85, Evidence: "current work targets accuracy/speed architecture bottlenecks"},
				{Name: "Reproducible scan benchmarks", Status: pg.FeatureInProgress, PlannedDate: d(2026, 11, 1), EffortPoints: 8, ConfidencePct: 90, Evidence: "planned before/after benchmark comparison"},
				{Name: "APEX scan observation contract", Status: pg.FeaturePlanned, PlannedDate: d(2026, 11, 30), EffortPoints: 8, ConfidencePct: 80, Evidence: "canonical ownership: scan execution and observations"},
			},
		},
		{
			Key: "asmodeus", Name: "Asmodeus", Type: pg.ProductTypeSecurity, Owner: "security-validation",
			Repo: "onixus/Asmodeus",
			Features: []APEXFeatureTemplate{
				{Name: "BAS exercise engine", Status: pg.FeatureInProgress, PlannedDate: d(2026, 10, 31), EffortPoints: 21, ConfidencePct: 80, Evidence: "product role: synthetic BAS and resilience validation"},
				{Name: "Safety boundaries and constrained execution", Status: pg.FeatureInProgress, PlannedDate: d(2026, 11, 15), EffortPoints: 13, ConfidencePct: 85, Evidence: "architecture work centers explicit safety boundaries"},
				{Name: "Blue Team feedback metrics", Status: pg.FeaturePlanned, PlannedDate: d(2026, 12, 15), EffortPoints: 13, ConfidencePct: 75, Evidence: "canonical ownership includes validation telemetry"},
				{Name: "APEX exercise/event contract", Status: pg.FeaturePlanned, PlannedDate: d(2027, 1, 15), EffortPoints: 8, ConfidencePct: 75, Evidence: "cross-product exercise events need v1 canonical envelope"},
			},
		},
	}
}

// APEX seeds the eight canonical APEX participants and their portfolio
// workstreams. Existing products are preserved; workstreams are created only
// with a newly created product, matching the idempotency model of the legacy
// reference seeds.
func APEX(ctx context.Context, svc *pg.Service, sc authz.Scope) (Result, error) {
	res := Result{Products: map[string]kernel.ID{}, Features: map[string]kernel.ID{}, Contracts: map[string]kernel.ID{}}
	for _, tpl := range APEXProductTemplates() {
		id, created, err := ensureProduct(ctx, svc, sc, pg.ProductInput{
			Key: tpl.Key, Name: tpl.Name, Type: tpl.Type, Owner: tpl.Owner,
			Lifecycle: pg.LifecycleActive, HubManual: tpl.Key == "apex-gateway",
		})
		if err != nil {
			return res, fmt.Errorf("seed APEX %s: %w", tpl.Key, err)
		}
		res.Products[tpl.Key] = id
		if !created {
			continue
		}
		capability, err := svc.CreateCapability(ctx, sc, id, fmt.Sprintf(
			"Portfolio baseline: %d%% complete · %d pts remaining · %d%% confidence",
			tpl.CompletionPct(), tpl.RemainingEffortPoints(), tpl.ConfidencePct(),
		))
		if err != nil {
			return res, fmt.Errorf("seed APEX capability %s: %w", tpl.Key, err)
		}
		for _, f := range tpl.Features {
			createdFeature, err := svc.CreateFeature(ctx, sc, id, pg.FeatureInput{
				CapabilityID: capability.ID,
				Name:         f.Name,
				Status:       f.Status,
				PlannedDate:  f.PlannedDate,
			})
			if err != nil {
				return res, fmt.Errorf("seed APEX feature %s/%s: %w", tpl.Key, f.Name, err)
			}
			res.Features[tpl.Key+"/"+f.Name] = createdFeature.ID
			if _, err := svc.CreateRequirement(ctx, sc, createdFeature.ID,
				fmt.Sprintf("Planning estimate: effort=%d pts; confidence=%d%%. Evidence: %s", f.EffortPoints, f.ConfidencePct, f.Evidence)); err != nil {
				return res, fmt.Errorf("seed APEX estimate %s/%s: %w", tpl.Key, f.Name, err)
			}
		}
	}
	return res, nil
}
