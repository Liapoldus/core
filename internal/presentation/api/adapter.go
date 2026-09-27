// Package api adapts the Management API to application use cases.
package api

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/internal/infrastructure/security"
	"golang.org/x/crypto/bcrypt"
)

type Server struct {
	Token                    string
	ServiceAccounts          []models.ServiceAccount
	mu                       sync.RWMutex
	Operations               application.OperationService
	AdminSurfaces            []AdminSurface
	AdminDispatcher          *plugins.Dispatcher
	Plugins                  []any
	PluginIDField            string
	RestartPlugin            func(context.Context, string) (models.Operation, error)
	Audit                    *application.AuditService
	GroupService             application.GroupService
	GroupReleases            *application.GroupReleaseService
	CookiePolicies           *application.PluginCookiePolicyService
	GroupReleasePolicy       models.GroupReleasePolicy
	AdminMutations           *application.AdminMutationService
	AdminWords               config.AdminMutationWords
	AccessService            *application.AccessService
	CaddyVariant             string
	CaddyBuildID             string
	CaddyModules             []string
	DataPlaneState           string
	DataPlaneReason          string
	DataPlaneReadiness       func(context.Context) (string, string)
	AuditWords               config.AuditWords
	Management               config.ManagementWords
	Errors                   config.ErrorCatalog
	TLSConfig                *tls.Config
	RequireClientCertificate bool
	contractOnce             sync.Once
}

type AdminSurface struct {
	Plugin       string   `json:"plugin"`
	Namespace    string   `json:"namespace"`
	Version      string   `json:"version"`
	Title        string   `json:"title"`
	Capabilities []string `json:"capabilities"`
}

func (server *Server) Handler() http.Handler {
	server.contractOnce.Do(func() {
		if server.Management.Paths.Groups == "" {
			server.Management, _ = config.LoadManagement()
		}
		server.Errors, _ = config.LoadErrorCatalog()
		if server.AdminWords.Paths.ManagementPrefix == "" {
			server.AdminWords, _ = config.LoadAdminMutation()
		}
		if server.GroupReleasePolicy.OperationKind == "" {
			server.GroupReleasePolicy, _ = config.LoadGroupRelease()
		}
	})
	return http.HandlerFunc(server.handle)
}

