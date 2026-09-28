// Package api adapts the Management API to application use cases.
package api

import (
	"context"
	"crypto/tls"
	"net/http"
	"sync"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/internal/presentation/api/handlers"
)

type Server struct {
	Token                    string
	ServiceAccounts          []models.ServiceAccount
	mu                       sync.RWMutex
	Operations               application.OperationService
	AdminSurfaces            []handlers.AdminSurface
	AdminDispatcher          *plugins.Dispatcher
	Plugins                  []any
	PluginIDField            string
	RestartPlugin            func(context.Context, string) (models.Operation, error)
	Audit                    *application.AuditService
	GroupService             application.GroupService
	GroupReleases            *application.GroupReleaseService
	CookiePolicies           *application.PluginCookiePolicyService
	GroupReleasePolicy       models.GroupReleasePolicy
	AdminMutations           *application.AdminMutationService
	AdminWords               config.AdminMutationWords
	AccessService            *application.AccessService
	CaddyVariant             string
	CaddyBuildID             string
	CaddyModules             []string
	DataPlaneState           string
	DataPlaneReason          string
	DataPlaneReadiness       func(context.Context) (string, string)
	AuditWords               config.AuditWords
	Management               config.ManagementWords
	Errors                   config.ErrorCatalog
	TLSConfig                *tls.Config
	RequireClientCertificate bool
	contractOnce             sync.Once
}

func (server *Server) Handler() http.Handler {
	server.contractOnce.Do(func() {
		if server.Management.Paths.Groups == "" {
			server.Management, _ = config.LoadManagement()
		}
		server.Errors, _ = config.LoadErrorCatalog()
		if server.AdminWords.Paths.ManagementPrefix == "" {
			server.AdminWords, _ = config.LoadAdminMutation()
		}
		if server.GroupReleasePolicy.OperationKind == "" {
			server.GroupReleasePolicy, _ = config.LoadGroupRelease()
		}
	})
	return http.HandlerFunc(server.handle)
}

func (server *Server) Listen(ctx context.Context, address string, configurations ...*application.PluginConfigurationService) error {
	handler := server.Handler()
	if len(configurations) > 0 {
		handler = WithPluginConfigurations(handler, configurations[0])
	}
	httpServer := &http.Server{Addr: address, Handler: handler}
	result := make(chan error, 1)
	go func() {
		if server.TLSConfig != nil {
			httpServer.TLSConfig = server.TLSConfig
			result <- httpServer.ListenAndServeTLS("", "")
			return
		}
		result <- httpServer.ListenAndServe()
	}()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownContext)
	}
}
