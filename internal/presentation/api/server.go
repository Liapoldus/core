// Package api adapts the Management API to application use cases.
package api

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/domain/interfaces"
	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/infrastructure/plugins"
)

type Server struct {
	CoreSettings       interfaces.CoreSettings
	ValidateSettings   func([]byte) error
	Token              string
	ServiceAccounts    []models.ServiceAccount
	mu                 sync.RWMutex
	Operations         application.OperationService
	ConfigBundles      *application.ConfigBundleService
	PluginAdminControl *plugins.SDKAdminControl
	PluginLinks        *application.PluginLinkPolicyService
	TrafficRollouts    *application.TrafficRolloutService
	TrafficRolloutAPI  config.TrafficRolloutAPIContract
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
	return server.ListenWithReady(ctx, address, nil, configurations...)
}

// ListenWithReady binds the management listener before reporting readiness.
// The optional ready channel is used by the public Core host composition so a
// caller never observes a ready host before its management socket exists.
func (server *Server) ListenWithReady(ctx context.Context, address string, ready chan<- struct{}, configurations ...*application.PluginConfigurationService) error {
	handler := server.Handler()
	if len(configurations) > 0 {
		handler = WithPluginConfigurations(handler, configurations[0])
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	httpServer := &http.Server{Addr: address, Handler: handler}
	if server.TLSConfig != nil {
		listener = tls.NewListener(listener, server.TLSConfig)
	}
	if ready != nil {
		close(ready)
	}
	result := make(chan error, 1)
	go func() { result <- httpServer.Serve(listener) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownContext)
	}
}
