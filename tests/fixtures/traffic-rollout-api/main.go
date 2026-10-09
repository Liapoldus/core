package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/api"
)

type replicaSource struct {
	replicas []models.TrafficRolloutReplica
}

func (source replicaSource) TrafficRolloutReplicas(context.Context, string) ([]models.TrafficRolloutReplica, error) {
	return append([]models.TrafficRolloutReplica(nil), source.replicas...), nil
}

type configValidator struct{}

func (configValidator) ValidateTrafficRolloutConfiguration(_ context.Context, _ string, document []byte) error {
	var value map[string]any
	if json.Unmarshal(document, &value) != nil || value == nil {
		return models.TrafficRolloutInvalid{}
	}
	return nil
}

func main() {
	ctx := context.Background()
	contract, err := config.LoadSQLiteContract()
	check(err)
	database, err := storage.OpenSQLite(ctx, os.Args[1], storage.SQLiteOptions{
		Driver: contract.Driver, ParentDirectoryMode: contract.ParentDirectoryMode, DatabaseFileMode: contract.DatabaseFileMode,
		MaxOpenConnections: contract.MaxOpenConnections, MaxIdleConnections: contract.MaxIdleConnections,
		SchemaVersion: contract.SchemaVersion, HasMigrationTableQuery: contract.HasMigrationTableQuery,
		MigrationVersionQuery: contract.MigrationVersionQuery, SchemaVersionError: contract.SchemaVersionError,
		IntegrityCheckQuery: contract.IntegrityCheckQuery, ForeignKeyCheckQuery: contract.ForeignKeyCheckQuery,
		IntegritySuccess: contract.IntegritySuccess, IntegrityError: contract.IntegrityError, Pragmas: contract.Pragmas,
	}, contract.Schema)
	check(err)
	defer database.Close()
	_, err = database.ExecContext(ctx, `INSERT INTO plugin_instances(id, manifest_json, state) VALUES (?, ?, ?)`, "forms", []byte(`{}`), "ready")
	check(err)
	store, err := storage.NewSQLiteTrafficRolloutStore(database)
	check(err)
	operationStore, err := storage.NewSQLiteOperationStore(database)
	check(err)
	words, err := config.LoadManagement()
	check(err)
	audit, err := config.LoadAudit()
	check(err)
	apiContract, err := config.LoadTrafficRolloutAPIContract()
	check(err)
	pluginConfigWords, err := config.LoadPluginConfiguration()
	check(err)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	service := &application.TrafficRolloutService{
		Store: store, Operations: application.OperationService{Store: operationStore},
		Replicas: replicaSource{replicas: []models.TrafficRolloutReplica{{
			ReplicaID: "candidate-1", Incarnation: "inc-1",
			ReleaseSHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			LeaseExpiresAt: now.Add(time.Hour), Ready: true,
		}}},
		Validator: configValidator{}, SchemaVersion: 1, MaximumConfigurationBytes: 1024,
		States: application.TrafficRolloutStates{Running: words.Statuses.Running, Pending: words.Statuses.Pending, Active: pluginConfigWords.Slots.Active},
		Now:    func() time.Time { return now },
	}
	server := &api.Server{
		Token: "fixture-only-admin-token", Management: words, AuditWords: audit,
		Operations:      application.OperationService{Store: operationStore},
		TrafficRollouts: service, TrafficRolloutAPI: apiContract,
	}
	handler := server.Handler()
	path := words.Paths.Plugins + "/forms" + apiContract.CollectionSuffix
	metadata := []byte(`{"releaseSha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","targets":[{"replicaId":"candidate-1","incarnation":"inc-1"}],"stages":[{"id":"full","candidateWeightPercent":100,"minimumObservationSeconds":0}]}`)
	configuration := []byte(` { "form" : {"fields":["email"]} } `)
	accepted := sendCreate(handler, apiContract, words, path, metadata, configuration, http.MethodPost, `"0"`, "same-key")
	replay := sendCreate(handler, apiContract, words, path, metadata, configuration, http.MethodPost, `"0"`, "same-key")
	wrongMethod := sendCreate(handler, apiContract, words, path, metadata, configuration, http.MethodPut, `"0"`, "unused-key")
	largeMetadata := bytes.Repeat([]byte(" "), int(apiContract.MaximumMetadataBytes)+1)
	tooLarge := sendCreate(handler, apiContract, words, path, largeMetadata, configuration, http.MethodPost, `"0"`, "large-key")
	rolloutID := rolloutIDForOperation(ctx, database, responseOperationID(accepted))
	checkRolloutAudit := models.AuditRecord{Actor: "controller", Action: "traffic_rollout.confirm", Resource: "forms", Result: audit.Audit.Results.Succeeded, RequestID: "confirm-request"}
	_, err = store.ConfirmStage(ctx, rolloutID, 1, "full", 100, "controller-r1", now, checkRolloutAudit)
	check(err)
	detailPath := path + "/" + rolloutID
	detail := sendGet(handler, words, detailPath)
	approvalPath := path + "/" + rolloutID + "/stages/full/approve"
	approved := sendApproval(handler, words, approvalPath, detail.Header().Get(words.Headers.ETag), "approve-key")
	approvalReplay := sendApproval(handler, words, approvalPath, detail.Header().Get(words.Headers.ETag), "approve-key")
	var rolloutState string
	check(database.QueryRowContext(ctx, `SELECT state FROM traffic_rollouts WHERE id = ?`, rolloutID).Scan(&rolloutState))
	var count int
	check(database.QueryRowContext(ctx, `SELECT COUNT(*) FROM traffic_rollouts`).Scan(&count))
	var stored []byte
	check(database.QueryRowContext(ctx, `SELECT raw_json FROM plugin_config_generations WHERE instance_id = ? AND slot = 'active'`, "forms").Scan(&stored))
	check(json.NewEncoder(os.Stdout).Encode(map[string]bool{
		"accepted":                   accepted.Code == http.StatusAccepted,
		"operationCreated":           responseHasOperation(accepted) && count == 1,
		"idempotentReplay":           replay.Code == http.StatusAccepted && responseOperationID(replay) == responseOperationID(accepted),
		"rawConfigurationPreserved":  bytes.Equal(stored, configuration),
		"wrongMethodRejected":        wrongMethod.Code != http.StatusAccepted,
		"oversizedMetadataRejected":  tooLarge.Code != http.StatusAccepted,
		"manualApprovalAccepted":     approved.Code == http.StatusOK,
		"manualApprovalIdempotent":   approvalReplay.Code == http.StatusOK && responseOperationID(approvalReplay) == responseOperationID(approved),
		"manualApprovalAdvanced":     rolloutState == "completed",
		"approvalOperationCompleted": responseOperationState(approved) == words.Statuses.Completed,
		"adminCanReadRevision":       detail.Code == http.StatusOK && detail.Header().Get(words.Headers.ETag) == `"2"`,
	}))
}

