package economics

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

type ImportTemplate struct {
	ProductID kernel.ID             `json:"product_id"`
	Name      string                `json:"name"`
	Template  ports.FinanceTemplate `json:"template"`
	UpdatedAt time.Time             `json:"updated_at"`
}

// Import accepts previews produced by the trusted Finance port, never client-provided parsed rows.
func (s *Service) Import(ctx context.Context, sc authz.Scope, product kernel.ID, period string, preview ports.FinancePreview, expectedVersion int, recalculate bool) (Snapshot, error) {
	if err := sc.Require(authz.ActionWriteFinance, product); err != nil {
		return Snapshot{}, err
	}
	if err := validatePeriod(period); err != nil {
		return Snapshot{}, err
	}
	if len(preview.Errors) > 0 || len(preview.Rows) == 0 {
		return Snapshot{}, kernel.Invalid("import", "a nonempty preview without row errors is required")
	}
	previous, err := s.store.Snapshot(ctx, sc, product, period, 0)
	if err != nil && !errors.Is(err, kernel.ErrNotFound) {
		return Snapshot{}, err
	}
	input := SaveInput{ProductID: product, Period: period, Currency: preview.Rows[0].Amount.Currency, ExpectedVersion: expectedVersion, Recalculate: recalculate,
		Source: "import", SourceHash: preview.SourceHash, Fields: previous.Fields, Rows: make([]Row, 0, len(preview.Rows))}
	switch strings.ToLower(filepath.Ext(preview.Rows[0].Source.File)) {
	case ".csv":
		input.Source = "csv"
	case ".xlsx":
		input.Source = "xlsx"
	}
	if strings.HasPrefix(preview.Rows[0].Source.File, "1c") {
		input.Source = "1c"
	}
	if len(preview.SourceHashes) > 100 {
		return Snapshot{}, kernel.Invalid("source_hashes", "too many financial sources")
	}
	allowedHashes := map[string]bool{preview.SourceHash: true}
	for _, hash := range preview.SourceHashes {
		if hash == "" || len(hash) > 128 {
			return Snapshot{}, kernel.Invalid("source_hashes", "invalid financial source hash")
		}
		allowedHashes[hash] = true
	}
	for _, row := range preview.Rows {
		if row.Period != period {
			return Snapshot{}, kernel.Invalid("period", "import contains another period")
		}
		if preview.SourceHash != "" && !allowedHashes[row.Source.Hash] {
			return Snapshot{}, kernel.Invalid("source_hash", "source lineage hash mismatch")
		}
		input.Rows = append(input.Rows, Row{ID: kernel.NewID(), ProductID: row.ProductID, Category: row.Category, Amount: row.Amount, TeamID: row.TeamID, Headcount: row.Headcount,
			FeatureID: row.FeatureID, CertificationTrackID: row.CertificationTrackID, Branch: row.Branch, BundleID: row.BundleID, Description: row.Description,
			Source: Source{File: row.Source.File, Sheet: row.Source.Sheet, Row: row.Source.Row, Hash: row.Source.Hash}, Allocations: []Allocation{}, Values: map[string]string{}})
	}
	return s.Save(ctx, sc, input)
}

func (s *Service) SaveTemplate(ctx context.Context, sc authz.Scope, template ImportTemplate) (ImportTemplate, error) {
	if err := sc.Require(authz.ActionWriteFinance, template.ProductID); err != nil {
		return ImportTemplate{}, err
	}
	if s.references == nil {
		return ImportTemplate{}, fmt.Errorf("%w: financial reference validation is required", kernel.ErrUnavailable)
	}
	if err := s.references.ValidateFinanceProduct(ctx, sc, template.ProductID); err != nil {
		return ImportTemplate{}, err
	}
	if strings.TrimSpace(template.Name) == "" || len(template.Name) > 128 || template.Template.HeaderRow < 0 || template.Template.HeaderRow > 1000 || len(template.Template.Sheet) > 256 || len(template.Template.Columns) > 64 {
		return ImportTemplate{}, kernel.Invalid("template", "invalid template bounds")
	}
	if template.Template.Delimiter != "" && template.Template.Delimiter != "," && template.Template.Delimiter != ";" && template.Template.Delimiter != "\t" {
		return ImportTemplate{}, kernel.Invalid("template", "unsupported delimiter")
	}
	for field, column := range template.Template.Columns {
		if len(field) > 64 || len(column) > 256 || field == "" || column == "" {
			return ImportTemplate{}, kernel.Invalid("template", "invalid field or column")
		}
	}
	template.UpdatedAt = s.clock.Now().UTC()
	if err := s.record(ctx, sc, template.ProductID, "template:"+template.Name, 0, audit.ActionRuleChange); err != nil {
		return ImportTemplate{}, err
	}
	if err := s.store.UpsertTemplate(ctx, sc, template); err != nil {
		return ImportTemplate{}, err
	}
	return template, nil
}

func (s *Service) Templates(ctx context.Context, sc authz.Scope, product kernel.ID) ([]ImportTemplate, error) {
	templates, err := s.store.ListTemplates(ctx, sc, product)
	if err != nil {
		return nil, err
	}
	if err := s.record(ctx, sc, product, "templates", 0, audit.ActionViewFinance); err != nil {
		return nil, err
	}
	return templates, nil
}
