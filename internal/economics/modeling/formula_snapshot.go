package economics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

type formulaSnapshotKey struct{}
type platformCacheKey struct {
	identity *byte
	scope    string
	source   string
	slice    string
}
type formulaCaptureKey struct {
	provider   any
	scope, key string
}
type formulaCapture struct {
	done  chan struct{}
	value any
	err   error
}

const maxFormulaCaptureBytes = 32 << 20

type formulaSnapshot struct {
	captureMu    sync.Mutex
	captureBytes int
	captures     map[formulaCaptureKey]*formulaCapture
	mu           sync.Mutex
	values       map[platformCacheKey]PlatformValue
	cost         int
	explain      explanationBudget
}

// WithFormulaSnapshot pins direct source reads across a single composed calculation.
// The context must stay within one operation; services and authorization scopes have
// distinct cache namespaces, while external book evaluation clones retain their source identity.
func WithFormulaSnapshot(ctx context.Context) context.Context {
	if _, ok := ctx.Value(formulaSnapshotKey{}).(*formulaSnapshot); ok {
		return ctx
	}
	return context.WithValue(ctx, formulaSnapshotKey{}, &formulaSnapshot{values: map[platformCacheKey]PlatformValue{}, captures: map[formulaCaptureKey]*formulaCapture{}})
}

func formulaScopeKey(sc authz.Scope) (string, error) {
	roles := sc.Roles()
	sort.Slice(roles, func(i, j int) bool { return roles[i] < roles[j] })
	type productAccess struct {
		ID     kernel.ID
		Access authz.Access
	}
	products := []productAccess{}
	for _, id := range sc.ProductIDs(authz.AccessStrategic) {
		products = append(products, productAccess{id, sc.Product(id)})
	}
	sort.Slice(products, func(i, j int) bool { return products[i].ID.String() < products[j].ID.String() })
	raw, err := json.Marshal(struct {
		Subject  string
		Roles    []authz.Role
		Products []productAccess
		Global   authz.Access
		Finance  authz.FinanceLevel
		Audience authz.Audience
	}{sc.Subject(), roles, products, sc.Product(kernel.NilID), sc.Finance(), sc.Audience()})
	if err != nil {
		return "", fmt.Errorf("scope snapshot key: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// CaptureFormulaInput pins a trusted provider's complete input within an operation,
// before source slices are projected. The provider identity and original Scope isolate
// callers. Callbacks run without the cache mutex, so captures may depend on other keys.
// A context without WithFormulaSnapshot performs an ordinary uncached read.
func CaptureFormulaInput[T any, P comparable](ctx context.Context, sc authz.Scope, provider P, key string, read func(context.Context) (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if !sc.Valid() {
		return zero, kernel.ErrForbidden
	}
	session, ok := ctx.Value(formulaSnapshotKey{}).(*formulaSnapshot)
	if !ok {
		return read(ctx)
	}
	scope, err := formulaScopeKey(sc)
	if err != nil {
		return zero, err
	}
	cacheKey := formulaCaptureKey{provider: provider, scope: scope, key: key}
	session.captureMu.Lock()
	entry, exists := session.captures[cacheKey]
	if !exists {
		if len(session.captures) >= 128 {
			session.captureMu.Unlock()
			return zero, kernel.Invalid("sources", "не более 128 полных снимков источников за расчёт")
		}
		entry = &formulaCapture{done: make(chan struct{})}
		session.captures[cacheKey] = entry
	}
	session.captureMu.Unlock()
	if !exists {
		value, readErr := read(ctx)
		if readErr == nil {
			readErr = ctx.Err()
		}
		if readErr == nil {
			encoded, encodeErr := json.Marshal(value)
			if encodeErr != nil {
				readErr = kernel.Invalid("sources", "complete source input cannot be budgeted")
			} else {
				session.captureMu.Lock()
				if len(encoded) > maxFormulaCaptureBytes-session.captureBytes {
					readErr = kernel.Invalid("sources", "полные снимки источников превышают 32 MiB за расчёт")
				} else {
					session.captureBytes += len(encoded)
				}
				session.captureMu.Unlock()
			}
		}
		// Failed/oversized responses are never retained, including partial payloads.
		if readErr == nil {
			entry.value = kernel.CloneValue(value)
		}
		entry.err = readErr
		close(entry.done)
	}
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-entry.done:
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if entry.err != nil {
		return zero, entry.err
	}
	value, ok := entry.value.(T)
	if !ok {
		return zero, kernel.Invalid("source", "capture namespace type mismatch")
	}
	return kernel.CloneValue(value), nil
}
