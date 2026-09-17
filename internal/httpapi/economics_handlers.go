package httpapi

import (
	"context"
	"io"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/economics/formula"
	"github.com/onixus/metis/internal/httpapi/gen"
	"github.com/onixus/metis/internal/kernel"
)

// maxImportSize ограничивает размер загружаемой книги (NF-P05).
const maxImportSize = 32 << 20

func (s *Server) requireEconomics() error {
	if s.d.Economics == nil {
		return kernel.ErrUnavailable
	}
	return nil
}

// ---- преобразования ----

func decStr(d decimal.Decimal) string { return d.String() }

func parseDec(s string) (decimal.Decimal, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, kernel.Invalid("value", "значение не число: "+s)
	}
	return d, nil
}

func parseShares(in map[string]string) (map[kernel.ID]decimal.Decimal, error) {
	out := make(map[kernel.ID]decimal.Decimal, len(in))
	for rawID, rawValue := range in {
		id, err := kernel.ParseID(rawID)
		if err != nil {
			return nil, err
		}
		v, err := parseDec(rawValue)
		if err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, nil
}

func sharesOut(in map[kernel.ID]decimal.Decimal) map[string]string {
	out := make(map[string]string, len(in))
	for id, v := range in {
		out[id.String()] = v.String()
	}
	return out
}

func periodOf(p *string) (economics.Period, error) {
	if p == nil || *p == "" {
		return economics.Period{}, nil
	}
	return economics.ParsePeriod(*p)
}

func periodRequired(p *string) (economics.Period, error) {
	period, err := periodOf(p)
	if err != nil {
		return economics.Period{}, err
	}
	if period.IsZero() {
		return economics.Period{}, kernel.Invalid("period", "период обязателен")
	}
	return period, nil
}

func toField(f economics.Field) gen.FinancialField {
	versions := make([]gen.FinancialFieldVersion, 0, len(f.Versions))
	for _, v := range f.Versions {
		dims := make([]string, 0, len(v.Dimensions))
		for _, d := range v.Dimensions {
			dims = append(dims, string(d))
		}
		at := v.At
		actor := v.Actor
		currency := v.Currency
		versions = append(versions, gen.FinancialFieldVersion{
			Version: v.Version, EffectiveFrom: datePtr(v.EffectiveFrom), Name: v.Name,
			Type: string(v.Type), Currency: &currency, Source: string(v.Source),
			Dimensions: &dims, Actor: &actor, At: &at,
		})
	}
	return gen.FinancialField{Id: f.ID, Key: f.Key, Versions: versions}
}

func toMetric(m economics.Metric) gen.Metric {
	versions := make([]gen.MetricVersion, 0, len(m.Versions))
	for _, v := range m.Versions {
		refs := make([]struct {
			Key  *string `json:"key,omitempty"`
			Kind *string `json:"kind,omitempty"`
		}, 0, len(v.Refs))
		for _, r := range v.Refs {
			kind, key := string(r.Kind), r.Key
			refs = append(refs, struct {
				Key  *string `json:"key,omitempty"`
				Kind *string `json:"kind,omitempty"`
			}{Key: &key, Kind: &kind})
		}
		at, actor, currency := v.At, v.Actor, v.Currency
		versions = append(versions, gen.MetricVersion{
			Version: v.Version, EffectiveFrom: datePtr(v.EffectiveFrom), Name: v.Name,
			Expression: v.Expression, Refs: &refs, Currency: &currency, Actor: &actor, At: &at,
		})
	}
	return gen.Metric{Id: m.ID, Key: m.Key, Versions: versions}
}

func toFactRow(r economics.FactRow) gen.FactRow {
	sheet, row := r.Sheet, r.Row
	version := r.DataVersion
	batch := r.BatchID
	item := r.Item
	return gen.FactRow{
		Id: r.ID, BatchId: &batch, FieldKey: r.FieldKey, ProductId: idPtr(r.ProductID), TeamId: idPtr(r.TeamID),
		Period: r.Period.String(), Item: &item, Value: decStr(r.Value), DataVersion: &version,
		Sheet: &sheet, Row: &row,
	}
}

func toExplanation(e economics.Explanation) gen.Explanation {
	out := gen.Explanation{Key: e.Key, Kind: gen.ExplanationKind(e.Kind), Value: decStr(e.Value)}
	if e.Expression != "" {
		expr := e.Expression
		out.Expression = &expr
	}
	if len(e.Rows) > 0 {
		rows := make([]gen.FactRow, 0, len(e.Rows))
		for _, r := range e.Rows {
			rows = append(rows, toFactRow(r))
		}
		out.Rows = &rows
	}
	if len(e.Inputs) > 0 {
		inputs := make([]gen.Explanation, 0, len(e.Inputs))
		for _, in := range e.Inputs {
			inputs = append(inputs, toExplanation(in))
		}
		out.Inputs = &inputs
	}
	return out
}

func toBatch(b economics.ImportBatch) gen.ImportBatch {
	tpl, version, rows := b.TemplateID, b.DataVersion, b.Rows
	file, sum, actor, at, scheduled := b.FileName, b.SHA256, b.Actor, b.At, b.Scheduled
	errs := make([]gen.RowError, 0, len(b.Errors))
	for _, e := range b.Errors {
		sheet, row, column := e.Sheet, e.Row, e.Column
		errs = append(errs, gen.RowError{Sheet: &sheet, Row: &row, Column: &column, Message: e.Message})
	}
	return gen.ImportBatch{
		Id: b.ID, TemplateId: &tpl, Period: b.Period.String(), DataVersion: &version,
		Status: gen.ImportBatchStatus(b.Status), FileName: &file, Sha256: &sum, Rows: &rows,
		Errors: &errs, Actor: &actor, At: &at, Scheduled: &scheduled,
	}
}

func toImportTemplate(t economics.Template) gen.ImportTemplate {
	sheets := make([]gen.ImportSheet, 0, len(t.Sheets))
	for _, sh := range t.Sheets {
		columns := make([]gen.ImportColumn, 0, len(sh.Columns))
		for _, c := range sh.Columns {
			minor := c.MinorUnits
			columns = append(columns, gen.ImportColumn{Column: c.Column, FieldKey: c.FieldKey, MinorUnits: &minor})
		}
		header, product, team, period, item := sh.HeaderRow, sh.ProductColumn, sh.TeamColumn, sh.PeriodColumn, sh.ItemColumn
		sheets = append(sheets, gen.ImportSheet{Sheet: sh.Sheet, HeaderRow: &header, ProductColumn: &product,
			TeamColumn: &team, PeriodColumn: &period, ItemColumn: &item, Columns: columns})
	}
	created, updated := t.CreatedAt, t.UpdatedAt
	return gen.ImportTemplate{Id: t.ID, Name: t.Name, Sheets: sheets, CreatedAt: &created, UpdatedAt: &updated}
}

func toPnL(p economics.PnL) gen.PnL {
	bundle := money(p.BundleRevenue)
	return gen.PnL{ProductId: p.ProductID, Period: p.Period.String(), Revenue: money(p.Revenue),
		BundleRevenue: &bundle, DirectCosts: money(p.DirectCosts), HubLoad: money(p.HubLoad),
		DirectProfit: money(p.DirectProfit), LoadedProfit: money(p.LoadedProfit)}
}

func toAllocationRule(r economics.AllocationRule) gen.AllocationRule {
	shares := sharesOut(r.Shares)
	consumers := append([]kernel.ID(nil), r.Consumers...)
	actor, at := r.Actor, r.At
	return gen.AllocationRule{Id: r.ID, HubProductId: r.HubProductID, Basis: string(r.Basis),
		Shares: &shares, Consumers: &consumers, Version: r.Version,
		EffectiveFrom: datePtr(r.EffectiveFrom), Actor: &actor, At: &at}
}

func toBundleRule(r economics.BundleRule) gen.BundleRule {
	actor, at := r.Actor, r.At
	return gen.BundleRule{Id: r.ID, BundleKey: r.BundleKey, Shares: sharesOut(r.Shares),
		Version: r.Version, EffectiveFrom: datePtr(r.EffectiveFrom), Actor: &actor, At: &at}
}

// ---- поля и показатели ----

// ListFinancialFields — EC-08.
func (s *Server) ListFinancialFields(ctx context.Context, _ gen.ListFinancialFieldsRequestObject) (gen.ListFinancialFieldsResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	list, err := s.d.Economics.Fields(ctx, scope(ctx))
	if err != nil {
		return nil, err
	}
	out := make(gen.ListFinancialFields200JSONResponse, 0, len(list))
	for _, f := range list {
		out = append(out, toField(f))
	}
	return out, nil
}

// SaveFinancialField — EC-08, EC-11.
func (s *Server) SaveFinancialField(ctx context.Context, req gen.SaveFinancialFieldRequestObject) (gen.SaveFinancialFieldResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	b := req.Body
	in := economics.FieldInput{Key: b.Key, Name: b.Name, Type: economics.FieldType(b.Type),
		Currency: strOrEmpty(b.Currency), Source: economics.FieldSource(b.Source), EffectiveFrom: dateOf(b.EffectiveFrom)}
	if b.Dimensions != nil {
		for _, d := range *b.Dimensions {
			in.Dimensions = append(in.Dimensions, formula.Dimension(d))
		}
	}
	f, err := s.d.Economics.SaveField(ctx, scope(ctx), in)
	if err != nil {
		return nil, err
	}
	return gen.SaveFinancialField200JSONResponse(toField(f)), nil
}

// ListMetrics — EC-09.
func (s *Server) ListMetrics(ctx context.Context, _ gen.ListMetricsRequestObject) (gen.ListMetricsResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	list, err := s.d.Economics.Metrics(ctx, scope(ctx))
	if err != nil {
		return nil, err
	}
	out := make(gen.ListMetrics200JSONResponse, 0, len(list))
	for _, m := range list {
		out = append(out, toMetric(m))
	}
	return out, nil
}

// SaveMetric — EC-09, EC-10, EC-11.
func (s *Server) SaveMetric(ctx context.Context, req gen.SaveMetricRequestObject) (gen.SaveMetricResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	b := req.Body
	m, err := s.d.Economics.SaveMetric(ctx, scope(ctx), economics.MetricInput{
		Key: b.Key, Name: b.Name, Expression: b.Expression, Currency: strOrEmpty(b.Currency),
		EffectiveFrom: dateOf(b.EffectiveFrom)})
	if err != nil {
		return nil, err
	}
	return gen.SaveMetric200JSONResponse(toMetric(m)), nil
}

func sliceOf(product *kernel.ID, period economics.Period) economics.Slice {
	sl := economics.Slice{Period: period}
	if product != nil {
		sl.ProductID = *product
	}
	return sl
}

// GetMetricValue — EC-09.
func (s *Server) GetMetricValue(ctx context.Context, req gen.GetMetricValueRequestObject) (gen.GetMetricValueResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	period, err := periodOf(req.Params.Period)
	if err != nil {
		return nil, err
	}
	sl := sliceOf(req.Params.ProductId, period)
	v, err := s.d.Economics.Value(ctx, scope(ctx), req.MetricKey, sl)
	if err != nil {
		return nil, err
	}
	p := period.String()
	return gen.GetMetricValue200JSONResponse{Key: req.MetricKey, Value: decStr(v),
		Period: &p, ProductId: idPtr(sl.ProductID)}, nil
}

// ExplainMetric — EC-10.
func (s *Server) ExplainMetric(ctx context.Context, req gen.ExplainMetricRequestObject) (gen.ExplainMetricResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	period, err := periodOf(req.Params.Period)
	if err != nil {
		return nil, err
	}
	ex, err := s.d.Economics.Explain(ctx, scope(ctx), req.MetricKey, sliceOf(req.Params.ProductId, period))
	if err != nil {
		return nil, err
	}
	return gen.ExplainMetric200JSONResponse(toExplanation(ex)), nil
}

// CompareMetricVersions — EC-11.
func (s *Server) CompareMetricVersions(ctx context.Context, req gen.CompareMetricVersionsRequestObject) (gen.CompareMetricVersionsResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	period, err := periodOf(req.Params.Period)
	if err != nil {
		return nil, err
	}
	a, b, err := s.d.Economics.CompareVersions(ctx, scope(ctx), req.MetricKey,
		sliceOf(req.Params.ProductId, period), req.Params.A, req.Params.B)
	if err != nil {
		return nil, err
	}
	return gen.CompareMetricVersions200JSONResponse{Key: req.MetricKey, A: req.Params.A, B: req.Params.B,
		ValueA: decStr(a), ValueB: decStr(b)}, nil
}

// ---- импорт (EC-01, EC-07) ----

// ListImportTemplates — EC-07.
func (s *Server) ListImportTemplates(ctx context.Context, _ gen.ListImportTemplatesRequestObject) (gen.ListImportTemplatesResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	list, err := s.d.Economics.Templates(ctx, scope(ctx))
	if err != nil {
		return nil, err
	}
	out := make(gen.ListImportTemplates200JSONResponse, 0, len(list))
	for _, t := range list {
		out = append(out, toImportTemplate(t))
	}
	return out, nil
}

// SaveImportTemplate — EC-07.
func (s *Server) SaveImportTemplate(ctx context.Context, req gen.SaveImportTemplateRequestObject) (gen.SaveImportTemplateResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	in := economics.TemplateInput{ID: idOrNil(req.Body.Id), Name: req.Body.Name}
	for _, sh := range req.Body.Sheets {
		mapped := economics.SheetMap{Sheet: sh.Sheet, HeaderRow: intOr(sh.HeaderRow),
			ProductColumn: strOrEmpty(sh.ProductColumn), TeamColumn: strOrEmpty(sh.TeamColumn),
			PeriodColumn: strOrEmpty(sh.PeriodColumn), ItemColumn: strOrEmpty(sh.ItemColumn)}
		for _, c := range sh.Columns {
			mapped.Columns = append(mapped.Columns, economics.ColumnMap{Column: c.Column,
				FieldKey: c.FieldKey, MinorUnits: boolOr(c.MinorUnits)})
		}
		in.Sheets = append(in.Sheets, mapped)
	}
	t, err := s.d.Economics.SaveTemplate(ctx, scope(ctx), in)
	if err != nil {
		return nil, err
	}
	return gen.SaveImportTemplate200JSONResponse(toImportTemplate(t)), nil
}

func intOr(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// ListImportBatches — EC-07.
func (s *Server) ListImportBatches(ctx context.Context, req gen.ListImportBatchesRequestObject) (gen.ListImportBatchesResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	period, err := periodOf(req.Params.Period)
	if err != nil {
		return nil, err
	}
	list, err := s.d.Economics.Batches(ctx, scope(ctx), period)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListImportBatches200JSONResponse, 0, len(list))
	for _, b := range list {
		out = append(out, toBatch(b))
	}
	return out, nil
}

// ImportFinanceFile — EC-01, EC-07: загрузка книги XLSX вручную или по расписанию.
func (s *Server) ImportFinanceFile(ctx context.Context, req gen.ImportFinanceFileRequestObject) (gen.ImportFinanceFileResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	period, err := periodOf(req.Params.Period)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(req.Body, maxImportSize+1))
	if err != nil {
		return nil, kernel.Invalid("body", "не удалось прочитать файл")
	}
	if len(data) > maxImportSize {
		return nil, kernel.Invalid("body", "файл превышает допустимый размер")
	}
	in := economics.ImportInput{TemplateID: req.Params.TemplateId, Period: period,
		FileName: strOrEmpty(req.Params.FileName), Data: data, Force: boolOr(req.Params.Force)}
	var res economics.ImportResult
	if boolOr(req.Params.Preview) {
		res, err = s.d.Economics.PreviewImport(ctx, scope(ctx), in)
	} else {
		res, err = s.d.Economics.ApplyImport(ctx, scope(ctx), in)
	}
	if err != nil {
		return nil, err
	}
	rows := make([]gen.FactRow, 0, len(res.Rows))
	for _, r := range res.Rows {
		rows = append(rows, toFactRow(r))
	}
	return gen.ImportFinanceFile200JSONResponse{Batch: toBatch(res.Batch), Rows: &rows}, nil
}

