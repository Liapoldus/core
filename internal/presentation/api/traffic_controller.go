package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
)

const trafficRolloutsIntentPath = "/internal/v2/traffic-rollouts"

var errInvalidTrafficControllerIntent = errors.New("invalid traffic controller intent")

// NewTrafficControllerHandler is mounted only on the dedicated mTLS listener.
// It has no Management bearer authentication or platform-admin permissions.
func NewTrafficControllerHandler(service *application.TrafficRolloutService, allowed []config.PeerIdentityConfig, contract config.TrafficRolloutAPIContract) (http.Handler, error) {
	management, err := config.LoadManagement()
	if err != nil {
		return nil, err
	}
	catalog, err := config.LoadErrorCatalog()
	if err != nil {
		return nil, err
	}
	errorCodes, err := config.LoadTrafficControllerErrorCodes()
	if err != nil {
		return nil, err
	}
	auditWords, err := config.LoadAudit()
	if err != nil {
		return nil, err
	}
	identities := make(map[string]string, len(allowed)*2)
	for _, identity := range allowed {
		if identity.CommonName != "" {
			identities["cn:"+identity.CommonName] = identity.CommonName
		}
		if identity.UniformResourceIdentifier != "" {
			identities["uri:"+identity.UniformResourceIdentifier] = identity.UniformResourceIdentifier
		}
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		identity, identityAllowed := trafficControllerIdentity(request, identities)
		if !identityAllowed {
			writeTrafficControllerProblem(response, catalog, management, errorCodes.Forbidden, errorCodes.Unavailable)
			return
		}
		if request.URL.Path == trafficRolloutsIntentPath {
			if request.Method != http.MethodGet {
				response.Header().Set("Allow", http.MethodGet)
				http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
				return
			}
			writeControllerIntents(response, request, service, contract, catalog, management, errorCodes.Unavailable)
			return
		}
		rolloutID, matched := controllerConfirmationPath(request.URL.Path)
		if !matched {
			http.NotFound(response, request)
			return
		}
		if request.Method != http.MethodPut {
			response.Header().Set("Allow", http.MethodPut)
			http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if service == nil || service.Store == nil {
			writeTrafficControllerProblem(response, catalog, management, errorCodes.Unavailable, errorCodes.Unavailable)
			return
		}
		mediaType, _, mediaErr := mime.ParseMediaType(request.Header.Get(management.Headers.ContentType))
		if mediaErr != nil || mediaType != management.ContentTypes.JSON {
			writeTrafficControllerProblem(response, catalog, management, management.Codes.InvalidRequest, errorCodes.Unavailable)
			return
		}
		body, err := io.ReadAll(io.LimitReader(request.Body, contract.MaximumControllerConfirmationBytes+1))
		if err != nil || int64(len(body)) > contract.MaximumControllerConfirmationBytes {
			writeTrafficControllerProblem(response, catalog, management, management.Codes.InvalidRequest, errorCodes.Unavailable)
			return
		}
		fields := contract.ControllerJSON
		var confirmation map[string]json.RawMessage
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		if decoder.Decode(&confirmation) != nil || len(confirmation) != 3 || decoder.Decode(new(any)) != io.EOF {
			writeTrafficControllerProblem(response, catalog, management, management.Codes.InvalidRequest, errorCodes.Unavailable)
			return
		}
		var stageID, controllerRevision string
		var appliedWeight int
		if json.Unmarshal(confirmation[fields.StageID], &stageID) != nil || stageID == "" ||
			json.Unmarshal(confirmation[fields.ControllerRevision], &controllerRevision) != nil || controllerRevision == "" || len(controllerRevision) > 256 ||
			json.Unmarshal(confirmation[fields.AppliedCandidateWeightPercent], &appliedWeight) != nil {
			writeTrafficControllerProblem(response, catalog, management, management.Codes.InvalidRequest, errorCodes.Unavailable)
			return
		}
		expectedRevision, valid := parseControllerRevisionETag(request.Header.Get(management.Headers.IfMatch))
		key := request.Header.Get(management.Idempotency.Key)
		keyLength := utf8.RuneCountInString(key)
		if !valid || keyLength < management.Idempotency.KeyMin || keyLength > management.Idempotency.KeyMax {
			writeTrafficControllerProblem(response, catalog, management, management.Codes.InvalidRequest, errorCodes.Unavailable)
			return
		}
		digestInput := append([]byte(request.Method+"\x00"+request.URL.Path+"\x00"+request.Header.Get(management.Headers.IfMatch)+"\x00"), body...)
		digest := sha256.Sum256(digestInput)
		audit := models.AuditRecord{Actor: identity, Action: contract.ControllerConfirmationAuditAction, Resource: rolloutID,
			Result: auditWords.Audit.Results.Succeeded, RequestID: key}
		receipt, _, err := service.Store.ConfirmStageOnce(request.Context(), rolloutID, expectedRevision, stageID,
			appliedWeight, controllerRevision, identity, key, hex.EncodeToString(digest[:]), time.Now().UTC(), audit)
		if err != nil {
			code := errorCodes.Unavailable
			var idempotencyConflict models.IdempotencyConflict
			var rolloutConflict models.TrafficRolloutConflict
			var notFound models.TrafficRolloutNotFound
			var invalid models.TrafficRolloutInvalid
			switch {
			case errors.As(err, &idempotencyConflict):
				code = management.Codes.IdempotencyConflict
			case errors.As(err, &rolloutConflict):
				code = management.Codes.PluginRevisionConflict
			case errors.As(err, &notFound):
				code = management.Codes.PluginNotFound
			case errors.As(err, &invalid):
				code = management.Codes.InvalidRequest
			}
			writeTrafficControllerProblem(response, catalog, management, code, errorCodes.Unavailable)
			return
		}
		response.Header().Set(management.Headers.ETag, `"`+strconv.FormatInt(receipt.Revision, 10)+`"`)
		writeJSONResponse(response, http.StatusOK, management.Headers.ContentType, management.ContentTypes.JSON, receipt)
	}), nil
}

