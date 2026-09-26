package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/artifacts"
	"github.com/Liapoldus/core/internal/infrastructure/caddy"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/api"
)

type vector struct {
	ID    string `json:"id"`
	Input struct {
		Entry   string `json:"entry"`
		Entries []struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		} `json:"entries"`
		CorruptGzip bool `json:"corruptGzip"`
	} `json:"input"`
	Expected struct {
		Accepted              bool   `json:"accepted"`
		Code                  string `json:"code"`
		ActiveRevisionChanged bool   `json:"activeRevisionChanged"`
	} `json:"expected"`
}

type observation struct {
	Accepted              bool   `json:"accepted"`
	Code                  string `json:"code"`
	ActiveRevisionChanged bool   `json:"activeRevisionChanged"`
}

func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
	var testVector vector
	vectorContents, err := os.ReadFile(os.Args[1])
	if err != nil || json.Unmarshal(vectorContents, &testVector) != nil || testVector.ID == "" || (testVector.Input.Entry == "" && len(testVector.Input.Entries) == 0) {
		os.Exit(2)
	}

	ctx := context.Background()
	sqlite, err := config.LoadSQLiteContract()
	if err != nil {
		panic(err)
	}
	database, err := storage.OpenSQLite(ctx, os.Args[2], storage.SQLiteOptions{
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
	groupStore, err := storage.NewSQLiteGroupStore(database)
	if err != nil {
		panic(err)
	}
	groupID := "application-golden-vector"
	if _, err := groupStore.CreateApplicationGroup(ctx, groupID, models.AuditRecord{Actor: "fixture", Action: "group.create", Resource: "groups", Result: "succeeded", RequestID: "vector-request"}); err != nil {
		panic(err)
	}
	currentRevision := strings.Repeat("a", 64)
	if _, err := groupStore.CreateRevision(ctx, models.GroupRevision{
		ID: currentRevision, GroupID: groupID,
		CaddyfileDigest: strings.Repeat("b", 64), CaddyfilePath: filepath.Join(filepath.Dir(os.Args[2]), "active.caddyfile"),
	}); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(os.Args[2]), "active.caddyfile"), []byte("example.test {\n  respond 200\n}\n"), 0o600); err != nil {
		panic(err)
	}
	if _, err := groupStore.AdvanceCurrent(ctx, groupID, currentRevision, nil); err != nil {
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
	root := filepath.Dir(os.Args[2])
	releaseArtifacts, err := artifacts.NewGroupReleaseArtifacts(root)
	if err != nil {
		panic(err)
	}
	management, err := config.LoadManagement()
	if err != nil {
		panic(err)
	}
	errors, err := config.LoadErrorCatalog()
	if err != nil {
		panic(err)
	}
	audit, err := config.LoadAudit()
	if err != nil {
		panic(err)
	}
	releaseService := &application.GroupReleaseService{
		Store: groupStore, Releases: releases,
		ContentReader: artifacts.GroupRevisionReader{Root: root},
		Artifacts:     releaseArtifacts, Activator: activator{}, Policy: policy,
	}
	server := &api.Server{
		Token: "vector-management-token", Management: management, Errors: errors, AuditWords: audit,
		Operations: application.OperationService{Store: operations}, GroupReleases: releaseService,
		GroupReleasePolicy: policy,
	}

	before, err := groupStore.GetPointers(ctx, groupID)
	if err != nil {
		panic(err)
	}
	response := submit(server.Handler(), groupID, testVector.Input.Entry, testVector.Input.Entries, testVector.Input.CorruptGzip)
	var problem struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &problem)
	after, err := groupStore.GetPointers(ctx, groupID)
	if err != nil {
		panic(err)
	}
	observation := observation{
		Accepted:              response.Code == http.StatusAccepted,
		Code:                  problem.Code,
		ActiveRevisionChanged: before.CurrentRevisionID == nil || after.CurrentRevisionID == nil || *before.CurrentRevisionID != *after.CurrentRevisionID,
	}
	if err := json.NewEncoder(os.Stdout).Encode(observation); err != nil {
		panic(err)
	}
}

func submit(handler http.Handler, groupID, entry string, entries []struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}, corruptGzip bool) *httptest.ResponseRecorder {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	metadata, _ := json.Marshal(map[string]any{"idempotencyKey": "archive-vector-key", "expectedCurrentRevision": strings.Repeat("a", 64)})
	metadataPart, err := writer.CreatePart(map[string][]string{
		"Content-Disposition": {"form-data; name=\"metadata\""},
		"Content-Type":        {"application/json"},
	})
	if err != nil {
		panic(err)
	}
	if _, err := metadataPart.Write(metadata); err != nil {
		panic(err)
	}
	caddyfilePart, err := writer.CreatePart(map[string][]string{
		"Content-Disposition": {"form-data; name=\"caddyfile\"; filename=\"Caddyfile\""},
		"Content-Type":        {"text/plain; charset=utf-8"},
	})
	if err != nil {
		panic(err)
	}
	if _, err := caddyfilePart.Write([]byte("example.test {\n  respond 201\n}\n")); err != nil {
		panic(err)
	}
	artifactPart, err := writer.CreatePart(map[string][]string{
		"Content-Disposition": {"form-data; name=\"artifact\"; filename=\"frontend.tar.gz\""},
		"Content-Type":        {"application/gzip"},
	})
	if err != nil {
		panic(err)
	}
	if _, err := artifactPart.Write(makeArchive(entry, entries, corruptGzip)); err != nil {
		panic(err)
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/groups/"+groupID+"/releases", &body)
	request.Header.Set("Authorization", "Bearer vector-management-token")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func makeArchive(entry string, entries []struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}, corruptGzip bool) []byte {
	var compressed bytes.Buffer
	compressor := gzip.NewWriter(&compressed)
	writer := tar.NewWriter(compressor)
	if entry != "" {
		entries = append(entries, struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		}{Name: entry, Content: "vector payload"})
	}
	for _, archiveEntry := range entries {
		contents := []byte(archiveEntry.Content)
		if err := writer.WriteHeader(&tar.Header{Name: archiveEntry.Name, Mode: 0o600, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
			panic(err)
		}
		if _, err := writer.Write(contents); err != nil {
			panic(err)
		}
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}
	if err := compressor.Close(); err != nil {
		panic(err)
	}
	if corruptGzip && compressed.Len() > 0 {
		archive := compressed.Bytes()
		archive[len(archive)-8] ^= 0xff
	}
	return compressed.Bytes()
}

type activator struct{}

func (activator) Validate(_ context.Context, source []byte) error {
	_, _, err := caddy.AdaptCaddyfile(source)
	return err
}

func (activator) Activate(context.Context, []byte) error { return nil }
