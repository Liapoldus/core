package runtime

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"net"
	"time"

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/domain/interfaces"
	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/infrastructure/security"
	"github.com/Liapoldus/core/v3/internal/infrastructure/services"
	"github.com/Liapoldus/core/v3/internal/presentation/api"
)

type ServiceRESTControl struct {
	ConfigPullListener  net.Listener
	ConfigPullTLS       *tls.Config
	ResolveReplica      interfaces.ServiceReplicaIdentityResolver
	ReplicaDirectory    *services.ServiceReplicaDirectory
	ReplicaLifecycle    services.SDKReplicaLifecycleContract
	RegisterReplica     func(context.Context, services.SDKReplicaRegistrationRequest) error
	OnReplicaRegistered func(context.Context, string, string) error
	RegisteredReloads   *services.RegisteredReplicaReloadResolver
	HTTPContract        services.SDKHTTPContract
	// PeerDirectory supplies the Service SDK v2 authenticated peer-directory
	// poll. When PeerDirectoryPoll is zero the endpoint is not mounted, which
	// keeps the v1 control listener unchanged for deployments without v2
	// registrations.
	PeerDirectoryPoll    services.SDKPeerDirectoryPollContract
	PeerDirectoryRules   func(callerInstanceID string) []models.PeerLinkRule
	PeerDirectoryChanges func() (<-chan struct{}, func())
	PeerDirectoryTTL     time.Duration
	// CloseReleases closes idle connections opened for live registration leases.
	CloseReleases func()
}

var errInvalidServiceRESTControl = errors.New("invalid service REST control configuration")

// startServiceRESTControl serves the Service SDK private control endpoints for
// exact-generation config pulls and scoped secret grants. It never starts,
// supervises, or relaunches a service process.
func startServiceRESTControl(configuration *ServiceRESTControl, store interfaces.ServiceRetainedConfigurationReader, references interfaces.ServiceSecretReferenceStore, secretRoot string) (func(), error) {
	if configuration == nil {
		return nil, errInvalidServiceRESTControl
	}
	if configuration.ConfigPullListener == nil && configuration.ConfigPullTLS == nil {
		return func() {
			if configuration.CloseReleases != nil {
				configuration.CloseReleases()
			}
		}, nil
	}
	if configuration.ConfigPullListener == nil || configuration.ConfigPullTLS == nil || configuration.ReplicaDirectory == nil || configuration.RegisterReplica == nil ||
		configuration.ConfigPullTLS.ClientAuth != tls.RequireAndVerifyClientCert || configuration.ConfigPullTLS.ClientCAs == nil ||
		configuration.ConfigPullTLS.MinVersion < tls.VersionTLS12 ||
		len(configuration.ConfigPullTLS.Certificates) == 0 && configuration.ConfigPullTLS.GetCertificate == nil {
		return nil, errInvalidServiceRESTControl
	}
	contract, err := services.LoadSDKHTTPContract()
	if err != nil || secretRoot == "" || references == nil {
		return nil, errInvalidServiceRESTControl
	}
	grantPolicy, err := config.LoadServiceSecretGrantPolicy()
	if err != nil {
		return nil, errInvalidServiceRESTControl
	}
	grantService := &application.ServiceSecretGrantService{
		Configurations: store,
		References:     references,
		ResolveSecret: func(ctx context.Context, reference string, maximumBytes int64) ([]byte, error) {
			return security.ReadStagedServiceSecret(secretRoot, reference, maximumBytes)
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
		return nil, errInvalidServiceRESTControl
	}
	handler, err := api.NewServiceSDKControlHandler(store, configuration.ResolveReplica, grantService)
	if err != nil {
		return nil, errInvalidServiceRESTControl
	}
	if configuration.PeerDirectoryPoll.ContractVersion != "" {
		if configuration.PeerDirectoryRules == nil || configuration.PeerDirectoryChanges == nil || configuration.PeerDirectoryTTL <= 0 {
			return nil, errInvalidServiceRESTControl
		}
		handler, err = api.NewServicePeerDirectoryHandler(
			configuration.PeerDirectoryPoll, configuration.ReplicaDirectory, configuration.PeerDirectoryRules,
			configuration.PeerDirectoryChanges, time.Now, configuration.PeerDirectoryTTL, handler,
		)
		if err != nil {
			return nil, errInvalidServiceRESTControl
		}
	}
	if configuration.OnReplicaRegistered == nil {
		handler, err = api.NewServiceReplicaLifecycleHandler(configuration.ReplicaLifecycle, configuration.ReplicaDirectory, configuration.RegisterReplica, handler)
	} else {
		handler, err = api.NewServiceReplicaLifecycleHandler(configuration.ReplicaLifecycle, configuration.ReplicaDirectory, configuration.RegisterReplica, handler, configuration.OnReplicaRegistered)
	}
	if err != nil {
		return nil, errInvalidServiceRESTControl
	}
	done := make(chan error, 1)
	go func() {
		done <- api.ServeServiceConfigurationPull(configuration.ConfigPullListener, handler, configuration.ConfigPullTLS)
	}()
	return func() {
		_ = configuration.ConfigPullListener.Close()
		<-done
		if configuration.CloseReleases != nil {
			configuration.CloseReleases()
		}
	}, nil
}
