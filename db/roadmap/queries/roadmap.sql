-- name: UpsertItem :exec
INSERT INTO roadmap.items (id, product_id, feature_id, title, bucket, start_date, end_date, release_id, audience, status, kind, commitment_id, created_at, updated_at, launch_tier, launch_date)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
ON CONFLICT (id) DO UPDATE SET product_id = EXCLUDED.product_id, feature_id = EXCLUDED.feature_id, title = EXCLUDED.title,
  bucket = EXCLUDED.bucket, start_date = EXCLUDED.start_date, end_date = EXCLUDED.end_date, release_id = EXCLUDED.release_id,
  audience = EXCLUDED.audience, status = EXCLUDED.status, kind = EXCLUDED.kind, commitment_id = EXCLUDED.commitment_id,
  updated_at = EXCLUDED.updated_at, launch_tier = EXCLUDED.launch_tier, launch_date = EXCLUDED.launch_date;

-- name: GetItem :one
SELECT * FROM roadmap.items WHERE id = $1;

-- name: ListItemsByProduct :many
SELECT * FROM roadmap.items WHERE product_id = $1 ORDER BY created_at, id;

-- name: ListItemsByFeature :many
SELECT * FROM roadmap.items WHERE feature_id = $1 ORDER BY created_at, id;

-- name: GetItemByCommitment :one
SELECT * FROM roadmap.items WHERE commitment_id = $1 ORDER BY created_at, id LIMIT 1;

-- name: UpsertRelease :exec
INSERT INTO roadmap.releases (id, product_id, name, version, planned_date, status, branch, base_release_id, feature_ids, release_notes, eol, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (id) DO UPDATE SET product_id = EXCLUDED.product_id, name = EXCLUDED.name, version = EXCLUDED.version,
  planned_date = EXCLUDED.planned_date, status = EXCLUDED.status, branch = EXCLUDED.branch, base_release_id = EXCLUDED.base_release_id,
  feature_ids = EXCLUDED.feature_ids, release_notes = EXCLUDED.release_notes, eol = EXCLUDED.eol, updated_at = EXCLUDED.updated_at;

-- name: GetRelease :one
SELECT * FROM roadmap.releases WHERE id = $1;

-- name: ListReleasesByProduct :many
SELECT * FROM roadmap.releases WHERE product_id = $1 ORDER BY created_at, id;

-- name: InsertDateChange :exec
INSERT INTO roadmap.date_history (id, item_id, product_id, old_start, old_end, new_start, new_end, reason, actor, at, event_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: ListDateHistory :many
SELECT * FROM roadmap.date_history WHERE item_id = $1 ORDER BY seq;

-- name: EventProcessed :one
SELECT EXISTS (SELECT 1 FROM roadmap.processed_events WHERE event_id = $1);

-- name: MarkEventProcessed :exec
INSERT INTO roadmap.processed_events (event_id) VALUES ($1) ON CONFLICT (event_id) DO NOTHING;
