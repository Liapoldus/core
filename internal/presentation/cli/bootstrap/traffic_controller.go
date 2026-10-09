package bootstrap

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/internal/presentation/api"
)

func trafficControllerTLS(configuration config.TrafficControllerConfig) (*tls.Config, error) {
	certificate, err := loadKeyPair(configuration.Certificate, configuration.Key)
	if err != nil {
		return nil, errPluginControlTrustInvalid
	}
	roots, err := certPool(configuration.ClientCA)
	if err != nil {
		return nil, errPluginControlTrustInvalid
	}
	verifyRevocation, err := plugins.NewTLSRevocationVerifier(configuration.ClientCA, configuration.ClientCRLs)
	if err != nil {
		return nil, errPluginControlTrustInvalid
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate},
		ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert, VerifyConnection: verifyRevocation,
	}, nil
}

func startTrafficController(
	configuration *config.TrafficControllerConfig,
	service *application.TrafficRolloutService,
	apiContract config.TrafficRolloutAPIContract,
) (func(), <-chan error, error) {
	if configuration == nil {
		return func() {}, nil, nil
	}
	tlsConfig, err := trafficControllerTLS(*configuration)
	if err != nil {
		return nil, nil, err
	}
	listener, err := net.Listen("tcp", configuration.Listen)
	if err != nil {
		return nil, nil, errPluginControlListen
	}
	handler, err := api.NewTrafficControllerHandler(service, configuration.AllowedIdentities, apiContract)
	if err != nil {
		_ = listener.Close()
		return nil, nil, err
	}
	server := &http.Server{Handler: handler, TLSConfig: tlsConfig,
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(tls.NewListener(listener, tlsConfig)) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(ctx)
			select {
			case <-done:
			case <-time.After(6 * time.Second):
				_ = listener.Close()
			}
		})
	}
	return stop, done, nil
}

func serveManagementAndTrafficController(
	ctx context.Context,
	management *api.Server,
	managementAddress string,
	configuration *application.PluginConfigurationService,
	stopTrafficController func(),
	trafficControllerDone <-chan error,
) error {
	managementDone := make(chan error, 1)
	go func() { managementDone <- management.Listen(ctx, managementAddress, configuration) }()
	if trafficControllerDone == nil {
		return <-managementDone
	}
	select {
	case err := <-managementDone:
		stopTrafficController()
		return err
	case err := <-trafficControllerDone:
		stopTrafficController()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
