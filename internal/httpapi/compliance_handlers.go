package httpapi

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/compliance"
	"github.com/onixus/metis/internal/httpapi/gen"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
)

// ---- compliance: CM-01…CM-07 ----

func (s *Server) requireCompliance() error {
	if s.d.Compliance == nil {
		return kernel.ErrUnavailable
	}
	return nil
}

func toRequirementSet(rs compliance.RequirementSet) gen.RequirementSet {
	items := make([]gen.RequirementItem, 0, len(rs.Items))
	for _, it := range rs.Items {
		items = append(items, gen.RequirementItem{Key: it.Key, Text: it.Text})
	}
	return gen.RequirementSet{
		Id: rs.ID, Code: rs.Code, Version: rs.Version, ProductType: gen.RequirementSetProductType(rs.ProductType), Items: items,
		Status: gen.RequirementSetStatus(rs.Status), CreatedBy: rs.CreatedBy, CreatedAt: rs.CreatedAt, UpdatedAt: rs.UpdatedAt,
	}
}

func toGateTemplate(g compliance.GateTemplate) gen.GateTemplate {
	checklist := g.Checklist
	if checklist == nil {
		checklist = []string{}
	}
	return gen.GateTemplate{Key: g.Key, Name: g.Name, Kind: gen.GateTemplateKind(g.Kind), Order: g.Order, ParallelGroup: strPtr(g.ParallelGroup), RequirementSetCode: strPtr(g.RequirementSetCode), Checklist: checklist}
}

func toTemplate(t compliance.TrackTemplate) gen.TrackTemplate {
	gates := make([]gen.GateTemplate, 0, len(t.Gates))
	for _, g := range t.Gates {
		gates = append(gates, toGateTemplate(g))
	}
	return gen.TrackTemplate{Id: t.ID, ProductType: gen.TrackTemplateProductType(t.ProductType), Name: t.Name, Gates: gates, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}
}

func toGate(g compliance.Gate) gen.Gate {
	checklist := make([]gen.ChecklistItem, 0, len(g.Checklist))
	for _, it := range g.Checklist {
		checklist = append(checklist, gen.ChecklistItem{Key: it.Key, Text: it.Text, Done: it.Done, EvidenceId: idPtr(it.EvidenceID)})
	}
	out := gen.Gate{
		Id: g.ID, Key: g.Key, Name: g.Name, Kind: gen.GateKind(g.Kind), Order: g.Order, ParallelGroup: strPtr(g.ParallelGroup),
		RequirementSetCode: strPtr(g.RequirementSetCode), Owner: strPtr(g.Owner), DueDate: datePtr(g.DueDate), Cost: money(g.Cost),
		Checklist: checklist, Status: gen.GateStatus(g.Status),
	}
	if !g.PassedAt.IsZero() {
		out.PassedAt = ptr(g.PassedAt)
	}
	return out
}