// ---- правила ----

// ListAllocationRules — EC-02.
func (s *Server) ListAllocationRules(ctx context.Context, _ gen.ListAllocationRulesRequestObject) (gen.ListAllocationRulesResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	list, err := s.d.Economics.AllocationRules(ctx, scope(ctx))
	if err != nil {
		return nil, err
	}
	out := make(gen.ListAllocationRules200JSONResponse, 0, len(list))
	for _, r := range list {
		out = append(out, toAllocationRule(r))
	}
	return out, nil
}

// SaveAllocationRule — EC-02.
func (s *Server) SaveAllocationRule(ctx context.Context, req gen.SaveAllocationRuleRequestObject) (gen.SaveAllocationRuleResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	in := economics.AllocationInput{HubProductID: req.Body.HubProductId,
		Basis: economics.AllocationBasis(req.Body.Basis), EffectiveFrom: dateOf(req.Body.EffectiveFrom)}
	if req.Body.Shares != nil {
		shares, err := parseShares(*req.Body.Shares)
		if err != nil {
			return nil, err
		}
		in.Shares = shares
	}
	if req.Body.Consumers != nil {
		in.Consumers = append(in.Consumers, *req.Body.Consumers...)
	}
	r, err := s.d.Economics.SaveAllocationRule(ctx, scope(ctx), in)
	if err != nil {
		return nil, err
	}
	return gen.SaveAllocationRule201JSONResponse(toAllocationRule(r)), nil
}

