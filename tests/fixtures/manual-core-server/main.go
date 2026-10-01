package main

import (
	"bytes"
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

	_ "modernc.org/sqlite"
)

type certificate struct{ pem, key []byte }

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	directory, err := os.MkdirTemp("", "core-server-e2e-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	root, coreCert, serverCert, formsCert, crl, err := issueCertificates()
	if err != nil {
		return err
	}
	for name, value := range map[string][]byte{
		"root.pem": root, "core.pem": coreCert.pem, "core.key": coreCert.key,
		"server.pem": serverCert.pem, "server.key": serverCert.key,
		"forms.pem": formsCert.pem, "forms.key": formsCert.key, "revocation.pem": crl,
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
	restAddress, err := freeAddress()
	if err != nil {
		return err
	}
	formsRESTAddress, err := freeAddress()
	if err != nil {
		return err
	}
	formsPeerAddress, err := freeAddress()
	if err != nil {
		return err
	}
	publicAddress, err := freeAddress()
	if err != nil {
		return err
	}
	oldOrigin := httpOrigin("old")
	defer oldOrigin.Close()
	newOrigin := httpOrigin("new")
	defer newOrigin.Close()
	coreRoot := repositoryRoot()
	serverRoot := filepath.Clean(filepath.Join(coreRoot, "..", "plugins", "server"))
	formsRoot := filepath.Clean(filepath.Join(coreRoot, "..", "plugins", "forms-db"))
	coreBinary := filepath.Join(directory, "core")
	serverBinary := filepath.Join(directory, "server")
	formsBinary := filepath.Join(directory, "forms-db")
	if err := build(coreRoot, coreBinary, "./cmd/core"); err != nil {
		return err
	}
	if err := build(serverRoot, serverBinary, "./cmd/server"); err != nil {
		return err
	}
	if err := build(formsRoot, formsBinary, "./cmd/forms-db"); err != nil {
		return err
	}
	databasePath := filepath.Join(directory, "core.sqlite")
	configuration := filepath.Join(directory, "core.yaml")
	contents := strings.Join([]string{
		"state:", "  path: " + databasePath,
		"management:", "  listen: " + managementAddress,
		"  tls:", "    certificate: file:" + filepath.Join(directory, "core.pem"),
		"    key: file:" + filepath.Join(directory, "core.key"),
		"pluginControl:", "  listen: " + controlAddress,
		"  publicURL: https://" + controlAddress,
		"  tls:", "    certificate: file:" + filepath.Join(directory, "core.pem"),
		"    key: file:" + filepath.Join(directory, "core.key"),
		"    replicaClientCA: file:" + filepath.Join(directory, "root.pem"),
		"    replicaServerCA: file:" + filepath.Join(directory, "root.pem"),
		"plugins:", "  - instanceId: server-child", "    replicas:",
		"      - replicaId: server-replica", "        endpoint: https://" + restAddress,
		"        expectedPeerIdentity:", "          commonName: server-child", "",
		"  - instanceId: forms-child", "    replicas:",
		"      - replicaId: forms-replica", "        endpoint: https://" + formsRESTAddress,
		"        expectedPeerIdentity:", "          commonName: forms-child", "",
	}, "\n")
	if err := os.WriteFile(configuration, []byte(contents), 0o600); err != nil {
		return err
	}
	bootstrap := exec.Command(coreBinary, "--config", configuration, "access", "bootstrap")
	bootstrap.Dir = coreRoot
	output, err := bootstrap.Output()
	if err != nil {
		return fmt.Errorf("Core bootstrap failed: %w", err)
	}
	token := strings.TrimSpace(string(output))
	if token == "" {
		return errors.New("Core bootstrap returned no service credential")
	}
	server := exec.Command(serverBinary,
		"--instance-id=server-child", "--replica-id=server-replica", "--rest-listen="+restAddress,
		"--core-url=https://"+controlAddress, "--core-server-name=localhost",
		"--core-common-name=core-child", "--core-client-common-name=core-child",
		"--ca-file="+filepath.Join(directory, "root.pem"),
		"--server-cert="+filepath.Join(directory, "server.pem"), "--server-key="+filepath.Join(directory, "server.key"),
		"--client-cert="+filepath.Join(directory, "server.pem"), "--client-key="+filepath.Join(directory, "server.key"),
		"--crl-file="+filepath.Join(directory, "revocation.pem"),
	)
	server.Dir = serverRoot
	if err := server.Start(); err != nil {
		return err
	}
	defer func() { stop(server) }()
	forms := exec.Command(formsBinary,
		"--instance-id=forms-child", "--replica-id=forms-replica", "--rest-listen="+formsRESTAddress,
		"--core-url=https://"+controlAddress, "--core-server-name=localhost",
		"--core-common-name=core-child", "--core-client-common-name=core-child",
		"--ca-file="+filepath.Join(directory, "root.pem"),
		"--server-cert="+filepath.Join(directory, "forms.pem"), "--server-key="+filepath.Join(directory, "forms.key"),
		"--client-cert="+filepath.Join(directory, "forms.pem"), "--client-key="+filepath.Join(directory, "forms.key"),
		"--crl-file="+filepath.Join(directory, "revocation.pem"),
		"--peer-listen="+formsPeerAddress, "--peer-identity=spiffe://liapoldus.test/forms",
		"--peer-allowed-caller=spiffe://liapoldus.test/server",
		"--peer-ca-file="+filepath.Join(directory, "root.pem"),
		"--peer-cert="+filepath.Join(directory, "forms.pem"), "--peer-key="+filepath.Join(directory, "forms.key"),
		"--peer-carrier=tcp",
	)
	forms.Dir = formsRoot
	if err := forms.Start(); err != nil {
		return err
	}
	defer stop(forms)
	core := exec.Command(coreBinary, "--config", configuration, "serve")
	core.Dir = coreRoot
	if err := core.Start(); err != nil {
		return err
	}
	defer func() { stop(core) }()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(root) {
		return errors.New("invalid test trust root")
	}
	if err := waitServerHealth(restAddress, roots, coreCert); err != nil {
		return err
	}
	if err := waitServerHealth(formsRESTAddress, roots, coreCert); err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, Timeout: 5 * time.Second}
	base := "https://" + managementAddress
	if err := waitForHealth(client, base, core); err != nil {
		return err
	}
	firstSettings := settings(publicAddress, oldOrigin.Addr())
	firstID, err := putSettings(client, base, token, `"0"`, "initial-server-config-0001", firstSettings)
	if err != nil {
		return err
	}
	if err := waitOperation(client, base, token, firstID, databasePath); err != nil {
		return err
	}
	first, err := readPublic(publicAddress)
	if err != nil {
		return err
	}
	secondSettings := settings(publicAddress, newOrigin.Addr())
	secondID, err := putSettings(client, base, token, `"1"`, "updated-server-config-0002", secondSettings)
	if err != nil {
		return err
	}
	if err := waitOperation(client, base, token, secondID, databasePath); err != nil {
		return err
	}
	formsSettings := []byte(`{"driver":"memory","schemas":{"contact":{"type":"object","properties":{"email":{"type":"string"}},"required":["email"],"additionalProperties":false}}}`)
	formsID, err := putPluginSettings(client, base, token, "forms-child", `"0"`, "initial-forms-config-0001", formsSettings)
	if err != nil {
		return err
	}
	if err := waitOperation(client, base, token, formsID, databasePath); err != nil {
		return err
	}
	second, err := readPublic(publicAddress)
	if err != nil {
		return err
	}
	rollbackID, err := rollbackSettings(client, base, token, "server-child", `"2"`, "rollback-server-config-0003")
	if err != nil {
		return err
	}
	if err := waitOperation(client, base, token, rollbackID, databasePath); err != nil {
		return err
	}
	afterRollback, err := readPublic(publicAddress)
	if err != nil {
		return err
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return err
	}
	defer database.Close()
	var active, previous int64
	var stored []byte
	var digest string
	if err := database.QueryRow(`SELECT generation, raw_json, sha256 FROM plugin_config_generations WHERE instance_id = ? AND slot = 'active'`, "server-child").Scan(&active, &stored, &digest); err != nil {
		return err
	}
	if err := database.QueryRow(`SELECT generation FROM plugin_config_generations WHERE instance_id = ? AND slot = 'previous'`, "server-child").Scan(&previous); err != nil {
		return err
	}
	var formsActive int64
	var formsStored []byte
	if err := database.QueryRow(`SELECT generation, raw_json FROM plugin_config_generations WHERE instance_id = ? AND slot = 'active'`, "forms-child").Scan(&formsActive, &formsStored); err != nil {
		return err
	}
	if !bytes.Equal(formsStored, formsSettings) {
		return errors.New("Core changed forms-db settings bytes")
	}
	_ = database.Close()
	stop(core)
	stop(server)
	server = exec.Command(serverBinary, server.Args[1:]...)
	server.Dir = serverRoot
	if err := server.Start(); err != nil {
		return err
	}
	if err := waitServerHealth(restAddress, roots, coreCert); err != nil {
		return err
	}
	core = exec.Command(coreBinary, "--config", configuration, "serve")
	core.Dir = coreRoot
	if err := core.Start(); err != nil {
		return err
	}
	if err := waitForHealth(client, base, core); err != nil {
		return err
	}
	restartedServer, err := waitPublic(publicAddress)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(firstSettings)
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"first": first, "second": second, "afterRollback": afterRollback,
		"active": active, "previous": previous,
		"exactDigest": bytes.Equal(stored, firstSettings) && digest == hex.EncodeToString(hash[:]),
		"formsActive": formsActive,
		"restartedServer": restartedServer,
	})
}

func repositoryRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func build(directory, binary, target string) error {
	command := exec.Command("go", "build", "-o", binary, target)
	command.Dir = directory
	command.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=go1.26.0")
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("build %s: %w: %s", target, err, output)
	}
	return nil
}

func stop(child *exec.Cmd) {
	if child == nil || child.Process == nil {
		return
	}
	_ = child.Process.Signal(syscall.SIGTERM)
	finished := make(chan struct{})
	go func() { _ = child.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		_ = child.Process.Kill()
		<-finished
	}
}

func freeAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	address := listener.Addr().String()
	return address, listener.Close()
}

func httpOrigin(body string) *originServer {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(writer, body) })}
	go func() { _ = server.Serve(listener) }()
	return &originServer{server: server, listener: listener}
}

type originServer struct {
	server   *http.Server
	listener net.Listener
}

func (origin *originServer) Addr() string { return origin.listener.Addr().String() }
func (origin *originServer) Close()       { _ = origin.server.Close() }

func settings(publicAddress, origin string) []byte {
	value, _ := json.Marshal(map[string]any{"schemaVersion": 1, "config": map[string]any{
		"listeners": []any{map[string]any{"id": "web", "kind": "http", "address": publicAddress,
			"hostnames": []string{}, "protocols": []string{"http1"}, "tls": map[string]any{"mode": "disabled"}}},
		"routes": []any{map[string]any{"id": "proxy", "listenerId": "web", "handler": map[string]any{
			"type": "reverseProxy", "upstreams": []any{map[string]any{"origin": "http://" + origin, "weight": 100}}}}},
	}})
	return value
}

