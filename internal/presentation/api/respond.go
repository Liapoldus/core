package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
)

func (server *Server) writeJSON(response http.ResponseWriter, status int, value any) {
	server.writeResponse(response, status, server.Management.ContentTypes.JSON, value)
}

func (server *Server) writeProblem(response http.ResponseWriter, status int, code, detail, requestID string) {
	server.writeResponse(response, status, server.Management.ContentTypes.Problem, map[string]any{"type": "about:blank", "title": code, server.Management.JSON.Status: status, "code": code, "detail": detail, "instance": "", server.Management.JSON.RequestID: requestID})
}

func (server *Server) writeResponse(response http.ResponseWriter, status int, contentType string, value any) {
	response.Header().Set("Content-Type", contentType)
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func (server *Server) writeCatalogProblem(response http.ResponseWriter, code, requestID string) {
	problem, exists := server.Errors.Lookup(code)
	if !exists {
		server.writeProblem(response, http.StatusInternalServerError, "config_invalid", "management error contract is invalid", requestID)
		return
	}
	server.writeProblem(response, problem.Status, problem.Code, problem.Detail, requestID)
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
