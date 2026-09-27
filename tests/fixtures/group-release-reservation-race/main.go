package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/artifacts"
	"github.com/Liapoldus/core/internal/infrastructure/caddy"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/api"
)

type event struct {
	Kind            string `json:"kind"`
	Address         string `json:"address,omitempty"`
	ValidationCount int64  `json:"validationCount,omitempty"`
	ActivationCount int64  `json:"activationCount,omitempty"`
}

type fixtureActivator struct {
	validationTarget  int64
	validationCount   atomic.Int64
	validationBarrier chan struct{}
	validationOnce    sync.Once
	activationGate    chan struct{}
	activationCount   atomic.Int64
	outputMu          sync.Mutex
}

func newFixtureActivator(validationTarget int64) *fixtureActivator {
	var barrier chan struct{}
	if validationTarget > 0 {
		barrier = make(chan struct{})
	}
	return &fixtureActivator{
		validationTarget: validationTarget, validationBarrier: barrier,
		activationGate: make(chan struct{}),
	}
}

func (activator *fixtureActivator) Validate(_ context.Context, source []byte) error {
	_, _, err := caddy.AdaptCaddyfile(source)
	if err != nil || activator.validationBarrier == nil {
		return err
	}
	count := activator.validationCount.Add(1)
	activator.emit(event{Kind: "validation_entered", ValidationCount: count})
	if count == activator.validationTarget {
		activator.validationOnce.Do(func() { close(activator.validationBarrier) })
	}
	<-activator.validationBarrier
	return nil
}

func (activator *fixtureActivator) Activate(context.Context, []byte) error {
	count := activator.activationCount.Add(1)
	activator.emit(event{Kind: "activation_started", ActivationCount: count})
	<-activator.activationGate
	activator.emit(event{Kind: "activation_finished", ActivationCount: count})
	return nil
}

func (activator *fixtureActivator) emit(value event) {
	activator.outputMu.Lock()
	defer activator.outputMu.Unlock()
	_ = json.NewEncoder(os.Stdout).Encode(value)
}

func main() {
	if len(os.Args) != 3 {
		panic("expected a database path and fixture mode")
	}
	ctx := context.Background()
	sqlite, err := config.LoadSQLiteContract()
	if err != nil {
		panic(err)
	}
	database, err := storage.OpenSQLite(ctx, os.Args[1], storage.SQLiteOptions{
		Driver: sqlite.Driver, ParentDirectoryMode: sqlite.ParentDirectoryMode,
		DatabaseFileMode: sqlite.DatabaseFileMode, MaxOpenConnections: sqlite.MaxOpenConnections,
		MaxIdleConnections: sqlite.MaxIdleConnections, SchemaVersion: sqlite.SchemaVersion,
		HasMigrationTableQuery: sqlite.HasMigrationTableQuery, MigrationVersionQuery: sqlite.MigrationVersionQuery,
		SchemaVersionError: sqlite.SchemaVersionError, Pragmas: sqlite.Pragmas,
	}, sqlite.Schema)
	if err != nil {
		panic(err)
	}
	defer database.Close()

	management, err := config.LoadManagement()
	if err != nil {
		panic(err)
	}
	errorCatalog, err := config.LoadErrorCatalog()
	if err != nil {
		panic(err)
	}
	auditWords, err := config.LoadAudit()
	if err != nil {
		panic(err)
	}
	groupStore, err := storage.NewSQLiteGroupStore(database)
	if err != nil {
		panic(err)
	}
	groupID := "release-race"
	if _, err := groupStore.CreateApplicationGroup(ctx, groupID, models.AuditRecord{
		Timestamp: time.Now().UTC(), Actor: auditWords.Audit.Actors.StaticToken,
		Action: auditWords.Audit.Actions.GroupCreate, Resource: auditWords.Audit.Resources.Groups,
		Result: auditWords.Audit.Results.Succeeded, RequestID: "fixture-group-create",
	}); err != nil {
		panic(err)
	}

	operations, err := storage.NewSQLiteOperationStore(database)
	if err != nil {
		panic(err)
	}
	releases, err := storage.NewSQLiteGroupReleaseStore(database)
	if err != nil {
		panic(err)
	}
	policy, err := config.LoadGroupRelease()
	if err != nil {
		panic(err)
	}
	root := filepath.Dir(os.Args[1])
	releaseArtifacts, err := artifacts.NewGroupReleaseArtifacts(root)
	if err != nil {
		panic(err)
	}
	activator := newFixtureActivator(0)
	if os.Args[2] == "duplicate" {
		activator = newFixtureActivator(2)
	}
	reader := artifacts.GroupRevisionReader{Root: root}
	releaseService := &application.GroupReleaseService{
		Store: groupStore, Releases: releases, ContentReader: reader,
		Artifacts: releaseArtifacts, Activator: activator, Policy: policy,
	}
	server := &api.Server{
		Token: "fixture-management-token", Management: management, Errors: errorCatalog,
		AuditWords: auditWords, Operations: application.OperationService{Store: operations},
		GroupService:  application.GroupService{Store: groupStore, ContentReader: reader},
		GroupReleases: releaseService, GroupReleasePolicy: policy,
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	go func() {
		if err := http.Serve(listener, server.Handler()); err != nil && err != http.ErrServerClosed {
			panic(err)
		}
	}()
	activator.emit(event{Kind: "ready", Address: "http://" + listener.Addr().String()})

	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			switch scanner.Text() {
			case "release":
				select {
				case <-activator.activationGate:
				default:
					close(activator.activationGate)
				}
			case "state":
				activator.emit(event{Kind: "state", ActivationCount: activator.activationCount.Load()})
			}
		}
	}()

	select {}
}
