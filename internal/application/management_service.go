package application

import domain "github.com/Liapoldus/core/internal/domain/management"

type ManagementService struct {
	Authorizer domain.Authorizer
}

func (service ManagementService) Authorize(actor domain.Actor, action string) error {
	return service.Authorizer.Authorize(actor, action)
}
