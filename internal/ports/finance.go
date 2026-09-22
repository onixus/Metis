package ports

import (
	"context"

	"github.com/onixus/metis/internal/kernel"
)

// FinanceTemplate maps canonical fields to source column headers. HeaderRow is
// one-based (zero means the first row); CSV defaults to a comma delimiter.
// Periods are explicit YYYY-MM strings; amounts use decimal major units or
// integer amount_minor. The importer never evaluates spreadsheet formulas.
type FinanceTemplate struct {
	Sheet     string            `json:"sheet"`
	HeaderRow int               `json:"header_row"`
	Delimiter string            `json:"delimiter"`
	Columns   map[string]string `json:"columns"`
}

// FinanceSource identifies the original row without retaining an executable
// document. Hash is SHA-256 of the complete source document or OData snapshot.
type FinanceSource struct {
	File  string `json:"file"`
	Sheet string `json:"sheet"`
	Row   int    `json:"row"`
	Hash  string `json:"hash"`
}

// FinanceRow is a normalized, externally owned financial fact. Payroll rows
// represent teams, never individual employees. Economics validates references,
// authorizes every ProductID, and commits a complete preview as a new version.
type FinanceRow struct {
	ProductID            kernel.ID     `json:"product_id"`
	Period               string        `json:"period"`
	Category             string        `json:"category"`
	Amount               kernel.Money  `json:"amount"`
	TeamID               string        `json:"team_id,omitempty"`
	Headcount            int           `json:"headcount,omitempty"`
	FeatureID            kernel.ID     `json:"feature_id,omitempty"`
	CertificationTrackID kernel.ID     `json:"certification_track_id,omitempty"`
	Branch               string        `json:"branch,omitempty"`
	BundleID             string        `json:"bundle_id,omitempty"`
	Description          string        `json:"description,omitempty"`
	Source               FinanceSource `json:"source"`
}

// FinanceRowError deliberately omits raw cell values from diagnostics.
type FinanceRowError struct {
	Row     int    `json:"row"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// FinancePreview includes valid rows and row errors for review. Any error must
// prevent committing the preview; partial financial imports are forbidden.
type FinancePreview struct {
	Rows       []FinanceRow      `json:"rows"`
	Errors     []FinanceRowError `json:"errors"`
	SourceHash string            `json:"source_hash"`
	// SourceHashes is populated only by trusted source composition. It preserves
	// child lineage when SourceHash identifies a combined snapshot.
	SourceHashes []string `json:"source_hashes,omitempty"`
	Sheet        string   `json:"sheet"`
}

// Finance is the read-only file-import port (EC-01, EC-07, NF-S14).
type Finance interface {
	Parse(ctx context.Context, filename string, data []byte, template FinanceTemplate) (FinancePreview, error)
}

// FinanceSourceReader reads an explicitly configured external financial source.
// It never writes to that source; scheduling and persistence belong to the app.
type FinanceSourceReader interface {
	Read(ctx context.Context) (FinancePreview, error)
}
