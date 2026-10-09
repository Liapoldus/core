package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
	pluginsdk "github.com/Liapoldus/plugin-sdk/infrastructure"
	_ "modernc.org/sqlite"
)

type certificate struct{ pem, key []byte }

// formsDBCursorGrantPurpose is the public cross-repository test contract. The
// production value is code-owned by forms-db, not loaded from a runtime file.
const formsDBCursorGrantPurpose = "cursor-signing"

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (buffer *safeBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(value)
}

func (buffer *safeBuffer) contains(value string) bool {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return bytes.Contains(buffer.buf.Bytes(), []byte(value))
}

func (buffer *safeBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}

func (buffer *safeBuffer) lifecycleSummary() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	var order []string
	counts := map[string]int{}
	for _, line := range strings.Split(buffer.buf.String(), "\n") {
		var record struct {
			Kind    string `json:"kind"`
			Outcome string `json:"outcome"`
		}
		if json.Unmarshal([]byte(line), &record) == nil && record.Kind != "" && record.Outcome != "" {
			key := record.Kind + "=" + record.Outcome
			if counts[key] == 0 {
				order = append(order, key)
			}
			counts[key]++
		}
	}
	for index, key := range order {
		order[index] = fmt.Sprintf("%s:%d", key, counts[key])
	}
	return strings.Join(order, ",")
}

func corePayloadAbsent(databasePath string, logs *safeBuffer, marker string) bool {
	if logs == nil || marker == "" || logs.contains(marker) {
		return false
	}
	for _, path := range []string{databasePath, databasePath + "-wal", databasePath + "-shm"} {
		contents, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false
		}
		if bytes.Contains(contents, []byte(marker)) {
			return false
		}
	}
	databaseURL := (&url.URL{Scheme: "file", Path: databasePath}).String() + "?mode=ro"
	database, err := sql.Open("sqlite", databaseURL)
	if err != nil {
		return false
	}
	defer database.Close()
	rows, err := database.Query("SELECT actor, action, resource, result, request_id, COALESCE(before_digest, ''), COALESCE(after_digest, '') FROM audit_events")
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var fields [7]string
		destinations := make([]any, len(fields))
		for index := range fields {
			destinations[index] = &fields[index]
		}
		if rows.Scan(destinations...) != nil {
			return false
		}
		for _, field := range fields {
			if strings.Contains(field, marker) {
				return false
			}
		}
	}
	return rows.Err() == nil
}