func sendGet(handler http.Handler, words config.ManagementWords, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer fixture-only-admin-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func sendApproval(handler http.Handler, words config.ManagementWords, path, ifMatch, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, nil)
	request.Header.Set(words.Headers.IfMatch, ifMatch)
	request.Header.Set(words.Idempotency.Key, key)
	request.Header.Set("Authorization", "Bearer fixture-only-admin-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func rolloutIDForOperation(ctx context.Context, database *sql.DB, operationID string) string {
	var id string
	check(database.QueryRowContext(ctx, `SELECT id FROM traffic_rollouts WHERE operation_id = ?`, operationID).Scan(&id))
	return id
}

func sendCreate(handler http.Handler, apiContract config.TrafficRolloutAPIContract, words config.ManagementWords, path string, metadata, configuration []byte, method, ifMatch, key string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writePart(writer, apiContract.MetadataPart, apiContract.MetadataMediaType, metadata)
	writePart(writer, apiContract.ConfigurationPart, apiContract.ConfigurationMediaType, configuration)
	check(writer.Close())
	request := httptest.NewRequest(method, path, &body)
	request.Header.Set(words.Headers.ContentType, writer.FormDataContentType())
	request.Header.Set(words.Headers.IfMatch, ifMatch)
	request.Header.Set(words.Idempotency.Key, key)
	request.Header.Set("Authorization", "Bearer fixture-only-admin-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func writePart(writer *multipart.Writer, name, mediaType string, content []byte) {
	header := make(map[string][]string)
	header["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="%s"`, name)}
	header["Content-Type"] = []string{mediaType}
	part, err := writer.CreatePart(header)
	check(err)
	_, err = part.Write(content)
	check(err)
}

func responseHasOperation(response *httptest.ResponseRecorder) bool {
	return responseOperationID(response) != ""
}

func responseOperationID(response *httptest.ResponseRecorder) string {
	var value map[string]any
	if json.Unmarshal(response.Body.Bytes(), &value) != nil {
		return ""
	}
	result, _ := value["operationId"].(string)
	return result
}

func responseOperationState(response *httptest.ResponseRecorder) string {
	var value map[string]any
	if json.Unmarshal(response.Body.Bytes(), &value) != nil {
		return ""
	}
	result, _ := value["state"].(string)
	return result
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
