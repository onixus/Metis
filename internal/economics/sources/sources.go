// Package sources owns immutable, scoped aggregates used by financial formulas.
package sources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/shopspring/decimal"
)

type Key struct {
	Portfolio bool      `json:"portfolio"`
	ProductID kernel.ID `json:"product_id"`
	Period    string    `json:"period"`
	Kind      string    `json:"kind"`
	TeamID    kernel.ID `json:"team_id"`
}
type Provenance struct {
	Source   string `json:"source"`
	RecordID string `json:"record_id"`
	Version  string `json:"version"`
}
type Input struct {
	Key
	Value      decimal.Decimal `json:"value"`
	Currency   string          `json:"currency"`
	AsOf       time.Time       `json:"as_of"`
	Provenance []Provenance    `json:"provenance"`
}
type Snapshot struct {
	Input
	ID          kernel.ID `json:"id"`
	Version     int       `json:"version"`
	ContentHash string    `json:"content_hash"`
	CreatedAt   time.Time `json:"created_at"`
	Actor       string    `json:"actor"`
}
type Store interface {
	Latest(context.Context, authz.Scope, Key) (Snapshot, error)
	History(context.Context, authz.Scope, Key) ([]Snapshot, error)
	Append(context.Context, authz.Scope, Snapshot, int) error
}
type TransactionalStore interface {
	Transact(context.Context, authz.Scope, func(context.Context) error) error
}
type Auditor interface {
	Append(context.Context, audit.Entry) (audit.Record, error)
}
type Service struct {
	store Store
	audit Auditor
	clock kernel.Clock
	mu    sync.Mutex
}

func NewService(store Store, auditor Auditor, clock kernel.Clock) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{store: store, audit: auditor, clock: clock}
}
func Require(sc authz.Scope, k Key, write bool) error {
	if k.Portfolio && !sc.SeesAllProducts() {
		return kernel.ErrForbidden
	}
	if sc.Finance() < authz.FinanceFull {
		return kernel.ErrForbidden
	}
	action := authz.ActionReadModelFinance
	if write {
		action = authz.ActionWriteModelFinance
	}
	if k.ProductID == kernel.NilID {
		return kernel.Invalid("product_id", "concrete product required")
	}
	if err := sc.Require(action, k.ProductID); err != nil {
		return err
	}
	if !sc.Allows(authz.ActionReadModelFinance, k.ProductID) {
		return kernel.ErrForbidden
	}
	if p, err := time.Parse("2006-01", k.Period); err != nil || p.Format("2006-01") != k.Period {
		return kernel.Invalid("period", "expected YYYY-MM")
	}
	switch k.Kind {
	case "worklogs", "crm_revenue", "headcount":
	default:
		return kernel.Invalid("kind", "unknown formula source")
	}
	return nil
}
func (s *Service) record(ctx context.Context, sc authz.Scope, k Key, action audit.Action, versions ...Snapshot) error {
	if s.audit == nil {
		return kernel.ErrUnavailable
	}
	details := map[string]any{"kind": k.Kind, "period": k.Period, "team_id": k.TeamID, "portfolio": k.Portfolio}
	if len(versions) > 0 {
		v := versions[0]
		details["snapshot_id"] = v.ID
		details["version"] = v.Version
		details["content_hash"] = v.ContentHash
	}
	_, err := s.audit.Append(ctx, audit.Entry{Actor: sc.Subject(), Action: action, ObjectType: "economics.formula_source", ObjectID: k.Kind + ":" + k.Period, ProductID: k.ProductID, Details: details})
	return err
}
func (s *Service) Latest(ctx context.Context, sc authz.Scope, k Key) (Snapshot, error) {
	if err := Require(sc, k, false); err != nil {
		return Snapshot{}, err
	}
	v, err := s.store.Latest(ctx, sc, k)
	if err != nil {
		return Snapshot{}, err
	}
	if err = s.record(ctx, sc, k, audit.ActionViewFinance, v); err != nil {
		return Snapshot{}, err
	}
	return v, nil
}
func (s *Service) History(ctx context.Context, sc authz.Scope, k Key) ([]Snapshot, error) {
	if err := Require(sc, k, false); err != nil {
		return nil, err
	}
	v, err := s.store.History(ctx, sc, k)
	if err != nil {
		return nil, err
	}
	if err = s.record(ctx, sc, k, audit.ActionViewFinance); err != nil {
		return nil, err
	}
	return v, nil
}

