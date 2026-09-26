package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/presentation/api"
)

func main() {
	management, err := config.LoadManagement()
	if err != nil {
		panic(err)
	}
	server := (&api.Server{Token: "fixture-token", Management: management}).Handler()
	first := perform(server, http.MethodPut, `{"allowedNames":["session"]}`, `"0"`)
	stale := perform(server, http.MethodPut, `{"allowedNames":["theme"]}`, `"0"`)
	current := perform(server, http.MethodGet, "", "")
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"firstPutStatus": first.Code,
		"firstPutETag": first.Header().Get("ETag"),
		"stalePutStatus": stale.Code,
		"getStatus": current.Code,
		"getETag": current.Header().Get("ETag"),
	}); err != nil {
		panic(err)
	}
}

func perform(handler http.Handler, method, body, etag string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/api/plugins/fixture/cookie-policies/forms.submit", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer fixture-token")
	if etag != "" {
		request.Header.Set("If-Match", etag)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
