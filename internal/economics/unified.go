package economics

import (
	"context"
	"time"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// DefinitionPolicy prevents a second independent definition store after explicit unification.
type DefinitionPolicy interface {
	ValidateFinanceDefinitions(context.Context, authz.Scope, kernel.ID, string, []Field, []Field) error
}

// CommonDefinitionPolicy identifies books whose current calculated fields are
// owned by the common registry. Historical pinned snapshots keep their DSL.
type CommonDefinitionPolicy interface {
	CommonFinanceDefinitions(context.Context, authz.Scope, kernel.ID, string, int) (bool, error)
}

// ReportEvaluator enriches the legacy book response using the common definition registry.
type ReportEvaluator interface {
	EnrichFinanceReport(context.Context, authz.Scope, ReportInput, *Report) error
}

func (s *Service) WithDefinitionPolicy(p DefinitionPolicy) *Service { s.definitionPolicy = p; return s }
func (s *Service) WithReportEvaluator(p ReportEvaluator) *Service   { s.reportEvaluator = p; return s }

// SourceSnapshotRef is source-neutral lineage retained by common book reports.
type SourceSnapshotRef struct {
	Kind          string    `json:"kind"`
	SnapshotID    string    `json:"snapshot_id"`
	AsOf          time.Time `json:"as_of"`
	CapturedValue string    `json:"captured_value"`
	Overridden    bool      `json:"overridden"`
	Currency      string    `json:"currency"`
}

// CanonicalManualSource is reserved for application-owned zero-cost model input rows.
const CanonicalManualSource = "metis:canonical-manual"

// candidateSnapshotKey is private: only Save can stage a trusted, validated book.
// Snapshot reads still enforce the caller's original financial authorization.
type candidateSnapshotKey struct{}

func (s *Service) readSnapshot(ctx context.Context, sc authz.Scope, owner kernel.ID, period string, version int) (Snapshot, error) {
	if candidate, ok := ctx.Value(candidateSnapshotKey{}).(Snapshot); ok && candidate.ProductID == owner && candidate.Period == period && (version == 0 || version == candidate.Version) {
		if err := RequireSnapshot(sc, candidate, authz.ActionReadFinance); err != nil {
			return Snapshot{}, err
		}
		return cloneSnapshot(candidate)
	}
	return s.store.Snapshot(ctx, sc, owner, period, version)
}

func (s *Service) usesCommonDefinitions(ctx context.Context, sc authz.Scope, snapshot Snapshot) (bool, error) {
	policy, ok := s.definitionPolicy.(CommonDefinitionPolicy)
	if !ok {
		return false, nil
	}
	return policy.CommonFinanceDefinitions(ctx, sc, snapshot.ProductID, snapshot.Period, snapshot.Version)
}

func (s *Service) calculateSnapshot(ctx context.Context, sc authz.Scope, snapshot Snapshot, in ReportInput) (Report, error) {
	common, err := s.usesCommonDefinitions(ctx, sc, snapshot)
	if err != nil {
		return Report{}, err
	}
	if !common {
		return calculate(snapshot, in)
	}
	return calculateMode(snapshot, in, true)
}
