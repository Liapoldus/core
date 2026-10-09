package bootstrap

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"net"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/internal/presentation/api"
)

type PluginRESTControl struct {
	ConfigPullListener  net.Listener
	ConfigPullTLS       *tls.Config
	ResolveReplica      interfaces.PluginReplicaIdentityResolver
	ReplicaDirectory    *plugins.PluginReplicaDirectory
	ReplicaLifecycle    plugins.SDKReplicaLifecycleContract
	RegisterReplica     func(context.Context, plugins.SDKReplicaRegistrationRequest) error
	OnReplicaRegistered func(context.Context, string, string) error
	ReloadClients       map[string]plugins.SDKReloadClient
	RegisteredReloads   *plugins.RegisteredReplicaReloadResolver
	HTTPContract        plugins.SDKHTTPContract
	// PeerDirectory supplies the Plugin SDK v2 authenticated peer-directory
	// poll. When PeerDirectoryPoll is zero the endpoint is not mounted, which
	// keeps the v1 control listener unchanged for deployments without v2
	// registrations.
	PeerDirectoryPoll    plugins.SDKPeerDirectoryPollContract
	PeerDirectoryRules   func(callerInstanceID string) []models.PeerLinkRule
	PeerDirectoryChanges func() (<-chan struct{}, func())
	PeerDirectoryTTL     time.Duration
	// CloseReleases dials idle keep-alive connections to declared replicas on
	// shutdown. A plugin is never restarted by Core, so leaving those sockets
	// open would keep TLS sessions to a replica alive after Core stopped.
	CloseReleases func()
}

var errInvalidPluginRESTControl = errors.New("invalid plugin REST control configuration")

// startPluginRESTControl serves the Plugin SDK private control endpoints for
// exact-generation config pulls and scoped secret grants. It never starts,
// supervises, or relaunches a plugin process.
func startPluginRESTControl(configuration *PluginRESTControl, store interfaces.PluginRetainedConfigurationReader, bootstrapPath string) (func(), error) {
	if configuration == nil {
		return nil, errInvalidPluginRESTControl
	}
	if configuration.ConfigPullListener == nil || configuration.ConfigPullTLS == nil || configuration.ReplicaDirectory == nil || configuration.RegisterReplica == nil ||
		configuration.ConfigPullTLS.ClientAuth != tls.RequireAndVerifyClientCert || configuration.ConfigPullTLS.ClientCAs == nil ||
		configuration.ConfigPullTLS.MinVersion < tls.VersionTLS12 ||
		len(configuration.ConfigPullTLS.Certificates) == 0 && configuration.ConfigPullTLS.GetCertificate == nil {
		return nil, errInvalidPluginRESTControl
	}
	contract, err := plugins.LoadSDKHTTPContract()
	if err != nil || bootstrapPath == "" {
		return nil, errInvalidPluginRESTControl
	}
	grantPolicy, err := config.LoadPluginSecretGrantPolicy()
	if err != nil {
		return nil, errInvalidPluginRESTControl
	}
	grantService := &application.PluginSecretGrantService{
		Configurations: store,
		ResolveSecret: func(ctx context.Context, reference string, maximumBytes int64) ([]byte, error) {
			return config.ReadSecretReference(bootstrapPath, reference, maximumBytes)
		},
		GrantTTL:            time.Duration(contract.Deadlines.CoreSecretGrantSeconds) * time.Second,
		MaximumValueBytes:   contract.Core.SecretGrant.Redemption.MaximumResponseBytes,
		MaximumOutstanding:  grantPolicy.MaximumOutstanding,
		MaximumReferenceLen: contract.Core.SecretGrant.ReferenceMaximumLength,
		MaximumPurposeLen:   contract.Core.SecretGrant.PurposeMaximumLength,
		Now:                 time.Now,
		Random:              rand.Reader,
	}
	if grantService.GrantTTL <= 0 || grantService.MaximumOutstanding <= 0 || grantService.MaximumReferenceLen <= 0 || grantService.MaximumPurposeLen <= 0 {
		return nil, errInvalidPluginRESTControl
	}
	handler, err := api.NewPluginSDKControlHandler(store, configuration.ResolveReplica, grantService)
	if err != nil {
		return nil, errInvalidPluginRESTControl
	}
	if configuration.PeerDirectoryPoll.ContractVersion != "" {
		if configuration.PeerDirectoryRules == nil || configuration.PeerDirectoryChanges == nil || configuration.PeerDirectoryTTL <= 0 {
			return nil, errInvalidPluginRESTControl
		}
		handler, err = api.NewPluginPeerDirectoryHandler(
			configuration.PeerDirectoryPoll, configuration.ReplicaDirectory, configuration.PeerDirectoryRules,
			configuration.PeerDirectoryChanges, time.Now, configuration.PeerDirectoryTTL, handler,
		)
		if err != nil {
			return nil, errInvalidPluginRESTControl
		}
	}
	if configuration.OnReplicaRegistered == nil {
		handler, err = api.NewPluginReplicaLifecycleHandler(configuration.ReplicaLifecycle, configuration.ReplicaDirectory, configuration.RegisterReplica, handler)
	} else {
		handler, err = api.NewPluginReplicaLifecycleHandler(configuration.ReplicaLifecycle, configuration.ReplicaDirectory, configuration.RegisterReplica, handler, configuration.OnReplicaRegistered)
	}
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
