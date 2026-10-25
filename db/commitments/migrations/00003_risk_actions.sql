-- +goose Up
CREATE TABLE commitments.alert_actions (
    seq bigint GENERATED ALWAYS AS IDENTITY,
    id uuid PRIMARY KEY,
    alert_id uuid NOT NULL REFERENCES commitments.alerts(id),
    product_id uuid NOT NULL,
    kind text NOT NULL CHECK (kind IN ('assigned', 'escalated', 'noted')),
    recipient text NOT NULL DEFAULT '',
    note text NOT NULL CHECK (length(btrim(note)) > 0),
    actor text NOT NULL,
    created_at timestamptz NOT NULL,
    CHECK (kind = 'noted' OR length(btrim(recipient)) > 0)
);
CREATE INDEX alert_actions_alert_idx ON commitments.alert_actions (alert_id, seq);
-- +goose StatementBegin
CREATE FUNCTION commitments.guard_alert_action() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'commitments.alert_actions: только INSERT'
      USING ERRCODE = 'insufficient_privilege';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER alert_actions_guard BEFORE UPDATE OR DELETE ON commitments.alert_actions
FOR EACH ROW EXECUTE FUNCTION commitments.guard_alert_action();
-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'metis_app') THEN
    GRANT SELECT, INSERT ON commitments.alert_actions TO metis_app;
    GRANT USAGE, SELECT ON SEQUENCE commitments.alert_actions_seq_seq TO metis_app;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE commitments.alert_actions;
DROP FUNCTION commitments.guard_alert_action();
