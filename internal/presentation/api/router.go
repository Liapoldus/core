package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/security"
	"github.com/Liapoldus/core/internal/presentation/api/handlers"
	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
	"golang.org/x/crypto/bcrypt"
)

type pluginConfigurationContextKey struct{}

func WithPluginConfigurations(handler http.Handler, service *application.PluginConfigurationService) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		ctx := context.WithValue(request.Context(), pluginConfigurationContextKey{}, service)
		handler.ServeHTTP(response, request.WithContext(ctx))
	})
}

func pluginConfigurationServiceFromContext(ctx context.Context) *application.PluginConfigurationService {
	service, _ := ctx.Value(pluginConfigurationContextKey{}).(*application.PluginConfigurationService)
	return service
}

func (server *Server) handlerDependencies() handlers.Dependencies {
	return handlers.Dependencies{
		AccessService:       server.AccessService,
		Management:          server.Management,
		AuditWords:          server.AuditWords,
		GenerateServiceKey:  security.GenerateServiceKey,
		WriteJSON:           server.writeJSON,
		WriteProblem:        server.writeProblem,
		WriteCatalogProblem: server.writeCatalogProblem,
	}
}

func (server *Server) pluginHandlerDependencies() handlers.PluginDependencies {
	server.mu.RLock()
	pluginInventory := append([]any(nil), server.Plugins...)
	adminControl := server.PluginAdminControl
	server.mu.RUnlock()

	dependencies := handlers.PluginDependencies{
		Management:          server.Management,
		AuditWords:          server.AuditWords,
		PluginIDField:       server.PluginIDField,
		Plugins:             pluginInventory,
		Operations:          server.Operations,
		PluginLinks:         server.PluginLinks,
		TrafficRollouts:     server.TrafficRollouts,
		TrafficRolloutAPI:   server.TrafficRolloutAPI,
		WriteJSON:           server.writeJSON,
		WriteProblem:        server.writeProblem,
		WriteCatalogProblem: server.writeCatalogProblem,
		WritePage:           server.writePage,
	}
	if adminControl != nil {
		limits := adminControl.AdminLimits()
		dependencies.AdminLimits = handlers.AdminLimits{
			JSONRequestBytes: limits.JSONRequestBytes, ArtifactBytes: limits.ArtifactBytes, MinimumArtifact: limits.MinimumArtifact,
			MetadataBytes: limits.MetadataBytes, MultipartBytes: limits.MultipartBytes,
			MaximumRequest: limits.MaximumRequest, MetadataPartName: limits.MetadataPartName,
			ArtifactPartName: limits.ArtifactPartName, MultipartMediaType: limits.MultipartMediaType,
			MetadataMediaType: limits.MetadataMediaType,
		}
		dependencies.ListAdminSurfaces = func(ctx context.Context) ([]handlers.AdminSurfaceItem, error) {
			surfaces, err := adminControl.Surfaces(ctx)
			if err != nil {
				return nil, err
			}
			result := make([]handlers.AdminSurfaceItem, len(surfaces))
			for index, surface := range surfaces {
				result[index] = handlers.AdminSurfaceItem{InstanceID: surface.InstanceID, Descriptor: surface.Descriptor, SHA256: surface.SHA256}
			}
			return result, nil
		}
		dependencies.AdminSurfaceDigest = adminControl.SurfaceDigest
		dependencies.DispatchAdmin = func(ctx context.Context, invocation handlers.AdminInvocation, input []byte) (handlers.PluginAdminResult, error) {
			result, err := adminControl.Action(ctx, sdkmodels.AdminActionInvocation{
				CallerID: invocation.CallerID, InstanceID: invocation.InstanceID, PageID: invocation.PageID,
				ActionID: invocation.ActionID, SurfaceDigest: invocation.SurfaceDigest, RequestID: invocation.RequestID,
				IdempotencyKey: invocation.IdempotencyKey, IfMatch: invocation.IfMatch,
			}, input)
			return handlers.PluginAdminResult{Status: result.StatusCode, ContentType: server.Management.ContentTypes.JSON, Body: result.Body}, err
		}
		dependencies.ForwardArtifact = func(ctx context.Context, invocation handlers.AdminInvocation, metadata []byte, contentType string, artifact io.ReadCloser) (handlers.PluginAdminResult, error) {
			result, err := adminControl.Artifact(ctx, sdkmodels.ArtifactInvocation{
				CallerID: invocation.CallerID, InstanceID: invocation.InstanceID, PageID: invocation.PageID,
				ActionID: invocation.ActionID, SurfaceDigest: invocation.SurfaceDigest, RequestID: invocation.RequestID,
				IdempotencyKey: invocation.IdempotencyKey, IfMatch: invocation.IfMatch,
			}, metadata, contentType, artifact)
			return handlers.PluginAdminResult{Status: result.StatusCode, ContentType: server.Management.ContentTypes.JSON, Body: result.Body}, err
		}
		dependencies.RecordAdminOutcome = func(ctx context.Context, actor, instanceID, requestID string, succeeded bool) error {
			if server.Audit == nil {
				return errors.New("audit service unavailable")
			}
			result := server.AuditWords.Audit.Results.Failed
			if succeeded {
				result = server.AuditWords.Audit.Results.Succeeded
			}
			return server.Audit.Record(ctx, models.AuditRecord{
				Actor: actor, Action: server.AuditWords.Audit.Actions.PluginAdminAction,
				Resource: instanceID, Result: result, RequestID: requestID,
			})
		}
	}
	return dependencies
}

