// Package registry orchestrates serialized release publication and rollback.
package registry

import domain "github.com/Liapoldus/core/internal/domain/registry"

type Service struct {
	Store domain.Store
}

func (service Service) Publish(site, source string) (domain.Release, error) {
	return service.Store.Publish(site, source)
}

func (service Service) Rollback(site string) (domain.Release, error) {
	return service.Store.Rollback(site)
}
