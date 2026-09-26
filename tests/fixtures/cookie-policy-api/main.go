package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/api"
)

func main() {
	if len(os.Args) != 2 {
		panic("expected database path")
	}
	sqliteContract, err := config.LoadSQLiteContract()
	if err != nil {
		panic(err)
	}
	database, err := storage.OpenSQLite(context.Background(), os.Args[1], storage.SQLiteOptions{
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
	if _, err := database.Exec(`INSERT INTO plugin_instances(id, mode, settings_json, manifest_json, state, revision) VALUES(?, ?, ?, ?, ?, ?)`, "fixture", "remote", []byte(`{}`), []byte(`{"capabilities":["forms.submit"]}`), "configured", 1); err != nil {
		panic(err)
	}
	store, err := storage.NewSQLitePluginCookiePolicyStore(database)
	if err != nil {
		panic(err)
	}
	activator := &fixtureActivator{}
	management, err := config.LoadManagement()
	if err != nil {
		panic(err)
	}
	audit, err := config.LoadAudit()
	if err != nil {
		panic(err)
	}
	service := &application.PluginCookiePolicyService{
		Store: store, Activator: activator,
		AuditAction:      audit.Audit.Actions.PluginCookiePolicyReplace,
		Resource:         audit.Audit.Resources.PluginCookiePolicies,
		Success:          audit.Audit.Results.Succeeded,
		Invalid:          management.Codes.InvalidCookiePolicy,
		RevisionConflict: "policy revision conflict",
	}
	server := (&api.Server{Token: "fixture-token", Management: management, AuditWords: audit, CookiePolicies: service}).Handler()
	missingPrecondition := perform(server, http.MethodPut, `{"allowedNames":["session"]}`, "")
	invalid := perform(server, http.MethodPut, `{"allowedNames":["*"]}`, `"0"`)
	first := perform(server, http.MethodPut, `{"allowedNames":["session"]}`, `"0"`)
	stale := perform(server, http.MethodPut, `{"allowedNames":["theme"]}`, `"0"`)
	if _, err := database.Exec(`CREATE TRIGGER fixture_reject_cookie_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT, 'fixture audit failure'); END`); err != nil {
		panic(err)
	}
	failedCommit := perform(server, http.MethodPut, `{"allowedNames":["theme"]}`, `"1"`)
	current := perform(server, http.MethodGet, "", "")
	var currentBody struct {
		Revision     int64    `json:"revision"`
		AllowedNames []string `json:"allowedNames"`
	}
	if err := json.Unmarshal(current.Body.Bytes(), &currentBody); err != nil {
		panic(err)
	}
	auditStore, err := storage.NewSQLiteAuditStore(database)
	if err != nil {
		panic(err)
	}
	auditPage, err := auditStore.List(context.Background(), time.Time{}, "", 10)
	if err != nil {
		panic(err)
	}
	actions := make([]string, 0, len(auditPage.Items))
	for _, item := range auditPage.Items {
		actions = append(actions, item.Action)
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"firstPutStatus":            first.Code,
		"firstPutETag":              first.Header().Get("ETag"),
		"stalePutStatus":            stale.Code,
		"failedCommitStatus":        failedCommit.Code,
		"missingPreconditionStatus": missingPrecondition.Code,
		"invalidPolicyStatus":       invalid.Code,
		"getStatus":                 current.Code,
		"getETag":                   current.Header().Get("ETag"),
		"getRevision":               currentBody.Revision,
		"allowedNames":              currentBody.AllowedNames,
		"auditActions":              actions,
		"activationCount":           activator.count,
		"staleDidNotActivate":       activator.count == 1,
	}); err != nil {
		panic(err)
	}
}

type fixtureActivator struct{ count int }

func (activator *fixtureActivator) ActivatePluginCookiePolicy(_ context.Context, _ models.PluginCookiePolicy) (func(context.Context) error, error) {
	activator.count++
	activated := true
	return func(context.Context) error {
		if activated {
			activator.count--
			activated = false
		}
		return nil
	}, nil
}

func perform(handler http.Handler, method, body, etag string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/api/plugins/fixture/cookie-policies/forms.submit", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer fixture-token")
	if etag != "" {
		request.Header.Set("If-Match", etag)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
