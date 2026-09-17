-- +goose Up
CREATE SCHEMA IF NOT EXISTS audit;

-- Журнал аудита (AD-04, NF-S05, инвариант 8): только INSERT, сцепка хешей.
CREATE TABLE audit.records (
    seq         bigint      PRIMARY KEY,
    at          timestamptz NOT NULL,
    actor       text        NOT NULL,
    action      text        NOT NULL,
    object_type text        NOT NULL DEFAULT '',
    object_id   text        NOT NULL DEFAULT '',
    product_id  uuid        NULL,
    details     jsonb       NULL,
    prev_hash   char(64)    NOT NULL,
    hash        char(64)    NOT NULL
);
CREATE INDEX records_product_idx ON audit.records (product_id);
CREATE INDEX records_at_idx ON audit.records (at);

-- +goose StatementBegin
CREATE FUNCTION audit.reject_change() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'audit.records: % запрещён (журнал только для INSERT)', TG_OP
    USING ERRCODE = 'insufficient_privilege';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER records_immutable
  BEFORE UPDATE OR DELETE ON audit.records
  FOR EACH ROW EXECUTE FUNCTION audit.reject_change();

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'metis_app') THEN
    GRANT USAGE ON SCHEMA audit TO metis_app;
    GRANT SELECT, INSERT ON audit.records TO metis_app;
    REVOKE UPDATE, DELETE ON audit.records FROM metis_app;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER records_immutable ON audit.records;
DROP FUNCTION audit.reject_change();
DROP TABLE audit.records;
DROP SCHEMA audit;
