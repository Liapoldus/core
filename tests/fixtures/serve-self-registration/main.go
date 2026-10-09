package main

// serve-self-registration exercises the real `core serve` process end to end
// over mTLS REST: a neutral replica registers against a freshly started Core,
// the wait-free peer-directory poll authenticates the registered caller, the
// durable registration marker survives a real process restart while the
// in-memory lease does not, and the replica must register again.

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
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
	sdkinfrastructure "github.com/Liapoldus/plugin-sdk/infrastructure"
)

const (
	dynamicPlacement = "node-1"
	staticInstanceID = "static-child"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	lifecycle, err := sdkinfrastructure.LoadReplicaLifecycleContract()
	if err != nil {
		return err
	}
	poll, err := sdkinfrastructure.LoadPeerDirectoryPollContract()
	if err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "core-serve-registration-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)

	rootPEM, root, rootKey, coreCertificate, err := issueRoot(lifecycle)
	if err != nil {
		return err
	}
	dynamicIdentity := sdkmodels.PeerReplicaID{
		InstanceID: "dynamic-child", ReplicaID: "dynamic-replica", IncarnationID: "inc-1", PlacementID: dynamicPlacement,
	}
	dynamicCertificate, err := issueReplica(lifecycle, root, rootKey, dynamicIdentity)
	if err != nil {
		return err
	}
	for name, value := range map[string][]byte{
		"root.pem": rootPEM, "core.pem": coreCertificate.pem, "core.key": coreCertificate.key,
		"dynamic.pem": dynamicCertificate.pem, "dynamic.key": dynamicCertificate.key,
	} {
		if err := os.WriteFile(filepath.Join(directory, name), value, 0o600); err != nil {
			return err
		}
	}

	managementAddress, err := freeAddress()
	if err != nil {
		return err
	}
	controlAddress, err := freeAddress()
	if err != nil {
		return err
	}
	databasePath := filepath.Join(directory, "core.sqlite")
	environment := append(os.Environ(),
		"CORE_SQLITE_PATH="+databasePath,
		"CORE_INIT_MANAGEMENT_LISTEN="+managementAddress,
		"CORE_INIT_MANAGEMENT_CERTIFICATE="+filepath.Join(directory, "core.pem"),
		"CORE_INIT_MANAGEMENT_KEY="+filepath.Join(directory, "core.key"),
		"CORE_INIT_CONTROL_LISTEN="+controlAddress,
		"CORE_INIT_CONTROL_PUBLIC_URL=https://"+controlAddress,
		"CORE_INIT_CONTROL_CERTIFICATE="+filepath.Join(directory, "core.pem"),
		"CORE_INIT_CONTROL_KEY="+filepath.Join(directory, "core.key"),
		"CORE_INIT_REPLICA_CLIENT_CA="+filepath.Join(directory, "root.pem"),
		"CORE_INIT_REPLICA_SERVER_CA="+filepath.Join(directory, "root.pem"),
		"CORE_INIT_SECRET_ROOT="+directory,
	)

	coreRoot := repositoryRoot()
	coreBinary := filepath.Join(directory, "core")
	if err := build(coreRoot, coreBinary, "./cmd/core"); err != nil {
		return err
	}
	initialize := exec.Command(coreBinary, "init")
	initialize.Dir = coreRoot
	initialize.Env = environment
	if output, err := initialize.CombinedOutput(); err != nil {
		return fmt.Errorf("core init: %w: %s", err, output)
	}
	bootstrap := exec.Command(coreBinary, "access", "bootstrap")
	bootstrap.Dir = coreRoot
	bootstrap.Env = environment
	tokenOutput, err := bootstrap.Output()
	if err != nil {
		return fmt.Errorf("core access bootstrap: %w", err)
	}
	if strings.TrimSpace(string(tokenOutput)) == "" {
		return errors.New("core access bootstrap returned no service credential")
	}

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootPEM) {
		return errors.New("invalid root certificate")
	}
	managementClient, err := peerClient(roots, certificate{})
	if err != nil {
		return err
	}
	dynamicClient, err := peerClient(roots, dynamicCertificate)
	if err != nil {
		return err
	}
	controlBase := "https://" + controlAddress

	process, err := startCore(coreBinary, environment, coreRoot)
	if err != nil {
		return err
	}
	defer stopCore(process)
	if err := waitForHealth(managementClient, "https://"+managementAddress); err != nil {
		return err
	}

	registerPath := lifecycle.Endpoints["register"].Path
	registerBody, err := json.Marshal(sdkmodels.ReplicaRegistrationRequest{
		ContractVersion:     lifecycle.ContractVersion,
		Identity:            dynamicIdentity,
		RestEndpoint:        "https://dynamic.internal:9443",
		PeerEndpoints:       []sdkmodels.ReplicaPeerEndpoint{},
		Release:             sdkmodels.ReplicaRelease{Version: "1.0.0", SHA256: strings.Repeat("a", 64)},
		AdvertisedContracts: []sdkmodels.ContractVersion{},
		AcceptedContracts:   []sdkmodels.ContractRange{},
		AppliedGeneration:   "1",
		Ready:               true,
	})
	if err != nil {
		return err
	}
	registerStatus, err := post(dynamicClient, controlBase+registerPath, registerBody)
	if err != nil {
		return err
	}
	registeredOverRealCore := registerStatus == lifecycle.Responses["register"].Status

	pollStatus, err := pollDirectory(dynamicClient, controlBase, poll)
	if err != nil {
		return err
	}
	peerDirectoryPollAuthenticated := pollStatus == poll.Responses.Directory.Status

	stopCore(process)

	markers, err := registrationMarkers(databasePath)
	if err != nil {
		return err
	}
	durableMarkerPersisted := contains(markers, dynamicIdentity.InstanceID)
	staticInstanceNotMarked := !contains(markers, staticInstanceID)

	process, err = startCore(coreBinary, environment, coreRoot)
	if err != nil {
		return err
	}
	defer stopCore(process)
	if err := waitForHealth(managementClient, "https://"+managementAddress); err != nil {
		return err
	}

	postRestartPoll, err := pollDirectory(dynamicClient, controlBase, poll)
	if err != nil {
		return err
	}
	restartRequiresReRegistration := postRestartPoll != poll.Responses.Directory.Status

	reRegisterStatus, err := post(dynamicClient, controlBase+registerPath, registerBody)
	if err != nil {
		return err
	}
	reRegisteredAfterRestart := reRegisterStatus == lifecycle.Responses["register"].Status

	stopCore(process)
	markers, err = registrationMarkers(databasePath)
	if err != nil {
		return err
	}
	markerSingleAfterReregister := count(markers, dynamicIdentity.InstanceID) == 1 && !contains(markers, staticInstanceID)

	observations := map[string]bool{
		"registeredOverRealCore":         registeredOverRealCore,
		"peerDirectoryPollAuthenticated": peerDirectoryPollAuthenticated,
		"durableMarkerPersisted":         durableMarkerPersisted,
		"staticInstanceNotMarked":        staticInstanceNotMarked,
		"restartRequiresReRegistration":  restartRequiresReRegistration,
		"reRegisteredAfterRestart":       reRegisteredAfterRestart,
		"markerSingleAfterReregister":    markerSingleAfterReregister,
	}
	encoded, err := json.Marshal(observations)
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

