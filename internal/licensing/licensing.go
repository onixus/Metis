// Package licensing — лицензирование поставки (AD-06, ADR-0006).
//
// Ключ подписан Ed25519 и проверяется без доступа в интернет: публичные ключи поставщика
// вшиты в бинарник. Истёкший ключ не останавливает чтение — ограничиваются только операции,
// попадающие под лимиты; в течение льготного периода запись разрешена и пишется в аудит.
package licensing

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// Prefix — префикс формата ключа.
const Prefix = "metis-1"

// GraceDays — льготный период после истечения ключа, в днях.
const GraceDays = 30

// ClockSkewHours — допуск на расхождение часов при проверке начала действия.
const ClockSkewHours = 24

// Limits — лимиты поставки.
type Limits struct {
	Products int `json:"products"`
	Users    int `json:"users"`
}

// License — полезная нагрузка ключа.
type License struct {
	ID        string      `json:"id"`
	Customer  string      `json:"customer"`
	Edition   string      `json:"edition"`
	IssuedAt  kernel.Date `json:"issued_at"`
	NotBefore kernel.Date `json:"not_before"`
	NotAfter  kernel.Date `json:"not_after"`
	Limits    Limits      `json:"limits"`
	Modules   []string    `json:"modules,omitempty"`
}

// Status — результат проверки ключа.
type Status struct {
	Installed bool    `json:"installed"`
	Valid     bool    `json:"valid"`
	License   License `json:"license"`
	Expired   bool    `json:"expired"`
	InGrace   bool    `json:"in_grace"`
	// DaysLeft — дней до истечения; отрицательное значение — дней после истечения.
	DaysLeft int    `json:"days_left"`
	Reason   string `json:"reason,omitempty"`
}

// ErrInvalidKey — ключ не разбирается или подпись не сходится.
var ErrInvalidKey = fmt.Errorf("%w: лицензионный ключ недействителен", kernel.ErrValidation)

// Sign выпускает ключ: подпись Ed25519 по каноническому JSON полезной нагрузки.
// Приватный ключ принадлежит поставщику и в репозитории отсутствует.
func Sign(priv ed25519.PrivateKey, l License) (string, error) {
	body, err := kernel.CanonicalJSON(l)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(priv, body)
	enc := base64.RawURLEncoding
	return Prefix + "." + enc.EncodeToString(body) + "." + enc.EncodeToString(sig), nil
}

// Verifier проверяет ключи офлайн по списку публичных ключей поставщика.
// Список допускает несколько ключей, чтобы смена пары не ломала выданные лицензии.
type Verifier struct {
	keys  []ed25519.PublicKey
	clock kernel.Clock
}

// NewVerifier создаёт проверяющего.
func NewVerifier(clock kernel.Clock, keys ...ed25519.PublicKey) *Verifier {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Verifier{keys: keys, clock: clock}
}

// Verify разбирает ключ, проверяет подпись и сроки.
func (v *Verifier) Verify(key string) (Status, error) {
	l, err := v.parse(key)
	if err != nil {
		return Status{Installed: true, Reason: err.Error()}, err
	}
	today := kernel.DateFromTime(v.clock.Now())
	st := Status{Installed: true, License: l, Valid: true}
	if !l.NotBefore.IsZero() {
		start := l.NotBefore.Time().Add(-ClockSkewHours * time.Hour)
		if v.clock.Now().Before(start) {
			st.Valid, st.Reason = false, fmt.Sprintf("ключ начинает действовать %s", l.NotBefore)
			return st, nil
		}
	}
	if l.NotAfter.IsZero() {
		return st, nil
	}
	st.DaysLeft = today.DaysUntil(l.NotAfter)
	if st.DaysLeft >= 0 {
		return st, nil
	}
	st.Expired = true
	if -st.DaysLeft <= GraceDays {
		st.InGrace = true
		st.Reason = fmt.Sprintf("ключ истёк %s; льготный период до %s", l.NotAfter, l.NotAfter.AddDays(GraceDays))
		return st, nil
	}
	st.Valid = false
	st.Reason = fmt.Sprintf("ключ истёк %s, льготный период закончился", l.NotAfter)
	return st, nil
}

