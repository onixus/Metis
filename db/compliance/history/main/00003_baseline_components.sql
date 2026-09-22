-- +goose Up
-- CM-08: состав компонентов сертифицированного baseline. По идентификатору уязвимого компонента
-- платформа отвечает списком сертифицированных версий, поэтому состав хранится вместе с baseline.
-- Состав приходит из SBOM пайплайна безопасности (CM-09) или заводится вручную.
ALTER TABLE compliance.baselines ADD COLUMN components jsonb NOT NULL DEFAULT '[]'::jsonb;
CREATE INDEX baselines_components_idx ON compliance.baselines USING gin (components jsonb_path_ops);

-- +goose Down
DROP INDEX compliance.baselines_components_idx;
ALTER TABLE compliance.baselines DROP COLUMN components;
