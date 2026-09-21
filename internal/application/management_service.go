package application

import (
	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

type ManagementService struct {
	Authorizer interfaces.Authorizer
}

func (service ManagementService) Authorize(actor models.Actor, action string) error {
	return service.Authorizer.Authorize(actor, action)
}
