package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/presentation/api"
)

type vector struct {
	Input struct {
		Bearer bool `json:"bearer"`
	} `json:"input"`
}

type observation struct {
	Authorized bool `json:"authorized"`
	Status     int  `json:"status"`
}

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	contents, err := os.ReadFile(os.Args[1])
	if err != nil {
		os.Exit(2)
	}
	var input vector
	if err := json.Unmarshal(contents, &input); err != nil {
		os.Exit(2)
	}
	management, err := config.LoadManagement()
	if err != nil {
		os.Exit(2)
	}
	errorCatalog, err := config.LoadErrorCatalog()
	if err != nil {
		os.Exit(2)
	}
	server := &api.Server{
		Token:      "golden-vector-management-token",
		Management: management,
		Errors:     errorCatalog,
	}
	request := httptest.NewRequest(management.Methods.Get, management.Paths.Status, nil)
	if input.Input.Bearer {
		request.Header.Set("Authorization", "Bearer golden-vector-management-token")
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	result := observation{Authorized: response.Code < http.StatusBadRequest, Status: response.Code}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		os.Exit(2)
	}
}
