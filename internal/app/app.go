// Package app собирает платформу из модулей: хранилища, аутентификацию, HTTP-слой.
// Используется cmd/api и e2e-тестами; конфигурация только из окружения.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/onixus/metis/internal/adapters/crmfile"
	"github.com/onixus/metis/internal/adapters/jira"
	"github.com/onixus/metis/internal/audit"
	auditpg "github.com/onixus/metis/internal/audit/pgstore"
	"github.com/onixus/metis/internal/delivery"
	"github.com/onixus/metis/internal/httpapi"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/migrate"
	"github.com/onixus/metis/internal/kernel/outbox"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/observability"
	"github.com/onixus/metis/internal/portfoliograph"
	graphpg "github.com/onixus/metis/internal/portfoliograph/pgstore"
	"github.com/onixus/metis/internal/ports"
	"github.com/onixus/metis/internal/prioritization"
	"github.com/onixus/metis/internal/roadmap"
	"github.com/onixus/metis/internal/seed"
	"github.com/onixus/metis/internal/signals"
)

// Config — конфигурация приложения.
type Config struct {
	HTTPAddr    string // METIS_HTTP_ADDR
	Storage     string // METIS_STORAGE: memory | postgres
	DatabaseURL string // METIS_DATABASE_URL
	Migrate     bool   // METIS_MIGRATE
	AuthMode    string // METIS_AUTH_MODE: oidc | hmac
	OIDCIssuer  string // METIS_OIDC_ISSUER
	OIDCClient  string // METIS_OIDC_CLIENT_ID
	HMACSecret  string // METIS_HMAC_SECRET (только стенд и e2e)
	HMACIssuer  string // METIS_HMAC_ISSUER
	JiraBaseURL string // METIS_JIRA_BASE_URL (пусто — адаптер выключен, NF-L03)
	JiraToken   string // METIS_JIRA_TOKEN
	CRMDir      string // METIS_CRM_DIR (каталог CSV-выгрузок)
	Seed        bool   // METIS_SEED: загрузить референсные портфели
	OTelExport  string // METIS_OTEL_EXPORTER: stdout | otlp | none
	LogLevel    string // METIS_LOG_LEVEL
	Version     string
}

// FromEnv читает конфигурацию из окружения.
func FromEnv() (Config, error) {
	c := Config{
		HTTPAddr:    envOr("METIS_HTTP_ADDR", ":8081"),
		Storage:     envOr("METIS_STORAGE", "postgres"),
		DatabaseURL: os.Getenv("METIS_DATABASE_URL"),
		AuthMode:    envOr("METIS_AUTH_MODE", "oidc"),
		OIDCIssuer:  os.Getenv("METIS_OIDC_ISSUER"),
		OIDCClient:  os.Getenv("METIS_OIDC_CLIENT_ID"),
		HMACSecret:  os.Getenv("METIS_HMAC_SECRET"),
		HMACIssuer:  envOr("METIS_HMAC_ISSUER", "metis-stand"),
		JiraBaseURL: os.Getenv("METIS_JIRA_BASE_URL"),
		JiraToken:   os.Getenv("METIS_JIRA_TOKEN"),
		CRMDir:      os.Getenv("METIS_CRM_DIR"),
		OTelExport:  envOr("METIS_OTEL_EXPORTER", "none"),
		LogLevel:    envOr("METIS_LOG_LEVEL", "info"),
		Version:     envOr("METIS_VERSION", "0.1.0"),
	}
	var err error
	if c.Migrate, err = envBool("METIS_MIGRATE"); err != nil {
		return c, err
	}
	if c.Seed, err = envBool("METIS_SEED"); err != nil {
		return c, err
	}
	if c.Storage == "postgres" && c.DatabaseURL == "" {
		return c, fmt.Errorf("%w: METIS_DATABASE_URL обязателен при METIS_STORAGE=postgres", kernel.ErrValidation)
	}
	return c, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envBool(k string) (bool, error) {
	v := os.Getenv(k)
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%w: %s: %w", kernel.ErrValidation, k, err)
	}
	return b, nil
}

// App — собранное приложение.
type App struct {
	Cfg            Config
	Log            *slog.Logger
	Handler        http.Handler
	Portfolio      *portfoliograph.Service
	Signals        *signals.Service
	Prioritization *prioritization.Service
	Roadmap        *roadmap.Service
	Delivery       *delivery.Service
	Audit          *audit.Logger
	AuditStore     audit.Store
	Outbox         outbox.Store
	Worker         *outbox.Worker
	Jira           *jira.Client
	ServiceScope   authz.Scope
	db             *pgdb.DB
	shutdownTrace  func(context.Context) error
}

