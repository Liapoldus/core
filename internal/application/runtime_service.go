package application

import (
	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

type RuntimeService struct {
	Store interfaces.SnapshotStore
}

func (service RuntimeService) Apply(snapshot models.Snapshot) error {
	return service.Store.Replace(snapshot)
}
