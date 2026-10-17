package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
)

type coreSettingsFixture struct {
	snapshot models.CoreSettingsSnapshot
	updated  []byte
	actor    string
	err      error
}

func (fixture *coreSettingsFixture) Read(context.Context) (models.CoreSettingsSnapshot, error) {
	return fixture.snapshot, fixture.err
}

func (fixture *coreSettingsFixture) Update(_ context.Context, expected int64, document []byte, actor string) (models.CoreSettingsSnapshot, error) {
	if fixture.err != nil {
		return models.CoreSettingsSnapshot{}, fixture.err
	}
	if expected != fixture.snapshot.DesiredRevision {
		return models.CoreSettingsSnapshot{}, errors.New("revision conflict")
	}
	fixture.updated = append([]byte(nil), document...)
	fixture.actor = actor
	fixture.snapshot.DesiredRevision++
	fixture.snapshot.DesiredDocument = append([]byte(nil), document...)
	fixture.snapshot.Pending = true
	return fixture.snapshot, nil
}

func TestCoreSettingsAuthenticatedReadSeparatesDesiredAndEffective(t *testing.T) {
	settings := &coreSettingsFixture{snapshot: models.CoreSettingsSnapshot{
		DesiredRevision: 8, EffectiveRevision: 6, DesiredDocument: []byte(`{"management":{"listen":"127.0.0.1:8080"}}`),
		EffectiveDocument: []byte(`{"management":{"listen":"127.0.0.1:7000"}}`), Pending: true,
	}}
	server := coreSettingsTestServer(settings, nil)
	request := httptest.NewRequest(http.MethodGet, settingsPath, nil)
	request.Header.Set("Authorization", "Bearer fixture-token")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body=%s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("ETag"); got != `"core-settings-8"` {
		t.Fatalf("ETag = %q", got)
	}
	var payload struct {
		DesiredRevision   int64           `json:"desiredRevision"`
		EffectiveRevision int64           `json:"effectiveRevision"`
		Pending           bool            `json:"pending"`
		RestartRequired   bool            `json:"restartRequired"`
		Settings          json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.DesiredRevision != 8 || payload.EffectiveRevision != 6 || !payload.Pending || !payload.RestartRequired {
		t.Fatalf("desired/effective state was collapsed: %+v", payload)
	}
	if string(payload.Settings) != string(settings.snapshot.DesiredDocument) {
		t.Fatalf("GET did not return desired settings: %s", payload.Settings)
	}
}

func TestCoreSettingsUpdateRequiresExactRevisionAndValidDocument(t *testing.T) {
	tests := []struct {
		name        string
		etag        string
		contentType string
		body        string
		validate    func([]byte) error
		wantStatus  int
		wantUpdate  bool
	}{
		{name: "accepted", etag: `"core-settings-3"`, contentType: "application/json", body: `{"management":{"listen":"127.0.0.1:8081"}}`, validate: func([]byte) error { return nil }, wantStatus: http.StatusOK, wantUpdate: true},
		{name: "stale revision", etag: `"core-settings-2"`, contentType: "application/json", body: `{}`, validate: func([]byte) error { return nil }, wantStatus: http.StatusPreconditionFailed},
		{name: "missing revision", contentType: "application/json", body: `{}`, validate: func([]byte) error { return nil }, wantStatus: http.StatusPreconditionFailed},
		{name: "wrong content type", etag: `"core-settings-3"`, contentType: "text/plain", body: `{}`, validate: func([]byte) error { return nil }, wantStatus: http.StatusUnsupportedMediaType},
		{name: "invalid settings", etag: `"core-settings-3"`, contentType: "application/json", body: `{"bad":true}`, validate: func([]byte) error { return errors.New("unsafe validator detail") }, wantStatus: http.StatusUnprocessableEntity},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := &coreSettingsFixture{snapshot: models.CoreSettingsSnapshot{
				DesiredRevision: 3, EffectiveRevision: 3, DesiredDocument: []byte(`{"management":{"listen":"127.0.0.1:8080"}}`),
			}}
			server := coreSettingsTestServer(settings, test.validate)
			request := httptest.NewRequest(http.MethodPut, settingsPath, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer fixture-token")
			request.Header.Set("Content-Type", test.contentType)
			if test.etag != "" {
				request.Header.Set("If-Match", test.etag)
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("PUT status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if (settings.updated != nil) != test.wantUpdate {
				t.Fatalf("update accepted = %v, want %v", settings.updated != nil, test.wantUpdate)
			}
			if test.wantUpdate {
				if string(settings.updated) != test.body {
					t.Fatalf("stored bytes = %q, want exact request bytes %q", settings.updated, test.body)
				}
				if settings.actor == "" {
					t.Fatal("authenticated actor was not passed to the settings store")
				}
				if got := response.Header().Get("ETag"); got != `"core-settings-4"` {
					t.Fatalf("updated ETag = %q", got)
				}
			}
			if test.name == "invalid settings" && strings.Contains(response.Body.String(), "unsafe validator detail") {
				t.Fatal("validator detail leaked into the response")
			}
		})
	}
}

func TestCoreSettingsRejectsUnauthenticatedAndUnsupportedMethods(t *testing.T) {
	server := coreSettingsTestServer(&coreSettingsFixture{snapshot: models.CoreSettingsSnapshot{
		DesiredRevision: 1, DesiredDocument: []byte(`{}`),
	}}, func([]byte) error { return nil })

	unauthorized := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, settingsPath, nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET status = %d", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodDelete, settingsPath, nil)
	request.Header.Set("Authorization", "Bearer fixture-token")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, PUT" {
		t.Fatalf("DELETE status/Allow = %d/%q", response.Code, response.Header().Get("Allow"))
	}
}

func coreSettingsTestServer(settings *coreSettingsFixture, validate func([]byte) error) *Server {
	var audit config.AuditWords
	audit.Audit.Actors.StaticToken = "fixture-operator"
	return &Server{CoreSettings: settings, ValidateSettings: validate, Token: "fixture-token", AuditWords: audit}
}