type certificateSet struct {
	root               []byte
	core               certificate
	server             certificate
	forms              certificate
	serverRegistration certificate
	formsRegistration  certificate
	revoked            certificate
	crl                []byte
	revokedCRL         []byte
	formsRevokedCRL    []byte
}

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	formsDriver, formsDSN, err := formsDatabaseSelection()
	if err != nil {
		return err
	}
	var formsSQLProxy *databaseProxy
	if formsDriver == "mysql" || formsDriver == "postgres" {
		formsSQLProxy, formsDSN, err = newDatabaseProxy(formsDriver, formsDSN)
		if err != nil {
			return err
		}
		defer formsSQLProxy.Close()
	}
	directory, err := os.MkdirTemp("", "core-server-e2e-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	formsSettings, err := buildFormsSettings(directory, formsDriver, formsDSN)
	if err != nil {
		return err
	}
	childEnvironment := withoutVariables(os.Environ(),
		"LIAPOLDUS_V1_E2E_FORMS_DRIVER", "LIAPOLDUS_V1_E2E_FORMS_DSN",
		"LIAPOLDUS_V1_E2E_POSTGRES_DSN", "LIAPOLDUS_V1_E2E_MYSQL_DSN", "LIAPOLDUS_V1_E2E_MARIADB_DSN",
	)
	childNoSQLDSNEnvironment := true
	for _, name := range []string{
		"LIAPOLDUS_V1_E2E_FORMS_DSN", "LIAPOLDUS_V1_E2E_POSTGRES_DSN",
		"LIAPOLDUS_V1_E2E_MYSQL_DSN", "LIAPOLDUS_V1_E2E_MARIADB_DSN",
	} {
		if environmentHasVariable(childEnvironment, name) {
			childNoSQLDSNEnvironment = false
		}
	}
	if !childNoSQLDSNEnvironment {
		return errors.New("SQL test connection variable reached a child process environment")
	}
	issued, err := issueCertificates()
	if err != nil {
		return err
	}
	root, coreCert, serverCert, formsCert, revokedCert, crl, revokedCRL :=
		issued.root, issued.core, issued.server, issued.forms, issued.revoked, issued.crl, issued.revokedCRL
	for name, value := range map[string][]byte{
		"root.pem": root, "core.pem": coreCert.pem, "core.key": coreCert.key,
		"server-registration.pem": issued.serverRegistration.pem, "server-registration.key": issued.serverRegistration.key,
		"forms-registration.pem": issued.formsRegistration.pem, "forms-registration.key": issued.formsRegistration.key,
		"server.pem": serverCert.pem, "server.key": serverCert.key,
		"forms.pem": formsCert.pem, "forms.key": formsCert.key, "revocation.pem": crl,
		"revoked.pem": revokedCert.pem,
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
	siteAddress, err := freeAddress()
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
	serverDataHome := filepath.Join(directory, "server-data")
	if err := os.MkdirAll(serverDataHome, 0o700); err != nil {
		return err
	}
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
	childEnvironment = append(childEnvironment,
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
		"CORE_INIT_REPLICA_CLIENT_CRLS="+filepath.Join(directory, "revocation.pem"),
		"CORE_INIT_REPLICA_SERVER_CRLS="+filepath.Join(directory, "revocation.pem"),
		"CORE_INIT_SECRET_ROOT="+directory,
	)
	initialize := exec.Command(coreBinary, "init")
	initialize.Dir = coreRoot
	initialize.Env = childEnvironment
	if output, err := initialize.CombinedOutput(); err != nil {
		return fmt.Errorf("Core init failed: %w: %s", err, output)
	}
	bootstrap := exec.Command(coreBinary, "access", "bootstrap")
	bootstrap.Dir = coreRoot
	bootstrap.Env = childEnvironment
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
		"--server-cert="+filepath.Join(directory, "server-registration.pem"), "--server-key="+filepath.Join(directory, "server-registration.key"),
		"--client-cert="+filepath.Join(directory, "server-registration.pem"), "--client-key="+filepath.Join(directory, "server-registration.key"),
		"--crl-file="+filepath.Join(directory, "revocation.pem"),
		"--peer-target-id=forms-child", "--peer-endpoint="+formsPeerAddress,
		"--peer-identity=spiffe://liapoldus.test/server", "--peer-expected-identity=spiffe://liapoldus.test/forms",
		"--peer-ca-file="+filepath.Join(directory, "root.pem"),
		"--peer-cert="+filepath.Join(directory, "server.pem"), "--peer-key="+filepath.Join(directory, "server.key"),
	)
	server.Dir = serverRoot
	server.Env = replaceEnvironment(childEnvironment, "XDG_DATA_HOME", serverDataHome)
	serverLogs := &safeBuffer{}
	server.Stdout = serverLogs
	server.Stderr = serverLogs
	if err := server.Start(); err != nil {
		return err
	}
	defer func() { stop(server) }()
	formsArgs := []string{
		"--instance-id=forms-child", "--replica-id=forms-replica", "--rest-listen=" + formsRESTAddress,
		"--core-url=https://" + controlAddress, "--core-server-name=localhost",
		"--core-common-name=core-child", "--core-client-common-name=core-child",
		"--ca-file=" + filepath.Join(directory, "root.pem"),
		"--server-cert=" + filepath.Join(directory, "forms-registration.pem"), "--server-key=" + filepath.Join(directory, "forms-registration.key"),
		"--client-cert=" + filepath.Join(directory, "forms-registration.pem"), "--client-key=" + filepath.Join(directory, "forms-registration.key"),
		"--crl-file=" + filepath.Join(directory, "revocation.pem"),
		"--peer-listen=" + formsPeerAddress, "--peer-identity=spiffe://liapoldus.test/forms",
		"--peer-allowed-caller=spiffe://liapoldus.test/server",
		"--peer-ca-file=" + filepath.Join(directory, "root.pem"),
		"--peer-cert=" + filepath.Join(directory, "forms.pem"), "--peer-key=" + filepath.Join(directory, "forms.key"),
		"--peer-carrier=tcp",
	}
	formsLogs := &safeBuffer{}
	forms := exec.Command(formsBinary, formsArgs...)
	forms.Dir = formsRoot
	forms.Env = childEnvironment
	forms.Stdout = formsLogs
	forms.Stderr = formsLogs
	if err := forms.Start(); err != nil {
		return err
	}
	defer func() { stop(forms) }()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(root) {
		return errors.New("invalid test trust root")
	}
	if err := waitServerHealth(restAddress, roots, coreCert); err != nil {
		return fmt.Errorf("%w (Server logs: %s)", err, serverLogs.String())
	}
	if err := waitServerHealth(formsRESTAddress, roots, coreCert); err != nil {
		return fmt.Errorf("%w (forms-db logs: %s)", err, formsLogs.String())
	}
	core := exec.Command(coreBinary, "serve")
	core.Dir = coreRoot
	core.Env = childEnvironment
	coreLogs := &safeBuffer{}
	core.Stdout = coreLogs
	core.Stderr = coreLogs
	if err := core.Start(); err != nil {
		return err
	}
	defer func() { stop(core) }()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, Timeout: 5 * time.Second}
	base := "https://" + managementAddress
	if err := waitForHealth(client, base, core); err != nil {
		return err
	}
	stopServerLease, err := registerTestReplica(controlAddress, directory, "server-child", "server-replica", "manual-server-incarnation", restAddress, "server-registration")
	if err != nil {
		return fmt.Errorf("register Server replica: %w", err)
	}
	defer func() { stopServerLease() }()
	stopFormsLease, err := registerTestReplica(controlAddress, directory, "forms-child", "forms-replica", "manual-forms-incarnation", formsRESTAddress, "forms-registration")
	if err != nil {
		return fmt.Errorf("register forms-db replica: %w", err)
	}
	defer func() { stopFormsLease() }()
	if err := waitForPluginInstances(databasePath, "server-child", "forms-child"); err != nil {
		return fmt.Errorf("Core did not durably admit plugin registrations: %w", err)
	}
	databaseRestoreRejectedWhileServing, err := verifyRestoreRejectedWhileServing(coreBinary, coreRoot, childEnvironment, directory)
	if err != nil {
		return err
	}
	firstSettings := settings(publicAddress, siteAddress, oldOrigin.Addr())
	firstID, err := putSettings(client, base, token, `"0"`, "initial-server-config-0001", firstSettings)
	if err != nil {
		return err
	}
	if err := waitOperation(client, base, token, firstID, databasePath); err != nil {
		return fmt.Errorf("%w (Server logs: %s; forms-db logs: %s; Core logs: %s)", err,
			serverLogs.lifecycleSummary(), formsLogs.lifecycleSummary(), coreLogs.String())
	}
	if _, err := waitForConvergence(client, base, token); err != nil {
		return fmt.Errorf("Core did not converge registered replicas before site publish: %w", err)
	}
	sitePublishAccepted, siteOperationCompleted, err := publishSite(client, base, token, siteAddress)
	if err != nil {
		return fmt.Errorf("%w (replicas: %s; Server logs: %s; Core logs: %s)", err,
			pluginReplicaObservations(databasePath), serverLogs.lifecycleSummary(), coreLogs.String())
	}
	publishedSite, err := readPublic(siteAddress)
	if err != nil || publishedSite != "published-site" {
		return fmt.Errorf("published site did not serve expected content: %q (%v)", publishedSite, err)
	}
	first, err := readPublic(publicAddress)
	if err != nil {
		return err
	}
	secondSettings := settings(publicAddress, siteAddress, newOrigin.Addr())
	secondID, err := putSettings(client, base, token, `"1"`, "updated-server-config-0002", secondSettings)
	if err != nil {
		return err
	}
	if err := waitOperation(client, base, token, secondID, databasePath); err != nil {
		return err
	}
	formsID, err := putPluginSettings(client, base, token, "forms-child", `"0"`, "initial-forms-config-0001", formsSettings)
	if err != nil {
		return fmt.Errorf("initial forms-db settings: %w", err)
	}
	if err := waitOperation(client, base, token, formsID, databasePath); err != nil {
		return err
	}
	if _, err := waitForConvergence(client, base, token); err != nil {
		return fmt.Errorf("Core did not converge registered replicas before scoped-grant test: %w", err)
	}
	staleProductionGrantDenied, err := denyProductionGrantAfterGenerationChange(
		client, base, token, controlAddress, roots, issued.formsRegistration, formsSettings, databasePath, coreLogs,
	)
	if err != nil {
		return fmt.Errorf("production stale-grant check failed: %w", err)
	}
	if !staleProductionGrantDenied {
		return errors.New("Core accepted a secret grant after its plugin configuration generation changed")
	}
	const allowedCookieValue = "liapoldus-allowed-cookie-marker"
	const unlistedCookieValue = "liapoldus-unlisted-cookie-marker"
	formsSubmit, err := submitFormWithCookies(publicAddress, "core-e2e@example.test", allowedCookieValue, unlistedCookieValue)
	if err != nil {
		return err
	}
	incomingCookiesRedactedEverywhere := corePayloadAbsent(databasePath, coreLogs, allowedCookieValue) &&
		corePayloadAbsent(databasePath, coreLogs, unlistedCookieValue) &&
		!serverLogs.contains(allowedCookieValue) && !serverLogs.contains(unlistedCookieValue) &&
		!formsLogs.contains(allowedCookieValue) && !formsLogs.contains(unlistedCookieValue)
	if !incomingCookiesRedactedEverywhere {
		return errors.New("an incoming cookie value appeared in Core state or plugin diagnostics")
	}
	formsListRoundTrip, err := listSubmittedForm(publicAddress)
	if err != nil {
		return fmt.Errorf("list forms submission through the production Server route: %w", err)
	}
	formsConcurrentRoundTrip, err := concurrentFormsRoundTrip(publicAddress, 24)
	if err != nil {
		return fmt.Errorf("concurrent forms round trip through the production Server route: %w", err)
	}
	var formsRecordID string
	formsAdminQueryRoundTrip, formsAdminCursorGrantRoundTrip, err := queryFormsAdminThroughCore(client, base, token, databasePath, "core-e2e@example.test", &formsRecordID)
	if err != nil {
		return fmt.Errorf("forms Admin Surface query through Core failed: %w", err)
	}
	if !formsAdminQueryRoundTrip {
		return errors.New("forms Admin Surface query through Core did not return and audit the expected data")
	}
	coreDidNotObservePeerPayload := corePayloadAbsent(databasePath, coreLogs, "core-e2e@example.test")
	if !coreDidNotObservePeerPayload {
		return errors.New("Core logs or durable SQLite files contained a forms payload")
	}
	formsCandidateRefusalPreserved := false
	formsCandidateRollbackRecovered := false
	formsListAfterCandidateRollback := false
	stop(core)
	coreUnavailableStatus, coreUnavailableBody, err := listSubmittedFormResponse(publicAddress)
	if err != nil || coreUnavailableStatus != http.StatusServiceUnavailable {
		return errors.New("forms list did not fail closed while Core secret-grant service was unavailable")
	}
	var coreUnavailableProblem struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(coreUnavailableBody, &coreUnavailableProblem) != nil || coreUnavailableProblem.Code != "storage_unavailable" ||
		bytes.Contains(coreUnavailableBody, []byte("forms-child-dsn")) || bytes.Contains(coreUnavailableBody, []byte("cursor-signing")) {
		return errors.New("Core-unavailable response did not use the bounded redacted forms error")
	}
	coreUnavailableFailsClosed := true
	core = exec.Command(coreBinary, "serve")
	core.Dir = coreRoot
	core.Env = childEnvironment
	if err := core.Start(); err != nil {
		return err
	}
	if err := waitForHealth(client, base, core); err != nil {
		return err
	}
	if _, err := waitForConvergence(client, base, token); err != nil {
		return fmt.Errorf("Core unavailable recovery: %w", err)
	}
	coreUnavailableRecovered, err := waitForFormsList(publicAddress)
	if err != nil || !coreUnavailableRecovered {
		return errors.New("forms list did not recover after Core secret-grant service returned")
	}
	formsStorageUnavailableDuringOutage := false
	formsStorageRecoveredAfterOutage := false
	if formsSQLProxy != nil {
		formsSQLProxy.Pause()
		status, body, queryErr := listSubmittedFormResponse(publicAddress)
		if queryErr != nil || status != http.StatusServiceUnavailable {
			return errors.New("production Server did not return retryable failure during external SQL outage")
		}
		var problem struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(body, &problem) != nil || problem.Code != "storage_unavailable" ||
			strings.Contains(strings.ToLower(string(body)), "dsn") ||
			strings.Contains(strings.ToLower(string(body)), "postgres") ||
			strings.Contains(strings.ToLower(string(body)), "mysql") {
			return errors.New("external SQL outage response was not classified and redacted")
		}
		formsStorageUnavailableDuringOutage = true
		formsSQLProxy.Resume()
		formsStorageRecoveredAfterOutage, err = waitForFormsList(publicAddress)
		if err != nil {
			return fmt.Errorf("production forms route did not recover after external SQL outage: %w", err)
		}
		if !formsStorageRecoveredAfterOutage {
			return errors.New("production forms route recovered without the persisted submission")
		}
	}
	formsDataPersistedAfterRestart := false
	if formsDriver != "memory" {
		formsDataPersistedBeforeRestart, err := listSubmittedForm(publicAddress)
		if err != nil {
			return fmt.Errorf("list external SQL submission before forms-db restart: %w", err)
		}
		if !formsDataPersistedBeforeRestart {
			return errors.New("forms-db external SQL submission was not readable before plugin restart")
		}
	}
	stop(forms)
	if err := waitForDegraded(client, base, token); err != nil {
		return err
	}
	forms = exec.Command(formsBinary, formsArgs...)
	forms.Dir = formsRoot
	forms.Env = childEnvironment
	forms.Stdout = formsLogs
	forms.Stderr = formsLogs
	if err := forms.Start(); err != nil {
		return err
	}
	if err := waitServerHealth(formsRESTAddress, roots, coreCert); err != nil {
		return err
	}
	stop(core)
	core = exec.Command(coreBinary, "serve")
	core.Dir = coreRoot
	core.Env = childEnvironment
	if err := core.Start(); err != nil {
		return err
	}
	if err := waitForHealth(client, base, core); err != nil {
		return err
	}
	if _, err := waitForConvergence(client, base, token); err != nil {
		return fmt.Errorf("forms-db manual restart recovery: %w", err)
	}
	formsDataPersistedAfterRestart, err = waitForFormsList(publicAddress)
	if err != nil {
		return err
	}
	formsPeerReconnected := true
	if formsDriver != "memory" && !formsDataPersistedAfterRestart {
		return errors.New("forms-db external SQL submission did not persist after plugin restart")
	}
	formsListWithoutLeaseRejected, err := rejectFormsListWithoutLease(
		client, base, token, formsBinary, formsArgs, formsRoot, childEnvironment,
		databasePath, publicAddress,
		&forms,
	)
	if err != nil {
		return fmt.Errorf("production stale-generation forms.list check failed: %w", err)
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
	serverArgs := append([]string(nil), server.Args[1:]...)
	stop(server)
	if err := waitForDegraded(client, base, token); err != nil {
		return err
	}
	server = exec.Command(serverBinary, serverArgs...)
	server.Dir = serverRoot
	server.Env = replaceEnvironment(childEnvironment, "XDG_DATA_HOME", serverDataHome)
	if err := server.Start(); err != nil {
		return err
	}
	if err := waitServerHealth(restAddress, roots, coreCert); err != nil {
		return err
	}
	// A fresh authenticated lease is the operator-owned restart boundary. Core
	// re-announces the durable active generation to the new incarnation.
	serverRestartConverged, err := waitForConvergence(client, base, token)
	if err != nil {
		return fmt.Errorf("Core did not reapply the active generation after Server restart: %w", err)
	}
	stop(core)
	core = exec.Command(coreBinary, "serve")
	core.Dir = coreRoot
	core.Env = childEnvironment
	if err := core.Start(); err != nil {
		return err
	}
	if err := waitForHealth(client, base, core); err != nil {
		return err
	}
	serverReconnectConverged, err := waitForConvergence(client, base, token)
	if err != nil {
		return fmt.Errorf("Server manual restart recovery: %w", err)
	}
	reconnectedServer, err := waitPublic(publicAddress)
	if err != nil || reconnectedServer != "old" {
		return fmt.Errorf("Server did not restore active config after operator restart: %q (%v)", reconnectedServer, err)
	}
	stop(core)
	stop(server)
	server = exec.Command(serverBinary, serverArgs...)
	server.Dir = serverRoot
	server.Env = replaceEnvironment(childEnvironment, "XDG_DATA_HOME", serverDataHome)
	if err := server.Start(); err != nil {
		return err
	}
	if err := waitServerHealth(restAddress, roots, coreCert); err != nil {
		return err
	}
	core = exec.Command(coreBinary, "serve")
	core.Dir = coreRoot
	core.Env = childEnvironment
	if err := core.Start(); err != nil {
		return err
	}
	if err := waitForHealth(client, base, core); err != nil {
		return err
	}
	coreRestartConverged, err := waitForConvergence(client, base, token)
	if err != nil {
		return fmt.Errorf("joint Server/Core restart recovery: %w", err)
	}
	restartedServer, err := waitPublic(publicAddress)
	if err != nil {
		return err
	}
	restartedPublishedSite, err := waitPublic(siteAddress)
	if err != nil {
		return err
	}
	rogueStatus, err := pullGeneration(controlAddress, roots, revokedCert, 2)
	if err != nil || rogueStatus != http.StatusOK {
		return fmt.Errorf("fixture identity was not accepted before its CRL revocation: status=%d (%v)", rogueStatus, err)
	}
	if err := os.WriteFile(filepath.Join(directory, "revocation.pem"), revokedCRL, 0o600); err != nil {
		return err
	}
	stop(core)
	core = exec.Command(coreBinary, "serve")
	core.Dir = coreRoot
	core.Env = childEnvironment
	if err := core.Start(); err != nil {
		return err
	}
	if err := waitForHealth(client, base, core); err != nil {
		return err
	}
	revokedStatus, revokedErr := pullGeneration(controlAddress, roots, revokedCert, 2)
	pluginControlRevocationEnforced := revokedErr != nil || revokedStatus != http.StatusOK
	if !pluginControlRevocationEnforced {
		return errors.New("Core accepted a replica identity listed in its configured signed CRL")
	}
	oldRoots := roots.Clone()
	rotated, err := issueCertificates()
	if err != nil {
		return err
	}
	// The fixture's renewal clients hold the trust root and certificate loaded
	// at registration time. Stop them before rotating the files and recreate
	// them after Core and both plugin processes trust the replacement root.
	stopServerLease()
	stopFormsLease()
	stop(forms)
	stop(server)
	stop(core)
	for name, value := range map[string][]byte{
		"root.pem": rotated.root, "core.pem": rotated.core.pem, "core.key": rotated.core.key,
		"server.pem": rotated.server.pem, "server.key": rotated.server.key,
		"forms.pem": rotated.forms.pem, "forms.key": rotated.forms.key,
		"server-registration.pem": rotated.serverRegistration.pem, "server-registration.key": rotated.serverRegistration.key,
		"forms-registration.pem": rotated.formsRegistration.pem, "forms-registration.key": rotated.formsRegistration.key,
		"revocation.pem": rotated.crl,
	} {
		if err := os.WriteFile(filepath.Join(directory, name), value, 0o600); err != nil {
			return err
		}
	}
	roots = x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rotated.root) {
		return errors.New("invalid rotated trust root")
	}
	forms = exec.Command(formsBinary, formsArgs...)
	forms.Dir = formsRoot
	forms.Env = childEnvironment
	forms.Stdout = formsLogs
	forms.Stderr = formsLogs
	if err := forms.Start(); err != nil {
		return err
	}
	server = exec.Command(serverBinary, serverArgs...)
	server.Dir = serverRoot
	server.Env = replaceEnvironment(childEnvironment, "XDG_DATA_HOME", serverDataHome)
	if err := server.Start(); err != nil {
		return err
	}
	client = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, Timeout: 5 * time.Second}
	if err := waitServerHealth(restAddress, roots, rotated.core); err != nil {
		return err
	}
	if err := waitServerHealth(formsRESTAddress, roots, rotated.core); err != nil {
		return err
	}
	core = exec.Command(coreBinary, "serve")
	core.Dir = coreRoot
	core.Env = childEnvironment
	if err := core.Start(); err != nil {
		return err
	}
	if err := waitForHealth(client, base, core); err != nil {
		return err
	}
	stopServerLease, err = registerTestReplica(controlAddress, directory, "server-child", "server-replica", "manual-server-incarnation", restAddress, "server-registration")
	if err != nil {
		return fmt.Errorf("register Server replica after trust-root rotation: %w", err)
	}
	stopFormsLease, err = registerTestReplica(controlAddress, directory, "forms-child", "forms-replica", "manual-forms-incarnation", formsRESTAddress, "forms-registration")
	if err != nil {
		return fmt.Errorf("register forms-db replica after trust-root rotation: %w", err)
	}
	if _, err := waitForConvergence(client, base, token); err != nil {
		return fmt.Errorf("trust-root rotation recovery (%s; server=%s; forms=%s): %w",
			pluginReplicaObservations(databasePath), serverLogs.lifecycleSummary(), formsLogs.lifecycleSummary(), err)
	}
	newRootPullStatus, newRootPullErr := pullGeneration(controlAddress, roots, rotated.formsRegistration, 2)
	oldRootPullStatus, oldRootPullErr := pullGeneration(controlAddress, oldRoots, issued.formsRegistration, 2)
	coordinatedTrustRootRotationConverged := newRootPullErr == nil && newRootPullStatus == http.StatusOK &&
		(oldRootPullErr != nil || oldRootPullStatus != http.StatusOK)
	if !coordinatedTrustRootRotationConverged {
		return errors.New("Core and manually restarted plugin replicas did not converge on the replacement trust root")
	}
	formsListRevokedReplicaDenied, err := denyFormsListForRevokedReplica(
		client, base, token, coreBinary, coreRoot, directory, childEnvironment,
		controlAddress, roots, rotated.formsRegistration, publicAddress,
		rotated.formsRevokedCRL,
		&core, coreLogs,
	)
	if err != nil {
		return fmt.Errorf("production revoked-replica forms.list check failed: %w", err)
	}
	if formsSQLProxy != nil {
		candidateSettings, candidateErr := buildRejectedFormsSettings(directory, formsDriver, formsSettings)
		if candidateErr != nil {
			return candidateErr
		}
		candidateID, candidateErr := putPluginSettings(client, base, token, "forms-child", `"2"`, "rejected-forms-config-0004", candidateSettings)
		if candidateErr != nil {
			return fmt.Errorf("rejected forms-db candidate settings: %w", candidateErr)
		}
		if err := waitFailedOperation(client, base, token, candidateID); err != nil {
			return fmt.Errorf("rejected forms-db candidate operation did not finish: %w", err)
		}
		if err := waitForDegraded(client, base, token); err != nil {
			return fmt.Errorf("Core did not fence the rejected forms-db candidate: %w", err)
		}
		formsCandidateRefusalPreserved, err = submitFormWithEmail(publicAddress, "candidate-refusal@example.test")
		if err != nil || !formsCandidateRefusalPreserved {
			return errors.New("previous forms-db runtime did not remain usable after candidate refusal")
		}
		rollbackID, rollbackErr := rollbackSettings(client, base, token, "forms-child", `"3"`, "rollback-forms-config-0005")
		if rollbackErr != nil {
			return rollbackErr
		}
		if err := waitOperation(client, base, token, rollbackID, databasePath); err != nil {
			return fmt.Errorf("forms-db rollback after rejected candidate did not complete: %w", err)
		}
		rollbackSubmitRecovered, submitErr := submitFormWithEmail(publicAddress, "rollback-recovered@example.test")
		if submitErr != nil {
			return fmt.Errorf("forms-db did not resume submissions after candidate rollback: %w", submitErr)
		}
		storedSettings, readErr := activePluginSettings(databasePath, "forms-child")
		if readErr != nil {
			return readErr
		}
		formsCandidateRollbackRecovered = rollbackSubmitRecovered && bytes.Equal(storedSettings, formsSettings)
		if !formsCandidateRollbackRecovered {
			return errors.New("forms-db rollback did not restore the exact previous settings bytes")
		}
		formsListAfterCandidateRollback, err = waitForFormsList(publicAddress)
		if err != nil {
			return fmt.Errorf("forms-db list did not recover after candidate rollback: %w (lifecycle=%s)", err, formsLogs.lifecycleSummary())
		}
		if !formsListAfterCandidateRollback {
			return errors.New("forms-db list did not return the durable submission after candidate rollback")
		}
	}
	adminDeleteSubmission, err := submitFormWithEmail(publicAddress, "admin-delete@example.test")
	if err != nil || !adminDeleteSubmission {
		return errors.New("could not prepare a forms submission for Admin Action deletion")
	}
	formsAdminDeleteSemantics, err := verifyFormsAdminDeleteThroughCore(client, base, token, databasePath, coreLogs)
	if err != nil {
		return fmt.Errorf("forms Admin Action delete through Core failed: %w", err)
	}
	hash := sha256.Sum256(firstSettings)
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"first": first, "second": second, "afterRollback": afterRollback,
		"active": active, "previous": previous,
		"exactDigest":                           bytes.Equal(stored, firstSettings) && digest == hex.EncodeToString(hash[:]),
		"formsActive":                           formsActive,
		"restartedServer":                       restartedServer,
		"sitePublishAccepted":                   sitePublishAccepted,
		"siteOperationCompleted":                siteOperationCompleted,
		"publishedSite":                         publishedSite,
		"restartedPublishedSite":                restartedPublishedSite,
		"serverRestartConverged":                serverRestartConverged,
		"coreRestartConverged":                  coreRestartConverged,
		"coreUnavailableFailsClosed":            coreUnavailableFailsClosed,
		"coreUnavailableRecovered":              coreUnavailableRecovered,
		"pluginControlRevocationEnforced":       pluginControlRevocationEnforced,
		"coordinatedTrustRootRotationConverged": coordinatedTrustRootRotationConverged,
		"serverReconnectConverged":              serverReconnectConverged,
		"formsSubmit":                           formsSubmit,
		"formsAdminQueryRoundTrip":              formsAdminQueryRoundTrip,
		"formsAdminCursorGrantRoundTrip":        formsAdminCursorGrantRoundTrip,
		"formsAdminDeleteSemantics":             formsAdminDeleteSemantics,
		"staleProductionGrantDenied":            staleProductionGrantDenied,
		"formsListWithoutLeaseRejected":         formsListWithoutLeaseRejected,
		"formsListRevokedReplicaDenied":         formsListRevokedReplicaDenied,
		"incomingCookiesRedactedEverywhere":     incomingCookiesRedactedEverywhere,
		"coreDidNotObservePeerPayload":          coreDidNotObservePeerPayload,
		"formsDatabaseDriver":                   formsDriver,
		"formsListRoundTrip":                    formsListRoundTrip,
		"formsConcurrentRoundTrip":              formsConcurrentRoundTrip,
		"formsCandidateRefusalPreserved":        formsCandidateRefusalPreserved,
		"formsCandidateRollbackRecovered":       formsCandidateRollbackRecovered,
		"formsListAfterCandidateRollback":       formsListAfterCandidateRollback,
		"formsPeerReconnected":                  formsPeerReconnected,
		"formsStorageUnavailableDuringOutage":   formsStorageUnavailableDuringOutage,
		"formsStorageRecoveredAfterOutage":      formsStorageRecoveredAfterOutage,
		"databaseRestoreRejectedWhileServing":   databaseRestoreRejectedWhileServing,
		"childNoSQLDSNEnvironment":              childNoSQLDSNEnvironment,
		"formsDataPersistedAfterRestart":        formsDataPersistedAfterRestart,
	})
}

