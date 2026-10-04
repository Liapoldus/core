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
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/api"
	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
	pluginsdk "github.com/Liapoldus/plugin-sdk/infrastructure"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	ctx := context.Background()
	directory := os.Args[1]
	database, err := openDatabase(ctx, filepath.Join(directory, "core.db"))
	check(err)
	defer database.Close()

	secret := []byte("fixture-database-password-never-log")
	secretPath := filepath.Join(directory, "database-password")
	check(os.WriteFile(secretPath, secret, 0o600))
	defer clear(secret)
	secretReference := "file:" + secretPath
	activeJSON, err := json.Marshal(map[string]string{"credential": secretReference})
	check(err)
	previousJSON, err := json.Marshal(map[string]string{"credential": secretReference})
	check(err)
	_, err = database.ExecContext(ctx, "INSERT INTO plugin_instances (id, manifest_json, state) VALUES (?, ?, ?)", "forms", []byte(`{"name":"generic"}`), "configured")
	check(err)
	insertGeneration(ctx, database, "forms", 1, "active", activeJSON)
	insertGeneration(ctx, database, "forms", 2, "previous", previousJSON)
	store, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	snapshot, err := storage.NewPluginConfigurationSnapshot(ctx, store, []string{"forms"})
	check(err)

	contract, err := pluginsdk.LoadHTTPContract()
	check(err)
	grantPolicy, err := config.LoadPluginSecretGrantPolicy()
	check(err)
	certificates, err := newCertificates()
	check(err)
	service := &application.PluginSecretGrantService{
		Configurations: snapshot,
		ResolveSecret: func(_ context.Context, reference string, maximumBytes int64) ([]byte, error) {
			return config.ReadSecretReference(filepath.Join(directory, "core.yaml"), reference, maximumBytes)
		},
		GrantTTL:            time.Duration(contract.Deadlines.CoreSecretGrantSeconds) * time.Second,
		MaximumValueBytes:   int64(contract.Core.SecretGrant.Redemption.MaximumResponseBytes),
		MaximumOutstanding:  grantPolicy.MaximumOutstanding,
		MaximumReferenceLen: contract.Core.SecretGrant.ReferenceMaximumLength,
		MaximumPurposeLen:   contract.Core.SecretGrant.PurposeMaximumLength,
		Now:                 time.Now,
		Random:              rand.Reader,
	}
	capacity := verifyGrantCapacity(ctx, snapshot, service, secretReference, certificates.replica)
	pullHandler, err := api.NewPluginConfigurationPullHandler(snapshot, resolveReplica)
	check(err)
	grantHandler, err := api.NewPluginSecretGrantHandler(contract, service, resolveReplica, pullHandler)
	check(err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	check(err)
	serverTLS := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificates.server}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: certificates.roots}
	server := &http.Server{Handler: grantHandler, TLSConfig: serverTLS}
	done := make(chan error, 1)
	go func() { done <- server.Serve(tls.NewListener(listener, serverTLS)) }()
	defer func() { _ = server.Close(); <-done }()
	baseURL := "https://localhost:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	client := controlClient(baseURL, certificates.replica, certificates.roots)
	otherClient := controlClient(baseURL, certificates.other, certificates.roots)
	var observedErrors []string
	issue := func(request sdkmodels.SecretGrantRequest, client *http.Client) (sdkmodels.SecretGrant, error) {
		grant, issueErr := issueGrant(ctx, client, baseURL, contract, request)
		if issueErr != nil {
			observedErrors = append(observedErrors, issueErr.Error())
		}
		return grant, issueErr
	}

	validRequest := sdkmodels.SecretGrantRequest{Reference: secretReference, Purpose: "forms-storage", Generation: "1"}
	activeGrant, activeIssueErr := issue(validRequest, client)
	_, previousErr := issue(sdkmodels.SecretGrantRequest{Reference: secretReference, Purpose: "forms-storage", Generation: "2"}, client)
	_, unreferencedErr := issue(sdkmodels.SecretGrantRequest{Reference: "file:unconfigured", Purpose: "forms-storage", Generation: "1"}, client)
	wrongReplicaGrant, wrongReplicaIssueErr := issue(validRequest, client)
	wrongReplicaDenied := false
	if wrongReplicaIssueErr == nil {
		_, redeemErr := redeemGrant(ctx, otherClient, baseURL, contract, wrongReplicaGrant.Handle)
		if redeemErr != nil {
			observedErrors = append(observedErrors, redeemErr.Error())
			wrongReplicaDenied = true
		}
	}
	changedGenerationGrant, changedIssueErr := issue(validRequest, client)
	_, err = database.ExecContext(ctx, "UPDATE plugin_config_generations SET slot = ? WHERE instance_id = ? AND generation = ? AND slot = ?", "staging", "forms", 1, "active")
	check(err)
	_, err = database.ExecContext(ctx, "UPDATE plugin_config_generations SET slot = ? WHERE instance_id = ? AND generation = ? AND slot = ?", "active", "forms", 2, "previous")
	check(err)
	_, err = database.ExecContext(ctx, "UPDATE plugin_config_generations SET slot = ? WHERE instance_id = ? AND generation = ? AND slot = ?", "previous", "forms", 1, "staging")
	check(err)
	check(snapshot.Refresh(ctx, "forms"))
	changedGenerationDenied := false
	if changedIssueErr == nil {
		_, redeemErr := redeemGrant(ctx, client, baseURL, contract, changedGenerationGrant.Handle)
		if redeemErr != nil {
			observedErrors = append(observedErrors, redeemErr.Error())
			changedGenerationDenied = true
		}
	}

	newActiveRequest := validRequest
	newActiveRequest.Generation = "2"
	broker, err := pluginsdk.NewCoreSecretBroker(contract, baseURL, client, 16)
	check(err)
	finalGrant, finalIssueErr := broker.IssueGrant(ctx, newActiveRequest)
	if finalIssueErr != nil {
		observedErrors = append(observedErrors, finalIssueErr.Error())
	}
	var redeemed []byte
	var redeemErr error
	if finalIssueErr == nil {
		value, valueErr := broker.Redeem(ctx, sdkmodels.SecretRedemption{Handle: finalGrant.Handle})
		redeemed = value.Bytes()
		value.Destroy()
		redeemErr = valueErr
		if redeemErr != nil {
			observedErrors = append(observedErrors, redeemErr.Error())
		}
	}
	secondRejected := false
	if finalIssueErr == nil {
		_, secondErr := redeemGrant(ctx, client, baseURL, contract, finalGrant.Handle)
		if secondErr != nil {
			observedErrors = append(observedErrors, secondErr.Error())
			secondRejected = true
		}
	}
	output, err := json.Marshal(map[string]any{
		"grantLimitEnforced":            capacity,
		"issuedForActiveGeneration":     activeIssueErr == nil && activeGrant.Generation == "1" && activeGrant.Reference == secretReference && activeGrant.Purpose == "forms-storage",
		"previousGenerationDenied":      previousErr != nil,
		"unreferencedSecretDenied":      unreferencedErr != nil,
		"wrongReplicaDenied":            wrongReplicaDenied,
		"activeGenerationChangeDenied":  changedGenerationDenied,
		"redemptionReturnsExactSecret":  redeemErr == nil && string(redeemed) == string(secret),
		"secondRedemptionRejected":      secondRejected,
		"secretAbsentFromLogsAndErrors": !strings.Contains(strings.Join(observedErrors, "\n"), string(secret)),
		"handleAbsentFromLogsAndErrors": !strings.Contains(strings.Join(observedErrors, "\n"), activeGrant.Handle) && !strings.Contains(strings.Join(observedErrors, "\n"), finalGrant.Handle),
	})
	check(err)
	_, err = os.Stdout.Write(append(output, '\n'))
	check(err)
}