// ListBundleRules — EC-04.
func (s *Server) ListBundleRules(ctx context.Context, _ gen.ListBundleRulesRequestObject) (gen.ListBundleRulesResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	list, err := s.d.Economics.BundleRules(ctx, scope(ctx))
	if err != nil {
		return nil, err
	}
	out := make(gen.ListBundleRules200JSONResponse, 0, len(list))
	for _, r := range list {
		out = append(out, toBundleRule(r))
	}
	return out, nil
}

// SaveBundleRule — EC-04.
func (s *Server) SaveBundleRule(ctx context.Context, req gen.SaveBundleRuleRequestObject) (gen.SaveBundleRuleResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	shares, err := parseShares(req.Body.Shares)
	if err != nil {
		return nil, err
	}
	r, err := s.d.Economics.SaveBundleRule(ctx, scope(ctx), economics.BundleInput{
		BundleKey: req.Body.BundleKey, Shares: shares, EffectiveFrom: dateOf(req.Body.EffectiveFrom)})
	if err != nil {
		return nil, err
	}
	return gen.SaveBundleRule201JSONResponse(toBundleRule(r)), nil
}

// ---- команды (EC-12) ----

// ListTeams — EC-12.
func (s *Server) ListTeams(ctx context.Context, _ gen.ListTeamsRequestObject) (gen.ListTeamsResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	list, err := s.d.Economics.Teams(ctx, scope(ctx))
	if err != nil {
		return nil, err
	}
	out := make(gen.ListTeams200JSONResponse, 0, len(list))
	for _, t := range list {
		out = append(out, gen.Team{Id: t.ID, Key: t.Key, Name: t.Name})
	}
	return out, nil
}