func waitForHealth(client *http.Client, base string, child *exec.Cmd) error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if child.ProcessState != nil {
			return errors.New("Core exited before readiness")
		}
		response, err := client.Get(base + "/healthz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("Core Management API did not become healthy")
}

func waitServerHealth(address string, roots *x509.CertPool, credential certificate) error {
	pair, err := tls.X509KeyPair(credential.pem, credential.key)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: roots, Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12,
	}}, Timeout: time.Second}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("https://" + address + "/_liapoldus/v1/health")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("Server Plugin SDK REST endpoint did not become healthy")
}

func putSettings(client *http.Client, base, token, etag, key string, raw []byte) (string, error) {
	return putPluginSettings(client, base, token, "server-child", etag, key, raw)
}

func rollbackSettings(client *http.Client, base, token, instanceID, etag, key string) (string, error) {
	request, err := http.NewRequest(http.MethodPost, base+"/api/plugins/"+instanceID+"/rollback", nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("If-Match", etag)
	request.Header.Set("Idempotency-Key", key)
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("Core rollback returned HTTP %d", response.StatusCode)
	}
	var body struct {
		OperationID string `json:"operationId"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&body); err != nil {
		return "", err
	}
	if body.OperationID == "" {
		return "", errors.New("Core rollback returned no operation ID")
	}
	return body.OperationID, nil
}

func putPluginSettings(client *http.Client, base, token, instanceID, etag, key string, raw []byte) (string, error) {
	request, err := http.NewRequest(http.MethodPut, base+"/api/plugins/"+instanceID+"/settings", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", etag)
	request.Header.Set("Idempotency-Key", key)
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("Core settings PUT returned HTTP %d", response.StatusCode)
	}
	var body struct {
		OperationID string `json:"operationId"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&body); err != nil {
		return "", err
	}
	if body.OperationID == "" {
		return "", errors.New("Core settings PUT returned no operation ID")
	}
	return body.OperationID, nil
}