func verifyGrantCapacity(
	ctx context.Context,
	snapshot interfaces.PluginRetainedConfigurationReader,
	service *application.PluginSecretGrantService,
	reference string,
	certificate tls.Certificate,
) bool {
	capacityService := &application.PluginSecretGrantService{
		Configurations:      snapshot,
		ResolveSecret:       service.ResolveSecret,
		GrantTTL:            service.GrantTTL,
		MaximumValueBytes:   service.MaximumValueBytes,
		MaximumOutstanding:  service.MaximumOutstanding,
		MaximumReferenceLen: service.MaximumReferenceLen,
		MaximumPurposeLen:   service.MaximumPurposeLen,
		Now:                 service.Now,
		Random:              service.Random,
	}
	identity := models.PluginReplicaIdentity{InstanceID: "forms", Fingerprint: certificateFingerprint(certificate.Leaf)}
	accepted := 0
	for range capacityService.MaximumOutstanding + 1 {
		_, err := capacityService.Issue(ctx, identity, reference, "capacity-check", "1")
		if err != nil {
			return accepted == capacityService.MaximumOutstanding
		}
		accepted++
	}
	return false
}

func issueGrant(ctx context.Context, client *http.Client, baseURL string, contract pluginsdk.HTTPContract, request sdkmodels.SecretGrantRequest) (sdkmodels.SecretGrant, error) {
	endpoint := contract.Core.SecretGrant.Issue
	body, err := json.Marshal(request)
	if err != nil {
		return sdkmodels.SecretGrant{}, err
	}
	response, err := send(ctx, client, endpoint.Method, baseURL+endpoint.PathTemplate, endpoint.RequestMediaType, body)
	if err != nil {
		return sdkmodels.SecretGrant{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var problem sdkmodels.Problem
		_ = json.NewDecoder(response.Body).Decode(&problem)
		return sdkmodels.SecretGrant{}, errors.New(string(problem.Outcome))
	}
	var grant sdkmodels.SecretGrant
	err = json.NewDecoder(response.Body).Decode(&grant)
	return grant, err
}

func redeemGrant(ctx context.Context, client *http.Client, baseURL string, contract pluginsdk.HTTPContract, handle string) ([]byte, error) {
	endpoint := contract.Core.SecretGrant.Redemption
	path := strings.Replace(endpoint.PathTemplate, "{handle}", url.PathEscape(handle), 1)
	body, err := json.Marshal(sdkmodels.SecretRedemption{Handle: handle})
	if err != nil {
		return nil, err
	}
	response, err := send(ctx, client, endpoint.Method, baseURL+path, endpoint.RequestMediaType, body)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	contents, err := ioReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var problem sdkmodels.Problem
		_ = json.Unmarshal(contents, &problem)
		return nil, errors.New(string(problem.Outcome))
	}
	return contents, nil
}

