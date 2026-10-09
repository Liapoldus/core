package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/internal/presentation/api"
	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
	sdkinfrastructure "github.com/Liapoldus/plugin-sdk/infrastructure"
)

func main() {
	contract, err := sdkinfrastructure.LoadReplicaLifecycleContract()
	check(err)
	now := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	directory, err := plugins.NewPluginReplicaDirectory(contract, func() time.Time { return now })
	check(err)
	identity := sdkmodels.PeerReplicaID{InstanceID: "forms-db", ReplicaID: "replica-a", IncarnationID: "inc-a", PlacementID: "node-a"}
	certificate := certificateFor(takeURI(contract, identity))
	registration := registrationFor(contract, identity)
	firstLease, err := directory.Register(registration, certificate)
	check(err)
	now = now.Add(time.Second)
	activeDuplicate, err := directory.Register(registration, certificate)
	check(err)

	wrongCertificate := certificateFor("spiffe://liapoldus/plugin/forms-db/replica-a/other")
	_, identityMismatchErr := directory.Register(registration, wrongCertificate)

	changedEndpoint := registration
	changedEndpoint.RestEndpoint = "https://forms-b.internal:9443"
	_, immutableErr := directory.Register(changedEndpoint, certificate)

	now = activeDuplicate.LeaseExpiresAt
	_, oldRenewalErr := directory.Renew(sdkmodels.ReplicaRenewalRequest{
		ContractVersion:   contract.ContractVersion,
		Identity:          identity,
		AppliedGeneration: "2",
		Ready:             true,
	}, certificate)
	_, oldReregisterErr := directory.Register(registration, certificate)

	nextIdentity := identity
	nextIdentity.IncarnationID = "inc-b"
	nextCertificate := certificateFor(takeURI(contract, nextIdentity))
	nextRegistration := registrationFor(contract, nextIdentity)
	_, nextIncarnationErr := directory.Register(nextRegistration, nextCertificate)
	_, nextIncarnationEligible := directory.Resolve(nextCertificate)

	now = now.Add(time.Duration(contract.Lease.TTLSeconds) * time.Second)
	_, expiredEligible := directory.Resolve(nextCertificate)

	httpDirectory, err := plugins.NewPluginReplicaDirectory(contract, nil)
	check(err)
	httpIdentity := sdkmodels.PeerReplicaID{InstanceID: "forms-http", ReplicaID: "replica-http", IncarnationID: "inc-http", PlacementID: "node-http"}
	caCertificate, caKey := newCA()
	clientCertificate := issueClientCertificate(caCertificate, caKey, contract, httpIdentity)
	callbackCount := 0
	afterRegisterCount := 0
	registrationVisibleBeforeReload := false
	reloadHookReceivesExactReplica := false
	handler, err := api.NewPluginReplicaLifecycleHandler(contract, httpDirectory, func(_ context.Context, _ sdkmodels.ReplicaRegistrationRequest) error {
		callbackCount++
		return nil
	}, http.NotFoundHandler(), func(_ context.Context, instanceID, replicaID string) error {
		afterRegisterCount++
		registrationVisibleBeforeReload = instanceID == httpIdentity.InstanceID && httpDirectory.HasRegisteredInstance(instanceID)
		reloadHookReceivesExactReplica = replicaID == httpIdentity.ReplicaID
		return nil
	})
	check(err)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: certificatePool(caCertificate)}
	server.StartTLS()
	defer server.Close()
	client := server.Client()
	transport := client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.Certificates = []tls.Certificate{clientCertificate}
	client.Transport = transport

	httpRegistration := registrationFor(contract, httpIdentity)
	registrationBody, err := json.Marshal(httpRegistration)
	check(err)
	registerPath := contract.Endpoints["register"].Path
	validStatus := post(client, server.URL+registerPath, registrationBody)
	lockedDirectory, err := plugins.NewPluginReplicaDirectory(contract, nil)
	check(err)
	lockedPersistenceEntered := make(chan struct{}, 1)
	lockedHandler, err := api.NewPluginReplicaLifecycleHandler(contract, lockedDirectory, func(_ context.Context, _ sdkmodels.ReplicaRegistrationRequest) error {
		lockedPersistenceEntered <- struct{}{}
		return nil
	}, http.NotFoundHandler())
	check(err)
	requestEntered := make(chan struct{})
	lockedServer := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		close(requestEntered)
		lockedHandler.ServeHTTP(response, request)
	}))
	lockedServer.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: certificatePool(caCertificate)}
	lockedServer.StartTLS()
	defer lockedServer.Close()
	lockedClient := lockedServer.Client()
	configureClientCertificate(lockedClient, clientCertificate)
	application.SnapshotActivationLock.Lock()
	registrationResult := make(chan int, 1)
	go func() { registrationResult <- post(lockedClient, lockedServer.URL+registerPath, registrationBody) }()
	<-requestEntered
	registrationWaitsForSnapshotActivation := true
	select {
	case <-lockedPersistenceEntered:
		registrationWaitsForSnapshotActivation = false
	case <-time.After(100 * time.Millisecond):
	}
	application.SnapshotActivationLock.Unlock()
	lockedStatus := <-registrationResult
	registrationWaitsForSnapshotActivation = registrationWaitsForSnapshotActivation && lockedStatus == contract.Responses["register"].Status
	wrongIdentity := httpIdentity
	wrongIdentity.IncarnationID = "other-http"
	wrongClientCertificate := issueClientCertificate(caCertificate, caKey, contract, wrongIdentity)
	wrongClient := server.Client()
	wrongTransport := wrongClient.Transport.(*http.Transport).Clone()
	wrongTransport.TLSClientConfig = wrongTransport.TLSClientConfig.Clone()
	wrongTransport.TLSClientConfig.Certificates = []tls.Certificate{wrongClientCertificate}
	wrongClient.Transport = wrongTransport
	wrongIdentityStatus := post(wrongClient, server.URL+registerPath, registrationBody)
	oversizedStatus := post(client, server.URL+registerPath, bytes.Repeat([]byte("x"), int(contract.Requests["register"].MaximumBytes+1)))

	// A failed persistence callback for one duplicate registration must not
	// remove another concurrent registration that successfully persisted.
	raceDirectory, err := plugins.NewPluginReplicaDirectory(contract, nil)
	check(err)
	var persistenceCalls atomic.Int32
	firstPersistenceEntered := make(chan struct{})
	secondPersistenceEntered := make(chan struct{})
	secondRequestArrived := make(chan struct{})
	releaseFirstPersistence := make(chan struct{})
	raceHandler, err := api.NewPluginReplicaLifecycleHandler(contract, raceDirectory, func(_ context.Context, _ sdkmodels.ReplicaRegistrationRequest) error {
		switch persistenceCalls.Add(1) {
		case 1:
			close(firstPersistenceEntered)
			<-releaseFirstPersistence
			return errors.New("persistence failed")
		default:
			close(secondPersistenceEntered)
			return nil
		}
	}, http.NotFoundHandler())
	check(err)
	arrivals := atomic.Int32{}
	raceServer := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == registerPath && arrivals.Add(1) == 2 {
			close(secondRequestArrived)
		}
		raceHandler.ServeHTTP(response, request)
	}))
	raceServer.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: certificatePool(caCertificate)}
	raceServer.StartTLS()
	defer raceServer.Close()
	raceClient := raceServer.Client()
	configureClientCertificate(raceClient, clientCertificate)
	raceBody, err := json.Marshal(registrationFor(contract, httpIdentity))
	check(err)
	firstResult := make(chan int, 1)
	go func() { firstResult <- post(raceClient, raceServer.URL+registerPath, raceBody) }()
	<-firstPersistenceEntered
	secondResult := make(chan int, 1)
	go func() { secondResult <- post(raceClient, raceServer.URL+registerPath, raceBody) }()
	<-secondRequestArrived
	select {
	case <-secondPersistenceEntered:
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirstPersistence)
	firstStatus := <-firstResult
	secondStatus := <-secondResult
	parsedClientCertificate, err := x509.ParseCertificate(clientCertificate.Certificate[0])
	check(err)
	_, concurrentReplicaEligible := raceDirectory.Resolve(parsedClientCertificate)

	// Once an identity has participated in lifecycle registration, an expired
	// or replaced incarnation must not fall back to a legacy identity resolver.
	var fallbackNow = time.Now().UTC()
	fallbackDirectory, err := plugins.NewPluginReplicaDirectory(contract, func() time.Time { return fallbackNow })
	check(err)
	fallback := func(certificate *x509.Certificate) (string, bool) {
		if certificate == nil || len(certificate.URIs) == 0 {
			return "", false
		}
		return "legacy-instance", strings.HasPrefix(certificate.URIs[0].String(), "spiffe://liapoldus/plugin/")
	}
	dynamicIdentity := sdkmodels.PeerReplicaID{InstanceID: "forms-dynamic", ReplicaID: "replica-a", IncarnationID: "inc-a", PlacementID: "node-a"}
	dynamicCertificate := certificateFor(takeURI(contract, dynamicIdentity))
	unregisteredInstance, unregisteredAuthorized := fallbackDirectory.ResolveWithFallback(dynamicCertificate, fallback)
	dynamicRegistration := registrationFor(contract, dynamicIdentity)
	dynamicLease, err := fallbackDirectory.Register(dynamicRegistration, dynamicCertificate)
	check(err)
	resolvedInstance, activeAuthorized := fallbackDirectory.ResolveWithFallback(dynamicCertificate, fallback)
	fallbackNow = dynamicLease.LeaseExpiresAt.Add(time.Second)
	_, expiredAuthorized := fallbackDirectory.ResolveWithFallback(dynamicCertificate, fallback)

	nextDynamicIdentity := dynamicIdentity
	nextDynamicIdentity.IncarnationID = "inc-b"
	nextDynamicCertificate := certificateFor(takeURI(contract, nextDynamicIdentity))
	nextDynamicRegistration := registrationFor(contract, nextDynamicIdentity)
	_, err = fallbackDirectory.Register(nextDynamicRegistration, nextDynamicCertificate)
	check(err)
	_, replacedAuthorized := fallbackDirectory.ResolveWithFallback(dynamicCertificate, fallback)

	result := map[string]bool{
		"validRegistration":                          err == nil && !firstLease.LeaseExpiresAt.IsZero(),
		"activeDuplicateRenews":                      activeDuplicate.LeaseExpiresAt.After(firstLease.LeaseExpiresAt),
		"certificateIdentityMismatchRejected":        identityMismatchErr == sdkmodels.ErrReplicaIdentityMismatch,
		"changedImmutableMetadataRejected":           immutableErr == sdkmodels.ErrReplicaIdentityConflict,
		"oldIncarnationRenewalRejected":              oldRenewalErr == sdkmodels.ErrReplicaLeaseExpired,
		"expiredSameIncarnationRegistrationRejected": oldReregisterErr == sdkmodels.ErrReplicaLeaseExpired,
		"newIncarnationReplacesExpired":              nextIncarnationErr == nil && nextIncarnationEligible,
		"expiredReplicaNotEligible":                  !expiredEligible,
		"authenticatedHTTPRegistration":              validStatus == contract.Responses["register"].Status && callbackCount == 1,
		"registrationWaitsForSnapshotActivation":     registrationWaitsForSnapshotActivation,
		"reloadHookRunsAfterRegistrationPublication": afterRegisterCount == 1 && registrationVisibleBeforeReload,
		"reloadHookReceivesExactReplica":             reloadHookReceivesExactReplica,
		"identityMismatchHTTPRejected":               wrongIdentityStatus == contract.Problems["identityMismatch"].Status,
		"oversizedRegistrationRejected":              oversizedStatus == contract.Problems["invalidRequest"].Status && callbackCount == 1,
		"concurrentRegistrationSurvivesPersistenceFailure": firstStatus == contract.Problems["unavailable"].Status &&
			secondStatus == contract.Responses["register"].Status && concurrentReplicaEligible,
		"unregisteredIdentityUsesFallback":    unregisteredAuthorized && unregisteredInstance == "legacy-instance",
		"registeredIdentityOverridesFallback": activeAuthorized && resolvedInstance == dynamicIdentity.InstanceID,
		"expiredIdentityCannotDowngrade":      !expiredAuthorized,
		"replacedIncarnationCannotDowngrade":  !replacedAuthorized,
	}
	check(json.NewEncoder(os.Stdout).Encode(result))
}

