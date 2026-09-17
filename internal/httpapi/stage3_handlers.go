package httpapi

import (
	"context"
	"strconv"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/onixus/metis/internal/analytics"
	"github.com/onixus/metis/internal/compliance"
	"github.com/onixus/metis/internal/delivery"
	"github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/httpapi/gen"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/licensing"
	"github.com/onixus/metis/internal/marketing"
)

// ---- сценарии (DA-02, EC-13) ----

func toScenario(sn economics.Scenario) gen.Scenario {
	overrides := make([]gen.ScenarioOverride, 0, len(sn.Overrides))
	for _, o := range sn.Overrides {
		period := o.Period.String()
		overrides = append(overrides, gen.ScenarioOverride{FieldKey: o.FieldKey, ProductId: idPtr(o.ProductID),
			Period: &period, Value: decStr(o.Value)})
	}
	products := append([]kernel.ID(nil), sn.Products...)
	budget := money(sn.BudgetDelta)
	shift, desc, actor, created, updated := sn.CapacityShiftDays, sn.Description, sn.Actor, sn.CreatedAt, sn.UpdatedAt
	return gen.Scenario{Id: sn.ID, Name: sn.Name, Description: &desc, Period: sn.Period.String(),
		Products: &products, Overrides: &overrides, CapacityShiftDays: &shift, BudgetDelta: &budget,
		Actor: &actor, CreatedAt: &created, UpdatedAt: &updated}
}

// ListScenarios — DA-02.
func (s *Server) ListScenarios(ctx context.Context, _ gen.ListScenariosRequestObject) (gen.ListScenariosResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	list, err := s.d.Economics.Scenarios(ctx, scope(ctx))
	if err != nil {
		return nil, err
	}
	out := make(gen.ListScenarios200JSONResponse, 0, len(list))
	for _, sn := range list {
		out = append(out, toScenario(sn))
	}
	return out, nil
}

// SaveScenario — DA-02, EC-13.
func (s *Server) SaveScenario(ctx context.Context, req gen.SaveScenarioRequestObject) (gen.SaveScenarioResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	b := req.Body
	period, err := economics.ParsePeriod(b.Period)
	if err != nil {
		return nil, err
	}
	sn := economics.Scenario{ID: idOrNil(b.Id), Name: b.Name, Description: strOrEmpty(b.Description),
		Period: period, CapacityShiftDays: intOr(b.CapacityShiftDays), BudgetDelta: moneyOf(b.BudgetDelta)}
	if b.Products != nil {
		sn.Products = append(sn.Products, *b.Products...)
	}
	if b.Overrides != nil {
		for _, o := range *b.Overrides {
			value, err := parseDec(o.Value)
			if err != nil {
				return nil, err
			}
			op, err := periodOf(o.Period)
			if err != nil {
				return nil, err
			}
			sn.Overrides = append(sn.Overrides, economics.Override{FieldKey: o.FieldKey,
				ProductID: idOrNil(o.ProductId), Period: op, Value: value})
		}
	}
	saved, err := s.d.Economics.SaveScenario(ctx, scope(ctx), sn)
	if err != nil {
		return nil, err
	}
	return gen.SaveScenario200JSONResponse(toScenario(saved)), nil
}

