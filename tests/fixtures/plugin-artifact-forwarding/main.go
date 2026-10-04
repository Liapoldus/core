package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/internal/presentation/api"
	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
	sdkinfrastructure "github.com/Liapoldus/plugin-sdk/infrastructure"
)

const token = "artifact-forwarding-token"

func main() {
	management, err := config.LoadManagement()
	if err != nil {
		panic(err)
	}
	auditWords, err := config.LoadAudit()
	if err != nil {
		panic(err)
	}
	replica := &artifactReplica{}
	surface := []byte(`{"pages":[]}`)
	digest := sha256.Sum256(surface)
	replica.surface = surface
	replica.surfaceDigest = "sha256:" + hex.EncodeToString(digest[:])
	control := &plugins.SDKAdminControl{
		Clients: map[string]plugins.SDKReloadClient{"forms-a": &plugins.SDKReloadFanout{
			InstanceID: "forms-a", Replicas: []plugins.SDKReloadReplicaClient{{ReplicaID: "replica-a", Client: replica}},
		}},
		EligibleReplicaIDs: func(context.Context, string) ([]string, error) { return []string{"replica-a"}, nil },
		HTTPContract: sdkinfrastructure.HTTPContract{Plugin: sdkinfrastructure.PluginContract{
			AdminAction:  sdkinfrastructure.AdminActionContract{MaximumRequestBytes: 64 * 1024},
			AdminSurface: sdkinfrastructure.DocumentContract{DigestAlgorithm: "SHA-256"},
			ArtifactStream: sdkinfrastructure.ArtifactStreamContract{
				MediaType:            "multipart/form-data",
				MaximumArtifactBytes: 8 * 1024 * 1024, MinimumArtifactBytes: 1,
				MaximumMetadataBytes: 64 * 1024, MaximumMultipartOverheadBytes: 64 * 1024,
				MaximumRequestBytes: 9 * 1024 * 1024, MaximumReceiptBytes: 1024,
				Parts: []string{"metadata", "artifact"}, MetadataMediaType: "application/json",
			},
		}},
	}
	auditStore := &auditMemoryStore{}
	server := &api.Server{
		Token: token, Management: management, AuditWords: auditWords, PluginAdminControl: control,
		Audit: &application.AuditService{Store: auditStore, RetentionDays: auditWords.Audit.RetentionDays,
			MinimumLimit: management.Pagination.LimitMin, DefaultLimit: management.Pagination.LimitDefault,
			MaximumLimit: management.Pagination.LimitMax, InvalidLimit: auditWords.Audit.InvalidLimit},
	}
	listener := httptest.NewServer(server.Handler())
	defer listener.Close()
	client := listener.Client()

	artifact := bytes.Repeat([]byte{0x5a}, 2*1024*1024)
	metadata := []byte(`{"siteId":"site-a","idempotencyKey":"artifact-op"}`)
	path := management.Paths.Plugins + management.Paths.PluginIDSeparator + "forms-a" + management.Paths.PluginIDSeparator +
		management.Paths.AdminPages + management.Paths.PluginIDSeparator + "sites" + management.Paths.PluginIDSeparator +
		management.Paths.AdminActions + management.Paths.PluginIDSeparator + "publish"
	validBody, contentType := multipartBody(metadata, artifact, true)
	valid := send(client, listener.URL+path, token, contentType, validBody, management.Headers.ContentType, management.Headers.IfMatch,
		management.Idempotency.Key, "artifact-idempotency", `"site-revision-1"`)
	validInvocation := replica.invocation
	validArtifactBytes := replica.artifactBytes

	noMatchBody, noMatchType := multipartBody(metadata, artifact[:1], true)
	noMatch := send(client, listener.URL+path, token, noMatchType, noMatchBody, management.Headers.ContentType, management.Headers.IfMatch,
		management.Idempotency.Key, "artifact-create", "")
	noMatchForwarded := replica.invocation.IfMatch == ""

	missingKeyBody, missingKeyType := multipartBody(metadata, artifact[:1], true)
	missingKey := send(client, listener.URL+path, token, missingKeyType, missingKeyBody, management.Headers.ContentType, management.Headers.IfMatch,
		management.Idempotency.Key, "", `"site-revision-1"`)
	invalid := send(client, listener.URL+path, token, "multipart/form-data; boundary=broken", strings.NewReader("not multipart"), management.Headers.ContentType,
		management.Headers.IfMatch, management.Idempotency.Key, "artifact-idempotency", `"site-revision-1"`)
	unauthorized := send(client, listener.URL+path, "", contentType, strings.NewReader(""), management.Headers.ContentType,
		management.Headers.IfMatch, management.Idempotency.Key, "artifact-idempotency", `"site-revision-1"`)

	var accepted map[string]any
	if err := json.Unmarshal(valid.body, &accepted); err != nil {
		panic(err)
	}
	invocation := validInvocation
	bound := invocation.CallerID == auditWords.Audit.Actors.StaticToken && invocation.InstanceID == "forms-a" &&
		invocation.PageID == "sites" && invocation.ActionID == "publish" && invocation.RequestID != "" &&
		invocation.IdempotencyKey == "artifact-idempotency" && invocation.IfMatch == `"site-revision-1"` &&
		invocation.SurfaceDigest == replica.surfaceDigest
	result := map[string]any{
		"acceptedStatus":             valid.status,
		"optionalIfMatchAccepted":    noMatch.status == http.StatusAccepted,
		"optionalIfMatchForwarded":   noMatchForwarded,
		"metadataPreserved":          bytes.Equal(replica.metadata, metadata),
		"artifactByteCount":          validArtifactBytes,
		"invocationBound":            bound && validInvocation.IfMatch == `"site-revision-1"`,
		"missingIdempotencyRejected": missingKey.status == http.StatusBadRequest,
		"invalidMultipartRejected":   invalid.status == http.StatusBadRequest,
		"unauthorizedRejected":       unauthorized.status == http.StatusUnauthorized,
		"noFilenameForwarded":        replica.filename == "" && valid.status == http.StatusAccepted,
	}
	_ = accepted
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
}

