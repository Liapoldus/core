package plugins

import (
	"bytes"
	"context"
	"strconv"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	sdkmodels "liapoldus.local/plugin-sdk/domain/models"
)

type SDKReloadClient interface {
	Reload(context.Context, sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error)
}

type SDKConfigurationApplier struct {
	Store   interfaces.PluginConfigurationStore
	Clients map[string]SDKReloadClient
}

var _ interfaces.PluginConfigurationApplier = (*SDKConfigurationApplier)(nil)

func (applier *SDKConfigurationApplier) ApplyConfiguration(ctx context.Context, instanceID, generation string, rawJSON []byte) error {
	if applier == nil || applier.Store == nil || instanceID == "" {
		return ErrPluginUnavailable
	}
	client := applier.Clients[instanceID]
	if client == nil {
		return ErrPluginUnavailable
	}
	generationNumber, err := strconv.ParseInt(generation, 10, 64)
	if err != nil || generationNumber < 1 || strconv.FormatInt(generationNumber, 10) != generation {
		return ErrProtocolViolation
	}
	revision, err := applier.Store.GetRevision(ctx, instanceID, generationNumber)
	if err != nil {
		return err
	}
	if !bytes.Equal(revision.SettingsJSON, rawJSON) {
		return ErrProtocolViolation
	}
	_, err = client.Reload(ctx, sdkmodels.Reload{
		Generation:    generation,
		SHA256:        revision.Digest,
		SchemaVersion: strconv.FormatInt(revision.SchemaVersion, 10),
	})
	return err
}
