// Package runtime orchestrates immutable snapshot replacement.
package runtime

import domain "github.com/Liapoldus/core/internal/domain/runtime"

type Service struct {
	Store domain.SnapshotStore
}

func (service Service) Apply(snapshot domain.Snapshot) error {
	return service.Store.Replace(snapshot)
}
