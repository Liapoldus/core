package runtime

import (
	"errors"

	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/infrastructure/storage"
)

// PresentServiceInventory exposes only generic Core-owned instance metadata. The
// stored manifest document is service-owned and is never decoded or interpreted
// by Core.
func PresentServiceInventory(records []storage.ServiceInstanceRecord, contract config.ServiceInventoryContract) ([]any, error) {
	items := make([]any, 0, len(records))
	for _, record := range records {
		if record.ID == "" || record.Revision < 1 {
			return nil, errors.New(contract.Diagnostics.InvalidRecord)
		}
		items = append(items, map[string]any{
			contract.JSON.ID:       record.ID,
			contract.JSON.State:    record.State,
			contract.JSON.Revision: record.Revision,
		})
	}
	return items, nil
}
