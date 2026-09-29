package bootstrap

import (
	"errors"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

// PresentPluginInventory exposes only generic Core-owned instance metadata. The
// stored manifest document is plugin-owned and is never decoded or interpreted
// by Core.
func PresentPluginInventory(records []storage.PluginInstanceRecord, contract config.PluginInventoryContract) ([]any, error) {
	items := make([]any, 0, len(records))
	for _, record := range records {
		if record.ID == "" || record.Revision < 1 {
			return nil, errors.New(contract.Diagnostics.InvalidRecord)
		}
		items = append(items, map[string]any{
			contract.JSON.ID:       record.ID,
			contract.JSON.Mode:     record.Mode,
			contract.JSON.State:    record.State,
			contract.JSON.Revision: record.Revision,
		})
	}
	return items, nil
}
