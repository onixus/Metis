// Package db содержит миграции goose и SQL-запросы sqlc, встроенные в бинарник.
package db

import "embed"

// Migrations — каталоги <module>/migrations/*.sql для всех модулей.
//
//go:embed */migrations/*.sql
var Migrations embed.FS
