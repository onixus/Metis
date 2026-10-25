-- +goose Up
ALTER TABLE modeling.documents DROP CONSTRAINT documents_kind_check;
ALTER TABLE modeling.documents ADD CONSTRAINT documents_kind_check CHECK (kind IN ('field','metric','template','batch','fact','allocation','bundle','team','shares','closed','scenario','unification'));
-- +goose Down
-- Keep the accepted kind while immutable unification records exist; old binaries ignore it.
SELECT 1;
