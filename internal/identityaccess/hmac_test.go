package identityaccess_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
)

func TestAD01_HMACVerifierAcceptsValidRejectsTampered(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	clock := kernel.FixedClock{T: time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)}
	v, err := identityaccess.NewHMACVerifier(secret, "metis-stand", clock)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := identityaccess.MintHS256(secret, "metis-stand", "pm-edr", []string{"pm"}, []string{"edr"}, "", time.Hour, clock)
	if err != nil {
		t.Fatal(err)
	}
	c, err := v.Verify(context.Background(), tok)
	if err != nil || c.Subject != "pm-edr" || c.Roles[0] != "pm" || c.Products[0] != "edr" {
		t.Fatalf("verify: %+v %v", c, err)
	}
	if _, err := v.Verify(context.Background(), tok[:len(tok)-2]+"xx"); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatal("подделанная подпись должна отклоняться")
	}
	expired, _ := identityaccess.MintHS256(secret, "metis-stand", "x", nil, nil, "", -time.Hour, clock)
	if _, err := v.Verify(context.Background(), expired); !errors.Is(err, kernel.ErrForbidden) {
		t.Fatal("просроченный токен должен отклоняться")
	}
	if _, err := identityaccess.NewHMACVerifier([]byte("short"), "i", clock); !errors.Is(err, kernel.ErrValidation) {
		t.Fatal("короткий секрет запрещён")
	}
}