func (server *Server) Listen(ctx context.Context, address string) error {
	httpServer := &http.Server{Addr: address, Handler: server.Handler()}
	result := make(chan error, 1)
	go func() {
		if server.TLSConfig != nil {
			httpServer.TLSConfig = server.TLSConfig
			result <- httpServer.ListenAndServeTLS("", "")
			return
		}
		result <- httpServer.ListenAndServe()
	}()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownContext)
	}
}
func (server *Server) handle(response http.ResponseWriter, request *http.Request) {
	requestID := "req_" + randomID()
	response.Header().Set(server.Management.Headers.RequestID, requestID)
	if request.URL.Path == server.Management.Paths.Healthz {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			server.writeProblem(response, http.StatusMethodNotAllowed, "method_not_allowed", "health endpoint accepts GET and HEAD", requestID)
			return
		}
		server.writeJSON(response, http.StatusOK, map[string]any{server.Management.JSON.Status: server.Management.Statuses.OK, server.Management.JSON.RequestID: requestID})
		return
	}
	if server.RequireClientCertificate && (request.TLS == nil || len(request.TLS.PeerCertificates) == 0) {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementMTLSRequired, requestID)
		return
	}
	actor, authorized, authErr := server.authenticate(request.Context(), request.Header.Get("Authorization"))
	if authErr != nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	if !authorized {
		server.writeCatalogProblem(response, server.Management.Codes.BearerRequired, requestID)
		return
	}
	path := strings.TrimSuffix(request.URL.Path, "/")
	switch {
	case path == server.Management.Paths.Status && request.Method == http.MethodGet:
		server.mu.RLock()
		state, reason := server.DataPlaneState, server.DataPlaneReason
		provider := server.DataPlaneReadiness
		server.mu.RUnlock()
		if provider != nil {
			state, reason = provider(request.Context())
		}
		readiness := map[string]any{server.Management.JSON.State: state}
		if state == server.Management.Statuses.NotReady {
			readiness[server.Management.JSON.Reason] = reason
		}
		server.writeJSON(response, 200, map[string]any{
			server.Management.JSON.Caddy: map[string]any{
				server.Management.JSON.Variant: server.CaddyVariant,
				server.Management.JSON.BuildID: server.CaddyBuildID,
				server.Management.JSON.Modules: server.CaddyModules,
			},
			server.Management.JSON.Drift:              false,
			server.Management.JSON.DataPlaneReadiness: readiness,
			server.Management.JSON.RequestID:          requestID,
		})
	case strings.HasPrefix(request.URL.Path, server.AdminWords.Paths.ManagementPrefix):
		server.handleCaddyAdmin(response, request, requestID, actor)
	case path == server.Management.Paths.ServiceKeys && request.Method == server.Management.Methods.Post:
		server.handleServiceKeyCreate(response, request, requestID, actor)
	case path == server.Management.Paths.ServiceKeys && request.Method == server.Management.Methods.Get:
		server.handleServiceKeyList(response, request, requestID)
	case path == server.Management.Paths.Groups && request.Method == server.Management.Methods.Get:
		server.handleGroupList(response, request, requestID)
	case path == server.Management.Paths.Groups && request.Method == server.Management.Methods.Post:
		server.handleGroupCreate(response, request, requestID, actor)
	case server.isPluginCookiePolicyPath(path) && (request.Method == server.Management.Methods.Get || request.Method == server.Management.Methods.Put):
		server.handlePluginCookiePolicy(response, request, path, requestID, actor)
	case strings.HasPrefix(path, server.Management.Paths.GroupByID) && strings.HasSuffix(path, server.Management.Paths.GroupReleases) && request.Method == server.Management.Methods.Post:
		server.handleGroupPublish(response, request, path, requestID, actor)
	case strings.HasPrefix(path, server.Management.Paths.GroupByID) && strings.HasSuffix(path, server.Management.Paths.GroupReleases) && request.Method == server.Management.Methods.Get:
		server.handleGroupReleases(response, request, path, requestID)
	case strings.HasPrefix(path, server.Management.Paths.GroupByID) && strings.Contains(path, server.Management.Paths.GroupReleases+server.Management.Paths.GroupIDSeparator) && request.Method == server.Management.Methods.Get:
		server.handleGroupRelease(response, request, path, requestID)
	case strings.HasPrefix(path, server.Management.Paths.GroupByID) && strings.HasSuffix(path, server.Management.Paths.GroupIDSeparator+server.Management.Paths.GroupRollback) && request.Method == server.Management.Methods.Post:
		server.handleGroupRollback(response, request, path, requestID, actor)
	case strings.HasPrefix(path, server.Management.Paths.GroupByID) && request.Method == server.Management.Methods.Get:
		server.handleGroupGet(response, request, path, requestID)
	case path == server.Management.Paths.Plugins && request.Method == http.MethodGet:
		server.writePage(response, server.Plugins, request, requestID)
	case path == server.Management.Paths.AdminSurfaces && request.Method == http.MethodGet:
		server.mu.RLock()
		surfaces := append([]AdminSurface(nil), server.AdminSurfaces...)
		server.mu.RUnlock()
		server.writeJSON(response, 200, map[string]any{server.Management.JSON.Items: surfaces, server.Management.JSON.RequestID: requestID})
	case server.isPluginDetailPath(path) && request.Method == server.Management.Methods.Get:
		server.handlePluginDetail(response, path, requestID)
	case path == server.Management.Paths.Audit && request.Method == http.MethodGet:
		if server.Audit == nil {
			server.writeJSON(response, http.StatusOK, map[string]any{server.Management.JSON.Items: []models.AuditRecord{}, server.Management.JSON.NextCursor: nil, server.Management.JSON.RequestID: requestID})
			return
		}
		limit := server.Management.Pagination.LimitDefault
		if rawLimit := request.URL.Query().Get(server.Management.JSON.Limit); rawLimit != "" {
			parsed, err := strconv.Atoi(rawLimit)
			if err != nil {
				server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
				return
			}
			limit = parsed
		}
		page, err := server.Audit.Records(request.Context(), request.URL.Query().Get(server.Management.JSON.Cursor), limit)
		if err != nil {
			var pageError models.AuditPageError
			if errors.As(err, &pageError) {
				server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
				return
			}
			server.writeProblem(response, http.StatusServiceUnavailable, server.AuditWords.Audit.StorageUnavailable.Code, server.AuditWords.Audit.StorageUnavailable.Detail, requestID)
			return
		}
		server.writeJSON(response, http.StatusOK, map[string]any{server.Management.JSON.Items: page.Items, server.Management.JSON.NextCursor: page.NextCursor, server.Management.JSON.RequestID: requestID})
	case strings.HasPrefix(path, server.Management.Paths.Plugins+"/") && strings.Contains(path, "/"+server.Management.Paths.AdminPages+"/") && (request.Method == http.MethodGet || request.Method == http.MethodPost):
		server.handlePluginAdmin(response, request, path, requestID)
	case strings.HasPrefix(path, server.Management.Paths.Plugins+"/") && strings.HasSuffix(path, "/"+server.Management.Paths.Restart) && request.Method == http.MethodPost:
		if server.RestartPlugin == nil {
			server.writeProblem(response, 501, "not_implemented", "plugin restart is unavailable", requestID)
			return
		}
		instance := strings.TrimSuffix(strings.TrimPrefix(path, server.Management.Paths.Plugins+"/"), "/"+server.Management.Paths.Restart)
		if len(instance) == 0 || strings.Contains(instance, "/") {
			server.writeProblem(response, http.StatusNotFound, "not_found", "plugin resource not found", requestID)
			return
		}
		if server.Operations.Store == nil {
			server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
			return
		}
		op, err := server.RestartPlugin(request.Context(), instance)
		if err != nil {
			server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
			return
		}
		if op.ID == "" || op.Kind == "" || (op.State != server.Management.Statuses.Pending && op.State != server.Management.Statuses.Running) {
			server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
			return
		}
		op.RequestID = requestID
		op.Actor = actor
		op.Resource = instance
		if op.CreatedAt.IsZero() {
			op.CreatedAt = time.Now().UTC()
		}
		if err := server.Operations.Create(request.Context(), op); err != nil {
			server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
			return
		}
		server.writeJSON(response, http.StatusAccepted, map[string]any{
			server.Management.JSON.OperationID: op.ID,
			server.Management.JSON.State:       op.State,
			server.Management.JSON.RequestID:   requestID,
		})
	case strings.HasPrefix(path, server.Management.Paths.Operations+"/") && request.Method == http.MethodGet:
		id := strings.TrimPrefix(path, server.Management.Paths.Operations+"/")
		if server.Operations.Store == nil {
			server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
			return
		}
		operation, err := server.Operations.Get(request.Context(), id)
		if err != nil {
			var notFound models.OperationNotFound
			if errors.As(err, &notFound) {
				server.writeProblem(response, http.StatusNotFound, server.Management.Codes.OperationNotFound, server.Management.Diagnostics.OperationNotFound, requestID)
				return
			}
			server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
			return
		}
		result := map[string]any{
			server.Management.JSON.ID:        operation.ID,
			server.Management.JSON.Kind:      operation.Kind,
			server.Management.JSON.State:     operation.State,
			server.Management.JSON.CreatedAt: operation.CreatedAt,
			server.Management.JSON.RequestID: operation.RequestID,
		}
		if operation.UpdatedAt != nil {
			result[server.Management.JSON.UpdatedAt] = *operation.UpdatedAt
		}
		server.writeJSON(response, http.StatusOK, result)
	default:
		server.writeProblem(response, 404, "not_found", "resource not found", requestID)
	}
}

func (server *Server) handleServiceKeyList(response http.ResponseWriter, request *http.Request, requestID string) {
	if server.AccessService == nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	metadata, err := server.AccessService.Metadata(request.Context())
	if err != nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	items := make([]map[string]any, 0, len(metadata))
	for _, item := range metadata {
		items = append(items, map[string]any{
			server.Management.JSON.ID:        item.ID,
			server.Management.JSON.Name:      item.Name,
			server.Management.JSON.Role:      item.Role,
			server.Management.JSON.CreatedAt: item.CreatedAt,
			server.Management.JSON.ExpiresAt: item.ExpiresAt,
			server.Management.JSON.RevokedAt: item.RevokedAt,
		})
	}
	server.writeJSON(response, http.StatusOK, map[string]any{
		server.Management.JSON.Items:     items,
		server.Management.JSON.RequestID: requestID,
	})
}

