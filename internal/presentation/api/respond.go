package api

import (
	"encoding/json"
	"net/http"
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
