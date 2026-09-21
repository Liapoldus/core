// Package runtime defines active immutable Gateway snapshots.
package runtime

import "github.com/Liapoldus/core/internal/domain/configuration"

type Snapshot struct {
	Graph configuration.CompiledGraph
}

type SnapshotStore interface {
	Active() Snapshot
	Replace(Snapshot) error
}
