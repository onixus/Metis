package economics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	book "github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// CanonicalBooks is the public, scoped port to immutable book history.
type CanonicalBooks interface {
	History(context.Context, authz.Scope, kernel.ID, string) ([]book.SnapshotInfo, error)
	Snapshot(context.Context, authz.Scope, kernel.ID, string, int) (book.Snapshot, error)
	Project(context.Context, authz.Scope, kernel.ID, string, int) (book.Projection, error)
	Report(context.Context, authz.Scope, book.ReportInput) (book.Report, error)
}

// HistoryStore stores an immutable reconciliation plan and canonical binding.
type HistoryStore interface {
	Unification(context.Context, authz.Scope, Period) (HistoryArchive, error)
	Unifications(context.Context, authz.Scope) ([]HistoryArchive, error)
	SaveUnification(context.Context, authz.Scope, HistoryArchive) error
	UnificationBindings(context.Context, authz.Scope) ([]HistoryBinding, error)
}
type HistoryBinding struct {
	OriginalBookVersion int               `json:"original_book_version"`
	BookProductID       kernel.ID         `json:"book_product_id"`
	Period              Period            `json:"period"`
	FieldKeys           map[string]string `json:"field_keys"`
	MetricKeys          map[string]string `json:"metric_keys"`
}
type HistoryUnificationInput struct {
	BookProductID        kernel.ID `json:"book_product_id"`
	Period               Period    `json:"period"`
	ExpectedBookVersion  int       `json:"expected_book_version"`
	ExpectedModelVersion int       `json:"expected_model_version"`
	Digest               string    `json:"digest"`
}
type HistoryUnificationPreview struct {
	Digest              string            `json:"digest"`
	BookProductID       kernel.ID         `json:"book_product_id"`
	Period              Period            `json:"period"`
	BookVersions        int               `json:"book_versions"`
	ModelVersions       int               `json:"model_versions"`
	CanonicalVersion    int               `json:"canonical_version"`
	ModelVersion        int               `json:"model_version"`
	DefinitionMappings  map[string]string `json:"definition_mappings"`
	PreviousPnL         []PnL             `json:"previous_pnl"`
	CanonicalPnL        []PnL             `json:"canonical_pnl"`
	ExcludedModelFields []string          `json:"excluded_model_fields"`
	DefinitionKeys      []string          `json:"definition_keys"`
	Warnings            []string          `json:"warnings"`
	Applied             bool              `json:"applied"`
}
type HistoricalFacts struct {
	Batch ImportBatch `json:"batch"`
	Facts []FactRow   `json:"facts"`
}
type HistoryArchive struct {
	ID            kernel.ID         `json:"id"`
	BookProductID kernel.ID         `json:"book_product_id"`
	Period        Period            `json:"period"`
	Digest        string            `json:"digest"`
	BookVersions  []book.Snapshot   `json:"book_versions"`
	ModelVersions []HistoricalFacts `json:"model_versions"`
	Fields        []Field           `json:"fields"`
	Metrics       []Metric          `json:"metrics"`
	Allocations   []AllocationRule  `json:"allocations"`
	Bundles       []BundleRule      `json:"bundles"`
	Teams         []Team            `json:"teams"`
	Shares        []TeamShare       `json:"shares"`
	Closed        bool              `json:"closed"`
	FieldKeys     map[string]string `json:"field_keys"`
	MetricKeys    map[string]string `json:"metric_keys"`
	Actor         string            `json:"actor"`
	At            time.Time         `json:"at"`
}