func pluginReplicaObservations(path string) string {
	databaseURL := (&url.URL{Scheme: "file", Path: path}).String() + "?mode=ro"
	database, err := sql.Open("sqlite", databaseURL)
	if err != nil {
		return "observations-unavailable"
	}
	defer database.Close()
	rows, err := database.Query(`SELECT instance_id, replica_id, observed_generation, observed_state, COALESCE(last_failure_code, '') FROM plugin_replicas ORDER BY instance_id, replica_id`)
	if err != nil {
		return "observations-unavailable"
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var instanceID, replicaID, state, failureCode string
		var generation sql.NullInt64
		if rows.Scan(&instanceID, &replicaID, &generation, &state, &failureCode) != nil {
			return "observations-unavailable"
		}
		generationText := "none"
		if generation.Valid {
			generationText = strconv.FormatInt(generation.Int64, 10)
		}
		values = append(values, instanceID+"/"+replicaID+":"+state+":"+generationText+":"+failureCode)
	}
	if rows.Err() != nil {
		return "observations-unavailable"
	}
	return strings.Join(values, ",")
}

func denyProductionGrantAfterGenerationChange(
	managementClient *http.Client,
	managementBase, token, controlAddress string,
	roots *x509.CertPool,
	credential certificate,
	settings []byte,
	databasePath string,
	coreLogs *safeBuffer,
) (bool, error) {
	var configured struct {
		CursorSecretRef string `json:"cursorSecretRef"`
	}
	if json.Unmarshal(settings, &configured) != nil || configured.CursorSecretRef == "" {
		return false, errors.New("forms-db settings have no cursor secret reference")
	}
	contract, err := pluginsdk.LoadHTTPContract()
	if err != nil {
		return false, errors.New("Plugin SDK HTTP contract unavailable to integration fixture")
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return false, errors.New("Core settings database unavailable to integration fixture")
	}
	var generation int64
	err = database.QueryRow(`SELECT generation FROM plugin_config_generations WHERE instance_id = ? AND slot = 'active'`, "forms-child").Scan(&generation)
	_ = database.Close()
	if err != nil || generation < 1 {
		return false, errors.New("forms-db active configuration generation unavailable")
	}
	pair, err := tls.X509KeyPair(credential.pem, credential.key)
	if err != nil {
		return false, errors.New("forms-db test identity unavailable")
	}
	controlClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: roots, Certificates: []tls.Certificate{pair}, ServerName: "localhost", MinVersion: tls.VersionTLS12,
	}}, Timeout: 5 * time.Second}
	grantRequest, err := json.Marshal(sdkmodels.SecretGrantRequest{
		Reference: configured.CursorSecretRef, Purpose: formsDBCursorGrantPurpose, Generation: strconv.FormatInt(generation, 10),
	})
	if err != nil {
		return false, errors.New("could not prepare scoped grant request")
	}
	issue := contract.Core.SecretGrant.Issue
	request, err := http.NewRequest(issue.Method, "https://"+controlAddress+issue.PathTemplate, bytes.NewReader(grantRequest))
	if err != nil {
		return false, errors.New("could not prepare scoped grant call")
	}
	request.Header.Set("Content-Type", issue.RequestMediaType)
	response, err := controlClient.Do(request)
	if err != nil {
		return false, errors.New("Core did not answer scoped grant call")
	}
	if response.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		return false, fmt.Errorf("Core refused scoped grant for the active generation (status=%d body=%s replicas=%s)",
			response.StatusCode, payload, pluginReplicaObservations(databasePath))
	}
	var grant sdkmodels.SecretGrant
	decodeErr := json.NewDecoder(io.LimitReader(response.Body, issue.MaximumResponseBytes)).Decode(&grant)
	_ = response.Body.Close()
	if decodeErr != nil || grant.Handle == "" {
		return false, errors.New("Core returned an invalid scoped grant")
	}
	operationID, err := putPluginSettings(managementClient, managementBase, token, "forms-child", `"1"`, "stale-grant-generation-0002", settings)
	if err != nil {
		return false, err
	}
	if err := waitOperation(managementClient, managementBase, token, operationID, databasePath); err != nil {
		return false, errors.New("Core did not activate the next forms-db generation")
	}
	redemption := contract.Core.SecretGrant.Redemption
	redemptionPath := strings.Replace(redemption.PathTemplate, "{handle}", url.PathEscape(grant.Handle), 1)
	redemptionBody, err := json.Marshal(sdkmodels.SecretRedemption{Handle: grant.Handle})
	if err != nil {
		return false, errors.New("could not prepare scoped redemption")
	}
	request, err = http.NewRequest(redemption.Method, "https://"+controlAddress+redemptionPath, bytes.NewReader(redemptionBody))
	if err != nil {
		return false, errors.New("could not prepare scoped redemption call")
	}
	request.Header.Set("Content-Type", redemption.RequestMediaType)
	response, err = controlClient.Do(request)
	if err != nil {
		return false, errors.New("Core did not answer scoped redemption")
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, redemption.MaximumResponseBytes+1))
	if readErr != nil || int64(len(responseBody)) > redemption.MaximumResponseBytes {
		return false, errors.New("Core returned an unusable scoped-redemption response")
	}
	deniedProblem := contract.Errors[contract.OutcomeProblems[string(sdkmodels.OutcomeGrantDenied)]]
	handleAbsent := !bytes.Contains(responseBody, []byte(grant.Handle))
	payloadAbsent := corePayloadAbsent(databasePath, coreLogs, grant.Handle)
	if response.StatusCode != deniedProblem.Status || !handleAbsent || !payloadAbsent {
		return false, fmt.Errorf("stale grant conformance mismatch (status=%d expected=%d handleAbsent=%t payloadAbsent=%t)",
			response.StatusCode, deniedProblem.Status, handleAbsent, payloadAbsent)
	}
	return true, nil
}

