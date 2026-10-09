package bootstrap

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"os"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
)

// pluginRegistry contains optional legacy declarations used only by the offline
// migration path. Runtime membership is established by authenticated SDK
// registration and leases stored by the Core control plane.
type pluginRegistry struct {
	instances []config.PluginInstanceConfig
}

var (
	errPluginRegistryIncomplete = errors.New("plugin registry requires pluginControl trust material and declared replicas")
	errPluginControlListen      = errors.New("plugin control listener address is not usable")
)

// newPluginRegistry converts the bootstrap registry into the shape the control
// plane needs. Every declared instance must resolve to at least one replica and
// every replica must carry a usable HTTPS endpoint, because Core refuses to
// start a control plane that cannot reach part of what the operator declared.
func newPluginRegistry(bootstrapConfig config.BootstrapConfig) (pluginRegistry, error) {
	if bootstrapConfig.PluginControlListen == "" || bootstrapConfig.PluginControlCertificate == "" ||
		bootstrapConfig.PluginControlKey == "" || bootstrapConfig.PluginReplicaClientCA == "" ||
		bootstrapConfig.PluginReplicaServerCA == "" {
		return pluginRegistry{}, errPluginRegistryIncomplete
	}
	registry := pluginRegistry{instances: append([]config.PluginInstanceConfig(nil), bootstrapConfig.Plugins...)}
	for _, instance := range registry.instances {
		if instance.InstanceID == "" || len(instance.Replicas) == 0 {
			return pluginRegistry{}, errPluginRegistryIncomplete
		}
		for _, replica := range instance.Replicas {
			if _, err := config.PluginEndpoint(replica.Endpoint); err != nil {
				return pluginRegistry{}, err
			}
		}
	}
	return registry, nil
}

// pluginControlTLS builds the exact-generation pull listener configuration. It
// serves Core's own keypair and demands a verifiable replica client certificate
// against the dedicated replica trust root, which is independent from the
// Management client CA and from plugin-to-plugin roots.
func pluginControlTLS(bootstrapConfig config.BootstrapConfig) (*tls.Config, error) {
	certificate, err := loadKeyPair(bootstrapConfig.PluginControlCertificate, bootstrapConfig.PluginControlKey)
	if err != nil {
		return nil, err
	}
	roots, err := certPool(bootstrapConfig.PluginReplicaClientCA)
	if err != nil {
		return nil, err
	}
	verifyRevocation, err := plugins.NewTLSRevocationVerifier(bootstrapConfig.PluginReplicaClientCA, bootstrapConfig.PluginReplicaClientCRLs)
	if err != nil {
		return nil, errPluginRegistryIncomplete
	}
	return &tls.Config{
		MinVersion:       tls.VersionTLS12,
		Certificates:     []tls.Certificate{certificate},
		ClientCAs:        roots,
		ClientAuth:       tls.RequireAndVerifyClientCert,
		VerifyConnection: verifyRevocation,
	}, nil
}

// replicaDialTLS builds the configuration Core presents when it dials a declared
// replica. Core authenticates with the same keypair it serves on the pull
// listener and verifies the replica against replicaServerCA.
func replicaDialTLS(bootstrapConfig config.BootstrapConfig, serverName string, identity config.PeerIdentityConfig) (*tls.Config, error) {
	certificate, err := loadKeyPair(bootstrapConfig.PluginControlCertificate, bootstrapConfig.PluginControlKey)
	if err != nil {
		return nil, err
	}
	roots, err := certPool(bootstrapConfig.PluginReplicaServerCA)
	if err != nil {
		return nil, err
	}
	verifyRevocation, err := plugins.NewTLSRevocationVerifier(bootstrapConfig.PluginReplicaServerCA, bootstrapConfig.PluginReplicaServerCRLs)
	if err != nil {
		return nil, errPluginRegistryIncomplete
	}
	verifyIdentity := plugins.PinnedReplicaIdentityVerifier(
		identity.CommonName,
		identity.UniformResourceIdentifier,
		verifyRevocation,
	)
	return &tls.Config{
			MinVersion:       tls.VersionTLS12,
			Certificates:     []tls.Certificate{certificate},
			RootCAs:          roots,
			ServerName:       serverName,
			VerifyConnection: verifyIdentity,
		},
		nil
}

// replicaIdentityResolver is retained for migration diagnostics. Runtime
// authorization uses PluginReplicaDirectory leases instead.
func replicaIdentityResolver(registry pluginRegistry) interfaces.PluginReplicaIdentityResolver {
	expected := make(map[string]string)
	for _, instance := range registry.instances {
		for _, replica := range instance.Replicas {
			for _, candidate := range identityCandidates(replica.ExpectedPeerIdentity) {
				expected[candidate] = instance.InstanceID
			}
		}
	}
	return func(certificate *x509.Certificate) (string, bool) {
		if certificate == nil {
			return "", false
		}
		if instanceID, ok := expected[certificate.Subject.CommonName]; ok {
			return instanceID, true
		}
		for _, identifier := range certificate.URIs {
			if instanceID, ok := expected[identifier.String()]; ok {
				return instanceID, true
			}
		}
		return "", false
	}
}

// identityCandidates returns the exact strings a replica certificate may present
// for one declared identity. Both entries are exact-match values; neither is a
// pattern and an absent URI never widens the set.
func identityCandidates(identity config.PeerIdentityConfig) []string {
	if identity.UniformResourceIdentifier == "" {
		return []string{identity.CommonName}
	}
	return []string{identity.CommonName, identity.UniformResourceIdentifier}
}

