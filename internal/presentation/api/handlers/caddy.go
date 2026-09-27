package handlers

import (
	"io"
	"net/http"
	"strings"

	"github.com/Liapoldus/core/internal/domain/models"
)

func CaddyAdmin(deps CaddyDependencies, response http.ResponseWriter, request *http.Request, requestID, actor string) {
	if deps.AdminMutations == nil || deps.AdminWords.Paths.ManagementPrefix == "" {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	allowed := containsString(deps.AdminWords.Methods.ReadOnly, request.Method) || containsString(deps.AdminWords.Methods.Mutating, request.Method)
	if !allowed {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	path := request.URL.Path
	if !strings.HasPrefix(path, deps.AdminWords.Paths.ManagementPrefix) {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	suffix := strings.TrimPrefix(path, deps.AdminWords.Paths.ManagementPrefix)
	if suffix == "" || containsAnyString(suffix, deps.AdminWords.Paths.ForbiddenPathCharacters) || invalidCaddyPathSegment(suffix, deps.AdminWords.Paths.PathSeparator) {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	adminPath := deps.AdminWords.Paths.LeadingSlash + suffix
	if request.URL.RawQuery != "" {
		adminPath += deps.AdminWords.Paths.QuerySeparator + request.URL.RawQuery
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, deps.AdminWords.Limits.RequestBodyBytes+1))
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	if int64(len(body)) > deps.AdminWords.Limits.RequestBodyBytes {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ArtifactTooLarge, requestID)
		return
	}
	headers := make(map[string][]string)
	for _, name := range deps.AdminWords.Headers.ForwardRequest {
		if values := request.Header.Values(name); len(values) > 0 {
			headers[name] = append([]string(nil), values...)
		}
	}
	result, err := deps.AdminMutations.Handle(request.Context(), models.AdminMutationCommand{
		Request: models.CaddyAdminRequest{
			Method: request.Method, Path: adminPath, Headers: headers, Body: body,
		},
		Actor: actor, RequestID: requestID,
	})
	if err != nil || result.Status < http.StatusContinue || result.Status > http.StatusNetworkAuthenticationRequired {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	for _, name := range deps.AdminWords.Headers.ForwardResponse {
		for _, value := range result.Headers[name] {
			response.Header().Add(name, value)
		}
	}
	response.WriteHeader(result.Status)
	if request.Method != http.MethodHead {
		_, _ = response.Write(result.Body)
	}
}

func invalidCaddyPathSegment(value, separator string) bool {
	for _, segment := range strings.Split(value, separator) {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

func containsAnyString(value string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