func (server *Server) managementHandlerDependencies() handlers.ManagementDependencies {
	server.mu.RLock()
	defer server.mu.RUnlock()
	return handlers.ManagementDependencies{
		Audit:               server.Audit,
		Operations:          server.Operations,
		DataPlaneState:      server.DataPlaneState,
		DataPlaneReason:     server.DataPlaneReason,
		DataPlaneReadiness:  server.DataPlaneReadiness,
		DataPlaneDrift:      server.DataPlaneDrift,
		Management:          server.Management,
		AuditWords:          server.AuditWords,
		WriteJSON:           server.writeJSON,
		WriteProblem:        server.writeProblem,
		WriteCatalogProblem: server.writeCatalogProblem,
	}
}

func (server *Server) handle(response http.ResponseWriter, request *http.Request) {
	requestID := "req_" + randomID()
	response.Header().Set(server.Management.Headers.RequestID, requestID)
	if handlers.Healthz(server.managementHandlerDependencies(), response, request, requestID) {
		return
	}
	actor, authorized := server.authorizeManagementRequest(response, request, requestID)
	if !authorized {
		return
	}
	if server.dispatchSettings(response, request, actor, requestID) {
		return
	}
	path := strings.TrimSuffix(request.URL.Path, "/")
	if server.dispatchReadiness(response, request, path, requestID) ||
		server.dispatchConfigBundles(response, request, path, requestID, actor) ||
		server.dispatchAccess(response, request, path, requestID, actor) ||
		server.dispatchPluginCollections(response, request, path, requestID, actor) ||
		server.dispatchPluginLinks(response, request, path, requestID, actor) ||
		server.dispatchAudit(response, request, path, requestID) ||
		server.dispatchPluginActions(response, request, path, requestID, actor) ||
		server.dispatchOperations(response, request, path, requestID) {
		return
	}
	server.writeProblem(response, 404, "not_found", "resource not found", requestID)
}

func (server *Server) dispatchConfigBundles(response http.ResponseWriter, request *http.Request, path, requestID, actor string) bool {
	planPath := server.Management.Paths.ConfigBundlePlan
	applyPath := server.Management.Paths.ConfigBundleApply
	if server.ConfigBundles == nil || (path != planPath && path != applyPath) {
		return false
	}
	deps := handlers.ConfigBundleDependencies{
		WriteJSON: server.writeJSON, WriteProblem: server.writeProblem,
		WriteCatalogProblem: server.writeCatalogProblem,
		Plan:                server.ConfigBundles.Plan, Apply: server.ConfigBundles.Apply,
	}
	switch {
	case path == planPath && request.Method == server.Management.Methods.Post:
		handlers.ConfigBundlePlan(deps, response, request, requestID)
		return true
	case path == applyPath && request.Method == server.Management.Methods.Post:
		handlers.ConfigBundleApply(deps, response, request, requestID, actor)
		return true
	default:
		server.writeProblem(response, http.StatusMethodNotAllowed, "method_not_allowed", "unsupported config bundle method", requestID)
		return true
	}
}

func (server *Server) authorizeManagementRequest(response http.ResponseWriter, request *http.Request, requestID string) (string, bool) {
	actor, authorized, err := server.authenticate(request.Context(), request.Header.Get("Authorization"))
	if err != nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return "", false
	}
	if !authorized {
		server.writeCatalogProblem(response, server.Management.Codes.BearerRequired, requestID)
		return "", false
	}
	return actor, true
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

func (server *Server) dispatchReadiness(response http.ResponseWriter, request *http.Request, path, requestID string) bool {
	if path == server.Management.Paths.Status && request.Method == http.MethodGet {
		handlers.Readiness(server.managementHandlerDependencies(), response, request, requestID)
		return true
	}
	return false
}

func (server *Server) dispatchAccess(response http.ResponseWriter, request *http.Request, path, requestID, actor string) bool {
	if path == server.Management.Paths.ServiceKeys && request.Method == server.Management.Methods.Post {
		handlers.ServiceKeyCreate(server.handlerDependencies(), response, request, requestID, actor)
		return true
	}
	if path == server.Management.Paths.ServiceKeys && request.Method == server.Management.Methods.Get {
		handlers.ServiceKeyList(server.handlerDependencies(), response, request, requestID)
		return true
	}
	return false
}