func waitOperation(client *http.Client, base, token, id, databasePath string) error {
	deadline := time.Now().Add(35 * time.Second)
	lastState := ""
	for time.Now().Before(deadline) {
		request, err := http.NewRequest(http.MethodGet, base+"/api/operations/"+id, nil)
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		var body struct {
			State     string `json:"state"`
			ErrorCode string `json:"errorCode"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&body)
		_ = response.Body.Close()
		if decodeErr != nil {
			return decodeErr
		}
		lastState = body.State
		switch body.State {
		case "succeeded":
			return nil
		case "failed":
			return fmt.Errorf("Core settings operation failed: %s", body.ErrorCode)
		}
		time.Sleep(50 * time.Millisecond)
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return fmt.Errorf("Core settings operation did not complete (last state %q)", lastState)
	}
	defer database.Close()
	var slot, replicaState, failure string
	_ = database.QueryRow(`SELECT slot FROM plugin_config_generations WHERE instance_id = ? ORDER BY generation DESC LIMIT 1`, "server-child").Scan(&slot)
	_ = database.QueryRow(`SELECT observed_state, last_failure_code FROM plugin_replicas WHERE instance_id = ? LIMIT 1`, "server-child").Scan(&replicaState, &failure)
	return fmt.Errorf("Core settings operation did not complete (last state %q; slot %q; replica %q; code %q)", lastState, slot, replicaState, failure)
}

func readPublic(address string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get("http://" + address + "/")
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Server public HTTP status %d", response.StatusCode)
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, 128))
	return string(contents), err
}

func waitPublic(address string) (string, error) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		body, err := readPublic(address)
		if err == nil {
			return body, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", errors.New("restarted Server did not apply active generation")
}

func issueCertificates() ([]byte, certificate, certificate, certificate, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, certificate{}, certificate{}, certificate{}, nil, err
	}
	rootTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test root"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &key.PublicKey, key)
	if err != nil {
		return nil, certificate{}, certificate{}, certificate{}, nil, err
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return nil, certificate{}, certificate{}, certificate{}, nil, err
	}
	issue := func(serial int64, name string) (certificate, error) {
		leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return certificate{}, err
		}
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name},
			DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
			NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		if name == "forms-child" {
			identity, _ := url.Parse("spiffe://liapoldus.test/forms")
			template.URIs = []*url.URL{identity}
		}
		der, err := x509.CreateCertificate(rand.Reader, template, root, &leafKey.PublicKey, key)
		if err != nil {
			return certificate{}, err
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
		if err != nil {
			return certificate{}, err
		}
		return certificate{pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
			key: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})}, nil
	}
	core, err := issue(2, "core-child")
	if err != nil {
		return nil, certificate{}, certificate{}, certificate{}, nil, err
	}
	server, err := issue(3, "server-child")
	if err != nil {
		return nil, certificate{}, certificate{}, certificate{}, nil, err
	}
	forms, err := issue(4, "forms-child")
	if err != nil {
		return nil, certificate{}, certificate{}, certificate{}, nil, err
	}
	crlDER, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{Number: big.NewInt(1),
		ThisUpdate: time.Now().Add(-time.Minute), NextUpdate: time.Now().Add(time.Hour)}, root, key)
	if err != nil {
		return nil, certificate{}, certificate{}, certificate{}, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}), core, server, forms,
		pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: crlDER}), nil
}
