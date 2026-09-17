package identityaccess

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/onixus/metis/internal/kernel"
)

// HMACVerifier проверяет токены HS256, подписанные общим секретом. Только для стенда и e2e
// (METIS_AUTH_MODE=hmac); в поставке используется OIDC (AD-01).
// TODO(question-04): аварийный доступ.
type HMACVerifier struct {
	secret []byte
	issuer string
	clock  kernel.Clock
}

// NewHMACVerifier создаёт проверку HS256.
func NewHMACVerifier(secret []byte, issuer string, clock kernel.Clock) (*HMACVerifier, error) {
	if len(secret) < 32 {
		return nil, kernel.Invalid("secret", "секрет HMAC короче 32 байт")
	}
	return &HMACVerifier{secret: secret, issuer: issuer, clock: clock}, nil
}

type hmacClaims struct {
	Iss      string   `json:"iss"`
	Sub      string   `json:"sub"`
	Exp      int64    `json:"exp"`
	Roles    []string `json:"roles"`
	Products []string `json:"products"`
	Finance  string   `json:"finance,omitempty"`
}

// Verify реализует TokenVerifier.
func (v *HMACVerifier) Verify(_ context.Context, raw string) (Claims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Claims{}, fmt.Errorf("%w: формат токена", kernel.ErrForbidden)
	}
	mac := hmac.New(sha256.New, v.secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return Claims{}, fmt.Errorf("%w: подпись токена", kernel.ErrForbidden)
	}
	var hdr struct {
		Alg string `json:"alg"`
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(hb, &hdr) != nil || hdr.Alg != "HS256" {
		return Claims{}, fmt.Errorf("%w: заголовок токена", kernel.ErrForbidden)
	}
	pb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: тело токена", kernel.ErrForbidden)
	}
	var c hmacClaims
	if err := json.Unmarshal(pb, &c); err != nil {
		return Claims{}, fmt.Errorf("%w: клеймы: %w", kernel.ErrForbidden, err)
	}
	if c.Iss != v.issuer || c.Sub == "" || c.Exp < v.clock.Now().Unix() {
		return Claims{}, fmt.Errorf("%w: издатель, субъект или срок токена", kernel.ErrForbidden)
	}
	return Claims{Subject: c.Sub, Roles: c.Roles, Products: c.Products, Finance: c.Finance}, nil
}

// MintHS256 выпускает токен HS256 (стенд, e2e, seed).
func MintHS256(secret []byte, issuer, subject string, roles, products []string, finance string, ttl time.Duration, clock kernel.Clock) (string, error) {
	hb, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	pb, err := json.Marshal(hmacClaims{Iss: issuer, Sub: subject, Exp: clock.Now().Add(ttl).Unix(), Roles: roles, Products: products, Finance: finance})
	if err != nil {
		return "", fmt.Errorf("mint: %w", err)
	}
	head := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(head))
	return head + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