// RunScenario — DA-02: влияние сценария на показатели, обязательства и треки.
func (s *Server) RunScenario(ctx context.Context, req gen.RunScenarioRequestObject) (gen.RunScenarioResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	var metrics []string
	if req.Body != nil && req.Body.Metrics != nil {
		metrics = *req.Body.Metrics
	}
	res, err := s.d.Economics.RunScenario(ctx, scope(ctx), req.ScenarioId, metrics)
	if err != nil {
		return nil, err
	}
	pnl := make([]gen.PnL, 0, len(res.PnL))
	for _, p := range res.PnL {
		pnl = append(pnl, toPnL(p))
	}
	basePnL := make([]gen.PnL, 0, len(res.BasePnL))
	for _, p := range res.BasePnL {
		basePnL = append(basePnL, toPnL(p))
	}
	impacts := make([]gen.CommitmentImpact, 0, len(res.Commitments))
	for _, c := range res.Commitments {
		item := gen.CommitmentImpact{Breached: c.Breached}
		reason := c.Reason
		item.Reason = &reason
		id, product, title, regulatory := c.Commitment.ID, c.Commitment.ProductID, c.Commitment.Title, c.Commitment.Regulatory
		item.Commitment.Id, item.Commitment.ProductId = &id, &product
		item.Commitment.Title, item.Commitment.Regulatory = &title, &regulatory
		item.Commitment.DueDate = datePtr(c.Commitment.DueDate)
		impacts = append(impacts, item)
	}
	tracks := make([]gen.TrackImpact, 0, len(res.Tracks))
	for _, t := range res.Tracks {
		item := gen.TrackImpact{AtRisk: t.AtRisk}
		reason, shortage := t.Reason, money(t.Shortage)
		item.Reason, item.Shortage = &reason, &shortage
		id, product, name, cost := t.Track.ID, t.Track.ProductID, t.Track.Name, money(t.Track.PlannedCost)
		item.Track.Id, item.Track.ProductId = &id, &product
		item.Track.Name, item.Track.PlannedCost = &name, &cost
		item.Track.Deadline = datePtr(t.Track.Deadline)
		tracks = append(tracks, item)
	}
	metricsOut, baseOut := res.Metrics, res.BaseMetrics
	return gen.RunScenario200JSONResponse{Scenario: toScenario(res.Scenario), Metrics: &metricsOut,
		BaseMetrics: &baseOut, Pnl: &pnl, BasePnl: &basePnL, Commitments: &impacts, Tracks: &tracks}, nil
}

// ---- маркетинг (DA-04) ----

// GetWinLoss — DA-04.
func (s *Server) GetWinLoss(ctx context.Context, req gen.GetWinLossRequestObject) (gen.GetWinLossResponseObject, error) {
	if s.d.Marketing == nil {
		return nil, kernel.ErrUnavailable
	}
	rep, err := s.d.Marketing.WinLoss(ctx, scope(ctx), marketing.Filter{
		ProductKey: strOrEmpty(req.Params.ProductKey), From: dateOf(req.Params.From), To: dateOf(req.Params.To)})
	if err != nil {
		return nil, err
	}
	counters := func(in []marketing.Counter) *[]gen.WinLossCounter {
		out := make([]gen.WinLossCounter, 0, len(in))
		for _, c := range in {
			amount := money(c.Amount)
			out = append(out, gen.WinLossCounter{Key: c.Key, Won: c.Won, Lost: c.Lost, Amount: &amount})
		}
		return &out
	}
	features := make([]struct {
		Amount  *gen.Money `json:"amount,omitempty"`
		Deals   *int       `json:"deals,omitempty"`
		Feature *string    `json:"feature,omitempty"`
	}, 0, len(rep.Features))
	for _, f := range rep.Features {
		amount, deals, name := money(f.Amount), f.Deals, f.Feature
		features = append(features, struct {
			Amount  *gen.Money `json:"amount,omitempty"`
			Deals   *int       `json:"deals,omitempty"`
			Feature *string    `json:"feature,omitempty"`
		}{Amount: &amount, Deals: &deals, Feature: &name})
	}
	attach := make([]struct {
		Deals      *int    `json:"deals,omitempty"`
		ProductKey *string `json:"product_key,omitempty"`
		Rate       *string `json:"rate,omitempty"`
	}, 0, len(rep.Attach))
	for _, a := range rep.Attach {
		deals, key, rate := a.Deals, a.ProductKey, a.Rate
		attach = append(attach, struct {
			Deals      *int    `json:"deals,omitempty"`
			ProductKey *string `json:"product_key,omitempty"`
			Rate       *string `json:"rate,omitempty"`
		}{Deals: &deals, ProductKey: &key, Rate: &rate})
	}
	productKey, wonAmount, lostAmount := rep.ProductKey, money(rep.WonAmount), money(rep.LostAmount)
	return gen.GetWinLoss200JSONResponse{ProductKey: &productKey, Won: rep.Won, Lost: rep.Lost,
		WonAmount: &wonAmount, LostAmount: &lostAmount, ByReason: counters(rep.ByReason),
		BySegment: counters(rep.BySegment), Features: &features, AttachRate: &attach}, nil
}

// ---- roadmap RM-06 ----