// CreateTeam — EC-12.
func (s *Server) CreateTeam(ctx context.Context, req gen.CreateTeamRequestObject) (gen.CreateTeamResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	t, err := s.d.Economics.SaveTeam(ctx, scope(ctx), req.Body.Key, req.Body.Name)
	if err != nil {
		return nil, err
	}
	return gen.CreateTeam201JSONResponse{Id: t.ID, Key: t.Key, Name: t.Name}, nil
}

// SetTeamShares — EC-12, DL-05.
func (s *Server) SetTeamShares(ctx context.Context, req gen.SetTeamSharesRequestObject) (gen.SetTeamSharesResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	period, err := economics.ParsePeriod(req.Body.Period)
	if err != nil {
		return nil, err
	}
	shares, err := parseShares(req.Body.Shares)
	if err != nil {
		return nil, err
	}
	if req.Body.Source != nil && *req.Body.Source == gen.TeamSharesInputSourceWorklogs {
		err = s.d.Economics.SetWorklogShares(ctx, scope(ctx), req.TeamId, period, shares)
	} else {
		err = s.d.Economics.SetTeamShares(ctx, scope(ctx), req.TeamId, period, shares)
	}
	if err != nil {
		return nil, err
	}
	return gen.SetTeamShares204Response{}, nil
}

