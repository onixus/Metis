// Package oidc — адаптер go-oidc для проверки токенов IdP (AD-01).
package oidc

import (
	"context"
	"fmt"

	gooidc "github.com/coreos/go-oidc/v3/oidc"

	"github.com/onixus/metis/internal/kernel"
)

// Claims — проверенные клеймы токена.
type Claims struct {
	Subject  string
	Roles    []string
	Products []string
	Finance  string
}

// Verifier проверяет ID/access-токены по JWKS издателя.
type Verifier struct {
	v *gooidc.IDTokenVerifier
}

// New подключается к издателю и загружает его конфигурацию.
func New(ctx context.Context, issuer, clientID string) (*Verifier, error) {
	p, err := gooidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc provider %s: %w", issuer, err)
	}
	return &Verifier{v: p.Verifier(&gooidc.Config{ClientID: clientID})}, nil
}

// NewWithKeySet строит верификатор по известному набору ключей (для тестов и офлайн-стендов).
func NewWithKeySet(issuer, clientID string, ks gooidc.KeySet) *Verifier {
	return &Verifier{v: gooidc.NewVerifier(issuer, ks, &gooidc.Config{ClientID: clientID})}
}

type rawClaims struct {
	Roles    []string `json:"roles"`
	Products []string `json:"products"`
	Finance  string   `json:"finance"`
}

// Verify проверяет токен и извлекает клеймы.
func (v *Verifier) Verify(ctx context.Context, raw string) (Claims, error) {
	tok, err := v.v.Verify(ctx, raw)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: токен отклонён: %w", kernel.ErrForbidden, err)
	}
	var rc rawClaims
	if err := tok.Claims(&rc); err != nil {
		return Claims{}, fmt.Errorf("%w: клеймы: %w", kernel.ErrForbidden, err)
	}
	return Claims{Subject: tok.Subject, Roles: rc.Roles, Products: rc.Products, Finance: rc.Finance}, nil
}