// GetLaunchCalendar — RM-06.
func (s *Server) GetLaunchCalendar(ctx context.Context, req gen.GetLaunchCalendarRequestObject) (gen.GetLaunchCalendarResponseObject, error) {
	if err := s.requireRoadmap(); err != nil {
		return nil, err
	}
	cal, err := s.d.Roadmap.LaunchCalendar(ctx, scope(ctx), req.ProductId, dateOf(req.Params.From), dateOf(req.Params.To))
	if err != nil {
		return nil, err
	}
	entries := make([]gen.LaunchEntry, 0, len(cal.Entries))
	for _, e := range cal.Entries {
		audience := gen.LaunchEntryAudience(e.Audience)
		entries = append(entries, gen.LaunchEntry{ItemId: e.ItemID, ProductId: e.ProductID, Title: e.Title,
			Tier: gen.LaunchEntryTier(e.Tier), LaunchDate: datePtr(e.LaunchDate),
			ReleaseId: idPtr(e.ReleaseID), Audience: &audience})
	}
	return gen.GetLaunchCalendar200JSONResponse{From: datePtr(cal.From), To: datePtr(cal.To),
		Audience: gen.LaunchCalendarAudience(cal.Audience), Entries: entries}, nil
}

// ---- delivery DL-04, DL-05 ----

func (s *Server) requireDelivery() error {
	if s.d.Delivery == nil {
		return kernel.ErrUnavailable
	}
	return nil
}

// GetFeatureForecast — DL-04.
func (s *Server) GetFeatureForecast(ctx context.Context, req gen.GetFeatureForecastRequestObject) (gen.GetFeatureForecastResponseObject, error) {
	if err := s.requireDelivery(); err != nil {
		return nil, err
	}
	opts := delivery.ForecastOptions{Samples: intOr(req.Params.Samples)}
	if req.Params.Seed != nil && *req.Params.Seed != "" {
		seed, err := strconv.ParseUint(*req.Params.Seed, 10, 64)
		if err != nil {
			return nil, kernel.Invalid("seed", "зерно генератора — целое число")
		}
		opts.Seed = seed
	}
	fc, err := s.d.Delivery.Forecast(ctx, scope(ctx), req.FeatureId, opts)
	if err != nil {
		return nil, err
	}
	epic, iterations, samples := fc.EpicKey, fc.IterationDays, fc.Samples
	throughput := append([]int(nil), fc.Throughput...)
	return gen.GetFeatureForecast200JSONResponse{FeatureId: fc.FeatureID, ProductId: fc.ProductID,
		EpicKey: &epic, Remaining: fc.Remaining, Throughput: &throughput, IterationDays: &iterations,
		Samples: &samples, From: datePtr(fc.From), P50: datePtr(fc.P50), P85: datePtr(fc.P85),
		P95: datePtr(fc.P95), IntervalLow: datePtr(fc.IntervalLow), IntervalHigh: datePtr(fc.IntervalHigh)}, nil
}

// GetWorklogCostBase — DL-05.
func (s *Server) GetWorklogCostBase(ctx context.Context, req gen.GetWorklogCostBaseRequestObject) (gen.GetWorklogCostBaseResponseObject, error) {
	if err := s.requireDelivery(); err != nil {
		return nil, err
	}
	from, to := time.Time{}, time.Time{}
	if req.Params.From != nil {
		from = *req.Params.From
	}
	if req.Params.To != nil {
		to = *req.Params.To
	}
	rep, err := s.d.Delivery.WorklogCostBase(ctx, scope(ctx), from, to)
	if err != nil {
		return nil, err
	}
	conv := func(in []delivery.WorklogShare) *[]gen.WorklogShare {
		out := make([]gen.WorklogShare, 0, len(in))
		for _, sh := range in {
			epic := sh.EpicKey
			out = append(out, gen.WorklogShare{ProductId: sh.ProductID, FeatureId: idPtr(sh.FeatureID),
				EpicKey: &epic, Seconds: sh.Seconds, Share: decStr(sh.Share)})
		}
		return &out
	}
	f, t := rep.From, rep.To
	return gen.GetWorklogCostBase200JSONResponse{From: &f, To: &t, TotalSeconds: rep.TotalSeconds,
		ByProduct: conv(rep.ByProduct), ByFeature: conv(rep.ByFeature)}, nil
}

// ---- дашборды DA-05 ----

