// Package pgstore — публичный конструктор PostgreSQL-хранилища графа портфеля.
// Вынесен из пакета portfoliograph, чтобы домен не зависел от pgx (инвариант 2, depguard).
package pgstore

import (
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/portfoliograph"
	impl "github.com/onixus/metis/internal/portfoliograph/internal/pgstore"
)

// Store — portfoliograph.Store с дополнительным чтением rollup.
type Store = impl.PG

// New создаёт portfoliograph.Store на схеме portfoliograph. clock может быть nil.
func New(db *pgdb.DB, clock kernel.Clock) portfoliograph.Store { return impl.New(db, clock) }

// NewStore — тот же конструктор с конкретным типом (доступ к Rollup).
func NewStore(db *pgdb.DB, clock kernel.Clock) *Store { return impl.New(db, clock) }