func (server *Server) handleCaddyAdmin(response http.ResponseWriter, request *http.Request, requestID, actor string) {
	if server.AdminMutations == nil || server.AdminWords.Paths.ManagementPrefix == "" {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	allowed := containsString(server.AdminWords.Methods.ReadOnly, request.Method) || containsString(server.AdminWords.Methods.Mutating, request.Method)
	if !allowed {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
		return
	}
	path := request.URL.Path
	if !strings.HasPrefix(path, server.AdminWords.Paths.ManagementPrefix) {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
		return
	}
	suffix := strings.TrimPrefix(path, server.AdminWords.Paths.ManagementPrefix)
	if suffix == "" || containsAnyString(suffix, server.AdminWords.Paths.ForbiddenPathCharacters) || invalidCaddyPathSegment(suffix, server.AdminWords.Paths.PathSeparator) {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
		return
	}
	adminPath := server.AdminWords.Paths.LeadingSlash + suffix
	if request.URL.RawQuery != "" {
		adminPath += server.AdminWords.Paths.QuerySeparator + request.URL.RawQuery
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, server.AdminWords.Limits.RequestBodyBytes+1))
	if err != nil {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
		return
	}
	if int64(len(body)) > server.AdminWords.Limits.RequestBodyBytes {
		server.writeCatalogProblem(response, server.Management.Codes.ArtifactTooLarge, requestID)
		return
	}
	headers := make(map[string][]string)
	for _, name := range server.AdminWords.Headers.ForwardRequest {
		if values := request.Header.Values(name); len(values) > 0 {
			headers[name] = append([]string(nil), values...)
		}
	}
	result, err := server.AdminMutations.Handle(request.Context(), models.AdminMutationCommand{
		Request: models.CaddyAdminRequest{
			Method: request.Method, Path: adminPath, Headers: headers, Body: body,
		},
		Actor: actor, RequestID: requestID,
	})
	if err != nil || result.Status < http.StatusContinue || result.Status > http.StatusNetworkAuthenticationRequired {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	for _, name := range server.AdminWords.Headers.ForwardResponse {
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

func (server *Server) handleServiceKeyCreate(response http.ResponseWriter, request *http.Request, requestID, actor string) {
	if server.AccessService == nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	fields := make(map[string]json.RawMessage)
	decoder := json.NewDecoder(request.Body)
	if err := decoder.Decode(&fields); err != nil || len(fields) != 1 {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
		return
	}
	rawName, exists := fields[server.Management.JSON.Name]
	if !exists {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
		return
	}
	var name string
	if err := json.Unmarshal(rawName, &name); err != nil || utf8.RuneCountInString(name) < server.Management.ServiceKeys.NameMinLength || utf8.RuneCountInString(name) > server.Management.ServiceKeys.NameMaxLength {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
		return
	}
	words, err := config.LoadCLI()
	if err != nil || words.ServiceKey.KeyBytes < 1 || words.ServiceKey.HashCost < 1 || words.ServiceKey.RolePlatformAdmin == "" || server.Management.ServiceKeys.CreatedStatus < 1 {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	id, token, verifier, err := security.GenerateServiceKey(words.ServiceKey.KeyBytes, words.ServiceKey.HashCost)
	if err != nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	defer clear(verifier)
	record := models.AuditRecord{
		Actor: actor, Action: server.AuditWords.Audit.Actions.ServiceKeyCreate,
		Resource: server.AuditWords.Audit.Resources.ServiceKeys,
		Result:   server.AuditWords.Audit.Results.Succeeded, RequestID: requestID,
	}
	if err := server.AccessService.Create(request.Context(), models.ServiceKey{
		ID: id, Name: name, Verifier: verifier, Role: words.ServiceKey.RolePlatformAdmin,
	}, record); err != nil {
		var auditFailure models.AuditAppendError
		if errors.As(err, &auditFailure) {
			server.writeProblem(response, http.StatusServiceUnavailable, server.AuditWords.Audit.StorageUnavailable.Code, server.AuditWords.Audit.StorageUnavailable.Detail, requestID)
			return
		}
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	server.writeJSON(response, server.Management.ServiceKeys.CreatedStatus, map[string]any{
		server.Management.JSON.ID:        id,
		server.Management.JSON.Name:      name,
		server.Management.JSON.Role:      words.ServiceKey.RolePlatformAdmin,
		server.Management.JSON.Token:     token,
		server.Management.JSON.RequestID: requestID,
	})
}

type pluginCookiePolicyInput struct {
	AllowedNames []string `json:"allowedNames"`
}

func (server *Server) isPluginDetailPath(path string) bool {
	separator := server.Management.Paths.GroupIDSeparator
	if server.Management.Paths.Plugins == "" || separator == "" {
		return false
	}
	prefix := server.Management.Paths.Plugins + separator
	instanceID := strings.TrimPrefix(path, prefix)
	return instanceID != path && instanceID != "" && !strings.Contains(instanceID, separator)
}

func (server *Server) handlePluginDetail(response http.ResponseWriter, path, requestID string) {
	separator := server.Management.Paths.GroupIDSeparator
	instanceID := strings.TrimPrefix(path, server.Management.Paths.Plugins+separator)
	if server.PluginIDField == "" {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}

	var matched map[string]any
	server.mu.RLock()
	for _, item := range server.Plugins {
		plugin, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if candidateID, ok := plugin[server.PluginIDField].(string); ok && candidateID == instanceID {
			matched = plugin
			break
		}
	}
	server.mu.RUnlock()
	if matched == nil {
		server.writeCatalogProblem(response, server.Management.Codes.PluginNotFound, requestID)
		return
	}
	server.writeJSON(response, http.StatusOK, matched)
}

func (server *Server) isPluginCookiePolicyPath(path string) bool {
	separator := server.Management.Paths.GroupIDSeparator
	if separator == "" || server.Management.Paths.PluginCookiePolicies == "" || server.Management.Paths.CookiePoliciesSuffix == "" {
		return false
	}
	prefix := strings.TrimSuffix(server.Management.Paths.PluginCookiePolicies, separator) + separator
	rest := strings.TrimPrefix(path, prefix)
	if rest == path {
		return false
	}
	suffix := server.Management.Paths.CookiePoliciesSuffix
	index := strings.Index(rest, suffix)
	if index <= 0 || !strings.HasPrefix(rest[index:], suffix) {
		return false
	}
	capability := rest[index+len(suffix):]
	return capability != "" && !strings.Contains(capability, separator) && !strings.Contains(rest[:index], separator)
}

func (server *Server) handlePluginCookiePolicy(response http.ResponseWriter, request *http.Request, path, requestID, actor string) {
	if server.CookiePolicies == nil {
		server.writeCatalogProblem(response, server.Management.Codes.CookiePolicyUnavailable, requestID)
		return
	}
	instanceID, capability, ok := server.pluginCookiePolicyResource(path)
	if !ok {
		server.writeCatalogProblem(response, server.Management.Codes.CookiePolicyNotFound, requestID)
		return
	}
	if request.Method == server.Management.Methods.Get {
		policy, err := server.CookiePolicies.Get(request.Context(), instanceID, capability)
		if err != nil {
			server.writeCookiePolicyFailure(response, err, requestID)
			return
		}
		response.Header().Set(server.Management.Headers.ETag, cookiePolicyETag(policy.Revision))
		server.writeJSON(response, http.StatusOK, policy)
		return
	}
	rawETag := request.Header.Get(server.Management.Headers.IfMatch)
	if rawETag == "" {
		server.writeCatalogProblem(response, server.Management.Codes.CookiePolicyPreconditionRequired, requestID)
		return
	}
	expected, valid := parseCookiePolicyETag(rawETag)
	if !valid {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, server.Management.CookiePolicy.MaximumBodyBytes))
	decoder.DisallowUnknownFields()
	var input pluginCookiePolicyInput
	if err := decoder.Decode(&input); err != nil || input.AllowedNames == nil || decoder.Decode(&struct{}{}) != io.EOF {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidCookiePolicy, requestID)
		return
	}
	policy := plugins.CookiePolicy{
		Version: server.Management.CookiePolicy.Version, InstanceID: instanceID,
		Capability: capability, AllowedNames: input.AllowedNames,
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	validated, err := plugins.DecodeCookiePolicy(encoded)
	if err != nil {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidCookiePolicy, requestID)
		return
	}
	result, err := server.CookiePolicies.Replace(request.Context(), expected, models.PluginCookiePolicy{
		InstanceID: validated.InstanceID, Capability: validated.Capability, AllowedNames: validated.AllowedNames,
	}, actor, requestID)
	if err != nil {
		server.writeCookiePolicyFailure(response, err, requestID)
		return
	}
	response.Header().Set(server.Management.Headers.ETag, cookiePolicyETag(result.Revision))
	server.writeJSON(response, http.StatusOK, result)
}

func (server *Server) pluginCookiePolicyResource(path string) (string, string, bool) {
	separator := server.Management.Paths.GroupIDSeparator
	prefix := strings.TrimSuffix(server.Management.Paths.PluginCookiePolicies, separator) + separator
	rest := strings.TrimPrefix(path, prefix)
	if rest == path {
		return "", "", false
	}
	suffix := server.Management.Paths.CookiePoliciesSuffix
	index := strings.Index(rest, suffix)
	if index <= 0 || !strings.HasPrefix(rest[index:], suffix) {
		return "", "", false
	}
	instanceID, capability := rest[:index], rest[index+len(suffix):]
	if capability == "" || strings.Contains(capability, separator) || strings.Contains(instanceID, separator) {
		return "", "", false
	}
	return instanceID, capability, true
}

func cookiePolicyETag(revision int64) string {
	return strconv.Quote(strconv.FormatInt(revision, 10))
}

func parseCookiePolicyETag(value string) (int64, bool) {
	decoded, err := strconv.Unquote(value)
	if err != nil || decoded == "" {
		return 0, false
	}
	revision, err := strconv.ParseInt(decoded, 10, 64)
	return revision, err == nil && revision >= 0 && strconv.FormatInt(revision, 10) == decoded
}

func (server *Server) writeCookiePolicyFailure(response http.ResponseWriter, err error, requestID string) {
	var notFound models.PluginCookiePolicyNotFound
	var conflict models.PluginCookiePolicyRevisionConflict
	var invalid models.PluginCookiePolicyValidationError
	var unavailable models.PluginCookiePolicyUnavailable
	switch {
	case errors.As(err, &notFound):
		server.writeCatalogProblem(response, server.Management.Codes.CookiePolicyNotFound, requestID)
	case errors.As(err, &conflict):
		server.writeCatalogProblem(response, server.Management.Codes.CookiePolicyRevisionConflict, requestID)
	case errors.As(err, &invalid):
		server.writeCatalogProblem(response, server.Management.Codes.InvalidCookiePolicy, requestID)
	case errors.As(err, &unavailable):
		server.writeCatalogProblem(response, server.Management.Codes.CookiePolicyUnavailable, requestID)
	default:
		server.writeCatalogProblem(response, server.Management.Codes.CookiePolicyUnavailable, requestID)
	}
}

func (server *Server) handleGroupList(response http.ResponseWriter, request *http.Request, requestID string) {
	if server.GroupService.Store == nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	groups, err := server.GroupService.List(request.Context())
	if err != nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	items := make([]map[string]any, 0, len(groups.Items))
	for _, group := range groups.Items {
		items = append(items, server.groupResponse(group))
	}
	server.writeJSON(response, http.StatusOK, map[string]any{
		server.Management.JSON.Items:     items,
		server.Management.JSON.RequestID: requestID,
	})
}

func (server *Server) handleGroupCreate(response http.ResponseWriter, request *http.Request, requestID, actor string) {
	fail := func(code string) {
		if err := server.recordAudit(request.Context(), actor, server.AuditWords.Audit.Actions.GroupCreate, server.AuditWords.Audit.Resources.Groups, server.AuditWords.Audit.Results.Failed, requestID, "", ""); err != nil {
			server.writeProblem(response, http.StatusServiceUnavailable, server.AuditWords.Audit.StorageUnavailable.Code, server.AuditWords.Audit.StorageUnavailable.Detail, requestID)
			return
		}
		server.writeCatalogProblem(response, code, requestID)
	}
	decoder := json.NewDecoder(request.Body)
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		fail(server.Management.Codes.InvalidRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		fail(server.Management.Codes.InvalidRequest)
		return
	}
	if len(fields) != 2 {
		fail(server.Management.Codes.InvalidRequest)
		return
	}
	for key := range fields {
		if key != server.Management.JSON.ID && key != server.Management.JSON.IdempotencyKey {
			fail(server.Management.Codes.InvalidRequest)
			return
		}
	}
	var id, key string
	if json.Unmarshal(fields[server.Management.JSON.ID], &id) != nil || json.Unmarshal(fields[server.Management.JSON.IdempotencyKey], &key) != nil || len(key) < server.Management.Idempotency.KeyMin || len(key) > server.Management.Idempotency.KeyChars || !ascii(key) {
		fail(server.Management.Codes.InvalidRequest)
		return
	}
	validID, err := regexp.MatchString(server.Management.Paths.GroupIDPattern, id)
	if err != nil {
		fail(server.Management.Codes.ManagementUnavailable)
		return
	}
	if !validID {
		fail(server.Management.Codes.InvalidRequest)
		return
	}
	if server.GroupService.Store == nil {
		fail(server.Management.Codes.ManagementUnavailable)
		return
	}
	auditRecord := models.AuditRecord{
		Actor:     actor,
		Action:    server.AuditWords.Audit.Actions.GroupCreate,
		Resource:  server.AuditWords.Audit.Resources.Groups,
		Result:    server.AuditWords.Audit.Results.Succeeded,
		RequestID: requestID,
	}
	group, err := server.GroupService.Create(request.Context(), id, auditRecord)
	if err != nil {
		var auditFailure models.AuditAppendError
		if errors.As(err, &auditFailure) {
			server.writeProblem(response, http.StatusServiceUnavailable, server.AuditWords.Audit.StorageUnavailable.Code, server.AuditWords.Audit.StorageUnavailable.Detail, requestID)
			return
		}
		var exists models.GroupAlreadyExists
		if errors.As(err, &exists) {
			fail(server.Management.Codes.GroupAlreadyExists)
			return
		}
		fail(server.Management.Codes.ManagementUnavailable)
		return
	}
	response.Header().Set(server.Management.Headers.Location, server.Management.Paths.GroupByID+id)
	result := server.groupResponse(group)
	result[server.Management.JSON.RequestID] = requestID
	server.writeJSON(response, http.StatusCreated, result)
}

func (server *Server) handleGroupGet(response http.ResponseWriter, request *http.Request, path, requestID string) {
	if server.GroupService.Store == nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	id := strings.TrimPrefix(path, server.Management.Paths.GroupByID)
	if id == "" || strings.Contains(id, server.Management.Paths.GroupIDSeparator) {
		server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
		return
	}
	group, err := server.GroupService.Get(request.Context(), id)
	if err != nil {
		var notFound models.GroupNotFound
		if errors.As(err, &notFound) {
			server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
			return
		}
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	result := server.groupResponse(group)
	result[server.Management.JSON.RequestID] = requestID
	server.writeJSON(response, http.StatusOK, result)
}

func (server *Server) handleGroupReleases(response http.ResponseWriter, request *http.Request, path, requestID string) {
	if server.GroupService.Store == nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	suffix := server.Management.Paths.GroupIDSeparator + server.Management.Paths.GroupReleases
	groupID := strings.TrimSuffix(strings.TrimPrefix(path, server.Management.Paths.GroupByID), suffix)
	if groupID == "" || strings.Contains(groupID, server.Management.Paths.GroupIDSeparator) {
		server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
		return
	}
	limit := 0
	if rawLimit := request.URL.Query().Get(server.Management.JSON.Limit); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
			return
		}
		limit = parsed
	}
	page, err := server.GroupService.ListRevisions(request.Context(), groupID, request.URL.Query().Get(server.Management.JSON.Cursor), limit)
	if err != nil {
		var notFound models.GroupNotFound
		if errors.As(err, &notFound) {
			server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
			return
		}
		var invalidPage models.GroupRevisionPageError
		if errors.As(err, &invalidPage) {
			server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
			return
		}
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, revision := range page.Items {
		items = append(items, map[string]any{
			server.Management.JSON.ID:              revision.ID,
			server.Management.JSON.GroupID:         revision.GroupID,
			server.Management.JSON.CaddyfileDigest: revision.CaddyfileDigest,
			server.Management.JSON.ArtifactDigest:  revision.ArtifactDigest,
			server.Management.JSON.CreatedAt:       revision.CreatedAt,
			server.Management.JSON.Actor:           revision.Actor,
		})
	}
	server.writeJSON(response, http.StatusOK, map[string]any{
		server.Management.JSON.Items:      items,
		server.Management.JSON.NextCursor: page.NextCursor,
		server.Management.JSON.RequestID:  requestID,
	})
}

func (server *Server) handleGroupRelease(response http.ResponseWriter, request *http.Request, path, requestID string) {
	if server.GroupService.Store == nil || server.GroupService.ContentReader == nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	suffix := server.Management.Paths.GroupIDSeparator + server.Management.Paths.GroupReleases + server.Management.Paths.GroupIDSeparator
	resource := strings.TrimPrefix(path, server.Management.Paths.GroupByID)
	groupID, revisionID, found := strings.Cut(resource, suffix)
	if !found || groupID == "" || revisionID == "" || strings.Contains(revisionID, server.Management.Paths.GroupIDSeparator) {
		server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
		return
	}
	detail, err := server.GroupService.GetRevisionDetail(request.Context(), groupID, revisionID)
	if err != nil {
		var notFound models.GroupRevisionNotFound
		if errors.As(err, &notFound) {
			server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
			return
		}
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	revision := detail.Revision
	frontends := make([]map[string]any, 0, len(detail.Frontends))
	for _, frontend := range detail.Frontends {
		frontends = append(frontends, map[string]any{
			server.Management.JSON.ID:     frontend.ID,
			server.Management.JSON.Digest: frontend.Digest,
			server.Management.JSON.Files:  frontend.Files,
		})
	}
	server.writeJSON(response, http.StatusOK, map[string]any{
		server.Management.JSON.ID:              revision.ID,
		server.Management.JSON.GroupID:         revision.GroupID,
		server.Management.JSON.Caddyfile:       detail.Caddyfile,
		server.Management.JSON.CaddyfileDigest: revision.CaddyfileDigest,
		server.Management.JSON.ArtifactDigest:  revision.ArtifactDigest,
		server.Management.JSON.Frontends:       frontends,
		server.Management.JSON.CreatedAt:       revision.CreatedAt,
		server.Management.JSON.Actor:           revision.Actor,
		server.Management.JSON.RequestID:       requestID,
	})
}

func (server *Server) handleGroupPublish(response http.ResponseWriter, request *http.Request, path, requestID, actor string) {
	if server.GroupReleases == nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	groupID := strings.TrimSuffix(strings.TrimPrefix(path, server.Management.Paths.GroupByID), server.Management.Paths.GroupIDSeparator+server.Management.Paths.GroupReleases)
	if groupID == "" || strings.Contains(groupID, server.Management.Paths.GroupIDSeparator) {
		server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
		return
	}
	contentType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != server.GroupReleasePolicy.MultipartContentType || parameters["boundary"] == "" {
		server.writeCatalogProblem(response, server.GroupReleasePolicy.InvalidRequestCode, requestID)
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, server.GroupReleasePolicy.RequestLimitBytes)
	metadata, caddyfile, artifact, err := readGroupReleaseMultipart(multipart.NewReader(request.Body, parameters["boundary"]), server.GroupReleasePolicy, server.Management)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.ArtifactTooLargeCode, requestID)
			return
		}
		var archiveError models.GroupReleaseArchiveError
		if errors.As(err, &archiveError) {
			if archiveError.TooLarge {
				server.writeCatalogProblem(response, server.GroupReleasePolicy.ArtifactTooLargeCode, requestID)
				return
			}
			server.writeCatalogProblem(response, server.GroupReleasePolicy.ArtifactInvalidCode, requestID)
			return
		}
		server.writeCatalogProblem(response, server.GroupReleasePolicy.InvalidRequestCode, requestID)
		return
	}
	operation, _, err := server.GroupReleases.Accept(request.Context(), models.GroupReleaseCommand{
		GroupID: groupID, Actor: actor, RequestID: requestID,
		IdempotencyKey:          metadata.IdempotencyKey,
		IdempotencyScope:        server.GroupReleasePolicy.ScopePrefix + groupID + server.GroupReleasePolicy.ScopeSuffix,
		ExpectedCurrentRevision: metadata.ExpectedCurrentRevision,
		IdempotencyWindow:       server.GroupReleasePolicy.IdempotencyWindow,
		Caddyfile:               caddyfile, Artifact: artifact,
	})
	if err != nil {
		var driftBlocked models.GroupDriftBlocked
		if errors.As(err, &driftBlocked) {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.DriftBlockedCode, requestID)
			return
		}
		var conflict models.GroupRevisionConflict
		if errors.As(err, &conflict) {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.RevisionConflictCode, requestID)
			return
		}
		var idempotencyConflict models.IdempotencyConflict
		if errors.As(err, &idempotencyConflict) {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.IdempotencyConflictCode, requestID)
			return
		}
		var groupNotFound models.GroupNotFound
		if errors.As(err, &groupNotFound) {
			server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
			return
		}
		var validation models.GroupReleaseValidationError
		if errors.As(err, &validation) {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.CaddyAdaptFailedCode, requestID)
			return
		}
		var archiveError models.GroupReleaseArchiveError
		if errors.As(err, &archiveError) {
			if archiveError.TooLarge {
				server.writeCatalogProblem(response, server.GroupReleasePolicy.ArtifactTooLargeCode, requestID)
				return
			}
			server.writeCatalogProblem(response, server.GroupReleasePolicy.ArtifactInvalidCode, requestID)
			return
		}
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	state := operation.State
	if state != server.GroupReleasePolicy.PendingState && state != server.GroupReleasePolicy.RunningState {
		state = server.GroupReleasePolicy.PendingState
	}
	server.writeJSON(response, http.StatusAccepted, map[string]any{
		server.Management.JSON.OperationID: operation.ID,
		server.Management.JSON.State:       state,
		server.Management.JSON.RequestID:   requestID,
	})
}