func toDashboard(d analytics.Dashboard) gen.Dashboard {
	panels := make([]gen.DashboardPanel, 0, len(d.Panels))
	for _, p := range d.Panels {
		params, width := p.Params, p.Width
		panels = append(panels, gen.DashboardPanel{Key: p.Key, Title: p.Title, Source: string(p.Source),
			Kind: gen.DashboardPanelKind(p.Kind), Params: &params, Width: &width})
	}
	shared, created, updated := d.Shared, d.CreatedAt, d.UpdatedAt
	return gen.Dashboard{Id: d.ID, ProductId: idPtr(d.ProductID), Name: d.Name, Owner: d.Owner,
		Shared: &shared, Panels: panels, CreatedAt: &created, UpdatedAt: &updated}
}

func (s *Server) requireDashboards() error {
	if s.d.Analytics == nil {
		return kernel.ErrUnavailable
	}
	return nil
}

// ListDashboards — DA-05.
func (s *Server) ListDashboards(ctx context.Context, _ gen.ListDashboardsRequestObject) (gen.ListDashboardsResponseObject, error) {
	if err := s.requireDashboards(); err != nil {
		return nil, err
	}
	list, err := s.d.Analytics.List(ctx, scope(ctx))
	if err != nil {
		return nil, err
	}
	out := make(gen.ListDashboards200JSONResponse, 0, len(list))
	for _, d := range list {
		out = append(out, toDashboard(d))
	}
	return out, nil
}

// SaveDashboard — DA-05.
func (s *Server) SaveDashboard(ctx context.Context, req gen.SaveDashboardRequestObject) (gen.SaveDashboardResponseObject, error) {
	if err := s.requireDashboards(); err != nil {
		return nil, err
	}
	in := analytics.Input{ID: idOrNil(req.Body.Id), ProductID: idOrNil(req.Body.ProductId),
		Name: req.Body.Name, Shared: boolOr(req.Body.Shared)}
	for _, p := range req.Body.Panels {
		panel := analytics.Panel{Key: p.Key, Title: p.Title, Source: analytics.Source(p.Source),
			Kind: analytics.Kind(p.Kind), Width: intOr(p.Width)}
		if p.Params != nil {
			panel.Params = *p.Params
		}
		in.Panels = append(in.Panels, panel)
	}
	d, err := s.d.Analytics.Save(ctx, scope(ctx), in)
	if err != nil {
		return nil, err
	}
	return gen.SaveDashboard200JSONResponse(toDashboard(d)), nil
}

// ListDashboardSources — DA-05.
func (s *Server) ListDashboardSources(_ context.Context, _ gen.ListDashboardSourcesRequestObject) (gen.ListDashboardSourcesResponseObject, error) {
	if err := s.requireDashboards(); err != nil {
		return nil, err
	}
	sources := analytics.Sources()
	out := make(gen.ListDashboardSources200JSONResponse, 0, len(sources))
	for _, src := range sources {
		out = append(out, string(src))
	}
	return out, nil
}

// GetDashboard — DA-05.
func (s *Server) GetDashboard(ctx context.Context, req gen.GetDashboardRequestObject) (gen.GetDashboardResponseObject, error) {
	if err := s.requireDashboards(); err != nil {
		return nil, err
	}
	d, err := s.d.Analytics.Get(ctx, scope(ctx), req.DashboardId)
	if err != nil {
		return nil, err
	}
	return gen.GetDashboard200JSONResponse(toDashboard(d)), nil
}

// DeleteDashboard — DA-05.
func (s *Server) DeleteDashboard(ctx context.Context, req gen.DeleteDashboardRequestObject) (gen.DeleteDashboardResponseObject, error) {
	if err := s.requireDashboards(); err != nil {
		return nil, err
	}
	if err := s.d.Analytics.Delete(ctx, scope(ctx), req.DashboardId); err != nil {
		return nil, err
	}
	return gen.DeleteDashboard204Response{}, nil
}

// ---- лицензия AD-06 ----

func (s *Server) requireLicensing() error {
	if s.d.Licensing == nil {
		return kernel.ErrUnavailable
	}
	return nil
}