// Build собирает приложение по конфигурации.
func Build(ctx context.Context, cfg Config, log *slog.Logger) (*App, error) {
	if log == nil {
		log = observability.Logger(parseLevel(cfg.LogLevel))
	}
	a := &App{Cfg: cfg, Log: log, ServiceScope: identityaccess.ServiceScope("api")}
	clock := kernel.SystemClock{}

	var (
		graphStore portfoliograph.Store
		auditStore audit.Store
		obStore    outbox.Store
		auditClock kernel.Clock = clock
	)
	switch cfg.Storage {
	case "memory":
		graphStore, auditStore, obStore = portfoliograph.NewMemStore(), audit.NewMemStore(), outbox.NewMemStore()
	case "postgres":
		db, err := pgdb.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			return nil, fmt.Errorf("подключение к БД: %w", err)
		}
		a.db = db
		if cfg.Migrate {
			if err := migrate.Up(ctx, db.Pool(), log); err != nil {
				return nil, fmt.Errorf("миграции: %w", err)
			}
		}
		graphStore, auditStore, obStore = graphpg.New(db, clock), auditpg.New(db), outbox.NewPGStore(db)
		auditClock = auditpg.Clock{Inner: clock}
	default:
		return nil, fmt.Errorf("%w: неизвестное хранилище %q", kernel.ErrValidation, cfg.Storage)
	}
	a.AuditStore, a.Outbox = auditStore, obStore
	a.Audit = audit.NewLogger(auditStore, auditClock)
	pub := outbox.NewPublisher(obStore, clock)

	a.Portfolio = portfoliograph.NewService(graphStore, pub, clock).WithAuditor(dateAuditor{a.Audit})
	if err := a.Portfolio.Load(ctx); err != nil {
		return nil, err
	}
	a.Signals = signals.NewService(signals.NewMemStore(), a.Portfolio, pub, clock)
	a.Prioritization = prioritization.NewService(prioritization.NewMemStore(), pub, clock).WithMoneyMetrics(a.Signals).WithDerivedDemand(a.Portfolio)
	a.Roadmap = roadmap.NewService(roadmap.NewMemStore(), pub, clock)

	// Delivery: адаптер Jira подключается только при заданном URL; ядро работает без него (NF-L03).
	var tracker ports.DeliveryTracker
	if cfg.JiraBaseURL != "" {
		jc, err := jira.New(cfg.JiraBaseURL, jira.NewStaticToken(cfg.JiraToken), jira.DefaultFieldConfig(), &http.Client{Timeout: 15 * time.Second})
		if err != nil {
			return nil, fmt.Errorf("адаптер Jira: %w", err)
		}
		a.Jira, tracker = jc, jc
	}
	a.Delivery = delivery.NewService(delivery.NewMemStore(), tracker, a.Portfolio, pub, clock, delivery.Config{Name: "jira", ServiceScope: identityaccess.ServiceScope("delivery")})

	// Воркер outbox: обработчики модулей.
	a.Worker = outbox.NewWorker(obStore, clock, outbox.Config{}, log)
	a.Worker.Register(portfoliograph.EventDateShifted, roadmap.NewShiftHandler(a.Roadmap, identityaccess.ServiceScope("roadmap")))
	if tracker != nil {
		a.Worker.Register(delivery.EventEpicCreateRequested, delivery.NewCreateEpicHandler(tracker, a.Delivery, a.Portfolio, identityaccess.ServiceScope("delivery")))
	}
	a.Delivery = a.Delivery.WithDLQ(dlqAdapter{a.Worker})

	// Аутентификация.
	var verifier identityaccess.TokenVerifier
	switch cfg.AuthMode {
	case "oidc":
		v, err := identityaccess.NewOIDCVerifier(ctx, cfg.OIDCIssuer, cfg.OIDCClient)
		if err != nil {
			return nil, fmt.Errorf("OIDC: %w", err)
		}
		verifier = v
	case "hmac":
		v, err := identityaccess.NewHMACVerifier([]byte(cfg.HMACSecret), cfg.HMACIssuer, clock)
		if err != nil {
			return nil, fmt.Errorf("HMAC: %w", err)
		}
		log.Warn("режим аутентификации hmac: только для стенда и e2e")
		verifier = v
	default:
		return nil, fmt.Errorf("%w: неизвестный режим аутентификации %q", kernel.ErrValidation, cfg.AuthMode)
	}
	resolver := identityaccess.NewResolver(a.Portfolio, a.Portfolio)

	var crm ports.CRM
	if cfg.CRMDir != "" {
		crm = crmfile.New(cfg.CRMDir)
	}

	if cfg.Seed {
		if _, err := seed.Security(ctx, a.Portfolio, identityaccess.ServiceScope("seed")); err != nil {
			return nil, fmt.Errorf("seed: %w", err)
		}
		if _, err := seed.Infrastructure(ctx, a.Portfolio, identityaccess.ServiceScope("seed")); err != nil {
			return nil, fmt.Errorf("seed: %w", err)
		}
	}

	shutdown, err := observability.Tracing(ctx, "metis-api", cfg.Version, cfg.OTelExport)
	if err != nil {
		return nil, err
	}
	a.shutdownTrace = shutdown
	metrics := observability.NewMetrics("metis")

	srv := httpapi.NewServer(httpapi.Deps{
		Log: log, Auth: httpapi.NewAuthenticator(verifier, resolver, a.Audit),
		Portfolio: a.Portfolio, AuditStore: auditStore, Audit: a.Audit,
		Signals: a.Signals, Prioritization: a.Prioritization, Roadmap: a.Roadmap, CRM: crm,
		Ready:      a.ready,
		Metrics:    metrics.Handler(),
		Instrument: func(h http.Handler) http.Handler { return observability.Instrument(h, "metis-api") },
		Extra:      a.extraRoutes,
	})
	a.Handler = srv.Handler()
	return a, nil
}