func (server *Server) handleGroupRollback(response http.ResponseWriter, request *http.Request, path, requestID, actor string) {
	if server.GroupReleases == nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	groupID := strings.TrimSuffix(strings.TrimPrefix(path, server.Management.Paths.GroupByID), server.Management.Paths.GroupIDSeparator+server.Management.Paths.GroupRollback)
	if groupID == "" || strings.Contains(groupID, server.Management.Paths.GroupIDSeparator) {
		server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
		return
	}
	contentType, _, contentTypeErr := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if contentTypeErr != nil || contentType != server.GroupReleasePolicy.MetadataContentType {
		server.writeCatalogProblem(response, server.GroupReleasePolicy.InvalidRequestCode, requestID)
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, server.GroupReleasePolicy.RequestLimitBytes)
	fields := make(map[string]json.RawMessage)
	decoder := json.NewDecoder(request.Body)
	if err := decoder.Decode(&fields); err != nil || fields == nil || len(fields) != 2 {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.ArtifactTooLargeCode, requestID)
			return
		}
		server.writeCatalogProblem(response, server.GroupReleasePolicy.InvalidRequestCode, requestID)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		server.writeCatalogProblem(response, server.GroupReleasePolicy.InvalidRequestCode, requestID)
		return
	}
	keyJSON, hasKey := fields[server.Management.JSON.IdempotencyKey]
	expectedJSON, hasExpected := fields[server.GroupReleasePolicy.ExpectedCurrentRevisionField]
	var idempotencyKey string
	var expected *string
	if !hasKey || !hasExpected || json.Unmarshal(keyJSON, &idempotencyKey) != nil || len(idempotencyKey) < server.Management.Idempotency.KeyMin || len(idempotencyKey) > server.Management.Idempotency.KeyChars || !ascii(idempotencyKey) || json.Unmarshal(expectedJSON, &expected) != nil {
		server.writeCatalogProblem(response, server.GroupReleasePolicy.InvalidRequestCode, requestID)
		return
	}
	if expected != nil {
		valid, err := regexp.MatchString(server.GroupReleasePolicy.RevisionIDPattern, *expected)
		if err != nil || !valid {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.InvalidRequestCode, requestID)
			return
		}
	}
	operation, _, err := server.GroupReleases.Rollback(request.Context(), models.GroupRollbackCommand{
		GroupID: groupID, Actor: actor, RequestID: requestID,
		IdempotencyKey:          idempotencyKey,
		IdempotencyScope:        server.GroupReleasePolicy.ScopePrefix + groupID + server.GroupReleasePolicy.RollbackScopeSuffix,
		ExpectedCurrentRevision: expected, IdempotencyWindow: server.GroupReleasePolicy.IdempotencyWindow,
	})
	if err != nil {
		var conflict models.GroupRevisionConflict
		if errors.As(err, &conflict) {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.RevisionConflictCode, requestID)
			return
		}
		var idempotencyConflict models.IdempotencyConflict
		if errors.As(err, &idempotencyConflict) {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.IdempotencyConflictCode, requestID)
			return
		}
		var groupNotFound models.GroupNotFound
		if errors.As(err, &groupNotFound) {
			server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
			return
		}
		var revisionNotFound models.GroupRevisionNotFound
		if errors.As(err, &revisionNotFound) {
			server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
			return
		}
		var validation models.GroupReleaseValidationError
		if errors.As(err, &validation) {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.CaddyAdaptFailedCode, requestID)
			return
		}
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	state := operation.State
	if state != server.GroupReleasePolicy.PendingState && state != server.GroupReleasePolicy.RunningState {
		state = server.GroupReleasePolicy.PendingState
	}
	server.writeJSON(response, http.StatusAccepted, map[string]any{
		server.Management.JSON.OperationID: operation.ID,
		server.Management.JSON.State:       state,
		server.Management.JSON.RequestID:   requestID,
	})
}