func registrationFor(contract sdkinfrastructure.ReplicaLifecycleContract, identity sdkmodels.PeerReplicaID) sdkmodels.ReplicaRegistrationRequest {
	return sdkmodels.ReplicaRegistrationRequest{
		ContractVersion: contract.ContractVersion,
		Identity:        identity,
		RestEndpoint:    "https://forms-a.internal:9443",
		PeerEndpoints:   []sdkmodels.ReplicaPeerEndpoint{},
		Release: sdkmodels.ReplicaRelease{
			Version: "1.0.0",
			SHA256:  "0000000000000000000000000000000000000000000000000000000000000000",
		},
		AdvertisedContracts: []sdkmodels.ContractVersion{},
		AcceptedContracts:   []sdkmodels.ContractRange{},
		AppliedGeneration:   "1",
		Ready:               true,
	}
}

func takeURI(contract sdkinfrastructure.ReplicaLifecycleContract, identity sdkmodels.PeerReplicaID) string {
	uri, err := contract.ReplicaIdentityURI(identity)
	check(err)
	return uri
}

func certificateFor(value string) *x509.Certificate {
	identifier, err := url.Parse(value)
	check(err)
	return &x509.Certificate{URIs: []*url.URL{identifier}}
}

func newCA() (*x509.Certificate, *ecdsa.PrivateKey) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	check(err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "fixture-ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	check(err)
	certificate, err := x509.ParseCertificate(der)
	check(err)
	return certificate, key
}

func issueClientCertificate(ca *x509.Certificate, caKey *ecdsa.PrivateKey, contract sdkinfrastructure.ReplicaLifecycleContract, identity sdkmodels.PeerReplicaID) tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	check(err)
	uri, err := url.Parse(takeURI(contract, identity))
	check(err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(int64(time.Now().UnixNano())),
		Subject:      pkix.Name{CommonName: "not-used"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	check(err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	check(err)
	certificate, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	check(err)
	return certificate
}

func certificatePool(certificate *x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	return pool
}

func post(client *http.Client, endpoint string, body []byte) int {
	request, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	check(err)
	request.Header.Set("content-type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return 0
	}
	defer response.Body.Close()
	return response.StatusCode
}

func configureClientCertificate(client *http.Client, certificate tls.Certificate) {
	transport := client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.Certificates = []tls.Certificate{certificate}
	client.Transport = transport
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