func (s *Service) WithCanonicalBooks(p CanonicalBooks) *Service {
	if current, ok := s.store.(*canonicalStore); ok {
		current.books = p
		return s
	}
	s.store = &canonicalStore{Store: s.store, books: p, cfg: s.cfg}
	return s
}
func (s *Service) canonicalStore() (*canonicalStore, error) {
	c, ok := s.store.(*canonicalStore)
	if !ok || c.books == nil {
		return nil, kernel.ErrUnavailable
	}
	if _, ok := c.Store.(HistoryStore); !ok {
		return nil, kernel.ErrUnavailable
	}
	return c, nil
}
func historyPrefix(owner kernel.ID, p Period) string {
	return "u_" + strings.ReplaceAll(owner.String(), "-", "") + "_" + strings.ReplaceAll(p.String(), "-", "") + "_"
}
func (s *Service) PreviewUnification(ctx context.Context, sc authz.Scope, in HistoryUnificationInput) (HistoryUnificationPreview, error) {
	a, p, err := s.prepareUnification(ctx, sc, in)
	_ = a
	if err != nil {
		return p, err
	}
	if err := s.logAccessChecked(ctx, sc, "read", "economics.unification.preview", in.BookProductID, map[string]any{"period": in.Period.String(), "digest": p.Digest}); err != nil {
		return HistoryUnificationPreview{}, err
	}
	return p, nil
}
func (s *Service) prepareUnification(ctx context.Context, sc authz.Scope, in HistoryUnificationInput) (HistoryArchive, HistoryUnificationPreview, error) {
	if err := s.requireWrite(sc, kernel.NilID); err != nil {
		return HistoryArchive{}, HistoryUnificationPreview{}, err
	}
	if !sc.SeesAllProducts() || sc.Finance() != authz.FinanceFull {
		return HistoryArchive{}, HistoryUnificationPreview{}, kernel.ErrForbidden
	}
	if in.BookProductID == kernel.NilID || in.Period.IsZero() {
		return HistoryArchive{}, HistoryUnificationPreview{}, kernel.Invalid("unification", "book and month required")
	}
	c, err := s.canonicalStore()
	if err != nil {
		return HistoryArchive{}, HistoryUnificationPreview{}, err
	}
	hs := c.Store.(HistoryStore)
	if _, err := c.canonicalTeamMap(ctx, sc); err != nil {
		return HistoryArchive{}, HistoryUnificationPreview{}, err
	}
	infos, err := c.books.History(ctx, sc, in.BookProductID, in.Period.String())
	if err != nil {
		return HistoryArchive{}, HistoryUnificationPreview{}, err
	}
	if len(infos) == 0 || len(infos) > 128 {
		return HistoryArchive{}, HistoryUnificationPreview{}, kernel.Invalid("history", "1..128 book revisions required")
	}
	if infos[len(infos)-1].Version != in.ExpectedBookVersion {
		return HistoryArchive{}, HistoryUnificationPreview{}, kernel.ErrConflict
	}
	if previous, lookupErr := hs.Unification(ctx, sc, in.Period); lookupErr == nil {
		if previous.BookProductID != in.BookProductID {
			return HistoryArchive{}, HistoryUnificationPreview{}, kernel.Invalid("book_product_id", "month is bound to another canonical book")
		}
		p, err := s.previewAppliedArchive(ctx, sc, previous)
		if err != nil {
			return HistoryArchive{}, p, err
		}
		p.CanonicalVersion = infos[len(infos)-1].Version
		return previous, p, nil
	} else if !errors.Is(lookupErr, kernel.ErrNotFound) {
		return HistoryArchive{}, HistoryUnificationPreview{}, lookupErr
	}
	budget := historyByteBudget{remaining: maxHistoryArchiveBytes - 4096}
	a := HistoryArchive{ID: kernel.NewID(), BookProductID: in.BookProductID, Period: in.Period, BookVersions: []book.Snapshot{}, ModelVersions: []HistoricalFacts{}, FieldKeys: map[string]string{}, MetricKeys: map[string]string{}}
	prefix := historyPrefix(in.BookProductID, in.Period)
	for _, info := range infos {
		snap, err := c.books.Snapshot(ctx, sc, in.BookProductID, in.Period.String(), info.Version)
		if err != nil {
			return a, HistoryUnificationPreview{}, err
		}
		if snap.Currency != s.cfg.Currency {
			return a, HistoryUnificationPreview{}, kernel.Invalid("currency", "canonical book currency must match model")
		}
		if err := budget.charge(snap); err != nil {
			return a, HistoryUnificationPreview{}, err
		}
		a.BookVersions = append(a.BookVersions, snap)
		for _, f := range snap.Fields {
			if f.Source == "calculated" {
				a.MetricKeys[f.Key] = canonicalDefinitionKey(prefix, f.Key)
			} else {
				a.FieldKeys[f.Key] = canonicalDefinitionKey(prefix, f.Key)
			}
		}
	}
	batches, err := c.Batches(ctx, sc, in.Period)
	if err != nil {
		return a, HistoryUnificationPreview{}, err
	}
	latest := 0
	for _, b := range batches {
		if b.Status != BatchApplied {
			historical := HistoricalFacts{Batch: b, Facts: []FactRow{}}
			if err := budget.charge(historical); err != nil {
				return a, HistoryUnificationPreview{}, err
			}
			a.ModelVersions = append(a.ModelVersions, historical)
			continue
		}
		if b.DataVersion > latest {
			latest = b.DataVersion
		}
		v := b.DataVersion
		facts, err := c.Store.Facts(ctx, sc, FactFilter{Period: &in.Period, DataVersion: &v})
		if err != nil {
			return a, HistoryUnificationPreview{}, err
		}
		historical := HistoricalFacts{Batch: b, Facts: facts}
		if err := budget.charge(historical); err != nil {
			return a, HistoryUnificationPreview{}, err
		}
		a.ModelVersions = append(a.ModelVersions, historical)
	}
	if latest != in.ExpectedModelVersion {
		return a, HistoryUnificationPreview{}, kernel.ErrConflict
	}
	if a.Fields, err = c.Fields(ctx, sc); err != nil {
		return a, HistoryUnificationPreview{}, err
	}
	if err := budget.charge(a.Fields); err != nil {
		return a, HistoryUnificationPreview{}, err
	}
	if a.Metrics, err = c.Metrics(ctx, sc); err != nil {
		return a, HistoryUnificationPreview{}, err
	}
	if err := budget.charge(a.Metrics); err != nil {
		return a, HistoryUnificationPreview{}, err
	}
	if a.Allocations, err = c.AllocationRules(ctx, sc); err != nil {
		return a, HistoryUnificationPreview{}, err
	}
	if err := budget.charge(a.Allocations); err != nil {
		return a, HistoryUnificationPreview{}, err
	}
	if a.Bundles, err = c.BundleRules(ctx, sc); err != nil {
		return a, HistoryUnificationPreview{}, err
	}
	if err := budget.charge(a.Bundles); err != nil {
		return a, HistoryUnificationPreview{}, err
	}
	if a.Teams, err = c.Store.Teams(ctx, sc); err != nil {
		return a, HistoryUnificationPreview{}, err
	}
	if err := budget.charge(a.Teams); err != nil {
		return a, HistoryUnificationPreview{}, err
	}
	if a.Shares, err = c.Store.TeamShares(ctx, sc, in.Period); err != nil {
		return a, HistoryUnificationPreview{}, err
	}
	if err := budget.charge(a.Shares); err != nil {
		return a, HistoryUnificationPreview{}, err
	}
	closed, err := c.Store.ClosedPeriods(ctx, sc)
	if err != nil {
		return a, HistoryUnificationPreview{}, err
	}
	for _, p := range closed {
		if p == in.Period {
			a.Closed = true
		}
	}
	if a.Closed && !a.BookVersions[len(a.BookVersions)-1].Closed {
		return a, HistoryUnificationPreview{}, kernel.Invalid("closed", "book and model close states must match before unification")
	}
	if a.Fields == nil {
		a.Fields = []Field{}
	}
	if a.Metrics == nil {
		a.Metrics = []Metric{}
	}
	if a.Allocations == nil {
		a.Allocations = []AllocationRule{}
	}
	if a.Bundles == nil {
		a.Bundles = []BundleRule{}
	}
	if a.Teams == nil {
		a.Teams = []Team{}
	}
	if a.Shares == nil {
		a.Shares = []TeamShare{}
	}
	sort.Slice(a.ModelVersions, func(i, j int) bool {
		if a.ModelVersions[i].Batch.DataVersion != a.ModelVersions[j].Batch.DataVersion {
			return a.ModelVersions[i].Batch.DataVersion < a.ModelVersions[j].Batch.DataVersion
		}
		return a.ModelVersions[i].Batch.ID.String() < a.ModelVersions[j].Batch.ID.String()
	})
	if err := budget.charge(a.FieldKeys); err != nil {
		return a, HistoryUnificationPreview{}, err
	}
	if err := budget.charge(a.MetricKeys); err != nil {
		return a, HistoryUnificationPreview{}, err
	}
	// ID is generated after the stable digest: it must not invalidate a repeated preview.
	a.ID = kernel.NilID
	raw, err := json.Marshal(a)
	if err != nil {
		return a, HistoryUnificationPreview{}, err
	}
	if len(raw) > maxHistoryArchiveBytes {
		return a, HistoryUnificationPreview{}, kernel.Invalid("history", "archive exceeds 32 MiB")
	}
	sum := sha256.Sum256(raw)
	a.Digest = hex.EncodeToString(sum[:])
	keys := []string{}
	seenKeys := map[string]bool{}
	for _, key := range a.FieldKeys {
		if !seenKeys[key] {
			keys = append(keys, key)
			seenKeys[key] = true
		}
	}
	for _, key := range a.MetricKeys {
		if !seenKeys[key] {
			keys = append(keys, key)
			seenKeys[key] = true
		}
	}
	sort.Strings(keys)
	p := HistoryUnificationPreview{Digest: a.Digest, BookProductID: in.BookProductID, Period: in.Period, BookVersions: len(a.BookVersions), ModelVersions: len(a.ModelVersions), CanonicalVersion: in.ExpectedBookVersion, ModelVersion: latest, DefinitionMappings: map[string]string{}, DefinitionKeys: keys, Warnings: []string{"book becomes the sole current fact source; original model versions remain immutable", "legacy model writes for this month are disabled", "canonical revenue includes the bundle component; do not add bundle_revenue again in common formulas", "canonical book data requires finance/full; aggregate-only legacy access is retained only for unbound months"}}
	legacy, err := NewService(c.Store, nil, s.clock, s.cfg)
	if err != nil {
		return a, p, err
	}
	products, err := legacy.productsWithData(ctx, sc, in.Period)
	if err != nil {
		return a, p, err
	}
	p.PreviousPnL = []PnL{}
	for _, pid := range products {
		value, err := legacy.pnl(ctx, sc, pid, in.Period, nil)
		if err != nil {
			return a, p, err
		}
		p.PreviousPnL = append(p.PreviousPnL, value)
	}
	canonical, err := c.books.Report(ctx, sc, book.ReportInput{ProductID: in.BookProductID, Period: in.Period.String(), Version: in.ExpectedBookVersion})
	if err != nil {
		return a, p, err
	}
	p.CanonicalPnL = []PnL{}
	for _, r := range canonical.Products {
		p.CanonicalPnL = append(p.CanonicalPnL, canonicalProductPnL(r, in.Period, canonical.Currency))
	}
	present := map[string]bool{}
	projection, err := c.books.Project(ctx, sc, in.BookProductID, in.Period.String(), in.ExpectedBookVersion)
	if err != nil {
		return a, p, err
	}
	for _, f := range projection.Facts {
		if a.FieldKeys[f.FieldKey] == "" {
			present[f.FieldKey] = true
		}
	}
	present[s.cfg.DirectCostField] = present[book.DirectCost]
	p.ExcludedModelFields = []string{}
	seenExcluded := map[string]bool{}
	for _, version := range a.ModelVersions {
		for _, row := range version.Facts {
			if !present[row.FieldKey] && !seenExcluded[row.FieldKey] {
				seenExcluded[row.FieldKey] = true
				p.ExcludedModelFields = append(p.ExcludedModelFields, row.FieldKey)
			}
		}
	}
	sort.Strings(p.ExcludedModelFields)
	for k, v := range a.FieldKeys {
		p.DefinitionMappings[k] = v
	}
	for k, v := range a.MetricKeys {
		p.DefinitionMappings[k] = v
	}
	previous, err := hs.Unification(ctx, sc, in.Period)
	if err == nil {
		if previous.BookProductID != in.BookProductID {
			return a, p, kernel.Invalid("book_product_id", "month is bound to another canonical book")
		}
		p.Applied = previous.Digest == a.Digest
	} else if !errors.Is(err, kernel.ErrNotFound) {
		return a, p, err
	}
	fields, metrics, err := s.migratedDefinitions(a)
	if err != nil {
		return a, p, err
	}
	for _, f := range fields {
		existing, err := c.Field(ctx, sc, f.Key)
		if err == nil {
			if existing.Latest().Type != f.Latest().Type || existing.Latest().Currency != f.Latest().Currency || (strings.HasPrefix(f.Key, prefix) && !sameFieldHistory(existing, f)) {
				return a, p, kernel.Invalid("field_mapping", "existing field has incompatible semantic history: "+f.Key)
			}
		} else if !errors.Is(err, kernel.ErrNotFound) {
			return a, p, err
		}
	}
	for _, m := range metrics {
		existing, err := c.Metric(ctx, sc, m.Key)
		if err == nil && !sameMetricHistory(existing, m) {
			return a, p, kernel.Invalid("metric_mapping", "namespace collision for metric: "+m.Key)
		} else if err != nil && !errors.Is(err, kernel.ErrNotFound) {
			return a, p, err
		}
	}

	if err := s.preflightUnification(ctx, sc, a, fields, metrics, canonical); err != nil {
		return a, p, err
	}
	return a, p, nil
}
func (s *Service) ApplyUnification(ctx context.Context, sc authz.Scope, in HistoryUnificationInput) (HistoryUnificationPreview, error) {
	if err := s.requireWrite(sc, kernel.NilID); err != nil {
		return HistoryUnificationPreview{}, err
	}
	if sc.Finance() != authz.FinanceFull || !sc.SeesAllProducts() {
		return HistoryUnificationPreview{}, kernel.ErrForbidden
	}
	c, err := s.canonicalStore()
	if err != nil {
		return HistoryUnificationPreview{}, err
	}
	tx, ok := c.Store.(TransactionalStore)
	if !ok {
		return HistoryUnificationPreview{}, kernel.ErrUnavailable
	}
	var result HistoryUnificationPreview
	err = tx.Transact(ctx, sc, func(ctx context.Context) error {
		if err := s.requireWrite(sc, kernel.NilID); err != nil {
			return err
		}
		if sc.Finance() != authz.FinanceFull || !sc.SeesAllProducts() {
			return kernel.ErrForbidden
		}
		if prior, err := c.Store.(HistoryStore).Unification(ctx, sc, in.Period); err == nil && prior.Digest == in.Digest && prior.BookProductID == in.BookProductID {
			if _, err := c.books.Snapshot(ctx, sc, in.BookProductID, in.Period.String(), 0); err != nil {
				return err
			}
			result = HistoryUnificationPreview{Digest: prior.Digest, BookProductID: prior.BookProductID, Period: prior.Period, BookVersions: len(prior.BookVersions), ModelVersions: len(prior.ModelVersions), CanonicalVersion: prior.BookVersions[len(prior.BookVersions)-1].Version, ModelVersion: in.ExpectedModelVersion, DefinitionKeys: []string{}, DefinitionMappings: map[string]string{}, PreviousPnL: []PnL{}, CanonicalPnL: []PnL{}, ExcludedModelFields: []string{}, Warnings: []string{}, Applied: true}
			for k, v := range prior.FieldKeys {
				result.DefinitionMappings[k] = v
				result.DefinitionKeys = append(result.DefinitionKeys, v)
			}
			for k, v := range prior.MetricKeys {
				result.DefinitionMappings[k] = v
				result.DefinitionKeys = append(result.DefinitionKeys, v)
			}
			sort.Strings(result.DefinitionKeys)
			return nil
		}
		a, p, err := s.prepareUnification(ctx, sc, in)
		if err != nil {
			return err
		}
		if in.Digest == "" || in.Digest != p.Digest {
			return kernel.ErrConflict
		}
		result = p
		if p.Applied {
			return nil
		}
		fields, metrics, err := s.migratedDefinitions(a)
		if err != nil {
			return err
		}
		for _, f := range fields {
			old, err := c.Field(ctx, sc, f.Key)
			if err == nil {
				if old.Latest().Type != f.Latest().Type || old.Latest().Currency != f.Latest().Currency || (strings.HasPrefix(f.Key, historyPrefix(a.BookProductID, a.Period)) && !sameFieldHistory(old, f)) {
					return kernel.Invalid("field_mapping", "field already has incompatible semantic history")
				}
				continue
			}
			if !errors.Is(err, kernel.ErrNotFound) {
				return err
			}
			for n := range f.Versions {
				next := f
				next.Versions = f.Versions[:n+1]
				if err := c.Store.SaveField(ctx, sc, next); err != nil {
					return err
				}
			}
		}
		for _, m := range metrics {
			old, err := c.Metric(ctx, sc, m.Key)
			if err == nil {
				if !sameMetricHistory(old, m) {
					return kernel.Invalid("metric_mapping", "namespaced metric already has different definition")
				}
				continue
			}
			if !errors.Is(err, kernel.ErrNotFound) {
				return err
			}
			for n := range m.Versions {
				next := m
				next.Versions = m.Versions[:n+1]
				if err := c.SaveMetric(ctx, sc, next); err != nil {
					return err
				}
			}
		}
		a.ID, a.Actor, a.At = kernel.NewID(), sc.Subject(), s.clock.Now().UTC()
		encodedArchive, err := json.Marshal(a)
		if err != nil {
			return fmt.Errorf("encode history archive: %w", err)
		}
		if len(encodedArchive) > maxHistoryArchiveBytes {
			return kernel.Invalid("history", "archive exceeds 32 MiB")
		}

		if err := s.logAccessChecked(ctx, sc, "write", "economics.unification", in.BookProductID, map[string]any{"period": in.Period.String(), "digest": a.Digest, "book_versions": len(a.BookVersions), "model_versions": len(a.ModelVersions)}); err != nil {
			return err
		}
		if err := c.Store.(HistoryStore).SaveUnification(ctx, sc, a); err != nil {
			return err
		}
		if err := s.emit(ctx, "economics.history.unified", a.ID, a.BookProductID, sc.Subject(), map[string]any{"period": a.Period.String(), "digest": a.Digest}); err != nil {
			return err
		}
		result.Applied = true
		return nil
	})
	return result, err
}
func sameFieldDefinition(a, b FieldVersion) bool {
	return a.Type == b.Type && a.Source == b.Source && a.Currency == b.Currency
}
func (s *Service) UnificationHistory(ctx context.Context, sc authz.Scope, p Period) (HistoryArchive, error) {
	if err := s.requireRead(sc, kernel.NilID, authz.FinanceFull); err != nil {
		return HistoryArchive{}, err
	}
	c, err := s.canonicalStore()
	if err != nil {
		return HistoryArchive{}, err
	}
	a, err := c.Store.(HistoryStore).Unification(ctx, sc, p)
	if err != nil {
		return a, err
	}
	if _, err := c.books.Snapshot(ctx, sc, a.BookProductID, p.String(), 0); err != nil {
		return HistoryArchive{}, err
	}
	if err := s.logAccessChecked(ctx, sc, "read", "economics.unification.history", a.BookProductID, map[string]any{"period": p.String()}); err != nil {
		return HistoryArchive{}, err
	}
	return a, nil
}

