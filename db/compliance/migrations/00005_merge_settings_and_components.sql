-- +goose Up
ALTER TABLE compliance.baselines ADD COLUMN IF NOT EXISTS components jsonb NOT NULL DEFAULT '[]'::jsonb;
CREATE INDEX IF NOT EXISTS baselines_components_idx ON compliance.baselines USING gin (components jsonb_path_ops);
ALTER TABLE compliance.settings ADD COLUMN IF NOT EXISTS singleton boolean NOT NULL DEFAULT true;
CREATE UNIQUE INDEX IF NOT EXISTS settings_singleton_idx ON compliance.settings(singleton);
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='compliance' AND table_name='settings' AND column_name='id') THEN
  ALTER TABLE compliance.settings ALTER COLUMN id SET DEFAULT 1;
 END IF;
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'metis_app') THEN
  GRANT SELECT, INSERT, UPDATE ON compliance.settings TO metis_app;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Forward compatibility bridge intentionally preserves both historical representations.
SELECT 1;
