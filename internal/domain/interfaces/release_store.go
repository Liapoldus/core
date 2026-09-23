package interfaces

import "github.com/Liapoldus/core/internal/domain/models"

type ReleaseStore interface {
	Publish(site, source string) (models.Release, error)
	Rollback(site string) (models.Release, error)
	PublishIfCurrent(site, source string, expected *string) (models.Release, *models.ReleaseRevisionConflict, error)
	RollbackIfCurrent(site string, expected *string) (models.Release, *models.ReleaseRevisionConflict, error)
	Versions(site string) ([]models.Release, error)
	Current(site string) (models.Release, error)
	Previous(site string) (models.Release, error)
}
