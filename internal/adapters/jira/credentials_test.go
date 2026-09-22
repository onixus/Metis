package jira_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onixus/metis/internal/adapters/jira"
	"github.com/onixus/metis/internal/kernel"
)

type failingCredentials struct{}

func (failingCredentials) Token(context.Context) (string, error) {
	return "", errors.New("synthetic-sensitive-token-provider-error")
}

func TestNFS04_JiraCredentialProviderErrorIsRedacted(t *testing.T) {
	c, err := jira.New("http://localhost", failingCredentials{}, jira.DefaultFieldConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Epic(context.Background(), "MET-1")
	if !errors.Is(err, kernel.ErrForbidden) || strings.Contains(err.Error(), "synthetic-sensitive") {
		t.Fatalf("provider error not redacted: %v", err)
	}
}
