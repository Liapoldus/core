package interfaces

import "github.com/Liapoldus/core/internal/domain/models"

type SnapshotStore interface {
	Active() models.Snapshot
	Replace(models.Snapshot) error
}