func toTrack(t compliance.Track) gen.Track {
	gates := make([]gen.Gate, 0, len(t.Gates))
	for _, g := range t.Gates {
		gates = append(gates, toGate(g))
	}
	return gen.Track{
		Id: t.ID, ProductId: t.ProductID, ReleaseId: t.ReleaseID, Version: t.Version, TemplateId: t.TemplateID, Status: gen.TrackStatus(t.Status),
		Gates: gates, BaselineId: idPtr(t.BaselineID), CreatedBy: t.CreatedBy, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}

func toEvidenceItem(e compliance.EvidenceItem) gen.EvidenceItem {
	out := gen.EvidenceItem{
		Seq: e.Seq, Id: e.ID, ProductId: e.ProductID, TrackId: e.TrackID, GateId: e.GateID, Url: e.URL, Sha256: e.SHA256,
		Status: gen.EvidenceItemStatus(e.Status), Comment: strPtr(e.Comment), Actor: e.Actor, At: e.At,
	}
	if e.Supersedes != 0 {
		out.Supersedes = ptr(e.Supersedes)
	}
	return out
}

func toImpact(a compliance.ImpactAssessment) gen.ImpactAssessment {
	return gen.ImpactAssessment{Id: a.ID, FeatureId: a.FeatureID, ProductId: a.ProductID, Class: gen.ImpactAssessmentClass(a.Class), Justification: a.Justification, Author: a.Author, At: a.At}
}

func toBaseline(b compliance.CertifiedBaseline) gen.CertifiedBaseline {
	return gen.CertifiedBaseline{
		Id: b.ID, ProductId: b.ProductID, TrackId: idPtr(b.TrackID), Version: b.Version, RequirementSetId: idPtr(b.RequirementSetID),
		CertificateNo: b.CertificateNo, CertifiedAt: openapi_types.Date{Time: b.CertifiedAt.Time()}, Eol: openapi_types.Date{Time: b.EOL.Time()}, CreatedAt: b.CreatedAt,
	}
}

func toComplianceSettings(st compliance.Settings) gen.ComplianceSettings {
	out := gen.ComplianceSettings{CostByClass: map[string]gen.Money{}, CertifiedProcessDiscount: st.CertifiedProcessDiscount.String(), BaselineLifetimeYears: st.BaselineLifetimeYears}
	for c, m := range st.CostByClass {
		out.CostByClass[string(c)] = money(m)
	}
	return out
}

// ListRequirementSets — CM-01.
func (s *Server) ListRequirementSets(ctx context.Context, req gen.ListRequirementSetsRequestObject) (gen.ListRequirementSetsResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	list, err := s.d.Compliance.RequirementSets(ctx, scope(ctx), strOrEmpty(req.Params.Code))
	if err != nil {
		return nil, err
	}
	out := make(gen.ListRequirementSets200JSONResponse, 0, len(list))
	for _, rs := range list {
		out = append(out, toRequirementSet(rs))
	}
	return out, nil
}

// CreateRequirementSet — CM-01.
func (s *Server) CreateRequirementSet(ctx context.Context, req gen.CreateRequirementSetRequestObject) (gen.CreateRequirementSetResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	in := compliance.RequirementSetInput{Code: req.Body.Code, ProductType: portfoliograph.ProductType(req.Body.ProductType)}
	for _, it := range req.Body.Items {
		in.Items = append(in.Items, compliance.RequirementItem{Key: it.Key, Text: it.Text})
	}
	rs, err := s.d.Compliance.CreateRequirementSet(ctx, scope(ctx), in)
	if err != nil {
		return nil, err
	}
	return gen.CreateRequirementSet201JSONResponse(toRequirementSet(rs)), nil
}

// SetRequirementSetStatus — CM-01.
func (s *Server) SetRequirementSetStatus(ctx context.Context, req gen.SetRequirementSetStatusRequestObject) (gen.SetRequirementSetStatusResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	rs, err := s.d.Compliance.SetRequirementSetStatus(ctx, scope(ctx), req.SetId, compliance.RequirementSetStatus(req.Body.Status))
	if err != nil {
		return nil, err
	}
	return gen.SetRequirementSetStatus200JSONResponse(toRequirementSet(rs)), nil
}

// ListTrackTemplates — CM-02.
func (s *Server) ListTrackTemplates(ctx context.Context, req gen.ListTrackTemplatesRequestObject) (gen.ListTrackTemplatesResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	var pt portfoliograph.ProductType
	if req.Params.ProductType != nil {
		pt = portfoliograph.ProductType(*req.Params.ProductType)
	}
	list, err := s.d.Compliance.Templates(ctx, scope(ctx), pt)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListTrackTemplates200JSONResponse, 0, len(list))
	for _, t := range list {
		out = append(out, toTemplate(t))
	}
	return out, nil
}

