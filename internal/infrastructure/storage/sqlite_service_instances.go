package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// ServiceInstanceRecord is one row of the service inventory. Launch settings and
// capability descriptors were v2 artifacts of local process supervision and are
// deliberately absent: an inventory record describes a declared instance and its
// active generation, nothing about how a replica is executed.
type ServiceInstanceRecord struct {
	ID           string
	State        string
	Revision     int64
	ManifestJSON []byte
	SettingsJSON []byte
}

type ServiceInstanceQuery struct {
	SelectInstances string
	ValidStates     []string
	InvalidContract string
	InvalidRecord   string
}

func ListServiceInstances(ctx context.Context, database *sql.DB, query ServiceInstanceQuery) ([]ServiceInstanceRecord, error) {
	if database == nil || query.SelectInstances == "" {
		return nil, errors.New(query.InvalidContract)
	}
	rows, err := database.QueryContext(ctx, query.SelectInstances)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	instances := make([]ServiceInstanceRecord, 0)
	for rows.Next() {
		var instance ServiceInstanceRecord
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

func RegisterServiceInstance(ctx context.Context, database *sql.DB, instanceID string, manifestJSON []byte, state, updatedAt string) error {
	_, err := database.ExecContext(ctx, registerServiceInstanceSQL, instanceID, manifestJSON, state, updatedAt)
	return err
}

func RegisterServiceReplica(ctx context.Context, database *sql.DB, instanceID, replicaID, state, observedAt string) error {
	_, err := database.ExecContext(ctx, registerServiceReplicaSQL, instanceID, replicaID, state, observedAt)
	return err
}

// RegisterServiceInstanceReplica commits the generic instance and its first
// observed replica together. A failed replica insert must not leave a durable
// service instance that Core has not admitted into its live directory.
func RegisterServiceInstanceReplica(
	ctx context.Context,
	database *sql.DB,
	instanceID, replicaID string,
	manifestJSON []byte,
	instanceState, replicaState, updatedAt string,
) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, registerServiceInstanceSQL, instanceID, manifestJSON, instanceState, updatedAt); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, registerServiceReplicaSQL, instanceID, replicaID, replicaState, updatedAt); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, markServiceInstanceRegisteredSQL, instanceID, updatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// ListRegisteredServiceInstances returns the durable registration-mode markers.
// It intentionally does not restore replica endpoints, identities, readiness,
// or leases; every replica must authenticate and register again after restart.
func ListRegisteredServiceInstances(ctx context.Context, database *sql.DB) ([]string, error) {
	if database == nil {
		return nil, errors.New("service registration store unavailable")
	}
	rows, err := database.QueryContext(ctx, listRegisteredServiceInstancesSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	instances := make([]string, 0)
	for rows.Next() {
		var instanceID string
		if err := rows.Scan(&instanceID); err != nil {
			return nil, err
		}
		if instanceID == "" {
			return nil, errors.New("invalid service registration marker")
		}
		instances = append(instances, instanceID)
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