func writeControllerIntents(response http.ResponseWriter, request *http.Request, service *application.TrafficRolloutService,
	contract config.TrafficRolloutAPIContract,
	catalog config.ErrorCatalog, management config.ManagementWords, unavailable string) {
	records, err := service.ControllerIntents(request.Context())
	if err != nil {
		writeTrafficControllerProblem(response, catalog, management, unavailable, unavailable)
		return
	}
	intents, err := controllerIntents(records, contract.ControllerJSON)
	if err != nil {
		writeTrafficControllerProblem(response, catalog, management, unavailable, unavailable)
		return
	}
	body, err := json.Marshal(map[string]any{contract.ControllerJSON.Rollouts: intents})
	if err != nil {
		writeTrafficControllerProblem(response, catalog, management, unavailable, unavailable)
		return
	}
	digest := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(digest[:]) + `"`
	response.Header().Set(management.Headers.ETag, etag)
	response.Header().Set(management.Headers.ContentType, management.ContentTypes.JSON)
	if request.Header.Get("If-None-Match") == etag {
		response.WriteHeader(http.StatusNotModified)
		return
	}
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(body)
}

func writeTrafficControllerProblem(response http.ResponseWriter, catalog config.ErrorCatalog, management config.ManagementWords, code, fallback string) {
	problem, exists := catalog.Lookup(code)
	if !exists {
		problem, _ = catalog.Lookup(fallback)
	}
	writeJSONResponse(response, problem.Status, management.Headers.ContentType, management.ContentTypes.Problem, problem)
}

func trafficControllerIdentity(request *http.Request, allowed map[string]string) (string, bool) {
	if request == nil || request.TLS == nil || len(request.TLS.VerifiedChains) == 0 || len(request.TLS.PeerCertificates) == 0 {
		return "", false
	}
	certificate := request.TLS.PeerCertificates[0]
	if identity, exists := allowed["cn:"+certificate.Subject.CommonName]; exists {
		return identity, true
	}
	for _, uri := range certificate.URIs {
		if identity, exists := allowed["uri:"+uri.String()]; exists {
			return identity, true
		}
	}
	return "", false
}

func controllerConfirmationPath(path string) (string, bool) {
	const suffix = "/confirmation"
	resource := strings.TrimPrefix(path, trafficRolloutsIntentPath+"/")
	if resource == path || !strings.HasSuffix(resource, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(resource, suffix)
	return id, id != "" && !strings.ContainsAny(id, "/\\")
}

func parseControllerRevisionETag(value string) (int64, bool) {
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' || strings.HasPrefix(value, "W/") {
		return 0, false
	}
	revision, err := strconv.ParseInt(value[1:len(value)-1], 10, 64)
	return revision, err == nil && revision > 0
}

func controllerIntents(records []models.TrafficRolloutRecord, fields config.TrafficControllerJSONFields) ([]map[string]any, error) {
	intents := make([]map[string]any, 0, len(records))
	for _, record := range records {
		var plan models.TrafficRolloutPlan
		if record.ID == "" || record.InstanceID == "" || record.Generation < 1 || json.Unmarshal(record.PlanJSON, &plan) != nil || len(record.Stages) == 0 ||
			record.ActiveStageIndex < 0 || record.ActiveStageIndex >= len(record.Stages) || record.Stages[record.ActiveStageIndex].Stage.ID == "" {
			return nil, errInvalidTrafficControllerIntent
		}
		stages := make([]map[string]any, len(record.Stages))
		state := "awaiting_controller"
		var lastWeight *int
		for index, stage := range record.Stages {
			stages[index] = map[string]any{
				fields.StageID:                   stage.Stage.ID,
				fields.CandidateWeightPercent:    stage.Stage.CandidateWeightPercent,
				fields.MinimumObservationSeconds: stage.Stage.MinimumObservationSeconds,
				fields.RequireManualApproval:     stage.Stage.RequireManualApproval,
			}
			if stage.State == "confirmed" {
				weight := stage.AppliedCandidateWeight
				lastWeight = &weight
			}
		}
		active := record.Stages[record.ActiveStageIndex]
		if active.State == "confirmed" {
			state = "awaiting_manual_approval"
		} else if active.State != "active" {
			return nil, errInvalidTrafficControllerIntent
		}
		intent := map[string]any{
			fields.ID:               record.ID,
			fields.PluginID:         record.InstanceID,
			fields.ActiveGeneration: record.Generation,
			fields.Revision:         record.Revision,
			fields.ReleaseSHA256:    record.ReleaseSHA256,
			fields.Stages:           stages,
			fields.StageID:          active.Stage.ID,
			fields.State:            state,
		}
		if lastWeight != nil {
			intent[fields.LastConfirmedCandidateWeightPercent] = *lastWeight
		}
		intents = append(intents, intent)
	}
	return intents, nil
}