// CanonicalBookOwner returns the single current owner for a unified month.
func (s *Service) CanonicalBookOwner(ctx context.Context, sc authz.Scope, p Period) (kernel.ID, error) {
	if err := s.requireRead(sc, kernel.NilID, authz.FinanceFull); err != nil {
		return kernel.NilID, err
	}
	c, err := s.canonicalStore()
	if err != nil {
		return kernel.NilID, err
	}
	a, bound, err := c.binding(ctx, sc, p)
	if err != nil {
		return kernel.NilID, err
	}
	if !bound {
		return kernel.NilID, kernel.ErrNotFound
	}
	if _, err := c.books.Snapshot(ctx, sc, a.BookProductID, p.String(), 0); err != nil {
		return kernel.NilID, err
	}
	return a.BookProductID, nil
}

// TODO(question-53): pinned post-binding versions pin inputs, not a materialized report result.
// BookSnapshotUsesCommonDefinitions preserves original archived DSL only for
// superseded pre-binding versions. Post-binding versions pin inputs and use the
// period-effective common registry, even when explicitly selected later.
func (s *Service) BookSnapshotUsesCommonDefinitions(ctx context.Context, sc authz.Scope, owner kernel.ID, p Period, version int) (bool, error) {
	if err := s.requireRead(sc, owner, authz.FinanceFull); err != nil {
		return false, err
	}
	c, err := s.canonicalStore()
	if err != nil {
		return false, nil
	}
	a, bound, err := c.binding(ctx, sc, p)
	if err != nil || !bound || a.BookProductID != owner {
		return false, err
	}
	if version > a.OriginalBookVersion {
		return true, nil
	}
	latest, err := c.books.Snapshot(ctx, sc, owner, p.String(), 0)
	if err != nil {
		return false, err
	}
	return version == latest.Version, nil
}

