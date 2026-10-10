package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/infrastructure/storage"
)

func main() {
	ctx := context.Background()
	sqliteContract, err := config.LoadSQLiteContract()
	check(err)
	database, err := storage.OpenSQLite(ctx, os.Args[1], storage.SQLiteOptions{
		Driver: sqliteContract.Driver, ParentDirectoryMode: sqliteContract.ParentDirectoryMode,
		DatabaseFileMode: sqliteContract.DatabaseFileMode, MaxOpenConnections: sqliteContract.MaxOpenConnections,
		MaxIdleConnections: sqliteContract.MaxIdleConnections, SchemaVersion: sqliteContract.SchemaVersion,
		HasMigrationTableQuery: sqliteContract.HasMigrationTableQuery, MigrationVersionQuery: sqliteContract.MigrationVersionQuery,
		SchemaVersionError: sqliteContract.SchemaVersionError, Pragmas: sqliteContract.Pragmas,
	}, sqliteContract.Schema)
	check(err)
	defer database.Close()
	management, err := config.LoadManagement()
	check(err)

	linkStore, err := storage.NewSQLitePluginLinkPolicyStore(database)
	check(err)
	operationStore, err := storage.NewSQLiteOperationStore(database)
	check(err)
	snapshot, err := storage.NewPluginLinkPolicySnapshot(ctx, linkStore)
	check(err)
	service := &application.PluginLinkPolicyService{
		Store: linkStore, Reader: linkStore, Operations: operationStore,
		Kinds: application.PluginLinkPolicyOperationKinds{
			Create:  management.OperationKinds.PluginLinkCreate,
			Replace: management.OperationKinds.PluginLinkReplace,
			Delete:  management.OperationKinds.PluginLinkDelete,
		},
		States: application.PluginLinkPolicyOperationStates{
			Pending: management.Statuses.Pending, Succeeded: management.Statuses.Succeeded, Failed: management.Statuses.Failed,
		},
		Now:     time.Now,
		Refresh: func() { _ = snapshot.Refresh(context.Background()) },
	}

	observations := map[string]any{}
	observations["initialRules"] = len(snapshot.View().Rules("caller-a"))

	policy := models.PluginLinkPolicy{
		CallerInstanceID: "caller-a",
		TargetInstanceID: "target-b",
		Rules: []models.PeerLinkRule{{
			CallerInstanceID: "caller-a", TargetInstanceID: "target-b",
			PlacementRule: models.PeerLinkPlacementRemote, Carrier: models.PeerLinkCarrierTCP, Weight: 5,
		}},
	}
	_, err = service.Create(ctx, policy, models.AuditRecord{}, "admin", "req-1", "plugin-link", "key-create", "digest-a")
	check(err)
	createdRules := snapshot.View().Rules("caller-a")
	observations["afterCreateRules"] = len(createdRules)
	observations["createWeight"] = int(createdRules[0].Weight)
	observations["otherCallerRules"] = len(snapshot.View().Rules("caller-other"))

	replacement := models.PluginLinkPolicy{
		CallerInstanceID: "caller-a",
		TargetInstanceID: "target-b",
		Rules: []models.PeerLinkRule{{
			CallerInstanceID: "caller-a", TargetInstanceID: "target-b",
			PlacementRule: models.PeerLinkPlacementSame, Carrier: models.PeerLinkCarrierUnix, Weight: 3,
		}},
	}
	_, err = service.Replace(ctx, replacement, 1, models.AuditRecord{}, "admin", "req-2", "plugin-link", "key-replace", "digest-b")
	check(err)
	replacedRules := snapshot.View().Rules("caller-a")
	observations["afterReplaceCount"] = len(replacedRules)
	observations["replaceCarrier"] = replacedRules[0].Carrier

	err = service.Delete(ctx, "caller-a", "target-b", 2, models.AuditRecord{}, "admin", "req-3", "plugin-link", "key-delete", "digest-c")
	check(err)
	observations["afterDeleteRules"] = len(snapshot.View().Rules("caller-a"))

	_, err = service.Create(ctx, policy, models.AuditRecord{}, "admin", "req-4", "plugin-link", "key-restore", "digest-d")
	check(err)
	restored, err := storage.NewPluginLinkPolicySnapshot(ctx, linkStore)
	check(err)
	observations["restartRestoredRules"] = len(restored.View().Rules("caller-a"))

	encoded, err := json.Marshal(observations)
	check(err)
	fmt.Println(string(encoded))
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
