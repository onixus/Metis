package confluence_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onixus/metis/internal/adapters/confluence"
	"github.com/onixus/metis/internal/kernel"
)

type failingCredentials struct{}

func (failingCredentials) Token(context.Context) (string, error) {
	return "", errors.New("synthetic-sensitive-token-provider-error")
}

func TestNFS04_ConfluenceCredentialProviderErrorIsRedacted(t *testing.T) {
	c, err := confluence.New("http://localhost", failingCredentials{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Page(context.Background(), "1")
	if !errors.Is(err, kernel.ErrForbidden) || strings.Contains(err.Error(), "synthetic-sensitive") {
		t.Fatalf("provider error not redacted: %v", err)
	}
}
