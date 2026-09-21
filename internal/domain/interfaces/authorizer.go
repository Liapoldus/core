package interfaces

import "github.com/Liapoldus/core/internal/domain/models"

type Authorizer interface {
	Authorize(models.Actor, string) error
}
