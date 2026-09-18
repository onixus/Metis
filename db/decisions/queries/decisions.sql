-- name: UpsertRecord :exec
INSERT INTO decisions.records (id, product_id, title, context, snapshot, options, chosen_key, rationale, expected_effect, review_date, status, superseded_by, links, page_id, author, created_at, updated_at, effect_metric, effect_value, effect_period, review)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
ON CONFLICT (id) DO UPDATE SET product_id = EXCLUDED.product_id, title = EXCLUDED.title, context = EXCLUDED.context,
  snapshot = EXCLUDED.snapshot, options = EXCLUDED.options, chosen_key = EXCLUDED.chosen_key, rationale = EXCLUDED.rationale,
  expected_effect = EXCLUDED.expected_effect, review_date = EXCLUDED.review_date, status = EXCLUDED.status,
  superseded_by = EXCLUDED.superseded_by, links = EXCLUDED.links, page_id = EXCLUDED.page_id, author = EXCLUDED.author,
  updated_at = EXCLUDED.updated_at, effect_metric = EXCLUDED.effect_metric, effect_value = EXCLUDED.effect_value,
  effect_period = EXCLUDED.effect_period, review = EXCLUDED.review;

-- name: ListRecordsDueForReview :many
-- DA-06: решения с наступившей датой ревизии, по которым ревизии ещё не было.
SELECT * FROM decisions.records
WHERE review IS NULL AND review_date IS NOT NULL AND review_date <= sqlc.arg('on')::date
  AND status = 'accepted'
ORDER BY review_date, id;

-- name: GetRecord :one
SELECT * FROM decisions.records WHERE id = $1;

-- name: ListRecords :many
SELECT * FROM decisions.records
WHERE (NOT @has_product::bool OR product_id IS NOT DISTINCT FROM sqlc.narg('product_id')::uuid)
  AND (@status::text = '' OR status = @status::text)
  AND (sqlc.narg('link')::jsonb IS NULL OR links @> sqlc.narg('link')::jsonb)
ORDER BY created_at, id;

-- name: EventProcessed :one
SELECT EXISTS (SELECT 1 FROM decisions.processed_events WHERE event_id = $1);

-- name: MarkEventProcessed :exec
INSERT INTO decisions.processed_events (event_id) VALUES ($1) ON CONFLICT (event_id) DO NOTHING;