// ---- отчёты ----

// GetProductPnL — EC-03.
func (s *Server) GetProductPnL(ctx context.Context, req gen.GetProductPnLRequestObject) (gen.GetProductPnLResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	period, err := periodRequired(req.Params.Period)
	if err != nil {
		return nil, err
	}
	pnl, err := s.d.Economics.ProductPnL(ctx, scope(ctx), req.ProductId, period)
	if err != nil {
		return nil, err
	}
	return gen.GetProductPnL200JSONResponse(toPnL(pnl)), nil
}

// GetPortfolioPnL — EC-03.
func (s *Server) GetPortfolioPnL(ctx context.Context, req gen.GetPortfolioPnLRequestObject) (gen.GetPortfolioPnLResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	period, err := periodRequired(req.Params.Period)
	if err != nil {
		return nil, err
	}
	port, err := s.d.Economics.PortfolioPnL(ctx, scope(ctx), period)
	if err != nil {
		return nil, err
	}
	products := make([]gen.PnL, 0, len(port.Products))
	for _, p := range port.Products {
		products = append(products, toPnL(p))
	}
	return gen.GetPortfolioPnL200JSONResponse{Period: port.Period.String(), Products: products,
		Revenue: money(port.Revenue), Costs: money(port.Costs), Profit: money(port.Profit)}, nil
}

