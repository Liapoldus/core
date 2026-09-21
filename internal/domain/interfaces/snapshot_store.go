package interfaces

import "github.com/Liapoldus/core/internal/domain/models"

type SnapshotStore interface {
	Active() models.Snapshot
	Prepare(models.Snapshot) (models.PreparedSnapshot, error)
	Activate(models.PreparedSnapshot) (models.Snapshot, error)
	Drain(models.Snapshot) error
}
