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

func TestPluginRegistrationAcceptsOnlySafeMetadata(t *testing.T) {
	input := models.PluginRegistration{
		ArtifactID: "server", ArtifactVersion: "1.0.0", Digest: "sha256:" + "a1" + strings.Repeat("0", 62),
		InstanceID: "server", ReplicaID: "replica-1", ControlEndpoint: "https://server:8443", RuntimeStatus: "started",
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/plugins/server/replicas/register", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	var received models.PluginRegistration
	PluginRegistration(response, request, func(_ context.Context, value models.PluginRegistration) (models.PluginRegistrationResult, error) {
		received = value
		return models.PluginRegistrationResult{Accepted: true, InstanceID: value.InstanceID, ReplicaID: value.ReplicaID, Status: "pending"}, nil
	}, "server", jsonWriterForTest, problemWriterForTest, "req-1")
	if response.Code != http.StatusAccepted || received != input {
		t.Fatalf("status=%d received=%+v", response.Code, received)
	}
}

func TestPluginRegistrationRejectsPathAndSecretLikeMetadata(t *testing.T) {
	input := `{"artifactId":"server","artifactVersion":"1.0.0","digest":"sha256:` + "a1" + strings.Repeat("0", 62) + `","instanceId":"server","replicaId":"replica-1","controlEndpoint":"file:///private/key","runtimeStatus":"started"}`
	request := httptest.NewRequest(http.MethodPost, "/api/plugins/server/replicas/register", bytes.NewBufferString(input))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	PluginRegistration(response, request, func(context.Context, models.PluginRegistration) (models.PluginRegistrationResult, error) {
		t.Fatal("writer must not be called")
		return models.PluginRegistrationResult{}, nil
	}, "server", jsonWriterForTest, problemWriterForTest, "req-1")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
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
