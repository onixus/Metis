package app

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/shopspring/decimal"

	"github.com/onixus/metis/internal/adapters/financexlsx"
	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/commitments"
	"github.com/onixus/metis/internal/compliance"
	"github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/licensing"
)

// Адаптеры между модулями этапа 3 и модулями этапов 1–2. Модули не знают друг о друге:
// связи собираются здесь через публичные интерфейсы (инвариант 1).

// deadlineAdapter запускает регуляторный срок устранения уязвимости в реестре обязательств
// по сообщению compliance (CM-08 → CT-02).
type deadlineAdapter struct{ svc *commitments.Service }

func (d deadlineAdapter) StartVulnerabilityDeadline(ctx context.Context, sc authz.Scope, req compliance.DeadlineRequest) (kernel.ID, error) {
	c, err := d.svc.StartVulnerabilityDeadline(ctx, sc, req.ProductID, req.Subject, req.Basis, req.DueDate)
	if err != nil {
		return kernel.NilID, err
	}
	return c.ID, nil
}

// commitmentsAdapter отдаёт экономике действующие обязательства для расчёта сценариев (DA-02).
type commitmentsAdapter struct{ svc *commitments.Service }

func (a commitmentsAdapter) Due(ctx context.Context, sc authz.Scope, products []kernel.ID) ([]economics.CommitmentRef, error) {
	out := make([]economics.CommitmentRef, 0)
	for _, productID := range products {
		list, err := a.svc.List(ctx, sc, commitments.Filter{ProductID: productID, Statuses: []commitments.Status{commitments.StatusActive}})
		if err != nil {
			return nil, err
		}
		for _, c := range list {
			out = append(out, economics.CommitmentRef{ID: c.ID, ProductID: c.ProductID, Title: c.Subject,
				DueDate: c.DueDate, Regulatory: c.Kind == commitments.KindRegulatory})
		}
	}
	return out, nil
}

// tracksAdapter отдаёт экономике идущие треки сертификации с плановыми затратами (DA-02).
type tracksAdapter struct{ svc *compliance.Service }

func (a tracksAdapter) Active(ctx context.Context, sc authz.Scope, products []kernel.ID) ([]economics.TrackRef, error) {
	out := make([]economics.TrackRef, 0)
	for _, productID := range products {
		tracks, err := a.svc.Tracks(ctx, sc, productID)
		if err != nil {
			return nil, err
		}
		for _, t := range tracks {
			if t.Status != compliance.TrackActive {
				continue
			}
			cost, err := a.svc.TrackPlannedCost(ctx, sc, t.ID)
			if err != nil {
				return nil, err
			}
			out = append(out, economics.TrackRef{ID: t.ID, ProductID: t.ProductID,
				Name: trackName(t), PlannedCost: cost, Deadline: trackDeadline(t)})
		}
	}
	return out, nil
}

// trackName — читаемое имя трека: версия продукта.
func trackName(t compliance.Track) string {
	if t.Version == "" {
		return "трек сертификации"
	}
	return "сертификация " + t.Version
}

// trackDeadline — ближайший срок незакрытого гейта трека.
func trackDeadline(t compliance.Track) kernel.Date {
	out := kernel.Date{}
	for _, g := range t.Gates {
		if g.DueDate.IsZero() || g.Status == compliance.GatePassed {
			continue
		}
		if out.IsZero() || g.DueDate.Before(out) {
			out = g.DueDate
		}
	}
	return out
}

// metricsAdapter отдаёт модулю решений фактические значения показателей экономики (DA-06).
type metricsAdapter struct{ svc *economics.Service }

func (a metricsAdapter) MetricValue(ctx context.Context, sc authz.Scope, key string, product kernel.ID, period string) (decimal.Decimal, error) {
	p, err := economics.ParsePeriod(period)
	if err != nil {
		return decimal.Zero, err
	}
	return a.svc.Value(ctx, sc, key, economics.Slice{ProductID: product, Period: p})
}

// financeAuditor пишет в журнал аудита просмотр и изменение финансовых данных (AD-04, NF-S02).
type financeAuditor struct{ log *audit.Logger }

