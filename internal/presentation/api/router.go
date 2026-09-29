package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/internal/infrastructure/security"
	"github.com/Liapoldus/core/internal/presentation/api/handlers"
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
	adminSurfaces := handlers.CloneAdminSurfaces(server.AdminSurfaces)
	server.mu.RUnlock()

	dependencies := handlers.PluginDependencies{
		Management:          server.Management,
		AuditWords:          server.AuditWords,
		PluginIDField:       server.PluginIDField,
		Plugins:             pluginInventory,
		AdminSurfaces:       adminSurfaces,
		Operations:          server.Operations,
		WriteJSON:           server.writeJSON,
		WriteProblem:        server.writeProblem,
		WriteCatalogProblem: server.writeCatalogProblem,
		WritePage:           server.writePage,
	}
	if server.AdminDispatcher != nil {
		dependencies.DispatchAdmin = func(ctx context.Context, instance, page, action, method, requestID, actor string, input json.RawMessage) (handlers.PluginAdminResult, error) {
			result, err := server.AdminDispatcher.Dispatch(ctx, instance, plugins.RequestContext{
				Instance: instance, Page: page, Action: action, Method: method,
				RequestID: requestID, Actor: actor, Input: input,
			})
			return handlers.PluginAdminResult{
				Status: result.Status, ContentType: result.ContentType, Body: []byte(result.Body),
			}, err
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
	path := strings.TrimSuffix(request.URL.Path, "/")
	if server.dispatchReadiness(response, request, path, requestID) ||
		server.dispatchAccess(response, request, path, requestID, actor) ||
		server.dispatchPluginCollections(response, request, path, requestID, actor) ||
		server.dispatchAudit(response, request, path, requestID) ||
		server.dispatchPluginActions(response, request, path, requestID, actor) ||
		server.dispatchOperations(response, request, path, requestID) {
		return
	}
	server.writeProblem(response, 404, "not_found", "resource not found", requestID)
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
		handlers.AdminSurfaceList(server.pluginHandlerDependencies(), response, requestID)
		return true
	}
	pluginDependencies := server.pluginHandlerDependencies()
	if handlers.IsPluginSettingsPath(pluginDependencies, path) && request.Method == server.Management.Methods.Get {
		handlers.PluginSettings(pluginDependencies, response, pluginConfigurationServiceFromContext(request.Context()), request.Context(), path, requestID)
		return true
	}
	if handlers.IsPluginSettingsPath(pluginDependencies, path) && request.Method == server.Management.Methods.Put {
		handlers.PluginSettingsUpdate(pluginDependencies, response, request, pluginConfigurationServiceFromContext(request.Context()), path, requestID, actor)
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

func (server *Server) dispatchPluginActions(response http.ResponseWriter, request *http.Request, path, requestID, actor string) bool {
	pluginDependencies := server.pluginHandlerDependencies()
	if handlers.IsPluginRollbackPath(pluginDependencies, path) && request.Method == server.Management.Methods.Post {
		handlers.PluginSettingsRollback(pluginDependencies, response, request, pluginConfigurationServiceFromContext(request.Context()), path, requestID, actor)
		return true
	}
	if strings.HasPrefix(path, server.Management.Paths.Plugins+"/") && strings.Contains(path, "/"+server.Management.Paths.AdminPages+"/") && (request.Method == http.MethodGet || request.Method == http.MethodPost) {
		handlers.PluginAdmin(server.pluginHandlerDependencies(), response, request, path, requestID)
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
