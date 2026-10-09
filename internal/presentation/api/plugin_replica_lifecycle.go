package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
	sdkinfrastructure "github.com/Liapoldus/plugin-sdk/infrastructure"
)

// NewPluginReplicaLifecycleHandler provides the generic Plugin SDK v2
// registration endpoints on Core's private mTLS listener.
func NewPluginReplicaLifecycleHandler(
	contract sdkinfrastructure.ReplicaLifecycleContract,
	directory *plugins.PluginReplicaDirectory,
	register func(context.Context, sdkmodels.ReplicaRegistrationRequest) error,
	next http.Handler,
	afterRegister ...func(context.Context, string, string) error,
) (http.Handler, error) {
	if contract.Validate() != nil || directory == nil || register == nil || next == nil || len(afterRegister) > 1 || !validReplicaLifecycleEndpoints(contract) {
		return nil, sdkinfrastructure.ErrInvalidReplicaLifecycleContract
	}
	var registered func(context.Context, string, string) error
	if len(afterRegister) == 1 {
		registered = afterRegister[0]
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		for _, operation := range []string{"register", "renew", "deregister"} {
			endpoint := contract.Endpoints[operation]
			if request.URL.Path != endpoint.Path {
				continue
			}
			serveReplicaLifecycleOperation(contract, directory, register, registered, operation, response, request)
			return
		}
		next.ServeHTTP(response, request)
	}), nil
}

func serveReplicaLifecycleOperation(
	contract sdkinfrastructure.ReplicaLifecycleContract,
	directory *plugins.PluginReplicaDirectory,
	register func(context.Context, sdkmodels.ReplicaRegistrationRequest) error,
	afterRegister func(context.Context, string, string) error,
	operation string,
	response http.ResponseWriter,
	request *http.Request,
) {
	endpoint := contract.Endpoints[operation]
	requestSpec := contract.Requests[operation]
	responseSpec := contract.Responses[operation]
	if request.Method != endpoint.Method {
		writeReplicaLifecycleProblem(response, contract, "invalidRequest")
		return
	}
	if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 || len(request.TLS.VerifiedChains) == 0 {
		writeReplicaLifecycleProblem(response, contract, "unauthenticated")
		return
	}
	body, ok := readReplicaLifecycleBody(response, request, requestSpec.MediaType, requestSpec.MaximumBytes, contract)
	if !ok {
		return
	}
	defer clear(body)
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	certificate := request.TLS.PeerCertificates[0]
	switch operation {
	case "register":
		var input sdkmodels.ReplicaRegistrationRequest
		if decoder.Decode(&input) != nil || !singleJSONDocument(decoder) {
			writeReplicaLifecycleProblem(response, contract, "invalidRequest")
			return
		}
		result, err := withReplicaMembershipLock(func() (sdkmodels.ReplicaRegistrationResponse, error) {
			return directory.RegisterAndPersist(request.Context(), input, certificate, register)
		})
		if err != nil {
			writeReplicaLifecycleError(response, contract, err)
			return
		}
		if afterRegister != nil {
			if err := afterRegister(request.Context(), input.Identity.InstanceID, input.Identity.ReplicaID); err != nil {
				writeReplicaLifecycleProblem(response, contract, "unavailable")
				return
			}
		}
		writeJSONResponse(response, responseSpec.Status, http.CanonicalHeaderKey("content-type"), responseSpec.MediaType, result)
	case "renew":
		var input sdkmodels.ReplicaRenewalRequest
		if decoder.Decode(&input) != nil || !singleJSONDocument(decoder) {
			writeReplicaLifecycleProblem(response, contract, "invalidRequest")
			return
		}
		result, err := withReplicaMembershipLock(func() (sdkmodels.ReplicaLease, error) {
			return directory.Renew(input, certificate)
		})
		if err != nil {
			writeReplicaLifecycleError(response, contract, err)
			return
		}
		writeJSONResponse(response, responseSpec.Status, http.CanonicalHeaderKey("content-type"), responseSpec.MediaType, result)
	case "deregister":
		var input sdkmodels.ReplicaDeregistrationRequest
		if decoder.Decode(&input) != nil || !singleJSONDocument(decoder) {
			writeReplicaLifecycleProblem(response, contract, "invalidRequest")
			return
		}
		result, err := withReplicaMembershipLock(func() (sdkmodels.ReplicaDeregistrationResponse, error) {
			return directory.Deregister(input, certificate)
		})
		if err != nil {
			writeReplicaLifecycleError(response, contract, err)
			return
		}
		writeJSONResponse(response, responseSpec.Status, http.CanonicalHeaderKey("content-type"), responseSpec.MediaType, result)
	default:
		writeReplicaLifecycleProblem(response, contract, "unavailable")
	}
}