type groupReleaseMetadata struct {
	IdempotencyKey          string
	ExpectedCurrentRevision *string
}

func readGroupReleaseMultipart(reader *multipart.Reader, policy models.GroupReleasePolicy, management config.ManagementWords) (groupReleaseMetadata, []byte, []byte, error) {
	var result groupReleaseMetadata
	var metadataBytes, caddyfile, artifact []byte
	metadataSeen, caddyfileSeen := false, false
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return result, nil, nil, err
		}
		contents, err := io.ReadAll(io.LimitReader(part, policy.RequestLimitBytes+1))
		_ = part.Close()
		if err != nil || int64(len(contents)) > policy.RequestLimitBytes {
			return result, nil, nil, errors.New(policy.InvalidConfiguration)
		}
		switch part.FormName() {
		case policy.MetadataPart:
			if metadataSeen {
				return result, nil, nil, errors.New(policy.InvalidConfiguration)
			}
			partType, _, typeErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
			if typeErr != nil || partType != policy.MetadataContentType {
				return result, nil, nil, errors.New(policy.InvalidConfiguration)
			}
			metadataSeen = true
			metadataBytes = contents
		case policy.CaddyfilePart:
			if caddyfileSeen || !utf8.Valid(contents) {
				return result, nil, nil, errors.New(policy.InvalidConfiguration)
			}
			partType, _, typeErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
			if typeErr != nil || partType != policy.CaddyfileContentType {
				return result, nil, nil, errors.New(policy.InvalidConfiguration)
			}
			caddyfileSeen = true
			caddyfile = contents
		case policy.ArtifactPart:
			if artifact != nil {
				return result, nil, nil, models.GroupReleaseArchiveError{Cause: errors.New(policy.InvalidConfiguration)}
			}
			partType, _, typeErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
			fileName := part.FileName()
			if typeErr != nil || partType != policy.ArtifactContentType || !strings.HasSuffix(strings.ToLower(fileName), policy.ArtifactSuffix) || strings.ContainsAny(fileName, "/\\") {
				return result, nil, nil, models.GroupReleaseArchiveError{Cause: errors.New(policy.InvalidConfiguration)}
			}
			if int64(len(contents)) > policy.CompressedArtifactLimitBytes {
				return result, nil, nil, models.GroupReleaseArchiveError{Cause: errors.New(policy.InvalidConfiguration), TooLarge: true}
			}
			artifact = contents
		default:
			return result, nil, nil, errors.New(policy.InvalidConfiguration)
		}
	}
	if !metadataSeen || !caddyfileSeen || len(caddyfile) == 0 {
		return result, nil, nil, errors.New(policy.InvalidConfiguration)
	}
	fields := make(map[string]json.RawMessage)
	if err := json.Unmarshal(metadataBytes, &fields); err != nil || len(fields) != 2 {
		return result, nil, nil, errors.New(policy.InvalidConfiguration)
	}
	keyJSON, hasKey := fields[policy.MetadataIdempotencyKeyField]
	expectedJSON, hasExpected := fields[policy.MetadataExpectedRevisionField]
	if !hasKey || !hasExpected || json.Unmarshal(keyJSON, &result.IdempotencyKey) != nil || len(result.IdempotencyKey) < management.Idempotency.KeyMin || len(result.IdempotencyKey) > management.Idempotency.KeyChars || !ascii(result.IdempotencyKey) {
		return result, nil, nil, errors.New(policy.InvalidConfiguration)
	}
	var expected *string
	if err := json.Unmarshal(expectedJSON, &expected); err != nil {
		return result, nil, nil, errors.New(policy.InvalidConfiguration)
	}
	if expected != nil {
		valid, err := regexp.MatchString(policy.RevisionIDPattern, *expected)
		if err != nil || !valid {
			return result, nil, nil, errors.New(policy.InvalidConfiguration)
		}
		result.ExpectedCurrentRevision = expected
	}
	return result, caddyfile, artifact, nil
}

