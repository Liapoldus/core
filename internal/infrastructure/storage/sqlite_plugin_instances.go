package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type PluginInstanceRecord struct {
	ID           string
	Mode         string
	State        string
	Revision     int64
	ManifestJSON []byte
}

type PluginInstanceQuery struct {
	SelectInstances string
	ValidModes      []string
	ValidStates     []string
	InvalidContract string
	InvalidRecord   string
}

func ListPluginInstances(ctx context.Context, database *sql.DB, query PluginInstanceQuery) ([]PluginInstanceRecord, error) {
	if database == nil || query.SelectInstances == "" {
		return nil, errors.New(query.InvalidContract)
	}
	rows, err := database.QueryContext(ctx, query.SelectInstances)
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
		if instance.ID == "" || instance.Revision < 1 || !contains(query.ValidModes, instance.Mode) || !contains(query.ValidStates, instance.State) || !json.Valid(instance.ManifestJSON) {
			return nil, errors.New(query.InvalidRecord)
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
