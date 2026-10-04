package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/cli/bootstrap"
)

func main() {
	ctx := context.Background()
	directory, err := os.MkdirTemp("", "liapoldus-convergence-")
	check(err)
	defer os.RemoveAll(directory)
	database, err := bootstrap.OpenDatabase(ctx, filepath.Join(directory, "core.db"))
	check(err)
	_, err = database.ExecContext(ctx, "INSERT INTO plugin_instances (id, manifest_json, state) VALUES (?, ?, ?)", "fixture", []byte(`{}`), "configured")
	check(err)
	raw := []byte(`{"enabled":true}`)
	digest := sha256.Sum256(raw)
	_, err = database.ExecContext(ctx, "INSERT INTO plugin_config_generations (instance_id,generation,slot,raw_json,sha256,schema_version,created_at) VALUES (?,?,?,?,?,1,?)", "fixture", 1, "active", raw, hex.EncodeToString(digest[:]), time.Now().UTC().Format(time.RFC3339Nano))
	check(err)
	configurations, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	replicas := storage.NewPluginReplicaStore(database, "invalid observation")
	check(replicas.Record(ctx, storage.PluginReplicaObservation{InstanceID: "fixture", ReplicaID: "replica-1", ObservedGeneration: sql.NullInt64{Int64: 1, Valid: true}, ObservedState: storage.ReplicaObservedAcknowledged}))
	snapshot, err := storage.NewPluginConvergenceSnapshot(ctx, configurations, replicas, []string{"fixture"})
	check(err)
	check(database.Close())
	view, err := snapshot.Current()
	check(err)
	snapshot.Invalidate()
	_, unavailable := snapshot.Current()
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{
		"desiredGeneration":            view.Desired["fixture"],
		"observedGeneration":           view.Records[0].ObservedGeneration.Int64,
		"observations":                 len(view.Records),
		"unavailableAfterInvalidation": unavailable != nil,
	}))
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