func (f financeAuditor) FinanceAccess(ctx context.Context, actor, action, object string, productID kernel.ID, details map[string]any) error {
	auditAction := audit.ActionViewFinance
	if action == "write" {
		auditAction = audit.ActionRuleChange
	}
	_, err := f.log.Append(ctx, audit.Entry{Actor: actor, Action: auditAction, ObjectType: "economics",
		ObjectID: object, ProductID: productID, Details: details})
	return err
}

// licenseAuditor пишет в журнал установку ключа и запись в льготном периоде (AD-04, AD-06).
type licenseAuditor struct{ log *audit.Logger }

func (l licenseAuditor) LicenseEvent(ctx context.Context, actor, action string, details map[string]any) error {
	_, err := l.log.Append(ctx, audit.Entry{Actor: actor, Action: audit.ActionRuleChange,
		ObjectType: "license", ObjectID: action, Details: details})
	return err
}

// buildLicensing собирает проверку лицензии поставки (AD-06). Публичный ключ поставщика задаётся
// окружением: секретов и ключей в репозитории нет.
func buildLicensing(cfg Config, clock kernel.Clock, auditLog *audit.Logger, log *slog.Logger) (*licensing.Service, error) {
	var keys []ed25519.PublicKey
	if cfg.LicensePubKey != "" {
		raw, err := base64.StdEncoding.DecodeString(cfg.LicensePubKey)
		if err != nil {
			return nil, fmt.Errorf("%w: METIS_LICENSE_PUBKEY: %w", kernel.ErrValidation, err)
		}
		if len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: METIS_LICENSE_PUBKEY: длина ключа %d", kernel.ErrValidation, len(raw))
		}
		keys = append(keys, raw)
	}
	svc := licensing.NewService(licensing.NewVerifier(clock, keys...)).WithAuditor(licenseAuditor{auditLog})
	if cfg.LicenseKey == "" {
		log.Warn("лицензионный ключ не задан: лимиты поставки не проверяются (AD-06)")
		return svc, nil
	}
	st, err := svc.InstallFromKey(cfg.LicenseKey)
	if err != nil {
		return nil, fmt.Errorf("лицензия: %w", err)
	}
	if !st.Valid {
		log.Warn("лицензия недействительна", "reason", st.Reason)
	}
	return svc, nil
}

// envDuration читает интервал из окружения.
func envDuration(k string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(k)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%w: %s: %w", kernel.ErrValidation, k, err)
	}
	return d, nil
}

// RunFinanceImports выполняет загрузку финансовых книг по расписанию (EC-01): каталог задаётся
// METIS_FINANCE_DIR, шаблон — METIS_FINANCE_TEMPLATE. Файл, загруженный раньше, пропускается по SHA-256.
func (a *App) RunFinanceImports(ctx context.Context) ([]economics.ImportBatch, error) {
	if a.Economics == nil || a.Cfg.FinanceDir == "" || a.Cfg.FinanceTemplate == "" {
		return nil, nil
	}
	sc := identityaccess.FinanceServiceScope("finance-import")
	templates, err := a.Economics.Templates(ctx, sc)
	if err != nil {
		return nil, err
	}
	for _, t := range templates {
		if t.Name != a.Cfg.FinanceTemplate {
			continue
		}
		return a.Economics.RunScheduledImports(ctx, sc, t.ID, financexlsx.NewDirSource(a.Cfg.FinanceDir))
	}
	return nil, fmt.Errorf("%w: шаблон импорта %q не заведён", kernel.ErrNotFound, a.Cfg.FinanceTemplate)
}

// FinanceImportLoop запускает загрузку по расписанию с интервалом METIS_FINANCE_INTERVAL.
// Ошибка одной итерации не останавливает цикл: она попадает в журнал (NF-R05).
func (a *App) FinanceImportLoop(ctx context.Context) {
	if a.Cfg.FinanceDir == "" || a.Cfg.FinanceInterval <= 0 {
		return
	}
	ticker := time.NewTicker(a.Cfg.FinanceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			batches, err := a.RunFinanceImports(ctx)
			if err != nil {
				a.Log.ErrorContext(ctx, "загрузка финансовых данных по расписанию", "err", err)
				continue
			}
			if len(batches) > 0 {
				a.Log.InfoContext(ctx, "загружены финансовые данные", "files", len(batches))
			}
		}
	}
}
