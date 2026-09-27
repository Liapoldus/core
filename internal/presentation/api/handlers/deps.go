package handlers

import (
	"context"
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

type GroupDependencies struct {
	GroupService        application.GroupService
	GroupReleases       *application.GroupReleaseService
	GroupReleasePolicy  models.GroupReleasePolicy
	Management          config.ManagementWords
	AuditWords          config.AuditWords
	RecordAudit         func(context.Context, string, string, string, string, string, string, string) error
	WriteJSON           func(http.ResponseWriter, int, any)
	WriteProblem        func(http.ResponseWriter, int, string, string, string)
	WriteCatalogProblem func(http.ResponseWriter, string, string)
}

type CaddyDependencies struct {
	AdminMutations      *application.AdminMutationService
	AdminWords          config.AdminMutationWords
	Management          config.ManagementWords
	WriteCatalogProblem func(http.ResponseWriter, string, string)
}
