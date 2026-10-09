package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

type report struct {
	CreatedRevision       int64    `json:"createdRevision"`
	LoadedPlacements      []string `json:"loadedPlacements"`
	LoadedCarriers        []string `json:"loadedCarriers"`
	LoadedWeight          uint16   `json:"loadedWeight"`
	LoadedContractIDs     []string `json:"loadedContractIDs"`
	DuplicateConflict     bool     `json:"duplicateConflict"`
	ReplacedRevision      int64    `json:"replacedRevision"`
	StaleReplaceConflict  bool     `json:"staleReplaceConflict"`
	MissingReplaceMissing bool     `json:"missingReplaceNotFound"`
	StaleDeleteConflict   bool     `json:"staleDeleteConflict"`
	DeleteSucceeded       bool     `json:"deleteSucceeded"`
	DeleteMissingNotFound bool     `json:"deleteMissingNotFound"`
	RemainingPolicies     int      `json:"remainingPolicies"`
	AuditEvents           int      `json:"auditEvents"`
}

func main() {
	ctx := context.Background()
	sqliteContract, err := config.LoadSQLiteContract()
	if err != nil {
		panic(err)
	}
	database, err := storage.OpenSQLite(ctx, os.Args[1], storage.SQLiteOptions{
		Driver: sqliteContract.Driver, ParentDirectoryMode: sqliteContract.ParentDirectoryMode,
		DatabaseFileMode: sqliteContract.DatabaseFileMode, MaxOpenConnections: sqliteContract.MaxOpenConnections,
		MaxIdleConnections: sqliteContract.MaxIdleConnections, SchemaVersion: sqliteContract.SchemaVersion,
		HasMigrationTableQuery: sqliteContract.HasMigrationTableQuery, MigrationVersionQuery: sqliteContract.MigrationVersionQuery,
		SchemaVersionError: sqliteContract.SchemaVersionError, Pragmas: sqliteContract.Pragmas,
	}, sqliteContract.Schema)
	if err != nil {
		panic(err)
	}
	defer database.Close()
	store, err := storage.NewSQLitePluginLinkPolicyStore(database)
	if err != nil {
		panic(err)
	}
	audit := func(action string) models.AuditRecord {
		return models.AuditRecord{Actor: "fixture", Action: action, Resource: "fixture", Result: "succeeded", RequestID: "fixture-request"}
	}
	result := report{}
	policy := models.PluginLinkPolicy{
		CallerInstanceID: "caller-a",
		TargetInstanceID: "target-b",
		Rules: []models.PeerLinkRule{
			{CallerInstanceID: "caller-a", TargetInstanceID: "target-b", PlacementRule: models.PeerLinkPlacementSame, Carrier: models.PeerLinkCarrierUnix, Weight: 5},
			{CallerInstanceID: "caller-a", TargetInstanceID: "target-b", PlacementRule: models.PeerLinkPlacementRemote, Carrier: models.PeerLinkCarrierTCP, Weight: 40,
				RequiredContracts: []models.PeerContractRange{{ContractID: "example.contract", MinimumVersion: "1.0.0", MaximumVersionExclusive: "2.0.0"}}},
		},
	}
	created, err := store.Create(ctx, policy, audit("plugin-link.create"))
	if err != nil {
		panic(err)
	}
	result.CreatedRevision = created.Revision

	if _, err := store.Create(ctx, policy, audit("plugin-link.create")); errors.Is(err, models.PeerLinkPolicyConflict{}) {
		result.DuplicateConflict = true
	} else if err != nil {
		panic(err)
	}

	loaded, err := store.Get(ctx, "caller-a", "target-b")
	if err != nil {
		panic(err)
	}
	result.LoadedWeight = loaded.Rules[0].Weight
	for _, rule := range loaded.Rules {
		result.LoadedPlacements = append(result.LoadedPlacements, rule.PlacementRule)
		result.LoadedCarriers = append(result.LoadedCarriers, rule.Carrier)
		for _, requirement := range rule.RequiredContracts {
			result.LoadedContractIDs = append(result.LoadedContractIDs, requirement.ContractID)
		}
	}

	replaced, err := store.Replace(ctx, "caller-a", "target-b", 1, models.PluginLinkPolicy{
		CallerInstanceID: "caller-a",
		TargetInstanceID: "target-b",
		Rules: []models.PeerLinkRule{
			{CallerInstanceID: "caller-a", TargetInstanceID: "target-b", PlacementRule: models.PeerLinkPlacementRemote, Carrier: models.PeerLinkCarrierQUIC, Weight: 7},
		},
	}, audit("plugin-link.replace"))
	if err != nil {
		panic(err)
	}
	result.ReplacedRevision = replaced.Revision

	stale := models.PluginLinkPolicy{
		CallerInstanceID: "caller-a",
		TargetInstanceID: "target-b",
		Rules:            []models.PeerLinkRule{{CallerInstanceID: "caller-a", TargetInstanceID: "target-b", PlacementRule: models.PeerLinkPlacementRemote, Carrier: models.PeerLinkCarrierTCP, Weight: 3}},
	}
	if _, err := store.Replace(ctx, "caller-a", "target-b", 1, stale, audit("plugin-link.replace")); errors.Is(err, models.PeerLinkPolicyConflict{}) {
		result.StaleReplaceConflict = true
	} else if err != nil {
		panic(err)
	}

	if _, err := store.Replace(ctx, "caller-x", "target-y", 1, models.PluginLinkPolicy{
		CallerInstanceID: "caller-x",
		TargetInstanceID: "target-y",
		Rules:            []models.PeerLinkRule{{CallerInstanceID: "caller-x", TargetInstanceID: "target-y", PlacementRule: models.PeerLinkPlacementRemote, Carrier: models.PeerLinkCarrierTCP, Weight: 1}},
	}, audit("plugin-link.replace")); errors.Is(err, models.PeerLinkPolicyNotFound{}) {
		result.MissingReplaceMissing = true
	} else if err != nil {
		panic(err)
	}

	if _, err := store.Create(ctx, models.PluginLinkPolicy{
		CallerInstanceID: "caller-c",
		TargetInstanceID: "target-d",
		Rules:            []models.PeerLinkRule{{CallerInstanceID: "caller-c", TargetInstanceID: "target-d", PlacementRule: models.PeerLinkPlacementSame, Carrier: models.PeerLinkCarrierWindowsPipe, Weight: 1}},
	}, audit("plugin-link.create")); err != nil {
		panic(err)
	}

	if err := store.Delete(ctx, "caller-a", "target-b", 1, audit("plugin-link.delete")); errors.Is(err, models.PeerLinkPolicyConflict{}) {
		result.StaleDeleteConflict = true
	} else if err != nil {
		panic(err)
	}
	if err := store.Delete(ctx, "caller-a", "target-b", 2, audit("plugin-link.delete")); err != nil {
		panic(err)
	} else {
		result.DeleteSucceeded = true
	}
	if err := store.Delete(ctx, "caller-a", "target-b", 2, audit("plugin-link.delete")); errors.Is(err, models.PeerLinkPolicyNotFound{}) {
		result.DeleteMissingNotFound = true
	} else if err != nil {
		panic(err)
	}

	remaining, err := store.List(ctx)
	if err != nil {
		panic(err)
	}
	result.RemainingPolicies = len(remaining)

	if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_events").Scan(&result.AuditEvents); err != nil {
		panic(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
}
