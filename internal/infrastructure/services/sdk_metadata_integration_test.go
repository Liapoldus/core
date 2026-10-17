package services

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	sdkmodels "github.com/Liapoldus/service-sdk/v2/domain/models"
	sdkinfrastructure "github.com/Liapoldus/service-sdk/v2/infrastructure"
)

func TestCoreReadsLiveMetadataThroughServiceSDKRESTOverMTLS(t *testing.T) {
	now := time.Now().UTC()
	serviceIdentity := sdkmodels.PeerReplicaID{
		InstanceID: "server", ReplicaID: "replica-1", IncarnationID: "incarnation-1", PlacementID: "local",
	}
	lifecycle, err := sdkinfrastructure.LoadReplicaLifecycleContract()
	if err != nil {
		t.Fatal(err)
	}
	serviceURI, err := lifecycle.ReplicaIdentityURI(serviceIdentity)
	if err != nil {
		t.Fatal(err)
	}

	clientCA, clientKey := metadataTestAuthority(t, "core-client-ca", now)
	serverCA, serverKey := metadataTestAuthority(t, "service-server-ca", now)
	coreCertificate, coreLeafKey := metadataTestLeaf(t, clientCA, clientKey, "core-management", nil, now)
	serviceCertificate, serviceLeafKey := metadataTestLeaf(t, serverCA, serverKey, "server", []string{serviceURI}, now)
	serverRoots := x509.NewCertPool()
	serverRoots.AddCert(serverCA)
	clientRoots := x509.NewCertPool()
	clientRoots.AddCert(clientCA)
	serviceTLSCertificate := metadataTLSCertificate(t, serviceCertificate, serviceLeafKey)
	coreTLSCertificate := metadataTLSCertificate(t, coreCertificate, coreLeafKey)

	httpContract, err := sdkinfrastructure.LoadHTTPContract()
	if err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"id":"server","version":"1.2.3","manifestVersion":"service-runtime/v1"}`)
	schema := []byte(`{"type":"object","properties":{"port":{"type":"integer"}}}`)
	registration := sdkmodels.Registration{
		ContractVersion: "service-sdk/v2", InstanceID: serviceIdentity.InstanceID,
		ReplicaID: serviceIdentity.ReplicaID, AppliedGeneration: "1", Ready: true,
	}
	readiness := sdkmodels.Readiness{
		Ready: true, Generation: "1", SHA256: strings.Repeat("a", 64), SchemaVersion: "1",
		InstanceID: serviceIdentity.InstanceID, ReplicaID: serviceIdentity.ReplicaID,
	}
	metadataDocuments := map[string][]byte{}
	for name, value := range map[string]any{"identity": registration, "ready": readiness} {
		metadataDocuments[name], err = json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
	}
	metadataDocuments["manifest"] = manifest
	metadataDocuments["configSchema"] = schema

	var requestMu sync.Mutex
	var requestPaths []string
	service := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.TLS == nil || len(request.TLS.PeerCertificates) != 1 || request.TLS.PeerCertificates[0].Subject.CommonName != "core-management" {
			http.Error(writer, "Core mTLS identity required", http.StatusUnauthorized)
			return
		}
		for _, name := range []string{"manifest", "configSchema", "identity", "ready"} {
			endpoint, endpointErr := httpContract.Endpoint(name)
			if endpointErr != nil {
				t.Errorf("load %s endpoint from Service SDK contract: %v", name, endpointErr)
				return
			}
			if request.Method != endpoint.Method || request.URL.Path != endpoint.Path {
				continue
			}
			requestMu.Lock()
			requestPaths = append(requestPaths, request.URL.Path)
			requestMu.Unlock()
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write(metadataDocuments[name])
			return
		}
		http.NotFound(writer, request)
	}))
	service.TLS = &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serviceTLSCertificate},
		ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientRoots,
	}
	service.StartTLS()
	t.Cleanup(service.Close)

	serviceEndpoint, err := url.Parse(service.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{coreTLSCertificate},
		RootCAs: serverRoots, ServerName: "127.0.0.1",
		VerifyConnection: PinnedReplicaIdentityVerifier("", serviceURI, nil),
	}}
	t.Cleanup(transport.CloseIdleConnections)

	live := LiveServiceReplica{
		Registration: sdkmodels.ReplicaRegistrationRequest{
			Identity: serviceIdentity, RestEndpoint: service.URL,
			PeerEndpoints: []sdkmodels.ReplicaPeerEndpoint{},
			Release:       sdkmodels.ReplicaRelease{Version: "1.2.3", SHA256: strings.Repeat("a", 64)},
		},
		LeaseExpires: now.Add(time.Minute),
	}
	resolver := NewRegisteredReplicaReloadResolver(metadataTestSource{replica: live}, func(_ context.Context, replica LiveServiceReplica) (SDKReloadClient, func(), error) {
		clients, release, buildErr := RegisteredReplicaFanouts([]RegisteredReplicaTarget{{
			InstanceID: replica.Registration.Identity.InstanceID,
			ReplicaID:  replica.Registration.Identity.ReplicaID,
			Endpoint:   serviceEndpoint, ExpectedResourceIdentifier: serviceURI,
			Transport: transport.Clone(),
		}})
		if buildErr != nil {
			return nil, nil, buildErr
		}
		fanout, ok := clients[replica.Registration.Identity.InstanceID].(*SDKReloadFanout)
		if !ok || len(fanout.Replicas) != 1 {
			release()
			return nil, nil, ErrServiceUnavailable
		}
		return fanout.Replicas[0].Client, release, nil
	}, func() time.Time { return now })
	got, err := resolver.Metadata(context.Background(), serviceIdentity.InstanceID)
	if err != nil {
		t.Fatalf("Core failed to read metadata through the SDK REST client: %v", err)
	}
	if string(got.Manifest) != string(manifest) || string(got.ConfigurationSchema) != string(schema) ||
		got.Identity != registration || got.Readiness != readiness {
		t.Fatalf("Core metadata does not match live SDK responses: %+v", got)
	}
	if len(got.Replicas) != 1 || got.Replicas[0].IncarnationID != serviceIdentity.IncarnationID {
		t.Fatalf("Core metadata lost the authenticated fan-out incarnation: %+v", got.Replicas)
	}

	wantPaths := make([]string, 0, 4)
	for _, name := range []string{"manifest", "configSchema", "identity", "ready"} {
		endpoint, endpointErr := httpContract.Endpoint(name)
		if endpointErr != nil {
			t.Fatal(endpointErr)
		}
		wantPaths = append(wantPaths, endpoint.Path)
	}
	requestMu.Lock()
	gotPaths := append([]string(nil), requestPaths...)
	requestMu.Unlock()
	sort.Strings(gotPaths)
	sort.Strings(wantPaths)
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("Core SDK REST requests = %v, want contract metadata routes %v", gotPaths, wantPaths)
	}
}

func metadataTestAuthority(t *testing.T, commonName string, now time.Time) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: commonName},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, key
}

func metadataTestLeaf(t *testing.T, authority *x509.Certificate, authorityKey *ecdsa.PrivateKey, commonName string, identityURIs []string, now time.Time) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: commonName},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	for _, raw := range identityURIs {
		parsed, parseErr := url.Parse(raw)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		template.URIs = append(template.URIs, parsed)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, authority, &key.PublicKey, authorityKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, key
}

func metadataTLSCertificate(t *testing.T, certificate *x509.Certificate, key *ecdsa.PrivateKey) tls.Certificate {
	t.Helper()
	return tls.Certificate{Certificate: [][]byte{certificate.Raw}, PrivateKey: key, Leaf: certificate}
}