func rejectFormsListWithoutLease(
	client *http.Client, base, token, formsBinary string, formsArgs []string,
	formsRoot string, childEnvironment []string, databasePath, publicAddress string,
	forms **exec.Cmd,
) (bool, error) {
	stop(*forms)
	if err := waitForDegraded(client, base, token); err != nil {
		return false, errors.New("Core did not fence the forms-db replica after its lease expired")
	}
	status, body, err := listSubmittedFormResponse(publicAddress)
	if err != nil {
		return false, errors.New("Server did not return a bounded response without an active forms-db lease")
	}
	if status != http.StatusServiceUnavailable || len(body) > 4096 ||
		bytes.Contains(body, []byte("forms-child-dsn")) ||
		bytes.Contains(body, []byte("cursor-signing")) {
		return false, fmt.Errorf("forms.list without an active lease was not denied with a bounded redacted error (status=%d body=%s)", status, body)
	}
	process := exec.Command(formsBinary, formsArgs...)
	process.Dir, process.Env = formsRoot, childEnvironment
	if err := process.Start(); err != nil {
		return false, errors.New("forms-db could not restart after lease loss")
	}
	*forms = process
	if _, err := waitForConvergence(client, base, token); err != nil {
		return false, fmt.Errorf("forms-db did not recover after obtaining a fresh lease (%s): %w", pluginReplicaObservations(databasePath), err)
	}
	if err := waitForFormsListAvailable(publicAddress); err != nil {
		return false, errors.New("forms.list did not recover after forms-db obtained a fresh lease")
	}
	return true, nil
}

func waitForFormsListAvailable(publicAddress string) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, _, err := listSubmittedFormResponse(publicAddress)
		if err == nil && status == http.StatusOK {
			return nil
		}
		if err != nil && !strings.Contains(err.Error(), "connection refused") {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("forms.list remained unavailable after generation recovery")
}

