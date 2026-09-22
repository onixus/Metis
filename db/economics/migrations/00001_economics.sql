-- +goose Up
CREATE SCHEMA economics;
CREATE TABLE economics.snapshots (
    id uuid PRIMARY KEY,
    product_id uuid NOT NULL,
    period text NOT NULL CHECK (period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
    version integer NOT NULL CHECK (version > 0),
    body jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (product_id, period, version)
);
CREATE TABLE economics.import_templates (
    product_id uuid NOT NULL,
    name text NOT NULL,
    body jsonb NOT NULL,
    PRIMARY KEY (product_id, name)
);
-- +goose StatementBegin
CREATE FUNCTION economics.reject_snapshot_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'financial versions are immutable';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER immutable_snapshot BEFORE UPDATE OR DELETE ON economics.snapshots
FOR EACH ROW EXECUTE FUNCTION economics.reject_snapshot_mutation();

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'metis_app') THEN
    GRANT USAGE ON SCHEMA economics TO metis_app;
    GRANT SELECT, INSERT ON economics.snapshots TO metis_app;
    GRANT SELECT, INSERT, UPDATE ON economics.import_templates TO metis_app;
    REVOKE UPDATE, DELETE ON economics.snapshots FROM metis_app;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP SCHEMA economics CASCADE;
