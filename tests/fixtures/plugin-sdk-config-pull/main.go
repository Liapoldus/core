package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
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
	"time"

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/v3/internal/infrastructure/storage"
	"github.com/Liapoldus/core/v3/internal/presentation/api"
	sdkmodels "github.com/Liapoldus/plugin-sdk/v2/domain/models"
	pluginsdk "github.com/Liapoldus/plugin-sdk/v2/infrastructure"
)

func main() {
	ctx := context.Background()
	database, err := openDatabase(ctx, os.Args[1])
	check(err)
	defer database.Close()
	_, err = database.ExecContext(ctx, "INSERT INTO plugin_instances (id, manifest_json, state) VALUES (?, ?, ?)", "fixture", []byte(`{"name":"fixture"}`), "configured")
	check(err)
	configurations := []struct {
		generation int
		slot       string
		raw        []byte
	}{
		{1, "active", []byte("{ \"origin\" : \"active\" }\n")},
		{2, "previous", []byte(`{"origin":"previous"}`)},
		{3, "staging", []byte(`{"origin":"staging"}`)},
	}
	for _, configuration := range configurations {
		digest := sha256.Sum256(configuration.raw)
		_, err := database.ExecContext(ctx, "INSERT INTO plugin_config_generations (instance_id,generation,slot,raw_json,sha256,schema_version,created_at) VALUES (?,?,?,?,?,1,?)",
			"fixture", configuration.generation, configuration.slot, configuration.raw, hex.EncodeToString(digest[:]), time.Now().UTC().Format(time.RFC3339Nano))
		check(err)
	}
	store, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	management, err := config.LoadManagement()
	check(err)
	operationStore, err := storage.NewSQLiteOperationStore(database)
	check(err)
	check(operationStore.Create(ctx, models.Operation{
		ID: "operation-config-pull", Kind: management.OperationKinds.PluginSettingsApply,
		State: management.Statuses.Running, RequestID: "request-config-pull", Actor: "operator",
		Resource: "fixture", CreatedAt: time.Now().UTC(),
	}))
	snapshot, err := storage.NewPluginConfigurationSnapshot(ctx, store, []string{"fixture"})
	check(err)
	httpContract, err := pluginsdk.LoadHTTPContract()
	check(err)
	contract, err := newCertificates()
	check(err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	check(err)
	serverTLS := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{contract.server}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: contract.roots}
	handler, err := api.NewPluginConfigurationPullHandler(snapshot, func(certificate *x509.Certificate) (string, bool) {
		if certificate.Subject.CommonName == "fixture-replica" {
			return "fixture", true
		}
		if certificate.Subject.CommonName == "other-replica" {
			return "other", true
		}
		return "", false
	})
	check(err)
	done := make(chan error, 1)
	go func() { done <- api.ServePluginConfigurationPull(listener, handler, serverTLS) }()
	defer func() { _ = listener.Close(); <-done }()
	baseURL := "https://localhost:" + itoa(listener.Addr().(*net.TCPAddr).Port)
	validSource := configSource(baseURL, contract.replica, contract.roots, httpContract)
	active, err := validSource.PullExact(ctx, "1")
	check(err)
	previous, err := validSource.PullExact(ctx, "2")
	check(err)
	pendingErr := pullError(ctx, validSource, "3")
	unknownErr := pullError(ctx, validSource, "99")
	otherSource := configSource(baseURL, contract.other, contract.roots, httpContract)
	otherErr := pullError(ctx, otherSource, "2")
	unauthenticatedErr := unauthenticatedPull(baseURL, contract.roots, httpContract)
	pluginListener, err := net.Listen("tcp", "127.0.0.1:0")
	check(err)
	reloadEndpoint, err := httpContract.Endpoint("reload")
	check(err)
	var reloadPulled []byte
	pluginHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != reloadEndpoint.Method || request.URL.Path != reloadEndpoint.Path {
			http.NotFound(writer, request)
			return
		}
		var reload sdkmodels.Reload
		check(json.NewDecoder(request.Body).Decode(&reload))
		configuration, pullErr := validSource.PullExact(request.Context(), reload.Generation)
		if pullErr != nil || configuration.Configuration.SHA256 != reload.SHA256 || configuration.Configuration.SchemaVersion != reload.SchemaVersion {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		reloadPulled = configuration.Configuration.RawJSON
		writer.Header().Set("Content-Type", httpContract.Plugin.Responses.ContentTypes.JSON)
		check(json.NewEncoder(writer).Encode(sdkmodels.ReloadAcknowledgement{Generation: reload.Generation, SHA256: reload.SHA256, SchemaVersion: reload.SchemaVersion, Applied: true, Outcome: sdkmodels.OutcomeApplied}))
	})
	pluginTLS := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{contract.server}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: contract.roots}
	pluginServer := &http.Server{Handler: pluginHandler, TLSConfig: pluginTLS}
	pluginDone := make(chan error, 1)
	go func() { pluginDone <- pluginServer.Serve(tls.NewListener(pluginListener, pluginTLS)) }()
	defer func() { _ = pluginServer.Shutdown(context.Background()); <-pluginDone }()
	coreTransport := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: contract.roots, ServerName: "localhost", Certificates: []tls.Certificate{contract.core}}}}
	peerIdentity, err := sdkmodels.NewPeerIdentity("fixture-core", "")
	check(err)
	reloadClient, err := pluginsdk.NewPluginClient(httpContract, "https://localhost:"+itoa(pluginListener.Addr().(*net.TCPAddr).Port), coreTransport, peerIdentity)
	check(err)
	dynamicReloadCalls := 0
	registered := registeredReplicaSource{replicas: []plugins.LivePluginReplica{{
		Registration: sdkmodels.ReplicaRegistrationRequest{
			Identity:     sdkmodels.PeerReplicaID{InstanceID: "fixture", ReplicaID: "replica-dynamic", IncarnationID: "inc-1", PlacementID: "node-a"},
			RestEndpoint: "https://localhost:" + itoa(pluginListener.Addr().(*net.TCPAddr).Port),
			Release:      sdkmodels.ReplicaRelease{Version: "1.0.0", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		},
		LeaseExpires: time.Now().Add(time.Minute),
	}}}
	registeredReloads := plugins.NewRegisteredReplicaReloadResolver(registered, func(context.Context, plugins.LivePluginReplica) (plugins.SDKReloadClient, func(), error) {
		return countingReloadClient{delegate: reloadClient, count: &dynamicReloadCalls}, nil, nil
	}, nil)
	applier := &plugins.SDKConfigurationApplier{
		Store: store, Snapshot: snapshot,
		Registered: registeredReloads,
	}
	service := application.PluginConfigurationService{Store: store, Applier: applier,
		OperationKind:  management.OperationKinds.PluginSettingsApply,
		RevisionStates: application.PluginConfigurationRevisionStates{Active: "active", Candidate: "staging"}}
	activated, reloadErr := service.Apply(ctx, application.ApplyPluginConfigurationCommand{
		OperationID: "operation-config-pull", InstanceID: "fixture", ExpectedRevision: 1, CandidateRevision: 3, SchemaVersion: 1,
		SettingsJSON:   []byte(`{"origin":"staging"}`),
		CandidateAudit: audit("candidate", "succeeded"), AppliedAudit: audit("applied", "succeeded"), FailedAudit: audit("failed", "failed"),
	})
	registrationReloadErr := applier.ReloadActive(ctx, "fixture")
	bytesErr := applier.ApplyConfiguration(ctx, "fixture", "3", []byte(`{"origin":"tampered"}`))
	unknownPluginErr := applier.ApplyConfiguration(ctx, "unknown", "3", []byte(`{"origin":"staging"}`))
	_, pointers, err := store.Current(ctx, "fixture")
	check(err)
	output := map[string]any{
		"activeRaw": string(active.Configuration.RawJSON), "previousRaw": string(previous.Configuration.RawJSON), "pendingGenerationRejected": pendingErr != nil,
		"exactDigest": active.Configuration.SHA256 == digest(active.Configuration.RawJSON), "unknownGenerationRejected": unknownErr != nil,
		"otherInstanceRejected": otherErr != nil, "unauthenticatedRejected": unauthenticatedErr != nil,
		"reloadAcknowledged": reloadErr == nil, "reloadGeneration": "3", "reloadPulledRaw": string(reloadPulled),
		"dynamicReplicaPulledRaw": string(reloadPulled), "dynamicReloadCalls": dynamicReloadCalls,
		"registrationReloadAcknowledged": registrationReloadErr == nil,
		"activatedGeneration":            activated.Revision, "activeAfterReload": pointers.CurrentRevision,
		"previousAfterReload": pointers.PreviousRevision, "stagingCleared": pointers.PendingRevision == 0,
		"mismatchedBytesRejected": bytesErr != nil, "unknownPluginRejected": unknownPluginErr != nil,
	}
	check(json.NewEncoder(os.Stdout).Encode(output))
}

