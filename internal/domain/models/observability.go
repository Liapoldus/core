package models

type Observability struct {
	Logging LoggingConfig
	Metrics MetricsConfig
	Tracing TracingConfig
}
