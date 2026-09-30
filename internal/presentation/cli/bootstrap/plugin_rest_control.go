package bootstrap

import (
	"crypto/tls"
	"errors"
	"net"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/internal/presentation/api"
)

type PluginRESTControl struct {
	ConfigPullListener net.Listener
	ConfigPullTLS      *tls.Config
	ResolveReplica     interfaces.PluginReplicaIdentityResolver
	ReloadClients      map[string]plugins.SDKReloadClient
	// CloseReleases dials idle keep-alive connections to declared replicas on
	// shutdown. A plugin is never restarted by Core, so leaving those sockets
	// open would keep TLS sessions to a replica alive after Core stopped.
	CloseReleases func()
}

var errInvalidPluginRESTControl = errors.New("invalid plugin REST control configuration")

// startPluginRESTControl serves the exact-generation configuration pull
// endpoint. It is the only Core-to-plugin configuration path; it never starts,
// supervises, or relaunches a plugin process.
func startPluginRESTControl(configuration *PluginRESTControl, store interfaces.PluginConfigurationStore) (func(), error) {
	if configuration == nil {
		return nil, errInvalidPluginRESTControl
	}
	if configuration.ConfigPullListener == nil || configuration.ConfigPullTLS == nil ||
		configuration.ConfigPullTLS.ClientAuth != tls.RequireAndVerifyClientCert || configuration.ConfigPullTLS.ClientCAs == nil ||
		configuration.ConfigPullTLS.MinVersion < tls.VersionTLS12 ||
		len(configuration.ConfigPullTLS.Certificates) == 0 && configuration.ConfigPullTLS.GetCertificate == nil {
		return nil, errInvalidPluginRESTControl
	}
	handler, err := api.NewPluginConfigurationPullHandler(store, configuration.ResolveReplica)
	if err != nil {
		return nil, errInvalidPluginRESTControl
	}
	done := make(chan error, 1)
	go func() {
		done <- api.ServePluginConfigurationPull(configuration.ConfigPullListener, handler, configuration.ConfigPullTLS)
	}()
	return func() {
		_ = configuration.ConfigPullListener.Close()
		<-done
		if configuration.CloseReleases != nil {
			configuration.CloseReleases()
		}
	}, nil
}