func (server *Server) groupResponse(group models.Group) map[string]any {
	state := server.Management.Statuses.Ready
	if group.CurrentRevisionID == nil {
		state = server.Management.Statuses.Empty
	}
	return map[string]any{
		server.Management.JSON.ID:               group.ID,
		server.Management.JSON.Kind:             group.Kind,
		server.Management.JSON.Active:           group.Active,
		server.Management.JSON.CurrentRevision:  group.CurrentRevisionID,
		server.Management.JSON.PreviousRevision: group.PreviousRevisionID,
		server.Management.JSON.State:            state,
	}
}

func (server *Server) writePage(response http.ResponseWriter, values any, request *http.Request, requestID string) {
	// Lists are kept as typed slices internally; pagination is an opaque offset cursor.
	limit := server.Management.Pagination.LimitDefault
	if raw := request.URL.Query().Get(server.Management.JSON.Limit); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	if limit < server.Management.Pagination.LimitMin || limit > server.Management.Pagination.LimitMax {
		server.writeProblem(response, 400, "invalid_pagination", "limit must be between 1 and 100", requestID)
		return
	}
	offset := 0
	if cursor := request.URL.Query().Get(server.Management.JSON.Cursor); cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		parsed, parseErr := strconv.Atoi(string(decoded))
		if err != nil || parseErr != nil || parsed < 0 {
			server.writeProblem(response, 400, "invalid_cursor", "cursor is invalid", requestID)
			return
		} else {
			offset = parsed
		}
	}
	items := sliceValues(values)
	if offset > len(items) {
		server.writeProblem(response, 400, "invalid_cursor", "cursor is invalid", requestID)
		return
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	var next any
	if end < len(items) {
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}
	server.writeJSON(response, 200, map[string]any{server.Management.JSON.Items: items[offset:end], server.Management.JSON.NextCursor: next, server.Management.JSON.RequestID: requestID})
}

