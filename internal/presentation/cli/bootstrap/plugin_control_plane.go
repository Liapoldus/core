package bootstrap

import (
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

// pluginRegistry is the operator-declared plugin topology loaded from core.yaml.
// Endpoint and expected identity are read from this file only: Core never
// discovers, accepts or negotiates them from a plugin.
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
		bootstrapConfig.PluginReplicaServerCA == "" || len(bootstrapConfig.Plugins) == 0 {
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
func replicaDialTLS(bootstrapConfig config.BootstrapConfig, serverName string) (*tls.Config, error) {
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
	return &tls.Config{
			MinVersion:       tls.VersionTLS12,
			Certificates:     []tls.Certificate{certificate},
			RootCAs:          roots,
			ServerName:       serverName,
			VerifyConnection: verifyRevocation,
		},
		nil
}

// replicaIdentityResolver maps a verified replica client certificate to the
// instance whose generation it may pull. Only identities the operator declared
// in core.yaml are honoured, so an unknown replica is refused rather than
// granted access to an instance that happens to exist.
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
// the exact-generation pull listener, the operator-declared identity resolver and
// one control client per declared replica. It is built from core.yaml alone, so
// an ordinary `core serve` connects to declared endpoints instead of dropping
// the pluginControl configuration.
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
	return &PluginRESTControl{
		ConfigPullListener: listener,
		ConfigPullTLS:      pullTLS,
		ResolveReplica:     replicaIdentityResolver(registry),
		ReloadClients:      clients,
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
			dialTLS, err := replicaDialTLS(bootstrapConfig, endpoint.Hostname())
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
