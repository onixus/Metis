package identityaccess

import (
	"context"

	"github.com/onixus/metis/internal/identityaccess/internal/oidc"
)

type oidcVerifier struct{ v *oidc.Verifier }

func (o oidcVerifier) Verify(ctx context.Context, raw string) (Claims, error) {
	c, err := o.v.Verify(ctx, raw)
	if err != nil {
		return Claims{}, err
	}
	return Claims{Subject: c.Subject, Roles: c.Roles, Products: c.Products, Finance: c.Finance}, nil
}

// NewOIDCVerifier создаёт проверку токенов по издателю OIDC (AD-01).
func NewOIDCVerifier(ctx context.Context, issuer, clientID string) (TokenVerifier, error) {
	v, err := oidc.New(ctx, issuer, clientID)
	if err != nil {
		return nil, err
	}
	return oidcVerifier{v: v}, nil
}
