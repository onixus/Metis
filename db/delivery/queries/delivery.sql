-- name: UpsertMapping :exec
INSERT INTO delivery.mappings (feature_id, product_id, epic_key, project, created_at) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (feature_id) DO UPDATE SET product_id = EXCLUDED.product_id, epic_key = EXCLUDED.epic_key,
  project = EXCLUDED.project, created_at = EXCLUDED.created_at;

-- name: GetMappingByFeature :one
SELECT * FROM delivery.mappings WHERE feature_id = $1;

-- name: GetMappingByEpic :one
SELECT * FROM delivery.mappings WHERE epic_key = $1 ORDER BY created_at, feature_id LIMIT 1;

-- name: ListMappings :many
SELECT * FROM delivery.mappings ORDER BY epic_key, feature_id;

-- name: UpsertReleaseMapping :exec
INSERT INTO delivery.release_mappings (release_id, product_id, project, fix_version, created_at) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (release_id) DO UPDATE SET product_id = EXCLUDED.product_id, project = EXCLUDED.project,
  fix_version = EXCLUDED.fix_version, created_at = EXCLUDED.created_at;

-- name: ListReleaseMappings :many
SELECT * FROM delivery.release_mappings ORDER BY fix_version, release_id;

-- name: UpsertEpic :exec
INSERT INTO delivery.epics (feature_id, product_id, epic_key, summary, status, due_date, fix_versions, issues, initial_scope, first_seen_at, synced_at, source_event_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (feature_id) DO UPDATE SET product_id = EXCLUDED.product_id, epic_key = EXCLUDED.epic_key, summary = EXCLUDED.summary,
  status = EXCLUDED.status, due_date = EXCLUDED.due_date, fix_versions = EXCLUDED.fix_versions, issues = EXCLUDED.issues,
  initial_scope = EXCLUDED.initial_scope, first_seen_at = EXCLUDED.first_seen_at, synced_at = EXCLUDED.synced_at,
  source_event_id = EXCLUDED.source_event_id;

-- name: GetEpicByFeature :one
SELECT * FROM delivery.epics WHERE feature_id = $1;

-- name: DeleteSprintsByProduct :exec
DELETE FROM delivery.sprints WHERE product_id = $1;

-- name: InsertSprint :exec
INSERT INTO delivery.sprints (product_id, position, board, sprint_id, name, goal, state, start_date, end_date, issues, total, done, carried_over, synced_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14);

-- name: ListSprintsByProduct :many
SELECT * FROM delivery.sprints WHERE product_id = $1 ORDER BY position;

-- name: UpsertSyncState :exec
INSERT INTO delivery.sync_state (id, last_success_at, last_attempt_at, last_error, lag_ns, stale) VALUES (1, $1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE SET last_success_at = EXCLUDED.last_success_at, last_attempt_at = EXCLUDED.last_attempt_at,
  last_error = EXCLUDED.last_error, lag_ns = EXCLUDED.lag_ns, stale = EXCLUDED.stale;

-- name: GetSyncState :one
SELECT * FROM delivery.sync_state WHERE id = 1;

-- name: UpsertFieldMapping :exec
INSERT INTO delivery.field_mapping (id, mapping, updated_at) VALUES (1, $1, $2)
ON CONFLICT (id) DO UPDATE SET mapping = EXCLUDED.mapping, updated_at = EXCLUDED.updated_at;

-- name: GetFieldMapping :one
SELECT mapping FROM delivery.field_mapping WHERE id = 1;

-- name: MarkProcessed :execrows
INSERT INTO delivery.processed_events (external_id) VALUES ($1) ON CONFLICT (external_id) DO NOTHING;