type registeredReplicaSource struct {
	replicas []plugins.LivePluginReplica
}

func (source registeredReplicaSource) Snapshot() []plugins.LivePluginReplica {
	return source.replicas
}

func (registeredReplicaSource) HasRegisteredInstance(string) bool { return true }

type countingReloadClient struct {
	delegate plugins.SDKReloadClient
	count    *int
}

func (client countingReloadClient) Reload(ctx context.Context, reload sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
	*client.count++
	return client.delegate.Reload(ctx, reload)
}

type certificateSet struct {
	roots   *x509.CertPool
	server  tls.Certificate
	core    tls.Certificate
	replica tls.Certificate
	other   tls.Certificate
}

func newCertificates() (certificateSet, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return certificateSet{}, err
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return certificateSet{}, err
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return certificateSet{}, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	issue := func(name string, server bool) (tls.Certificate, error) {
		key, keyErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if keyErr != nil {
			return tls.Certificate{}, keyErr
		}
		serial, serialErr := rand.Int(rand.Reader, big.NewInt(1<<60))
		if serialErr != nil {
			return tls.Certificate{}, serialErr
		}
		usage := []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		if server {
			usage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		}
		template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: usage}
		der, certErr := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
		if certErr != nil {
			return tls.Certificate{}, certErr
		}
		privateKey, keyErr := x509.MarshalPKCS8PrivateKey(key)
		if keyErr != nil {
			return tls.Certificate{}, keyErr
		}
		return tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey}))
	}
	server, err := issue("config-core", true)
	if err != nil {
		return certificateSet{}, err
	}
	replica, err := issue("fixture-replica", false)
	if err != nil {
		return certificateSet{}, err
	}
	other, err := issue("other-replica", false)
	if err != nil {
		return certificateSet{}, err
	}
	core, err := issue("fixture-core", false)
	if err != nil {
		return certificateSet{}, err
	}
	return certificateSet{roots: roots, server: server, core: core, replica: replica, other: other}, nil
}

