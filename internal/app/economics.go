package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/onixus/metis/internal/adapters/financefile"
	"github.com/onixus/metis/internal/adapters/onec"
	"github.com/onixus/metis/internal/economics"
	economicspg "github.com/onixus/metis/internal/economics/pgstore"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
	"github.com/shopspring/decimal"
)

// FinanceSourcesConfig contains no credentials. ZUP and the business base are
// collected completely before appending one book, so payroll cannot replace revenue.
type FinanceSourcesConfig struct {
	BookProductID kernel.ID             `json:"book_product_id"`
	Sources       []FinanceSourceConfig `json:"sources"`
}
type FinanceSourceConfig struct {
	Name        string                `json:"name"`
	Kind        string                `json:"kind"` // file | onec
	File        string                `json:"file,omitempty"`
	Template    ports.FinanceTemplate `json:"template,omitempty"`
	OneC        onec.Config           `json:"onec,omitempty"`
	UsernameEnv string                `json:"username_env,omitempty"`
	PasswordEnv string                `json:"password_env,omitempty"`
}

func (a *App) buildEconomics(_ context.Context) error {
	var store economics.Store = economics.NewMemStore()
	if a.db != nil {
		store = economicspg.New(a.db)
	}
	a.Economics = economics.NewService(store, a.Audit, kernel.SystemClock{}).WithReferences(financeReferences{a})
	a.Finance = financefile.New()
	if a.Cfg.FinanceSourcesFile == "" {
		return nil
	}
	data, err := readFinanceFile(a.Cfg.FinanceSourcesFile, 1<<20)
	if err != nil {
		return fmt.Errorf("finance profiles: %w", err)
	}
	var cfg FinanceSourcesConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return kernel.Invalid("finance_sources", "invalid source profiles JSON")
	}
	if cfg.BookProductID == kernel.NilID || len(cfg.Sources) == 0 || len(cfg.Sources) > 8 {
		return kernel.Invalid("finance_sources", "book product and 1..8 sources required")
	}
	names := map[string]bool{}
	for _, profile := range cfg.Sources {
		if profile.Name == "" || names[profile.Name] {
			return kernel.Invalid("finance_sources", "unique source names required")
		}
		names[profile.Name] = true
		switch profile.Kind {
		case "file":
			if profile.File == "" {
				return kernel.Invalid("finance_sources", "source file path required")
			}
			a.financeSources = append(a.financeSources, financeFileSource{a.Finance, profile.File, profile.Template})
		case "onec":
			if profile.UsernameEnv == "" || profile.PasswordEnv == "" {
				return kernel.Invalid("finance_sources", "credential environment references required")
			}
			profile.OneC.SourceName = profile.Name
			source, err := onec.New(profile.OneC, onec.NewStaticCredentials(os.Getenv(profile.UsernameEnv), os.Getenv(profile.PasswordEnv)), &http.Client{Timeout: 20 * time.Second})
			if err != nil {
				return fmt.Errorf("finance OData profile: %w", err)
			}
			a.financeSources = append(a.financeSources, source)
		default:
			return kernel.Invalid("finance_sources", "source kind must be file or onec")
		}
	}
	a.financeBook = cfg.BookProductID
	return nil
}

type financeFileSource struct {
	parser   ports.Finance
	filename string
	template ports.FinanceTemplate
}

func (f financeFileSource) Read(ctx context.Context) (ports.FinancePreview, error) {
	if err := ctx.Err(); err != nil {
		return ports.FinancePreview{}, err
	}
	data, err := readFinanceFile(f.filename, 8<<20)
	if err != nil {
		return ports.FinancePreview{}, fmt.Errorf("finance source read: %w", err)
	}
	return f.parser.Parse(ctx, filepath.Base(f.filename), data, f.template)
}

