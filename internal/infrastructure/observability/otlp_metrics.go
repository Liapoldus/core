package observability

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	dto "github.com/prometheus/client_model/go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

type OTLPExporter struct {
	registry       *Registry
	exporter       *otlpmetrichttp.Exporter
	interval       time.Duration
	exporterName   string
	exporterLabel  string
	failureMessage string
	scopeName      string
	unit           string
	startedAt      time.Time
	logger         *JSONLogger
}

func NewOTLPExporter(endpoint, interval, scopeName, unit, exporterName, exporterLabel, failureMessage, invalidIntervalMessage string, sensitiveTerms []string, registry *Registry) (*OTLPExporter, error) {
	parsedInterval, err := time.ParseDuration(interval)
	if err != nil {
		return nil, err
	}
	if parsedInterval <= 0 {
		return nil, errors.New(invalidIntervalMessage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), parsedInterval)
	defer cancel()
	exporter, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(endpoint))
	if err != nil {
		return nil, err
	}
	return &OTLPExporter{
		registry:       registry,
		exporter:       exporter,
		interval:       parsedInterval,
		exporterName:   exporterName,
		exporterLabel:  exporterLabel,
		failureMessage: failureMessage,
		scopeName:      scopeName,
		unit:           unit,
		startedAt:      time.Now(),
		logger:         NewJSONLogger(os.Stderr, slog.LevelWarn, sensitiveTerms),
	}, nil
}

func (e *OTLPExporter) Run(ctx context.Context) {
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()
	defer func() { _ = e.exporter.Shutdown(context.Background()) }()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.export(ctx)
		}
	}
}

func (e *OTLPExporter) export(ctx context.Context) {
	families, err := e.registry.registry.Gather()
	if err != nil {
		e.failed(ctx)
		return
	}
	metrics := make([]metricdata.Metrics, 0, len(families))
	now := time.Now()
	for _, family := range families {
		converted, ok := convertMetricFamily(family, e.startedAt, now, e.unit)
		if ok {
			metrics = append(metrics, converted)
		}
	}
	if len(metrics) == 0 {
		return
	}
	data := &metricdata.ResourceMetrics{
		Resource: resource.Empty(),
		ScopeMetrics: []metricdata.ScopeMetrics{{
			Scope:   instrumentation.Scope{Name: e.scopeName},
			Metrics: metrics,
		}},
	}
	if err := e.exporter.Export(ctx, data); err != nil {
		e.failed(ctx)
	}
}

func (e *OTLPExporter) failed(ctx context.Context) {
	e.registry.ExportFailures.WithLabelValues(e.exporterName).Inc()
	e.logger.Log(ctx, slog.LevelWarn, e.failureMessage, slog.String(e.exporterLabel, e.exporterName))
}

func convertMetricFamily(family *dto.MetricFamily, startedAt, now time.Time, unit string) (metricdata.Metrics, bool) {
	converted := metricdata.Metrics{Name: family.GetName(), Description: family.GetHelp(), Unit: unit}
	switch family.GetType() {
	case dto.MetricType_COUNTER:
		points := make([]metricdata.DataPoint[float64], 0, len(family.Metric))
		for _, sample := range family.Metric {
			points = append(points, metricdata.DataPoint[float64]{
				Attributes: metricAttributes(sample), StartTime: startedAt, Time: now,
				Value: sample.GetCounter().GetValue(),
			})
		}
		converted.Data = metricdata.Sum[float64]{DataPoints: points, Temporality: metricdata.CumulativeTemporality, IsMonotonic: true}
	case dto.MetricType_GAUGE:
		points := make([]metricdata.DataPoint[float64], 0, len(family.Metric))
		for _, sample := range family.Metric {
			points = append(points, metricdata.DataPoint[float64]{
				Attributes: metricAttributes(sample), StartTime: startedAt, Time: now,
				Value: sample.GetGauge().GetValue(),
			})
		}
		converted.Data = metricdata.Gauge[float64]{DataPoints: points}
	case dto.MetricType_HISTOGRAM:
		points := make([]metricdata.HistogramDataPoint[float64], 0, len(family.Metric))
		for _, sample := range family.Metric {
			histogram := sample.GetHistogram()
			buckets := histogram.GetBucket()
			bounds := make([]float64, 0, len(buckets))
			counts := make([]uint64, 0, len(buckets)+1)
			var previous uint64
			for _, bucket := range buckets {
				count := bucket.GetCumulativeCount()
				bounds = append(bounds, bucket.GetUpperBound())
				counts = append(counts, count-previous)
				previous = count
			}
			counts = append(counts, histogram.GetSampleCount()-previous)
			points = append(points, metricdata.HistogramDataPoint[float64]{
				Attributes: metricAttributes(sample), StartTime: startedAt, Time: now,
				Count: histogram.GetSampleCount(), Bounds: bounds, BucketCounts: counts,
				Sum: histogram.GetSampleSum(),
			})
		}
		converted.Data = metricdata.Histogram[float64]{DataPoints: points, Temporality: metricdata.CumulativeTemporality}
	default:
		return metricdata.Metrics{}, false
	}
	return converted, true
}

func metricAttributes(sample *dto.Metric) attribute.Set {
	values := make([]attribute.KeyValue, 0, len(sample.Label))
	for _, pair := range sample.Label {
		values = append(values, attribute.String(pair.GetName(), pair.GetValue()))
	}
	return attribute.NewSet(values...)
}
