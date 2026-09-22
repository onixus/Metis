package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"strconv"

	"github.com/onixus/metis/internal/adapters/financefile"
	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/httpapi/gen"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

func (s *Server) financePreview(ctx context.Context, product kernel.ID, period string, body *gen.FinanceFileInput) (ports.FinancePreview, error) {
	if s.d.Economics == nil || s.d.Finance == nil || s.d.Audit == nil {
		return ports.FinancePreview{}, kernel.ErrUnavailable
	}
	if err := scope(ctx).Require(authz.ActionWriteFinance, product); err != nil {
		return ports.FinancePreview{}, err
	}
	if body == nil {
		return ports.FinancePreview{}, kernel.Invalid("body", "обязательно")
	}
	if len(body.ContentBase64) > 11184812 || len(body.Filename) > 240 {
		return ports.FinancePreview{}, kernel.Invalid("file", "превышен размер файла")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(body.ContentBase64)
	if err != nil {
		return ports.FinancePreview{}, kernel.Invalid("content_base64", "некорректная кодировка")
	}
	preview, err := s.d.Finance.Parse(ctx, body.Filename, data, body.Template)
	if err != nil {
		return ports.FinancePreview{}, err
	}
	for _, row := range preview.Rows {
		if err := scope(ctx).Require(authz.ActionReadFinance, row.ProductID); err != nil {
			return ports.FinancePreview{}, err
		}
		if row.Period != period {
			preview.Errors = append(preview.Errors, ports.FinanceRowError{Row: row.Source.Row, Field: "period", Message: "период строки отличается от выбранного"})
		}
	}
	_, err = s.d.Audit.Append(ctx, audit.Entry{Actor: scope(ctx).Subject(), Action: audit.ActionViewFinance, ObjectType: "economics.preview", ProductID: product, ObjectID: period})
	if err != nil {
		return ports.FinancePreview{}, err
	}
	if preview.Rows == nil {
		preview.Rows = []ports.FinanceRow{}
	}
	if preview.Errors == nil {
		preview.Errors = []ports.FinanceRowError{}
	}
	return preview, nil
}

func (s *Server) PreviewFinanceImport(ctx context.Context, req gen.PreviewFinanceImportRequestObject) (gen.PreviewFinanceImportResponseObject, error) {
	p, err := s.financePreview(ctx, req.ProductId, req.Period, req.Body)
	if err != nil {
		return nil, err
	}
	return gen.PreviewFinanceImport200JSONResponse(p), nil
}

func (s *Server) ImportFinanceFile(ctx context.Context, req gen.ImportFinanceFileRequestObject) (gen.ImportFinanceFileResponseObject, error) {
	p, err := s.financePreview(ctx, req.ProductId, req.Period, req.Body)
	if err != nil {
		return nil, err
	}
	snapshot, err := s.d.Economics.Import(ctx, scope(ctx), req.ProductId, req.Period, p, req.Body.ExpectedVersion, req.Body.Recalculate != nil && *req.Body.Recalculate)
	if err != nil {
		return nil, err
	}
	return gen.ImportFinanceFile201JSONResponse(snapshot), nil
}

func (s *Server) ListEconomicsVersions(ctx context.Context, req gen.ListEconomicsVersionsRequestObject) (gen.ListEconomicsVersionsResponseObject, error) {
	if s.d.Economics == nil {
		return nil, kernel.ErrUnavailable
	}
	v, err := s.d.Economics.History(ctx, scope(ctx), req.ProductId, req.Period)
	if err != nil {
		return nil, err
	}
	return gen.ListEconomicsVersions200JSONResponse(v), nil
}

func (s *Server) GetEconomicsSnapshot(ctx context.Context, req gen.GetEconomicsSnapshotRequestObject) (gen.GetEconomicsSnapshotResponseObject, error) {
	if s.d.Economics == nil {
		return nil, kernel.ErrUnavailable
	}
	v, err := s.d.Economics.Snapshot(ctx, scope(ctx), req.ProductId, req.Period, intValue(req.Params.Version))
	if err != nil {
		return nil, err
	}
	return gen.GetEconomicsSnapshot200JSONResponse(v), nil
}

func (s *Server) ConfigureEconomics(ctx context.Context, req gen.ConfigureEconomicsRequestObject) (gen.ConfigureEconomicsResponseObject, error) {
	if s.d.Economics == nil {
		return nil, kernel.ErrUnavailable
	}
	if req.Body == nil {
		return nil, kernel.Invalid("body", "обязательно")
	}
	b := req.Body
	old, err := s.d.Economics.Snapshot(ctx, scope(ctx), req.ProductId, req.Period, b.ExpectedVersion)
	if err != nil {
		return nil, err
	}
	indices := map[kernel.ID]int{}
	for i, row := range old.Rows {
		indices[row.ID] = i
	}
	manual := map[string]bool{}
	for _, field := range b.Fields {
		if field.Source == "manual" {
			manual[field.Key] = true
		}
	}
	seen := map[kernel.ID]bool{}
	for _, rule := range b.Rows {
		i, exists := indices[rule.RowId]
		if !exists || seen[rule.RowId] {
			return nil, kernel.Invalid("rows", "неизвестная или повторная строка")
		}
		seen[rule.RowId] = true
		old.Rows[i].Allocations, old.Rows[i].AllocationSource = rule.Allocations, "manual"
		if rule.Values != nil {
			if old.Rows[i].Values == nil {
				old.Rows[i].Values = map[string]string{}
			}
			for key, value := range *rule.Values {
				if !manual[key] {
					return nil, kernel.Invalid("values", "менять можно только поля ручного ввода")
				}
				old.Rows[i].Values[key] = value
			}
		}
	}
	next, err := s.d.Economics.Save(ctx, scope(ctx), economics.SaveInput{ProductID: req.ProductId, Period: req.Period, Currency: old.Currency,
		ExpectedVersion: b.ExpectedVersion, Recalculate: b.Recalculate != nil && *b.Recalculate, Source: old.Source, SourceHash: old.SourceHash, Rows: old.Rows, Fields: b.Fields})
	if err != nil {
		return nil, err
	}
	return gen.ConfigureEconomics200JSONResponse(next), nil
}

func (s *Server) CloseEconomics(ctx context.Context, req gen.CloseEconomicsRequestObject) (gen.CloseEconomicsResponseObject, error) {
	if s.d.Economics == nil {
		return nil, kernel.ErrUnavailable
	}
	if req.Body == nil {
		return nil, kernel.Invalid("body", "обязательно")
	}
	v, err := s.d.Economics.Close(ctx, scope(ctx), req.ProductId, req.Period, req.Body.ExpectedVersion)
	if err != nil {
		return nil, err
	}
	return gen.CloseEconomics200JSONResponse(v), nil
}

func reportInput(product kernel.ID, period string, version *int, filter *kernel.ID, team *string) economics.ReportInput {
	in := economics.ReportInput{ProductID: product, Period: period, Version: intValue(version)}
	if filter != nil {
		in.FilterProductID = *filter
	}
	if team != nil {
		in.Team = *team
	}
	return in
}

func intValue(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func (s *Server) GetEconomicsReport(ctx context.Context, req gen.GetEconomicsReportRequestObject) (gen.GetEconomicsReportResponseObject, error) {
	if s.d.Economics == nil {
		return nil, kernel.ErrUnavailable
	}
	r, err := s.d.Economics.Report(ctx, scope(ctx), reportInput(req.ProductId, req.Period, req.Params.Version, req.Params.FilterProductId, req.Params.Team))
	if err != nil {
		return nil, err
	}
	return gen.GetEconomicsReport200JSONResponse(r), nil
}

func (s *Server) CalculateEconomicsScenario(ctx context.Context, req gen.CalculateEconomicsScenarioRequestObject) (gen.CalculateEconomicsScenarioResponseObject, error) {
	if s.d.Economics == nil {
		return nil, kernel.ErrUnavailable
	}
	if req.Body == nil {
		return nil, kernel.Invalid("body", "обязательно")
	}
	in := reportInput(req.ProductId, req.Period, &req.Body.Version, req.Body.FilterProductId, req.Body.Team)
	in.Overrides = req.Body.Overrides
	r, err := s.d.Economics.Report(ctx, scope(ctx), in)
	if err != nil {
		return nil, err
	}
	return gen.CalculateEconomicsScenario200JSONResponse(r), nil
}

func (s *Server) ExportEconomics(ctx context.Context, req gen.ExportEconomicsRequestObject) (gen.ExportEconomicsResponseObject, error) {
	if s.d.Economics == nil {
		return nil, kernel.ErrUnavailable
	}
	in := reportInput(req.ProductId, req.Period, req.Params.Version, req.Params.FilterProductId, req.Params.Team)
	in.Export = true
	r, err := s.d.Economics.Report(ctx, scope(ctx), in)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write([]string{"product_id", "period", "version", "currency", "revenue_minor", "direct_cost_minor", "hub_cost_minor", "direct_profit_minor", "loaded_profit_minor"}); err != nil {
		return nil, err
	}
	for _, p := range r.Products {
		row := []string{p.ProductID.String(), r.Period, strconv.Itoa(r.Version), r.Currency, strconv.FormatInt(p.Revenue, 10), strconv.FormatInt(p.DirectCost, 10), strconv.FormatInt(p.HubCost, 10), strconv.FormatInt(p.DirectProfit, 10), strconv.FormatInt(p.LoadedProfit, 10)}
		for i := 0; i < 4; i++ {
			row[i] = financefile.EscapeCSVCell(row[i])
		}
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return gen.ExportEconomics200TextcsvResponse{Body: bytes.NewReader(buf.Bytes()), ContentLength: int64(buf.Len())}, nil
}

func (s *Server) ListFinanceTemplates(ctx context.Context, req gen.ListFinanceTemplatesRequestObject) (gen.ListFinanceTemplatesResponseObject, error) {
	if s.d.Economics == nil {
		return nil, kernel.ErrUnavailable
	}
	v, err := s.d.Economics.Templates(ctx, scope(ctx), req.ProductId)
	if err != nil {
		return nil, err
	}
	return gen.ListFinanceTemplates200JSONResponse(v), nil
}

func (s *Server) SaveFinanceTemplate(ctx context.Context, req gen.SaveFinanceTemplateRequestObject) (gen.SaveFinanceTemplateResponseObject, error) {
	if s.d.Economics == nil {
		return nil, kernel.ErrUnavailable
	}
	if req.Body == nil {
		return nil, kernel.Invalid("body", "обязательно")
	}
	in := *req.Body
	in.ProductID = req.ProductId
	v, err := s.d.Economics.SaveTemplate(ctx, scope(ctx), in)
	if err != nil {
		return nil, err
	}
	return gen.SaveFinanceTemplate200JSONResponse(v), nil
}

func (s *Server) ApplyFinanceWorklogs(ctx context.Context, req gen.ApplyFinanceWorklogsRequestObject) (gen.ApplyFinanceWorklogsResponseObject, error) {
	if s.d.FinanceWorklogs == nil {
		return nil, kernel.ErrUnavailable
	}
	if req.Body == nil {
		return nil, kernel.Invalid("body", "обязательно")
	}
	v, err := s.d.FinanceWorklogs(ctx, scope(ctx), req.ProductId, req.Period, req.Body.ExpectedVersion, req.Body.Recalculate)
	if err != nil {
		return nil, err
	}
	return gen.ApplyFinanceWorklogs200JSONResponse(v), nil
}
