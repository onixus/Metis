package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
)

func TestAD01_DevTokenRequiresExplicitHMACStand(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"-subject", "synthetic-pm"}, func(string) string { return "" }, &out); err == nil || out.Len() != 0 {
		t.Fatal("CLI выпустил токен без явного режима стенда")
	}
	getenv := func(key string) string {
		switch key {
		case "METIS_AUTH_MODE":
			return "hmac"
		case "METIS_HMAC_SECRET":
			return strings.Repeat("x", 32)
		default:
			return ""
		}
	}
	if err := run([]string{"-subject", "synthetic-pm", "-roles", "pm", "-products", "edr,vm"}, getenv, &out); err != nil {
		t.Fatal(err)
	}
	verifier, err := identityaccess.NewHMACVerifier([]byte(strings.Repeat("x", 32)), "metis-stand", kernel.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifier.Verify(context.Background(), strings.TrimSpace(out.String()))
	if err != nil || claims.Subject != "synthetic-pm" || len(claims.Products) != 2 || claims.Roles[0] != "pm" {
		t.Fatalf("неверные клеймы: %+v %v", claims, err)
	}
	for _, args := range [][]string{
		{"-subject", "pm", "-ttl", "9h"},
		{"-subject", "pm", "-ttl", "0s"},
		{"-subject", "pm", "-roles", "service"},
		{},
	} {
		out.Reset()
		if err := run(args, getenv, &out); err == nil || out.Len() != 0 {
			t.Fatalf("опасные параметры приняты: %v", args)
		}
	}
}
