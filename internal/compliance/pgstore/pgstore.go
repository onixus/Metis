// Package pgstore — compliance.Store и compliance.EvidenceStore на PostgreSQL (схема compliance).
// Журнал доказательств и история оценок влияния — таблицы только для INSERT (инвариант 8).
package pgstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onixus/metis/internal/compliance"
	"github.com/onixus/metis/internal/compliance/internal/db"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/portfoliograph"
)

// Store — хранилище каталога, шаблонов, треков, оценок влияния и baseline.
type Store struct {
	db *pgdb.DB
}

var _ compliance.Store = (*Store)(nil)

// New создаёт хранилище на схеме compliance.
func New(d *pgdb.DB) *Store { return &Store{db: d} }

func (s *Store) q(ctx context.Context) *db.Queries { return db.New(pgdb.Querier(ctx, s.db)) }

// SaveRequirementSet создаёт или обновляет набор требований.
func (s *Store) SaveRequirementSet(ctx context.Context, rs compliance.RequirementSet) error {
	items := rs.Items
	if items == nil {
		items = []compliance.RequirementItem{}
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return fmt.Errorf("compliance requirement set %s: items: %w", rs.ID, err)
	}
	err = s.q(ctx).UpsertRequirementSet(ctx, db.UpsertRequirementSetParams{
		ID: rs.ID, Code: rs.Code, Version: int64(rs.Version), ProductType: string(rs.ProductType), Items: raw, Status: string(rs.Status),
		CreatedBy: rs.CreatedBy, CreatedAt: rs.CreatedAt.UTC(), UpdatedAt: rs.UpdatedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("compliance requirement set %s: %w", rs.ID, pgdb.MapError(err))
	}
	return nil
}

// RequirementSet возвращает набор.
func (s *Store) RequirementSet(ctx context.Context, id kernel.ID) (compliance.RequirementSet, error) {
	r, err := s.q(ctx).GetRequirementSet(ctx, id)
	if err != nil {
		return compliance.RequirementSet{}, fmt.Errorf("compliance requirement set %s: %w", id, pgdb.MapError(err))
	}
	return setFromRow(r)
}

// RequirementSets возвращает наборы по коду (пустой — все) по возрастанию версии.
func (s *Store) RequirementSets(ctx context.Context, code string) ([]compliance.RequirementSet, error) {
	rows, err := s.q(ctx).ListRequirementSets(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("compliance requirement sets: %w", pgdb.MapError(err))
	}
	out := make([]compliance.RequirementSet, 0, len(rows))
	for _, r := range rows {
		rs, err := setFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, rs)
	}
	return out, nil
}

func setFromRow(r db.ComplianceRequirementSet) (compliance.RequirementSet, error) {
	items := []compliance.RequirementItem{}
	if len(r.Items) > 0 {
		if err := json.Unmarshal(r.Items, &items); err != nil {
			return compliance.RequirementSet{}, fmt.Errorf("compliance requirement set %s: items: %w", r.ID, err)
		}
	}
	return compliance.RequirementSet{
		ID: r.ID, Code: r.Code, Version: int(r.Version), ProductType: portfoliograph.ProductType(r.ProductType), Items: items,
		Status: compliance.RequirementSetStatus(r.Status), CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}, nil
}