// buildPluginRESTControl assembles the production Plugin SDK REST control plane:
// the exact-generation pull listener and the lease-backed identity resolver.
// Static endpoint declarations are optional and are used only during migration.
func buildPluginRESTControl(bootstrapConfig config.BootstrapConfig) (*PluginRESTControl, error) {
	registry, err := newPluginRegistry(bootstrapConfig)
	if err != nil {
		return nil, err
	}
	pullTLS, err := pluginControlTLS(bootstrapConfig)
	if err != nil {
		return nil, err
	}
	listenAddress, err := net.ResolveTCPAddr("tcp", bootstrapConfig.PluginControlListen)
	if err != nil {
		return nil, errPluginControlListen
	}
	listener, err := net.Listen("tcp", listenAddress.String())
	if err != nil {
		return nil, err
	}
	clients, release, err := replicaReloadClients(bootstrapConfig, registry)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	httpContract, err := plugins.LoadSDKHTTPContract()
	if err != nil {
		_ = listener.Close()
		release()
		return nil, errPluginRegistryIncomplete
	}
	replicaLifecycle, err := plugins.LoadSDKReplicaLifecycleContract()
	if err != nil {
		_ = listener.Close()
		release()
		return nil, errPluginRegistryIncomplete
	}
	replicaDirectory, err := plugins.NewPluginReplicaDirectory(replicaLifecycle, nil)
	if err != nil {
		_ = listener.Close()
		release()
		return nil, errPluginRegistryIncomplete
	}
	registeredReloads := plugins.NewRegisteredReplicaReloadResolver(replicaDirectory, func(ctx context.Context, live plugins.LivePluginReplica) (plugins.SDKReloadClient, func(), error) {
		registration := live.Registration
		if registration.Identity.InstanceID == "" || registration.Identity.ReplicaID == "" {
			return nil, nil, plugins.ErrPluginUnavailable
		}
		endpoint, err := config.PluginEndpoint(registration.RestEndpoint)
		if err != nil {
			return nil, nil, plugins.ErrPluginUnavailable
		}
		identityURI, err := replicaLifecycle.ReplicaIdentityURI(registration.Identity)
		if err != nil {
			return nil, nil, plugins.ErrPluginUnavailable
		}
		dialTLS, err := replicaDialTLS(bootstrapConfig, endpoint.Hostname(), config.PeerIdentityConfig{UniformResourceIdentifier: identityURI})
		if err != nil {
			return nil, nil, plugins.ErrPluginUnavailable
		}
		declared := plugins.DeclaredReplica{
			InstanceID:                 registration.Identity.InstanceID,
			ReplicaID:                  registration.Identity.ReplicaID,
			Endpoint:                   endpoint,
			ExpectedResourceIdentifier: identityURI,
			Transport:                  &http.Transport{TLSClientConfig: dialTLS},
		}
		clients, release, err := plugins.DeclaredReplicaFanouts([]plugins.DeclaredReplica{declared})
		if err != nil {
			return nil, nil, plugins.ErrPluginUnavailable
		}
		fanout, ok := clients[registration.Identity.InstanceID].(*plugins.SDKReloadFanout)
		if !ok || len(fanout.Replicas) != 1 || fanout.Replicas[0].ReplicaID != registration.Identity.ReplicaID {
			release()
			return nil, nil, plugins.ErrPluginUnavailable
		}
		return fanout.Replicas[0].Client, release, nil
	}, nil)
	registeredReloads.ValidateSchema = config.ValidateJSONSchemaDocument
	resolveReplica := func(certificate *x509.Certificate) (string, bool) {
		return replicaDirectory.Resolve(certificate)
	}
	return &PluginRESTControl{
		ConfigPullListener: listener,
		ConfigPullTLS:      pullTLS,
		ResolveReplica:     resolveReplica,
		ReplicaDirectory:   replicaDirectory,
		ReplicaLifecycle:   replicaLifecycle,
		ReloadClients:      clients,
		RegisteredReloads:  registeredReloads,
		HTTPContract:       httpContract,
		CloseReleases:      release,
	}, nil
}

// replicaReloadClients reduces the declared registry to the verified transport
// and identity each replica needs, then hands the whole set to the Plugin SDK
// adapter. TLS and endpoint resolution stay here because they are Core-owned
// infrastructure; building SDK control clients belongs to the plugins adapter.
func replicaReloadClients(bootstrapConfig config.BootstrapConfig, registry pluginRegistry) (map[string]plugins.SDKReloadClient, func(), error) {
	declared := make([]plugins.DeclaredReplica, 0, len(registry.instances))
	for _, instance := range registry.instances {
		for _, replica := range instance.Replicas {
			endpoint, err := config.PluginEndpoint(replica.Endpoint)
			if err != nil {
				return nil, nil, err
			}
			dialTLS, err := replicaDialTLS(bootstrapConfig, endpoint.Hostname(), replica.ExpectedPeerIdentity)
			if err != nil {
				return nil, nil, err
			}
			declared = append(declared, plugins.DeclaredReplica{
				InstanceID:                 instance.InstanceID,
				ReplicaID:                  replica.ReplicaID,
				Endpoint:                   endpoint,
				ExpectedCommonName:         replica.ExpectedPeerIdentity.CommonName,
				ExpectedResourceIdentifier: replica.ExpectedPeerIdentity.UniformResourceIdentifier,
				Transport:                  &http.Transport{TLSClientConfig: dialTLS},
			})
		}
	}
	return plugins.DeclaredReplicaFanouts(declared)
}

func loadKeyPair(certificatePath, keyPath string) (tls.Certificate, error) {
	certificatePEM, err := os.ReadFile(certificatePath)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certificatePEM, keyPEM)
}

func certPool(path string) (*x509.CertPool, error) {
	caPEM, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errPluginRegistryIncomplete
	}
	return roots, nil
}