type certificate struct{ pem, key []byte }

func issueRoot(lifecycle sdkinfrastructure.ReplicaLifecycleContract) ([]byte, *x509.Certificate, *ecdsa.PrivateKey, certificate, error) {
	if lifecycle.ContractVersion == "" {
		return nil, nil, nil, certificate{}, errors.New("missing replica lifecycle contract")
	}
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, certificate{}, err
	}
	rootTemplate := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "serve-registration-root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		return nil, nil, nil, certificate{}, err
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return nil, nil, nil, certificate{}, err
	}
	coreCertificate, err := issueLeaf(root, rootKey, "core-child", nil, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth})
	if err != nil {
		return nil, nil, nil, certificate{}, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}), root, rootKey, coreCertificate, nil
}

func issueReplica(lifecycle sdkinfrastructure.ReplicaLifecycleContract, root *x509.Certificate, rootKey *ecdsa.PrivateKey, identity sdkmodels.PeerReplicaID) (certificate, error) {
	uri, err := lifecycle.ReplicaIdentityURI(identity)
	if err != nil {
		return certificate{}, err
	}
	return issueLeaf(root, rootKey, identity.InstanceID, []string{uri}, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
}

func issueLeaf(root *x509.Certificate, signingKey *ecdsa.PrivateKey, commonName string, uris []string, usage []x509.ExtKeyUsage) (certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return certificate{}, err
	}
	template := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  usage,
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	if len(uris) > 0 {
		parsed := make([]*url.URL, 0, len(uris))
		for _, value := range uris {
			reference, err := url.Parse(value)
			if err != nil {
				return certificate{}, err
			}
			parsed = append(parsed, reference)
		}
		template.URIs = parsed
	}
	der, err := x509.CreateCertificate(rand.Reader, template, root, &key.PublicKey, signingKey)
	if err != nil {
		return certificate{}, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return certificate{}, err
	}
	return certificate{
		pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		key: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	}, nil
}

