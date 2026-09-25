package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

type report struct {
	ExpiredRemaining int      `json:"expiredRemaining"`
	RetainedActors   []string `json:"retainedActors"`
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
	store, err := storage.NewSQLiteAuditStore(database)
	if err != nil {
		panic(err)
	}
	auditWords, err := config.LoadAudit()
	if err != nil {
		panic(err)
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -auditWords.Audit.RetentionDays)
	for _, record := range []models.AuditRecord{
		{Timestamp: cutoff.Add(-time.Second), Actor: "expired-actor", Action: "fixture", Resource: "fixture", Result: "succeeded", RequestID: "expired-request"},
		{Timestamp: cutoff.Add(time.Second), Actor: "recent-actor", Action: "fixture", Resource: "fixture", Result: "succeeded", RequestID: "recent-request"},
	} {
		if err := store.Append(ctx, record); err != nil {
			panic(err)
		}
	}
	page, err := store.List(ctx, cutoff, "", 10)
	if err != nil {
		panic(err)
	}
	result := report{RetainedActors: make([]string, 0, len(page.Items))}
	for _, record := range page.Items {
		result.RetainedActors = append(result.RetainedActors, record.Actor)
	}
	if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_events WHERE actor = ?", "expired-actor").Scan(&result.ExpiredRemaining); err != nil {
		panic(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
}
