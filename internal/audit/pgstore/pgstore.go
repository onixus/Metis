// Package pgstore — публичный конструктор PostgreSQL-хранилища журнала аудита.
package pgstore

import (
	"time"

	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/audit/internal/store"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/pgdb"
)

// New создаёт audit.Store на таблице audit.records.
func New(db *pgdb.DB) audit.Store { return store.New(db) }

// Clock округляет время до микросекунд — точности timestamptz, чтобы хеш записи,
// посчитанный до сохранения, совпадал с хешем прочитанной записи. Используйте его в audit.NewLogger.
type Clock struct{ Inner kernel.Clock }

// Now — время Inner (или системное), усечённое до микросекунд, в UTC.
func (c Clock) Now() time.Time {
	inner := c.Inner
	if inner == nil {
		inner = kernel.SystemClock{}
	}
	return inner.Now().UTC().Truncate(time.Microsecond)
}
