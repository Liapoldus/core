package main

import (
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
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/internal/presentation/api"
	sdkapplication "github.com/Liapoldus/plugin-sdk/application"
	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
	pluginsdk "github.com/Liapoldus/plugin-sdk/infrastructure"
)

// peer-directory-conformance is the cross-component gate for the v2 peer-link
// surface: the real Core poll handler served over real mTLS is consumed by the
// Plugin SDK PeerDirectoryClient and resolved by the Plugin SDK ResolvePeer.
// It proves the wire contract, caller binding, long-poll wake-up, schema parse,
// stable/sequence routing and caller isolation across the component boundary.
func main() {
	ctx := context.Background()

	lifecycle, err := plugins.LoadSDKReplicaLifecycleContract()
	check(err)
	httpContract, err := pluginsdk.LoadHTTPContract()
	check(err)
	poll, err := pluginsdk.LoadPeerDirectoryPollContract()
	check(err)

	contractVersion := lifecycle.ContractVersion
	now := func() time.Time { return time.Now().UTC() }
	directory, err := plugins.NewPluginReplicaDirectory(lifecycle, now)
	check(err)
	changes := plugins.NewPeerDirectoryBroadcaster()

	caller := sdkmodels.PeerReplicaID{InstanceID: "caller-instance", ReplicaID: "caller-replica", IncarnationID: "inc-caller", PlacementID: "placement-one"}
	otherCaller := sdkmodels.PeerReplicaID{InstanceID: "other-instance", ReplicaID: "other-replica", IncarnationID: "inc-other", PlacementID: "placement-two"}
	target := sdkmodels.PeerReplicaID{InstanceID: "target-instance", ReplicaID: "target-replica", IncarnationID: "inc-target", PlacementID: "placement-one"}
	targetSecond := sdkmodels.PeerReplicaID{InstanceID: "target-instance", ReplicaID: "target-replica-2", IncarnationID: "inc-target-2", PlacementID: "placement-one"}
	secondInstance := sdkmodels.PeerReplicaID{InstanceID: "second-instance", ReplicaID: "second-replica", IncarnationID: "inc-second", PlacementID: "placement-one"}
	otherTarget := sdkmodels.PeerReplicaID{InstanceID: "other-target-instance", ReplicaID: "other-target-replica", IncarnationID: "inc-other-target", PlacementID: "placement-two"}
	foreign := sdkmodels.PeerReplicaID{InstanceID: "foreign-instance", ReplicaID: "foreign-replica", IncarnationID: "inc-foreign", PlacementID: "placement-one"}

	targetEndpoint := sdkmodels.ReplicaPeerEndpoint{Carrier: sdkmodels.PeerCarrierUnix, Endpoint: "/run/liapoldus/target.sock", SecurityProfile: sdkmodels.PeerSecurityMTLS}
	secondEndpoint := sdkmodels.ReplicaPeerEndpoint{Carrier: sdkmodels.PeerCarrierUnix, Endpoint: "/run/liapoldus/target-2.sock", SecurityProfile: sdkmodels.PeerSecurityMTLS}
	secondInstanceEndpoint := sdkmodels.ReplicaPeerEndpoint{Carrier: sdkmodels.PeerCarrierUnix, Endpoint: "/run/liapoldus/second.sock", SecurityProfile: sdkmodels.PeerSecurityMTLS}
	otherEndpoint := sdkmodels.ReplicaPeerEndpoint{Carrier: sdkmodels.PeerCarrierUnix, Endpoint: "/run/liapoldus/other.sock", SecurityProfile: sdkmodels.PeerSecurityMTLS}

	register(directory, lifecycle, contractVersion, caller, nil)
	register(directory, lifecycle, contractVersion, otherCaller, nil)
	register(directory, lifecycle, contractVersion, target, []sdkmodels.ReplicaPeerEndpoint{targetEndpoint})
	register(directory, lifecycle, contractVersion, targetSecond, []sdkmodels.ReplicaPeerEndpoint{secondEndpoint})
	register(directory, lifecycle, contractVersion, secondInstance, []sdkmodels.ReplicaPeerEndpoint{secondInstanceEndpoint})
	register(directory, lifecycle, contractVersion, otherTarget, []sdkmodels.ReplicaPeerEndpoint{otherEndpoint})

	authority, err := newAuthority()
	check(err)
	serverCertificate, err := authority.issueServer("core-control")
	check(err)
	callerCertificate, err := authority.issueClient(takeURI(lifecycle, caller))
	check(err)
	otherCallerCertificate, err := authority.issueClient(takeURI(lifecycle, otherCaller))
	check(err)
	foreignCertificate, err := authority.issueClient(takeURI(lifecycle, foreign))
	check(err)

	linkRule := models.PeerLinkRule{
		CallerInstanceID: caller.InstanceID, TargetInstanceID: target.InstanceID,
		PlacementRule: models.PeerLinkPlacementSame, Carrier: models.PeerLinkCarrierUnix, Weight: 5,
	}
	otherRule := models.PeerLinkRule{
		CallerInstanceID: otherCaller.InstanceID, TargetInstanceID: otherTarget.InstanceID,
		PlacementRule: models.PeerLinkPlacementSame, Carrier: models.PeerLinkCarrierUnix, Weight: 5,
	}
	var rules atomic.Pointer[[]models.PeerLinkRule]
	initialRules := []models.PeerLinkRule{linkRule, otherRule}
	rules.Store(&initialRules)
	provider := func(callerInstanceID string) []models.PeerLinkRule {
		var filtered []models.PeerLinkRule
		for _, rule := range *rules.Load() {
			if rule.CallerInstanceID == callerInstanceID {
				filtered = append(filtered, rule)
			}
		}
		return filtered
	}

	notFound := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusTeapot) })
	handler, err := api.NewPluginPeerDirectoryHandler(poll, directory, provider, changes.Subscribe, now, 15*time.Second, notFound)
	check(err)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	check(err)
	serverTLS := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{serverCertificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    authority.roots,
	}
	server := &http.Server{Handler: handler, TLSConfig: serverTLS}
	done := make(chan error, 1)
	go func() { done <- server.Serve(tls.NewListener(listener, serverTLS)) }()
	defer func() { _ = listener.Close(); _ = server.Close(); <-done }()

	baseURL := "https://127.0.0.1:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)

	coreIdentity, err := sdkmodels.NewPeerIdentity("core-control", "")
	check(err)
	callerClient, err := directoryClient(httpContract, poll, authority, serverCertificate, callerCertificate, coreIdentity, baseURL, caller)
	check(err)
	otherClient, err := directoryClient(httpContract, poll, authority, serverCertificate, otherCallerCertificate, coreIdentity, baseURL, otherCaller)
	check(err)
	foreignClient, err := directoryClient(httpContract, poll, authority, serverCertificate, foreignCertificate, coreIdentity, baseURL, foreign)
	check(err)

	initial, err := callerClient.Poll(ctx, "", 0)
	check(err)
	check(initial.Directory.Validate())

	unchanged, err := callerClient.Poll(ctx, initial.ETag, 100)
	check(err)

	changedResult := make(chan pluginsdk.PeerDirectoryPollResult, 1)
	changedErr := make(chan error, 1)
	go func() {
		result, pollErr := callerClient.Poll(ctx, initial.ETag, 5000)
		changedResult <- result
		changedErr <- pollErr
	}()
	time.Sleep(100 * time.Millisecond)
	updatedRules := []models.PeerLinkRule{linkRule, otherRule, {
		CallerInstanceID: caller.InstanceID, TargetInstanceID: secondInstance.InstanceID,
		PlacementRule: models.PeerLinkPlacementSame, Carrier: models.PeerLinkCarrierUnix, Weight: 5,
	}}
	rules.Store(&updatedRules)
	changes.Notify()
	changed := <-changedResult
	check(<-changedErr)

	otherDirectory, err := otherClient.Poll(ctx, "", 0)
	check(err)

	foreignErr := pollError(ctx, foreignClient, "", 0)

	linkID := linkIDFor(changed.Directory, target.InstanceID)
	stableKey := "routing-key-one"
	stable, err := sdkapplication.ResolvePeer(changed.Directory, sdkmodels.PeerResolution{
		LinkID: linkID, Now: time.Now().UTC(), StableRoutingKey: stableKey,
	})
	check(err)
	stableAgain, err := sdkapplication.ResolvePeer(changed.Directory, sdkmodels.PeerResolution{
		LinkID: linkID, Now: time.Now().UTC(), StableRoutingKey: stableKey,
	})
	check(err)
	var ordinalZero uint64
	sequence, err := sdkapplication.ResolvePeer(changed.Directory, sdkmodels.PeerResolution{
		LinkID: linkID, Now: time.Now().UTC(), SelectionOrdinal: &ordinalZero,
	})
	check(err)

	expiredDirectory := changed.Directory
	expiredLink := linkIDFor(expiredDirectory, target.InstanceID)
	lapse := expiredDirectory.IssuedAt.Add(time.Second)
	for index := range expiredDirectory.Links {
		if expiredDirectory.Links[index].LinkID != expiredLink {
			continue
		}
		for replicaIndex := range expiredDirectory.Links[index].Replicas {
			expiredDirectory.Links[index].Replicas[replicaIndex].EligibleUntil = &lapse
		}
	}
	_, expiredErr := sdkapplication.ResolvePeer(expiredDirectory, sdkmodels.PeerResolution{
		LinkID: expiredLink, Now: lapse.Add(time.Second), StableRoutingKey: stableKey,
	})

	result := map[string]bool{
		"initialDirectoryParsed":        initial.NotModified == false && len(initial.Directory.Links) == 1,
		"initialCallerScoped":           initial.Directory.Caller == caller && targetLink(initial.Directory, target.InstanceID) != nil,
		"initialEvictsOtherCallerLink":  targetLink(initial.Directory, otherTarget.InstanceID) == nil,
		"initialBothReplicasReady":      readyReplicas(initial.Directory, target.InstanceID) == 2,
		"initialSocketOnlyEndpoints":    allUnix(initial.Directory),
		"unchangedPollNotModified":      unchanged.NotModified && unchanged.ETag == initial.ETag,
		"policyChangeWakesLongPoll":     changed.NotModified == false && len(changed.Directory.Links) == 2 && changed.ETag != initial.ETag,
		"otherCallerIsolated":           otherDirectory.Directory.Caller == otherCaller && targetLink(otherDirectory.Directory, otherTarget.InstanceID) != nil && targetLink(otherDirectory.Directory, target.InstanceID) == nil,
		"foreignCertificateUnavailable": errors.Is(foreignErr, pluginsdk.ErrPeerDirectoryUnavailable),
		"stableRoutingKeyDeterministic": stable == stableAgain && stable.TargetInstanceID == target.InstanceID,
		"stableRoutingKeyUsesSocket":    stable.Carrier == sdkmodels.PeerCarrierUnix && strings.HasPrefix(stable.Endpoint, "/run/liapoldus/"),
		"sequenceRoutingOrdinalZero":    sequence.TargetInstanceID == target.InstanceID && sequence.Carrier == sdkmodels.PeerCarrierUnix,
		"expiredPeerRejected":           errors.Is(expiredErr, sdkmodels.ErrNoEligiblePeer),
	}
	check(json.NewEncoder(os.Stdout).Encode(result))
}

