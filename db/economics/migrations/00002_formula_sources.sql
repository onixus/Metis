-- +goose Up
CREATE TABLE economics.formula_sources (
 id uuid PRIMARY KEY,
 product_id uuid NOT NULL,
 period text NOT NULL CHECK(period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
 kind text NOT NULL CHECK(kind IN ('worklogs','crm_revenue','headcount')),
 team_id uuid NOT NULL,
 portfolio boolean NOT NULL DEFAULT false,
 version integer NOT NULL CHECK(version > 0),
 body jsonb NOT NULL,
 created_at timestamptz NOT NULL,
 UNIQUE(product_id,period,kind,team_id,portfolio,version)
);
CREATE TRIGGER immutable_formula_source BEFORE UPDATE OR DELETE ON economics.formula_sources
FOR EACH ROW EXECUTE FUNCTION economics.reject_snapshot_mutation();
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='metis_app') THEN
  GRANT SELECT,INSERT ON economics.formula_sources TO metis_app;
  REVOKE UPDATE,DELETE ON economics.formula_sources FROM metis_app;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
DROP TABLE economics.formula_sources;