// GetLicenseStatus — AD-06.
func (s *Server) GetLicenseStatus(ctx context.Context, _ gen.GetLicenseStatusRequestObject) (gen.GetLicenseStatusResponseObject, error) {
	if err := s.requireLicensing(); err != nil {
		return nil, err
	}
	st, err := s.d.Licensing.Status(ctx, scope(ctx))
	if err != nil {
		return nil, err
	}
	return gen.GetLicenseStatus200JSONResponse(toLicenseStatus(st)), nil
}

// InstallLicense — AD-06.
func (s *Server) InstallLicense(ctx context.Context, req gen.InstallLicenseRequestObject) (gen.InstallLicenseResponseObject, error) {
	if err := s.requireLicensing(); err != nil {
		return nil, err
	}
	st, err := s.d.Licensing.Install(ctx, scope(ctx), req.Body.Key)
	if err != nil {
		return nil, err
	}
	return gen.InstallLicense200JSONResponse(toLicenseStatus(st)), nil
}

// toLicenseStatus переводит состояние лицензии в ответ API (AD-06).
func toLicenseStatus(st licensing.Status) gen.LicenseStatus {
	expired, grace, days, reason := st.Expired, st.InGrace, st.DaysLeft, st.Reason
	out := gen.LicenseStatus{Installed: st.Installed, Valid: st.Valid, Expired: &expired,
		InGrace: &grace, DaysLeft: &days, Reason: &reason}
	if !st.Installed {
		return out
	}
	id, customer, edition := st.License.ID, st.License.Customer, st.License.Edition
	products, users := st.License.Limits.Products, st.License.Limits.Users
	modules := append([]string(nil), st.License.Modules...)
	out.License = &struct {
		Customer *string             `json:"customer,omitempty"`
		Edition  *string             `json:"edition,omitempty"`
		Id       *string             `json:"id,omitempty"`
		IssuedAt *openapi_types.Date `json:"issued_at,omitempty"`
		Limits   *struct {
			Products *int `json:"products,omitempty"`
			Users    *int `json:"users,omitempty"`
		} `json:"limits,omitempty"`
		Modules   *[]string           `json:"modules,omitempty"`
		NotAfter  *openapi_types.Date `json:"not_after,omitempty"`
		NotBefore *openapi_types.Date `json:"not_before,omitempty"`
	}{
		Customer: &customer, Edition: &edition, Id: &id,
		IssuedAt: datePtr(st.License.IssuedAt),
		Limits: &struct {
			Products *int `json:"products,omitempty"`
			Users    *int `json:"users,omitempty"`
		}{Products: &products, Users: &users},
		Modules: &modules, NotAfter: datePtr(st.License.NotAfter), NotBefore: datePtr(st.License.NotBefore),
	}
	return out
}

// ---- CM-08, CM-09, DA-06 поверх модулей этапа 2 ----

// SetBaselineComponents — состав компонентов сертифицированной версии (CM-08).
func (s *Server) SetBaselineComponents(ctx context.Context, req gen.SetBaselineComponentsRequestObject) (gen.SetBaselineComponentsResponseObject, error) {
	if s.d.Compliance == nil {
		return nil, kernel.ErrUnavailable
	}
	components := make([]compliance.Component, 0, len(req.Body.Components))
	for _, c := range req.Body.Components {
		components = append(components, compliance.Component{Key: c.Key, Version: strOrEmpty(c.Version)})
	}
	b, err := s.d.Compliance.SetBaselineComponents(ctx, scope(ctx), req.BaselineId, components)
	if err != nil {
		return nil, err
	}
	return gen.SetBaselineComponents200JSONResponse(toBaseline(b)), nil
}