func denyFormsListForRevokedReplica(
	client *http.Client,
	base, token, coreBinary, coreRoot, directory string,
	childEnvironment []string,
	controlAddress string,
	roots *x509.CertPool,
	formsCredential certificate,
	publicAddress string,
	revokedClientCRL []byte,
	core **exec.Cmd,
	coreLogs *safeBuffer,
) (bool, error) {
	status, _, err := listSubmittedFormResponse(publicAddress)
	if err != nil || status != http.StatusOK {
		return false, errors.New("forms.list was not available before workload revocation")
	}
	clientRevocationPath := filepath.Join(directory, "client-revocation.pem")
	if err := os.WriteFile(clientRevocationPath, revokedClientCRL, 0o600); err != nil {
		return false, errors.New("could not prepare revoked replica-client CRL")
	}
	originalSettings, modifiedSettings, revision, err := coreSettingsWithRevokedClientCRL(client, base, token, clientRevocationPath)
	if err != nil {
		return false, errors.New("could not stage Core replica-client revocation setting")
	}
	if err := putCoreSettings(client, base, token, revision, modifiedSettings); err != nil {
		return false, fmt.Errorf("Core rejected replica-client revocation setting: %w", err)
	}
	startCore := func() error {
		process := exec.Command(coreBinary, "serve")
		process.Dir = coreRoot
		process.Env = childEnvironment
		process.Stdout = coreLogs
		process.Stderr = coreLogs
		if err := process.Start(); err != nil {
			return errors.New("Core could not start during replica revocation check")
		}
		*core = process
		return waitForHealth(client, base, process)
	}
	stop(*core)
	if err := startCore(); err != nil {
		return false, err
	}
	pullStatus, pullErr := pullGeneration(controlAddress, roots, formsCredential, 3)
	if pullErr == nil && pullStatus == http.StatusOK {
		return false, errors.New("Core accepted a replica certificate listed in its client CRL")
	}
	status, body, err := listSubmittedFormResponse(publicAddress)
	if err != nil {
		return false, errors.New("Server did not return a bounded response for revoked-replica forms.list")
	}
	var problem struct {
		Code string `json:"code"`
	}
	if status != http.StatusServiceUnavailable || json.Unmarshal(body, &problem) != nil ||
		problem.Code != "storage_unavailable" || bytes.Contains(body, []byte("forms-child-dsn")) ||
		bytes.Contains(body, []byte("cursor-signing")) || bytes.Contains(body, []byte(token)) {
		return false, errors.New("revoked-replica forms.list was not denied with a bounded redacted response")
	}
	revision, _, err = coreSettingsWithCurrentRevision(client, base, token)
	if err != nil {
		return false, errors.New("could not read Core settings revision while restoring replica trust")
	}
	if err := putCoreSettings(client, base, token, revision, originalSettings); err != nil {
		return false, errors.New("could not restore Core replica trust settings")
	}
	stop(*core)
	if err := startCore(); err != nil {
		return false, err
	}
	if _, err := waitForConvergence(client, base, token); err != nil {
		return false, errors.New("Core and plugin replicas did not reconverge after restoring replica trust")
	}
	if err := waitForFormsListAvailable(publicAddress); err != nil {
		return false, errors.New("forms.list did not recover after restoring replica trust")
	}
	return true, nil
}

func coreSettingsWithRevokedClientCRL(client *http.Client, base, token, crlPath string) ([]byte, []byte, int64, error) {
	revision, raw, err := coreSettingsWithCurrentRevision(client, base, token)
	if err != nil {
		return nil, nil, 0, err
	}
	var settings map[string]json.RawMessage
	if json.Unmarshal(raw, &settings) != nil {
		return nil, nil, 0, errors.New("invalid stored settings")
	}
	var control map[string]json.RawMessage
	if json.Unmarshal(settings["pluginControl"], &control) != nil {
		return nil, nil, 0, errors.New("invalid stored plugin-control settings")
	}
	crls, _ := json.Marshal([]string{crlPath})
	control["replicaClientCRLs"] = crls
	controlJSON, _ := json.Marshal(control)
	settings["pluginControl"] = controlJSON
	modified, err := json.Marshal(settings)
	return raw, modified, revision, err
}

func coreSettingsWithCurrentRevision(client *http.Client, base, token string) (int64, []byte, error) {
	request, err := http.NewRequest(http.MethodGet, base+"/api/v2/settings", nil)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	var snapshot struct {
		DesiredRevision int64           `json:"desiredRevision"`
		Settings        json.RawMessage `json:"settings"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&snapshot) != nil || snapshot.DesiredRevision < 1 || len(snapshot.Settings) == 0 {
		return 0, nil, errors.New("Core settings response unavailable")
	}
	return snapshot.DesiredRevision, append([]byte(nil), snapshot.Settings...), nil
}

func putCoreSettings(client *http.Client, base, token string, revision int64, raw []byte) error {
	request, err := http.NewRequest(http.MethodPut, base+"/api/v2/settings", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", fmt.Sprintf(`"core-settings-%d"`, revision))
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Core settings update returned %d: %s", response.StatusCode, body)
	}
	return nil
}

func queryFormsAdminThroughCore(client *http.Client, base, token, databasePath, expectedEmail string, recordID *string) (bool, bool, error) {
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return false, false, errors.New("open Core database for Admin Action audit assertion")
	}
	defer database.Close()
	var countBefore int
	err = database.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&countBefore)
	if err != nil {
		return false, false, fmt.Errorf("read prior forms Admin Action audit count: %w", err)
	}
	request, err := http.NewRequest(http.MethodGet, base+"/api/plugins/admin-surfaces", nil)
	if err != nil {
		return false, false, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return false, false, err
	}
	var surfaces struct {
		Items []struct {
			InstanceID string `json:"instanceId"`
			SHA256     string `json:"sha256"`
		} `json:"items"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&surfaces)
	_ = response.Body.Close()
	if decodeErr != nil || response.StatusCode != http.StatusOK {
		return false, false, errors.New("Core did not return plugin Admin Surface inventory")
	}
	found := false
	surfaceDigest := ""
	for _, surface := range surfaces.Items {
		if surface.InstanceID == "forms-child" {
			found = true
			surfaceDigest = surface.SHA256
		}
	}
	if !found || surfaceDigest == "" {
		return false, false, errors.New("Core Admin Surface inventory omitted the forms-db instance")
	}
	endpoint := "/api/plugins/forms-child/admin/pages/submissions/query"
	containsExpectedRecord := true
	responseItemCounts := make([]int, 0, 2)
	matchingRecords := 0
	requestCount := 0
	cursorRoundTrip := false
	for repetition := range 2 {
		cursor := ""
		foundRecord := false
		seenCursors := make(map[string]struct{})
		for page := range 8 {
			input := map[string]any{"site": "manual-site", "schemaName": "contact", "limit": 10}
			if cursor != "" {
				input["cursor"] = cursor
			}
			inputBytes, marshalErr := json.Marshal(input)
			if marshalErr != nil {
				return false, false, marshalErr
			}
			action, err := http.NewRequest(http.MethodPost, base+endpoint, bytes.NewReader(inputBytes))
			if err != nil {
				return false, false, err
			}
			action.Header.Set("Authorization", "Bearer "+token)
			action.Header.Set("Content-Type", "application/json")
			action.Header.Set("X-Liapoldus-Admin-Surface-Digest", surfaceDigest)
			action.Header.Set("Idempotency-Key", fmt.Sprintf("forms-admin-query-%d-%d", repetition, page))
			result, err := client.Do(action)
			if err != nil {
				return false, false, err
			}
			resultBytes, readErr := io.ReadAll(io.LimitReader(result.Body, 1<<20))
			_ = result.Body.Close()
			var body struct {
				Items []struct {
					Data map[string]any `json:"data"`
					ID   string         `json:"id"`
				} `json:"items"`
				NextCursor *string `json:"nextCursor"`
			}
			decodeErr := json.Unmarshal(resultBytes, &body)
			if readErr != nil || decodeErr != nil || result.StatusCode != http.StatusOK {
				return false, false, fmt.Errorf("Core Admin Action did not proxy the forms query response (status=%d body=%s)", result.StatusCode, resultBytes)
			}
			requestCount++
			responseItemCounts = append(responseItemCounts, len(body.Items))
			for _, item := range body.Items {
				if item.Data["email"] == expectedEmail {
					foundRecord = true
					matchingRecords++
					if recordID != nil {
						*recordID = item.ID
					}
				}
			}
			if body.NextCursor == nil {
				break
			}
			if page == 7 || *body.NextCursor == "" {
				return false, false, errors.New("forms Admin Surface cursor exceeded the bounded page count")
			}
			if _, duplicate := seenCursors[*body.NextCursor]; duplicate {
				return false, false, errors.New("forms Admin Surface cursor repeated before pagination completed")
			}
			seenCursors[*body.NextCursor] = struct{}{}
			cursorRoundTrip = true
			cursor = *body.NextCursor
		}
		containsExpectedRecord = containsExpectedRecord && foundRecord
	}
	var countAfter int
	err = database.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&countAfter)
	if err != nil {
		return false, false, fmt.Errorf("read forms Admin Action audit count: %w", err)
	}
	if !containsExpectedRecord {
		return false, false, fmt.Errorf("repeated Admin Action pagination omitted the expected submission (items=%v, matches=%d)", responseItemCounts, matchingRecords)
	}
	if countAfter != countBefore+requestCount {
		return false, false, fmt.Errorf("repeated Admin Action pagination wrote %d audit records for %d requests", countAfter-countBefore, requestCount)
	}
	return true, cursorRoundTrip, nil
}

func verifyFormsAdminDeleteThroughCore(client *http.Client, base, token, databasePath string, coreLogs *safeBuffer) (bool, error) {
	const email = "admin-delete@example.test"
	var recordID string
	queryOK, _, err := queryFormsAdminThroughCore(client, base, token, databasePath, email, &recordID)
	if err != nil {
		return false, err
	}
	if !queryOK || recordID == "" {
		return false, errors.New("Admin Surface query did not identify the test submission")
	}
	auditBeforeDelete, err := coreAuditCount(databasePath)
	if err != nil {
		return false, err
	}
	var surfaces struct {
		Items []struct {
			InstanceID string `json:"instanceId"`
			SHA256     string `json:"sha256"`
		} `json:"items"`
	}
	surfaceRequest, err := http.NewRequest(http.MethodGet, base+"/api/plugins/admin-surfaces", nil)
	if err != nil {
		return false, err
	}
	surfaceRequest.Header.Set("Authorization", "Bearer "+token)
	surfaceResponse, err := client.Do(surfaceRequest)
	if err != nil {
		return false, err
	}
	surfaceDecodeErr := json.NewDecoder(io.LimitReader(surfaceResponse.Body, 1<<20)).Decode(&surfaces)
	_ = surfaceResponse.Body.Close()
	if surfaceDecodeErr != nil || surfaceResponse.StatusCode != http.StatusOK {
		return false, errors.New("Core did not return Admin Surface digest for forms-db")
	}
	var surfaceDigest string
	for _, surface := range surfaces.Items {
		if surface.InstanceID == "forms-child" {
			surfaceDigest = surface.SHA256
			break
		}
	}
	if surfaceDigest == "" {
		return false, errors.New("Core Admin Surface inventory omitted the forms-db digest")
	}
	endpoint := "/api/plugins/forms-child/admin/pages/submissions/actions/delete"
	input, err := json.Marshal(map[string]string{"site": "manual-site", "schemaName": "contact", "id": recordID})
	if err != nil {
		return false, err
	}
	firstDeleteOK := false
	secondDeleteMissing := false
	for attempt := range 2 {
		request, err := http.NewRequest(http.MethodPost, base+endpoint, bytes.NewReader(input))
		if err != nil {
			return false, err
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Liapoldus-Admin-Surface-Digest", surfaceDigest)
		request.Header.Set("If-Match", "*")
		request.Header.Set("Idempotency-Key", fmt.Sprintf("forms-admin-delete-%d", attempt))
		response, err := client.Do(request)
		if err != nil {
			return false, err
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		if readErr != nil {
			return false, errors.New("read forms Admin Action delete response")
		}
		if attempt == 0 {
			var receipt struct {
				Deleted bool   `json:"deleted"`
				ID      string `json:"id"`
			}
			firstDeleteOK = response.StatusCode == http.StatusOK && json.Unmarshal(body, &receipt) == nil && receipt.Deleted && receipt.ID == recordID
			continue
		}
		var problem struct {
			Code string `json:"code"`
		}
		secondDeleteMissing = response.StatusCode == http.StatusNotFound && json.Unmarshal(body, &problem) == nil && problem.Code == "not_found"
	}
	countAfter, err := coreAuditCount(databasePath)
	if err != nil {
		return false, err
	}
	if !corePayloadAbsent(databasePath, coreLogs, email) || !corePayloadAbsent(databasePath, coreLogs, recordID) {
		return false, errors.New("Core persisted a forms Admin Action payload in logs or audit state")
	}
	return firstDeleteOK && secondDeleteMissing && countAfter == auditBeforeDelete+2, nil
}

func coreAuditCount(databasePath string) (int, error) {
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return 0, errors.New("open Core database for audit count")
	}
	defer database.Close()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&count); err != nil {
		return 0, errors.New("read Core audit count")
	}
	return count, nil
}

