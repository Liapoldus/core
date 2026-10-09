package bootstrap

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"os"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
)

var (
	errPluginControlTrustInvalid = errors.New("plugin control TLS trust configuration is invalid")
	errPluginControlListen       = errors.New("plugin control listener address is not usable")
)

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
		return nil, errPluginControlTrustInvalid
	}
	return &tls.Config{
		MinVersion:       tls.VersionTLS12,
		Certificates:     []tls.Certificate{certificate},
		ClientCAs:        roots,
		ClientAuth:       tls.RequireAndVerifyClientCert,
		VerifyConnection: verifyRevocation,
	}, nil
}

// replicaDialTLS builds the configuration Core presents when it dials a live
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
		return nil, errPluginControlTrustInvalid
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

// buildPluginRESTControl assembles the production Plugin SDK REST control plane:
// the exact-generation pull listener and the lease-backed identity resolver.
func buildPluginRESTControl(bootstrapConfig config.BootstrapConfig) (*PluginRESTControl, error) {
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
	httpContract, err := plugins.LoadSDKHTTPContract()
	if err != nil {
		_ = listener.Close()
		return nil, errPluginControlTrustInvalid
	}
	replicaLifecycle, err := plugins.LoadSDKReplicaLifecycleContract()
	if err != nil {
		_ = listener.Close()
		return nil, errPluginControlTrustInvalid
	}
	replicaDirectory, err := plugins.NewPluginReplicaDirectory(replicaLifecycle, nil)
	if err != nil {
		_ = listener.Close()
		return nil, errPluginControlTrustInvalid
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
		target := plugins.RegisteredReplicaTarget{
			InstanceID:                 registration.Identity.InstanceID,
			ReplicaID:                  registration.Identity.ReplicaID,
			Endpoint:                   endpoint,
			ExpectedResourceIdentifier: identityURI,
			Transport:                  &http.Transport{TLSClientConfig: dialTLS},
		}
		clients, release, err := plugins.RegisteredReplicaFanouts([]plugins.RegisteredReplicaTarget{target})
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
		RegisteredReloads:  registeredReloads,
		HTTPContract:       httpContract,
	}, nil
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
		return nil, errPluginControlTrustInvalid
	}
	return roots, nil
}
