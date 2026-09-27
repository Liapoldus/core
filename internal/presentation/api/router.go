package api

import (
	"net/http"
	"strings"

	"github.com/Liapoldus/core/internal/infrastructure/security"
	"github.com/Liapoldus/core/internal/presentation/api/handlers"
)

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

func (server *Server) groupHandlerDependencies() handlers.GroupDependencies {
	return handlers.GroupDependencies{
		GroupService:        server.GroupService,
		GroupReleases:       server.GroupReleases,
		GroupReleasePolicy:  server.GroupReleasePolicy,
		Management:          server.Management,
		AuditWords:          server.AuditWords,
		RecordAudit:         server.recordAudit,
		WriteJSON:           server.writeJSON,
		WriteProblem:        server.writeProblem,
		WriteCatalogProblem: server.writeCatalogProblem,
	}
}

func (server *Server) handle(response http.ResponseWriter, request *http.Request) {
	requestID := "req_" + randomID()
	response.Header().Set(server.Management.Headers.RequestID, requestID)
	if server.handleHealthz(response, request, requestID) {
		return
	}
	actor, authorized := server.authorizeManagementRequest(response, request, requestID)
	if !authorized {
		return
	}
	path := strings.TrimSuffix(request.URL.Path, "/")
	if server.dispatchReadinessAndAdmin(response, request, path, requestID, actor) ||
		server.dispatchAccessAndGroups(response, request, path, requestID, actor) ||
		server.dispatchGroupReleases(response, request, path, requestID, actor) ||
		server.dispatchPluginCollections(response, request, path, requestID) ||
		server.dispatchAudit(response, request, path, requestID) ||
		server.dispatchPluginActions(response, request, path, requestID, actor) ||
		server.dispatchOperations(response, request, path, requestID) {
		return
	}
	server.writeProblem(response, 404, "not_found", "resource not found", requestID)
}

func (server *Server) dispatchReadinessAndAdmin(response http.ResponseWriter, request *http.Request, path, requestID, actor string) bool {
	if path == server.Management.Paths.Status && request.Method == http.MethodGet {
		server.handleReadiness(response, request, requestID)
		return true
	}
	if strings.HasPrefix(request.URL.Path, server.AdminWords.Paths.ManagementPrefix) {
		server.handleCaddyAdmin(response, request, requestID, actor)
		return true
	}
	return false
}

func (server *Server) dispatchAccessAndGroups(response http.ResponseWriter, request *http.Request, path, requestID, actor string) bool {
	if path == server.Management.Paths.ServiceKeys && request.Method == server.Management.Methods.Post {
		handlers.ServiceKeyCreate(server.handlerDependencies(), response, request, requestID, actor)
		return true
	}
	if path == server.Management.Paths.ServiceKeys && request.Method == server.Management.Methods.Get {
		handlers.ServiceKeyList(server.handlerDependencies(), response, request, requestID)
		return true
	}
	if path == server.Management.Paths.Groups && request.Method == server.Management.Methods.Get {
		handlers.GroupList(server.groupHandlerDependencies(), response, request, requestID)
		return true
	}
	if path == server.Management.Paths.Groups && request.Method == server.Management.Methods.Post {
		handlers.GroupCreate(server.groupHandlerDependencies(), response, request, requestID, actor)
		return true
	}
	if server.isPluginCookiePolicyPath(path) && (request.Method == server.Management.Methods.Get || request.Method == server.Management.Methods.Put) {
		server.handlePluginCookiePolicy(response, request, path, requestID, actor)
		return true
	}
	return false
}

func (server *Server) dispatchGroupReleases(response http.ResponseWriter, request *http.Request, path, requestID, actor string) bool {
	if strings.HasPrefix(path, server.Management.Paths.GroupByID) && strings.HasSuffix(path, server.Management.Paths.GroupReleases) && request.Method == server.Management.Methods.Post {
		handlers.GroupPublish(server.groupHandlerDependencies(), response, request, path, requestID, actor)
		return true
	}
	if strings.HasPrefix(path, server.Management.Paths.GroupByID) && strings.HasSuffix(path, server.Management.Paths.GroupReleases) && request.Method == server.Management.Methods.Get {
		handlers.GroupReleases(server.groupHandlerDependencies(), response, request, path, requestID)
		return true
	}
	if strings.HasPrefix(path, server.Management.Paths.GroupByID) && strings.Contains(path, server.Management.Paths.GroupReleases+server.Management.Paths.GroupIDSeparator) && request.Method == server.Management.Methods.Get {
		handlers.GroupRelease(server.groupHandlerDependencies(), response, request, path, requestID)
		return true
	}
	if strings.HasPrefix(path, server.Management.Paths.GroupByID) && strings.HasSuffix(path, server.Management.Paths.GroupIDSeparator+server.Management.Paths.GroupRollback) && request.Method == server.Management.Methods.Post {
		handlers.GroupRollback(server.groupHandlerDependencies(), response, request, path, requestID, actor)
		return true
	}
	if strings.HasPrefix(path, server.Management.Paths.GroupByID) && request.Method == server.Management.Methods.Get {
		handlers.GroupGet(server.groupHandlerDependencies(), response, request, path, requestID)
		return true
	}
	return false
}

func (server *Server) dispatchPluginCollections(response http.ResponseWriter, request *http.Request, path, requestID string) bool {
	if path == server.Management.Paths.Plugins && request.Method == http.MethodGet {
		server.writePage(response, server.Plugins, request, requestID)
		return true
	}
	if path == server.Management.Paths.AdminSurfaces && request.Method == http.MethodGet {
		server.mu.RLock()
		surfaces := handlers.CloneAdminSurfaces(server.AdminSurfaces)
		server.mu.RUnlock()
		server.writeJSON(response, 200, map[string]any{server.Management.JSON.Items: surfaces, server.Management.JSON.RequestID: requestID})
		return true
	}
	if server.isPluginDetailPath(path) && request.Method == server.Management.Methods.Get {
		server.handlePluginDetail(response, path, requestID)
		return true
	}
	return false
}

func (server *Server) dispatchAudit(response http.ResponseWriter, request *http.Request, path, requestID string) bool {
	if path != server.Management.Paths.Audit || request.Method != http.MethodGet {
		return false
	}
	server.handleAuditList(response, request, requestID)
	return true
}

func (server *Server) dispatchPluginActions(response http.ResponseWriter, request *http.Request, path, requestID, actor string) bool {
	if strings.HasPrefix(path, server.Management.Paths.Plugins+"/") && strings.Contains(path, "/"+server.Management.Paths.AdminPages+"/") && (request.Method == http.MethodGet || request.Method == http.MethodPost) {
		server.handlePluginAdmin(response, request, path, requestID)
		return true
	}
	if strings.HasPrefix(path, server.Management.Paths.Plugins+"/") && strings.HasSuffix(path, "/"+server.Management.Paths.Restart) && request.Method == http.MethodPost {
		server.handlePluginRestart(response, request, path, requestID, actor)
		return true
	}
	return false
}

func (server *Server) dispatchOperations(response http.ResponseWriter, request *http.Request, path, requestID string) bool {
	if !strings.HasPrefix(path, server.Management.Paths.Operations+"/") || request.Method != http.MethodGet {
		return false
	}
	server.handleOperationGet(response, request, path, requestID)
	return true
}
