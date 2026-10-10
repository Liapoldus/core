package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/v3/internal/presentation/api"
	sdkmodels "github.com/Liapoldus/plugin-sdk/v2/domain/models"
	sdkinfrastructure "github.com/Liapoldus/plugin-sdk/v2/infrastructure"
)

const (
	managementToken = "admin-surface-fixture-token"
	requestBody     = `{"filter":"all"}`
	resultBody      = `{"accepted":true}`
)

func main() {
	management, err := config.LoadManagement()
	if err != nil {
		panic(err)
	}
	auditWords, err := config.LoadAudit()
	if err != nil {
		panic(err)
	}
	first := &fixtureReplica{surface: []byte("{ \"pages\" : [] }")}
	second := &fixtureReplica{surface: []byte("{ \"pages\" : [] }")}
	fanout := &plugins.SDKReloadFanout{InstanceID: "instance-a", Replicas: []plugins.SDKReloadReplicaClient{
		{ReplicaID: "replica-a", Client: first}, {ReplicaID: "replica-b", Client: second},
	}}
	control := &plugins.SDKAdminControl{
		Instances: func(context.Context) ([]string, error) { return []string{"instance-a"}, nil },
		ResolveFanout: func(context.Context, string) (*plugins.SDKReloadFanout, func(), bool, error) {
			return fanout, nil, true, nil
		},
		EligibleReplicaIDs: func(context.Context, string) ([]string, error) { return []string{"replica-b"}, nil },
		HTTPContract: sdkinfrastructure.HTTPContract{Plugin: sdkinfrastructure.PluginContract{
			AdminAction:  sdkinfrastructure.AdminActionContract{MaximumRequestBytes: 1024},
			AdminSurface: sdkinfrastructure.DocumentContract{DigestAlgorithm: "SHA-256"},
			ArtifactStream: sdkinfrastructure.ArtifactStreamContract{
				MediaType:            "multipart/form-data",
				MaximumArtifactBytes: 8 * 1024 * 1024, MaximumMetadataBytes: 64 * 1024,
				MaximumMultipartOverheadBytes: 64 * 1024, MaximumRequestBytes: 9 * 1024 * 1024,
				Parts: []string{"metadata", "artifact"}, MetadataMediaType: "application/json",
			},
		}},
	}
	auditStore := &memoryAuditStore{}
	server := &api.Server{
		Token: managementToken, Management: management, AuditWords: auditWords,
		PluginAdminControl: control,
		Audit: &application.AuditService{Store: auditStore, RetentionDays: auditWords.Audit.RetentionDays,
			MinimumLimit: management.Pagination.LimitMin, DefaultLimit: management.Pagination.LimitDefault,
			MaximumLimit: management.Pagination.LimitMax, InvalidLimit: auditWords.Audit.InvalidLimit},
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	client := httpServer.Client()

	unauthorizedSurface := perform(client, httpServer.URL, management.Paths.AdminSurfaces, "", http.MethodGet, nil, nil)
	firstSurface := perform(client, httpServer.URL, management.Paths.AdminSurfaces, managementToken, http.MethodGet, nil, nil)
	var surfaceDocument struct {
		Items []struct {
			InstanceID string          `json:"instanceId"`
			Descriptor json.RawMessage `json:"descriptor"`
			SHA256     string          `json:"sha256"`
		} `json:"items"`
	}
	if err := json.Unmarshal(firstSurface.body, &surfaceDocument); err != nil {
		panic(err)
	}
	digest := sha256.Sum256(first.surface)
	digestText := "sha256:" + hex.EncodeToString(digest[:])
	var descriptorCompact, sourceCompact bytes.Buffer
	if err := json.Compact(&descriptorCompact, surfaceDocument.Items[0].Descriptor); err != nil {
		panic(err)
	}
	if err := json.Compact(&sourceCompact, first.surface); err != nil {
		panic(err)
	}
	surfaceOK := firstSurface.response.StatusCode == http.StatusOK && len(surfaceDocument.Items) == 1 &&
		surfaceDocument.Items[0].InstanceID == "instance-a" && bytes.Equal(descriptorCompact.Bytes(), sourceCompact.Bytes()) &&
		surfaceDocument.Items[0].SHA256 == digestText

	second.surface = []byte(`{"pages":[]}`)
	mismatch := perform(client, httpServer.URL, management.Paths.AdminSurfaces, managementToken, http.MethodGet, nil, nil)
	second.surface = append([]byte(nil), first.surface...)

	basePath := management.Paths.Plugins + management.Paths.PluginIDSeparator + "instance-a" + management.Paths.PluginIDSeparator +
		management.Paths.AdminPages + management.Paths.PluginIDSeparator + "forms" + management.Paths.PluginIDSeparator +
		management.Paths.AdminActions + management.Paths.PluginIDSeparator + "export"
	unauthorizedAction := perform(client, httpServer.URL, basePath, "", http.MethodPost, strings.NewReader(requestBody), actionHeaders(management))
	forwarded := perform(client, httpServer.URL, basePath, managementToken, http.MethodPost, strings.NewReader(requestBody), actionHeaders(management))
	repeated := perform(client, httpServer.URL, basePath, managementToken, http.MethodPost, strings.NewReader(requestBody), actionHeaders(management))
	second.fail = true
	failed := perform(client, httpServer.URL, basePath, managementToken, http.MethodPost, strings.NewReader(requestBody), actionHeaders(management))
	second.fail = false

	var actionContextOK bool
	if len(second.invocations) == 3 {
		invocation := second.invocations[2]
		actionContextOK = invocation.CallerID == auditWords.Audit.Actors.StaticToken && invocation.InstanceID == "instance-a" &&
			invocation.PageID == "forms" && invocation.ActionID == "export" && invocation.RequestID != "" &&
			invocation.IdempotencyKey == "fixture-idem" && invocation.IfMatch == `"resource-1"` &&
			invocation.SurfaceDigest == digestText
	}
	sameKeyReinvoked := len(second.invocations) == 3 && second.invocations[0].IdempotencyKey == second.invocations[1].IdempotencyKey &&
		second.invocations[1].IdempotencyKey == second.invocations[2].IdempotencyKey &&
		second.invocations[0].RequestID != second.invocations[1].RequestID &&
		second.invocations[1].RequestID != second.invocations[2].RequestID

	audits := auditStore.snapshot()
	auditSafe := len(audits) == 3
	auditRequestIDs := make(map[string]struct{}, len(audits))
	for _, record := range audits {
		if _, exists := auditRequestIDs[record.RequestID]; exists {
			auditSafe = false
		}
		auditRequestIDs[record.RequestID] = struct{}{}
		auditSafe = auditSafe && record.Actor == auditWords.Audit.Actors.StaticToken && record.Resource == "instance-a" &&
			record.Action == auditWords.Audit.Actions.PluginAdminAction && record.RequestID != "" &&
			!strings.Contains(record.Resource, requestBody) && !strings.Contains(record.Resource, resultBody)
	}
	auditSafe = auditSafe && len(auditRequestIDs) == 3

	writeResult(map[string]any{
		"unauthorizedSurfaceDenied":         unauthorizedSurface.response.StatusCode == http.StatusUnauthorized,
		"surfaceDocumentsAggregated":        surfaceOK,
		"surfaceBytesAndDigestPreserved":    surfaceOK,
		"replicaSurfaceMismatchUnavailable": mismatch.response.StatusCode == http.StatusServiceUnavailable,
		"unauthorizedActionDenied":          unauthorizedAction.response.StatusCode == http.StatusUnauthorized,
		"actionContextBound":                actionContextOK && second.actionCalls == 3,
		"actionSelectedConvergedReplica":    first.actionCalls == 0 && second.actionCalls == 3,
		"actionResponsePreserved":           forwarded.response.StatusCode == http.StatusOK && string(forwarded.body) == resultBody,
		"sameKeyAcceptedActionInvokedAgain": repeated.response.StatusCode == http.StatusOK && string(repeated.body) == resultBody && sameKeyReinvoked,
		"failedActionNotReplayed":           failed.response.StatusCode == http.StatusServiceUnavailable && second.actionCalls == 3,
		"actionAuditRedacted":               auditSafe,
	})
}

func actionHeaders(management config.ManagementWords) http.Header {
	headers := make(http.Header)
	headers.Set(management.Headers.ContentType, management.ContentTypes.JSON)
	digest := sha256.Sum256([]byte("{ \"pages\" : [] }"))
	headers.Set(management.Headers.AdminSurfaceDigest, "sha256:"+hex.EncodeToString(digest[:]))
	headers.Set(management.Headers.IfMatch, `"resource-1"`)
	headers.Set(management.Idempotency.Key, "fixture-idem")
	return headers
}

type response struct {
	response *http.Response
	body     []byte
}

func perform(client *http.Client, baseURL, path, token, method string, body io.Reader, headers http.Header) response {
	request, err := http.NewRequest(method, baseURL+path, body)
	if err != nil {
		panic(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	result, err := client.Do(request)
	if err != nil {
		panic(err)
	}
	defer result.Body.Close()
	contents, err := io.ReadAll(result.Body)
	if err != nil {
		panic(err)
	}
	return response{response: result, body: contents}
}

type fixtureReplica struct {
	surface        []byte
	actionCalls    int
	fail           bool
	lastInvocation *sdkmodels.AdminActionInvocation
	invocations    []sdkmodels.AdminActionInvocation
}

func (replica *fixtureReplica) Reload(context.Context, sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
	return sdkmodels.ReloadAcknowledgement{}, nil
}

func (replica *fixtureReplica) AdminSurface(context.Context) (sdkinfrastructure.AdminSurfaceDocument, error) {
	digest := sha256.Sum256(replica.surface)
	return sdkinfrastructure.AdminSurfaceDocument{Bytes: append([]byte(nil), replica.surface...), SHA256: "sha256:" + hex.EncodeToString(digest[:])}, nil
}

func (replica *fixtureReplica) AdminAction(_ context.Context, invocation sdkmodels.AdminActionInvocation, input []byte) (sdkinfrastructure.AdminActionResult, error) {
	replica.actionCalls++
	copy := invocation
	replica.lastInvocation = &copy
	replica.invocations = append(replica.invocations, copy)
	if !bytesEqual(input, []byte(requestBody)) {
		return sdkinfrastructure.AdminActionResult{}, errors.New("invalid fixture input")
	}
	if replica.fail {
		return sdkinfrastructure.AdminActionResult{}, errors.New("fixture failure with sensitive text")
	}
	return sdkinfrastructure.AdminActionResult{StatusCode: http.StatusOK, Body: []byte(resultBody)}, nil
}

func (replica *fixtureReplica) ArtifactStream(context.Context, sdkmodels.ArtifactInvocation, []byte, string, io.ReadCloser) (sdkinfrastructure.ArtifactStreamResult, error) {
	return sdkinfrastructure.ArtifactStreamResult{}, errors.New("not used by this fixture")
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type memoryAuditStore struct {
	mu      sync.Mutex
	records []models.AuditRecord
}

func (store *memoryAuditStore) Append(_ context.Context, record models.AuditRecord) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.records = append(store.records, record)
	return nil
}

func (store *memoryAuditStore) List(context.Context, time.Time, string, int) (models.AuditPage, error) {
	return models.AuditPage{}, nil
}

func (store *memoryAuditStore) snapshot() []models.AuditRecord {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]models.AuditRecord(nil), store.records...)
}

func writeResult(value any) {
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		panic(err)
	}
}