func withReplicaMembershipLock[T any](operation func() (T, error)) (T, error) {
	application.SnapshotActivationLock.Lock()
	defer application.SnapshotActivationLock.Unlock()
	return operation()
}

func readReplicaLifecycleBody(
	response http.ResponseWriter,
	request *http.Request,
	expectedMediaType string,
	maximumBytes int64,
	contract sdkinfrastructure.ReplicaLifecycleContract,
) ([]byte, bool) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get(http.CanonicalHeaderKey("content-type")))
	if err != nil || mediaType != expectedMediaType {
		writeReplicaLifecycleProblem(response, contract, "invalidRequest")
		return nil, false
	}
	if request.Body == nil || maximumBytes <= 0 {
		writeReplicaLifecycleProblem(response, contract, "invalidRequest")
		return nil, false
	}
	defer request.Body.Close()
	body, err := io.ReadAll(io.LimitReader(request.Body, maximumBytes+1))
	if err != nil {
		writeReplicaLifecycleProblem(response, contract, "invalidRequest")
		return nil, false
	}
	if int64(len(body)) > maximumBytes {
		clear(body)
		writeReplicaLifecycleProblem(response, contract, "invalidRequest")
		return nil, false
	}
	return body, true
}

func writeReplicaLifecycleError(response http.ResponseWriter, contract sdkinfrastructure.ReplicaLifecycleContract, err error) {
	var problem string
	switch {
	case errors.Is(err, sdkmodels.ErrInvalidReplicaRegistration):
		problem = "invalidRequest"
	case errors.Is(err, sdkmodels.ErrReplicaIdentityMismatch):
		problem = "identityMismatch"
	case errors.Is(err, sdkmodels.ErrReplicaNotRegistered):
		problem = "notRegistered"
	case errors.Is(err, sdkmodels.ErrReplicaIdentityConflict):
		problem = "identityConflict"
	case errors.Is(err, sdkmodels.ErrReplicaLeaseExpired):
		problem = "leaseExpired"
	default:
		problem = "unavailable"
	}
	writeReplicaLifecycleProblem(response, contract, problem)
}

func writeReplicaLifecycleProblem(response http.ResponseWriter, contract sdkinfrastructure.ReplicaLifecycleContract, name string) {
	problem, ok := contract.Problems[name]
	if !ok || problem.Status < 400 || problem.Code == "" {
		fallback := contract.Problems["unavailable"]
		writeJSONResponse(response, fallback.Status, http.CanonicalHeaderKey("content-type"), contract.ProblemMediaType,
			map[string]string{"code": fallback.Code})
		return
	}
	writeJSONResponse(response, problem.Status, http.CanonicalHeaderKey("content-type"), contract.ProblemMediaType,
		map[string]string{"code": problem.Code})
}

func validReplicaLifecycleEndpoints(contract sdkinfrastructure.ReplicaLifecycleContract) bool {
	seen := make(map[string]struct{}, len(contract.Endpoints))
	for _, name := range []string{"register", "renew", "deregister"} {
		endpoint := contract.Endpoints[name]
		request := contract.Requests[name]
		response := contract.Responses[name]
		if endpoint.Method != http.MethodPost || endpoint.Path == "" || !strings.HasPrefix(endpoint.Path, "/") ||
			strings.ContainsAny(endpoint.Path, "?#\\") || request.MediaType == "" || request.MaximumBytes <= 0 ||
			response.MediaType == "" || response.MaximumBytes <= 0 || response.Status < 200 || response.Status >= 300 {
			return false
		}
		if _, exists := seen[endpoint.Path]; exists {
			return false
		}
		seen[endpoint.Path] = struct{}{}
	}
	return true
}
