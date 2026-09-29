package api

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	pluginsdk "liapoldus.local/plugin-sdk/infrastructure"
)

var errInvalidPluginConfigurationPullServer = errors.New("invalid plugin configuration pull server")

func ServePluginConfigurationPull(listener net.Listener, handler http.Handler, tlsConfiguration *tls.Config) error {
	if listener == nil || handler == nil || tlsConfiguration == nil ||
		len(tlsConfiguration.Certificates) == 0 && tlsConfiguration.GetCertificate == nil ||
		tlsConfiguration.ClientAuth != tls.RequireAndVerifyClientCert || tlsConfiguration.ClientCAs == nil ||
		tlsConfiguration.MinVersion < tls.VersionTLS12 {
		return errInvalidPluginConfigurationPullServer
	}
	return http.Serve(tls.NewListener(listener, tlsConfiguration.Clone()), handler)
}

func NewPluginConfigurationPullHandler(store interfaces.PluginConfigurationStore, resolve interfaces.PluginReplicaIdentityResolver) (http.Handler, error) {
	contract, err := pluginsdk.LoadHTTPContract()
	management, managementErr := config.LoadManagement()
	slotWords, slotErr := config.LoadPluginConfiguration()
	if err != nil || managementErr != nil || slotErr != nil || store == nil || resolve == nil {
		return nil, pluginsdk.ErrInvalidHTTPContract
	}
	contentTypeHeader := management.Headers.ContentType
	failureMediaType := contract.Core.ConfigPull.ResponseMediaType
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != contract.Core.ConfigPull.Method {
			http.NotFound(response, request)
			return
		}
		generation, ok := configurationGeneration(request.URL.Path, contract.Core.ConfigPull.PathTemplate)
		if !ok {
			writeConfigurationPullFailure(response, contentTypeHeader, failureMediaType, contract.Errors["invalidRequest"].Status, contract.Errors["invalidRequest"].Code)
			return
		}
		if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 || len(request.TLS.VerifiedChains) == 0 {
			writeConfigurationPullFailure(response, contentTypeHeader, failureMediaType, contract.Errors["configurationUnavailable"].Status, contract.Errors["configurationUnavailable"].Code)
			return
		}
		instanceID, authorized := resolve(request.TLS.PeerCertificates[0])
		if !authorized || instanceID == "" {
			writeConfigurationPullFailure(response, contentTypeHeader, failureMediaType, contract.Errors["configurationUnavailable"].Status, contract.Errors["configurationUnavailable"].Code)
			return
		}
		revision, pointers, err := store.Current(request.Context(), instanceID)
		state, retained := retainedConfigurationState(revision.Revision, pointers, generation, slotWords.Slots.Active, slotWords.Slots.Previous)
		if err != nil || !retained {
			writeConfigurationPullFailure(response, contentTypeHeader, failureMediaType, contract.Errors["configurationUnavailable"].Status, contract.Errors["configurationUnavailable"].Code)
			return
		}
		exact, err := store.GetRevision(request.Context(), instanceID, generation)
		if err != nil || int64(len(exact.SettingsJSON)) > contract.Core.ConfigPull.MaximumBytes {
			writeConfigurationPullFailure(response, contentTypeHeader, failureMediaType, contract.Errors["configurationUnavailable"].Status, contract.Errors["configurationUnavailable"].Code)
			return
		}
		headers := contract.Core.ConfigPull.ResponseHeaders
		response.Header().Set(contentTypeHeader, contract.Core.ConfigPull.ResponseMediaType)
		response.Header().Set(headers["generation"], strconv.FormatInt(exact.Revision, 10))
		response.Header().Set(headers["schemaVersion"], strconv.FormatInt(exact.SchemaVersion, 10))
		response.Header().Set(headers["sha256"], exact.Digest)
		response.Header().Set(headers["generationState"], state)
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write(exact.SettingsJSON)
	}), nil
}

func configurationGeneration(path, template string) (int64, bool) {
	placeholder := "{generation}"
	if !strings.Contains(template, placeholder) {
		return 0, false
	}
	parts := strings.SplitN(template, placeholder, 2)
	if !strings.HasPrefix(path, parts[0]) || !strings.HasSuffix(path, parts[1]) {
		return 0, false
	}
	value := strings.TrimSuffix(strings.TrimPrefix(path, parts[0]), parts[1])
	if strings.Contains(value, "/") {
		return 0, false
	}
	generation, err := strconv.ParseInt(value, 10, 64)
	return generation, err == nil && generation > 0 && strconv.FormatInt(generation, 10) == value
}

// retainedConfigurationState reports the published generation state Core will
// serve for a generation. Only the two retained slots are published: a
// candidate is validated and promoted inside one transaction before replicas
// are notified, so a pending candidate is never a pullable generation.
func retainedConfigurationState(active int64, pointers models.PluginConfigurationPointers, generation int64, activeWord, previousWord string) (string, bool) {
	switch generation {
	case active:
		return activeWord, true
	case pointers.PreviousRevision:
		return previousWord, true
	default:
		return "", false
	}
}

func writeConfigurationPullFailure(response http.ResponseWriter, headerName, contentType string, status int, code string) {
	writeJSONResponse(response, status, headerName, contentType, map[string]string{"code": code})
}