// GetTeamProductMatrix — EC-12.
func (s *Server) GetTeamProductMatrix(ctx context.Context, req gen.GetTeamProductMatrixRequestObject) (gen.GetTeamProductMatrixResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	period, err := periodRequired(req.Params.Period)
	if err != nil {
		return nil, err
	}
	m, err := s.d.Economics.Matrix(ctx, scope(ctx), period)
	if err != nil {
		return nil, err
	}
	cells := make(map[string]map[string]gen.Money, len(m.Cells))
	for team, byProduct := range m.Cells {
		row := make(map[string]gen.Money, len(byProduct))
		for product, v := range byProduct {
			row[product.String()] = money(v)
		}
		cells[team.String()] = row
	}
	return gen.GetTeamProductMatrix200JSONResponse{Period: m.Period.String(), Teams: m.Teams,
		Products: m.Products, Cells: cells}, nil
}

// GetCertificationEconomics — EC-06.
func (s *Server) GetCertificationEconomics(ctx context.Context, req gen.GetCertificationEconomicsRequestObject) (gen.GetCertificationEconomicsResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	period, err := periodRequired(req.Params.Period)
	if err != nil {
		return nil, err
	}
	ce, err := s.d.Economics.CertificationEconomics(ctx, scope(ctx), req.ProductId, period)
	if err != nil {
		return nil, err
	}
	return gen.GetCertificationEconomics200JSONResponse{ProductId: ce.ProductID, Period: ce.Period.String(),
		TrackCost: money(ce.TrackCost), CertifiedRevenue: money(ce.CertifiedRevenue), Balance: money(ce.Balance)}, nil
}

// GetBranchCosts — EC-05.
func (s *Server) GetBranchCosts(ctx context.Context, req gen.GetBranchCostsRequestObject) (gen.GetBranchCostsResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	period, err := periodOf(req.Params.Period)
	if err != nil {
		return nil, err
	}
	list, err := s.d.Economics.BranchCosts(ctx, scope(ctx), req.ProductId, period)
	if err != nil {
		return nil, err
	}
	out := make(gen.GetBranchCosts200JSONResponse, 0, len(list))
	for _, b := range list {
		out = append(out, gen.BranchCost{ProductId: b.ProductID, Branch: b.Branch, Cost: money(b.Cost)})
	}
	return out, nil
}

// GetFeatureEconomics — EC-05.
func (s *Server) GetFeatureEconomics(ctx context.Context, req gen.GetFeatureEconomicsRequestObject) (gen.GetFeatureEconomicsResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	period, err := periodOf(req.Params.Period)
	if err != nil {
		return nil, err
	}
	fe, err := s.d.Economics.FeatureEconomics(ctx, scope(ctx), req.ProductId, req.FeatureId, period)
	if err != nil {
		return nil, err
	}
	return gen.GetFeatureEconomics200JSONResponse{FeatureId: fe.FeatureID, Investment: money(fe.Investment),
		Revenue: money(fe.Revenue), Balance: money(fe.Balance)}, nil
}

// ClosePeriod — EC-11.
func (s *Server) ClosePeriod(ctx context.Context, req gen.ClosePeriodRequestObject) (gen.ClosePeriodResponseObject, error) {
	if err := s.requireEconomics(); err != nil {
		return nil, err
	}
	period, err := economics.ParsePeriod(req.Period)
	if err != nil {
		return nil, err
	}
	if err := s.d.Economics.ClosePeriod(ctx, scope(ctx), period); err != nil {
		return nil, err
	}
	return gen.ClosePeriod204Response{}, nil
}
