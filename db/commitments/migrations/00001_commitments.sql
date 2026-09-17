-- +goose Up
CREATE SCHEMA IF NOT EXISTS commitments;

-- Обязательства (CT-01, CT-02).
CREATE TABLE commitments.commitments (
    id              uuid        PRIMARY KEY,
    product_id      uuid        NOT NULL,
    kind            text        NOT NULL,
    subtype         text        NOT NULL DEFAULT '',
    counterparty    text        NOT NULL DEFAULT '',
    subject         text        NOT NULL,
    due_date        date        NULL,
    basis           text        NOT NULL DEFAULT '',
    owner           text        NOT NULL DEFAULT '',
    status          text        NOT NULL,
    feature_id      uuid        NULL,
    release_id      uuid        NULL,
    renewal_item_id uuid        NULL,
    created_by      text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL
);
CREATE INDEX commitments_product_idx ON commitments.commitments (product_id);
CREATE INDEX commitments_feature_idx ON commitments.commitments (feature_id);
CREATE INDEX commitments_release_idx ON commitments.commitments (release_id);
CREATE INDEX commitments_due_idx ON commitments.commitments (due_date);

-- Алерты (CT-03): append-only; меняется только подтверждение.
CREATE TABLE commitments.alerts (
    seq             bigint      GENERATED ALWAYS AS IDENTITY,
    id              uuid        PRIMARY KEY,
    commitment_id   uuid        NOT NULL,
    product_id      uuid        NOT NULL,
    kind            text        NOT NULL,
    message         text        NOT NULL DEFAULT '',
    event_id        uuid        NULL,
    new_date        date        NULL,
    due_date        date        NULL,
    raised_at       timestamptz NOT NULL,
    acknowledged    boolean     NOT NULL DEFAULT false,
    acknowledged_by text        NOT NULL DEFAULT '',
    acknowledged_at timestamptz NULL
);
CREATE INDEX alerts_product_idx ON commitments.alerts (product_id, seq);
CREATE INDEX alerts_commitment_idx ON commitments.alerts (commitment_id);

-- +goose StatementBegin
CREATE FUNCTION commitments.guard_alert() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'commitments.alerts: DELETE запрещён (список только для INSERT)'
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  IF NEW.seq IS DISTINCT FROM OLD.seq OR NEW.id IS DISTINCT FROM OLD.id
     OR NEW.commitment_id IS DISTINCT FROM OLD.commitment_id OR NEW.product_id IS DISTINCT FROM OLD.product_id
     OR NEW.kind IS DISTINCT FROM OLD.kind OR NEW.message IS DISTINCT FROM OLD.message
     OR NEW.event_id IS DISTINCT FROM OLD.event_id OR NEW.new_date IS DISTINCT FROM OLD.new_date
     OR NEW.due_date IS DISTINCT FROM OLD.due_date OR NEW.raised_at IS DISTINCT FROM OLD.raised_at THEN
    RAISE EXCEPTION 'commitments.alerts: изменяется только подтверждение'
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER alerts_guard
  BEFORE UPDATE OR DELETE ON commitments.alerts
  FOR EACH ROW EXECUTE FUNCTION commitments.guard_alert();

-- Обработанные события (идемпотентность обработчика по Event.ID).
CREATE TABLE commitments.processed_events (
    event_id     uuid        PRIMARY KEY,
    processed_at timestamptz NOT NULL DEFAULT now()
);

-- Настройки модуля (CT-04): одна строка, без product_id.
CREATE TABLE commitments.settings (
    id          smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    lead_months bigint      NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'metis_app') THEN
    GRANT USAGE ON SCHEMA commitments TO metis_app;
    GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA commitments TO metis_app;
    REVOKE DELETE ON commitments.alerts FROM metis_app;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE commitments.settings;
DROP TABLE commitments.processed_events;
DROP TRIGGER alerts_guard ON commitments.alerts;
DROP FUNCTION commitments.guard_alert();
DROP TABLE commitments.alerts;
DROP TABLE commitments.commitments;
DROP SCHEMA commitments;
