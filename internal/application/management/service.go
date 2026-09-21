// Package management orchestrates authorized control-plane operations.
package management

import domain "github.com/Liapoldus/core/internal/domain/management"

type Service struct {
	Authorizer domain.Authorizer
}

func (service Service) Authorize(actor domain.Actor, action string) error {
	return service.Authorizer.Authorize(actor, action)
}
