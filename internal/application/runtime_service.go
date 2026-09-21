package application

import domain "github.com/Liapoldus/core/internal/domain/runtime"

type RuntimeService struct {
	Store domain.SnapshotStore
}

func (service RuntimeService) Apply(snapshot domain.Snapshot) error {
	return service.Store.Replace(snapshot)
}
