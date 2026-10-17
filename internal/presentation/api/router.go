package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/security"
	"github.com/Liapoldus/core/v3/internal/presentation/api/handlers"
	sdkmodels "github.com/Liapoldus/service-sdk/v2/domain/models"
	"golang.org/x/crypto/bcrypt"
)

type serviceConfigurationContextKey struct{}

func WithServiceConfigurations(handler http.Handler, service *application.ServiceConfigurationService) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		ctx := context.WithValue(request.Context(), serviceConfigurationContextKey{}, service)
		handler.ServeHTTP(response, request.WithContext(ctx))
	})
}

func serviceConfigurationServiceFromContext(ctx context.Context) *application.ServiceConfigurationService {
	service, _ := ctx.Value(serviceConfigurationContextKey{}).(*application.ServiceConfigurationService)
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

func (server *Server) serviceHandlerDependencies() handlers.ServiceDependencies {
	server.mu.RLock()
	serviceInventory := append([]any(nil), server.Services...)
	adminControl := server.ServiceAdminControl
	server.mu.RUnlock()

	dependencies := handlers.ServiceDependencies{
		Management:              server.Management,
		AuditWords:              server.AuditWords,
		ServiceIDField:          server.ServiceIDField,
		Services:                serviceInventory,
		Operations:              server.Operations,
		ServiceLinks:            server.ServiceLinks,
		ServiceSecretReferences: server.ServiceSecretReferences,
		TrafficRollouts:         server.TrafficRollouts,
		TrafficRolloutAPI:       server.TrafficRolloutAPI,
		WriteJSON:               server.writeJSON,
		WriteProblem:            server.writeProblem,
		WriteCatalogProblem:     server.writeCatalogProblem,
		WritePage:               server.writePage,
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
		dependencies.DispatchAdmin = func(ctx context.Context, invocation handlers.AdminInvocation, input []byte) (handlers.ServiceAdminResult, error) {
			result, err := adminControl.Action(ctx, sdkmodels.AdminActionInvocation{
				CallerID: invocation.CallerID, InstanceID: invocation.InstanceID, PageID: invocation.PageID,
				ActionID: invocation.ActionID, SurfaceDigest: invocation.SurfaceDigest, RequestID: invocation.RequestID,
				IdempotencyKey: invocation.IdempotencyKey, IfMatch: invocation.IfMatch,
			}, input)
			return handlers.ServiceAdminResult{Status: result.StatusCode, ContentType: server.Management.ContentTypes.JSON, Body: result.Body}, err
		}
		dependencies.ForwardArtifact = func(ctx context.Context, invocation handlers.AdminInvocation, metadata []byte, contentType string, artifact io.ReadCloser) (handlers.ServiceAdminResult, error) {
			result, err := adminControl.Artifact(ctx, sdkmodels.ArtifactInvocation{
				CallerID: invocation.CallerID, InstanceID: invocation.InstanceID, PageID: invocation.PageID,
				ActionID: invocation.ActionID, SurfaceDigest: invocation.SurfaceDigest, RequestID: invocation.RequestID,
				IdempotencyKey: invocation.IdempotencyKey, IfMatch: invocation.IfMatch,
			}, metadata, contentType, artifact)
			return handlers.ServiceAdminResult{Status: result.StatusCode, ContentType: server.Management.ContentTypes.JSON, Body: result.Body}, err
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
				Actor: actor, Action: server.AuditWords.Audit.Actions.ServiceAdminAction,
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
		server.dispatchServiceRegistration(response, request, path, requestID) ||
		server.dispatchServiceMetadata(response, request, path, requestID) ||
		server.dispatchConfigBundles(response, request, path, requestID, actor) ||
		server.dispatchAccess(response, request, path, requestID, actor) ||
		server.dispatchServiceCollections(response, request, path, requestID, actor) ||
		server.dispatchServiceLinks(response, request, path, requestID, actor) ||
		server.dispatchAudit(response, request, path, requestID) ||
		server.dispatchServiceActions(response, request, path, requestID, actor) ||
		server.dispatchOperations(response, request, path, requestID, actor) {
		return
	}
	server.writeProblem(response, 404, "not_found", "resource not found", requestID)
}

func (server *Server) dispatchServiceRegistration(response http.ResponseWriter, request *http.Request, path, requestID string) bool {
	if server.RegisterService == nil || request.Method != http.MethodPost || !strings.HasPrefix(path, server.Management.Paths.Services+"/") {
		return false
	}
	resource := strings.TrimPrefix(path, server.Management.Paths.Services+"/")
	parts := strings.Split(resource, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] != "replicas" || parts[2] != "register" {
		return false
	}
	handlers.ServiceRegistration(response, request, server.RegisterService, parts[0], server.writeJSON, server.writeProblem, requestID)
	return true
}

func (server *Server) dispatchServiceMetadata(response http.ResponseWriter, request *http.Request, path, requestID string) bool {
	if server.ServiceMetadata == nil || request.Method != http.MethodGet || !strings.HasPrefix(path, server.Management.Paths.Services+"/") {
		return false
	}
	resource := strings.TrimPrefix(path, server.Management.Paths.Services+"/")
	parts := strings.Split(resource, "/")
	if len(parts) != 2 || parts[0] == "" || (parts[1] != "manifest" && parts[1] != "schema" && parts[1] != "status") {
		return false
	}
	handlers.ServiceMetadata(response, request, server.ServiceMetadata, parts[0], parts[1], server.writeJSON, server.writeProblem, requestID)
	return true
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

func (server *Server) dispatchServiceCollections(response http.ResponseWriter, request *http.Request, path, requestID, actor string) bool {
	if path == server.Management.Paths.Services && request.Method == http.MethodGet {
		handlers.ServiceList(server.serviceHandlerDependencies(), response, request, requestID)
		return true
	}
	if path == server.Management.Paths.AdminSurfaces && request.Method == http.MethodGet {
		handlers.AdminSurfaceList(server.serviceHandlerDependencies(), response, request, requestID)
		return true
	}
	serviceDependencies := server.serviceHandlerDependencies()
	if handlers.IsTrafficRolloutDetailPath(serviceDependencies, path) && request.Method == server.Management.Methods.Get {
		handlers.TrafficRolloutGet(serviceDependencies, response, request, path, requestID)
		return true
	}
	if handlers.IsTrafficRolloutApprovalPath(serviceDependencies, path) {
		handlers.TrafficRolloutApproveStage(serviceDependencies, response, request, path, requestID, actor)
		return true
	}
	if handlers.IsServiceSettingsPath(serviceDependencies, path) && request.Method == server.Management.Methods.Get {
		handlers.ServiceSettings(serviceDependencies, response, serviceConfigurationServiceFromContext(request.Context()), request.Context(), path, requestID)
		return true
	}
	if handlers.IsServiceSettingsPath(serviceDependencies, path) && request.Method == server.Management.Methods.Put {
		handlers.ServiceSettingsUpdate(serviceDependencies, response, request, serviceConfigurationServiceFromContext(request.Context()), path, requestID, actor)
		return true
	}
	if handlers.IsTrafficRolloutCollectionPath(serviceDependencies, path) && request.Method == server.Management.Methods.Post {
		handlers.TrafficRolloutCreate(serviceDependencies, response, request, path, requestID, actor)
		return true
	}
	if handlers.IsServiceDetailPath(serviceDependencies, path) && request.Method == server.Management.Methods.Get {
		handlers.ServiceDetail(serviceDependencies, response, path, requestID)
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

func (server *Server) dispatchServiceLinks(response http.ResponseWriter, request *http.Request, path, requestID, actor string) bool {
	serviceDependencies := server.serviceHandlerDependencies()
	if handlers.IsServiceLinkCollectionPath(serviceDependencies, path) {
		switch request.Method {
		case server.Management.Methods.Get:
			handlers.ServiceLinkList(serviceDependencies, response, request, requestID)
			return true
		case server.Management.Methods.Post:
			handlers.ServiceLinkCreate(serviceDependencies, response, request, requestID, actor)
			return true
		}
		return false
	}
	if handlers.IsServiceLinkDetailPath(serviceDependencies, path) {
		switch request.Method {
		case server.Management.Methods.Get:
			handlers.ServiceLinkGet(serviceDependencies, response, request, path, requestID)
			return true
		case server.Management.Methods.Put:
			handlers.ServiceLinkReplace(serviceDependencies, response, request, path, requestID, actor)
			return true
		case server.Management.Methods.Delete:
			handlers.ServiceLinkDelete(serviceDependencies, response, request, path, requestID, actor)
			return true
		}
		return false
	}
	return false
}

func (server *Server) dispatchServiceActions(response http.ResponseWriter, request *http.Request, path, requestID, actor string) bool {
	serviceDependencies := server.serviceHandlerDependencies()
	if handlers.IsServiceSecretReferencePath(serviceDependencies, path) {
		switch request.Method {
		case server.Management.Methods.Get:
			handlers.ServiceSecretReferenceList(serviceDependencies, response, request, path, requestID)
			return true
		case server.Management.Methods.Post:
			handlers.ServiceSecretReferenceCreate(serviceDependencies, response, request, path, requestID, actor)
			return true
		}
		server.writeProblem(response, http.StatusMethodNotAllowed, "method_not_allowed", "unsupported service secret method", requestID)
		return true
	}
	if handlers.IsServiceSecretReferenceDetailPath(serviceDependencies, path) && request.Method == server.Management.Methods.Delete {
		handlers.ServiceSecretReferenceRevoke(serviceDependencies, response, request, path, requestID, actor)
		return true
	}
	if handlers.IsServiceRollbackPath(serviceDependencies, path) && request.Method == server.Management.Methods.Post {
		handlers.ServiceSettingsRollback(serviceDependencies, response, request, serviceConfigurationServiceFromContext(request.Context()), path, requestID, actor)
		return true
	}
	if strings.HasPrefix(path, server.Management.Paths.Services+"/") && strings.Contains(path, "/"+server.Management.Paths.AdminPages+"/") && (request.Method == http.MethodGet || request.Method == http.MethodPost) {
		handlers.ServiceAdmin(server.serviceHandlerDependencies(), response, request, path, requestID, actor)
		return true
	}
	return false
}

func (server *Server) dispatchOperations(response http.ResponseWriter, request *http.Request, path, requestID, actor string) bool {
	if path == server.Management.Paths.OperationResolve {
		if request.Method != server.Management.Methods.Post {
			server.writeProblem(response, http.StatusMethodNotAllowed, "method_not_allowed", "operation resolve accepts POST", requestID)
			return true
		}
		handlers.OperationResolve(server.managementHandlerDependencies(), response, request, requestID, actor)
		return true
	}
	if !strings.HasPrefix(path, server.Management.Paths.Operations+"/") || request.Method != http.MethodGet {
		return false
	}
	handlers.OperationGet(server.managementHandlerDependencies(), response, request, path, requestID)
	return true
}