func verifyRestoreRejectedWhileServing(binary, workingDirectory string, environment []string, directory string) (bool, error) {
	backupPath := filepath.Join(directory, "live-core-backup.sqlite")
	backup := exec.Command(binary, "database", "backup", backupPath)
	backup.Dir = workingDirectory
	backup.Env = environment
	if _, err := backup.CombinedOutput(); err != nil {
		return false, errors.New("Core online SQLite backup failed")
	}
	restore := exec.Command(binary, "--output", "json", "database", "restore", backupPath)
	restore.Dir = workingDirectory
	restore.Env = environment
	output, err := restore.CombinedOutput()
	if err == nil {
		return false, errors.New("Core allowed SQLite restore while serving")
	}
	var response struct {
		OK      bool `json:"ok"`
		Problem struct {
			Code string `json:"code"`
		} `json:"problem"`
	}
	if json.Unmarshal(output, &response) != nil || response.OK || response.Problem.Code != "database_busy" {
		return false, errors.New("Core did not report the expected SQLite state conflict")
	}
	return true, nil
}

func repositoryRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func formsDatabaseSelection() (string, string, error) {
	driver := os.Getenv("LIAPOLDUS_V1_E2E_FORMS_DRIVER")
	dsn := os.Getenv("LIAPOLDUS_V1_E2E_FORMS_DSN")
	if driver == "" && dsn == "" {
		return "memory", "", nil
	}
	if dsn == "" || (driver != "sqlite" && driver != "mysql" && driver != "postgres") {
		return "", "", errors.New("invalid external forms-db test selection")
	}
	return driver, dsn, nil
}

type databaseProxy struct {
	listener    net.Listener
	target      string
	mu          sync.Mutex
	paused      bool
	closed      bool
	connections map[net.Conn]net.Conn
	acceptDone  chan struct{}
}

func newDatabaseProxy(driver, dsn string) (*databaseProxy, string, error) {
	var target, proxiedDSN string
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", errors.New("start test SQL availability proxy")
	}
	proxyAddress := listener.Addr().String()
	switch driver {
	case "postgres":
		parsed, parseErr := url.Parse(dsn)
		if parseErr != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Hostname() == "" {
			_ = listener.Close()
			return nil, "", errors.New("invalid PostgreSQL test endpoint")
		}
		target = parsed.Host
		if _, _, splitErr := net.SplitHostPort(target); splitErr != nil {
			target = net.JoinHostPort(parsed.Hostname(), "5432")
		}
		parsed.Host = proxyAddress
		proxiedDSN = parsed.String()
	case "mysql":
		const marker = "@tcp("
		markerIndex := strings.Index(dsn, marker)
		if markerIndex < 0 {
			_ = listener.Close()
			return nil, "", errors.New("invalid MySQL test endpoint")
		}
		addressStart := markerIndex + len(marker)
		addressEnd := strings.IndexByte(dsn[addressStart:], ')')
		if addressEnd < 1 {
			_ = listener.Close()
			return nil, "", errors.New("invalid MySQL test endpoint")
		}
		addressEnd += addressStart
		target = dsn[addressStart:addressEnd]
		if _, _, splitErr := net.SplitHostPort(target); splitErr != nil {
			_ = listener.Close()
			return nil, "", errors.New("invalid MySQL test endpoint")
		}
		proxiedDSN = dsn[:addressStart] + proxyAddress + dsn[addressEnd:]
	default:
		_ = listener.Close()
		return nil, "", errors.New("unsupported SQL test driver")
	}
	proxy := &databaseProxy{
		listener: listener, target: target, connections: make(map[net.Conn]net.Conn),
		acceptDone: make(chan struct{}),
	}
	go proxy.accept()
	return proxy, proxiedDSN, nil
}

func (proxy *databaseProxy) accept() {
	defer close(proxy.acceptDone)
	for {
		client, err := proxy.listener.Accept()
		if err != nil {
			return
		}
		proxy.mu.Lock()
		blocked := proxy.paused || proxy.closed
		proxy.mu.Unlock()
		if blocked {
			_ = client.Close()
			continue
		}
		upstream, err := net.DialTimeout("tcp", proxy.target, 3*time.Second)
		if err != nil {
			_ = client.Close()
			continue
		}
		proxy.mu.Lock()
		if proxy.paused || proxy.closed {
			proxy.mu.Unlock()
			_ = client.Close()
			_ = upstream.Close()
			continue
		}
		proxy.connections[client] = upstream
		proxy.mu.Unlock()
		go proxy.forward(client, upstream)
	}
}

func (proxy *databaseProxy) forward(client, upstream net.Conn) {
	finished := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, client); finished <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); finished <- struct{}{} }()
	<-finished
	_ = client.Close()
	_ = upstream.Close()
	proxy.mu.Lock()
	delete(proxy.connections, client)
	proxy.mu.Unlock()
}

func (proxy *databaseProxy) Pause() {
	proxy.mu.Lock()
	proxy.paused = true
	for client, upstream := range proxy.connections {
		_ = client.Close()
		_ = upstream.Close()
		delete(proxy.connections, client)
	}
	proxy.mu.Unlock()
}

func (proxy *databaseProxy) Resume() {
	proxy.mu.Lock()
	proxy.paused = false
	proxy.mu.Unlock()
}

func (proxy *databaseProxy) Close() {
	proxy.mu.Lock()
	proxy.closed = true
	for client, upstream := range proxy.connections {
		_ = client.Close()
		_ = upstream.Close()
		delete(proxy.connections, client)
	}
	proxy.mu.Unlock()
	_ = proxy.listener.Close()
	<-proxy.acceptDone
}

func buildFormsSettings(directory, driver, dsn string) ([]byte, error) {
	cursorKey := make([]byte, 32)
	if _, err := rand.Read(cursorKey); err != nil {
		return nil, err
	}
	cursorPath := filepath.Join(directory, "forms-cursor-key")
	if err := os.WriteFile(cursorPath, cursorKey, 0o600); err != nil {
		clear(cursorKey)
		return nil, err
	}
	clear(cursorKey)
	settings := map[string]any{
		"driver":          driver,
		"cursorSecretRef": "file:" + cursorPath,
		"tablePrefix":     "forms_e2e_" + randomHex(6),
		"schemas": map[string]any{
			"contact": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"email": map[string]any{"type": "string"}},
				"required":             []string{"email"},
				"additionalProperties": false,
			},
		},
	}
	if driver != "memory" {
		dsnBytes := []byte(dsn)
		dsnPath := filepath.Join(directory, "forms-storage-dsn")
		writeErr := os.WriteFile(dsnPath, dsnBytes, 0o600)
		clear(dsnBytes)
		if writeErr != nil {
			return nil, writeErr
		}
		settings["dsn"] = "file:" + dsnPath
	}
	return json.Marshal(settings)
}

func buildRejectedFormsSettings(directory, driver string, active []byte) ([]byte, error) {
	var settings map[string]any
	if err := json.Unmarshal(active, &settings); err != nil {
		return nil, err
	}
	var dsn string
	switch driver {
	case "postgres":
		dsn = "postgres://candidate:candidate@127.0.0.1:1/candidate?sslmode=disable"
	case "mysql":
		dsn = "candidate:candidate@tcp(127.0.0.1:1)/candidate"
	default:
		return nil, errors.New("candidate rejection fixture requires an external SQL driver")
	}
	dsnPath := filepath.Join(directory, "forms-rejected-candidate-dsn")
	if err := os.WriteFile(dsnPath, []byte(dsn), 0o600); err != nil {
		return nil, err
	}
	settings["dsn"] = "file:" + dsnPath
	settings["tablePrefix"] = "forms_candidate_" + randomHex(6)
	return json.Marshal(settings)
}

func activePluginSettings(databasePath, instanceID string) ([]byte, error) {
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	var settings []byte
	if err := database.QueryRow(`SELECT raw_json FROM plugin_config_generations WHERE instance_id = ? AND slot = 'active'`, instanceID).Scan(&settings); err != nil {
		return nil, err
	}
	return settings, nil
}

func randomHex(size int) string {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value)
}

func withoutVariables(environment []string, names ...string) []string {
	blocked := make(map[string]struct{}, len(names))
	for _, name := range names {
		blocked[name] = struct{}{}
	}
	result := make([]string, 0, len(environment))
	for _, item := range environment {
		name, _, _ := strings.Cut(item, "=")
		if _, remove := blocked[name]; !remove {
			result = append(result, item)
		}
	}
	return result
}

