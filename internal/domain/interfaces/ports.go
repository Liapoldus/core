// Package interfaces contains domain ports only.
package interfaces

import "github.com/Liapoldus/core/internal/domain/models"

type ConfigSource interface {
	Read(path string) ([]byte, error)
}

type ConfigCompiler interface {
	Compile(path string) (models.CompiledGraph, error)
}

type SnapshotStore interface {
	Active() models.Snapshot
	Replace(models.Snapshot) error
}

type RouteMatcher interface {
	Matches(any) bool
}

type CertificateProvider interface {
	Certificate(models.TLSProfile) (any, error)
}

type UpstreamResolver interface {
	Resolve(name string) ([]models.UpstreamTarget, error)
}

type PolicyEngine interface {
	Evaluate(any) (models.PolicyDecision, error)
}

type ReleaseStore interface {
	Publish(site, source string) (models.Release, error)
	Rollback(site string) (models.Release, error)
}

type PluginSupervisor interface {
	Restart(instance string) error
}

type Authorizer interface {
	Authorize(models.Actor, string) error
}

type TelemetrySink interface {
	Record(models.TelemetryEvent)
}