func (server *Server) dispatchPluginCollections(response http.ResponseWriter, request *http.Request, path, requestID, actor string) bool {
	if path == server.Management.Paths.Plugins && request.Method == http.MethodGet {
		handlers.PluginList(server.pluginHandlerDependencies(), response, request, requestID)
		return true
	}
	if path == server.Management.Paths.AdminSurfaces && request.Method == http.MethodGet {
		handlers.AdminSurfaceList(server.pluginHandlerDependencies(), response, request, requestID)
		return true
	}
	pluginDependencies := server.pluginHandlerDependencies()
	if handlers.IsTrafficRolloutDetailPath(pluginDependencies, path) && request.Method == server.Management.Methods.Get {
		handlers.TrafficRolloutGet(pluginDependencies, response, request, path, requestID)
		return true
	}
	if handlers.IsTrafficRolloutApprovalPath(pluginDependencies, path) {
		handlers.TrafficRolloutApproveStage(pluginDependencies, response, request, path, requestID, actor)
		return true
	}
	if handlers.IsPluginSettingsPath(pluginDependencies, path) && request.Method == server.Management.Methods.Get {
		handlers.PluginSettings(pluginDependencies, response, pluginConfigurationServiceFromContext(request.Context()), request.Context(), path, requestID)
		return true
	}
	if handlers.IsPluginSettingsPath(pluginDependencies, path) && request.Method == server.Management.Methods.Put {
		handlers.PluginSettingsUpdate(pluginDependencies, response, request, pluginConfigurationServiceFromContext(request.Context()), path, requestID, actor)
		return true
	}
	if handlers.IsTrafficRolloutCollectionPath(pluginDependencies, path) && request.Method == server.Management.Methods.Post {
		handlers.TrafficRolloutCreate(pluginDependencies, response, request, path, requestID, actor)
		return true
	}
	if handlers.IsPluginDetailPath(pluginDependencies, path) && request.Method == server.Management.Methods.Get {
		handlers.PluginDetail(pluginDependencies, response, path, requestID)
		return true
	}
	return false
}

func (server *Server) dispatchAudit(response http.ResponseWriter, request *http.Request, path, requestID string) bool {
	if path != server.Management.Paths.Audit || request.Method != http.MethodGet {
		return false
	}
	handlers.AuditList(server.managementHandlerDependencies(), response, request, requestID)
	return true
}

func (server *Server) dispatchPluginLinks(response http.ResponseWriter, request *http.Request, path, requestID, actor string) bool {
	pluginDependencies := server.pluginHandlerDependencies()
	if handlers.IsPluginLinkCollectionPath(pluginDependencies, path) {
		switch request.Method {
		case server.Management.Methods.Get:
			handlers.PluginLinkList(pluginDependencies, response, request, requestID)
			return true
		case server.Management.Methods.Post:
			handlers.PluginLinkCreate(pluginDependencies, response, request, requestID, actor)
			return true
		}
		return false
	}
	if handlers.IsPluginLinkDetailPath(pluginDependencies, path) {
		switch request.Method {
		case server.Management.Methods.Get:
			handlers.PluginLinkGet(pluginDependencies, response, request, path, requestID)
			return true
		case server.Management.Methods.Put:
			handlers.PluginLinkReplace(pluginDependencies, response, request, path, requestID, actor)
			return true
		case server.Management.Methods.Delete:
			handlers.PluginLinkDelete(pluginDependencies, response, request, path, requestID, actor)
			return true
		}
		return false
	}
	return false
}

func (server *Server) dispatchPluginActions(response http.ResponseWriter, request *http.Request, path, requestID, actor string) bool {
	pluginDependencies := server.pluginHandlerDependencies()
	if handlers.IsPluginRollbackPath(pluginDependencies, path) && request.Method == server.Management.Methods.Post {
		handlers.PluginSettingsRollback(pluginDependencies, response, request, pluginConfigurationServiceFromContext(request.Context()), path, requestID, actor)
		return true
	}
	if strings.HasPrefix(path, server.Management.Paths.Plugins+"/") && strings.Contains(path, "/"+server.Management.Paths.AdminPages+"/") && (request.Method == http.MethodGet || request.Method == http.MethodPost) {
		handlers.PluginAdmin(server.pluginHandlerDependencies(), response, request, path, requestID, actor)
		return true
	}
	return false
}

func (server *Server) dispatchOperations(response http.ResponseWriter, request *http.Request, path, requestID string) bool {
	if !strings.HasPrefix(path, server.Management.Paths.Operations+"/") || request.Method != http.MethodGet {
		return false
	}
	handlers.OperationGet(server.managementHandlerDependencies(), response, request, path, requestID)
	return true
}
