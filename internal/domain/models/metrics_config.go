package models

type MetricsConfig struct {
	Prometheus bool
	OTLP       *MetricsOTLP
}
