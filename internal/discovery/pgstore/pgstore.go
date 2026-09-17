// Package pgstore — discovery.Store и discovery.SimilarityIndex на PostgreSQL (схема discovery).
package pgstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onixus/metis/internal/discovery"
	"github.com/onixus/metis/internal/discovery/internal/db"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
)

// Store — хранилище гипотез, интервью, инсайтов, evidence и настроек AD-03.
type Store struct {
	db *pgdb.DB
}

var _ discovery.Store = (*Store)(nil)

// New создаёт хранилище на схеме discovery.
func New(d *pgdb.DB) *Store { return &Store{db: d} }

func (s *Store) q(ctx context.Context) *db.Queries { return db.New(pgdb.Querier(ctx, s.db)) }

// SaveHypothesis создаёт или обновляет гипотезу.
func (s *Store) SaveHypothesis(ctx context.Context, h discovery.Hypothesis) error {
	var custom []byte
	if h.CustomFields != nil {
		raw, err := json.Marshal(h.CustomFields)
		if err != nil {
			return fmt.Errorf("discovery hypothesis %s: custom fields: %w", h.ID, err)
		}
		custom = raw
	}
	err := s.q(ctx).UpsertHypothesis(ctx, db.UpsertHypothesisParams{
		ID: h.ID, ProductID: h.ProductID, Title: h.Title, Statement: h.Statement, Assumptions: pgdb.Strings(h.Assumptions),
		ConfirmationCriterion: h.ConfirmationCriterion, Status: string(h.Status), Resolution: h.Resolution,
		FeatureID: pgdb.NullID(h.FeatureID), CustomFields: custom, CreatedBy: h.CreatedBy,
		CreatedAt: h.CreatedAt.UTC(), UpdatedAt: h.UpdatedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("discovery hypothesis %s: %w", h.ID, pgdb.MapError(err))
	}
	return nil
}

// Hypothesis возвращает гипотезу.
func (s *Store) Hypothesis(ctx context.Context, id kernel.ID) (discovery.Hypothesis, error) {
	r, err := s.q(ctx).GetHypothesis(ctx, id)
	if err != nil {
		return discovery.Hypothesis{}, fmt.Errorf("discovery hypothesis %s: %w", id, pgdb.MapError(err))
	}
	return hypothesisFromRow(r)
}

// Hypotheses возвращает гипотезы по фильтру в порядке создания.
func (s *Store) Hypotheses(ctx context.Context, f discovery.HypothesisFilter) ([]discovery.Hypothesis, error) {
	statuses := make([]string, 0, len(f.Statuses))
	for _, st := range f.Statuses {
		statuses = append(statuses, string(st))
	}
	rows, err := s.q(ctx).ListHypotheses(ctx, db.ListHypothesesParams{ProductID: pgdb.NullID(f.ProductID), FeatureID: pgdb.NullID(f.FeatureID), Statuses: statuses})
	if err != nil {
		return nil, fmt.Errorf("discovery hypotheses: %w", pgdb.MapError(err))
	}
	out := make([]discovery.Hypothesis, 0, len(rows))
	for _, r := range rows {
		h, err := hypothesisFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}

func hypothesisFromRow(r db.DiscoveryHypothesis) (discovery.Hypothesis, error) {
	h := discovery.Hypothesis{
		ID: r.ID, ProductID: r.ProductID, Title: r.Title, Statement: r.Statement, Assumptions: pgdb.NilIfEmptyStrings(r.Assumptions),
		ConfirmationCriterion: r.ConfirmationCriterion, Status: discovery.HypothesisStatus(r.Status), Resolution: r.Resolution,
		FeatureID: r.FeatureID.UUID, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
	if len(r.CustomFields) > 0 && string(r.CustomFields) != "null" {
		if err := json.Unmarshal(r.CustomFields, &h.CustomFields); err != nil {
			return discovery.Hypothesis{}, fmt.Errorf("discovery hypothesis %s: custom fields: %w", r.ID, err)
		}
	}
	return h, nil
}

// SaveInterview создаёт или обновляет интервью.
func (s *Store) SaveInterview(ctx context.Context, i discovery.Interview) error {
	err := s.q(ctx).UpsertInterview(ctx, db.UpsertInterviewParams{
		ID: i.ID, ProductID: i.ProductID, AccountID: i.AccountID, Segment: i.Segment, Date: pgdb.ToDate(i.Date),
		Participants: pgdb.Strings(i.Participants), Notes: i.Notes, HypothesisIds: pgdb.IDs(i.HypothesisIDs),
		CreatedBy: i.CreatedBy, CreatedAt: i.CreatedAt.UTC(), UpdatedAt: i.UpdatedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("discovery interview %s: %w", i.ID, pgdb.MapError(err))
	}
	return nil
}

// Interview возвращает интервью.
func (s *Store) Interview(ctx context.Context, id kernel.ID) (discovery.Interview, error) {
	r, err := s.q(ctx).GetInterview(ctx, id)
	if err != nil {
		return discovery.Interview{}, fmt.Errorf("discovery interview %s: %w", id, pgdb.MapError(err))
	}
	return interviewFromRow(r), nil
}

// Interviews возвращает интервью продукта (NilID — все) в порядке создания.
func (s *Store) Interviews(ctx context.Context, productID kernel.ID) ([]discovery.Interview, error) {
	rows, err := s.q(ctx).ListInterviews(ctx, pgdb.NullID(productID))
	if err != nil {
		return nil, fmt.Errorf("discovery interviews: %w", pgdb.MapError(err))
	}
	out := make([]discovery.Interview, 0, len(rows))
	for _, r := range rows {
		out = append(out, interviewFromRow(r))
	}
	return out, nil
}

func interviewFromRow(r db.DiscoveryInterview) discovery.Interview {
	return discovery.Interview{
		ID: r.ID, ProductID: r.ProductID, AccountID: r.AccountID, Segment: r.Segment, Date: pgdb.FromDate(r.Date),
		Participants: pgdb.NilIfEmptyStrings(r.Participants), Notes: r.Notes, HypothesisIDs: pgdb.NilIfEmptyIDs(r.HypothesisIds),
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}

// SaveInsight создаёт или обновляет инсайт.
func (s *Store) SaveInsight(ctx context.Context, i discovery.Insight) error {
	err := s.q(ctx).UpsertInsight(ctx, db.UpsertInsightParams{
		ID: i.ID, ProductID: i.ProductID, Text: i.Text, InterviewID: pgdb.NullID(i.InterviewID),
		HypothesisIds: pgdb.IDs(i.HypothesisIDs), SignalIds: pgdb.IDs(i.SignalIDs), Confidence: string(i.Confidence),
		CreatedBy: i.CreatedBy, CreatedAt: i.CreatedAt.UTC(), UpdatedAt: i.UpdatedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("discovery insight %s: %w", i.ID, pgdb.MapError(err))
	}
	return nil
}

// Insight возвращает инсайт.
func (s *Store) Insight(ctx context.Context, id kernel.ID) (discovery.Insight, error) {
	r, err := s.q(ctx).GetInsight(ctx, id)
	if err != nil {
		return discovery.Insight{}, fmt.Errorf("discovery insight %s: %w", id, pgdb.MapError(err))
	}
	return insightFromRow(r), nil
}

// Insights возвращает инсайты по фильтру в порядке создания.
func (s *Store) Insights(ctx context.Context, f discovery.InsightFilter) ([]discovery.Insight, error) {
	rows, err := s.q(ctx).ListInsights(ctx, db.ListInsightsParams{
		ProductID: pgdb.NullID(f.ProductID), InterviewID: pgdb.NullID(f.InterviewID),
		HypothesisID: pgdb.NullID(f.HypothesisID), SignalID: pgdb.NullID(f.SignalID),
	})
	if err != nil {
		return nil, fmt.Errorf("discovery insights: %w", pgdb.MapError(err))
	}
	out := make([]discovery.Insight, 0, len(rows))
	for _, r := range rows {
		out = append(out, insightFromRow(r))
	}
	return out, nil
}

func insightFromRow(r db.DiscoveryInsight) discovery.Insight {
	return discovery.Insight{
		ID: r.ID, ProductID: r.ProductID, Text: r.Text, InterviewID: r.InterviewID.UUID,
		HypothesisIDs: pgdb.NilIfEmptyIDs(r.HypothesisIds), SignalIDs: pgdb.NilIfEmptyIDs(r.SignalIds),
		Confidence: discovery.Confidence(r.Confidence), CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}

// SaveEvidence создаёт или обновляет evidence.
func (s *Store) SaveEvidence(ctx context.Context, e discovery.Evidence) error {
	err := s.q(ctx).UpsertEvidence(ctx, db.UpsertEvidenceParams{
		ID: e.ID, ProductID: e.ProductID, Source: e.Source, SourceRef: e.SourceRef, Date: pgdb.ToDate(e.Date),
		Trust: string(e.Trust), Verification: string(e.Verification), Sha256: e.SHA256,
		HypothesisID: pgdb.NullID(e.HypothesisID), InsightID: pgdb.NullID(e.InsightID), FeatureID: pgdb.NullID(e.FeatureID),
		CreatedBy: e.CreatedBy, CreatedAt: e.CreatedAt.UTC(), UpdatedAt: e.UpdatedAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("discovery evidence %s: %w", e.ID, pgdb.MapError(err))
	}
	return nil
}

// Evidence возвращает evidence.
func (s *Store) Evidence(ctx context.Context, id kernel.ID) (discovery.Evidence, error) {
	r, err := s.q(ctx).GetEvidence(ctx, id)
	if err != nil {
		return discovery.Evidence{}, fmt.Errorf("discovery evidence %s: %w", id, pgdb.MapError(err))
	}
	return evidenceFromRow(r), nil
}

// EvidenceList возвращает evidence по фильтру в порядке создания.
func (s *Store) EvidenceList(ctx context.Context, f discovery.EvidenceFilter) ([]discovery.Evidence, error) {
	rows, err := s.q(ctx).ListEvidence(ctx, db.ListEvidenceParams{
		ProductID: pgdb.NullID(f.ProductID), HypothesisID: pgdb.NullID(f.HypothesisID), InsightID: pgdb.NullID(f.InsightID),
		FeatureID: pgdb.NullID(f.FeatureID), Verification: string(f.Verification),
	})
	if err != nil {
		return nil, fmt.Errorf("discovery evidence list: %w", pgdb.MapError(err))
	}
	out := make([]discovery.Evidence, 0, len(rows))
	for _, r := range rows {
		out = append(out, evidenceFromRow(r))
	}
	return out, nil
}

func evidenceFromRow(r db.DiscoveryEvidence) discovery.Evidence {
	return discovery.Evidence{
		ID: r.ID, ProductID: r.ProductID, Source: r.Source, SourceRef: r.SourceRef, Date: pgdb.FromDate(r.Date),
		Trust: discovery.Confidence(r.Trust), Verification: discovery.Verification(r.Verification), SHA256: r.Sha256,
		HypothesisID: r.HypothesisID.UUID, InsightID: r.InsightID.UUID, FeatureID: r.FeatureID.UUID,
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}

// SaveFieldDef создаёт или обновляет определение поля; ключ уникален в пределах сущности.
func (s *Store) SaveFieldDef(ctx context.Context, d discovery.CustomFieldDef) error {
	err := s.q(ctx).UpsertFieldDef(ctx, db.UpsertFieldDefParams{
		ID: d.ID, Entity: string(d.Entity), Key: d.Key, Label: d.Label, Type: string(d.Type), Options: pgdb.Strings(d.Options), Required: d.Required,
	})
	if err != nil {
		return fmt.Errorf("discovery field def %s.%s: %w", d.Entity, d.Key, pgdb.MapError(err))
	}
	return nil
}

// FieldDefs возвращает определения полей сущности в порядке создания.
func (s *Store) FieldDefs(ctx context.Context, e discovery.Entity) ([]discovery.CustomFieldDef, error) {
	rows, err := s.q(ctx).ListFieldDefs(ctx, string(e))
	if err != nil {
		return nil, fmt.Errorf("discovery field defs %s: %w", e, pgdb.MapError(err))
	}
	out := make([]discovery.CustomFieldDef, 0, len(rows))
	for _, r := range rows {
		out = append(out, discovery.CustomFieldDef{
			ID: r.ID, Entity: discovery.Entity(r.Entity), Key: r.Key, Label: r.Label, Type: discovery.FieldType(r.Type),
			Options: pgdb.NilIfEmptyStrings(r.Options), Required: r.Required,
		})
	}
	return out, nil
}

// SaveStatusDef создаёт или обновляет пользовательский статус.
func (s *Store) SaveStatusDef(ctx context.Context, d discovery.CustomStatusDef) error {
	err := s.q(ctx).UpsertStatusDef(ctx, db.UpsertStatusDefParams{Entity: string(d.Entity), Key: d.Key, Label: d.Label, Category: d.Category})
	if err != nil {
		return fmt.Errorf("discovery status def %s.%s: %w", d.Entity, d.Key, pgdb.MapError(err))
	}
	return nil
}

// StatusDefs возвращает пользовательские статусы сущности в порядке создания.
func (s *Store) StatusDefs(ctx context.Context, e discovery.Entity) ([]discovery.CustomStatusDef, error) {
	rows, err := s.q(ctx).ListStatusDefs(ctx, string(e))
	if err != nil {
		return nil, fmt.Errorf("discovery status defs %s: %w", e, pgdb.MapError(err))
	}
	out := make([]discovery.CustomStatusDef, 0, len(rows))
	for _, r := range rows {
		out = append(out, discovery.CustomStatusDef{Entity: discovery.Entity(r.Entity), Key: r.Key, Label: r.Label, Category: r.Category})
	}
	return out, nil
}
