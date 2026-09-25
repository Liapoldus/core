package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Liapoldus/core/internal/infrastructure/config"
)

type PluginInstanceRecord struct {
	ID           string
	Mode         string
	State        string
	Revision     int64
	ManifestJSON []byte
}

func ListPluginInstances(ctx context.Context, database *sql.DB, contract config.PluginInventoryContract) ([]PluginInstanceRecord, error) {
	if database == nil || contract.SelectInstances == "" {
		return nil, errors.New(contract.Diagnostics.InvalidContract)
	}
	rows, err := database.QueryContext(ctx, contract.SelectInstances)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	instances := make([]PluginInstanceRecord, 0)
	for rows.Next() {
		var instance PluginInstanceRecord
		if err := rows.Scan(&instance.ID, &instance.Mode, &instance.State, &instance.Revision, &instance.ManifestJSON); err != nil {
			return nil, err
		}
		if instance.ID == "" || instance.Revision < 1 || !contains(contract.ValidModes, instance.Mode) || !contains(contract.ValidStates, instance.State) || !json.Valid(instance.ManifestJSON) {
			return nil, errors.New(contract.Diagnostics.InvalidRecord)
		}
		instances = append(instances, instance)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return instances, nil
}

func contains(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
