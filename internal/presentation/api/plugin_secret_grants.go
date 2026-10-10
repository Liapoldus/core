package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/domain/interfaces"
	"github.com/Liapoldus/core/v3/internal/domain/models"
	sdkmodels "github.com/Liapoldus/plugin-sdk/v2/domain/models"
	pluginsdk "github.com/Liapoldus/plugin-sdk/v2/infrastructure"
)

// NewPluginSecretGrantHandler builds the generic, private Plugin SDK grant API.
// Requests are authorized only by the already verified mTLS replica identity;
// no product, capability or plugin name is interpreted here.
func NewPluginSecretGrantHandler(
	contract pluginsdk.HTTPContract,
	service *application.PluginSecretGrantService,
	resolve interfaces.PluginReplicaIdentityResolver,
	next http.Handler,
) (http.Handler, error) {
	if service == nil || resolve == nil || next == nil || !validSecretGrantEndpoints(contract) {
		return nil, pluginsdk.ErrInvalidHTTPContract
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == contract.Core.SecretGrant.Issue.PathTemplate {
			serveSecretGrantIssue(contract, service, resolve, response, request)
			return
		}
		handle, matches := contractPathParameter(contract.Core.SecretGrant.Redemption.PathTemplate, request.URL.EscapedPath(), "{handle}")
		if matches {
			serveSecretGrantRedemption(contract, service, resolve, handle, response, request)
			return
		}
		next.ServeHTTP(response, request)
	}), nil
}

// NewPluginSDKControlHandler combines the SDK config-pull and secret-grant
// endpoints on the same mTLS listener without involving Management REST.
func NewPluginSDKControlHandler(
	configurations interfaces.PluginRetainedConfigurationReader,
	resolve interfaces.PluginReplicaIdentityResolver,
	grants *application.PluginSecretGrantService,
) (http.Handler, error) {
	contract, err := pluginsdk.LoadHTTPContract()
	if err != nil {
		return nil, pluginsdk.ErrInvalidHTTPContract
	}
	pull, err := NewPluginConfigurationPullHandler(configurations, resolve)
	if err != nil {
		return nil, err
	}
	return NewPluginSecretGrantHandler(contract, grants, resolve, pull)
}

func serveSecretGrantIssue(
	contract pluginsdk.HTTPContract,
	service *application.PluginSecretGrantService,
	resolve interfaces.PluginReplicaIdentityResolver,
	response http.ResponseWriter,
	request *http.Request,
) {
	endpoint := contract.Core.SecretGrant.Issue
	if request.Method != endpoint.Method {
		writeSDKContractProblem(response, contract, "methodNotAllowed", "", endpoint.ResponseMediaType)
		return
	}
	body, ok := readSecretGrantRequest(response, request, endpoint.RequestMediaType, endpoint.MaximumRequestBytes, contract)
	if !ok {
		return
	}
	defer clear(body)
	var input sdkmodels.SecretGrantRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || !singleJSONDocument(decoder) || input.Validate() != nil {
		writeSDKContractProblem(response, contract, "invalidRequest", "", endpoint.ResponseMediaType)
		return
	}
	identity, ok := verifiedReplicaIdentity(request, resolve)
	if !ok {
		writeSDKOutcomeProblem(response, contract, sdkmodels.OutcomeNotPermitted, endpoint.ResponseMediaType)
		return
	}
	receipt, err := service.Issue(request.Context(), identity, input.Reference, input.Purpose, input.Generation)
	if err != nil {
		writeSecretGrantFailure(response, contract, err, endpoint.ResponseMediaType)
		return
	}
	writeSDKJSON(response, http.StatusOK, endpoint.ResponseMediaType, receipt)
}

func serveSecretGrantRedemption(
	contract pluginsdk.HTTPContract,
	service *application.PluginSecretGrantService,
	resolve interfaces.PluginReplicaIdentityResolver,
	handle string,
	response http.ResponseWriter,
	request *http.Request,
) {
	endpoint := contract.Core.SecretGrant.Redemption
	if request.Method != endpoint.Method {
		writeSDKContractProblem(response, contract, "methodNotAllowed", "", endpoint.ResponseMediaType)
		return
	}
	body, ok := readSecretGrantRequest(response, request, endpoint.RequestMediaType, endpoint.MaximumRequestBytes, contract)
	if !ok {
		return
	}
	defer clear(body)
	var input sdkmodels.SecretRedemption
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || !singleJSONDocument(decoder) || input.Validate() != nil || input.Handle != handle || len(handle) > contract.Core.SecretGrant.HandleMaximumLength {
		writeSDKContractProblem(response, contract, "invalidRequest", "", endpoint.ResponseMediaType)
		return
	}
	identity, ok := verifiedReplicaIdentity(request, resolve)
	if !ok {
		writeSDKOutcomeProblem(response, contract, sdkmodels.OutcomeNotPermitted, endpoint.ResponseMediaType)
		return
	}
	secret, err := service.Redeem(request.Context(), identity, handle)
	if err != nil {
		writeSecretGrantFailure(response, contract, err, endpoint.ResponseMediaType)
		return
	}
	defer clear(secret)
	response.Header().Set(http.CanonicalHeaderKey("content-type"), endpoint.ResponseMediaType)
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(secret)
}

