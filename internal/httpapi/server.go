package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/onixus/metis/api"
	"github.com/onixus/metis/internal/audit"
	"github.com/onixus/metis/internal/commitments"
	"github.com/onixus/metis/internal/compliance"
	"github.com/onixus/metis/internal/decisions"
	"github.com/onixus/metis/internal/discovery"
	"github.com/onixus/metis/internal/httpapi/gen"
	"github.com/onixus/metis/internal/portfoliograph"
	"github.com/onixus/metis/internal/ports"
	"github.com/onixus/metis/internal/prioritization"
	"github.com/onixus/metis/internal/roadmap"
	"github.com/onixus/metis/internal/signals"
)

// Deps — зависимости HTTP-сервера. Модули подключаются через публичные интерфейсы.
type Deps struct {
	Log        *slog.Logger
	Auth       *Authenticator
	Portfolio  *portfoliograph.Service
	AuditStore audit.Store
	Audit      *audit.Logger
	// Модули; nil — соответствующие маршруты отвечают 503.
	Signals        *signals.Service
	Prioritization *prioritization.Service
	Roadmap        *roadmap.Service
	CRM            ports.CRM
	Discovery      *discovery.Service
	Commitments    *commitments.Service
	Compliance     *compliance.Service
	Decisions      *decisions.Service
	// KnowledgeSpace — пространство базы знаний по умолчанию для страниц ADR (METIS_CONFLUENCE_SPACE).
	KnowledgeSpace string
	// Ready сообщает о готовности зависимостей (БД, миграции) для /readyz.
	Ready func(ctx context.Context) error
	// Metrics — обработчик метрик Prometheus; nil — не монтируется.
	Metrics http.Handler
	// Instrument оборачивает API-обработчик (трассировки); nil — без обёртки.
	Instrument func(http.Handler) http.Handler
	// Extra — дополнительные маршруты модулей под /api/v1 (регистрируются на том же роутере).
	Extra func(r chi.Router)
}

// Server реализует gen.StrictServerInterface.
type Server struct {
	d Deps
}

var _ gen.StrictServerInterface = (*Server)(nil)

// NewServer создаёт сервер.
func NewServer(d Deps) *Server {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Server{d: d}
}

// Handler собирает роутер: служебные маршруты без аутентификации, API — с ней.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer, middleware.Timeout(30*time.Second))
	r.Use(requestLogger(s.d.Log))
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if s.d.Ready != nil {
			if err := s.d.Ready(req.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	if s.d.Metrics != nil {
		r.Handle("/metrics", s.d.Metrics)
	}
	r.Get("/api/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(api.OpenAPI)
	})

	strict := gen.NewStrictHandlerWithOptions(s, nil, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  requestErrorHandler,
		ResponseErrorHandlerFunc: responseErrorHandler(s.d.Log),
	})
	r.Route("/api/v1", func(api chi.Router) {
		if s.d.Auth != nil {
			api.Use(s.d.Auth.Middleware)
		}
		gen.HandlerWithOptions(strict, gen.ChiServerOptions{BaseRouter: api, ErrorHandlerFunc: requestErrorHandler})
		if s.d.Extra != nil {
			s.d.Extra(api)
		}
	})
	if s.d.Instrument != nil {
		return s.d.Instrument(r)
	}
	return r
}

func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			log.InfoContext(r.Context(), "http",
				"method", r.Method, "path", r.URL.Path, "status", ww.Status(),
				"bytes", ww.BytesWritten(), "duration_ms", time.Since(start).Milliseconds(),
				"request_id", middleware.GetReqID(r.Context()))
		})
	}
}