func configSource(baseURL string, certificate tls.Certificate, roots *x509.CertPool, contract pluginsdk.HTTPContract) *pluginsdk.CoreConfigurationSource {
	parsed, err := url.Parse(baseURL)
	check(err)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: parsed.Hostname(), Certificates: []tls.Certificate{certificate}}}}
	source, err := pluginsdk.NewCoreConfigurationSource(contract, baseURL, client)
	check(err)
	return source
}

func pullError(ctx context.Context, source *pluginsdk.CoreConfigurationSource, generation string) error {
	_, err := source.PullExact(ctx, generation)
	return err
}

func unauthenticatedPull(baseURL string, roots *x509.CertPool, contract pluginsdk.HTTPContract) error {
	parsed, err := url.Parse(baseURL)
	check(err)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: parsed.Hostname()}}}
	path := strings.Replace(contract.Core.ConfigPull.PathTemplate, "{generation}", "2", 1)
	request, err := http.NewRequest(contract.Core.ConfigPull.Method, baseURL+path, nil)
	check(err)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("unexpected successful unauthenticated response")
	}
	return nil
}

func openDatabase(ctx context.Context, path string) (*sql.DB, error) {
	contract, err := config.LoadSQLiteContract()
	if err != nil {
		return nil, err
	}
	return storage.OpenSQLite(ctx, path, storage.SQLiteOptions{Driver: contract.Driver, ParentDirectoryMode: contract.ParentDirectoryMode, DatabaseFileMode: contract.DatabaseFileMode, MaxOpenConnections: contract.MaxOpenConnections, MaxIdleConnections: contract.MaxIdleConnections, SchemaVersion: contract.SchemaVersion, HasMigrationTableQuery: contract.HasMigrationTableQuery, MigrationVersionQuery: contract.MigrationVersionQuery, SchemaVersionError: contract.SchemaVersionError, Pragmas: contract.Pragmas}, contract.Schema)
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func audit(action, result string) models.AuditRecord {
	return models.AuditRecord{Timestamp: time.Now().UTC(), Actor: "operator", Action: action, Resource: "fixture", Result: result, RequestID: action}
}
func digest(raw []byte) string { value := sha256.Sum256(raw); return hex.EncodeToString(value[:]) }
func itoa(value int) string    { return strconv.Itoa(value) }