func (v *Verifier) parse(key string) (License, error) {
	parts := strings.Split(strings.TrimSpace(key), ".")
	if len(parts) != 3 || parts[0] != Prefix {
		return License{}, fmt.Errorf("%w: ожидается формат %s.<payload>.<signature>", ErrInvalidKey, Prefix)
	}
	enc := base64.RawURLEncoding
	body, err := enc.DecodeString(parts[1])
	if err != nil {
		return License{}, fmt.Errorf("%w: тело ключа: %w", ErrInvalidKey, err)
	}
	sig, err := enc.DecodeString(parts[2])
	if err != nil {
		return License{}, fmt.Errorf("%w: подпись: %w", ErrInvalidKey, err)
	}
	ok := false
	for _, pub := range v.keys {
		if ed25519.Verify(pub, body, sig) {
			ok = true
			break
		}
	}
	if !ok {
		return License{}, fmt.Errorf("%w: подпись не сходится", ErrInvalidKey)
	}
	var l License
	if err := unmarshalLicense(body, &l); err != nil {
		return License{}, err
	}
	return l, nil
}

// Service хранит установленный ключ и проверяет лимиты поставки.
type Service struct {
	verifier *Verifier
	audit    Auditor

	mu      sync.RWMutex
	key     string
	current Status
}

// Auditor — порт журнала аудита: установка ключа и работа в льготном периоде (AD-04).
type Auditor interface {
	LicenseEvent(ctx context.Context, actor, action string, details map[string]any) error
}

// NewService создаёт сервис лицензирования.
func NewService(v *Verifier) *Service { return &Service{verifier: v} }

// WithAuditor подключает журнал аудита.
func (s *Service) WithAuditor(a Auditor) *Service { s.audit = a; return s }

// Install устанавливает ключ. Требует права администратора (AD-06).
func (s *Service) Install(ctx context.Context, sc authz.Scope, key string) (Status, error) {
	if err := sc.Require(authz.ActionManageLicense, kernel.NilID); err != nil {
		return Status{}, err
	}
	st, err := s.verifier.Verify(key)
	if err != nil {
		return st, err
	}
	s.mu.Lock()
	s.key, s.current = key, st
	s.mu.Unlock()
	if s.audit != nil {
		_ = s.audit.LicenseEvent(ctx, sc.Subject(), "license.install", map[string]any{
			"license_id": st.License.ID, "customer": st.License.Customer,
			"not_after": st.License.NotAfter.String(), "valid": st.Valid,
		})
	}
	return st, nil
}

// InstallFromKey устанавливает ключ при старте приложения без участия пользователя.
func (s *Service) InstallFromKey(key string) (Status, error) {
	st, err := s.verifier.Verify(key)
	if err != nil {
		return st, err
	}
	s.mu.Lock()
	s.key, s.current = key, st
	s.mu.Unlock()
	return st, nil
}

// Status возвращает состояние лицензии, пересчитывая сроки на текущую дату.
func (s *Service) Status(ctx context.Context, sc authz.Scope) (Status, error) {
	if err := sc.Require(authz.ActionManageLicense, kernel.NilID); err != nil {
		return Status{}, err
	}
	return s.status(), nil
}

func (s *Service) status() Status {
	s.mu.RLock()
	key := s.key
	s.mu.RUnlock()
	if key == "" {
		return Status{Reason: "лицензионный ключ не установлен"}
	}
	st, err := s.verifier.Verify(key)
	if err != nil {
		return Status{Installed: true, Reason: err.Error()}
	}
	return st
}

// Resource — ресурс, ограниченный лицензией.
type Resource string

// Ограничиваемые ресурсы.
const (
	ResourceProducts Resource = "products"
	ResourceUsers    Resource = "users"
)

// CheckLimit проверяет, разрешено ли создать ещё одну единицу ресурса.
// Без установленного ключа платформа работает без ограничений только в режиме разработки:
// в поставке ключ ставится при установке.
func (s *Service) CheckLimit(ctx context.Context, actor string, r Resource, current int) error {
	st := s.status()
	if !st.Installed {
		return nil
	}
	if !st.Valid {
		return fmt.Errorf("%w: лицензия недействительна: %s", kernel.ErrForbidden, st.Reason)
	}
	limit := 0
	switch r {
	case ResourceProducts:
		limit = st.License.Limits.Products
	case ResourceUsers:
		limit = st.License.Limits.Users
	}
	if limit > 0 && current >= limit {
		return fmt.Errorf("%w: лимит лицензии по ресурсу %s: %d", kernel.ErrForbidden, r, limit)
	}
	if st.InGrace && s.audit != nil {
		_ = s.audit.LicenseEvent(ctx, actor, "license.grace_write", map[string]any{
			"license_id": st.License.ID, "resource": string(r), "not_after": st.License.NotAfter.String(),
		})
	}
	return nil
}
