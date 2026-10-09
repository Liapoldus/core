package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const settingsPath = "/api/v2/settings"

func (server *Server) dispatchSettings(response http.ResponseWriter, request *http.Request, actor, requestID string) bool {
	if request.URL.Path != settingsPath {
		return false
	}
	if server.CoreSettings == nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return true
	}
	snapshot, err := server.CoreSettings.Read(request.Context())
	if err != nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return true
	}
	etag := `"core-settings-` + strconv.FormatInt(snapshot.DesiredRevision, 10) + `"`
	if request.Method == http.MethodPut {
		matches := request.Header.Values("If-Match")
		if len(matches) != 1 || matches[0] != etag {
			server.writeProblem(response, http.StatusPreconditionFailed, "core_settings_conflict", "Settings revision does not match.", requestID)
			return true
		}
		if request.Header.Get("Content-Type") != "application/json" {
			server.writeProblem(response, http.StatusUnsupportedMediaType, "invalid_request", "Expected application/json.", requestID)
			return true
		}
		raw, err := io.ReadAll(http.MaxBytesReader(response, request.Body, 262144))
		if err != nil || server.ValidateSettings == nil || server.ValidateSettings(raw) != nil {
			server.writeProblem(response, http.StatusUnprocessableEntity, "core_settings_invalid", "Settings failed validation.", requestID)
			return true
		}
		snapshot, err = server.CoreSettings.Update(request.Context(), snapshot.DesiredRevision, raw, actor)
		if err != nil {
			// The persistence adapter never returns document, SQL or paths.
			if strings.Contains(err.Error(), "revision conflict") {
				server.writeProblem(response, http.StatusPreconditionFailed, "core_settings_conflict", "Settings revision does not match.", requestID)
			} else {
				server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
			}
			return true
		}
		etag = `"core-settings-` + strconv.FormatInt(snapshot.DesiredRevision, 10) + `"`
	} else if request.Method != http.MethodGet {
		response.Header().Set("Allow", "GET, PUT")
		server.writeProblem(response, http.StatusMethodNotAllowed, "method_not_allowed", "Settings endpoint accepts GET and PUT.", requestID)
		return true
	}
	response.Header().Set("ETag", etag)
	server.writeJSON(response, http.StatusOK, map[string]any{"desiredRevision": snapshot.DesiredRevision, "effectiveRevision": snapshot.EffectiveRevision,
		"pending": snapshot.Pending, "restartRequired": snapshot.Pending, "settings": json.RawMessage(snapshot.DesiredDocument)})
	return true
}
