package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
)

//go:embed sql/plugin_instance_registration.sql
var registerPluginInstanceSQL string

//go:embed sql/plugin_replica_registration.sql
var registerPluginReplicaSQL string

// PluginInstanceRecord is one row of the plugin inventory. Launch settings and
// capability descriptors were v2 artifacts of local process supervision and are
// deliberately absent: an inventory record describes a declared instance and its
// active generation, nothing about how a replica is executed.
type PluginInstanceRecord struct {
	ID           string
	State        string
	Revision     int64
	ManifestJSON []byte
	SettingsJSON []byte
}

type PluginInstanceQuery struct {
	SelectInstances string
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
		if err := rows.Scan(&instance.ID, &instance.State, &instance.Revision, &instance.ManifestJSON, &instance.SettingsJSON); err != nil {
			return nil, err
		}
		if instance.ID == "" || instance.Revision < 1 || !contains(query.ValidStates, instance.State) || !json.Valid(instance.ManifestJSON) {
			return nil, errors.New(query.InvalidRecord)
		}
		instances = append(instances, instance)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return instances, nil
}

func RegisterPluginInstance(ctx context.Context, database *sql.DB, instanceID string, manifestJSON []byte, state, updatedAt string) error {
	_, err := database.ExecContext(ctx, registerPluginInstanceSQL, instanceID, manifestJSON, state, updatedAt)
	return err
}

func RegisterPluginReplica(ctx context.Context, database *sql.DB, instanceID, replicaID, state, observedAt string) error {
	_, err := database.ExecContext(ctx, registerPluginReplicaSQL, instanceID, replicaID, state, observedAt)
	return err
}

func contains(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
