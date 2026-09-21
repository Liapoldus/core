package application

import domain "github.com/Liapoldus/core/internal/domain/registry"

type RegistryService struct {
	Store domain.Store
}

func (service RegistryService) Publish(site, source string) (domain.Release, error) {
	return service.Store.Publish(site, source)
}

func (service RegistryService) Rollback(site string) (domain.Release, error) {
	return service.Store.Rollback(site)
}
