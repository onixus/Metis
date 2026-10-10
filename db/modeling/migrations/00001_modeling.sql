-- +goose Up
CREATE SCHEMA modeling;
CREATE TABLE modeling.documents (
    kind text NOT NULL CHECK (kind IN ('field','metric','template','batch','fact','allocation','bundle','team','shares','closed','scenario')),
    key text NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    product_id uuid NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
    body jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, key, revision)
);
CREATE INDEX modeling_product ON modeling.documents(kind, product_id);
CREATE UNIQUE INDEX modeling_applied_version ON modeling.documents ((body->>'period'), ((body->>'data_version')::bigint))
WHERE kind = 'batch' AND body->>'status' = 'applied';
-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'metis_app') THEN
    GRANT USAGE ON SCHEMA modeling TO metis_app;
    GRANT SELECT, INSERT ON modeling.documents TO metis_app;
    REVOKE UPDATE, DELETE ON modeling.documents FROM metis_app;
  END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
DROP SCHEMA modeling CASCADE;
