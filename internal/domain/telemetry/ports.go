// Package telemetry defines redacted observation delivery.
package telemetry

type Event struct{}

type Sink interface {
	Record(Event)
}
