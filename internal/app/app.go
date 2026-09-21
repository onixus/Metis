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

	"github.com/onixus/metis/internal/adapters/confluence"
	"github.com/onixus/metis/internal/adapters/crmfile"
	"github.com/onixus/metis/internal/adapters/financexlsx"
	"github.com/onixus/metis/internal/adapters/jira"
	"github.com/onixus/metis/internal/adapters/securityfile"
	"github.com/onixus/metis/internal/analytics"
	"github.com/onixus/metis/internal/audit"
	auditpg "github.com/onixus/metis/internal/audit/pgstore"
	"github.com/onixus/metis/internal/commitments"
	commitmentspg "github.com/onixus/metis/internal/commitments/pgstore"
	"github.com/onixus/metis/internal/compliance"
	compliancepg "github.com/onixus/metis/internal/compliance/pgstore"
	"github.com/onixus/metis/internal/decisions"
	decisionspg "github.com/onixus/metis/internal/decisions/pgstore"
	"github.com/onixus/metis/internal/delivery"
	deliverypg "github.com/onixus/metis/internal/delivery/pgstore"
	"github.com/onixus/metis/internal/discovery"
	discoverypg "github.com/onixus/metis/internal/discovery/pgstore"
	"github.com/onixus/metis/internal/economics"
	"github.com/onixus/metis/internal/httpapi"
	"github.com/onixus/metis/internal/identityaccess"
	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/kernel/migrate"
	"github.com/onixus/metis/internal/kernel/outbox"
	"github.com/onixus/metis/internal/kernel/pgdb"
	"github.com/onixus/metis/internal/licensing"
	"github.com/onixus/metis/internal/marketing"
	"github.com/onixus/metis/internal/observability"
	"github.com/onixus/metis/internal/portfoliograph"
	graphpg "github.com/onixus/metis/internal/portfoliograph/pgstore"
	"github.com/onixus/metis/internal/ports"
	"github.com/onixus/metis/internal/prioritization"
	prioritypg "github.com/onixus/metis/internal/prioritization/pgstore"
	"github.com/onixus/metis/internal/roadmap"
	roadmappg "github.com/onixus/metis/internal/roadmap/pgstore"
	"github.com/onixus/metis/internal/seed"
	"github.com/onixus/metis/internal/signals"
	signalspg "github.com/onixus/metis/internal/signals/pgstore"
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
	// Адаптер Confluence (порт KnowledgeBase): пусто — выключен, ядро работает без него (NF-L03).
	ConfluenceBaseURL string // METIS_CONFLUENCE_BASE_URL
	ConfluenceToken   string // METIS_CONFLUENCE_TOKEN
	ConfluenceSpace   string // METIS_CONFLUENCE_SPACE (пространство страниц ADR по умолчанию)
	CRMDir            string
	// Этап 3.
	SecurityDir     string        // METIS_SECURITY_DIR (манифесты пайплайна безопасности, CM-09)
	LicenseKey      string        // METIS_LICENSE_KEY (ключ поставки, AD-06)
	LicensePubKey   string        // METIS_LICENSE_PUBKEY (публичный ключ поставщика, base64)
	FinanceDir      string        // METIS_FINANCE_DIR (каталог книг XLSX для загрузки по расписанию, EC-01)
	FinanceTemplate string        // METIS_FINANCE_TEMPLATE (название шаблона импорта)
	FinanceInterval time.Duration // METIS_FINANCE_INTERVAL (интервал загрузки, по умолчанию 1h) // METIS_CRM_DIR (каталог CSV-выгрузок)
	Seed            bool          // METIS_SEED: загрузить референсные портфели
	SeedAPEX        bool          // METIS_SEED_APEX: загрузить канонический портфель APEX
	OTelExport      string        // METIS_OTEL_EXPORTER: stdout | otlp | none
	LogLevel        string        // METIS_LOG_LEVEL
	Version         string
}

