package httpapi

import (
	"context"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/httpapi/gen"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/licensing"
	pg "github.com/onixus/metis/internal/portfoliograph"
)

// ---- Вспомогательные преобразования ----

func ptr[T any](v T) *T { return &v }

func strOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func boolOr(p *bool) bool { return p != nil && *p }

func idOrNil(p *openapi_types.UUID) kernel.ID {
	if p == nil {
		return kernel.NilID
	}
	return *p
}

func idPtr(id kernel.ID) *openapi_types.UUID {
	if id == kernel.NilID {
		return nil
	}
	return ptr(openapi_types.UUID(id))
}

func datePtr(d kernel.Date) *openapi_types.Date {
	if d.IsZero() {
		return nil
	}
	return &openapi_types.Date{Time: d.Time()}
}

func dateOf(p *openapi_types.Date) kernel.Date {
	if p == nil {
		return kernel.Date{}
	}
	return kernel.DateFromTime(p.Time)
}

func money(m kernel.Money) gen.Money { return gen.Money{Amount: m.Amount, Currency: m.Currency} }

func ids(in []kernel.ID) []openapi_types.UUID {
	out := make([]openapi_types.UUID, 0, len(in))
	out = append(out, in...)
	return out
}

func kids(in *[]openapi_types.UUID) []kernel.ID {
	if in == nil {
		return nil
	}
	out := make([]kernel.ID, 0, len(*in))
	out = append(out, *in...)
	return out
}

