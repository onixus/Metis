-- name: UpsertRequirementSet :exec
INSERT INTO compliance.requirement_sets (id, code, version, product_type, items, status, created_by, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET code = EXCLUDED.code, version = EXCLUDED.version, product_type = EXCLUDED.product_type,
  items = EXCLUDED.items, status = EXCLUDED.status, created_by = EXCLUDED.created_by, updated_at = EXCLUDED.updated_at;

-- name: GetRequirementSet :one
SELECT * FROM compliance.requirement_sets WHERE id = $1;

-- name: ListRequirementSets :many
SELECT * FROM compliance.requirement_sets WHERE (@code::text = '' OR code = @code::text) ORDER BY code, version, id;

-- name: UpsertTemplate :exec
INSERT INTO compliance.track_templates (id, product_type, name, gates, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (id) DO UPDATE SET product_type = EXCLUDED.product_type, name = EXCLUDED.name, gates = EXCLUDED.gates, updated_at = EXCLUDED.updated_at;

-- name: GetTemplate :one
SELECT * FROM compliance.track_templates WHERE id = $1;

-- name: ListTemplates :many
SELECT * FROM compliance.track_templates WHERE (@product_type::text = '' OR product_type = @product_type::text) ORDER BY created_at, id;

-- name: UpsertTrack :exec
INSERT INTO compliance.tracks (id, product_id, release_id, version, template_id, status, gates, baseline_id, created_by, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (id) DO UPDATE SET product_id = EXCLUDED.product_id, release_id = EXCLUDED.release_id, version = EXCLUDED.version,
  template_id = EXCLUDED.template_id, status = EXCLUDED.status, gates = EXCLUDED.gates, baseline_id = EXCLUDED.baseline_id,
  created_by = EXCLUDED.created_by, updated_at = EXCLUDED.updated_at;

-- name: GetTrack :one
SELECT * FROM compliance.tracks WHERE id = $1;

-- name: ListTracks :many
SELECT * FROM compliance.tracks
WHERE (sqlc.narg('product_id')::uuid IS NULL OR product_id = sqlc.narg('product_id')::uuid)
  AND (sqlc.narg('release_id')::uuid IS NULL OR release_id = sqlc.narg('release_id')::uuid)
ORDER BY created_at, id;

-- name: InsertImpact :exec
INSERT INTO compliance.impact_assessments (id, feature_id, product_id, class, justification, author, at) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListImpactHistory :many
SELECT * FROM compliance.impact_assessments WHERE feature_id = $1 ORDER BY seq;

-- name: UpsertBaseline :exec
INSERT INTO compliance.baselines (id, product_id, track_id, version, requirement_set_id, certificate_no, certified_at, eol, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET product_id = EXCLUDED.product_id, track_id = EXCLUDED.track_id, version = EXCLUDED.version,
  requirement_set_id = EXCLUDED.requirement_set_id, certificate_no = EXCLUDED.certificate_no, certified_at = EXCLUDED.certified_at, eol = EXCLUDED.eol;

-- name: GetBaseline :one
SELECT * FROM compliance.baselines WHERE id = $1;

-- name: ListBaselines :many
SELECT * FROM compliance.baselines WHERE (sqlc.narg('product_id')::uuid IS NULL OR product_id = sqlc.narg('product_id')::uuid) ORDER BY created_at, id;

-- name: LastEvidence :one
SELECT * FROM compliance.evidence_log ORDER BY seq DESC LIMIT 1;

-- name: InsertEvidence :exec
INSERT INTO compliance.evidence_log (seq, id, product_id, track_id, gate_id, url, sha256, status, comment, supersedes, actor, at, prev_hash, hash)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14);

-- name: EvidenceAfter :many
SELECT * FROM compliance.evidence_log WHERE seq > $1 ORDER BY seq LIMIT @lim::bigint;
