package economics

import (
	"context"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

type CanonicalManualInput struct {
	BookProductID kernel.ID
	FieldKey      string
	BookFieldKey  string
	Field         FieldVersion
	Slice         Slice
	Value         decimal.Decimal
}
type CanonicalManualWriter interface {
	WriteCanonicalManualValue(context.Context, authz.Scope, CanonicalManualInput) (FactRow, error)
}

func (s *Service) WithCanonicalManualWriter(w CanonicalManualWriter) *Service {
	s.canonicalManualWriter = w
	return s
}
func (s *Service) writeCanonicalManual(ctx context.Context, sc authz.Scope, key string, field FieldVersion, sl Slice, value decimal.Decimal) (FactRow, bool, error) {
	c, err := s.canonicalStore()
	if err != nil {
		return FactRow{}, false, nil
	}
	binding, bound, err := c.binding(ctx, sc, sl.Period)
	if err != nil || !bound {
		return FactRow{}, bound, err
	}
	if s.canonicalManualWriter == nil {
		return FactRow{}, true, kernel.ErrUnavailable
	}
	raw := key
	for from, to := range binding.FieldKeys {
		if to == key {
			raw = from
			break
		}
	}
	row, err := s.canonicalManualWriter.WriteCanonicalManualValue(ctx, sc, CanonicalManualInput{BookProductID: binding.BookProductID, FieldKey: key, BookFieldKey: raw, Field: field, Slice: sl, Value: value})
	return row, true, err
}
