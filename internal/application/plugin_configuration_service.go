package application

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

type PluginConfigurationService struct {
	Store       interfaces.PluginConfigurationStore
	Unavailable string
}

func (service *PluginConfigurationService) Current(ctx context.Context, instanceID string) (models.PluginConfigurationRevision, error) {
	if service == nil {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationUnavailable{}
	}
	if service.Store == nil {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationUnavailable{Message: service.Unavailable}
	}
	revision, _, err := service.Store.Current(ctx, instanceID)
	return revision, err
}
