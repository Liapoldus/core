package handlers

import (
	"net/http"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/infrastructure/config"
)

type Dependencies struct {
	AccessService       *application.AccessService
	Management          config.ManagementWords
	AuditWords          config.AuditWords
	GenerateServiceKey  func(int, int) (string, string, []byte, error)
	WriteJSON           func(http.ResponseWriter, int, any)
	WriteProblem        func(http.ResponseWriter, int, string, string, string)
	WriteCatalogProblem func(http.ResponseWriter, string, string)
}
