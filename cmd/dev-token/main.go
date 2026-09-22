// Команда dev-token выдаёт короткоживущий токен только для явно включённого локального HMAC-стенда.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/kernel"
)

func main() {
	if err := run(os.Args[1:], os.Getenv, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "dev-token:", err)
		os.Exit(1)
	}
}

func run(args []string, getenv func(string) string, output io.Writer) error {
	flags := flag.NewFlagSet("dev-token", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	subject := flags.String("subject", "", "синтетический пользователь стенда (обязательно)")
	roles := flags.String("roles", "pm", "роли через запятую")
	products := flags.String("products", "", "ключи продуктов через запятую (например edr,vm)")
	finance := flags.String("finance", "none", "финансовый уровень: none, aggregates, full")
	ttl := flags.Duration("ttl", time.Hour, "срок действия от 1m до 8h")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("параметры: %w", err)
	}
	if flags.NArg() != 0 || strings.TrimSpace(*subject) == "" || *ttl < time.Minute || *ttl > 8*time.Hour {
		return fmt.Errorf("требуются -subject и срок -ttl от 1m до 8h; позиционные аргументы не поддерживаются")
	}
	if getenv("METIS_AUTH_MODE") != "hmac" {
		return fmt.Errorf("доступно только при METIS_AUTH_MODE=hmac на локальном стенде")
	}
	secret := []byte(getenv("METIS_HMAC_SECRET"))
	issuer := getenv("METIS_HMAC_ISSUER")
	if issuer == "" {
		issuer = "metis-stand"
	}
	if _, err := identityaccess.NewHMACVerifier(secret, issuer, kernel.SystemClock{}); err != nil {
		return fmt.Errorf("конфигурация стенда: %w", err)
	}
	roleList := split(*roles)
	if len(roleList) == 0 {
		return fmt.Errorf("требуется хотя бы одна роль")
	}
	for _, role := range roleList {
		switch role {
		case "cpo", "pm", "dev_lead", "marketing", "finance", "compliance", "presale", "admin":
		default:
			return fmt.Errorf("неизвестная пользовательская роль %q", role)
		}
	}
	if *finance != "none" && *finance != "aggregates" && *finance != "full" {
		return fmt.Errorf("неизвестный финансовый уровень")
	}
	token, err := identityaccess.MintHS256(secret, issuer, *subject, roleList, split(*products), *finance, *ttl, kernel.SystemClock{})
	if err != nil {
		return fmt.Errorf("выдача токена: %w", err)
	}
	if _, err := fmt.Fprintln(output, token); err != nil {
		return fmt.Errorf("вывод токена: %w", err)
	}
	return nil
}

func split(value string) []string {
	values := []string{}
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}