// SaveTrackTemplate — CM-02.
func (s *Server) SaveTrackTemplate(ctx context.Context, req gen.SaveTrackTemplateRequestObject) (gen.SaveTrackTemplateResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	t := compliance.TrackTemplate{ID: idOrNil(req.Body.Id), ProductType: portfoliograph.ProductType(req.Body.ProductType), Name: req.Body.Name}
	for _, g := range req.Body.Gates {
		t.Gates = append(t.Gates, compliance.GateTemplate{
			Key: g.Key, Name: g.Name, Kind: compliance.GateKind(g.Kind), Order: g.Order, ParallelGroup: strOrEmpty(g.ParallelGroup),
			RequirementSetCode: strOrEmpty(g.RequirementSetCode), Checklist: g.Checklist,
		})
	}
	saved, err := s.d.Compliance.SaveTemplate(ctx, scope(ctx), t)
	if err != nil {
		return nil, err
	}
	return gen.SaveTrackTemplate200JSONResponse(toTemplate(saved)), nil
}

// ListTracks — CM-03.
func (s *Server) ListTracks(ctx context.Context, req gen.ListTracksRequestObject) (gen.ListTracksResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	list, err := s.d.Compliance.Tracks(ctx, scope(ctx), req.ProductId)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListTracks200JSONResponse, 0, len(list))
	for _, t := range list {
		out = append(out, toTrack(t))
	}
	return out, nil
}

// StartTrack — CM-03.
func (s *Server) StartTrack(ctx context.Context, req gen.StartTrackRequestObject) (gen.StartTrackResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	t, err := s.d.Compliance.StartTrack(ctx, scope(ctx), compliance.TrackInput{ProductID: req.ProductId, ReleaseID: req.Body.ReleaseId, Version: req.Body.Version, TemplateID: idOrNil(req.Body.TemplateId)})
	if err != nil {
		return nil, err
	}
	return gen.StartTrack201JSONResponse(toTrack(t)), nil
}

// GetTrack — трек.
func (s *Server) GetTrack(ctx context.Context, req gen.GetTrackRequestObject) (gen.GetTrackResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	t, err := s.d.Compliance.Track(ctx, scope(ctx), req.TrackId)
	if err != nil {
		return nil, err
	}
	return gen.GetTrack200JSONResponse(toTrack(t)), nil
}

// UpdateGate — CM-03.
func (s *Server) UpdateGate(ctx context.Context, req gen.UpdateGateRequestObject) (gen.UpdateGateResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	t, err := s.d.Compliance.UpdateGate(ctx, scope(ctx), req.TrackId, req.GateId, compliance.GateUpdate{Owner: strOrEmpty(req.Body.Owner), DueDate: dateOf(req.Body.DueDate), Cost: moneyOf(req.Body.Cost)})
	if err != nil {
		return nil, err
	}
	return gen.UpdateGate200JSONResponse(toTrack(t)), nil
}

// CheckGateItem — CM-03, CM-04.
func (s *Server) CheckGateItem(ctx context.Context, req gen.CheckGateItemRequestObject) (gen.CheckGateItemResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	t, err := s.d.Compliance.CheckItem(ctx, scope(ctx), req.TrackId, req.GateId, req.Body.Key, req.Body.EvidenceId)
	if err != nil {
		return nil, err
	}
	return gen.CheckGateItem200JSONResponse(toTrack(t)), nil
}

// PassGate — CM-03, CM-07.
func (s *Server) PassGate(ctx context.Context, req gen.PassGateRequestObject) (gen.PassGateResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	t, err := s.d.Compliance.PassGate(ctx, scope(ctx), req.TrackId, req.GateId)
	if err != nil {
		return nil, err
	}
	return gen.PassGate200JSONResponse(toTrack(t)), nil
}

// FailGate — CM-03.
func (s *Server) FailGate(ctx context.Context, req gen.FailGateRequestObject) (gen.FailGateResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	t, err := s.d.Compliance.FailGate(ctx, scope(ctx), req.TrackId, req.GateId, req.Body.Reason)
	if err != nil {
		return nil, err
	}
	return gen.FailGate200JSONResponse(toTrack(t)), nil
}

// ListTrackEvidence — CM-04.
func (s *Server) ListTrackEvidence(ctx context.Context, req gen.ListTrackEvidenceRequestObject) (gen.ListTrackEvidenceResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	list, err := s.d.Compliance.Evidence(ctx, scope(ctx), req.TrackId)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListTrackEvidence200JSONResponse, 0, len(list))
	for _, e := range list {
		out = append(out, toEvidenceItem(e))
	}
	return out, nil
}

