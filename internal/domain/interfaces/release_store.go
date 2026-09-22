package interfaces

import "github.com/Liapoldus/core/internal/domain/models"

type ReleaseStore interface {
	Publish(site, source string) (models.Release, error)
	Rollback(site string) (models.Release, error)
	Versions(site string) ([]models.Release, error)
}