// BookDefinitionsUnified lets the canonical owner guard legacy schema/formula writes.
func (s *Service) BookDefinitionsUnified(ctx context.Context, sc authz.Scope, owner kernel.ID, p Period) (bool, error) {
	if err := s.requireRead(sc, owner, authz.FinanceAggregates); err != nil {
		return false, err
	}
	c, err := s.canonicalStore()
	if err != nil {
		return false, nil
	}
	a, bound, err := c.binding(ctx, sc, p)
	if err != nil {
		return false, err
	}
	return bound && a.BookProductID == owner, nil
}

func (s *Service) previewAppliedArchive(ctx context.Context, sc authz.Scope, a HistoryArchive) (HistoryUnificationPreview, error) {
	p := HistoryUnificationPreview{Digest: a.Digest, BookProductID: a.BookProductID, Period: a.Period, BookVersions: len(a.BookVersions), ModelVersions: len(a.ModelVersions), CanonicalVersion: a.BookVersions[len(a.BookVersions)-1].Version, DefinitionKeys: []string{}, DefinitionMappings: map[string]string{}, PreviousPnL: []PnL{}, CanonicalPnL: []PnL{}, ExcludedModelFields: []string{}, Warnings: []string{"canonical owner already selected; archived originals remain immutable"}, Applied: true}
	for k, v := range a.FieldKeys {
		p.DefinitionMappings[k] = v
		p.DefinitionKeys = append(p.DefinitionKeys, v)
	}
	for k, v := range a.MetricKeys {
		p.DefinitionMappings[k] = v
		p.DefinitionKeys = append(p.DefinitionKeys, v)
	}
	sort.Strings(p.DefinitionKeys)
	for _, version := range a.ModelVersions {
		if version.Batch.Status == BatchApplied && version.Batch.DataVersion > p.ModelVersion {
			p.ModelVersion = version.Batch.DataVersion
		}
	}
	c, err := s.canonicalStore()
	if err != nil {
		return p, err
	}
	report, err := c.books.Report(ctx, sc, book.ReportInput{ProductID: a.BookProductID, Period: a.Period.String()})
	if err != nil {
		return p, err
	}
	for _, pr := range report.Products {
		p.CanonicalPnL = append(p.CanonicalPnL, canonicalProductPnL(pr, a.Period, report.Currency))
	}
	return p, nil
}

