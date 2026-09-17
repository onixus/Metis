package discovery

import (
	"context"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/signals"
)

// SignalReader — нужная discovery часть публичного интерфейса signals (DS-04, SG-04).
// Реализуется *signals.Service; авторизацию по продукту сигнала выполняет signals.
type SignalReader interface {
	Signal(ctx context.Context, sc authz.Scope, id kernel.ID) (signals.Signal, error)
	SignalsByHypothesis(ctx context.Context, sc authz.Scope, hypothesisID kernel.ID) ([]signals.Signal, error)
}

// SignalMerger — слияние дубликатов сигналов (SG-04). Реализуется *signals.Service.
type SignalMerger interface {
	Merge(ctx context.Context, sc authz.Scope, targetID kernel.ID, dupIDs []kernel.ID) error
}

// SignalLinker — привязка сигнала к гипотезе (DS-01). Реализуется *signals.Service.
type SignalLinker interface {
	LinkToHypothesis(ctx context.Context, sc authz.Scope, id, hypothesisID kernel.ID) (signals.Signal, error)
}

// FeatureReader — нужная discovery часть публичного интерфейса portfoliograph.
// Реализуется *portfoliograph.Service.
type FeatureReader interface {
	Feature(ctx context.Context, sc authz.Scope, id kernel.ID) (portfoliograph.Feature, error)
}

// DecisionLinks — решения, ссылающиеся на узел (DS-04, DA-01). Реализуется модулем decisions;
// kind — TraceKind в строковом виде. Nil-порт допустим: трассировка без решений.
type DecisionLinks interface {
	DecisionsFor(ctx context.Context, sc authz.Scope, kind string, id kernel.ID) ([]DecisionRef, error)
}

// SimilarityIndex — индекс похожести текстов (SG-04). Реализации: MemIndex (TF-IDF, память),
// pgvector в pgstore (следующая волна). kind — вид документа (signals.IndexKindSignal).
type SimilarityIndex interface {
	Upsert(ctx context.Context, kind string, id, productID kernel.ID, text string) error
	Similar(ctx context.Context, kind string, productID kernel.ID, text string, limit int) ([]Match, error)
}

var (
	_ SignalReader    = (*signals.Service)(nil)
	_ SignalMerger    = (*signals.Service)(nil)
	_ SignalLinker    = (*signals.Service)(nil)
	_ FeatureReader   = (*portfoliograph.Service)(nil)
	_ SimilarityIndex = (*MemIndex)(nil)
	_ signals.Indexer = (*MemIndex)(nil)
)