func (a *App) ready(ctx context.Context) error {
	if a.db != nil {
		return a.db.Pool().Ping(ctx)
	}
	return nil
}

// extraRoutes — маршруты, не входящие в OpenAPI: webhook трекера (входящий, идемпотентный).
// Webhook защищён общим секретом в заголовке X-Metis-Webhook-Token (METIS_WEBHOOK_TOKEN); без него маршрут выключен.
func (a *App) extraRoutes(r chi.Router) {
	token := os.Getenv("METIS_WEBHOOK_TOKEN")
	if a.Jira == nil || token == "" {
		return
	}
	r.With(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Header.Get("X-Metis-Webhook-Token") != token {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, req)
		})
	}).Post("/webhooks/jira", a.jiraWebhook)
}

// WebhookHandler — обработчик webhook без проверки токена (для e2e и внутренних вызовов).
func (a *App) WebhookHandler() http.HandlerFunc { return a.jiraWebhook }

func (a *App) jiraWebhook(w http.ResponseWriter, req *http.Request) {
	if a.Jira == nil {
		http.Error(w, "адаптер трекера выключен", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
	if err != nil {
		http.Error(w, "body", http.StatusBadRequest)
		return
	}
	ev, err := a.Jira.ParseWebhook(body)
	if err != nil {
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ignored"})
		return
	}
	if err := a.Delivery.HandleWebhook(req.Context(), ev); err != nil {
		a.Log.ErrorContext(req.Context(), "webhook", "err", err)
		http.Error(w, "обработка", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
}

// Close освобождает ресурсы.
func (a *App) Close(ctx context.Context) error {
	var errs []string
	if a.shutdownTrace != nil {
		if err := a.shutdownTrace(ctx); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if a.db != nil {
		a.db.Close()
	}
	if len(errs) > 0 {
		return fmt.Errorf("close: %s", strings.Join(errs, "; "))
	}
	return nil
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

// dlqAdapter приводит счётчик DLQ воркера к порту delivery.DLQReader.
type dlqAdapter struct{ w *outbox.Worker }

func (d dlqAdapter) DLQCount(ctx context.Context) (int, error) {
	n, err := d.w.DLQCount(ctx)
	return int(n), err
}

// dateAuditor записывает изменения дат в журнал аудита (AD-04).
type dateAuditor struct{ log *audit.Logger }

func (d dateAuditor) DateChanged(ctx context.Context, actor string, featureID, productID kernel.ID, oldDate, newDate kernel.Date, reason string, affected int) error {
	_, err := d.log.Append(ctx, audit.Entry{Actor: actor, Action: audit.ActionDateChange, ObjectType: "feature", ObjectID: featureID.String(), ProductID: productID,
		Details: map[string]any{"old": oldDate.String(), "new": newDate.String(), "reason": reason, "affected": affected}})
	return err
}