func directoryClient(
	httpContract pluginsdk.HTTPContract,
	poll pluginsdk.PeerDirectoryPollContract,
	authority *authority,
	server tls.Certificate,
	client tls.Certificate,
	coreIdentity sdkmodels.PeerIdentity,
	baseURL string,
	identity sdkmodels.PeerReplicaID,
) (*pluginsdk.PeerDirectoryClient, error) {
	credentials, err := pluginsdk.LoadCredentials(httpContract, pluginsdk.CredentialsMaterial{
		CABundle:             authority.certificatePEM,
		ServerCertificatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate[0]}),
		ServerKeyPEM:         privateKeyPEM(server),
		ClientCertificatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: client.Certificate[0]}),
		ClientKeyPEM:         privateKeyPEM(client),
	})
	if err != nil {
		return nil, err
	}
	provider, err := pluginsdk.NewStaticCredentialsProvider(credentials)
	if err != nil {
		return nil, err
	}
	mutualTLS, err := pluginsdk.NewMutualTLSClient(httpContract, provider, pluginsdk.MutualTLSClientConfig{
		Peer: coreIdentity, ServerName: "127.0.0.1", Revocation: notRevoked{}, Clock: wallClock{},
	})
	if err != nil {
		return nil, err
	}
	return pluginsdk.NewPeerDirectoryClient(poll, baseURL, mutualTLS, identity)
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

