-- name: UpsertHypothesis :exec
INSERT INTO discovery.hypotheses (id, product_id, title, statement, assumptions, confirmation_criterion, status, resolution, feature_id, custom_fields, created_by, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (id) DO UPDATE SET product_id = EXCLUDED.product_id, title = EXCLUDED.title, statement = EXCLUDED.statement,
  assumptions = EXCLUDED.assumptions, confirmation_criterion = EXCLUDED.confirmation_criterion, status = EXCLUDED.status,
  resolution = EXCLUDED.resolution, feature_id = EXCLUDED.feature_id, custom_fields = EXCLUDED.custom_fields,
  created_by = EXCLUDED.created_by, updated_at = EXCLUDED.updated_at;

-- name: GetHypothesis :one
SELECT * FROM discovery.hypotheses WHERE id = $1;

-- name: ListHypotheses :many
SELECT * FROM discovery.hypotheses
WHERE (sqlc.narg('product_id')::uuid IS NULL OR product_id = sqlc.narg('product_id')::uuid)
  AND (sqlc.narg('feature_id')::uuid IS NULL OR feature_id = sqlc.narg('feature_id')::uuid)
  AND (cardinality(@statuses::text[]) = 0 OR status = ANY(@statuses::text[]))
ORDER BY created_at, id;

-- name: UpsertInterview :exec
INSERT INTO discovery.interviews (id, product_id, account_id, segment, date, participants, notes, hypothesis_ids, created_by, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (id) DO UPDATE SET product_id = EXCLUDED.product_id, account_id = EXCLUDED.account_id, segment = EXCLUDED.segment,
  date = EXCLUDED.date, participants = EXCLUDED.participants, notes = EXCLUDED.notes, hypothesis_ids = EXCLUDED.hypothesis_ids,
  created_by = EXCLUDED.created_by, updated_at = EXCLUDED.updated_at;

-- name: GetInterview :one
SELECT * FROM discovery.interviews WHERE id = $1;

-- name: ListInterviews :many
SELECT * FROM discovery.interviews
WHERE (sqlc.narg('product_id')::uuid IS NULL OR product_id = sqlc.narg('product_id')::uuid)
ORDER BY created_at, id;

-- name: UpsertInsight :exec
INSERT INTO discovery.insights (id, product_id, text, interview_id, hypothesis_ids, signal_ids, confidence, created_by, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (id) DO UPDATE SET product_id = EXCLUDED.product_id, text = EXCLUDED.text, interview_id = EXCLUDED.interview_id,
  hypothesis_ids = EXCLUDED.hypothesis_ids, signal_ids = EXCLUDED.signal_ids, confidence = EXCLUDED.confidence,
  created_by = EXCLUDED.created_by, updated_at = EXCLUDED.updated_at;

-- name: GetInsight :one
SELECT * FROM discovery.insights WHERE id = $1;

-- name: ListInsights :many
SELECT * FROM discovery.insights
WHERE (sqlc.narg('product_id')::uuid IS NULL OR product_id = sqlc.narg('product_id')::uuid)
  AND (sqlc.narg('interview_id')::uuid IS NULL OR interview_id = sqlc.narg('interview_id')::uuid)
  AND (sqlc.narg('hypothesis_id')::uuid IS NULL OR sqlc.narg('hypothesis_id')::uuid = ANY(hypothesis_ids))
  AND (sqlc.narg('signal_id')::uuid IS NULL OR sqlc.narg('signal_id')::uuid = ANY(signal_ids))
ORDER BY created_at, id;

-- name: UpsertEvidence :exec
INSERT INTO discovery.evidence (id, product_id, source, source_ref, date, trust, verification, sha256, hypothesis_id, insight_id, feature_id, created_by, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (id) DO UPDATE SET product_id = EXCLUDED.product_id, source = EXCLUDED.source, source_ref = EXCLUDED.source_ref,
  date = EXCLUDED.date, trust = EXCLUDED.trust, verification = EXCLUDED.verification, sha256 = EXCLUDED.sha256,
  hypothesis_id = EXCLUDED.hypothesis_id, insight_id = EXCLUDED.insight_id, feature_id = EXCLUDED.feature_id,
  created_by = EXCLUDED.created_by, updated_at = EXCLUDED.updated_at;

-- name: GetEvidence :one
SELECT * FROM discovery.evidence WHERE id = $1;

-- name: ListEvidence :many
SELECT * FROM discovery.evidence
WHERE (sqlc.narg('product_id')::uuid IS NULL OR product_id = sqlc.narg('product_id')::uuid)
  AND (sqlc.narg('hypothesis_id')::uuid IS NULL OR hypothesis_id = sqlc.narg('hypothesis_id')::uuid)
  AND (sqlc.narg('insight_id')::uuid IS NULL OR insight_id = sqlc.narg('insight_id')::uuid)
  AND (sqlc.narg('feature_id')::uuid IS NULL OR feature_id = sqlc.narg('feature_id')::uuid)
  AND (@verification::text = '' OR verification = @verification::text)
ORDER BY created_at, id;

-- name: UpsertFieldDef :exec
INSERT INTO discovery.field_defs (id, entity, key, label, type, options, required) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (entity, key) DO UPDATE SET id = EXCLUDED.id, label = EXCLUDED.label, type = EXCLUDED.type,
  options = EXCLUDED.options, required = EXCLUDED.required;

-- name: ListFieldDefs :many
SELECT * FROM discovery.field_defs WHERE entity = $1 ORDER BY seq;

-- name: UpsertStatusDef :exec
INSERT INTO discovery.status_defs (entity, key, label, category) VALUES ($1, $2, $3, $4)
ON CONFLICT (entity, key) DO UPDATE SET label = EXCLUDED.label, category = EXCLUDED.category;

-- name: ListStatusDefs :many
SELECT * FROM discovery.status_defs WHERE entity = $1 ORDER BY seq;

-- name: UpsertEmbedding :exec
INSERT INTO discovery.embeddings (kind, id, product_id, text) VALUES ($1, $2, $3, $4)
ON CONFLICT (kind, id) DO UPDATE SET product_id = EXCLUDED.product_id, text = EXCLUDED.text;

-- name: DeleteEmbedding :exec
DELETE FROM discovery.embeddings WHERE kind = $1 AND id = $2;

-- name: SimilarEmbeddings :many
SELECT e.id, ts_rank_cd(e.tsv, q.query, 1 | 32)::float8 AS score
FROM discovery.embeddings e, websearch_to_tsquery('simple', @lexemes::text) q(query)
WHERE e.kind = @kind AND e.product_id = @product_id AND e.tsv @@ q.query
ORDER BY score DESC, e.id
LIMIT @lim::bigint;

-- name: Lexemes :one
SELECT array_to_string(tsvector_to_array(to_tsvector('russian', @text::text)), ' or ')::text AS lexemes;
