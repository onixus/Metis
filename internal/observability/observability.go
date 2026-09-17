// Package observability — трассировки OpenTelemetry, метрики Prometheus, структурные логи (NF-O04).
package observability

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Logger создаёт JSON-логгер slog.
func Logger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

// Tracing настраивает экспорт трассировок: "stdout", "otlp" (OTEL_EXPORTER_OTLP_ENDPOINT) или "" (выключено).
// Возвращает функцию завершения.
func Tracing(ctx context.Context, service, version, exporter string) (func(context.Context) error, error) {
	if exporter == "" || exporter == "none" {
		return func(context.Context) error { return nil }, nil
	}
	var exp sdktrace.SpanExporter
	var err error
	switch exporter {
	case "stdout":
		exp, err = stdouttrace.New(stdouttrace.WithWriter(os.Stderr))
	case "otlp":
		exp, err = otlptracehttp.New(ctx)
	default:
		return nil, fmt.Errorf("неизвестный экспортер трассировок %q", exporter)
	}
	if err != nil {
		return nil, fmt.Errorf("экспортер трассировок: %w", err)
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(service), semconv.ServiceVersion(version)))
	if err != nil {
		return nil, fmt.Errorf("ресурс трассировок: %w", err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return tp.Shutdown, nil
}

// Metrics — реестр Prometheus с метриками процесса и Go, и обработчик /metrics.
type Metrics struct {
	Registry     *prometheus.Registry
	HTTPRequests *prometheus.CounterVec
	HTTPDuration *prometheus.HistogramVec
}

// NewMetrics создаёт реестр.
func NewMetrics(namespace string) *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	m := &Metrics{
		Registry:     reg,
		HTTPRequests: prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: namespace, Name: "http_requests_total", Help: "HTTP-запросы"}, []string{"method", "route", "status"}),
		HTTPDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: namespace, Name: "http_request_duration_seconds", Help: "Длительность HTTP-запросов", Buckets: prometheus.DefBuckets}, []string{"method", "route"}),
	}
	reg.MustRegister(m.HTTPRequests, m.HTTPDuration)
	return m
}

// Handler — обработчик /metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}

// Instrument оборачивает обработчик трассировкой OpenTelemetry.
func Instrument(h http.Handler, operation string) http.Handler {
	return otelhttp.NewHandler(h, operation)
}
