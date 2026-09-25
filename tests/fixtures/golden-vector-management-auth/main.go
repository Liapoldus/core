package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"os"
	"net/http"
	"net/http/httptest"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/presentation/api"
)

type vector struct {
	Input struct {
		ClientCertificate bool `json:"clientCertificate"`
		Bearer            bool `json:"bearer"`
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
		Token:                    "golden-vector-management-token",
		Management:               management,
		Errors:                   errorCatalog,
		RequireClientCertificate: true,
	}
	request := httptest.NewRequest(management.Methods.Get, management.Paths.Status, nil)
	if input.Input.ClientCertificate {
		request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{}}}
	}
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