func canonicalDefinitionKey(prefix, raw string) string {
	if raw == strings.ToLower(raw) && raw != "" && raw[0] >= 'a' && raw[0] <= 'z' {
		return prefix + raw
	}
	sum := sha256.Sum256([]byte(raw))
	return prefix + strings.ToLower(raw) + "_" + hex.EncodeToString(sum[:])
}

func sameFieldHistory(a, b Field) bool {
	if len(a.Versions) != len(b.Versions) {
		return false
	}
	for n, x := range a.Versions {
		y := b.Versions[n]
		if x.Version != y.Version || x.Name != y.Name || x.Type != y.Type || x.Source != y.Source || x.Currency != y.Currency || x.EffectiveFrom.String() != y.EffectiveFrom.String() || !reflect.DeepEqual(x.Dimensions, y.Dimensions) {
			return false
		}
	}
	return true
}
func sameMetricHistory(a, b Metric) bool {
	if len(a.Versions) != len(b.Versions) {
		return false
	}
	for n, x := range a.Versions {
		y := b.Versions[n]
		if x.Version != y.Version || x.Name != y.Name || x.Expression != y.Expression || x.Currency != y.Currency || x.ResultType != y.ResultType || x.EffectiveFrom.String() != y.EffectiveFrom.String() || !reflect.DeepEqual(x.Refs, y.Refs) {
			return false
		}
	}
	return true
}

const maxHistoryArchiveBytes = 32 << 20

// historyByteBudget rejects a source revision before retaining it in the aggregate archive.
// One separator byte per charge and the initial header reserve make the budget conservative.
type historyByteBudget struct{ remaining int }

func (b *historyByteBudget) charge(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode history component: %w", err)
	}
	if len(encoded) >= b.remaining {
		return kernel.Invalid("history", "archive exceeds 32 MiB")
	}
	b.remaining -= len(encoded) + 1
	return nil
}
