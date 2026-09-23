package application

import (
	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

type RegistryService struct {
	Store interfaces.ReleaseStore
}

func (service RegistryService) Publish(site, source string) (models.Release, error) {
	return service.Store.Publish(site, source)
}

func (service RegistryService) Rollback(site string) (models.Release, error) {
	return service.Store.Rollback(site)
}

func (service RegistryService) Versions(site string) ([]models.Release, error) {
	return service.Store.Versions(site)
}

func (service RegistryService) Current(site string) (models.Release, error) {
	return service.Store.Current(site)
}

func (service RegistryService) Previous(site string) (models.Release, error) {
	return service.Store.Previous(site)
}