func multipartBody(metadata, artifact []byte, includeFilename bool) (io.Reader, string) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	metadataHeader := make(textproto.MIMEHeader)
	metadataHeader.Set("Content-Disposition", `form-data; name="metadata"`)
	metadataHeader.Set("Content-Type", "application/json")
	metadataPart, err := writer.CreatePart(metadataHeader)
	if err != nil {
		panic(err)
	}
	if _, err := metadataPart.Write(metadata); err != nil {
		panic(err)
	}
	artifactHeader := make(textproto.MIMEHeader)
	if includeFilename {
		artifactHeader.Set("Content-Disposition", `form-data; name="artifact"; filename="ignored.tar.gz"`)
	} else {
		artifactHeader.Set("Content-Disposition", `form-data; name="artifact"`)
	}
	artifactHeader.Set("Content-Type", "application/gzip")
	artifactPart, err := writer.CreatePart(artifactHeader)
	if err != nil {
		panic(err)
	}
	if _, err := artifactPart.Write(artifact); err != nil {
		panic(err)
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}
	return bytes.NewReader(body.Bytes()), writer.FormDataContentType()
}

type httpResult struct {
	status int
	body   []byte
}

func send(client *http.Client, url, bearer, contentType string, body io.Reader, contentTypeHeader, ifMatchHeader, idempotencyHeader, idempotencyValue, ifMatch string) httpResult {
	request, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		panic(err)
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	request.Header.Set(contentTypeHeader, contentType)
	if ifMatch != "" {
		request.Header.Set(ifMatchHeader, ifMatch)
	}
	request.Header.Set(idempotencyHeader, idempotencyValue)
	response, err := client.Do(request)
	if err != nil {
		panic(err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		panic(err)
	}
	return httpResult{status: response.StatusCode, body: contents}
}

type artifactReplica struct {
	surface       []byte
	surfaceDigest string
	invocation    sdkmodels.ArtifactInvocation
	metadata      []byte
	artifactBytes int64
	filename      string
	artifactCalls int
}

func (replica *artifactReplica) Reload(context.Context, sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
	return sdkmodels.ReloadAcknowledgement{}, nil
}

func (replica *artifactReplica) AdminSurface(context.Context) (sdkinfrastructure.AdminSurfaceDocument, error) {
	return sdkinfrastructure.AdminSurfaceDocument{Bytes: append([]byte(nil), replica.surface...), SHA256: replica.surfaceDigest}, nil
}

func (replica *artifactReplica) AdminAction(context.Context, sdkmodels.AdminActionInvocation, []byte) (sdkinfrastructure.AdminActionResult, error) {
	return sdkinfrastructure.AdminActionResult{}, errors.New("not used")
}

func (replica *artifactReplica) ArtifactStream(_ context.Context, invocation sdkmodels.ArtifactInvocation, metadata []byte, contentType string, artifact io.ReadCloser) (sdkinfrastructure.ArtifactStreamResult, error) {
	defer artifact.Close()
	if contentType != "application/gzip" {
		return sdkinfrastructure.ArtifactStreamResult{}, errors.New("unexpected media type")
	}
	replica.artifactCalls++
	replica.invocation = invocation
	replica.metadata = append([]byte(nil), metadata...)
	count, err := io.Copy(io.Discard, artifact)
	if err != nil {
		return sdkinfrastructure.ArtifactStreamResult{}, err
	}
	replica.artifactBytes = count
	return sdkinfrastructure.ArtifactStreamResult{StatusCode: http.StatusAccepted, Body: []byte(`{"operationId":"operation-1"}`)}, nil
}

type auditMemoryStore struct{}

func (*auditMemoryStore) Append(context.Context, models.AuditRecord) error { return nil }
func (*auditMemoryStore) List(context.Context, time.Time, string, int) (models.AuditPage, error) {
	return models.AuditPage{}, nil
}
