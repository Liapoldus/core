package models

type TracingConfig struct {
	OTLP     *TracingOTLP
	Sampling string
}