// SaveTemplate создаёт или обновляет шаблон трека; гейты — jsonb.
func (s *Store) SaveTemplate(ctx context.Context, t compliance.TrackTemplate) error {
	gates := t.Gates
	if gates == nil {
		gates = []compliance.GateTemplate{}
	}
	raw, err := json.Marshal(gates)
	if err != nil {
		return fmt.Errorf("compliance template %s: gates: %w", t.ID, err)
	}
	err = s.q(ctx).UpsertTemplate(ctx, db.UpsertTemplateParams{
		ID: t.ID, ProductType: string(t.ProductType), Name: t.Name, Gates: raw, CreatedAt: t.CreatedAt.UTC(), UpdatedAt: t.UpdatedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("compliance template %s: %w", t.ID, pgdb.MapError(err))
	}
	return nil
}

// Template возвращает шаблон.
func (s *Store) Template(ctx context.Context, id kernel.ID) (compliance.TrackTemplate, error) {
	r, err := s.q(ctx).GetTemplate(ctx, id)
	if err != nil {
		return compliance.TrackTemplate{}, fmt.Errorf("compliance template %s: %w", id, pgdb.MapError(err))
	}
	return templateFromRow(r)
}

// Templates возвращает шаблоны типа продукта (пустой — все) в порядке создания.
func (s *Store) Templates(ctx context.Context, pt portfoliograph.ProductType) ([]compliance.TrackTemplate, error) {
	rows, err := s.q(ctx).ListTemplates(ctx, string(pt))
	if err != nil {
		return nil, fmt.Errorf("compliance templates: %w", pgdb.MapError(err))
	}
	out := make([]compliance.TrackTemplate, 0, len(rows))
	for _, r := range rows {
		t, err := templateFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func templateFromRow(r db.ComplianceTrackTemplate) (compliance.TrackTemplate, error) {
	gates := []compliance.GateTemplate{}
	if len(r.Gates) > 0 {
		if err := json.Unmarshal(r.Gates, &gates); err != nil {
			return compliance.TrackTemplate{}, fmt.Errorf("compliance template %s: gates: %w", r.ID, err)
		}
	}
	return compliance.TrackTemplate{
		ID: r.ID, ProductType: portfoliograph.ProductType(r.ProductType), Name: r.Name, Gates: gates,
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}, nil
}

// SaveTrack создаёт или обновляет трек; гейты с чек-листами — jsonb.
func (s *Store) SaveTrack(ctx context.Context, t compliance.Track) error {
	gates := make([]compliance.Gate, len(t.Gates))
	for i, g := range t.Gates {
		g.PassedAt = g.PassedAt.UTC()
		gates[i] = g
	}
	raw, err := json.Marshal(gates)
	if err != nil {
		return fmt.Errorf("compliance track %s: gates: %w", t.ID, err)
	}
	err = s.q(ctx).UpsertTrack(ctx, db.UpsertTrackParams{
		ID: t.ID, ProductID: t.ProductID, ReleaseID: t.ReleaseID, Version: t.Version, TemplateID: t.TemplateID, Status: string(t.Status),
		Gates: raw, BaselineID: pgdb.NullID(t.BaselineID), CreatedBy: t.CreatedBy, CreatedAt: t.CreatedAt.UTC(), UpdatedAt: t.UpdatedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("compliance track %s: %w", t.ID, pgdb.MapError(err))
	}
	return nil
}

// Track возвращает трек.
func (s *Store) Track(ctx context.Context, id kernel.ID) (compliance.Track, error) {
	r, err := s.q(ctx).GetTrack(ctx, id)
	if err != nil {
		return compliance.Track{}, fmt.Errorf("compliance track %s: %w", id, pgdb.MapError(err))
	}
	return trackFromRow(r)
}

// Tracks возвращает треки по фильтру в порядке создания.
func (s *Store) Tracks(ctx context.Context, f compliance.TrackFilter) ([]compliance.Track, error) {
	rows, err := s.q(ctx).ListTracks(ctx, db.ListTracksParams{ProductID: pgdb.NullID(f.ProductID), ReleaseID: pgdb.NullID(f.ReleaseID)})
	if err != nil {
		return nil, fmt.Errorf("compliance tracks: %w", pgdb.MapError(err))
	}
	out := make([]compliance.Track, 0, len(rows))
	for _, r := range rows {
		t, err := trackFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func trackFromRow(r db.ComplianceTrack) (compliance.Track, error) {
	gates := []compliance.Gate{}
	if len(r.Gates) > 0 {
		if err := json.Unmarshal(r.Gates, &gates); err != nil {
			return compliance.Track{}, fmt.Errorf("compliance track %s: gates: %w", r.ID, err)
		}
	}
	for i := range gates {
		gates[i].PassedAt = gates[i].PassedAt.UTC()
		if gates[i].Checklist == nil {
			gates[i].Checklist = []compliance.ChecklistItem{}
		}
	}
	return compliance.Track{
		ID: r.ID, ProductID: r.ProductID, ReleaseID: r.ReleaseID, Version: r.Version, TemplateID: r.TemplateID,
		Status: compliance.TrackStatus(r.Status), Gates: gates, BaselineID: r.BaselineID.UUID,
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}, nil
}

// AppendImpact добавляет оценку класса влияния (только INSERT).
func (s *Store) AppendImpact(ctx context.Context, a compliance.ImpactAssessment) error {
	err := s.q(ctx).InsertImpact(ctx, db.InsertImpactParams{
		ID: a.ID, FeatureID: a.FeatureID, ProductID: a.ProductID, Class: string(a.Class), Justification: a.Justification,
		Author: a.Author, At: a.At.UTC(),
	})
	if err != nil {
		return fmt.Errorf("compliance impact %s: %w", a.ID, pgdb.MapError(err))
	}
	return nil
}

// ImpactHistory возвращает оценки фичи в порядке добавления.
func (s *Store) ImpactHistory(ctx context.Context, featureID kernel.ID) ([]compliance.ImpactAssessment, error) {
	rows, err := s.q(ctx).ListImpactHistory(ctx, featureID)
	if err != nil {
		return nil, fmt.Errorf("compliance impact history %s: %w", featureID, pgdb.MapError(err))
	}
	out := make([]compliance.ImpactAssessment, 0, len(rows))
	for _, r := range rows {
		out = append(out, compliance.ImpactAssessment{
			ID: r.ID, FeatureID: r.FeatureID, ProductID: r.ProductID, Class: compliance.ImpactClass(r.Class),
			Justification: r.Justification, Author: r.Author, At: r.At.UTC(),
		})
	}
	return out, nil
}

// SaveBaseline создаёт или обновляет baseline.
func (s *Store) SaveBaseline(ctx context.Context, b compliance.CertifiedBaseline) error {
	err := s.q(ctx).UpsertBaseline(ctx, db.UpsertBaselineParams{
		ID: b.ID, ProductID: b.ProductID, TrackID: pgdb.NullID(b.TrackID), Version: b.Version, RequirementSetID: pgdb.NullID(b.RequirementSetID),
		CertificateNo: b.CertificateNo, CertifiedAt: pgdb.ToDate(b.CertifiedAt), Eol: pgdb.ToDate(b.EOL), CreatedAt: b.CreatedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("compliance baseline %s: %w", b.ID, pgdb.MapError(err))
	}
	return nil
}

// Baseline возвращает baseline.
func (s *Store) Baseline(ctx context.Context, id kernel.ID) (compliance.CertifiedBaseline, error) {
	r, err := s.q(ctx).GetBaseline(ctx, id)
	if err != nil {
		return compliance.CertifiedBaseline{}, fmt.Errorf("compliance baseline %s: %w", id, pgdb.MapError(err))
	}
	return baselineFromRow(r), nil
}

// Baselines возвращает baseline продукта (NilID — все) в порядке создания.
func (s *Store) Baselines(ctx context.Context, productID kernel.ID) ([]compliance.CertifiedBaseline, error) {
	rows, err := s.q(ctx).ListBaselines(ctx, pgdb.NullID(productID))
	if err != nil {
		return nil, fmt.Errorf("compliance baselines: %w", pgdb.MapError(err))
	}
	out := make([]compliance.CertifiedBaseline, 0, len(rows))
	for _, r := range rows {
		out = append(out, baselineFromRow(r))
	}
	return out, nil
}

func baselineFromRow(r db.ComplianceBaseline) compliance.CertifiedBaseline {
	return compliance.CertifiedBaseline{
		ID: r.ID, ProductID: r.ProductID, TrackID: r.TrackID.UUID, Version: r.Version, RequirementSetID: r.RequirementSetID.UUID,
		CertificateNo: r.CertificateNo, CertifiedAt: pgdb.FromDate(r.CertifiedAt), EOL: pgdb.FromDate(r.Eol), CreatedAt: r.CreatedAt.UTC(),
	}
}
