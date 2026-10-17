package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

func TestServiceRegistrationAcceptsOnlySafeMetadata(t *testing.T) {
	input := models.ServiceRegistration{
		ArtifactID: "server", ArtifactVersion: "1.0.0", Digest: "sha256:" + "a1" + strings.Repeat("0", 62),
		InstanceID: "server", ReplicaID: "replica-1", ControlEndpoint: "https://server:8443", RuntimeStatus: "started",
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/services/server/replicas/register", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	var received models.ServiceRegistration
	ServiceRegistration(response, request, func(_ context.Context, value models.ServiceRegistration) (models.ServiceRegistrationResult, error) {
		received = value
		return models.ServiceRegistrationResult{Accepted: true, InstanceID: value.InstanceID, ReplicaID: value.ReplicaID, Status: "pending"}, nil
	}, "server", jsonWriterForTest, problemWriterForTest, "req-1")
	if response.Code != http.StatusAccepted || received != input {
		t.Fatalf("status=%d received=%+v", response.Code, received)
	}
}

func TestServiceRegistrationRejectsPathAndSecretLikeMetadata(t *testing.T) {
	input := `{"artifactId":"server","artifactVersion":"1.0.0","digest":"sha256:` + "a1" + strings.Repeat("0", 62) + `","instanceId":"server","replicaId":"replica-1","controlEndpoint":"file:///private/key","runtimeStatus":"started"}`
	request := httptest.NewRequest(http.MethodPost, "/api/services/server/replicas/register", bytes.NewBufferString(input))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	ServiceRegistration(response, request, func(context.Context, models.ServiceRegistration) (models.ServiceRegistrationResult, error) {
		t.Fatal("writer must not be called")
		return models.ServiceRegistrationResult{}, nil
	}, "server", jsonWriterForTest, problemWriterForTest, "req-1")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestServiceRegistrationReportsArtifactIdentityConflict(t *testing.T) {
	input := models.ServiceRegistration{
		ArtifactID: "server", ArtifactVersion: "1.0.0", Digest: "sha256:" + "a1" + strings.Repeat("0", 62),
		InstanceID: "server", ReplicaID: "replica-1", ControlEndpoint: "https://server:8443", RuntimeStatus: "started",
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/services/server/replicas/register", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	ServiceRegistration(response, request, func(context.Context, models.ServiceRegistration) (models.ServiceRegistrationResult, error) {
		return models.ServiceRegistrationResult{}, models.ErrServiceRegistrationMismatch
	}, "server", jsonWriterForTest, problemWriterForTest, "req-1")
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "running Service SDK identity does not match the CLI artifact metadata") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestServiceManifestRelaysSDKConfigurationUIWithoutInterpretation(t *testing.T) {
	manifest := []byte(`{"id":"server","configurationUI":{"version":"service-configuration-ui/v1","groups":[{"id":"listener","title":{"ru":"Слушатель","en":"Listener"},"fields":[{"path":"/port","label":{"ru":"Порт","en":"Port"},"widget":"slider"}]}]},"editorButtons":{"version":"service-editor-buttons/v1","buttons":[{"id":"server.endpoints","key":"endpoints","label":{"ru":"Эндпоинты","en":"Endpoints"},"description":{"ru":"Открыть редактор","en":"Open editor"}}]}}`)
	request := httptest.NewRequest(http.MethodGet, "/api/services/server/manifest", nil)
	response := httptest.NewRecorder()
	ServiceMetadata(response, request, func(context.Context, string) (models.ServiceMetadata, error) {
		return models.ServiceMetadata{Manifest: manifest}, nil
	}, "server", "manifest", jsonWriterForTest, problemWriterForTest, "req-1")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var received struct {
		ConfigurationUI json.RawMessage `json:"configurationUI"`
		EditorButtons json.RawMessage `json:"editorButtons"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &received); err != nil {
		t.Fatal(err)
	}
	if string(received.ConfigurationUI) != `{"version":"service-configuration-ui/v1","groups":[{"id":"listener","title":{"ru":"Слушатель","en":"Listener"},"fields":[{"path":"/port","label":{"ru":"Порт","en":"Port"},"widget":"slider"}]}]}` {
		t.Fatalf("Core did not relay the SDK descriptor: %s", received.ConfigurationUI)
	}
	if string(received.EditorButtons) != `{"version":"service-editor-buttons/v1","buttons":[{"id":"server.endpoints","key":"endpoints","label":{"ru":"Эндпоинты","en":"Endpoints"},"description":{"ru":"Открыть редактор","en":"Open editor"}}]}` { t.Fatalf("Core altered or dropped the SDK editor button descriptor: %s", received.EditorButtons) }
}

func TestServiceStatusBindsLiveMetadataToRegisteredArtifact(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/services/server/status", nil)
	response := httptest.NewRecorder()
	ServiceMetadata(response, request, func(context.Context, string) (models.ServiceMetadata, error) {
		return models.ServiceMetadata{
			Identity:  map[string]any{"instanceId": "server", "replicaId": "replica-1"},
			Readiness: map[string]any{"ready": true}, ReplicaID: "replica-1",
			ArtifactVersion: "1.2.3", ArtifactDigest: "sha256:" + strings.Repeat("a", 64),
			Replicas: []models.ServiceReplicaRelease{{
				ReplicaID: "replica-1", ArtifactVersion: "1.2.3", ArtifactDigest: "sha256:" + strings.Repeat("a", 64),
				Identity:      map[string]any{"instanceId": "server", "replicaId": "replica-1"},
				Readiness:     map[string]any{"instanceId": "server", "replicaId": "replica-1", "ready": true},
				Configuration: models.ServiceReplicaConfigurationStatus{State: models.ServiceConfigurationUnconfigured},
			}},
		}, nil
	}, "server", "status", jsonWriterForTest, problemWriterForTest, "req-1")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["artifactId"] != "server" || got["artifactVersion"] != "1.2.3" ||
		got["artifactDigest"] != "sha256:"+strings.Repeat("a", 64) || got["replicaId"] != "replica-1" {
		t.Fatalf("status omitted artifact provenance: %v", got)
	}
	if replicas, ok := got["replicas"].([]any); !ok || len(replicas) != 1 {
		t.Fatalf("status omitted active replica release list: %v", got["replicas"])
	} else if replica, ok := replicas[0].(map[string]any); !ok || replica["configuration"].(map[string]any)["state"] != models.ServiceConfigurationUnconfigured {
		t.Fatalf("status omitted per-replica configuration state: %v", replicas[0])
	}
}

func TestServiceStatusRejectsAcknowledgementForDifferentGeneration(t *testing.T) {
	desired, applied := int64(8), int64(7)
	metadata := models.ServiceMetadata{
		ReplicaID: "replica-1", ArtifactVersion: "1.2.3", ArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		Replicas: []models.ServiceReplicaRelease{{
			ReplicaID: "replica-1", ArtifactVersion: "1.2.3", ArtifactDigest: "sha256:" + strings.Repeat("a", 64),
			Identity:      map[string]any{"instanceId": "server", "replicaId": "replica-1"},
			Readiness:     map[string]any{"instanceId": "server", "replicaId": "replica-1", "ready": true},
			Configuration: models.ServiceReplicaConfigurationStatus{DesiredGeneration: &desired, AppliedGeneration: &applied, State: models.ServiceConfigurationAcknowledged},
		}},
	}
	request := httptest.NewRequest(http.MethodGet, "/api/services/server/status", nil)
	response := httptest.NewRecorder()
	ServiceMetadata(response, request, func(context.Context, string) (models.ServiceMetadata, error) { return metadata, nil }, "server", "status", jsonWriterForTest, problemWriterForTest, "req-1")
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestServiceStatusRejectsMissingArtifactProvenance(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/services/server/status", nil)
	response := httptest.NewRecorder()
	ServiceMetadata(response, request, func(context.Context, string) (models.ServiceMetadata, error) {
		return models.ServiceMetadata{Identity: map[string]any{"instanceId": "server"}}, nil
	}, "server", "status", jsonWriterForTest, problemWriterForTest, "req-1")
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestServiceStatusRejectsMixedReplicaReleaseClaims(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/services/server/status", nil)
	response := httptest.NewRecorder()
	metadata := models.ServiceMetadata{
		ReplicaID: "replica-1", ArtifactVersion: "1.2.3", ArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		Replicas: []models.ServiceReplicaRelease{
			{ReplicaID: "replica-1", ArtifactVersion: "1.2.3", ArtifactDigest: "sha256:" + strings.Repeat("a", 64), Identity: map[string]any{"instanceId": "server", "replicaId": "replica-1"}, Readiness: map[string]any{"instanceId": "server", "replicaId": "replica-1", "ready": true}, Configuration: models.ServiceReplicaConfigurationStatus{State: models.ServiceConfigurationUnconfigured}},
			{ReplicaID: "replica-2", ArtifactVersion: "1.2.3", ArtifactDigest: "sha256:" + strings.Repeat("b", 64), Identity: map[string]any{"instanceId": "server", "replicaId": "replica-2"}, Readiness: map[string]any{"instanceId": "server", "replicaId": "replica-2", "ready": true}, Configuration: models.ServiceReplicaConfigurationStatus{State: models.ServiceConfigurationUnconfigured}},
		},
	}
	ServiceMetadata(response, request, func(context.Context, string) (models.ServiceMetadata, error) {
		return metadata, nil
	}, "server", "status", jsonWriterForTest, problemWriterForTest, "req-1")
	if response.Code != http.StatusBadGateway {
		t.Fatalf("mixed replica release claims status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestServiceMetadataRejectsOversizedAndNonObjectDocuments(t *testing.T) {
	const maximumBytes = 262144
	oversized := []byte(`{"opaque":"` + strings.Repeat("x", maximumBytes) + `"}`)
	for _, test := range []struct {
		name     string
		kind     string
		manifest []byte
		schema   []byte
	}{
		{name: "manifest exceeds contract limit", kind: "manifest", manifest: oversized},
		{name: "schema exceeds contract limit", kind: "schema", schema: oversized},
		{name: "manifest is not an object", kind: "manifest", manifest: []byte(`[]`)},
		{name: "schema is not an object", kind: "schema", schema: []byte(`42`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/services/server/"+test.kind, nil)
			response := httptest.NewRecorder()
			ServiceMetadata(response, request, func(context.Context, string) (models.ServiceMetadata, error) {
				return models.ServiceMetadata{Manifest: test.manifest, ConfigurationSchema: test.schema}, nil
			}, "server", test.kind, jsonWriterForTest, problemWriterForTest, "req-1")
			if response.Code != http.StatusBadGateway || !strings.HasPrefix(response.Body.String(), "service_metadata_invalid:") {
				t.Fatalf("invalid metadata was not rejected: status=%d responseBytes=%d", response.Code, response.Body.Len())
			}
		})
	}
}

func jsonWriterForTest(response http.ResponseWriter, status int, value any) {
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func problemWriterForTest(response http.ResponseWriter, status int, code, message, requestID string) {
	response.WriteHeader(status)
	_, _ = response.Write([]byte(code + ":" + message + ":" + requestID))
}
