// Package economics implements immutable financial periods and reproducible P&L.
package economics

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/kernel"
)

const (
	Revenue            = "revenue"
	Payroll            = "payroll"
	DirectCost         = "direct_cost"
	Marketing          = "marketing"
	HubCost            = "hub_cost"
	CertificationCost  = "certification_cost"
	MaintenanceCost    = "maintenance_cost"
	MaxRows            = 10000
	MaxFields          = 64
	MaxCalculationCost = 500000
)

type Source struct {
	File  string `json:"file"`
	Sheet string `json:"sheet"`
	Row   int    `json:"row"`
	Hash  string `json:"hash"`
}

type Allocation struct {
	ProductID kernel.ID       `json:"product_id"`
	Share     decimal.Decimal `json:"share"`
}

// Row holds a team aggregate, never an individual salary.
type Row struct {
	ID                   kernel.ID         `json:"id"`
	ProductID            kernel.ID         `json:"product_id"`
	Category             string            `json:"category"`
	Amount               kernel.Money      `json:"amount"`
	TeamID               string            `json:"team_id,omitempty"`
	Headcount            int               `json:"headcount,omitempty"`
	FeatureID            kernel.ID         `json:"feature_id"`
	CertificationTrackID kernel.ID         `json:"certification_track_id"`
	Branch               string            `json:"branch,omitempty"`
	BundleID             string            `json:"bundle_id,omitempty"`
	Description          string            `json:"description,omitempty"`
	Source               Source            `json:"source"`
	Allocations          []Allocation      `json:"allocations"`
	AllocationSource     string            `json:"allocation_source,omitempty"`
	Values               map[string]string `json:"values"`
}

// Field is effective from its snapshot period; numeric inputs aggregate over the report selection.
type Field struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Type    string `json:"type"`   // money, number, percent, date, catalog
	Source  string `json:"source"` // import, manual, calculated
	Formula string `json:"formula,omitempty"`
}

type SaveInput struct {
	ProductID       kernel.ID `json:"product_id"`
	Period          string    `json:"period"`
	Currency        string    `json:"currency"`
	ExpectedVersion int       `json:"expected_version"`
	Recalculate     bool      `json:"recalculate"`
	Source          string    `json:"source"`
	SourceHash      string    `json:"source_hash"`
	Rows            []Row     `json:"rows"`
	Fields          []Field   `json:"fields"`
}

type SnapshotInfo struct {
	ID         kernel.ID `json:"id"`
	ProductID  kernel.ID `json:"product_id"`
	Period     string    `json:"period"`
	Currency   string    `json:"currency"`
	Version    int       `json:"version"`
	Closed     bool      `json:"closed"`
	Source     string    `json:"source"`
	SourceHash string    `json:"source_hash"`
	CreatedAt  time.Time `json:"created_at"`
	CreatedBy  string    `json:"created_by"`
	RowCount   int       `json:"row_count"`
}

type Snapshot struct {
	SnapshotInfo
	Rows   []Row   `json:"rows"`
	Fields []Field `json:"fields"`
}

type ReportInput struct {
	ProductID       kernel.ID         `json:"product_id"`
	Period          string            `json:"period"`
	Version         int               `json:"version"`
	FilterProductID kernel.ID         `json:"filter_product_id"`
	Team            string            `json:"team"`
	Overrides       map[string]string `json:"overrides"`
	Export          bool              `json:"export"`
}

type Lineage struct {
	Formula      string      `json:"formula"`
	Dependencies []string    `json:"dependencies"`
	RowIDs       []kernel.ID `json:"row_ids"`
	Sources      []Source    `json:"sources"`
}

type ProductReport struct {
	ProductID    kernel.ID          `json:"product_id"`
	Amounts      map[string]int64   `json:"amounts"`
	Revenue      int64              `json:"revenue"`
	DirectCost   int64              `json:"direct_cost"`
	HubCost      int64              `json:"hub_cost"`
	DirectProfit int64              `json:"direct_profit"`
	LoadedProfit int64              `json:"loaded_profit"`
	Metrics      map[string]string  `json:"metrics"`
	Lineage      map[string]Lineage `json:"lineage"`
}

type TeamCost struct {
	ProductID kernel.ID `json:"product_id"`
	TeamID    string    `json:"team_id"`
	Headcount int       `json:"headcount"`
	Cost      int64     `json:"cost"`
}

type Investment struct {
	ProductID kernel.ID `json:"product_id"`
	Kind      string    `json:"kind"`
	Key       string    `json:"key"`
	Revenue   int64     `json:"revenue"`
	Cost      int64     `json:"cost"`
	Balance   int64     `json:"balance"`
}

type Report struct {
	ProductID   kernel.ID       `json:"product_id"`
	Period      string          `json:"period"`
	Currency    string          `json:"currency"`
	Version     int             `json:"version"`
	Closed      bool            `json:"closed"`
	Scenario    bool            `json:"scenario"`
	Products    []ProductReport `json:"products"`
	Total       ProductReport   `json:"total"`
	Teams       []TeamCost      `json:"teams"`
	Investments []Investment    `json:"investments"`
}
