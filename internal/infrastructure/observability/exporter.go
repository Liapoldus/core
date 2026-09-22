// Package observability contains log, metrics and trace exporters.
package observability

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Registry struct {
	Requests        *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
	Management      *prometheus.CounterVec
	AuditRecords    *prometheus.CounterVec
	ExportFailures  *prometheus.CounterVec
	registry        *prometheus.Registry
}

func NewRegistry() *Registry {
	r := &Registry{registry: prometheus.NewRegistry()}
	r.Requests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "liapoldus_http_requests_total", Help: "Gateway HTTP requests."}, []string{"listener", "route", "site", "method", "status"})
	r.RequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "liapoldus_http_request_duration_seconds", Help: "Gateway HTTP request duration."}, []string{"listener", "route", "site", "method", "status"})
	r.Management = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "liapoldus_management_requests_total", Help: "Management API requests."}, []string{"method", "status"})
	r.AuditRecords = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "liapoldus_audit_records_total", Help: "Audit records written."}, []string{"action", "result"})
	r.ExportFailures = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "liapoldus_otel_export_failures_total", Help: "Telemetry exporter failures."}, []string{"exporter"})
	r.registry.MustRegister(r.Requests, r.RequestDuration, r.Management, r.AuditRecords, r.ExportFailures)
	return r
}
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.registry, promhttp.HandlerOpts{})
}
func (r *Registry) ObserveHTTP(listener, route, site, method, status string, duration time.Duration) {
	r.Requests.WithLabelValues(listener, route, site, method, status).Inc()
	r.RequestDuration.WithLabelValues(listener, route, site, method, status).Observe(duration.Seconds())
}
func (r *Registry) ObserveManagement(method, status string) {
	r.Management.WithLabelValues(method, status).Inc()
}
func (r *Registry) ObserveAudit(action, result string) {
	r.AuditRecords.WithLabelValues(action, result).Inc()
}

type JSONLogger struct{ logger *slog.Logger }

func NewJSONLogger(output io.Writer, level slog.Level) *JSONLogger {
	return &JSONLogger{logger: slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level}))}
}
func (l *JSONLogger) Log(ctx context.Context, level slog.Level, message string, attrs ...slog.Attr) {
	if l == nil || l.logger == nil {
		return
	}
	clean := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if sensitive(attr.Key) {
			attr.Value = slog.StringValue("[REDACTED]")
		}
		clean = append(clean, attr)
	}
	l.logger.LogAttrs(ctx, level, message, clean...)
}
func sensitive(key string) bool {
	key = strings.ToLower(key)
	return strings.Contains(key, "authorization") || strings.Contains(key, "cookie") || strings.Contains(key, "secret") || strings.Contains(key, "private-key") || strings.Contains(key, "service-key") || strings.Contains(key, "grant") || key == "token" || key == "password"
}

type OTLPExporter struct {
	Endpoint string
	Client   *http.Client
	Failures *prometheus.CounterVec
}

func NewOTLPExporter(endpoint string, failures *prometheus.CounterVec) (*OTLPExporter, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errInvalidEndpoint{}
	}
	return &OTLPExporter{Endpoint: endpoint, Client: &http.Client{Timeout: 5 * time.Second}, Failures: failures}, nil
}

type errInvalidEndpoint struct{}

func (errInvalidEndpoint) Error() string { return "invalid OTLP endpoint" }
func (e *OTLPExporter) Export(ctx context.Context, payload []byte) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.Endpoint, strings.NewReader(string(payload)))
	if err != nil {
		e.failed()
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := e.Client.Do(request)
	if err != nil {
		e.failed()
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		e.failed()
		return exportStatusError(response.StatusCode)
	}
	return nil
}
func (e *OTLPExporter) ExportAsync(ctx context.Context, payload []byte) {
	copyPayload := append([]byte(nil), payload...)
	go func() { _ = e.Export(ctx, copyPayload) }()
}
func (e *OTLPExporter) failed() {
	if e.Failures != nil {
		e.Failures.WithLabelValues("otlp").Inc()
	}
}

type exportStatusError int

func (exportStatusError) Error() string { return "OTLP exporter returned HTTP status" }
