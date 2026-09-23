// Package observability contains log, metrics and trace exporters.
package observability

import (
	"context"
	"io"
	"log/slog"
	"net/http"
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
	accessLogger    *AccessLogger
	tracing         *TracingRuntime
}

type RegistryContract struct {
	RequestTotalName        string
	RequestTotalHelp        string
	RequestDurationName     string
	RequestDurationHelp     string
	ManagementTotalName     string
	ManagementTotalHelp     string
	AuditRecordsTotalName   string
	AuditRecordsTotalHelp   string
	ExportFailuresTotalName string
	ExportFailuresTotalHelp string
	ListenerLabel           string
	RouteLabel              string
	SiteLabel               string
	MethodLabel             string
	StatusLabel             string
	ExporterLabel           string
	ActionLabel             string
	ResultLabel             string
}

func NewRegistry(contract RegistryContract) *Registry {
	r := &Registry{registry: prometheus.NewRegistry()}
	requestLabels := []string{contract.ListenerLabel, contract.RouteLabel, contract.SiteLabel, contract.MethodLabel, contract.StatusLabel}
	r.Requests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: contract.RequestTotalName, Help: contract.RequestTotalHelp}, requestLabels)
	r.RequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: contract.RequestDurationName, Help: contract.RequestDurationHelp}, requestLabels)
	r.Management = prometheus.NewCounterVec(prometheus.CounterOpts{Name: contract.ManagementTotalName, Help: contract.ManagementTotalHelp}, []string{contract.MethodLabel, contract.StatusLabel})
	r.AuditRecords = prometheus.NewCounterVec(prometheus.CounterOpts{Name: contract.AuditRecordsTotalName, Help: contract.AuditRecordsTotalHelp}, []string{contract.ActionLabel, contract.ResultLabel})
	r.ExportFailures = prometheus.NewCounterVec(prometheus.CounterOpts{Name: contract.ExportFailuresTotalName, Help: contract.ExportFailuresTotalHelp}, []string{contract.ExporterLabel})
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

func (r *Registry) SetAccessLogger(logger *AccessLogger) {
	if r != nil {
		r.accessLogger = logger
	}
}

func (r *Registry) WriteAccess(record AccessRecord) {
	if r != nil {
		r.accessLogger.Write(record)
	}
}

func (r *Registry) EnsureRequestID(response http.ResponseWriter, request *http.Request) string {
	if r == nil {
		return ""
	}
	return r.accessLogger.EnsureRequestID(response, request)
}

func (r *Registry) SetTracingRuntime(runtime *TracingRuntime) {
	if r != nil {
		r.tracing = runtime
	}
}

func (r *Registry) StartHTTPTrace(request *http.Request, listener string) (*http.Request, func(int)) {
	if r == nil {
		return request, func(int) {}
	}
	return r.tracing.StartHTTP(request, listener)
}

type JSONLogger struct {
	logger         *slog.Logger
	sensitiveTerms []string
}

func NewJSONLogger(output io.Writer, level slog.Level, sensitiveTerms []string) *JSONLogger {
	return &JSONLogger{
		logger:         slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level})),
		sensitiveTerms: append([]string(nil), sensitiveTerms...),
	}
}
func (l *JSONLogger) Log(ctx context.Context, level slog.Level, message string, attrs ...slog.Attr) {
	if l == nil || l.logger == nil {
		return
	}
	clean := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if l.sensitive(attr.Key) {
			attr.Value = slog.StringValue("[REDACTED]")
		}
		clean = append(clean, attr)
	}
	l.logger.LogAttrs(ctx, level, message, clean...)
}
func (l *JSONLogger) sensitive(key string) bool {
	key = strings.ToLower(key)
	for _, term := range l.sensitiveTerms {
		if strings.Contains(key, strings.ToLower(term)) {
			return true
		}
	}
	return false
}