// FromEnv читает конфигурацию из окружения.
func FromEnv() (Config, error) {
	c := Config{
		HTTPAddr:          envOr("METIS_HTTP_ADDR", ":8081"),
		Storage:           envOr("METIS_STORAGE", "postgres"),
		DatabaseURL:       os.Getenv("METIS_DATABASE_URL"),
		AuthMode:          envOr("METIS_AUTH_MODE", "oidc"),
		OIDCIssuer:        os.Getenv("METIS_OIDC_ISSUER"),
		OIDCClient:        os.Getenv("METIS_OIDC_CLIENT_ID"),
		HMACSecret:        os.Getenv("METIS_HMAC_SECRET"),
		HMACIssuer:        envOr("METIS_HMAC_ISSUER", "metis-stand"),
		JiraBaseURL:       os.Getenv("METIS_JIRA_BASE_URL"),
		JiraToken:         os.Getenv("METIS_JIRA_TOKEN"),
		ConfluenceBaseURL: os.Getenv("METIS_CONFLUENCE_BASE_URL"),
		ConfluenceToken:   os.Getenv("METIS_CONFLUENCE_TOKEN"),
		ConfluenceSpace:   envOr("METIS_CONFLUENCE_SPACE", "METIS"),
		CRMDir:            os.Getenv("METIS_CRM_DIR"),
		SecurityDir:       os.Getenv("METIS_SECURITY_DIR"),
		LicenseKey:        os.Getenv("METIS_LICENSE_KEY"),
		LicensePubKey:     os.Getenv("METIS_LICENSE_PUBKEY"),
		FinanceDir:        os.Getenv("METIS_FINANCE_DIR"),
		FinanceTemplate:   os.Getenv("METIS_FINANCE_TEMPLATE"),
		OTelExport:        envOr("METIS_OTEL_EXPORTER", "none"),
		LogLevel:          envOr("METIS_LOG_LEVEL", "info"),
		Version:           envOr("METIS_VERSION", "0.1.0"),
	}
	var err error
	if c.FinanceInterval, err = envDuration("METIS_FINANCE_INTERVAL", time.Hour); err != nil {
		return c, err
	}
	if c.Migrate, err = envBool("METIS_MIGRATE"); err != nil {
		return c, err
	}
	if c.Seed, err = envBool("METIS_SEED"); err != nil {
		return c, err
	}
	if c.SeedAPEX, err = envBool("METIS_SEED_APEX"); err != nil {
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
	Discovery      *discovery.Service
	Commitments    *commitments.Service
	Compliance     *compliance.Service
	Decisions      *decisions.Service
	Economics      *economics.Service
	Marketing      *marketing.Service
	Analytics      *analytics.Service
	Licensing      *licensing.Service
	// Index — индекс похожести сигналов (SG-04); в памяти до появления pgvector-хранилища.
	Index         *discovery.MemIndex
	Audit         *audit.Logger
	AuditStore    audit.Store
	Outbox        outbox.Store
	Worker        *outbox.Worker
	Jira          *jira.Client
	Confluence    *confluence.Client
	ServiceScope  authz.Scope
	db            *pgdb.DB
	shutdownTrace func(context.Context) error
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
		// complianceClock усекает время до микросекунд на PG: At входит в хеш журнала доказательств (вопрос №10).
		complianceClock kernel.Clock = clock
		// Хранилища модулей: память по умолчанию, PG-реализации в ветке "postgres" (вопрос №12 закрыт итерацией 11).
		signalsStore     signals.Store             = signals.NewMemStore()
		priorityStore    prioritization.Store      = prioritization.NewMemStore()
		roadmapStore     roadmap.Store             = roadmap.NewMemStore()
		deliveryStore    delivery.Store            = delivery.NewMemStore()
		discoveryStore   discovery.Store           = discovery.NewMemStore()
		commitmentsStore commitments.Store         = commitments.NewMemStore()
		complianceStore  compliance.Store          = compliance.NewMemStore()
		evidenceStore    compliance.EvidenceStore  = compliance.NewEvidenceMemStore()
		decisionsStore   decisions.Store           = decisions.NewMemStore()
		index            discovery.SimilarityIndex = discovery.NewMemIndex()
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
		signalsStore, priorityStore, roadmapStore, deliveryStore = signalspg.New(db), prioritypg.New(db), roadmappg.New(db), deliverypg.New(db, clock)
		discoveryStore, index = discoverypg.New(db), discoverypg.NewIndex(db)
		commitmentsStore, decisionsStore = commitmentspg.New(db, clock), decisionspg.New(db)
		complianceStore, evidenceStore = compliancepg.New(db), compliancepg.NewEvidenceStore(db)
		complianceClock = compliancepg.Clock{Inner: clock}
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
	if mi, ok := index.(*discovery.MemIndex); ok {
		a.Index = mi
	}
	a.Signals = signals.NewService(signalsStore, a.Portfolio, pub, clock).WithIndexer(index)
	// Этап 2: compliance — порт стоимости подтверждения (PR-05) и готовности релиза (CM-05);
	// commitments — порт обязательств графа (CT-03) и писатель roadmap (CT-04); decisions — связи трассировки (DS-04).
	a.Compliance = compliance.NewService(complianceStore, evidenceStore, a.Portfolio, pub, complianceClock)
	a.Prioritization = prioritization.NewService(priorityStore, pub, clock).WithMoneyMetrics(a.Signals).WithDerivedDemand(a.Portfolio).WithImpactCost(a.Compliance)
	a.Roadmap = roadmap.NewService(roadmapStore, pub, clock).WithContracts(a.Portfolio).WithReadiness(readinessAdapter{a.Compliance})
	// Порты roadmap подключаются после его создания: compliance проверяет принадлежность релиза продукту (CM-03).
	a.Compliance = a.Compliance.WithReleases(a.Roadmap)
	a.Commitments = commitments.NewService(commitmentsStore, pub, clock).WithRoadmapWriter(a.Roadmap).WithRoadmapReader(a.Roadmap)
	a.Portfolio = a.Portfolio.WithCommitments(a.Commitments)
	a.Decisions = decisions.NewService(decisionsStore, pub, clock)
	a.Discovery = discovery.NewService(discoveryStore, pub, clock,
		discovery.WithSignals(a.Signals), discovery.WithSignalMerger(a.Signals), discovery.WithSignalLinker(a.Signals),
		discovery.WithFeatures(a.Portfolio), discovery.WithDecisions(decisionLinks{a.Decisions}), discovery.WithIndex(index))

	// Этап 3: экономика, маркетинг, конструктор дашбордов, лицензия поставки.
	// compliance получает реестр сроков (CM-08 → CT-02) и пайплайн безопасности (CM-09).
	a.Compliance = a.Compliance.WithDeadlines(deadlineAdapter{svc: a.Commitments})
	if cfg.SecurityDir != "" {
		a.Compliance = a.Compliance.WithPipeline(securityfile.New(cfg.SecurityDir))
	}
	econ, err := economics.NewService(economics.NewMemStore(), pub, clock, economics.DefaultConfig())
	if err != nil {
		return nil, fmt.Errorf("экономика: %w", err)
	}
	a.Economics = econ.
		WithImport(financexlsx.New(), a.Portfolio).
		WithAuditor(financeAuditor{a.Audit}).
		WithTrackCosts(a.Compliance).
		WithCommitments(commitmentsAdapter{svc: a.Commitments}).
		WithTracks(tracksAdapter{svc: a.Compliance})
	a.Decisions = a.Decisions.WithMetrics(metricsAdapter{svc: a.Economics})
	a.Analytics = analytics.NewService(analytics.NewMemStore(), clock)
	a.Licensing, err = buildLicensing(cfg, clock, a.Audit, log)
	if err != nil {
		return nil, err
	}

	// Delivery: адаптер Jira подключается только при заданном URL; ядро работает без него (NF-L03).
	var tracker ports.DeliveryTracker
	if cfg.JiraBaseURL != "" {
		jc, err := jira.New(cfg.JiraBaseURL, jira.NewStaticToken(cfg.JiraToken), jira.DefaultFieldConfig(), &http.Client{Timeout: 15 * time.Second})
		if err != nil {
			return nil, fmt.Errorf("адаптер Jira: %w", err)
		}
		a.Jira, tracker = jc, jc
	}
	a.Delivery = delivery.NewService(deliveryStore, tracker, a.Portfolio, pub, clock, delivery.Config{Name: "jira", ServiceScope: identityaccess.ServiceScope("delivery")})

	// Воркер outbox: обработчики модулей.
	a.Worker = outbox.NewWorker(obStore, clock, outbox.Config{}, log)
	a.Worker.Register(portfoliograph.EventDateShifted, roadmap.NewShiftHandler(a.Roadmap, identityaccess.ServiceScope("roadmap")))
	if tracker != nil {
		a.Worker.Register(delivery.EventEpicCreateRequested, delivery.NewCreateEpicHandler(tracker, a.Delivery, a.Portfolio, identityaccess.ServiceScope("delivery")))
	}
	// CT-03: алерты по обязательствам при сдвигах фич и элементов roadmap.
	shift := commitments.NewShiftHandler(a.Commitments, identityaccess.ServiceScope("commitments"))
	a.Worker.Register(portfoliograph.EventDateShifted, shift)
	a.Worker.Register(roadmap.EventDatesChanged, shift)
	// База знаний: адаптер Confluence подключается только при заданном URL (NF-L03); страницы ADR
	// создаются исключительно обработчиком outbox (инвариант 5).
	if cfg.ConfluenceBaseURL != "" {
		cc, err := confluence.New(cfg.ConfluenceBaseURL, confluence.NewStaticToken(cfg.ConfluenceToken), &http.Client{Timeout: 15 * time.Second})
		if err != nil {
			return nil, fmt.Errorf("адаптер Confluence: %w", err)
		}
		a.Confluence = cc
		a.Worker.Register(decisions.EventPageRequested, decisions.NewPublishPageHandler(a.Decisions, cc, identityaccess.ServiceScope("decisions")))
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
		if err := seed.Stage2(ctx, seed.Stage2Deps{
			Portfolio: a.Portfolio, Roadmap: a.Roadmap, Compliance: a.Compliance, Commitments: a.Commitments, Discovery: a.Discovery, Decisions: a.Decisions,
		}, identityaccess.ServiceScope("seed")); err != nil {
			return nil, fmt.Errorf("seed этапа 2: %w", err)
		}
		// Этап 3: финансовые поля, показатели, шаблон импорта и правило аллокации затрат хаба SOAR.
		soar, err := a.Portfolio.ProductIDByKey(ctx, "soar")
		if err != nil {
			return nil, fmt.Errorf("seed этапа 3: продукт soar: %w", err)
		}
		shares := map[kernel.ID]string{}
		for key, share := range map[string]string{"edr": "0.4", "vm": "0.3", "deception": "0.3"} {
			id, err := a.Portfolio.ProductIDByKey(ctx, key)
			if err != nil {
				return nil, fmt.Errorf("seed этапа 3: продукт %s: %w", key, err)
			}
			shares[id] = share
		}
		if _, err := seed.Economics(ctx, a.Economics, identityaccess.FinanceServiceScope("seed"), soar, shares); err != nil {
			return nil, fmt.Errorf("seed этапа 3: %w", err)
		}
	}
	if cfg.SeedAPEX {
		if _, err := seed.APEX(ctx, a.Portfolio, identityaccess.ServiceScope("seed")); err != nil {
			return nil, fmt.Errorf("seed APEX: %w", err)
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
		Discovery: a.Discovery, Commitments: a.Commitments, Compliance: a.Compliance, Decisions: a.Decisions,
		Economics: a.Economics, Analytics: a.Analytics, Licensing: a.Licensing, Delivery: a.Delivery,
		Marketing:      marketing.NewService(crm).WithProducts(a.Portfolio),
		KnowledgeSpace: knowledgeSpace(cfg),
		Ready:          a.ready,
		Metrics:        metrics.Handler(),
		Instrument:     func(h http.Handler) http.Handler { return observability.Instrument(h, "metis-api") },
		Extra:          a.extraRoutes,
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

// knowledgeSpace — пространство базы знаний для страниц ADR; без адаптера страницы не запрашиваются (503).
func knowledgeSpace(cfg Config) string {
	if cfg.ConfluenceBaseURL == "" {
		return ""
	}
	return cfg.ConfluenceSpace
}

// readinessAdapter соединяет порт roadmap.ReadinessChecker с compliance.ReleaseReadiness (CM-05):
// типы Readiness у модулей разные, чтобы roadmap не зависел от compliance.
type readinessAdapter struct{ c *compliance.Service }

func (r readinessAdapter) ReleaseReadiness(ctx context.Context, sc authz.Scope, releaseID kernel.ID) (roadmap.Readiness, error) {
	rd, err := r.c.ReleaseReadiness(ctx, sc, releaseID)
	if err != nil {
		return roadmap.Readiness{}, err
	}
	return roadmap.Readiness{Ready: rd.Ready, OpenItems: rd.OpenItems}, nil
}

// decisionLinks соединяет порт discovery.DecisionLinks с decisions.DecisionsFor (DS-04): типы DecisionRef у модулей свои.
type decisionLinks struct{ d *decisions.Service }

func (l decisionLinks) DecisionsFor(ctx context.Context, sc authz.Scope, kind string, id kernel.ID) ([]discovery.DecisionRef, error) {
	refs, err := l.d.DecisionsFor(ctx, sc, kind, id)
	if err != nil {
		return nil, err
	}
	out := make([]discovery.DecisionRef, 0, len(refs))
	for _, r := range refs {
		out = append(out, discovery.DecisionRef{ID: r.ID, Title: r.Title})
	}
	return out, nil
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