func send(ctx context.Context, client *http.Client, method, target, mediaType string, body []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", mediaType)
	return client.Do(request)
}

func openDatabase(ctx context.Context, path string) (*sql.DB, error) {
	contract, err := config.LoadSQLiteContract()
	if err != nil {
		return nil, err
	}
	return storage.OpenSQLite(ctx, path, storage.SQLiteOptions{Driver: contract.Driver, ParentDirectoryMode: contract.ParentDirectoryMode, DatabaseFileMode: contract.DatabaseFileMode, MaxOpenConnections: contract.MaxOpenConnections, MaxIdleConnections: contract.MaxIdleConnections, SchemaVersion: contract.SchemaVersion, HasMigrationTableQuery: contract.HasMigrationTableQuery, MigrationVersionQuery: contract.MigrationVersionQuery, SchemaVersionError: contract.SchemaVersionError, IntegrityCheckQuery: contract.IntegrityCheckQuery, ForeignKeyCheckQuery: contract.ForeignKeyCheckQuery, IntegritySuccess: contract.IntegritySuccess, IntegrityError: contract.IntegrityError, Pragmas: contract.Pragmas}, contract.Schema)
}

func insertGeneration(ctx context.Context, database *sql.DB, instance string, generation int, slot string, raw []byte) {
	digest := sha256.Sum256(raw)
	_, err := database.ExecContext(ctx, "INSERT INTO plugin_config_generations (instance_id,generation,slot,raw_json,sha256,schema_version,created_at) VALUES (?,?,?,?,?,1,?)", instance, generation, slot, raw, hex.EncodeToString(digest[:]), time.Now().UTC().Format(time.RFC3339Nano))
	check(err)
}

type certificateSet struct {
	roots   *x509.CertPool
	server  tls.Certificate
	replica tls.Certificate
	other   tls.Certificate
}

func newCertificates() (certificateSet, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return certificateSet{}, err
	}
	rootTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture CA"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &key.PublicKey, key)
	if err != nil {
		return certificateSet{}, err
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return certificateSet{}, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	issue := func(name string, server bool) (tls.Certificate, error) {
		leafKey, keyErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
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
		der, certErr := x509.CreateCertificate(rand.Reader, template, root, &leafKey.PublicKey, key)
		if certErr != nil {
			return tls.Certificate{}, certErr
		}
		privateDER, keyErr := x509.MarshalPKCS8PrivateKey(leafKey)
		if keyErr != nil {
			return tls.Certificate{}, keyErr
		}
		return tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}))
	}
	server, err := issue("fixture-core", true)
	if err != nil {
		return certificateSet{}, err
	}
	replica, err := issue("forms-replica-a", false)
	if err != nil {
		return certificateSet{}, err
	}
	other, err := issue("forms-replica-b", false)
	if err != nil {
		return certificateSet{}, err
	}
	return certificateSet{roots: roots, server: server, replica: replica, other: other}, nil
}

func controlClient(baseURL string, certificate tls.Certificate, roots *x509.CertPool) *http.Client {
	parsed, err := url.Parse(baseURL)
	check(err)
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: parsed.Hostname(), Certificates: []tls.Certificate{certificate}}}}
}

func resolveReplica(certificate *x509.Certificate) (string, bool) {
	if certificate == nil {
		return "", false
	}
	switch certificate.Subject.CommonName {
	case "forms-replica-a":
		return "forms", true
	case "forms-replica-b":
		return "forms", true
	default:
		return "", false
	}
}

func certificateFingerprint(certificate *x509.Certificate) string {
	digest := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(digest[:])
}

func ioReadAll(reader io.Reader) ([]byte, error) { return io.ReadAll(reader) }

func check(err error) {
	if err != nil {
		panic(err)
	}
}
