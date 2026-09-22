package interfaces

import "github.com/Liapoldus/core/internal/domain/models"

type RouteMatcher interface {
	Matches(models.RouteInput) bool
}
