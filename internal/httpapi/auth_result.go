package httpapi

import (
	"context"

	"github.com/onixus/metis/internal/audit"
)

type authenticationResultKey struct{}

// AuthenticationResult preserves denial audit data across application rollback.
// It never stores a bearer token or unverified claims.
type AuthenticationResult struct {
	Denied *audit.Entry
}

// WithAuthenticationResult installs a per-request result for the runtime boundary.
func WithAuthenticationResult(ctx context.Context) (context.Context, *AuthenticationResult) {
	result := &AuthenticationResult{}
	return context.WithValue(ctx, authenticationResultKey{}, result), result
}

func authenticationDenied(ctx context.Context, actor, object, reason string) {
	if result, ok := ctx.Value(authenticationResultKey{}).(*AuthenticationResult); ok {
		result.Denied = &audit.Entry{Actor: actor, Action: audit.ActionLoginDenied, ObjectType: object, Details: map[string]any{"reason": reason}}
	}
}
