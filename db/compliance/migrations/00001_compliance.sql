-- +goose Up
CREATE SCHEMA IF NOT EXISTS compliance;

-- Каталог наборов требований (CM-01): портфельный, без product_id. Коды синтетические (ТЗ 8.2).
CREATE TABLE compliance.requirement_sets (
    id           uuid        PRIMARY KEY,
    code         text        NOT NULL,
    version      bigint      NOT NULL,
    product_type text        NOT NULL,
    items        jsonb       NOT NULL DEFAULT '[]'::jsonb,
    status       text        NOT NULL,
    created_by   text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL
);
CREATE INDEX requirement_sets_code_idx ON compliance.requirement_sets (code, version);

-- Шаблоны треков (CM-02): портфельный каталог; гейты — jsonb.
CREATE TABLE compliance.track_templates (
    id           uuid        PRIMARY KEY,
    product_type text        NOT NULL,
    name         text        NOT NULL,
    gates        jsonb       NOT NULL DEFAULT '[]'::jsonb,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL
);

-- Треки сертификации версии (CM-03): гейты с чек-листами — jsonb (деньги внутри — минорные единицы + валюта).
CREATE TABLE compliance.tracks (
    id          uuid        PRIMARY KEY,
    product_id  uuid        NOT NULL,
    release_id  uuid        NOT NULL,
    version     text        NOT NULL DEFAULT '',
    template_id uuid        NOT NULL,
    status      text        NOT NULL,
    gates       jsonb       NOT NULL DEFAULT '[]'::jsonb,
    baseline_id uuid        NULL,
    created_by  text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL
);
CREATE INDEX tracks_product_idx ON compliance.tracks (product_id);
CREATE INDEX tracks_release_idx ON compliance.tracks (release_id);

-- Оценки класса влияния (CM-06): только INSERT.
CREATE TABLE compliance.impact_assessments (
    seq           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    id            uuid        NOT NULL UNIQUE,
    feature_id    uuid        NOT NULL,
    product_id    uuid        NOT NULL,
    class         text        NOT NULL,
    justification text        NOT NULL DEFAULT '',
    author        text        NOT NULL DEFAULT '',
    at            timestamptz NOT NULL
);
CREATE INDEX impact_assessments_feature_idx ON compliance.impact_assessments (feature_id, seq);
CREATE INDEX impact_assessments_product_idx ON compliance.impact_assessments (product_id);

-- Сертифицированные конфигурации (CM-07).
CREATE TABLE compliance.baselines (
    id                 uuid        PRIMARY KEY,
    product_id         uuid        NOT NULL,
    track_id           uuid        NULL,
    version            text        NOT NULL DEFAULT '',
    requirement_set_id uuid        NULL,
    certificate_no     text        NOT NULL DEFAULT '',
    certified_at       date        NULL,
    eol                date        NULL,
    created_at         timestamptz NOT NULL
);
CREATE INDEX baselines_product_idx ON compliance.baselines (product_id);

-- Журнал доказательств (CM-04, инвариант 8): только INSERT, сцепка хешей.
CREATE TABLE compliance.evidence_log (
    seq        bigint      PRIMARY KEY,
    id         uuid        NOT NULL,
    product_id uuid        NOT NULL,
    track_id   uuid        NOT NULL,
    gate_id    uuid        NOT NULL,
    url        text        NOT NULL DEFAULT '',
    sha256     text        NOT NULL DEFAULT '',
    status     text        NOT NULL,
    comment    text        NOT NULL DEFAULT '',
    supersedes bigint      NOT NULL DEFAULT 0,
    actor      text        NOT NULL,
    at         timestamptz NOT NULL,
    prev_hash  char(64)    NOT NULL,
    hash       char(64)    NOT NULL
);
CREATE INDEX evidence_log_product_idx ON compliance.evidence_log (product_id);
CREATE INDEX evidence_log_track_idx ON compliance.evidence_log (track_id, gate_id);

-- +goose StatementBegin
CREATE FUNCTION compliance.reject_change() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION '%.%: % запрещён (журнал только для INSERT)', TG_TABLE_SCHEMA, TG_TABLE_NAME, TG_OP
    USING ERRCODE = 'insufficient_privilege';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER evidence_log_immutable
  BEFORE UPDATE OR DELETE ON compliance.evidence_log
  FOR EACH ROW EXECUTE FUNCTION compliance.reject_change();

CREATE TRIGGER impact_assessments_immutable
  BEFORE UPDATE OR DELETE ON compliance.impact_assessments
  FOR EACH ROW EXECUTE FUNCTION compliance.reject_change();

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'metis_app') THEN
    GRANT USAGE ON SCHEMA compliance TO metis_app;
    GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA compliance TO metis_app;
    REVOKE UPDATE, DELETE ON compliance.evidence_log FROM metis_app;
    REVOKE UPDATE, DELETE ON compliance.impact_assessments FROM metis_app;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER impact_assessments_immutable ON compliance.impact_assessments;
DROP TRIGGER evidence_log_immutable ON compliance.evidence_log;
DROP FUNCTION compliance.reject_change();
DROP TABLE compliance.evidence_log;
DROP TABLE compliance.baselines;
DROP TABLE compliance.impact_assessments;
DROP TABLE compliance.tracks;
DROP TABLE compliance.track_templates;
DROP TABLE compliance.requirement_sets;
DROP SCHEMA compliance;