func toProduct(p pg.Product) gen.Product {
	return gen.Product{
		Id: p.ID, Key: p.Key, Name: p.Name, Type: gen.ProductType(p.Type), Owner: ptr(p.Owner),
		Lifecycle: ptr(gen.ProductLifecycle(p.Lifecycle)), SsdlcCertified: ptr(p.SSDLCCertified), HubManual: ptr(p.HubManual),
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

func toProductInput(in gen.ProductInput) pg.ProductInput {
	out := pg.ProductInput{Key: in.Key, Name: in.Name, Type: pg.ProductType(in.Type), Owner: strOrEmpty(in.Owner), SSDLCCertified: boolOr(in.SsdlcCertified), HubManual: boolOr(in.HubManual)}
	if in.Lifecycle != nil {
		out.Lifecycle = pg.Lifecycle(*in.Lifecycle)
	}
	return out
}

func toFeature(f pg.Feature) gen.Feature {
	out := gen.Feature{
		Id: f.ID, ProductId: f.ProductID, Name: f.Name, Status: gen.FeatureStatus(f.Status), OwnValue: money(f.OwnValue),
		PlannedDate: datePtr(f.PlannedDate), Affected: f.Affected, AffectedBy: idPtr(f.AffectedBy), ImpliedDate: datePtr(f.ImpliedDate),
		CapabilityId: idPtr(f.CapabilityID), CreatedAt: ptr(f.CreatedAt), UpdatedAt: ptr(f.UpdatedAt),
	}
	if f.ExternalKey != "" {
		out.ExternalKey = ptr(f.ExternalKey)
	}
	return out
}

func toFeatureInput(in gen.FeatureInput) pg.FeatureInput {
	out := pg.FeatureInput{CapabilityID: idOrNil(in.CapabilityId), Name: strOrEmpty(in.Name), PlannedDate: dateOf(in.PlannedDate), ExternalKey: strOrEmpty(in.ExternalKey)}
	if in.Status != nil {
		out.Status = pg.FeatureStatus(*in.Status)
	}
	return out
}

func toLink(l pg.Link) gen.Link {
	return gen.Link{
		Id: l.ID, Type: gen.LinkType(l.Type), FromProductId: l.FromProductID, ToProductId: l.ToProductID,
		FromFeatureId: idPtr(l.FromFeatureID), ToFeatureId: idPtr(l.ToFeatureID), Criticality: gen.LinkCriticality(l.Criticality),
		ContractId: idPtr(l.ContractID), CreatedAt: l.CreatedAt,
	}
}

func toContract(c pg.IntegrationContract, ready kernel.Date) gen.Contract {
	pairs := make([]gen.VersionPair, 0, len(c.Compatibility))
	for _, p := range c.Compatibility {
		pairs = append(pairs, gen.VersionPair{ProviderVersion: p.ProviderVersion, ConsumerVersion: p.ConsumerVersion, Compatible: p.Compatible})
	}
	return gen.Contract{
		Id: c.ID, Name: c.Name, ProviderProductId: c.ProviderProductID, ConsumerProductId: c.ConsumerProductID,
		ProviderFeatureIds: ptr(ids(c.ProviderFeatureIDs)), ConsumerFeatureIds: ptr(ids(c.ConsumerFeatureIDs)),
		InterfaceVersion: ptr(c.InterfaceVersion), Owner: ptr(c.Owner), Status: ptr(gen.ContractStatus(c.Status)),
		Criticality: gen.ContractCriticality(c.Criticality), Compatibility: &pairs, SignalValue: money(c.SignalValue),
		ReadyDate: datePtr(ready), CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

func toContractInput(in gen.ContractInput) pg.ContractInput {
	out := pg.ContractInput{
		Name: in.Name, ProviderProductID: in.ProviderProductId, ConsumerProductID: in.ConsumerProductId,
		ProviderFeatureIDs: kids(in.ProviderFeatureIds), ConsumerFeatureIDs: kids(in.ConsumerFeatureIds),
		InterfaceVersion: strOrEmpty(in.InterfaceVersion), Owner: strOrEmpty(in.Owner), Criticality: pg.Criticality(in.Criticality),
	}
	if in.Status != nil {
		out.Status = pg.ContractStatus(*in.Status)
	}
	if in.Compatibility != nil {
		for _, p := range *in.Compatibility {
			out.Compatibility = append(out.Compatibility, pg.VersionPair{ProviderVersion: p.ProviderVersion, ConsumerVersion: p.ConsumerVersion, Compatible: p.Compatible})
		}
	}
	return out
}

func toFeatureValue(v pg.FeatureValue) gen.FeatureValue {
	return gen.FeatureValue{FeatureId: v.FeatureID, ProductId: v.ProductID, OwnValue: money(v.OwnValue), DerivedValue: money(v.DerivedValue), TotalValue: money(v.TotalValue), ComputedAt: v.ComputedAt}
}

func scope(ctx context.Context) authz.Scope { return authz.FromContext(ctx) }

// ---- system ----

func accessName(a authz.Access) string {
	switch a {
	case authz.AccessPrivate:
		return "private"
	case authz.AccessStrategic:
		return "strategic"
	}
	return "none"
}

// GetMe — область доступа субъекта.
func (s *Server) GetMe(ctx context.Context, _ gen.GetMeRequestObject) (gen.GetMeResponseObject, error) {
	sc := scope(ctx)
	roles := make([]string, 0)
	for _, r := range sc.Roles() {
		roles = append(roles, string(r))
	}
	products := map[string]gen.MeProducts{}
	for _, id := range sc.ProductIDs(authz.AccessStrategic) {
		products[id.String()] = gen.MeProducts(accessName(sc.Product(id)))
	}
	all := "none"
	if sc.SeesAllProducts() {
		all = accessName(sc.Product(kernel.NilID))
	}
	return gen.GetMe200JSONResponse{Subject: sc.Subject(), Roles: roles, Audience: gen.MeAudience(sc.Audience()), AllProducts: gen.MeAllProducts(all), Products: products}, nil
}

// ---- portfolio ----

// ListProducts — продукты, видимые субъекту.
func (s *Server) ListProducts(ctx context.Context, _ gen.ListProductsRequestObject) (gen.ListProductsResponseObject, error) {
	ps, err := s.d.Portfolio.Products(ctx, scope(ctx))
	if err != nil {
		return nil, err
	}
	out := make(gen.ListProducts200JSONResponse, 0, len(ps))
	for _, p := range ps {
		out = append(out, toProduct(p))
	}
	return out, nil
}

// CreateProduct — PG-01.
func (s *Server) CreateProduct(ctx context.Context, req gen.CreateProductRequestObject) (gen.CreateProductResponseObject, error) {
	if err := s.checkProductLimit(ctx); err != nil {
		return nil, err
	}
	p, err := s.d.Portfolio.CreateProduct(ctx, scope(ctx), toProductInput(*req.Body))
	if err != nil {
		return nil, err
	}
	s.auditGraph(ctx, "product", p.ID, p.ID, "create")
	return gen.CreateProduct201JSONResponse(toProduct(p)), nil
}

// GetProduct — продукт.
func (s *Server) GetProduct(ctx context.Context, req gen.GetProductRequestObject) (gen.GetProductResponseObject, error) {
	p, err := s.d.Portfolio.Product(ctx, scope(ctx), req.ProductId)
	if err != nil {
		return nil, err
	}
	return gen.GetProduct200JSONResponse(toProduct(p)), nil
}

// UpdateProduct — изменение продукта.
func (s *Server) UpdateProduct(ctx context.Context, req gen.UpdateProductRequestObject) (gen.UpdateProductResponseObject, error) {
	p, err := s.d.Portfolio.UpdateProduct(ctx, scope(ctx), req.ProductId, toProductInput(*req.Body))
	if err != nil {
		return nil, err
	}
	s.auditGraph(ctx, "product", p.ID, p.ID, "update")
	return gen.UpdateProduct200JSONResponse(toProduct(p)), nil
}

// DeleteProduct — удаление продукта с каскадом; 409 при наличии контрактов.
func (s *Server) DeleteProduct(ctx context.Context, req gen.DeleteProductRequestObject) (gen.DeleteProductResponseObject, error) {
	if err := s.d.Portfolio.DeleteProduct(ctx, scope(ctx), req.ProductId); err != nil {
		if status, body := problemFor(err); status == 409 {
			if p, ok := body.(gen.Problem); ok {
				return gen.DeleteProduct409ApplicationProblemPlusJSONResponse(p), nil
			}
		}
		return nil, err
	}
	s.auditGraph(ctx, "product", req.ProductId, req.ProductId, "delete")
	return gen.DeleteProduct204Response{}, nil
}

// GetStrategicSlice — PG-10.
func (s *Server) GetStrategicSlice(ctx context.Context, req gen.GetStrategicSliceRequestObject) (gen.GetStrategicSliceResponseObject, error) {
	sl, err := s.d.Portfolio.StrategicSlice(ctx, scope(ctx), req.ProductId)
	if err != nil {
		return nil, err
	}
	out := gen.StrategicSlice{Product: toProduct(sl.Product), Features: []gen.StrategicFeature{}, Contracts: []gen.Contract{}}
	for _, f := range sl.Features {
		out.Features = append(out.Features, gen.StrategicFeature{Id: f.ID, Name: f.Name, Status: string(f.Status), TotalValue: money(f.TotalValue), PlannedDate: datePtr(f.PlannedDate), Affected: f.Affected})
	}
	for _, c := range sl.Contracts {
		out.Contracts = append(out.Contracts, toContract(c, kernel.Date{}))
	}
	return gen.GetStrategicSlice200JSONResponse(out), nil
}

// CreateCapability — PG-02.
func (s *Server) CreateCapability(ctx context.Context, req gen.CreateCapabilityRequestObject) (gen.CreateCapabilityResponseObject, error) {
	c, err := s.d.Portfolio.CreateCapability(ctx, scope(ctx), req.ProductId, req.Body.Name)
	if err != nil {
		return nil, err
	}
	return gen.CreateCapability201JSONResponse{Id: c.ID, ProductId: c.ProductID, Name: c.Name}, nil
}

// ListFeatures — приватный контур продукта.
func (s *Server) ListFeatures(ctx context.Context, req gen.ListFeaturesRequestObject) (gen.ListFeaturesResponseObject, error) {
	fs, err := s.d.Portfolio.Features(ctx, scope(ctx), req.ProductId)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListFeatures200JSONResponse, 0, len(fs))
	for _, f := range fs {
		out = append(out, toFeature(f))
	}
	return out, nil
}

// CreateFeature — PG-02.
func (s *Server) CreateFeature(ctx context.Context, req gen.CreateFeatureRequestObject) (gen.CreateFeatureResponseObject, error) {
	f, err := s.d.Portfolio.CreateFeature(ctx, scope(ctx), req.ProductId, toFeatureInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return gen.CreateFeature201JSONResponse(toFeature(f)), nil
}

// ListFeatureValues — PG-07 по продукту.
func (s *Server) ListFeatureValues(ctx context.Context, req gen.ListFeatureValuesRequestObject) (gen.ListFeatureValuesResponseObject, error) {
	vs, err := s.d.Portfolio.FeatureValues(ctx, scope(ctx), req.ProductId)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListFeatureValues200JSONResponse, 0, len(vs))
	for _, v := range vs {
		out = append(out, toFeatureValue(v))
	}
	return out, nil
}

// GetFeature — фича.
func (s *Server) GetFeature(ctx context.Context, req gen.GetFeatureRequestObject) (gen.GetFeatureResponseObject, error) {
	f, err := s.d.Portfolio.Feature(ctx, scope(ctx), req.FeatureId)
	if err != nil {
		return nil, err
	}
	return gen.GetFeature200JSONResponse(toFeature(f)), nil
}

// UpdateFeature — изменение фичи без даты.
func (s *Server) UpdateFeature(ctx context.Context, req gen.UpdateFeatureRequestObject) (gen.UpdateFeatureResponseObject, error) {
	if req.Body.PlannedDate != nil {
		return nil, kernel.Invalid("planned_date", "дата меняется через shift-date с указанием причины")
	}
	f, err := s.d.Portfolio.UpdateFeature(ctx, scope(ctx), req.FeatureId, toFeatureInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return gen.UpdateFeature200JSONResponse(toFeature(f)), nil
}

// CreateRequirement — PG-02.
func (s *Server) CreateRequirement(ctx context.Context, req gen.CreateRequirementRequestObject) (gen.CreateRequirementResponseObject, error) {
	r, err := s.d.Portfolio.CreateRequirement(ctx, scope(ctx), req.FeatureId, req.Body.Text)
	if err != nil {
		return nil, err
	}
	return gen.CreateRequirement201JSONResponse{Id: r.ID, ProductId: r.ProductID, FeatureId: r.FeatureID, Text: r.Text}, nil
}

// ShiftFeatureDate — PG-08 и RM-03.
func (s *Server) ShiftFeatureDate(ctx context.Context, req gen.ShiftFeatureDateRequestObject) (gen.ShiftFeatureDateResponseObject, error) {
	res, err := s.d.Portfolio.ShiftFeatureDate(ctx, scope(ctx), req.FeatureId, kernel.DateFromTime(req.Body.PlannedDate.Time), req.Body.Reason)
	if err != nil {
		return nil, err
	}
	out := gen.ShiftResult{SourceFeature: res.SourceFeature, OldDate: datePtr(res.OldDate), NewDate: openapi_types.Date{Time: res.NewDate.Time()},
		Affected: []gen.AffectedFeature{}, Contracts: ids(res.Contracts), Commitments: ids(res.Commitments)}
	for _, a := range res.Affected {
		out.Affected = append(out.Affected, gen.AffectedFeature{FeatureId: a.FeatureID, ProductId: a.ProductID, PlannedDate: datePtr(a.PlannedDate), ImpliedDate: datePtr(a.ImpliedDate), ViaFeature: a.ViaFeature, Depth: a.Depth})
	}
	return gen.ShiftFeatureDate200JSONResponse(out), nil
}

// GetFeatureValue — PG-07 по фиче.
func (s *Server) GetFeatureValue(ctx context.Context, req gen.GetFeatureValueRequestObject) (gen.GetFeatureValueResponseObject, error) {
	v, err := s.d.Portfolio.FeatureValue(ctx, scope(ctx), req.FeatureId)
	if err != nil {
		return nil, err
	}
	return gen.GetFeatureValue200JSONResponse(toFeatureValue(v)), nil
}

// ---- links ----

// ListLinks — PG-03/PG-09.
func (s *Server) ListLinks(ctx context.Context, _ gen.ListLinksRequestObject) (gen.ListLinksResponseObject, error) {
	ls, err := s.d.Portfolio.Links(ctx, scope(ctx))
	if err != nil {
		return nil, err
	}
	out := make(gen.ListLinks200JSONResponse, 0, len(ls))
	for _, l := range ls {
		out = append(out, toLink(l))
	}
	return out, nil
}

// CreateLink — PG-03, при цикле 409 (PG-05).
func (s *Server) CreateLink(ctx context.Context, req gen.CreateLinkRequestObject) (gen.CreateLinkResponseObject, error) {
	in := pg.LinkInput{
		Type: pg.LinkType(req.Body.Type), FromProductID: idOrNil(req.Body.FromProductId), ToProductID: idOrNil(req.Body.ToProductId),
		FromFeatureID: idOrNil(req.Body.FromFeatureId), ToFeatureID: idOrNil(req.Body.ToFeatureId), Criticality: pg.Criticality(req.Body.Criticality), ContractID: idOrNil(req.Body.ContractId),
	}
	l, err := s.d.Portfolio.CreateLink(ctx, scope(ctx), in)
	if err != nil {
		if status, body := problemFor(err); status == 409 {
			if cp, ok := body.(gen.CycleProblem); ok {
				return gen.CreateLink409ApplicationProblemPlusJSONResponse(cp), nil
			}
		}
		return nil, err
	}
	s.auditGraph(ctx, "link", l.ID, l.FromProductID, "create")
	return gen.CreateLink201JSONResponse(toLink(l)), nil
}

// DeleteLink — удаление связи.
func (s *Server) DeleteLink(ctx context.Context, req gen.DeleteLinkRequestObject) (gen.DeleteLinkResponseObject, error) {
	if err := s.d.Portfolio.DeleteLink(ctx, scope(ctx), req.LinkId); err != nil {
		return nil, err
	}
	s.auditGraph(ctx, "link", req.LinkId, kernel.NilID, "delete")
	return gen.DeleteLink204Response{}, nil
}

// ListHubs — PG-06.
func (s *Server) ListHubs(ctx context.Context, _ gen.ListHubsRequestObject) (gen.ListHubsResponseObject, error) {
	hs, err := s.d.Portfolio.Hubs(ctx, scope(ctx))
	if err != nil {
		return nil, err
	}
	out := make(gen.ListHubs200JSONResponse, 0, len(hs))
	for _, h := range hs {
		out = append(out, gen.HubInfo{ProductId: h.ProductID, InDegree: h.InDegree, Manual: h.Manual, Computed: h.Computed})
	}
	return out, nil
}

// ---- contracts ----

// ListContracts — PG-04.
func (s *Server) ListContracts(ctx context.Context, _ gen.ListContractsRequestObject) (gen.ListContractsResponseObject, error) {
	cs, err := s.d.Portfolio.Contracts(ctx, scope(ctx))
	if err != nil {
		return nil, err
	}
	out := make(gen.ListContracts200JSONResponse, 0, len(cs))
	for _, c := range cs {
		_, ready, err := s.d.Portfolio.Contract(ctx, scope(ctx), c.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, toContract(c, ready))
	}
	return out, nil
}

// CreateContract — PG-04.
func (s *Server) CreateContract(ctx context.Context, req gen.CreateContractRequestObject) (gen.CreateContractResponseObject, error) {
	c, err := s.d.Portfolio.SaveContract(ctx, scope(ctx), kernel.NilID, toContractInput(*req.Body))
	if err != nil {
		if status, body := problemFor(err); status == 409 {
			if cp, ok := body.(gen.CycleProblem); ok {
				return gen.CreateContract409ApplicationProblemPlusJSONResponse(cp), nil
			}
		}
		return nil, err
	}
	_, ready, _ := s.d.Portfolio.Contract(ctx, scope(ctx), c.ID)
	s.auditGraph(ctx, "contract", c.ID, c.ConsumerProductID, "create")
	return gen.CreateContract201JSONResponse(toContract(c, ready)), nil
}

// GetContract — контракт со сроком готовности.
func (s *Server) GetContract(ctx context.Context, req gen.GetContractRequestObject) (gen.GetContractResponseObject, error) {
	c, ready, err := s.d.Portfolio.Contract(ctx, scope(ctx), req.ContractId)
	if err != nil {
		return nil, err
	}
	return gen.GetContract200JSONResponse(toContract(c, ready)), nil
}

// UpdateContract — изменение контракта.
func (s *Server) UpdateContract(ctx context.Context, req gen.UpdateContractRequestObject) (gen.UpdateContractResponseObject, error) {
	c, err := s.d.Portfolio.SaveContract(ctx, scope(ctx), req.ContractId, toContractInput(*req.Body))
	if err != nil {
		return nil, err
	}
	_, ready, _ := s.d.Portfolio.Contract(ctx, scope(ctx), c.ID)
	s.auditGraph(ctx, "contract", c.ID, c.ConsumerProductID, "update")
	return gen.UpdateContract200JSONResponse(toContract(c, ready)), nil
}

// ---- admin ----

// GetGraphSettings — коэффициенты.
func (s *Server) GetGraphSettings(ctx context.Context, _ gen.GetGraphSettingsRequestObject) (gen.GetGraphSettingsResponseObject, error) {
	if err := scope(ctx).Require(authz.ActionReadStrategic, kernel.NilID); err != nil && !scope(ctx).Valid() {
		return nil, err
	}
	st, err := s.d.Portfolio.Settings(ctx)
	if err != nil {
		return nil, err
	}
	out := gen.GraphSettings{Coefficients: map[string]string{}}
	for k, v := range st.Coefficients {
		out.Coefficients[string(k)] = v.String()
	}
	return gen.GetGraphSettings200JSONResponse(out), nil
}

// UpdateGraphSettings — изменение коэффициентов (admin).
func (s *Server) UpdateGraphSettings(ctx context.Context, req gen.UpdateGraphSettingsRequestObject) (gen.UpdateGraphSettingsResponseObject, error) {
	st := pg.Settings{Coefficients: map[pg.Criticality]decimal.Decimal{}}
	for k, v := range req.Body.Coefficients {
		d, err := decimal.NewFromString(v)
		if err != nil {
			return nil, kernel.Invalid("coefficients", "коэффициент "+k+" не число")
		}
		st.Coefficients[pg.Criticality(k)] = d
	}
	if err := s.d.Portfolio.UpdateSettings(ctx, scope(ctx), st); err != nil {
		return nil, err
	}
	if s.d.Audit != nil {
		_, _ = s.d.Audit.Append(ctx, audit.Entry{Actor: scope(ctx).Subject(), Action: audit.ActionRuleChange, ObjectType: "graph_settings", Details: map[string]any{"coefficients": req.Body.Coefficients}})
	}
	return gen.UpdateGraphSettings204Response{}, nil
}

// VerifyAudit — NF-S05.
func (s *Server) VerifyAudit(ctx context.Context, _ gen.VerifyAuditRequestObject) (gen.VerifyAuditResponseObject, error) {
	if err := scope(ctx).Require(authz.ActionReadAudit, kernel.NilID); err != nil {
		return nil, err
	}
	res, err := audit.Verify(ctx, s.d.AuditStore)
	if err != nil {
		return nil, err
	}
	out := gen.AuditVerifyResult{Checked: res.Checked, Ok: res.OK}
	if !res.OK {
		out.BrokenSeq = ptr(res.BrokenSeq)
		out.Reason = ptr(res.Reason)
	}
	return gen.VerifyAudit200JSONResponse(out), nil
}

func (s *Server) auditGraph(ctx context.Context, objectType string, id, product kernel.ID, op string) {
	if s.d.Audit == nil {
		return
	}
	_, _ = s.d.Audit.Append(ctx, audit.Entry{Actor: scope(ctx).Subject(), Action: audit.ActionGraphChange, ObjectType: objectType, ObjectID: id.String(), ProductID: product, Details: map[string]any{"op": op, "at": time.Now().UTC().Format(time.RFC3339)}})
}

// checkProductLimit проверяет лимит лицензии поставки на число продуктов (AD-06).
// Без установленного ключа ограничений нет: в разработке и на стенде лицензия не требуется.
func (s *Server) checkProductLimit(ctx context.Context) error {
	if s.d.Licensing == nil {
		return nil
	}
	existing, err := s.d.Portfolio.Products(ctx, scope(ctx))
	if err != nil {
		return err
	}
	return s.d.Licensing.CheckLimit(ctx, scope(ctx).Subject(), licensing.ResourceProducts, len(existing))
}
