package application

import (
	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

type RuntimeService struct {
	Store interfaces.SnapshotStore
}

func (service RuntimeService) Apply(snapshot models.Snapshot) error {
	prepared, err := service.Store.Prepare(snapshot)
	if err != nil {
		return err
	}
	previous, err := service.Store.Activate(prepared)
	if err != nil {
		return err
	}
	if previous.Graph.Revision.Value == "" && previous.Graph.Revision.Digest == "" {
		return nil
	}
	return service.Store.Drain(previous)
}
