package handlers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

type PluginMetadataReader func(context.Context, string) (models.PluginMetadata, error)

func PluginMetadata(response http.ResponseWriter, request *http.Request, reader PluginMetadataReader, instanceID, kind string, writeJSON func(http.ResponseWriter, int, any), writeProblem func(http.ResponseWriter, int, string, string, string), requestID string) {
	if reader == nil || strings.TrimSpace(instanceID) == "" {
		writeProblem(response, http.StatusServiceUnavailable, "plugin_unavailable", "plugin metadata is unavailable", requestID)
		return
	}
	metadata, err := reader(request.Context(), instanceID)
	if err != nil {
		writeProblem(response, http.StatusServiceUnavailable, "plugin_unavailable", "plugin metadata is unavailable", requestID)
		return
	}
	var value any
	switch kind {
	case "manifest":
		if !json.Valid(metadata.Manifest) {
			writeProblem(response, http.StatusBadGateway, "plugin_metadata_invalid", "plugin manifest is invalid", requestID)
			return
		}
		value = json.RawMessage(metadata.Manifest)
	case "schema":
		if !json.Valid(metadata.ConfigurationSchema) {
			writeProblem(response, http.StatusBadGateway, "plugin_metadata_invalid", "plugin schema is invalid", requestID)
			return
		}
		value = json.RawMessage(metadata.ConfigurationSchema)
	case "status":
		value = map[string]any{"instanceId": instanceID, "identity": metadata.Identity, "readiness": metadata.Readiness}
	default:
		writeProblem(response, http.StatusNotFound, "not_found", "plugin metadata resource not found", requestID)
		return
	}
	writeJSON(response, http.StatusOK, value)
}

type PluginRegistrationWriter func(context.Context, models.PluginRegistration) (models.PluginRegistrationResult, error)

// PluginRegistration accepts only safe runtime metadata from the CLI. Core
// obtains the live manifest, schema, identity and readiness from the replica
// through the Plugin SDK client; this request never carries those documents.
func PluginRegistration(response http.ResponseWriter, request *http.Request, writer PluginRegistrationWriter, routeInstanceID string, writeJSON func(http.ResponseWriter, int, any), writeProblem func(http.ResponseWriter, int, string, string, string), requestID string) {
	if writer == nil || request == nil || request.Method != http.MethodPost {
		writeProblem(response, http.StatusServiceUnavailable, "plugin_registration_unavailable", "plugin registration is unavailable", requestID)
		return
	}
	var registration models.PluginRegistration
	if err := decodePluginRegistration(request, &registration); err != nil || registration.InstanceID != routeInstanceID || !validPluginRegistration(registration) {
		writeProblem(response, http.StatusBadRequest, "invalid_request", "invalid plugin registration metadata", requestID)
		return
	}
	result, err := writer(request.Context(), registration)
	if err != nil {
		writeProblem(response, http.StatusServiceUnavailable, "plugin_registration_failed", "plugin registration failed", requestID)
		return
	}
	writeJSON(response, http.StatusAccepted, result)
}

func decodePluginRegistration(request *http.Request, target *models.PluginRegistration) error {
	if request == nil || request.Body == nil || request.Header.Get("Content-Type") != "application/json" {
		return errors.New("application/json body required")
	}
	defer request.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(request.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing request content")
	}
	return nil
}

func validPluginRegistration(registration models.PluginRegistration) bool {
	for _, value := range []string{registration.ArtifactID, registration.ArtifactVersion, registration.InstanceID, registration.ReplicaID, registration.RuntimeStatus} {
		if strings.TrimSpace(value) == "" || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return false
		}
	}
	if len(registration.Digest) != len("sha256:")+64 || !strings.HasPrefix(registration.Digest, "sha256:") {
		return false
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(registration.Digest, "sha256:")); err != nil {
		return false
	}
	for _, value := range []string{registration.ControlEndpoint, registration.PeerEndpoint} {
		if value == "" {
			continue
		}
		parsed, err := url.ParseRequestURI(value)
		if err != nil || parsed.User != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return false
		}
	}
	return true
}
