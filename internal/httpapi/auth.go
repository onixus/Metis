package httpapi

import (
	"net/http"
	"strings"

	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/httpapi/gen"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
)

// Authenticator превращает Bearer-токен в authz.Scope в контексте запроса (AD-01, AD-02).
type Authenticator struct {
	verifier identityaccess.TokenVerifier
	resolver *identityaccess.Resolver
	audit    *audit.Logger
}

// NewAuthenticator создаёт middleware аутентификации.
func NewAuthenticator(v identityaccess.TokenVerifier, r *identityaccess.Resolver, a *audit.Logger) *Authenticator {
	return &Authenticator{verifier: v, resolver: r, audit: a}
}

// Middleware проверяет токен; при отказе — 401 problem+json. Scope кладётся в контекст.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer"))
		if raw == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			w.Header().Set("WWW-Authenticate", `Bearer realm="metis"`)
			writeProblem(w, http.StatusUnauthorized, gen.Problem{Type: "urn:metis:problem:unauthenticated", Title: "Требуется аутентификация", Status: http.StatusUnauthorized})
			return
		}
		claims, err := a.verifier.Verify(r.Context(), raw)
		if err != nil {
			if a.audit != nil {
				_, _ = a.audit.Append(r.Context(), audit.Entry{Actor: "anonymous", Action: audit.ActionLoginDenied, ObjectType: "token", Details: map[string]any{"reason": "token"}})
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="metis", error="invalid_token"`)
			writeProblem(w, http.StatusUnauthorized, gen.Problem{Type: "urn:metis:problem:unauthenticated", Title: "Токен отклонён", Status: http.StatusUnauthorized})
			return
		}
		scope, err := a.resolver.ScopeFor(r.Context(), claims)
		if err != nil {
			if a.audit != nil {
				_, _ = a.audit.Append(r.Context(), audit.Entry{Actor: claims.Subject, Action: audit.ActionLoginDenied, ObjectType: "scope", Details: map[string]any{"reason": "roles"}})
			}
			status, body := problemFor(err)
			writeProblem(w, status, body)
			return
		}
		next.ServeHTTP(w, r.WithContext(authz.WithScope(r.Context(), scope)))
	})
}