func environmentHasVariable(environment []string, name string) bool {
	prefix := name + "="
	for _, item := range environment {
		if strings.HasPrefix(item, prefix) {
			return true
		}
	}
	return false
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

func settings(publicAddress, siteAddress, origin string) []byte {
	value, _ := json.Marshal(map[string]any{"schemaVersion": 1, "config": map[string]any{
		"listeners": []any{
			map[string]any{"id": "web", "kind": "http", "address": publicAddress,
				"hostnames": []string{}, "protocols": []string{"http1"}, "tls": map[string]any{"mode": "disabled"}},
			map[string]any{"id": "sites", "kind": "http", "address": siteAddress,
				"hostnames": []string{}, "protocols": []string{"http1"}, "tls": map[string]any{"mode": "disabled"}},
		},
		"routes": []any{
			map[string]any{"id": "forms-submit", "listenerId": "web",
				"match":   map[string]any{"path": map[string]any{"type": "exact", "value": "/forms/submit"}},
				"handler": map[string]any{"type": "plugin", "instanceId": "forms-child", "capability": "forms.submit", "mode": "call", "requestCookieNames": []string{"session"}}},
			map[string]any{"id": "forms-list", "listenerId": "web",
				"match":   map[string]any{"path": map[string]any{"type": "exact", "value": "/forms/list"}},
				"handler": map[string]any{"type": "plugin", "instanceId": "forms-child", "capability": "forms.list", "mode": "call"}},
			map[string]any{"id": "proxy", "listenerId": "web", "handler": map[string]any{
				"type": "reverseProxy", "upstreams": []any{map[string]any{"origin": "http://" + origin, "weight": 100}}}},
			map[string]any{"id": "site", "listenerId": "sites", "handler": map[string]any{"type": "static", "siteId": "manual-site"}},
		},
	}})
	return value
}

func publishSite(client *http.Client, base, token, siteAddress string) (bool, bool, error) {
	surfaceRequest, err := http.NewRequest(http.MethodGet, base+"/api/plugins/admin-surfaces", nil)
	if err != nil {
		return false, false, err
	}
	surfaceRequest.Header.Set("Authorization", "Bearer "+token)
	surfaceResponse, err := client.Do(surfaceRequest)
	if err != nil {
		return false, false, err
	}
	var surface struct {
		Items []struct {
			InstanceID string `json:"instanceId"`
			SHA256     string `json:"sha256"`
		} `json:"items"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(surfaceResponse.Body, 1<<20)).Decode(&surface)
	_ = surfaceResponse.Body.Close()
	if decodeErr != nil || surfaceResponse.StatusCode != http.StatusOK {
		return false, false, errors.New("Core did not publish plugin Admin Surface digests")
	}
	surfaceDigest := ""
	for _, item := range surface.Items {
		if item.InstanceID == "server-child" {
			surfaceDigest = item.SHA256
			break
		}
	}
	if surfaceDigest == "" {
		return false, false, errors.New("Server Admin Surface digest is missing")
	}
	archive, err := siteArchive()
	if err != nil {
		return false, false, err
	}
	digest := sha256.Sum256(archive)
	metadata, err := json.Marshal(map[string]any{
		"version":  1,
		"artifact": map[string]any{"mediaType": "application/gzip", "byteLength": len(archive), "sha256": "sha256:" + hex.EncodeToString(digest[:])},
		"payload":  map[string]any{"siteId": "manual-site"},
	})
	if err != nil {
		return false, false, err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	metadataPart, err := writer.CreatePart(mimeHeader("metadata", "application/json"))
	if err != nil {
		return false, false, err
	}
	if _, err := metadataPart.Write(metadata); err != nil {
		return false, false, err
	}
	artifactPart, err := writer.CreatePart(mimeHeader("artifact", "application/gzip"))
	if err != nil {
		return false, false, err
	}
	if _, err := artifactPart.Write(archive); err != nil {
		return false, false, err
	}
	if err := writer.Close(); err != nil {
		return false, false, err
	}
	endpoint := base + "/api/plugins/server-child/admin/pages/sites/actions/publish"
	request, err := http.NewRequest(http.MethodPost, endpoint, &body)
	if err != nil {
		return false, false, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Idempotency-Key", "manual-site-publish-0001")
	request.Header.Set("X-Liapoldus-Admin-Surface-Digest", surfaceDigest)
	response, err := client.Do(request)
	if err != nil {
		return false, false, err
	}
	receiptBytes, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
	_ = response.Body.Close()
	if readErr != nil {
		return false, false, readErr
	}
	if response.StatusCode != http.StatusAccepted {
		return false, false, fmt.Errorf("Core artifact action returned HTTP %d: %s", response.StatusCode, receiptBytes)
	}
	var receipt struct {
		OperationID string `json:"operationId"`
		State       string `json:"state"`
	}
	if err := json.Unmarshal(receiptBytes, &receipt); err != nil || receipt.OperationID == "" || receipt.State != "accepted" {
		return false, false, fmt.Errorf("invalid Server artifact receipt: %s", receiptBytes)
	}
	statusEndpoint := base + "/api/plugins/server-child/admin/pages/sites/actions/status"
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		statusBody, _ := json.Marshal(map[string]string{"operationId": receipt.OperationID})
		statusRequest, err := http.NewRequest(http.MethodPost, statusEndpoint, bytes.NewReader(statusBody))
		if err != nil {
			return false, false, err
		}
		statusRequest.Header.Set("Authorization", "Bearer "+token)
		statusRequest.Header.Set("Content-Type", "application/json")
		statusRequest.Header.Set("If-Match", `"`+receipt.OperationID+`"`)
		statusRequest.Header.Set("X-Liapoldus-Admin-Surface-Digest", surfaceDigest)
		statusRequest.Header.Set("Idempotency-Key", "manual-site-status-0001")
		statusResponse, err := client.Do(statusRequest)
		if err != nil {
			return false, false, err
		}
		statusBytes, readErr := io.ReadAll(io.LimitReader(statusResponse.Body, 4096))
		_ = statusResponse.Body.Close()
		if readErr != nil {
			return false, false, readErr
		}
		if statusResponse.StatusCode != http.StatusOK {
			return true, false, fmt.Errorf("Core artifact status action returned HTTP %d: %s", statusResponse.StatusCode, statusBytes)
		}
		var status struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(statusBytes, &status); err != nil {
			return true, false, err
		}
		if status.State == "completed" {
			return true, true, nil
		}
		if status.State == "failed" {
			return true, false, fmt.Errorf("Server site publish operation failed: %s", statusBytes)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return true, false, fmt.Errorf("Server site publish operation did not complete")
}

func mimeHeader(name, mediaType string) textproto.MIMEHeader {
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="`+name+`"`)
	header.Set("Content-Type", mediaType)
	return header
}

func siteArchive() ([]byte, error) {
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	entries := []struct {
		name string
		body []byte
	}{
		{"site-manifest.json", []byte(`{"schemaVersion":1,"siteId":"manual-site","documentRoot":"public","indexDocument":"index.html"}`)},
		{"public/index.html", []byte("published-site")},
	}
	for _, entry := range entries {
		if err := archive.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o600, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}); err != nil {
			return nil, err
		}
		if _, err := archive.Write(entry.body); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	if err := compressed.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func replaceEnvironment(environment []string, name, value string) []string {
	result := make([]string, 0, len(environment)+1)
	prefix := name + "="
	for _, item := range environment {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, prefix+value)
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

func registerTestReplica(controlAddress, directory, instanceID, replicaID, incarnationID, restAddress, credentialName string) (func(), error) {
	contract, err := pluginsdk.LoadReplicaLifecycleContract()
	if err != nil {
		return nil, errors.New("load Plugin SDK registration contract")
	}
	identity := sdkmodels.PeerReplicaID{InstanceID: instanceID, ReplicaID: replicaID, IncarnationID: incarnationID, PlacementID: "local-test"}
	uri, err := contract.ReplicaIdentityURI(identity)
	if err != nil {
		return nil, errors.New("build replica certificate identity")
	}
	certificatePath, keyPath := filepath.Join(directory, credentialName+".pem"), filepath.Join(directory, credentialName+".key")
	credentials, err := tls.LoadX509KeyPair(certificatePath, keyPath)
	if err != nil || len(credentials.Certificate) == 0 {
		return nil, errors.New("load replica credentials")
	}
	certificate, err := x509.ParseCertificate(credentials.Certificate[0])
	if err != nil {
		return nil, errors.New("parse replica certificate")
	}
	bound := false
	for _, identifier := range certificate.URIs {
		if identifier.String() == uri {
			bound = true
			break
		}
	}
	if !bound {
		return nil, errors.New("replica certificate is not bound to its registration identity")
	}
	roots := x509.NewCertPool()
	rootPEM, err := os.ReadFile(filepath.Join(directory, "root.pem"))
	if err != nil || !roots.AppendCertsFromPEM(rootPEM) {
		return nil, errors.New("load Core trust root")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "localhost", Certificates: []tls.Certificate{credentials}}}
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	request := sdkmodels.ReplicaRegistrationRequest{
		ContractVersion: contract.ContractVersion, Identity: identity, RestEndpoint: "https://" + restAddress,
		PeerEndpoints:       []sdkmodels.ReplicaPeerEndpoint{},
		Release:             sdkmodels.ReplicaRelease{Version: "1.0.0", SHA256: strings.Repeat("0", 64)},
		AdvertisedContracts: []sdkmodels.ContractVersion{}, AcceptedContracts: []sdkmodels.ContractRange{},
	}
	register := func() error {
		body, marshalErr := json.Marshal(request)
		if marshalErr != nil {
			return marshalErr
		}
		endpoint := contract.Endpoints["register"]
		outgoing, requestErr := http.NewRequest(endpoint.Method, "https://"+controlAddress+endpoint.Path, bytes.NewReader(body))
		if requestErr != nil {
			return requestErr
		}
		outgoing.Header.Set("Content-Type", contract.Requests["register"].MediaType)
		response, requestErr := client.Do(outgoing)
		if requestErr != nil {
			return requestErr
		}
		defer response.Body.Close()
		if response.StatusCode != contract.Responses["register"].Status {
			payload, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
			return fmt.Errorf("Core registration returned %d: %s", response.StatusCode, payload)
		}
		return nil
	}
	if err := register(); err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(time.Duration(contract.Lease.RenewIntervalSeconds) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = register()
			}
		}
	}()
	return func() {
		once.Do(func() {
			close(stop)
			transport.CloseIdleConnections()
		})
	}, nil
}

