// Package models contains Gateway domain data types only.
package models

type Revision struct {
	Value  string
	Digest string
}

type CompiledGraph struct {
	Revision Revision
}

type Snapshot struct {
	Graph CompiledGraph
}

type Capability struct {
	Name string
}

type Actor struct {
	ID string
}

type TLSProfile struct{}

type Release struct {
	ID string
}

type TelemetryEvent struct{}

type Route struct{}
type Action struct{}
type PolicyDecision uint8
type UpstreamTarget struct{}