// ReportVulnerableComponent — затронутые сертифицированные версии и запуск сроков (CM-08).
func (s *Server) ReportVulnerableComponent(ctx context.Context, req gen.ReportVulnerableComponentRequestObject) (gen.ReportVulnerableComponentResponseObject, error) {
	if s.d.Compliance == nil {
		return nil, kernel.ErrUnavailable
	}
	impact, err := s.d.Compliance.ReportVulnerableComponent(ctx, scope(ctx),
		compliance.Component{Key: req.Body.Component.Key, Version: strOrEmpty(req.Body.Component.Version)},
		compliance.Severity(req.Body.Severity))
	if err != nil {
		return nil, err
	}
	baselines := make([]gen.CertifiedBaseline, 0, len(impact.Baselines))
	for _, b := range impact.Baselines {
		baselines = append(baselines, toBaseline(b))
	}
	deadlines := make([]struct {
		BaselineId   *openapi_types.UUID `json:"baseline_id,omitempty"`
		CommitmentId *openapi_types.UUID `json:"commitment_id,omitempty"`
		DueDate      *openapi_types.Date `json:"due_date,omitempty"`
		ProductId    *openapi_types.UUID `json:"product_id,omitempty"`
		Version      *string             `json:"version,omitempty"`
	}, 0, len(impact.Deadlines))
	for _, d := range impact.Deadlines {
		baseline, commitment, product, version := d.BaselineID, d.CommitmentID, d.ProductID, d.Version
		deadlines = append(deadlines, struct {
			BaselineId   *openapi_types.UUID `json:"baseline_id,omitempty"`
			CommitmentId *openapi_types.UUID `json:"commitment_id,omitempty"`
			DueDate      *openapi_types.Date `json:"due_date,omitempty"`
			ProductId    *openapi_types.UUID `json:"product_id,omitempty"`
			Version      *string             `json:"version,omitempty"`
		}{BaselineId: &baseline, CommitmentId: &commitment, DueDate: datePtr(d.DueDate), ProductId: &product, Version: &version})
	}
	version := impact.Component.Version
	return gen.ReportVulnerableComponent200JSONResponse{
		Component: gen.Component{Key: impact.Component.Key, Version: &version},
		Severity:  string(impact.Severity), Baselines: &baselines, Deadlines: &deadlines,
		ReportedAt: datePtr(impact.ReportedAt),
	}, nil
}

// CollectPipelineEvidence — автосбор доказательств из пайплайна безопасности (CM-09).
func (s *Server) CollectPipelineEvidence(ctx context.Context, req gen.CollectPipelineEvidenceRequestObject) (gen.CollectPipelineEvidenceResponseObject, error) {
	if s.d.Compliance == nil {
		return nil, kernel.ErrUnavailable
	}
	since := time.Time{}
	if req.Body.Since != nil {
		since = *req.Body.Since
	}
	res, err := s.d.Compliance.CollectPipelineEvidence(ctx, scope(ctx), req.TrackId, req.GateId, req.Body.Project, since)
	if err != nil {
		return nil, err
	}
	collected := make([]gen.EvidenceItem, 0, len(res.Collected))
	for _, e := range res.Collected {
		collected = append(collected, toEvidenceItem(e))
	}
	sbom := make([]gen.Component, 0, len(res.SBOM))
	for _, c := range res.SBOM {
		version := c.Version
		sbom = append(sbom, gen.Component{Key: c.Key, Version: &version})
	}
	skipped, saved := res.Skipped, res.ComponentsSaved
	return gen.CollectPipelineEvidence200JSONResponse{TrackId: res.TrackID, GateId: res.GateID,
		Collected: &collected, Skipped: &skipped, ComponentsSaved: &saved, Sbom: &sbom}, nil
}

// ListDecisionsDueForReview — решения, которым пора ревизию (DA-06).
func (s *Server) ListDecisionsDueForReview(ctx context.Context, _ gen.ListDecisionsDueForReviewRequestObject) (gen.ListDecisionsDueForReviewResponseObject, error) {
	if s.d.Decisions == nil {
		return nil, kernel.ErrUnavailable
	}
	list, err := s.d.Decisions.DueForReview(ctx, scope(ctx), kernel.NilID)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListDecisionsDueForReview200JSONResponse, 0, len(list))
	for _, d := range list {
		out = append(out, toDecision(d))
	}
	return out, nil
}

// ReviewDecision — ревизия решения: ожидание против факта (DA-06).
func (s *Server) ReviewDecision(ctx context.Context, req gen.ReviewDecisionRequestObject) (gen.ReviewDecisionResponseObject, error) {
	if s.d.Decisions == nil {
		return nil, kernel.ErrUnavailable
	}
	comment := ""
	if req.Body != nil && req.Body.Comment != nil {
		comment = *req.Body.Comment
	}
	d, err := s.d.Decisions.ReviewDecision(ctx, scope(ctx), req.DecisionId, comment)
	if err != nil {
		return nil, err
	}
	return gen.ReviewDecision200JSONResponse(toDecision(d)), nil
}
