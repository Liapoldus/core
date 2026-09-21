package interfaces

import "github.com/Liapoldus/core/internal/domain/models"

type UpstreamResolver interface {
	Resolve(name string) ([]models.UpstreamTarget, error)
}