func waitForPluginInstances(databasePath string, instanceIDs ...string) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		database, err := sql.Open("sqlite", databasePath)
		if err == nil {
			ready := true
			for _, instanceID := range instanceIDs {
				var found int
				if database.QueryRow(`SELECT COUNT(*) FROM plugin_registered_instances WHERE instance_id = ?`, instanceID).Scan(&found) != nil || found != 1 {
					ready = false
					break
				}
			}
			_ = database.Close()
			if ready {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("one or more plugin replicas did not complete authenticated registration")
}

func waitForConvergence(client *http.Client, base, token string) (bool, error) {
	deadline := time.Now().Add(10 * time.Second)
	lastState := "unavailable"
	for time.Now().Before(deadline) {
		request, err := http.NewRequest(http.MethodGet, base+"/api/status", nil)
		if err != nil {
			return false, err
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		if err == nil {
			var status struct {
				Drift     bool `json:"drift"`
				Readiness struct {
					State  string `json:"state"`
					Reason string `json:"reason"`
				} `json:"dataPlaneReadiness"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&status)
			_ = response.Body.Close()
			if decodeErr == nil {
				lastState = fmt.Sprintf("status=%t readiness=%s reason=%q", status.Drift, status.Readiness.State, status.Readiness.Reason)
			}
			if decodeErr == nil && response.StatusCode == http.StatusOK && !status.Drift && status.Readiness.State == "ready" {
				return true, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false, fmt.Errorf("Core did not converge declared plugin replicas after restart: %s", lastState)
}

func waitForDegraded(client *http.Client, base, token string) error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		request, err := http.NewRequest(http.MethodGet, base+"/api/status", nil)
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		if err == nil {
			var status struct {
				Drift     bool `json:"drift"`
				Readiness struct {
					State string `json:"state"`
				} `json:"dataPlaneReadiness"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&status)
			_ = response.Body.Close()
			if decodeErr == nil && response.StatusCode == http.StatusOK && status.Drift && status.Readiness.State != "ready" {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("Core did not mark the stopped plugin replica degraded")
}

func waitServerHealth(address string, roots *x509.CertPool, credential certificate) error {
	pair, err := tls.X509KeyPair(credential.pem, credential.key)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: roots, Certificates: []tls.Certificate{pair}, ServerName: "localhost", MinVersion: tls.VersionTLS12,
	}}, Timeout: time.Second}
	deadline := time.Now().Add(15 * time.Second)
	var lastError string
	for time.Now().Before(deadline) {
		response, err := client.Get("https://" + address + "/_liapoldus/v1/health")
		if err != nil {
			lastError = err.Error()
		} else {
			lastError = fmt.Sprintf("HTTP %d", response.StatusCode)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("Plugin SDK REST endpoint %s did not become healthy: %s", address, lastError)
}

func pullGeneration(address string, roots *x509.CertPool, credential certificate, generation int64) (int, error) {
	pair, err := tls.X509KeyPair(credential.pem, credential.key)
	if err != nil {
		return 0, err
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: roots, Certificates: []tls.Certificate{pair}, ServerName: "localhost", MinVersion: tls.VersionTLS12,
	}}, Timeout: 5 * time.Second}
	response, err := client.Get("https://" + address + "/internal/v1/plugin-config/" + strconv.FormatInt(generation, 10))
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 262_144))
	return response.StatusCode, nil
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
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return "", fmt.Errorf("Core settings PUT for instance %q returned HTTP %d: %s", instanceID, response.StatusCode, body)
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

func waitFailedOperation(client *http.Client, base, token, id string) error {
	deadline := time.Now().Add(35 * time.Second)
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
			State string `json:"state"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&body)
		_ = response.Body.Close()
		if decodeErr != nil {
			return decodeErr
		}
		switch body.State {
		case "failed":
			return nil
		case "succeeded":
			return errors.New("candidate configuration unexpectedly applied")
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("candidate configuration operation did not reach a terminal state")
}

func submitFormWithEmail(address, email string) (bool, error) {
	return submitFormWithCookies(address, email, "", "")
}

func submitFormWithCookies(address, email, allowedCookie, unlistedCookie string) (bool, error) {
	payload, err := json.Marshal(map[string]any{
		"site": "manual-site", "schemaName": "contact",
		"data": map[string]string{"email": email},
	})
	if err != nil {
		return false, err
	}
	request, err := http.NewRequest(http.MethodPost, "http://"+address+"/forms/submit", bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	request.Header.Set("Content-Type", "application/json")
	if allowedCookie != "" || unlistedCookie != "" {
		request.Header.Set("Cookie", "session="+allowedCookie+"; private="+unlistedCookie)
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	var result struct {
		ID   string         `json:"id"`
		Data map[string]any `json:"data"`
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return false, fmt.Errorf("Server-to-forms submit returned HTTP %d: %s", response.StatusCode, body)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result); err != nil {
		return false, err
	}
	return result.ID != "" && result.Data["email"] == email, nil
}

func concurrentFormsRoundTrip(address string, count int) (bool, error) {
	expected := make(map[string]struct{}, count)
	results := make(chan error, count)
	var wait sync.WaitGroup
	for index := range count {
		email := fmt.Sprintf("concurrent-%02d@example.test", index)
		expected[email] = struct{}{}
		wait.Add(1)
		go func() {
			defer wait.Done()
			ok, err := submitFormWithEmail(address, email)
			if err != nil {
				results <- err
				return
			}
			if !ok {
				results <- errors.New("concurrent form submission was not acknowledged")
			}
		}()
	}
	wait.Wait()
	close(results)
	for err := range results {
		return false, err
	}

	seen := make(map[string]struct{}, count)
	cursor := ""
	for page := range 8 {
		status, body, err := listSubmittedFormPage(address, cursor, 10)
		if err != nil {
			return false, err
		}
		if status != http.StatusOK {
			return false, fmt.Errorf("concurrent forms list returned HTTP %d", status)
		}
		var result struct {
			Items []struct {
				Data map[string]any `json:"data"`
			} `json:"items"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return false, err
		}
		for _, item := range result.Items {
			if email, ok := item.Data["email"].(string); ok {
				if _, wanted := expected[email]; wanted {
					seen[email] = struct{}{}
				}
			}
		}
		if result.NextCursor == nil {
			break
		}
		if page == 7 {
			return false, errors.New("concurrent forms cursor did not terminate")
		}
		cursor = *result.NextCursor
	}
	return len(seen) == len(expected), nil
}

func listSubmittedForm(address string) (bool, error) {
	cursor := ""
	for page := range 8 {
		status, body, err := listSubmittedFormPage(address, cursor, 10)
		if err != nil {
			return false, err
		}
		if status != http.StatusOK {
			return false, fmt.Errorf("Server-to-forms list returned HTTP %d", status)
		}
		var result struct {
			Items []struct {
				Data map[string]any `json:"data"`
			} `json:"items"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return false, err
		}
		for _, item := range result.Items {
			if item.Data["email"] == "core-e2e@example.test" {
				return true, nil
			}
		}
		if result.NextCursor == nil {
			return false, nil
		}
		if page == 7 {
			return false, errors.New("forms cursor did not terminate")
		}
		cursor = *result.NextCursor
	}
	return false, nil
}

func listSubmittedFormResponse(address string) (int, []byte, error) {
	return listSubmittedFormPage(address, "", 10)
}

func listSubmittedFormPage(address, cursor string, limit int) (int, []byte, error) {
	input := map[string]any{"site": "manual-site", "schemaName": "contact", "limit": limit}
	if cursor != "" {
		input["cursor"] = cursor
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return 0, nil, err
	}
	request, err := http.NewRequest(http.MethodPost, "http://"+address+"/forms/list", bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, nil, err
	}
	return response.StatusCode, body, nil
}

func waitForFormsList(address string) (bool, error) {
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		found, err := listSubmittedForm(address)
		if err == nil {
			return found, nil
		}
		if err != nil && !strings.Contains(err.Error(), "HTTP 503") {
			return false, err
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		return false, lastErr
	}
	return false, errors.New("forms-db did not resume the list route after restart")
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

func issueCertificates() (certificateSet, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return certificateSet{}, err
	}
	rootTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test root"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &key.PublicKey, key)
	if err != nil {
		return certificateSet{}, err
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return certificateSet{}, err
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
		if name == "server-child" {
			peerIdentity, _ := url.Parse("spiffe://liapoldus.test/server")
			template.URIs = []*url.URL{peerIdentity}
		}
		if name == "forms-child" {
			peerIdentity, _ := url.Parse("spiffe://liapoldus.test/forms")
			template.URIs = []*url.URL{peerIdentity}
		}
		if name == "server-registration" {
			registrationIdentity, _ := url.Parse("spiffe://liapoldus/plugin/server-child/server-replica/manual-server-incarnation")
			template.URIs = []*url.URL{registrationIdentity}
		}
		if name == "forms-registration" || name == "forms-registration-revoked" {
			registrationIdentity, _ := url.Parse("spiffe://liapoldus/plugin/forms-child/forms-replica/manual-forms-incarnation")
			template.URIs = []*url.URL{registrationIdentity}
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
		return certificateSet{}, err
	}
	server, err := issue(3, "server-child")
	if err != nil {
		return certificateSet{}, err
	}
	forms, err := issue(4, "forms-child")
	if err != nil {
		return certificateSet{}, err
	}
	serverRegistration, err := issue(6, "server-registration")
	if err != nil {
		return certificateSet{}, err
	}
	formsRegistration, err := issue(7, "forms-registration")
	if err != nil {
		return certificateSet{}, err
	}
	revoked, err := issue(5, "forms-registration-revoked")
	if err != nil {
		return certificateSet{}, err
	}
	crlDER, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{Number: big.NewInt(1),
		ThisUpdate: time.Now().Add(-time.Minute), NextUpdate: time.Now().Add(time.Hour)}, root, key)
	if err != nil {
		return certificateSet{}, err
	}
	revokedCRLDER, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{Number: big.NewInt(2),
		ThisUpdate: time.Now().Add(-time.Minute), NextUpdate: time.Now().Add(time.Hour),
		RevokedCertificateEntries: []x509.RevocationListEntry{{SerialNumber: big.NewInt(5), RevocationTime: time.Now().Add(-time.Minute)}}}, root, key)
	if err != nil {
		return certificateSet{}, err
	}
	formsRevokedCRLDER, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{Number: big.NewInt(2),
		ThisUpdate: time.Now().Add(-time.Minute), NextUpdate: time.Now().Add(time.Hour),
		RevokedCertificateEntries: []x509.RevocationListEntry{{SerialNumber: big.NewInt(4), RevocationTime: time.Now().Add(-time.Minute)}}}, root, key)
	if err != nil {
		return certificateSet{}, err
	}
	return certificateSet{
		root: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}),
		core: core, server: server, forms: forms, serverRegistration: serverRegistration,
		formsRegistration: formsRegistration, revoked: revoked,
		crl:             pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: crlDER}),
		revokedCRL:      pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: revokedCRLDER}),
		formsRevokedCRL: pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: formsRevokedCRLDER}),
	}, nil
}