type notRevoked struct{}

func (notRevoked) Revoked([]byte) (bool, error) { return false, nil }

type authority struct {
	certificatePEM []byte
	roots          *x509.CertPool
	certificate    *x509.Certificate
	privateKey     *ecdsa.PrivateKey
}

func newAuthority() (*authority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "conformance CA"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	return &authority{
		certificatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		roots:          roots, certificate: certificate, privateKey: key,
	}, nil
}

func (authority *authority) issueServer(commonName string) (tls.Certificate, error) {
	return authority.issue(pkix.Name{CommonName: commonName}, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, nil)
}

func (authority *authority) issueClient(uri string) (tls.Certificate, error) {
	identifier, err := url.Parse(uri)
	if err != nil {
		return tls.Certificate{}, err
	}
	return authority.issue(pkix.Name{CommonName: "replica"}, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, nil, nil, []*url.URL{identifier})
}

func (authority *authority) issue(subject pkix.Name, usage []x509.ExtKeyUsage, dns []string, ips []net.IP, uris []*url.URL) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<60))
	if err != nil {
		return tls.Certificate{}, err
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: subject, DNSNames: dns, IPAddresses: ips, URIs: uris,
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: usage,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, authority.certificate, &key.PublicKey, authority.privateKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: mustMarshalKey(key)}),
	)
}