func sliceValues(values any) []any {
	switch typed := values.(type) {
	case []any:
		return typed
	case []models.AuditRecord:
		result := make([]any, len(typed))
		for i := range typed {
			result[i] = typed[i]
		}
		return result
	default:
		return nil
	}
}

func (server *Server) writeCatalogProblem(response http.ResponseWriter, code, requestID string) {
	problem, exists := server.Errors.Lookup(code)
	if !exists {
		server.writeProblem(response, http.StatusInternalServerError, "config_invalid", "management error contract is invalid", requestID)
		return
	}
	server.writeProblem(response, problem.Status, problem.Code, problem.Detail, requestID)
}

func ascii(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
}

func (server *Server) handlePluginAdmin(response http.ResponseWriter, request *http.Request, path, requestID string) {
	if server.AdminDispatcher == nil {
		server.writeProblem(response, 503, "plugin_unavailable", "plugin admin surface is unavailable", requestID)
		return
	}
	instanceAndRoute := strings.TrimPrefix(path, server.Management.Paths.Plugins+"/")
	instance, route, found := strings.Cut(instanceAndRoute, "/")
	pagePrefix := server.Management.Paths.AdminPages + "/"
	if !found || instance == "" || !strings.HasPrefix(route, pagePrefix) {
		server.writeProblem(response, 404, "not_found", "plugin admin resource not found", requestID)
		return
	}
	pageAndAction := strings.TrimPrefix(route, pagePrefix)
	parts := strings.Split(pageAndAction, "/")
	if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && parts[1] == "") {
		server.writeProblem(response, 404, "not_found", "plugin admin resource not found", requestID)
		return
	}
	page := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	var input json.RawMessage
	if request.Method == http.MethodPost {
		defer request.Body.Close()
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil && err.Error() != "EOF" {
			server.writeProblem(response, 400, "invalid_input", "request body must be JSON", requestID)
			return
		}
	}
	result, err := server.AdminDispatcher.Dispatch(request.Context(), instance, plugins.RequestContext{Instance: instance, Page: page, Action: action, Method: request.Method, RequestID: requestID, Actor: "management", Input: input})
	if err != nil {
		server.writeProblem(response, 502, "plugin_error", err.Error(), requestID)
		return
	}
	if result.Status == 0 {
		result.Status = 200
	}
	if result.ContentType == "" {
		result.ContentType = server.Management.ContentTypes.JSON
	}
	response.Header().Set("Content-Type", result.ContentType)
	response.WriteHeader(result.Status)
	_, _ = response.Write(result.Body)
}
func randomID() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(bytes)
}
func (server *Server) authenticate(ctx context.Context, value string) (string, bool, error) {
	if !strings.HasPrefix(value, "Bearer ") {
		return "", false, nil
	}
	key := strings.TrimPrefix(value, "Bearer ")
	if server.AccessService != nil {
		actor, valid, err := server.AccessService.Authenticate(ctx, key)
		return actor, valid, err
	}
	if server.Token != "" && key == server.Token {
		return server.AuditWords.Audit.Actors.StaticToken, true, nil
	}
	for _, account := range server.ServiceAccounts {
		if bcrypt.CompareHashAndPassword([]byte(account.KeyHash), []byte(key)) == nil {
			return account.ID, true, nil
		}
	}
	return "", false, nil
}

func (server *Server) recordAudit(ctx context.Context, actor, action, resource, result, requestID, digestBefore, digestAfter string) error {
	if server.Audit == nil || action == "" || actor == "" {
		return nil
	}
	record := models.AuditRecord{
		Timestamp:    time.Now().UTC(),
		Actor:        actor,
		Action:       action,
		Resource:     resource,
		Result:       result,
		RequestID:    requestID,
		DigestBefore: digestBefore,
		DigestAfter:  digestAfter,
	}
	return server.Audit.Record(ctx, record)
}
func (server *Server) writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", server.Management.ContentTypes.JSON)
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
func (server *Server) writeProblem(response http.ResponseWriter, status int, code, detail, requestID string) {
	response.Header().Set("Content-Type", server.Management.ContentTypes.Problem)
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]any{"type": "about:blank", "title": code, server.Management.JSON.Status: status, "code": code, "detail": detail, "instance": "", server.Management.JSON.RequestID: requestID})
}
