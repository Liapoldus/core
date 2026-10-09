package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net/url"
	"os"
	"strings"

	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/cli/bootstrap"
	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
	sdkinfrastructure "github.com/Liapoldus/plugin-sdk/infrastructure"
)

func main() {
	if len(os.Args) != 2 {
		panic("expected state database path")
	}
	ctx := context.Background()
	statePath := os.Args[1]
	lifecycle, err := sdkinfrastructure.LoadReplicaLifecycleContract()
	check(err)
	identity := sdkmodels.PeerReplicaID{InstanceID: "dynamic-forms", ReplicaID: "replica-a", IncarnationID: "inc-a", PlacementID: "node-a"}
	identityURI, err := lifecycle.ReplicaIdentityURI(identity)
	check(err)
	certificate := certificateFor(identityURI)
	registration := sdkmodels.ReplicaRegistrationRequest{
		ContractVersion:     lifecycle.ContractVersion,
		Identity:            identity,
		RestEndpoint:        "https://forms.internal:9443",
		PeerEndpoints:       []sdkmodels.ReplicaPeerEndpoint{},
		Release:             sdkmodels.ReplicaRelease{Version: "1.0.0", SHA256: strings.Repeat("0", 64)},
		AdvertisedContracts: []sdkmodels.ContractVersion{},
		AcceptedContracts:   []sdkmodels.ContractRange{},
	}
	firstDirectory, err := plugins.NewPluginReplicaDirectory(lifecycle, nil)
	check(err)
	database, err := bootstrap.OpenDatabase(ctx, statePath)
	check(err)
	_, err = firstDirectory.RegisterAndPersist(ctx, registration, certificate, func(ctx context.Context, registration sdkmodels.ReplicaRegistrationRequest) error {
		return storage.RegisterPluginInstanceReplica(ctx, database, registration.Identity.InstanceID,
			registration.Identity.ReplicaID, []byte("{}"), "configured", storage.ReplicaObservedPending, "2026-10-06T00:00:00Z")
	})
	check(err)
	check(database.Close())

	database, err = bootstrap.OpenDatabase(ctx, statePath)
	check(err)
	registeredInstances, err := storage.ListRegisteredPluginInstances(ctx, database)
	check(err)
	check(database.Close())

	restartedDirectory, err := plugins.NewPluginReplicaDirectory(lifecycle, nil)
	check(err)
	restartedDirectory.MarkRegisteredInstances(registeredInstances)
	_, registeredAdmitted := restartedDirectory.Resolve(certificate)
	staticAllowed := certificateFor("spiffe://liapoldus/plugin/static-forms/replica-a/inc-a")
	_, staticAdmitted := restartedDirectory.Resolve(staticAllowed)

	check(json.NewEncoder(os.Stdout).Encode(map[string]bool{
		"registeredInstanceRequiresFreshLeaseAfterRestart": !registeredAdmitted && restartedDirectory.HasRegisteredInstance(identity.InstanceID),
		"unregisteredStaticIdentityRejected":               !staticAdmitted,
	}))
}

func certificateFor(identityURI string) *x509.Certificate {
	parsed, err := url.Parse(identityURI)
	check(err)
	return &x509.Certificate{URIs: []*url.URL{parsed}}
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