func register(directory *plugins.PluginReplicaDirectory, lifecycle plugins.SDKReplicaLifecycleContract, contractVersion string, identity sdkmodels.PeerReplicaID, endpoints []sdkmodels.ReplicaPeerEndpoint) {
	_, err := directory.Register(registrationFor(contractVersion, identity, endpoints), certificateFor(takeURI(lifecycle, identity)))
	check(err)
}

func registrationFor(contractVersion string, identity sdkmodels.PeerReplicaID, endpoints []sdkmodels.ReplicaPeerEndpoint) sdkmodels.ReplicaRegistrationRequest {
	if endpoints == nil {
		endpoints = []sdkmodels.ReplicaPeerEndpoint{}
	}
	return sdkmodels.ReplicaRegistrationRequest{
		ContractVersion:     contractVersion,
		Identity:            identity,
		RestEndpoint:        "https://" + identity.InstanceID + ".internal:9443",
		PeerEndpoints:       endpoints,
		Release:             sdkmodels.ReplicaRelease{Version: "1.0.0", SHA256: strings.Repeat("0", 64)},
		AdvertisedContracts: []sdkmodels.ContractVersion{},
		AcceptedContracts:   []sdkmodels.ContractRange{},
		AppliedGeneration:   "1",
		Ready:               true,
	}
}

func takeURI(contract plugins.SDKReplicaLifecycleContract, identity sdkmodels.PeerReplicaID) string {
	uri, err := contract.ReplicaIdentityURI(identity)
	check(err)
	return uri
}

func certificateFor(value string) *x509.Certificate {
	identifier, err := url.Parse(value)
	check(err)
	return &x509.Certificate{URIs: []*url.URL{identifier}}
}

func pollError(ctx context.Context, client *pluginsdk.PeerDirectoryClient, etag string, waitMs int) error {
	_, err := client.Poll(ctx, etag, waitMs)
	return err
}

func linkIDFor(directory sdkmodels.PeerDirectory, targetInstanceID string) string {
	if link := targetLink(directory, targetInstanceID); link != nil {
		return link.LinkID
	}
	return ""
}

func targetLink(directory sdkmodels.PeerDirectory, targetInstanceID string) *sdkmodels.PeerLink {
	for index := range directory.Links {
		if directory.Links[index].TargetInstanceID == targetInstanceID {
			return &directory.Links[index]
		}
	}
	return nil
}

func readyReplicas(directory sdkmodels.PeerDirectory, targetInstanceID string) int {
	link := targetLink(directory, targetInstanceID)
	if link == nil {
		return 0
	}
	count := 0
	for _, replica := range link.Replicas {
		if replica.Eligibility == sdkmodels.PeerEligibilityReady {
			count++
		}
	}
	return count
}

func allUnix(directory sdkmodels.PeerDirectory) bool {
	for _, link := range directory.Links {
		if link.Carrier != sdkmodels.PeerCarrierUnix {
			return false
		}
		for _, replica := range link.Replicas {
			if !strings.HasPrefix(replica.Endpoint, "/") {
				return false
			}
		}
	}
	return true
}

func privateKeyPEM(certificate tls.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: mustMarshalKey(certificate.PrivateKey.(*ecdsa.PrivateKey))})
}

func mustMarshalKey(key *ecdsa.PrivateKey) []byte {
	der, err := x509.MarshalECPrivateKey(key)
	check(err)
	return der
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
