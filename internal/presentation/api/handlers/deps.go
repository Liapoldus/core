package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
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

type PluginDependencies struct {
	Management          config.ManagementWords
	PluginIDField       string
	Plugins             []any
	AdminSurfaces       []AdminSurface
	Operations          application.OperationService
	RestartPlugin       func(context.Context, string) (models.Operation, error)
	DispatchAdmin       func(context.Context, string, string, string, string, string, string, json.RawMessage) (PluginAdminResult, error)
	WriteJSON           func(http.ResponseWriter, int, any)
	WriteProblem        func(http.ResponseWriter, int, string, string, string)
	WriteCatalogProblem func(http.ResponseWriter, string, string)
	WritePage           func(http.ResponseWriter, any, *http.Request, string)
}

type PluginAdminResult struct {
	Status      int
	ContentType string
	Body        []byte
}

type ManagementDependencies struct {
	Audit               *application.AuditService
	Operations          application.OperationService
	DataPlaneState      string
	DataPlaneReason     string
	DataPlaneReadiness  func(context.Context) (string, string)
	Management          config.ManagementWords
	AuditWords          config.AuditWords
	WriteJSON           func(http.ResponseWriter, int, any)
	WriteProblem        func(http.ResponseWriter, int, string, string, string)
	WriteCatalogProblem func(http.ResponseWriter, string, string)
}