// Capture appends changed content only. AsOf is ingestion time, so an unchanged
// authoritative response retains the same revision and original capture timestamp.
func (s *Service) Capture(ctx context.Context, sc authz.Scope, in Input) (Snapshot, error) {
	if err := Require(sc, in.Key, true); err != nil {
		return Snapshot{}, err
	}
	if tx, ok := s.store.(TransactionalStore); ok {
		var out Snapshot
		err := tx.Transact(ctx, sc, func(ctx context.Context) error { var err error; out, err = s.capture(ctx, sc, in); return err })
		return out, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.capture(ctx, sc, in)
}
func (s *Service) capture(ctx context.Context, sc authz.Scope, in Input) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if in.AsOf.IsZero() || len(in.Provenance) == 0 || len(in.Provenance) > 256 {
		return Snapshot{}, kernel.Invalid("source", "timestamp and bounded provenance required")
	}
	if in.Value.Exponent() < -128 || in.Value.Exponent() > 128 || in.Value.Coefficient().BitLen() > 512 || in.Value.GreaterThan(decimal.NewFromInt(math.MaxInt64)) || !in.Value.Equal(in.Value.Truncate(0)) || in.Value.IsNegative() {
		return Snapshot{}, kernel.Invalid("value", "bounded nonnegative integer required")
	}
	if in.Kind == "crm_revenue" {
		if in.Currency == "" || len(in.Currency) != 3 || !in.Value.Equal(decimal.NewFromInt(in.Value.IntPart())) {
			return Snapshot{}, kernel.Invalid("currency", "int64 minor currency required")
		}
	} else if in.Currency != "" {
		return Snapshot{}, kernel.Invalid("currency", "nonmonetary source has no currency")
	}
	for _, p := range in.Provenance {
		if p.Source == "" || len(p.Source) > 128 || p.RecordID == "" || len(p.RecordID) > 256 || len(p.Version) > 256 {
			return Snapshot{}, kernel.Invalid("provenance", "invalid source provenance")
		}
	}
	raw, err := json.Marshal(struct {
		Key
		Value      string
		Currency   string
		Provenance []Provenance
	}{in.Key, in.Value.String(), in.Currency, in.Provenance})
	if err != nil {
		return Snapshot{}, fmt.Errorf("source content: %w", err)
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	previous, err := s.store.Latest(ctx, sc, in.Key)
	if err != nil && !errors.Is(err, kernel.ErrNotFound) {
		return Snapshot{}, err
	}
	if previous.ID != kernel.NilID && previous.ContentHash == hash {
		if err := s.record(ctx, sc, in.Key, audit.ActionViewFinance, previous); err != nil {
			return Snapshot{}, err
		}
		return previous, nil
	}
	out := Snapshot{Input: kernel.CloneValue(in), ID: kernel.NewID(), Version: previous.Version + 1, ContentHash: hash, CreatedAt: s.clock.Now().UTC(), Actor: sc.Subject()}
	if err := s.record(ctx, sc, in.Key, audit.ActionRuleChange, out); err != nil {
		return Snapshot{}, err
	}
	if err := s.store.Append(ctx, sc, out, previous.Version); err != nil {
		return Snapshot{}, err
	}
	return out, nil
}
func (v Snapshot) Revision() string { return strconv.Itoa(v.Version) + ":" + v.ContentHash }
