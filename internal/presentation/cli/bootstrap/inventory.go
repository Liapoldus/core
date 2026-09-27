package bootstrap

import (
	"errors"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

func PresentPluginInventory(records []storage.PluginInstanceRecord, contract config.PluginInventoryContract) ([]any, error) {
	items := make([]any, 0, len(records))
	for _, record := range records {
		manifest, err := plugins.ParseManifestInventory(record.ID, record.ManifestJSON)
		if err != nil {
			return nil, errors.New(contract.Diagnostics.InvalidManifest)
		}
		descriptors := make([]map[string]any, 0, len(manifest.Descriptors))
		for _, descriptor := range manifest.Descriptors {
			descriptors = append(descriptors, map[string]any{
				contract.JSON.DescriptorCapability: descriptor.Capability,
				contract.JSON.DescriptorModes:      descriptor.Modes,
			})
		}
		items = append(items, map[string]any{
			contract.JSON.ID:                    record.ID,
			contract.JSON.Mode:                  record.Mode,
			contract.JSON.State:                 record.State,
			contract.JSON.Revision:              record.Revision,
			contract.JSON.Capabilities:          manifest.Capabilities,
			contract.JSON.CapabilityDescriptors: descriptors,
		})
	}
	return items, nil
}
