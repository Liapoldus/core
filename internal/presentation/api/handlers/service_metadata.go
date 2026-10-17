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

const maximumServiceMetadataBytes = 262144

type ServiceMetadataReader func(context.Context, string) (models.ServiceMetadata, error)

func ServiceMetadata(response http.ResponseWriter, request *http.Request, reader ServiceMetadataReader, instanceID, kind string, writeJSON func(http.ResponseWriter, int, any), writeProblem func(http.ResponseWriter, int, string, string, string), requestID string) {
	if reader == nil || strings.TrimSpace(instanceID) == "" {
		writeProblem(response, http.StatusServiceUnavailable, "service_unavailable", "service metadata is unavailable", requestID)
		return
	}
	metadata, err := reader(request.Context(), instanceID)
	if err != nil {
		writeProblem(response, http.StatusServiceUnavailable, "service_unavailable", "service metadata is unavailable", requestID)
		return
	}
	var value any
	switch kind {
	case "manifest":
		if !validServiceMetadataDocument(metadata.Manifest) {
			writeProblem(response, http.StatusBadGateway, "service_metadata_invalid", "service manifest is invalid", requestID)
			return
		}
		value = json.RawMessage(metadata.Manifest)
	case "schema":
		if !validServiceMetadataDocument(metadata.ConfigurationSchema) {
			writeProblem(response, http.StatusBadGateway, "service_metadata_invalid", "service schema is invalid", requestID)
			return
		}
		value = json.RawMessage(metadata.ConfigurationSchema)
	case "status":
		if metadata.ReplicaID == "" || metadata.ArtifactVersion == "" || !validArtifactDigest(metadata.ArtifactDigest) || len(metadata.Replicas) == 0 {
			writeProblem(response, http.StatusBadGateway, "service_metadata_invalid", "service runtime provenance is invalid", requestID)
			return
		}
		replicas := make([]map[string]any, 0, len(metadata.Replicas))
		seenReplicaIDs := make(map[string]struct{}, len(metadata.Replicas))
		foundRepresentative := false
		for _, replica := range metadata.Replicas {
			if replica.ReplicaID == "" || replica.ArtifactVersion == "" || !validArtifactDigest(replica.ArtifactDigest) ||
				replica.ArtifactVersion != metadata.ArtifactVersion || replica.ArtifactDigest != metadata.ArtifactDigest {
				writeProblem(response, http.StatusBadGateway, "service_metadata_invalid", "service runtime provenance is invalid", requestID)
				return
			}
			if _, duplicate := seenReplicaIDs[replica.ReplicaID]; duplicate {
				writeProblem(response, http.StatusBadGateway, "service_metadata_invalid", "service runtime provenance is invalid", requestID)
				return
			}
			seenReplicaIDs[replica.ReplicaID] = struct{}{}
			identityInstance, identityReplica, identityOK := metadataIdentity(replica.Identity)
			readinessInstance, readinessReplica, ready, readinessOK := metadataReadiness(replica.Readiness)
			if !identityOK || !readinessOK || identityInstance != instanceID || identityReplica != replica.ReplicaID ||
				readinessInstance != instanceID || readinessReplica != replica.ReplicaID || !validReplicaConfigurationStatus(replica.Configuration) {
				writeProblem(response, http.StatusBadGateway, "service_metadata_invalid", "service runtime replica status is invalid", requestID)
				return
			}
			if replica.ReplicaID == metadata.ReplicaID {
				foundRepresentative = true
			}
			replicas = append(replicas, map[string]any{
				"replicaId": replica.ReplicaID, "artifactVersion": replica.ArtifactVersion,
				"artifactDigest": replica.ArtifactDigest, "identity": replica.Identity,
				"readiness": replica.Readiness, "ready": ready, "configuration": replica.Configuration,
			})
		}
		if !foundRepresentative {
			writeProblem(response, http.StatusBadGateway, "service_metadata_invalid", "service runtime provenance is invalid", requestID)
			return
		}
		value = map[string]any{
			"instanceId": instanceID, "replicaId": metadata.ReplicaID,
			"artifactId": instanceID, "artifactVersion": metadata.ArtifactVersion,
			"artifactDigest": metadata.ArtifactDigest,
			"identity":       metadata.Identity, "readiness": metadata.Readiness, "replicas": replicas,
		}
	default:
		writeProblem(response, http.StatusNotFound, "not_found", "service metadata resource not found", requestID)
		return
	}
	writeJSON(response, http.StatusOK, value)
}