// AppendTrackEvidence — CM-04.
func (s *Server) AppendTrackEvidence(ctx context.Context, req gen.AppendTrackEvidenceRequestObject) (gen.AppendTrackEvidenceResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	e, err := s.d.Compliance.AppendEvidence(ctx, scope(ctx), compliance.EvidenceInput{TrackID: req.TrackId, GateID: req.Body.GateId, URL: req.Body.Url, SHA256: req.Body.Sha256, Comment: strOrEmpty(req.Body.Comment)})
	if err != nil {
		return nil, err
	}
	return gen.AppendTrackEvidence201JSONResponse(toEvidenceItem(e)), nil
}

// SetEvidenceItemStatus — CM-04.
func (s *Server) SetEvidenceItemStatus(ctx context.Context, req gen.SetEvidenceItemStatusRequestObject) (gen.SetEvidenceItemStatusResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	e, err := s.d.Compliance.SetEvidenceStatus(ctx, scope(ctx), req.EvidenceId, compliance.EvidenceStatus(req.Body.Status), strOrEmpty(req.Body.Comment))
	if err != nil {
		return nil, err
	}
	return gen.SetEvidenceItemStatus200JSONResponse(toEvidenceItem(e)), nil
}

// VerifyEvidenceLog — CM-04.
func (s *Server) VerifyEvidenceLog(ctx context.Context, _ gen.VerifyEvidenceLogRequestObject) (gen.VerifyEvidenceLogResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	res, err := s.d.Compliance.VerifyEvidence(ctx, scope(ctx))
	if err != nil {
		return nil, err
	}
	out := gen.AuditVerifyResult{Checked: res.Checked, Ok: res.OK, Reason: strPtr(res.Reason)}
	if res.BrokenSeq != 0 {
		out.BrokenSeq = ptr(res.BrokenSeq)
	}
	return gen.VerifyEvidenceLog200JSONResponse(out), nil
}

// GetReleaseReadiness — CM-05. Права проверяются по продукту релиза до расчёта готовности:
// у релиза может не быть трека, и тогда проверка готовности отвечает «не готов» без обращения
// к правам — субъект без доступа к продукту отличал бы чужой релиз без трека от релиза с треком.
func (s *Server) GetReleaseReadiness(ctx context.Context, req gen.GetReleaseReadinessRequestObject) (gen.GetReleaseReadinessResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	if err := s.requireRoadmap(); err != nil {
		return nil, err
	}
	if _, err := s.d.Roadmap.Release(ctx, scope(ctx), req.ReleaseId); err != nil {
		return nil, err
	}
	r, err := s.d.Compliance.ReleaseReadiness(ctx, scope(ctx), req.ReleaseId)
	if err != nil {
		return nil, err
	}
	items := r.OpenItems
	if items == nil {
		items = []string{}
	}
	return gen.GetReleaseReadiness200JSONResponse{Ready: r.Ready, OpenItems: items}, nil
}

// GetFeatureImpact — CM-06.
func (s *Server) GetFeatureImpact(ctx context.Context, req gen.GetFeatureImpactRequestObject) (gen.GetFeatureImpactResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	a, err := s.d.Compliance.ImpactClass(ctx, scope(ctx), req.FeatureId)
	if err != nil {
		return nil, err
	}
	return gen.GetFeatureImpact200JSONResponse(toImpact(a)), nil
}

// SetFeatureImpact — CM-06. Продукт берётся из фичи.
func (s *Server) SetFeatureImpact(ctx context.Context, req gen.SetFeatureImpactRequestObject) (gen.SetFeatureImpactResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	f, err := s.d.Portfolio.Feature(ctx, scope(ctx), req.FeatureId)
	if err != nil {
		return nil, err
	}
	a, err := s.d.Compliance.SetImpactClass(ctx, scope(ctx), req.FeatureId, f.ProductID, compliance.ImpactClass(req.Body.Class), req.Body.Justification)
	if err != nil {
		return nil, err
	}
	return gen.SetFeatureImpact200JSONResponse(toImpact(a)), nil
}

