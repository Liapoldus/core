// Package api adapts the Management API to application use cases.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Liapoldus/core/internal/domain/models"
	"golang.org/x/crypto/bcrypt"
)

func (server *Server) handleHealthz(response http.ResponseWriter, request *http.Request, requestID string) bool {
	if request.URL.Path != server.Management.Paths.Healthz {
		return false
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		server.writeProblem(response, http.StatusMethodNotAllowed, "method_not_allowed", "health endpoint accepts GET and HEAD", requestID)
		return true
	}
	server.writeJSON(response, http.StatusOK, map[string]any{server.Management.JSON.Status: server.Management.Statuses.OK, server.Management.JSON.RequestID: requestID})
	return true
}

func (server *Server) authorizeManagementRequest(response http.ResponseWriter, request *http.Request, requestID string) (string, bool) {
	if server.RequireClientCertificate && (request.TLS == nil || len(request.TLS.PeerCertificates) == 0) {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementMTLSRequired, requestID)
		return "", false
	}
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

func (server *Server) handleReadiness(response http.ResponseWriter, request *http.Request, requestID string) {
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
}

func (server *Server) handleAuditList(response http.ResponseWriter, request *http.Request, requestID string) {
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
}

func (server *Server) handleOperationGet(response http.ResponseWriter, request *http.Request, path, requestID string) {
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