func validReplicaConfigurationStatus(status models.ServiceReplicaConfigurationStatus) bool {
	switch status.State {
	case models.ServiceConfigurationUnconfigured:
		return status.DesiredGeneration == nil && status.AppliedGeneration == nil && status.LastFailureCode == ""
	case models.ServiceConfigurationUnobserved, models.ServiceConfigurationPending, models.ServiceConfigurationAcknowledged,
		models.ServiceConfigurationStale, models.ServiceConfigurationFailed, models.ServiceConfigurationUnreachable:
		if status.DesiredGeneration == nil || *status.DesiredGeneration < 1 ||
			status.AppliedGeneration != nil && *status.AppliedGeneration < 1 || len(status.LastFailureCode) > 64 {
			return false
		}
		for _, character := range status.LastFailureCode {
			if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '.' || character == '-') {
				return false
			}
		}
		if status.State == models.ServiceConfigurationAcknowledged {
			return status.AppliedGeneration != nil && *status.AppliedGeneration == *status.DesiredGeneration && status.LastFailureCode == ""
		}
		return status.State != models.ServiceConfigurationUnconfigured
	default:
		return false
	}
}

func validArtifactDigest(digest string) bool {
	if len(digest) != len("sha256:")+64 || !strings.HasPrefix(digest, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	return err == nil
}

func metadataIdentity(value any) (string, string, bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", "", false
	}
	var identity struct {
		InstanceID string `json:"instanceId"`
		ReplicaID  string `json:"replicaId"`
	}
	if err = json.Unmarshal(encoded, &identity); err != nil || identity.InstanceID == "" || identity.ReplicaID == "" {
		return "", "", false
	}
	return identity.InstanceID, identity.ReplicaID, true
}

func metadataReadiness(value any) (string, string, bool, bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", "", false, false
	}
	var readiness struct {
		InstanceID string `json:"instanceId"`
		ReplicaID  string `json:"replicaId"`
		Ready      *bool  `json:"ready"`
	}
	if err = json.Unmarshal(encoded, &readiness); err != nil || readiness.InstanceID == "" || readiness.ReplicaID == "" || readiness.Ready == nil {
		return "", "", false, false
	}
	return readiness.InstanceID, readiness.ReplicaID, *readiness.Ready, true
}

func validServiceMetadataDocument(document []byte) bool {
	if len(document) == 0 || len(document) > maximumServiceMetadataBytes || !json.Valid(document) {
		return false
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(document, &object); err != nil || object == nil {
		return false
	}
	return true
}

type ServiceRegistrationWriter func(context.Context, models.ServiceRegistration) (models.ServiceRegistrationResult, error)

// ServiceRegistration accepts only safe runtime metadata from the CLI. Core
// obtains the live manifest, schema, identity and readiness from the replica
// through the Service SDK client; this request never carries those documents.
func ServiceRegistration(response http.ResponseWriter, request *http.Request, writer ServiceRegistrationWriter, routeInstanceID string, writeJSON func(http.ResponseWriter, int, any), writeProblem func(http.ResponseWriter, int, string, string, string), requestID string) {
	if writer == nil || request == nil || request.Method != http.MethodPost {
		writeProblem(response, http.StatusServiceUnavailable, "service_registration_unavailable", "service registration is unavailable", requestID)
		return
	}
	var registration models.ServiceRegistration
	if err := decodeServiceRegistration(request, &registration); err != nil || registration.InstanceID != routeInstanceID || !validServiceRegistration(registration) {
		writeProblem(response, http.StatusBadRequest, "invalid_request", "invalid service registration metadata", requestID)
		return
	}
	result, err := writer(request.Context(), registration)
	if err != nil {
		if errors.Is(err, models.ErrServiceRegistrationMismatch) {
			writeProblem(response, http.StatusServiceUnavailable, "service_registration_failed", "running Service SDK identity does not match the CLI artifact metadata", requestID)
			return
		}
		writeProblem(response, http.StatusServiceUnavailable, "service_registration_failed", "service registration failed", requestID)
		return
	}
	writeJSON(response, http.StatusAccepted, result)
}

func decodeServiceRegistration(request *http.Request, target *models.ServiceRegistration) error {
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

func validServiceRegistration(registration models.ServiceRegistration) bool {
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
