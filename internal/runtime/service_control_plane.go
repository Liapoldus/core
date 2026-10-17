package runtime

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/infrastructure/services"
)

var (
	errServiceControlTrustInvalid = errors.New("service control TLS trust configuration is invalid")
	errServiceControlListen       = errors.New("service control listener address is not usable")
)

// serviceControlTLS builds the exact-generation pull listener configuration. It
// serves Core's own keypair and demands a verifiable replica client certificate
// against the dedicated replica trust root, which is independent from the
// Management client CA and from service-to-service roots.
func serviceControlTLS(bootstrapConfig config.BootstrapConfig) (*tls.Config, error) {
	certificate, err := loadKeyPair(bootstrapConfig.ServiceControlCertificate, bootstrapConfig.ServiceControlKey)
	if err != nil {
		return nil, err
	}
	roots, err := certPool(bootstrapConfig.ServiceReplicaClientCA)
	if err != nil {
		return nil, err
	}
	verifyRevocation, err := services.NewTLSRevocationVerifier(bootstrapConfig.ServiceReplicaClientCA, bootstrapConfig.ServiceReplicaClientCRLs)
	if err != nil {
		return nil, errServiceControlTrustInvalid
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
	certificate, err := loadKeyPair(bootstrapConfig.ServiceControlCertificate, bootstrapConfig.ServiceControlKey)
	if err != nil {
		return nil, err
	}
	roots, err := certPool(bootstrapConfig.ServiceReplicaServerCA)
	if err != nil {
		return nil, err
	}
	verifyRevocation, err := services.NewTLSRevocationVerifier(bootstrapConfig.ServiceReplicaServerCA, bootstrapConfig.ServiceReplicaServerCRLs)
	if err != nil {
		return nil, errServiceControlTrustInvalid
	}
	verifyIdentity := services.PinnedReplicaIdentityVerifier(
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

// buildServiceRESTControl assembles the production Service SDK REST control plane:
// the exact-generation pull listener and the lease-backed identity resolver.
func buildServiceRESTControl(bootstrapConfig config.BootstrapConfig) (*ServiceRESTControl, error) {
	var pullTLS *tls.Config
	var listener net.Listener
	var err error

	pullTLS, err = serviceControlTLS(bootstrapConfig)
	if err != nil {
		return nil, err
	}
	listenAddress, resolveErr := net.ResolveTCPAddr("tcp", bootstrapConfig.ServiceControlListen)
	if resolveErr != nil {
		return nil, errServiceControlListen
	}
	listener, err = net.Listen("tcp", listenAddress.String())
	if err != nil {
		return nil, err
	}

	httpContract, err := services.LoadSDKHTTPContract()
	if err != nil {
		if listener != nil {
			_ = listener.Close()
		}
		return nil, errServiceControlTrustInvalid
	}
	replicaLifecycle, err := services.LoadSDKReplicaLifecycleContract()
	if err != nil {
		if listener != nil {
			_ = listener.Close()
		}
		return nil, errServiceControlTrustInvalid
	}
	replicaDirectory, err := services.NewServiceReplicaDirectory(replicaLifecycle, nil)
	if err != nil {
		_ = listener.Close()
		return nil, errServiceControlTrustInvalid
	}
	registeredReloads := services.NewRegisteredReplicaReloadResolver(replicaDirectory, func(ctx context.Context, live services.LiveServiceReplica) (services.SDKReloadClient, func(), error) {
		registration := live.Registration
		if registration.Identity.InstanceID == "" || registration.Identity.ReplicaID == "" {
			return nil, nil, services.ErrServiceUnavailable
		}
		endpoint, err := config.ServiceEndpoint(registration.RestEndpoint)
		if err != nil {
			return nil, nil, services.ErrServiceUnavailable
		}
		identityURI, err := replicaLifecycle.ReplicaIdentityURI(registration.Identity)
		if err != nil {
			return nil, nil, services.ErrServiceUnavailable
		}
		dialTLS, err := replicaDialTLS(bootstrapConfig, endpoint.Hostname(), config.PeerIdentityConfig{UniformResourceIdentifier: identityURI})
		if err != nil {
			return nil, nil, services.ErrServiceUnavailable
		}
		target := services.RegisteredReplicaTarget{
			InstanceID:                 registration.Identity.InstanceID,
			ReplicaID:                  registration.Identity.ReplicaID,
			Endpoint:                   endpoint,
			ExpectedResourceIdentifier: identityURI,
			Transport:                  &http.Transport{TLSClientConfig: dialTLS},
		}
		clients, release, err := services.RegisteredReplicaFanouts([]services.RegisteredReplicaTarget{target})
		if err != nil {
			return nil, nil, services.ErrServiceUnavailable
		}
		fanout, ok := clients[registration.Identity.InstanceID].(*services.SDKReloadFanout)
		if !ok || len(fanout.Replicas) != 1 || fanout.Replicas[0].ReplicaID != registration.Identity.ReplicaID {
			release()
			return nil, nil, services.ErrServiceUnavailable
		}
		return fanout.Replicas[0].Client, release, nil
	}, nil)
	registeredReloads.OnMetadataFailure = func(instanceID, phase string, failure error) {
		// The Service SDK converts transport and document failures to bounded,
		// non-sensitive errors. Keep the underlying detail in Core's local
		// operator log while the Management API continues returning its generic
		// service_unavailable problem document.
		reason := "unknown"
		if failure != nil {
			reason = failure.Error()
		}
		if len(reason) > 256 {
			reason = reason[:256]
		}
		log.Printf("Core service metadata read failed: instance=%q phase=%q reason=%q", instanceID, phase, reason)
	}
	registeredReloads.ValidateSchema = config.ValidateJSONSchemaDocument
	resolveReplica := func(certificate *x509.Certificate) (models.ServiceReplicaIdentity, bool) {
		identity, ok := replicaDirectory.ResolveIdentity(certificate)
		if !ok {
			return models.ServiceReplicaIdentity{}, false
		}
		return models.ServiceReplicaIdentity{
			InstanceID: identity.InstanceID, ReplicaID: identity.ReplicaID,
			IncarnationID: identity.IncarnationID, PlacementID: identity.PlacementID,
		}, true
	}
	return &ServiceRESTControl{
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
		return nil, errServiceControlTrustInvalid
	}
	return roots, nil
}
