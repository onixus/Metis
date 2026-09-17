-- +goose Up
-- CM-01: версия набора требований уникальна в пределах кода. Номер версии вычисляется чтением
-- (max + 1), поэтому без уникального индекса два параллельных создания дают две записи одной
-- версии, и выбор опубликованной версии для baseline становится произвольным.
-- Прежний неуникальный индекс requirement_sets_code_idx перекрыт уникальным и удаляется.
CREATE UNIQUE INDEX requirement_sets_code_version_key ON compliance.requirement_sets (code, version);
DROP INDEX compliance.requirement_sets_code_idx;

-- +goose Down
CREATE INDEX requirement_sets_code_idx ON compliance.requirement_sets (code, version);
DROP INDEX compliance.requirement_sets_code_version_key;