func readSecretGrantRequest(
	response http.ResponseWriter,
	request *http.Request,
	expectedMediaType string,
	maximumBytes int64,
	contract pluginsdk.HTTPContract,
) ([]byte, bool) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get(http.CanonicalHeaderKey("content-type")))
	if err != nil || mediaType != expectedMediaType {
		writeSDKContractProblem(response, contract, "unsupportedMediaType", "", expectedMediaType)
		return nil, false
	}
	if request.Body == nil || maximumBytes <= 0 {
		writeSDKContractProblem(response, contract, "invalidRequest", "", expectedMediaType)
		return nil, false
	}
	defer request.Body.Close()
	body, err := io.ReadAll(io.LimitReader(request.Body, maximumBytes+1))
	if err != nil {
		writeSDKContractProblem(response, contract, "invalidRequest", "", expectedMediaType)
		return nil, false
	}
	if int64(len(body)) > maximumBytes {
		clear(body)
		writeSDKContractProblem(response, contract, "payloadOversized", "", expectedMediaType)
		return nil, false
	}
	return body, true
}

func verifiedReplicaIdentity(request *http.Request, resolve interfaces.PluginReplicaIdentityResolver) (models.PluginReplicaIdentity, bool) {
	if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 || len(request.TLS.VerifiedChains) == 0 {
		return models.PluginReplicaIdentity{}, false
	}
	certificate := request.TLS.PeerCertificates[0]
	instanceID, authorized := resolve(certificate)
	if !authorized || instanceID == "" {
		return models.PluginReplicaIdentity{}, false
	}
	fingerprint := sha256.Sum256(certificate.Raw)
	return models.PluginReplicaIdentity{InstanceID: instanceID, Fingerprint: hex.EncodeToString(fingerprint[:])}, true
}

func writeSecretGrantFailure(response http.ResponseWriter, contract pluginsdk.HTTPContract, err error, mediaType string) {
	var failure models.PluginSecretGrantFailure
	if !errors.As(err, &failure) {
		writeSDKOutcomeProblem(response, contract, sdkmodels.OutcomeCoreUnavailable, mediaType)
		return
	}
	var outcome sdkmodels.Outcome
	switch failure.Kind {
	case models.PluginSecretGrantDenied:
		outcome = sdkmodels.OutcomeGrantDenied
	case models.PluginSecretGrantUnknown:
		outcome = sdkmodels.OutcomeGrantUnknown
	case models.PluginSecretGrantExpired:
		outcome = sdkmodels.OutcomeGrantExpired
	case models.PluginSecretGrantSpent:
		outcome = sdkmodels.OutcomeGrantSpent
	default:
		outcome = sdkmodels.OutcomeCoreUnavailable
	}
	writeSDKOutcomeProblem(response, contract, outcome, mediaType)
}

func writeSDKOutcomeProblem(response http.ResponseWriter, contract pluginsdk.HTTPContract, outcome sdkmodels.Outcome, mediaType string) {
	problem, err := contract.StatusForOutcome(string(outcome))
	if err != nil {
		writeSDKContractProblem(response, contract, "internalError", "", mediaType)
		return
	}
	writeSDKJSON(response, problem.Status, mediaType, sdkmodels.Problem{Outcome: outcome, Code: problem.Code})
}

func writeSDKContractProblem(response http.ResponseWriter, contract pluginsdk.HTTPContract, key, outcome, mediaType string) {
	problem, err := contract.Problem(key)
	if err != nil {
		writeSDKJSON(response, http.StatusInternalServerError, mediaType, sdkmodels.Problem{})
		return
	}
	value := sdkmodels.Problem{Code: problem.Code}
	if outcome != "" {
		value.Outcome = sdkmodels.Outcome(outcome)
	}
	writeSDKJSON(response, problem.Status, mediaType, value)
}

func writeSDKJSON(response http.ResponseWriter, status int, mediaType string, value any) {
	writeJSONResponse(response, status, http.CanonicalHeaderKey("content-type"), mediaType, value)
}

func singleJSONDocument(decoder *json.Decoder) bool {
	var trailing any
	return decoder.Decode(&trailing) == io.EOF
}

func validSecretGrantEndpoints(contract pluginsdk.HTTPContract) bool {
	issue, redemption := contract.Core.SecretGrant.Issue, contract.Core.SecretGrant.Redemption
	if issue.Method == "" || issue.PathTemplate == "" || issue.RequestMediaType == "" || issue.ResponseMediaType == "" || issue.MaximumRequestBytes <= 0 || issue.MaximumResponseBytes <= 0 ||
		redemption.Method == "" || redemption.PathTemplate == "" || redemption.RequestMediaType == "" || redemption.ResponseMediaType == "" || redemption.MaximumRequestBytes <= 0 || redemption.MaximumResponseBytes <= 0 {
		return false
	}
	if contract.Core.SecretGrant.RedemptionUseLimit != 1 || contract.Core.SecretGrant.HandleMaximumLength <= 0 || contract.Core.SecretGrant.ReferenceMaximumLength <= 0 || contract.Core.SecretGrant.PurposeMaximumLength <= 0 {
		return false
	}
	_, matches := contractPathParameter(redemption.PathTemplate, strings.Replace(redemption.PathTemplate, "{handle}", "x", 1), "{handle}")
	return matches && contract.Deadlines.CoreSecretGrantSeconds > 0 && contract.Deadlines.CoreSecretRedemptionSeconds > 0
}

func contractPathParameter(template, escapedPath, placeholder string) (string, bool) {
	if !strings.Contains(template, placeholder) || strings.Count(template, placeholder) != 1 {
		return "", false
	}
	parts := strings.SplitN(template, placeholder, 2)
	if !strings.HasPrefix(escapedPath, parts[0]) || !strings.HasSuffix(escapedPath, parts[1]) {
		return "", false
	}
	segment := strings.TrimSuffix(strings.TrimPrefix(escapedPath, parts[0]), parts[1])
	if segment == "" || strings.Contains(segment, "/") {
		return "", false
	}
	decoded, err := url.PathUnescape(segment)
	return decoded, err == nil && decoded != "" && !strings.Contains(decoded, "/")
}
