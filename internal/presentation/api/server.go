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
)

type Server struct {
	Token              string
	ServiceAccounts    []models.ServiceAccount
	mu                 sync.RWMutex
	Operations         application.OperationService
	PluginAdminControl *plugins.SDKAdminControl
	Plugins            []any
	PluginIDField      string
	Audit              *application.AuditService
	AccessService      *application.AccessService
	DataPlaneState     string
	DataPlaneReason    string
	DataPlaneReadiness func(context.Context) (string, string)
	DataPlaneDrift     func(context.Context) bool
	AuditWords         config.AuditWords
	Management         config.ManagementWords
	Errors             config.ErrorCatalog
	TLSConfig          *tls.Config
	contractOnce       sync.Once
}

func (server *Server) Handler() http.Handler {
	server.contractOnce.Do(func() {
		if server.Management.Paths.Plugins == "" {
			server.Management, _ = config.LoadManagement()
		}
		server.Errors, _ = config.LoadErrorCatalog()
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