// GetFeatureImpactHistory — CM-06.
func (s *Server) GetFeatureImpactHistory(ctx context.Context, req gen.GetFeatureImpactHistoryRequestObject) (gen.GetFeatureImpactHistoryResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	list, err := s.d.Compliance.ImpactHistory(ctx, scope(ctx), req.FeatureId)
	if err != nil {
		return nil, err
	}
	out := make(gen.GetFeatureImpactHistory200JSONResponse, 0, len(list))
	for _, a := range list {
		out = append(out, toImpact(a))
	}
	return out, nil
}

// GetAffectedBaselines — CM-07.
func (s *Server) GetAffectedBaselines(ctx context.Context, req gen.GetAffectedBaselinesRequestObject) (gen.GetAffectedBaselinesResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	list, err := s.d.Compliance.AffectedBaselines(ctx, scope(ctx), req.FeatureId)
	if err != nil {
		return nil, err
	}
	out := make(gen.GetAffectedBaselines200JSONResponse, 0, len(list))
	for _, ab := range list {
		out = append(out, gen.AffectedBaseline{Baseline: toBaseline(ab.Baseline), Path: ids(ab.Path), Procedure: gen.AffectedBaselineProcedure(ab.Procedure)})
	}
	return out, nil
}

// ListBaselines — CM-07.
func (s *Server) ListBaselines(ctx context.Context, req gen.ListBaselinesRequestObject) (gen.ListBaselinesResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	list, err := s.d.Compliance.Baselines(ctx, scope(ctx), req.ProductId)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListBaselines200JSONResponse, 0, len(list))
	for _, b := range list {
		out = append(out, toBaseline(b))
	}
	return out, nil
}

// GetComplianceSettings — настройки compliance; чтение доступно любому аутентифицированному субъекту.
func (s *Server) GetComplianceSettings(ctx context.Context, _ gen.GetComplianceSettingsRequestObject) (gen.GetComplianceSettingsResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	if !scope(ctx).Valid() {
		return nil, kernel.ErrForbidden
	}
	st, err := s.d.Compliance.Settings(ctx, scope(ctx))
	if err != nil {
		return nil, err
	}
	return gen.GetComplianceSettings200JSONResponse(toComplianceSettings(st)), nil
}

// UpdateComplianceSettings — настройки compliance (admin).
func (s *Server) UpdateComplianceSettings(ctx context.Context, req gen.UpdateComplianceSettingsRequestObject) (gen.UpdateComplianceSettingsResponseObject, error) {
	if err := s.requireCompliance(); err != nil {
		return nil, err
	}
	disc, err := decimal.NewFromString(req.Body.CertifiedProcessDiscount)
	if err != nil {
		return nil, kernel.Invalid("certified_process_discount", "ожидается десятичное число строкой")
	}
	st := compliance.Settings{CostByClass: map[compliance.ImpactClass]kernel.Money{}, CertifiedProcessDiscount: disc, BaselineLifetimeYears: req.Body.BaselineLifetimeYears}
	for c, m := range req.Body.CostByClass {
		st.CostByClass[compliance.ImpactClass(c)] = kernel.Money{Amount: m.Amount, Currency: m.Currency}
	}
	if err := s.d.Compliance.UpdateSettings(ctx, scope(ctx), st); err != nil {
		return nil, err
	}
	if s.d.Audit != nil {
		if _, err := s.d.Audit.Append(ctx, audit.Entry{Actor: scope(ctx).Subject(), Action: audit.ActionRuleChange, ObjectType: "compliance_settings", Details: map[string]any{"settings": st}}); err != nil {
			return nil, err
		}
	}
	cur, err := s.d.Compliance.Settings(ctx, scope(ctx))
	if err != nil {
		return nil, err
	}
	return gen.UpdateComplianceSettings200JSONResponse(toComplianceSettings(cur)), nil
}
