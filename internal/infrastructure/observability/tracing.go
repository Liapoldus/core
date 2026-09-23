package observability

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type TraceContract struct {
	SamplingParentBased                 string
	SamplingAlwaysOn                    string
	SamplingAlwaysOff                   string
	InitializationTimeout               string
	InvalidSamplingMessage              string
	InvalidInitializationTimeoutMessage string
	ScopeName                           string
	ServiceName                         string
	ServiceNameAttribute                string
	SpanName                            string
	ExporterName                        string
	ExporterLabel                       string
	ExportFailureMessage                string
	MethodAttribute                     string
	ListenerAttribute                   string
	StatusAttribute                     string
}

type TracingRuntime struct {
	provider      *sdktrace.TracerProvider
	tracer        trace.Tracer
	propagator    propagation.TextMapPropagator
	contract      TraceContract
	registry      *Registry
	logger        *JSONLogger
	previousError otel.ErrorHandler
}

type traceErrorHandler struct{ runtime *TracingRuntime }

func (h traceErrorHandler) Handle(error) { h.runtime.failed() }

func NewTracingRuntime(endpoint, sampling string, contract TraceContract, sensitiveTerms []string, registry *Registry) (*TracingRuntime, error) {
	var sampler sdktrace.Sampler
	switch sampling {
	case contract.SamplingAlwaysOn:
		sampler = sdktrace.AlwaysSample()
	case contract.SamplingAlwaysOff:
		sampler = sdktrace.NeverSample()
	case "", contract.SamplingParentBased:
		sampler = sdktrace.ParentBased(sdktrace.AlwaysSample())
	default:
		return nil, errors.New(contract.InvalidSamplingMessage)
	}
	initializationTimeout, err := time.ParseDuration(contract.InitializationTimeout)
	if err != nil || initializationTimeout <= 0 {
		return nil, errors.New(contract.InvalidInitializationTimeoutMessage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), initializationTimeout)
	defer cancel()
	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(sampler),
		sdktrace.WithResource(resource.NewWithAttributes("", attribute.String(contract.ServiceNameAttribute, contract.ServiceName))),
	)
	runtime := &TracingRuntime{
		provider:   provider,
		tracer:     provider.Tracer(contract.ScopeName),
		propagator: propagation.TraceContext{},
		contract:   contract,
		registry:   registry,
		logger:     NewJSONLogger(os.Stderr, slog.LevelWarn, sensitiveTerms),
	}
	runtime.previousError = otel.GetErrorHandler()
	otel.SetErrorHandler(traceErrorHandler{runtime: runtime})
	return runtime, nil
}

func (r *TracingRuntime) StartHTTP(request *http.Request, listener string) (*http.Request, func(int)) {
	if r == nil || r.tracer == nil || request == nil {
		return request, func(int) {}
	}
	ctx := r.propagator.Extract(request.Context(), propagation.HeaderCarrier(request.Header))
	ctx, span := r.tracer.Start(ctx, r.contract.SpanName,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String(r.contract.MethodAttribute, request.Method),
			attribute.String(r.contract.ListenerAttribute, listener),
		),
	)
	return request.WithContext(ctx), func(status int) {
		span.SetAttributes(attribute.Int(r.contract.StatusAttribute, status))
		if status >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, "")
		}
		span.End()
	}
}

func (r *TracingRuntime) Shutdown(ctx context.Context) error {
	if r == nil || r.provider == nil {
		return nil
	}
	err := r.provider.Shutdown(ctx)
	otel.SetErrorHandler(r.previousError)
	if err != nil {
		r.failed()
	}
	return err
}

func (r *TracingRuntime) failed() {
	if r.registry != nil && r.registry.ExportFailures != nil {
		r.registry.ExportFailures.WithLabelValues(r.contract.ExporterName).Inc()
	}
	r.logger.Log(context.Background(), slog.LevelWarn, r.contract.ExportFailureMessage,
		slog.String(r.contract.ExporterLabel, r.contract.ExporterName),
	)
}
