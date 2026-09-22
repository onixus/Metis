-- +goose Up
ALTER TABLE portfoliograph.products ADD COLUMN description text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE portfoliograph.products DROP COLUMN description;