func serial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	value, err := rand.Int(rand.Reader, limit)
	if err != nil {
		panic(err)
	}
	return value
}

func peerClient(roots *x509.CertPool, credential certificate) (*http.Client, error) {
	configuration := &tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12}
	if len(credential.pem) > 0 {
		pair, err := tls.X509KeyPair(credential.pem, credential.key)
		if err != nil {
			return nil, err
		}
		configuration.Certificates = []tls.Certificate{pair}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: configuration}, Timeout: 10 * time.Second}, nil
}

func post(client *http.Client, address string, body []byte) (int, error) {
	request, err := http.NewRequest(http.MethodPost, address, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	request.Header.Set("content-type", "application/json")
	return status(client, request)
}

func pollDirectory(client *http.Client, base string, contract sdkinfrastructure.PeerDirectoryPollContract) (int, error) {
	address := base + contract.Endpoint.Path + "?" + contract.Poll.WaitParameter + "=0"
	request, err := http.NewRequest(http.MethodGet, address, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("accept", contract.DirectoryMediaType)
	return status(client, request)
}

func status(client *http.Client, request *http.Request) (int, error) {
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	return response.StatusCode, nil
}

func registrationMarkers(path string) ([]string, error) {
	ctx := context.Background()
	sqliteContract, err := config.LoadSQLiteContract()
	if err != nil {
		return nil, err
	}
	database, err := storage.OpenSQLite(ctx, path, storage.SQLiteOptions{
		Driver: sqliteContract.Driver, ParentDirectoryMode: sqliteContract.ParentDirectoryMode,
		DatabaseFileMode: sqliteContract.DatabaseFileMode, MaxOpenConnections: sqliteContract.MaxOpenConnections,
		MaxIdleConnections: sqliteContract.MaxIdleConnections, SchemaVersion: sqliteContract.SchemaVersion,
		HasMigrationTableQuery: sqliteContract.HasMigrationTableQuery, MigrationVersionQuery: sqliteContract.MigrationVersionQuery,
		SchemaVersionError: sqliteContract.SchemaVersionError, Pragmas: sqliteContract.Pragmas,
	}, sqliteContract.Schema)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	return storage.ListRegisteredPluginInstances(ctx, database)
}

func contains(values []string, target string) bool { return count(values, target) > 0 }

func count(values []string, target string) int {
	matches := 0
	for _, value := range values {
		if value == target {
			matches++
		}
	}
	return matches
}

func startCore(binary string, environment []string, workingDirectory string) (*exec.Cmd, error) {
	process := exec.Command(binary, "serve")
	process.Dir = workingDirectory
	process.Env = environment
	process.Stdout = io.Discard
	process.Stderr = io.Discard
	if err := process.Start(); err != nil {
		return nil, fmt.Errorf("start core serve: %w", err)
	}
	return process, nil
}

func stopCore(process *exec.Cmd) {
	if process == nil || process.Process == nil {
		return
	}
	_ = process.Process.Signal(syscall.SIGTERM)
	finished := make(chan struct{})
	go func() { _ = process.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		_ = process.Process.Kill()
		<-finished
	}
}

func waitForHealth(client *http.Client, base string) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(base + "/healthz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("core serve management endpoint did not become healthy")
}

func build(directory, binary, target string) error {
	command := exec.Command("go", "build", "-o", binary, target)
	command.Dir = directory
	command.Env = append(os.Environ(), "GOTOOLCHAIN=go1.26.0")
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("build %s: %w: %s", target, err, output)
	}
	return nil
}

func freeAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	address := listener.Addr().String()
	return address, listener.Close()
}

func repositoryRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