func readFinanceFile(filename string, limit int64) ([]byte, error) {
	// Only deployment-owned paths reach this function; file paths are never accepted from HTTP.
	root, err := os.OpenRoot(filepath.Dir(filename))
	if err != nil {
		return nil, kernel.Invalid("finance_file", "source directory unavailable")
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(filepath.Base(filename))
	if err != nil {
		return nil, kernel.Invalid("finance_file", "source file unavailable")
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > limit {
		return nil, kernel.Invalid("finance_file", "regular bounded source file required")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, kernel.Invalid("finance_file", "cannot read source file")
	}
	if int64(len(data)) > limit {
		return nil, kernel.Invalid("finance_file", "source exceeds size limit")
	}
	return data, nil
}

// collectFinance fetches outside the application transaction and fails as a
// whole when any source is missing or invalid. No partial book is committed.
func (a *App) collectFinance(ctx context.Context) (map[string]ports.FinancePreview, error) {
	grouped := map[string]ports.FinancePreview{}
	allHashes := []string{}
	var expectedPeriods map[string]bool
	totalRows := 0
	for _, source := range a.financeSources {
		p, err := source.Read(ctx)
		if err != nil {
			return nil, err
		}
		if len(p.Errors) > 0 || len(p.Rows) == 0 {
			return nil, kernel.Invalid("finance_sources", "every source must have rows and no validation errors")
		}
		sourcePeriods := map[string]bool{}
		for _, row := range p.Rows {
			sourcePeriods[row.Period] = true
		}
		if expectedPeriods == nil {
			expectedPeriods = sourcePeriods
		} else {
			if len(expectedPeriods) != len(sourcePeriods) {
				return nil, kernel.Invalid("finance_sources", "sources must cover the same periods")
			}
			for period := range expectedPeriods {
				if !sourcePeriods[period] {
					return nil, kernel.Invalid("finance_sources", "sources must cover the same periods")
				}
			}
		}
		totalRows += len(p.Rows)
		if totalRows > economics.MaxRows {
			return nil, kernel.Invalid("finance_sources", "combined row limit exceeded")
		}
		if len(p.SourceHashes) > 0 {
			allHashes = append(allHashes, p.SourceHashes...)
		} else {
			allHashes = append(allHashes, p.SourceHash)
		}
		for _, row := range p.Rows {
			batch := grouped[row.Period]
			batch.Rows = append(batch.Rows, row)
			grouped[row.Period] = batch
		}
	}
	for period, batch := range grouped {
		// Canonical normalized period content includes source lineage and order; any
		// correction receives a fresh version, unchanged scheduler runs are no-ops.
		raw, err := json.Marshal(batch.Rows)
		if err != nil {
			return nil, fmt.Errorf("encode finance batch: %w", err)
		}
		sum := sha256.Sum256(raw)
		batch.SourceHash, batch.SourceHashes = hex.EncodeToString(sum[:]), allHashes
		batch.Errors = []ports.FinanceRowError{}
		grouped[period] = batch
	}
	return grouped, nil
}

func (a *App) syncFinance(ctx context.Context) {
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	batches, err := a.collectFinance(readCtx)
	cancel()
	if err == nil {
		err = a.runOperation(ctx, func(ctx context.Context) error { return a.applyFinance(ctx, batches) })
	}
	if err != nil && ctx.Err() == nil {
		a.Log.ErrorContext(ctx, "financial import failed; prior versions preserved", "err", err)
	}
}

func (a *App) applyFinance(ctx context.Context, batches map[string]ports.FinancePreview) error {
	sc := identityaccess.FinanceServiceScope("finance-import")
	periods := make([]string, 0, len(batches))
	for period := range batches {
		periods = append(periods, period)
	}
	sort.Strings(periods)
	for _, period := range periods {
		preview := batches[period]
		previous, err := a.Economics.Snapshot(ctx, sc, a.financeBook, period, 0)
		if err != nil && !errors.Is(err, kernel.ErrNotFound) {
			return err
		}
		if previous.Version > 0 && previous.SourceHash == preview.SourceHash {
			continue
		}
		for _, row := range previous.Rows {
			if len(row.Allocations) > 0 || len(row.Values) > 0 {
				return fmt.Errorf("%w: source changed after financial rules or manual inputs; review and reimport explicitly", kernel.ErrConflict)
			}
		}
		if _, err := a.Economics.Import(ctx, sc, a.financeBook, period, preview, previous.Version, false); err != nil {
			return err
		}
	}
	return nil
}

type financeReferences struct{ app *App }

func (r financeReferences) ValidateFinanceProduct(ctx context.Context, sc authz.Scope, product kernel.ID) error {
	if product == kernel.NilID {
		return kernel.Invalid("product_id", "product required")
	}
	_, err := r.app.Portfolio.Product(ctx, sc, product)
	return err
}
func (r financeReferences) ValidateFinanceRow(ctx context.Context, sc authz.Scope, row economics.Row) error {
	if row.FeatureID != kernel.NilID {
		f, err := r.app.Portfolio.Feature(ctx, sc, row.FeatureID)
		if err != nil {
			return err
		}
		if f.ProductID != row.ProductID {
			return kernel.Invalid("feature_id", "feature belongs to another product")
		}
	}
	if row.CertificationTrackID != kernel.NilID {
		t, err := r.app.Compliance.Track(ctx, sc, row.CertificationTrackID)
		if err != nil {
			return err
		}
		if t.ProductID != row.ProductID {
			return kernel.Invalid("certification_track_id", "track belongs to another product")
		}
	}
	return nil
}

func (a *App) seedEconomics(ctx context.Context) error {
	sc := identityaccess.FinanceServiceScope("seed-finance")
	products, err := a.Portfolio.Products(ctx, sc)
	if err != nil {
		return err
	}
	var edr, vm kernel.ID
	for _, p := range products {
		if p.Key == "edr" {
			edr = p.ID
		}
		if p.Key == "vm" {
			vm = p.ID
		}
	}
	if edr == kernel.NilID || vm == kernel.NilID {
		return nil
	}
	const period = "2026-09"
	if _, err := a.Economics.Snapshot(ctx, sc, edr, period, 0); err == nil {
		return nil
	} else if !errors.Is(err, kernel.ErrNotFound) {
		return err
	}
	rows := []economics.Row{
		{ProductID: edr, Category: economics.Revenue, Amount: kernel.Money{Amount: 600000000, Currency: "RUB"}, Description: "Синтетическая выручка EDR"},
		{ProductID: vm, Category: economics.Revenue, Amount: kernel.Money{Amount: 300000000, Currency: "RUB"}, Description: "Синтетическая выручка VM"},
		{ProductID: edr, Category: economics.Payroll, Amount: kernel.Money{Amount: 240000000, Currency: "RUB"}, TeamID: "endpoint-team", Headcount: 8},
		{ProductID: vm, Category: economics.Payroll, Amount: kernel.Money{Amount: 120000000, Currency: "RUB"}, TeamID: "vulnerability-team", Headcount: 6},
		{ProductID: edr, Category: economics.HubCost, Amount: kernel.Money{Amount: 60000000, Currency: "RUB"}, TeamID: "platform-team", Headcount: 5, AllocationSource: "manual", Allocations: []economics.Allocation{{ProductID: edr, Share: decimal.New(6, -1)}, {ProductID: vm, Share: decimal.New(4, -1)}}},
		{ProductID: edr, Category: economics.Marketing, Amount: kernel.Money{Amount: 30000000, Currency: "RUB"}},
	}
	for i := range rows {
		rows[i].Source = economics.Source{File: "synthetic-pilot", Row: i + 1}
	}
	_, err = a.Economics.Save(ctx, sc, economics.SaveInput{ProductID: edr, Period: period, Currency: "RUB", Source: "synthetic-pilot", Rows: rows,
		Fields: []economics.Field{{Key: "margin_percent", Name: "Маржинальность, %", Type: "percent", Source: "calculated", Formula: "ifgt(revenue,0,loaded_profit/revenue*100,0)"}}})
	return err
}
