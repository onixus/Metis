package pgstore

import (
	"context"
	"fmt"

	"github.com/onixus/metis/internal/discovery"
	"github.com/onixus/metis/internal/discovery/internal/db"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/signals"
)

// PGIndex — индекс похожести (SG-04) на полнотекстовом поиске PostgreSQL: таблица
// discovery.embeddings с tsvector (конфигурация 'russian'). Запрос строится как дизъюнкция лексем
// текста, оценка — ts_rank_cd с нормализацией по длине документа и rank/(rank+1) ∈ [0, 1). Документы разделены по виду
// и продукту. Модели эмбеддингов на этапе 2 нет (AI-10 — этап 5), поэтому колонка vector не
// добавлена — docs/questions.md №24.
type PGIndex struct {
	db *pgdb.DB
}

var (
	_ discovery.SimilarityIndex = (*PGIndex)(nil)
	_ signals.Indexer           = (*PGIndex)(nil)
)

// NewIndex создаёт индекс на таблице discovery.embeddings.
func NewIndex(d *pgdb.DB) *PGIndex { return &PGIndex{db: d} }

func (x *PGIndex) q(ctx context.Context) *db.Queries { return db.New(pgdb.Querier(ctx, x.db)) }

// Upsert добавляет или заменяет документ. Текст без токенов удаляет документ из индекса.
func (x *PGIndex) Upsert(ctx context.Context, kind string, id, productID kernel.ID, text string) error {
	if kind == "" {
		return kernel.Invalid("kind", "обязателен")
	}
	if id == kernel.NilID || productID == kernel.NilID {
		return kernel.Invalid("id", "идентификаторы документа и продукта обязательны")
	}
	if len(discovery.Tokenize(text)) == 0 {
		if err := x.q(ctx).DeleteEmbedding(ctx, db.DeleteEmbeddingParams{Kind: kind, ID: id}); err != nil {
			return fmt.Errorf("discovery index delete %s/%s: %w", kind, id, pgdb.MapError(err))
		}
		return nil
	}
	err := x.q(ctx).UpsertEmbedding(ctx, db.UpsertEmbeddingParams{Kind: kind, ID: id, ProductID: productID, Text: text})
	if err != nil {
		return fmt.Errorf("discovery index upsert %s/%s: %w", kind, id, pgdb.MapError(err))
	}
	return nil
}

// Similar возвращает до limit документов вида kind продукта productID, ближайших к тексту
// по полнотекстовому рангу; документы без общих лексем не возвращаются. limit ≤ 0 — без ограничения.
func (x *PGIndex) Similar(ctx context.Context, kind string, productID kernel.ID, text string, limit int) ([]discovery.Match, error) {
	if len(discovery.Tokenize(text)) == 0 {
		return nil, nil
	}
	q := x.q(ctx)
	lexemes, err := q.Lexemes(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("discovery index lexemes: %w", pgdb.MapError(err))
	}
	if lexemes == "" {
		return nil, nil
	}
	var lim int64 = 1<<31 - 1
	if limit > 0 {
		lim = int64(limit)
	}
	rows, err := q.SimilarEmbeddings(ctx, db.SimilarEmbeddingsParams{Lexemes: lexemes, Kind: kind, ProductID: productID, Lim: lim})
	if err != nil {
		return nil, fmt.Errorf("discovery index similar: %w", pgdb.MapError(err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out := make([]discovery.Match, 0, len(rows))
	for _, r := range rows {
		out = append(out, discovery.Match{ID: r.ID, Score: r.Score})
	}
	return out, nil
}
