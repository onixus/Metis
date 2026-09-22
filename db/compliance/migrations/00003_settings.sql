-- +goose Up
-- Module-wide configuration, shared by every API/worker process.
CREATE TABLE compliance.settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    value jsonb NOT NULL CHECK (jsonb_typeof(value) = 'object')
);

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'metis_app') THEN
    GRANT SELECT, INSERT, UPDATE ON compliance.settings TO metis_app;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE compliance.settings;
