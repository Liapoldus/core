package main

// serve-domain proves the real Core -> Plugin SDK -> Domain configuration and
// the mTLS pluginprotocol data path. It is run from the multi-repository test
// workspace with the Domain module path as its only argument.

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
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

	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/infrastructure/storage"
	sdkmodels "github.com/Liapoldus/plugin-sdk/v2/domain/models"
	sdkinfrastructure "github.com/Liapoldus/plugin-sdk/v2/infrastructure"
)

const (
	domainInstance  = "domain"
	domainReplica   = "domain-replica"
	runtimeInstance = "runtime-child"
	runtimeReplica  = "runtime-replica"
	writerURI       = "spiffe://liapoldus.test/plugins/writer/replica/writer/incarnation/1"
	readerURI       = "spiffe://liapoldus.test/plugins/reader/replica/reader/incarnation/1"
)

var peerClientBinary string

type certificate struct{ pem, key []byte }

type process struct {
	command *exec.Cmd
	done    chan struct{}
	logPath string
	err     error
}

type envelope struct {
	OK   bool `json:"ok"`
	Data struct {
		Operations []struct {
			ID string `json:"id"`
		} `json:"operations"`
		Found  bool `json:"found"`
		Object struct {
			Title string `json:"title"`
		} `json:"object"`
		Rows [][]any `json:"rows"`
	} `json:"data"`
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
}

type runtimeInvokeResponse struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

type runtimeInvokeBody struct {
	Command string `json:"command"`
	Code    string `json:"code"`
	Output  struct {
		OK      bool     `json:"ok"`
		Payload envelope `json:"payload"`
	} `json:"output"`
}

type runtimeCallResult struct {
	Status  int
	Body    runtimeInvokeBody
	RawBody string
}

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 3 || !filepath.IsAbs(os.Args[1]) || !filepath.IsAbs(os.Args[2]) {
		return errors.New("serve-domain requires absolute Domain and Runtime repository paths")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	workDirectory, err := os.MkdirTemp("", "liapoldus-core-domain-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workDirectory)

	lifecycle, err := sdkinfrastructure.LoadReplicaLifecycleContract()
	if err != nil {
		return err
	}
	runtimeIdentity := sdkmodels.PeerReplicaID{
		InstanceID: runtimeInstance, ReplicaID: runtimeReplica, IncarnationID: "inc-runtime", PlacementID: "runtime-test",
	}
	runtimeURI, err := lifecycle.ReplicaIdentityURI(runtimeIdentity)
	if err != nil {
		return err
	}
	rootPEM, root, rootKey, coreCert, err := issueRoot()
	if err != nil {
		return err
	}
	domainIdentity := sdkmodels.PeerReplicaID{
		InstanceID: domainInstance, ReplicaID: domainReplica, IncarnationID: "inc-1", PlacementID: "local-test",
	}
	domainURI, err := lifecycle.ReplicaIdentityURI(domainIdentity)
	if err != nil {
		return err
	}
	domainCert, err := issueLeaf(root, rootKey, domainInstance, []string{domainURI}, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth})
	if err != nil {
		return err
	}
	writerCert, err := issueLeaf(root, rootKey, "writer", []string{writerURI}, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	if err != nil {
		return err
	}
	readerCert, err := issueLeaf(root, rootKey, "reader", []string{readerURI}, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	if err != nil {
		return err
	}
	runtimeCert, err := issueLeaf(root, rootKey, "runtime", []string{runtimeURI}, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth})
	if err != nil {
		return err
	}
	crlPEM, err := issueCRL(root, rootKey)
	if err != nil {
		return err
	}
	files := map[string][]byte{
		"root.pem": rootPEM, "core.pem": coreCert.pem, "core.key": coreCert.key,
		"domain.pem": domainCert.pem, "domain.key": domainCert.key,
		"writer.pem": writerCert.pem, "writer.key": writerCert.key,
		"reader.pem": readerCert.pem, "reader.key": readerCert.key,
		"runtime.pem": runtimeCert.pem, "runtime.key": runtimeCert.key,
		"revoked.pem": crlPEM,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(workDirectory, name), contents, 0o600); err != nil {
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
	domainRestAddress, err := freeAddress()
	if err != nil {
		return err
	}
	domainPeerAddress, err := freeAddress()
	if err != nil {
		return err
	}
	databasePath := filepath.Join(workDirectory, "core.sqlite")
	coreRoot := repositoryRoot()
	domainRoot := os.Args[1]
	runtimeRoot := os.Args[2]
	coreBinary := filepath.Join(workDirectory, "core")
	domainBinary := filepath.Join(workDirectory, "domain")
	runtimeBinary := filepath.Join(workDirectory, "runtime")
	runtimeArtifacts := filepath.Join(workDirectory, "runtime-artifacts")
	if err := os.Mkdir(runtimeArtifacts, 0o700); err != nil {
		return err
	}
	peerClientBinary = filepath.Join(workDirectory, "plugin-peer-client")
	if err := build(domainRoot, peerClientBinary, "./tests/fixtures/peer-client"); err != nil {
		return err
	}
	readModule, err := domainReadModule()
	if err != nil {
		return err
	}
	writeModule, err := domainWriteModule()
	if err != nil {
		return err
	}
	drainModule, err := domainWriteThenLoopModule()
	if err != nil {
		return err
	}
	readDigest, writeDigest, drainDigest := sdkmodels.Digest(readModule), sdkmodels.Digest(writeModule), sdkmodels.Digest(drainModule)
	for digest, module := range map[string][]byte{readDigest: readModule, writeDigest: writeModule, drainDigest: drainModule} {
		if err := os.WriteFile(filepath.Join(runtimeArtifacts, digest+".wasm"), module, 0o400); err != nil {
			return err
		}
	}
	if err := build(coreRoot, coreBinary, "./cmd/core"); err != nil {
		return err
	}
	if err := build(domainRoot, domainBinary, "./cmd/domain"); err != nil {
		return err
	}
	if err := build(runtimeRoot, runtimeBinary, "./cmd/runtime"); err != nil {
		return err
	}
	coreEnvironment := append(os.Environ(),
		"CORE_SQLITE_PATH="+databasePath,
		"CORE_INIT_MANAGEMENT_LISTEN="+managementAddress,
		"CORE_INIT_MANAGEMENT_CERTIFICATE="+filepath.Join(workDirectory, "core.pem"),
		"CORE_INIT_MANAGEMENT_KEY="+filepath.Join(workDirectory, "core.key"),
		"CORE_INIT_CONTROL_LISTEN="+controlAddress,
		"CORE_INIT_CONTROL_PUBLIC_URL=https://"+controlAddress,
		"CORE_INIT_CONTROL_CERTIFICATE="+filepath.Join(workDirectory, "core.pem"),
		"CORE_INIT_CONTROL_KEY="+filepath.Join(workDirectory, "core.key"),
		"CORE_INIT_REPLICA_CLIENT_CA="+filepath.Join(workDirectory, "root.pem"),
		"CORE_INIT_REPLICA_SERVER_CA="+filepath.Join(workDirectory, "root.pem"),
		"CORE_INIT_SECRET_ROOT="+workDirectory,
	)
	bootstrap := exec.CommandContext(ctx, "go", "run", "./tests/fixtures/runtime-bootstrap")
	bootstrap.Dir = coreRoot
	bootstrap.Env = coreEnvironment
	bootstrapOutput, err := bootstrap.Output()
	if err != nil || strings.TrimSpace(string(bootstrapOutput)) == "" {
		return fmt.Errorf("Core bootstrap failed: %v", err)
	}
	bootstrapToken := strings.TrimSpace(string(bootstrapOutput))
	if err := seedDomainConfiguration(ctx, databasePath, domainRoot); err != nil {
		return err
	}
	if err := seedRuntimeConfiguration(ctx, databasePath, runtimeRoot, readDigest, writeDigest, drainDigest); err != nil {
		return err
	}

	coreProcess, err := startProcess(coreRoot, coreBinary, []string{"serve"}, coreEnvironment, workDirectory, "core")
	if err != nil {
		return err
	}
	defer stopProcess(coreProcess)
	rootPool := x509.NewCertPool()
	if !rootPool.AppendCertsFromPEM(rootPEM) {
		return errors.New("invalid test trust root")
	}
	coreHealthClient, err := httpClient(rootPool, certificate{})
	if err != nil {
		return err
	}
	if err := waitForHealth(ctx, coreHealthClient, "https://"+managementAddress, coreProcess); err != nil {
		return err
	}
	management, err := config.LoadManagement()
	if err != nil {
		return err
	}
	managementBase := "https://" + managementAddress

	domainArguments := []string{
		"--instance-id", domainInstance, "--replica-id", domainReplica, "--placement-id", "local-test", "--incarnation-id", "inc-1",
		"--rest-listen", domainRestAddress, "--rest-advertise-url", "https://" + domainRestAddress,
		"--core-url", "https://" + controlAddress, "--core-server-name", "localhost", "--core-peer-cn", "core-child",
		"--tls-ca-file", filepath.Join(workDirectory, "root.pem"),
		"--tls-server-cert-file", filepath.Join(workDirectory, "domain.pem"), "--tls-server-key-file", filepath.Join(workDirectory, "domain.key"),
		"--peer-listen", domainPeerAddress, "--peer-advertise-endpoint", domainPeerAddress, "--peer-carrier", "tcp",
		"--peer-identity", domainURI, "--peer-ca-file", filepath.Join(workDirectory, "root.pem"),
		"--peer-cert", filepath.Join(workDirectory, "domain.pem"), "--peer-key", filepath.Join(workDirectory, "domain.key"),
		"--tls-crl-file", filepath.Join(workDirectory, "revoked.pem"),
	}
	domainProcess, err := startProcess(domainRoot, domainBinary, domainArguments, os.Environ(), workDirectory, "domain")
	if err != nil {
		return err
	}
	defer stopProcess(domainProcess)

	readinessClient, err := httpClient(rootPool, coreCert)
	if err != nil {
		return err
	}
	httpContract, err := sdkinfrastructure.LoadHTTPContract()
	if err != nil {
		return err
	}
	readyEndpoint, err := httpContract.Endpoint("ready")
	if err != nil {
		return err
	}
	readinessURL := "https://" + domainRestAddress + readyEndpoint.Path
	if err := waitForDomainGeneration(ctx, readinessClient, readinessURL, "1", domainProcess); err != nil {
		return err
	}

	runtimeRESTAddress, err := freeAddress()
	if err != nil {
		return err
	}
	runtimePeerAddress, err := freeAddress()
	if err != nil {
		return err
	}
	runtimeArguments := []string{
		"--listen", runtimeRESTAddress,
		"--core-url", "https://" + controlAddress, "--core-server-name", "localhost", "--core-peer-cn", "core-child",
		"--instance-id", runtimeInstance, "--replica-id", runtimeReplica, "--placement-id", "runtime-test", "--incarnation-id", "inc-runtime",
		"--tls-ca-file", filepath.Join(workDirectory, "root.pem"),
		"--tls-server-cert-file", filepath.Join(workDirectory, "runtime.pem"), "--tls-server-key-file", filepath.Join(workDirectory, "runtime.key"),
		"--tls-client-cert-file", filepath.Join(workDirectory, "runtime.pem"), "--tls-client-key-file", filepath.Join(workDirectory, "runtime.key"),
		"--tls-crl-file", filepath.Join(workDirectory, "revoked.pem"), "--artifact-dir", runtimeArtifacts,
		"--peer-listen", runtimePeerAddress, "--peer-advertise-endpoint", runtimePeerAddress, "--peer-carrier", "tcp",
		"--rest-advertise-url", "https://" + runtimeRESTAddress,
		"--peer-identity", runtimeURI, "--peer-allowed-caller", readerURI,
		"--peer-ca-file", filepath.Join(workDirectory, "root.pem"),
		"--peer-cert", filepath.Join(workDirectory, "runtime.pem"), "--peer-key", filepath.Join(workDirectory, "runtime.key"),
	}
	runtimeProcess, err := startProcess(runtimeRoot, runtimeBinary, runtimeArguments, os.Environ(), workDirectory, "runtime")
	if err != nil {
		return err
	}
	defer stopProcess(runtimeProcess)
	runtimeReadinessURL := "https://" + runtimeRESTAddress + readyEndpoint.Path
	if err := waitForDomainGeneration(ctx, readinessClient, runtimeReadinessURL, "1", runtimeProcess); err != nil {
		return err
	}
	if err := createRuntimeDomainLink(ctx, readinessClient, management, managementBase, bootstrapToken); err != nil {
		return err
	}
	if err := waitForRuntimeDirectory(ctx, controlAddress, workDirectory, runtimeIdentity); err != nil {
		return err
	}
	entityDeniedCall, err := callRuntime(ctx, domainRoot, runtimePeerAddress, runtimeURI, readerURI, "readEntry", []string{"Secret"}, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"))
	if err != nil || entityDeniedCall.Status != http.StatusForbidden || entityDeniedCall.Body.Code != "forbidden_entity" {
		return fmt.Errorf("Runtime command accepted an entity outside its allowlist: status=%d body=%s err=%v", entityDeniedCall.Status, entityDeniedCall.RawBody, err)
	}
	directRuntimeDenied, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, runtimeURI, filepath.Join(workDirectory, "runtime.pem"), filepath.Join(workDirectory, "runtime.key"), filepath.Join(workDirectory, "root.pem"), "domain.transaction",
		[]byte(`{"operations":[{"kind":"create","entity":"Entry","object":{"title":"direct-runtime-must-be-denied"}}]}`))
	if err != nil || directRuntimeDenied.OK || directRuntimeDenied.Error == nil || directRuntimeDenied.Error.Code != "forbidden" {
		return fmt.Errorf("Domain did not deny direct Runtime identity without a Core grant: response=%s err=%v", marshalEnvelope(directRuntimeDenied), err)
	}
	readRuntimeCall, err := callRuntime(ctx, domainRoot, runtimePeerAddress, runtimeURI, readerURI, "readEntry", []string{"Entry"}, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"))
	if err != nil || readRuntimeCall.Status != http.StatusOK || !readRuntimeCall.Body.Output.OK || !readRuntimeCall.Body.Output.Payload.OK || readRuntimeCall.Body.Output.Payload.Data.Found {
		return fmt.Errorf("Runtime read-only Domain call did not reach the real Domain: status=%d body=%s err=%v", readRuntimeCall.Status, readRuntimeCall.RawBody, err)
	}
	deniedRuntimeCall, err := callRuntime(ctx, domainRoot, runtimePeerAddress, runtimeURI, readerURI, "writeEntry", []string{"Entry"}, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"))
	if err != nil {
		return err
	}
	if deniedRuntimeCall.Status != http.StatusOK || !deniedRuntimeCall.Body.Output.OK || deniedRuntimeCall.Body.Output.Payload.OK || deniedRuntimeCall.Body.Output.Payload.Error == nil || deniedRuntimeCall.Body.Output.Payload.Error.Code != "forbidden" {
		return fmt.Errorf("Runtime identity without a Core Domain write grant was not denied: status=%d body=%s runtime=%s core=%s domain=%s", deniedRuntimeCall.Status, deniedRuntimeCall.RawBody,
			readFixtureLog(workDirectory, "runtime"), readFixtureLog(workDirectory, "core"), readFixtureLog(workDirectory, "domain"))
	}
	// Reject a Runtime generation whose command points outside its immutable
	// module catalog. The old catalog remains active but fenced until Core
	// announces a valid replacement generation.
	invalidRuntimeSettings := bytes.Replace(runtimeSettingsDocument(readDigest, writeDigest, drainDigest),
		[]byte(`"moduleId":"domainReader"`), []byte(`"moduleId":"missingModule"`), 1)
	runtimeOperationID, err := updateSettingsForInstance(ctx, readinessClient, management, managementBase, bootstrapToken,
		runtimeInstance, 1, "runtime-invalid-settings-1", invalidRuntimeSettings)
	if err != nil {
		return err
	}
	if err := waitForOperationFailure(ctx, readinessClient, management, managementBase, bootstrapToken, runtimeOperationID, management.Codes.ActivationFailed); err != nil {
		return err
	}
	if err := assertReplicaGenerationRefused(ctx, readinessClient, runtimeReadinessURL, "1", "2", runtimeProcess, 500*time.Millisecond); err != nil {
		return err
	}
	validRuntimeSettings := runtimeSettingsDocument(readDigest, writeDigest, drainDigest)
	runtimeOperationID, err = updateSettingsForInstance(ctx, readinessClient, management, managementBase, bootstrapToken,
		runtimeInstance, 2, "runtime-settings-recovery-2", validRuntimeSettings)
	if err != nil {
		return err
	}
	if err := waitForOperation(ctx, readinessClient, management, managementBase, bootstrapToken, runtimeOperationID); err != nil {
		return err
	}
	if err := waitForDomainGeneration(ctx, readinessClient, runtimeReadinessURL, "3", runtimeProcess); err != nil {
		return err
	}
	if err := waitForRuntimeDirectory(ctx, controlAddress, workDirectory, runtimeIdentity); err != nil {
		return err
	}
	recoveredRuntimeCall, err := callRuntime(ctx, domainRoot, runtimePeerAddress, runtimeURI, readerURI,
		"readEntry", []string{"Entry"}, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"))
	if err != nil || recoveredRuntimeCall.Status != http.StatusOK || !recoveredRuntimeCall.Body.Output.OK || !recoveredRuntimeCall.Body.Output.Payload.OK {
		return fmt.Errorf("Runtime did not restore its prior command routing after valid Core generation: status=%d body=%s err=%v", recoveredRuntimeCall.Status, recoveredRuntimeCall.RawBody, err)
	}

	writePayload := []byte(`{"writeId":"cross-process-create","operations":[{"kind":"create","entity":"Entry","object":{"title":"from-core-generation"}}]}`)
	proxyEndpoint, droppedResponse, err := dropNextPeerCallResponse(ctx, domainPeerAddress, domainURI, writerURI,
		domainCert, writerCert, rootPool, func(probeContext context.Context) error {
			return waitForDomainTitle(probeContext, domainRoot, domainPeerAddress, domainURI, readerURI,
				"from-core-generation", filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"))
		})
	if err != nil {
		return fmt.Errorf("cannot start response-loss proxy: %w", err)
	}
	if _, err := callPeer(ctx, domainRoot, proxyEndpoint, domainURI, writerURI,
		filepath.Join(workDirectory, "writer.pem"), filepath.Join(workDirectory, "writer.key"), filepath.Join(workDirectory, "root.pem"), "domain.transaction", writePayload); err == nil {
		return errors.New("response-loss proxy unexpectedly delivered the first committed transaction response")
	}
	select {
	case dropErr := <-droppedResponse:
		if dropErr != nil {
			return fmt.Errorf("response-loss proxy failed before dropping the committed response: %w", dropErr)
		}
	case <-ctx.Done():
		return fmt.Errorf("timed out waiting for the committed response to be dropped: %w", ctx.Err())
	}
	created, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, writerURI, filepath.Join(workDirectory, "writer.pem"), filepath.Join(workDirectory, "writer.key"), filepath.Join(workDirectory, "root.pem"), "domain.transaction", writePayload)
	if err != nil || !created.OK || len(created.Data.Operations) != 1 || created.Data.Operations[0].ID == "" {
		return fmt.Errorf("retry after the committed response was dropped failed: response=%s err=%v", marshalEnvelope(created), err)
	}
	objectID := created.Data.Operations[0].ID
	// Exercise the actual cross-process retry path: the request traverses a new
	// authenticated pluginprotocol session, but the process-local write ledger
	// must return the original generated UUID without applying a second create.
	createdRetry, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, writerURI,
		filepath.Join(workDirectory, "writer.pem"), filepath.Join(workDirectory, "writer.key"), filepath.Join(workDirectory, "root.pem"), "domain.transaction",
		[]byte(`{"writeId":"cross-process-create","operations":[{"kind":"create","entity":"Entry","object":{"title":"from-core-generation"}}]}`))
	if err != nil || !createdRetry.OK || len(createdRetry.Data.Operations) != 1 || createdRetry.Data.Operations[0].ID != objectID {
		return fmt.Errorf("cross-process retry did not return the original committed result: response=%s err=%v", marshalEnvelope(createdRetry), err)
	}
	conflictingRetry, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, writerURI,
		filepath.Join(workDirectory, "writer.pem"), filepath.Join(workDirectory, "writer.key"), filepath.Join(workDirectory, "root.pem"), "domain.transaction",
		[]byte(`{"writeId":"cross-process-create","operations":[{"kind":"create","entity":"Entry","object":{"title":"different-payload"}}]}`))
	if err != nil || conflictingRetry.OK || conflictingRetry.Error == nil || conflictingRetry.Error.Code != "conflict" {
		return fmt.Errorf("cross-process writeId reuse with a different payload was not rejected: response=%s err=%v", marshalEnvelope(conflictingRetry), err)
	}
	read, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, readerURI, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"), "domain.get",
		[]byte(fmt.Sprintf(`{"entity":"Entry","id":%q}`, objectID)))
	if err != nil || !read.OK || !read.Data.Found || read.Data.Object.Title != "from-core-generation" {
		return fmt.Errorf("authenticated global read failed: response=%s err=%v", marshalEnvelope(read), err)
	}
	denied, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, readerURI, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"), "domain.transaction",
		[]byte(`{"operations":[{"kind":"create","entity":"Entry","object":{"title":"must-be-denied"}}]}`))
	if err != nil || denied.OK || denied.Error == nil || denied.Error.Code != "forbidden" {
		return fmt.Errorf("Core write grant was not enforced: response=%s err=%v", marshalEnvelope(denied), err)
	}
	// This candidate is structurally valid to Core but semantically invalid to
	// Domain: its grant references an entity absent from the ER model. Rejection
	// must retain the active generation, readiness, objects and existing grants.
	invalidSettings := domainSettingsWithUnknownGrant(writerURI)
	operationID, err := updateSettings(ctx, readinessClient, management, managementBase, bootstrapToken, 1, "domain-invalid-settings-1", invalidSettings)
	if err != nil {
		return err
	}
	if err := waitForOperationFailure(ctx, readinessClient, management, managementBase, bootstrapToken, operationID, management.Codes.ActivationFailed); err != nil {
		return err
	}
	if err := assertReplicaGenerationRefused(ctx, readinessClient, readinessURL, "1", "2", domainProcess, 500*time.Millisecond); err != nil {
		return err
	}
	retainedRead, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, readerURI, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"), "domain.get",
		[]byte(fmt.Sprintf(`{"entity":"Entry","id":%q}`, objectID)))
	if err != nil || !retainedRead.OK || !retainedRead.Data.Found || retainedRead.Data.Object.Title != "from-core-generation" {
		return fmt.Errorf("invalid Core candidate changed active Domain data: response=%s err=%v", marshalEnvelope(retainedRead), err)
	}
	retainedGrant, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, writerURI, filepath.Join(workDirectory, "writer.pem"), filepath.Join(workDirectory, "writer.key"), filepath.Join(workDirectory, "root.pem"), "domain.transaction",
		[]byte(`{"writeId":"after-invalid-settings","operations":[{"kind":"create","entity":"Entry","object":{"title":"active-grant-retained"}}]}`))
	if err != nil || !retainedGrant.OK {
		return fmt.Errorf("invalid Core candidate changed active Domain grants: response=%s err=%v", marshalEnvelope(retainedGrant), err)
	}
	settingsGeneration2 := domainSettings(readerURI, runtimeURI)
	operationID, err = updateSettings(ctx, readinessClient, management, managementBase, bootstrapToken, 2, "domain-grants-update-2", settingsGeneration2)
	if err != nil {
		return err
	}
	if err := waitForOperation(ctx, readinessClient, management, managementBase, bootstrapToken, operationID); err != nil {
		return err
	}
	if err := waitForDomainGeneration(ctx, readinessClient, readinessURL, "3", domainProcess); err != nil {
		return err
	}
	if err := waitForDomainGeneration(ctx, readinessClient, runtimeReadinessURL, "3", runtimeProcess); err != nil {
		return err
	}
	// Plugin-local readiness is acknowledged before Core's renewable peer
	// directory lease necessarily publishes the new eligible state. Do not
	// start the side-effecting WASM call until Core's caller-scoped directory
	// confirms Domain is routable for this Runtime incarnation.
	if err := waitForRuntimeDirectory(ctx, controlAddress, workDirectory, runtimeIdentity); err != nil {
		return err
	}
	grantedRuntimeCall, err := callRuntime(ctx, domainRoot, runtimePeerAddress, runtimeURI, readerURI, "writeEntry", []string{"Entry"}, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"))
	if err != nil || grantedRuntimeCall.Status != http.StatusOK || !grantedRuntimeCall.Body.Output.OK || !grantedRuntimeCall.Body.Output.Payload.OK || len(grantedRuntimeCall.Body.Output.Payload.Data.Operations) != 1 {
		return fmt.Errorf("Runtime write grant from Core was not applied: status=%d body=%s err=%v runtime=%s core=%s domain=%s", grantedRuntimeCall.Status, grantedRuntimeCall.RawBody, err,
			readFixtureLog(workDirectory, "runtime"), readFixtureLog(workDirectory, "core"), readFixtureLog(workDirectory, "domain"))
	}
	runtimeObjectID := grantedRuntimeCall.Body.Output.Payload.Data.Operations[0].ID
	if runtimeObjectID == "" {
		return errors.New("Runtime Domain transaction returned no generated object ID")
	}
	runtimeObject, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, readerURI, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"), "domain.get",
		[]byte(fmt.Sprintf(`{"entity":"Entry","id":%q}`, runtimeObjectID)))
	if err != nil || !runtimeObject.OK || !runtimeObject.Data.Found || runtimeObject.Data.Object.Title != "from-runtime" {
		return fmt.Errorf("Runtime-created object was not visible to another authenticated plugin: response=%s err=%v", marshalEnvelope(runtimeObject), err)
	}
	restrictedMethodCall, err := callRuntime(ctx, domainRoot, runtimePeerAddress, runtimeURI, readerURI, "writeEntryMethodDenied", []string{"Entry"}, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"))
	if err != nil || restrictedMethodCall.Status != http.StatusOK || restrictedMethodCall.Body.Output.OK {
		return fmt.Errorf("Runtime command invoked a method outside its host allowlist: status=%d body=%s err=%v", restrictedMethodCall.Status, restrictedMethodCall.RawBody, err)
	}
	oldObject, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, readerURI, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"), "domain.get",
		[]byte(fmt.Sprintf(`{"entity":"Entry","id":%q}`, objectID)))
	if err != nil || !oldObject.OK || !oldObject.Data.Found || oldObject.Data.Object.Title != "from-core-generation" {
		return fmt.Errorf("grant-only Core update lost existing Domain data: response=%s err=%v", marshalEnvelope(oldObject), err)
	}
	readerWrite, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, readerURI, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"), "domain.transaction",
		[]byte(`{"writeId":"reader-after-grant-update","operations":[{"kind":"create","entity":"Entry","object":{"title":"reader-granted"}}]}`))
	if err != nil || !readerWrite.OK {
		return fmt.Errorf("new Core write grant was not applied: response=%s err=%v", marshalEnvelope(readerWrite), err)
	}
	writerWrite, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, writerURI, filepath.Join(workDirectory, "writer.pem"), filepath.Join(workDirectory, "writer.key"), filepath.Join(workDirectory, "root.pem"), "domain.transaction",
		[]byte(`{"operations":[{"kind":"create","entity":"Entry","object":{"title":"writer-revoked"}}]}`))
	if err != nil || writerWrite.OK || writerWrite.Error == nil || writerWrite.Error.Code != "forbidden" {
		return fmt.Errorf("removed Core write grant remained active: response=%s err=%v", marshalEnvelope(writerWrite), err)
	}
	settingsGeneration3 := domainSettingsWithCategory(readerURI)
	operationID, err = updateSettings(ctx, readinessClient, management, managementBase, bootstrapToken, 3, "domain-model-update-3", settingsGeneration3)
	if err != nil {
		return err
	}
	if err := waitForOperation(ctx, readinessClient, management, managementBase, bootstrapToken, operationID); err != nil {
		return err
	}
	if err := waitForDomainGeneration(ctx, readinessClient, readinessURL, "4", domainProcess); err != nil {
		return err
	}
	resetRead, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, readerURI, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"), "domain.get",
		[]byte(fmt.Sprintf(`{"entity":"Entry","id":%q}`, objectID)))
	if err != nil || !resetRead.OK || resetRead.Data.Found {
		return fmt.Errorf("new Core ER model generation did not clear volatile objects: response=%s err=%v", marshalEnvelope(resetRead), err)
	}
	oldShapeWrite, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, readerURI, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"), "domain.transaction",
		[]byte(`{"operations":[{"kind":"create","entity":"Entry","object":{"title":"old-shape"}}]}`))
	if err != nil || oldShapeWrite.OK || oldShapeWrite.Error == nil || oldShapeWrite.Error.Code != "invalid_request" {
		return fmt.Errorf("updated ER model did not validate new required field: response=%s err=%v", marshalEnvelope(oldShapeWrite), err)
	}
	newShapeWrite, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, readerURI, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"), "domain.transaction",
		[]byte(`{"writeId":"new-model-shape","operations":[{"kind":"create","entity":"Entry","object":{"title":"new-shape","category":"article"}}]}`))
	if err != nil || !newShapeWrite.OK || len(newShapeWrite.Data.Operations) != 1 {
		return fmt.Errorf("new Core ER model field was not activated: response=%s err=%v", marshalEnvelope(newShapeWrite), err)
	}
	newObjectID := newShapeWrite.Data.Operations[0].ID
	queryResult, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, readerURI, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"), "domain.query",
		[]byte(fmt.Sprintf(`{"sql":"SELECT title FROM Entry WHERE id = ?","params":[%q]}`, newObjectID)))
	if err != nil || !queryResult.OK || len(queryResult.Data.Rows) != 1 || len(queryResult.Data.Rows[0]) != 1 || queryResult.Data.Rows[0][0] != "new-shape" {
		return fmt.Errorf("Core-configured Domain query failed: response=%s err=%v", marshalEnvelope(queryResult), err)
	}
	// Each write stays below its transaction budget, while their combined query
	// result exceeds the SQL response ceiling. Exercise both limits over the
	// real authenticated process boundary rather than a mocked handler.
	largeTitle := strings.Repeat("x", 12<<10)
	largeObject, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, readerURI,
		filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"),
		"domain.transaction", []byte(fmt.Sprintf(`{"operations":[{"kind":"create","entity":"Entry","object":{"title":%q,"category":"article"}}]}`, largeTitle)))
	if err != nil || !largeObject.OK || len(largeObject.Data.Operations) != 1 {
		return fmt.Errorf("large query fixture object could not be created: response=%s err=%v", marshalEnvelope(largeObject), err)
	}
	largeObjectID := largeObject.Data.Operations[0].ID
	writeRows := func(count, offset int) error {
		operations := make([]map[string]any, 0, count)
		for index := 0; index < count; index++ {
			operations = append(operations, map[string]any{
				"kind": "create", "entity": "Entry",
				"object": map[string]string{"title": fmt.Sprintf("%s-%03d", largeTitle, offset+index), "category": "article"},
			})
		}
		payload, marshalErr := json.Marshal(map[string]any{"operations": operations})
		if marshalErr != nil {
			return marshalErr
		}
		response, callErr := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, readerURI,
			filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"),
			"domain.transaction", payload)
		if callErr != nil || !response.OK || len(response.Data.Operations) != count {
			return fmt.Errorf("bounded bulk transaction failed: response=%s err=%v", marshalEnvelope(response), callErr)
		}
		return nil
	}
	if err := writeRows(50, 0); err != nil {
		return err
	}
	if err := writeRows(40, 50); err != nil {
		return err
	}
	queryOverLimit, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, readerURI,
		filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"),
		"domain.query", []byte(`{"sql":"SELECT title FROM Entry"}`))
	if err != nil || queryOverLimit.OK || queryOverLimit.Error == nil || queryOverLimit.Error.Code != "resource_exhausted" {
		return fmt.Errorf("query result ceiling was not enforced over mTLS: response=%s err=%v", marshalEnvelope(queryOverLimit), err)
	}
	const rejectedCreateID = "00000000-0000-7000-8000-000000000002"
	limitedOperations := []map[string]any{{
		"kind": "create", "entity": "Entry", "id": rejectedCreateID,
		"object": map[string]string{"title": "resource-bound-must-not-commit", "category": "article"},
	}}
	for index := 0; index < 99; index++ {
		limitedOperations = append(limitedOperations, map[string]any{"kind": "get", "entity": "Entry", "id": largeObjectID})
	}
	limitedPayload, err := json.Marshal(map[string]any{"operations": limitedOperations})
	if err != nil {
		return err
	}
	transactionOverLimit, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, readerURI,
		filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"),
		"domain.transaction", limitedPayload)
	if err != nil || transactionOverLimit.OK || transactionOverLimit.Error == nil || transactionOverLimit.Error.Code != "resource_exhausted" {
		return fmt.Errorf("transaction result ceiling was not enforced over mTLS: response=%s err=%v", marshalEnvelope(transactionOverLimit), err)
	}
	unpublished, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, readerURI,
		filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"),
		"domain.get", []byte(fmt.Sprintf(`{"entity":"Entry","id":%q}`, rejectedCreateID)))
	if err != nil || !unpublished.OK || unpublished.Data.Found {
		return fmt.Errorf("over-limit transaction partially published its write: response=%s err=%v", marshalEnvelope(unpublished), err)
	}
	// Restart the volatile Domain process under a fresh Core incarnation. Core
	// must republish the configured model, while all in-memory objects disappear.
	stopProcess(domainProcess)
	domainIdentity = sdkmodels.PeerReplicaID{
		InstanceID: domainInstance, ReplicaID: domainReplica, IncarnationID: "inc-2", PlacementID: "local-test",
	}
	domainURI, err = lifecycle.ReplicaIdentityURI(domainIdentity)
	if err != nil {
		return err
	}
	domainCert, err = issueLeaf(root, rootKey, domainInstance, []string{domainURI}, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth})
	if err != nil {
		return err
	}
	for name, contents := range map[string][]byte{"domain.pem": domainCert.pem, "domain.key": domainCert.key} {
		if err := os.WriteFile(filepath.Join(workDirectory, name), contents, 0o600); err != nil {
			return err
		}
	}
	restartedArguments := append([]string(nil), domainArguments...)
	for index, argument := range restartedArguments {
		if index+1 < len(restartedArguments) && argument == "--incarnation-id" {
			restartedArguments[index+1] = domainIdentity.IncarnationID
		}
		if index+1 < len(restartedArguments) && argument == "--peer-identity" {
			restartedArguments[index+1] = domainURI
		}
	}
	domainProcess, err = startProcess(domainRoot, domainBinary, restartedArguments, os.Environ(), workDirectory, "domain-restarted")
	if err != nil {
		return err
	}
	defer stopProcess(domainProcess)
	if err := waitForDomainGeneration(ctx, readinessClient, readinessURL, "4", domainProcess); err != nil {
		return err
	}
	restartedRead, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, readerURI, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"), "domain.get",
		[]byte(fmt.Sprintf(`{"entity":"Entry","id":%q}`, newObjectID)))
	if err != nil || !restartedRead.OK || restartedRead.Data.Found {
		return fmt.Errorf("Domain restart did not restore Core model while losing volatile objects: response=%s err=%v", marshalEnvelope(restartedRead), err)
	}
	if err := waitForRuntimeDirectory(ctx, controlAddress, workDirectory, runtimeIdentity); err != nil {
		return err
	}
	reconnectedRuntimeCall, err := callRuntime(ctx, domainRoot, runtimePeerAddress, runtimeURI, readerURI, "readEntry", []string{"Entry"}, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"))
	if err != nil || reconnectedRuntimeCall.Status != http.StatusOK || !reconnectedRuntimeCall.Body.Output.OK || !reconnectedRuntimeCall.Body.Output.Payload.OK {
		return fmt.Errorf("Runtime did not reconnect through Core directory to restarted Domain: status=%d body=%s err=%v", reconnectedRuntimeCall.Status, reconnectedRuntimeCall.RawBody, err)
	}
	// Runtime's catalog is also process-local. Restart it under a fresh Core
	// incarnation and prove it pulls its exact generation and re-resolves Domain.
	stopProcess(runtimeProcess)
	runtimeIdentity = sdkmodels.PeerReplicaID{
		InstanceID: runtimeInstance, ReplicaID: runtimeReplica, IncarnationID: "inc-runtime-2", PlacementID: "runtime-test",
	}
	runtimeURI, err = lifecycle.ReplicaIdentityURI(runtimeIdentity)
	if err != nil {
		return err
	}
	runtimeCert, err = issueLeaf(root, rootKey, "runtime", []string{runtimeURI}, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth})
	if err != nil {
		return err
	}
	for name, contents := range map[string][]byte{"runtime.pem": runtimeCert.pem, "runtime.key": runtimeCert.key} {
		if err := os.WriteFile(filepath.Join(workDirectory, name), contents, 0o600); err != nil {
			return err
		}
	}
	restartedRuntimeArguments := append([]string(nil), runtimeArguments...)
	for index, argument := range restartedRuntimeArguments {
		if index+1 < len(restartedRuntimeArguments) && argument == "--incarnation-id" {
			restartedRuntimeArguments[index+1] = runtimeIdentity.IncarnationID
		}
		if index+1 < len(restartedRuntimeArguments) && argument == "--peer-identity" {
			restartedRuntimeArguments[index+1] = runtimeURI
		}
	}
	runtimeProcess, err = startProcess(runtimeRoot, runtimeBinary, restartedRuntimeArguments, os.Environ(), workDirectory, "runtime-restarted")
	if err != nil {
		return err
	}
	defer stopProcess(runtimeProcess)
	if err := waitForDomainGeneration(ctx, readinessClient, runtimeReadinessURL, "3", runtimeProcess); err != nil {
		return err
	}
	if err := waitForRuntimeDirectory(ctx, controlAddress, workDirectory, runtimeIdentity); err != nil {
		return err
	}
	runtimeGrantSettings := domainSettingsWithCategory(readerURI, runtimeURI)
	operationID, err = updateSettings(ctx, readinessClient, management, managementBase, bootstrapToken,
		4, "domain-runtime-restart-grant-4", runtimeGrantSettings)
	if err != nil {
		return err
	}
	if err := waitForOperation(ctx, readinessClient, management, managementBase, bootstrapToken, operationID); err != nil {
		return err
	}
	if err := waitForDomainGeneration(ctx, readinessClient, readinessURL, "5", domainProcess); err != nil {
		return err
	}
	directRuntimeGrant, err := callDomain(ctx, domainRoot, domainPeerAddress, domainURI, runtimeURI,
		filepath.Join(workDirectory, "runtime.pem"), filepath.Join(workDirectory, "runtime.key"), filepath.Join(workDirectory, "root.pem"),
		"domain.transaction", []byte(`{"operations":[{"kind":"create","entity":"Entry","object":{"title":"runtime-restart-grant-check","category":"article"}}]}`))
	if err != nil || !directRuntimeGrant.OK {
		return fmt.Errorf("Core grant did not authorize the restarted Runtime identity: response=%s err=%v", marshalEnvelope(directRuntimeGrant), err)
	}
	runtimeAfterRestart, err := callRuntime(ctx, domainRoot, runtimePeerAddress, runtimeURI, readerURI, "readEntry", []string{"Entry"}, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"))
	if err != nil || runtimeAfterRestart.Status != http.StatusOK || !runtimeAfterRestart.Body.Output.OK || !runtimeAfterRestart.Body.Output.Payload.OK {
		return fmt.Errorf("Runtime restart did not pull Core generation and reconnect to Domain: status=%d body=%s err=%v", runtimeAfterRestart.Status, runtimeAfterRestart.RawBody, err)
	}
	if err := deleteRuntimeDomainLink(ctx, readinessClient, management, managementBase, bootstrapToken); err != nil {
		return err
	}
	linkRevokedCall, err := callRuntime(ctx, domainRoot, runtimePeerAddress, runtimeURI, readerURI, "readEntry", []string{"Entry"}, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"))
	if err != nil || linkRevokedCall.Status != http.StatusOK || linkRevokedCall.Body.Output.OK {
		return fmt.Errorf("Runtime continued calling Domain after Core peer-link deletion: status=%d body=%s err=%v", linkRevokedCall.Status, linkRevokedCall.RawBody, err)
	}
	if err := createRuntimeDomainLink(ctx, readinessClient, management, managementBase, bootstrapToken); err != nil {
		return err
	}
	if err := waitForRuntimeDirectory(ctx, controlAddress, workDirectory, runtimeIdentity); err != nil {
		return err
	}
	drainTitle := "runtime-drain-committed"
	drainContext, cancelDrain := context.WithTimeout(context.Background(), 20*time.Second)
	drainCall := make(chan runtimeCallResult, 1)
	drainCallErr := make(chan error, 1)
	go func() {
		result, callErr := callRuntime(drainContext, domainRoot, runtimePeerAddress, runtimeURI, readerURI,
			"drainWrite", []string{"Entry"}, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"))
		drainCall <- result
		drainCallErr <- callErr
	}()
	if err := waitForDomainTitle(drainContext, domainRoot, domainPeerAddress, domainURI, readerURI, drainTitle,
		filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem")); err != nil {
		cancelDrain()
		select {
		case completed := <-drainCall:
			callErr := <-drainCallErr
			return fmt.Errorf("Runtime drain invocation did not commit its observable Domain write: %w (call status=%d body=%s call error=%v; Runtime log: %s)", err, completed.Status, completed.RawBody, callErr, readFixtureLog(workDirectory, "runtime-restarted"))
		default:
			return fmt.Errorf("Runtime drain invocation did not commit its observable Domain write: %w", err)
		}
	}
	select {
	case <-drainCall:
		cancelDrain()
		return errors.New("Runtime drain invocation completed before shutdown was requested")
	default:
	}
	if err := runtimeProcess.command.Process.Signal(syscall.SIGTERM); err != nil {
		cancelDrain()
		return fmt.Errorf("signal Runtime graceful shutdown: %w", err)
	}
	refusalContext, cancelRefusal := context.WithTimeout(ctx, 3*time.Second)
	refusedAfterDrain, refusalErr := callRuntime(refusalContext, domainRoot, runtimePeerAddress, runtimeURI, readerURI,
		"drainWrite", []string{"Entry"}, filepath.Join(workDirectory, "reader.pem"), filepath.Join(workDirectory, "reader.key"), filepath.Join(workDirectory, "root.pem"))
	cancelRefusal()
	if refusalErr == nil && refusedAfterDrain.Status == http.StatusOK && refusedAfterDrain.Body.Output.OK {
		cancelDrain()
		return errors.New("Runtime admitted a new invocation after graceful shutdown began")
	}
	var completedDrain runtimeCallResult
	select {
	case completedDrain = <-drainCall:
		if err := <-drainCallErr; err != nil {
			cancelDrain()
			return fmt.Errorf("admitted Runtime drain invocation failed: %w", err)
		}
	case <-drainContext.Done():
		cancelDrain()
		return fmt.Errorf("Runtime did not finish its admitted invocation during shutdown: %w", drainContext.Err())
	}
	cancelDrain()
	if completedDrain.Status != http.StatusOK || !completedDrain.Body.Output.OK || !completedDrain.Body.Output.Payload.OK {
		return fmt.Errorf("admitted Runtime drain invocation returned failure: status=%d body=%s", completedDrain.Status, completedDrain.RawBody)
	}
	select {
	case <-runtimeProcess.done:
		if runtimeProcess.err != nil {
			return fmt.Errorf("Runtime exited unsuccessfully after drain: %w", runtimeProcess.err)
		}
	case <-ctx.Done():
		return fmt.Errorf("Runtime did not exit after draining: %w", ctx.Err())
	}
	if err := waitForCoreRuntimeDeregistration(ctx, readinessClient, managementBase, management, bootstrapToken); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"generationApplied": true, "coreConfiguredModel": true, "grantedWriteAccepted": true,
		"droppedWriteResponseObserved": true, "retryAfterDroppedResponseRecovered": true,
		"crossProcessWriteIDConflictRejected":         true,
		"invalidCandidateRefusedAndStateRetained":     true,
		"runtimeInvalidGenerationRefusedAndRecovered": true,
		"globalReadAccepted":                          true, "ungrantedWriteDenied": true, "readOnlyQueryAccepted": true,
		"grantGenerationUpdateApplied": true, "existingDataRetained": true,
		"newGrantApplied": true, "removedGrantDenied": true,
		"modelGenerationApplied": true, "modelGenerationClearedData": true,
		"newModelValidated": true, "runtimeReadPathAccepted": true,
		"queryResultLimitEnforced": true, "transactionResultLimitIsAtomic": true,
		"runtimeGrantDenied":      true,
		"runtimeCoreGrantApplied": true, "runtimeWriteAccepted": true,
		"runtimeWriteVisibleToOtherPlugin": true,
		"runtimeEntityNarrowingDenied":     true, "runtimeMethodNarrowingDenied": true,
		"domainRestartLostVolatileData": true, "domainReconnectRestoredGeneration": true,
		"runtimeHostCallReconnected": true, "runtimeRestartReloadedCoreGeneration": true,
		"runtimeCoreLinkRevocationDenied": true, "runtimeGracefulDeregisterObserved": true,
		"runtimeDrainedAdmittedCall": true, "runtimeRejectedCallAfterDrain": true,
	})
}

func waitForCoreRuntimeDeregistration(ctx context.Context, client *http.Client, baseURL string, management config.ManagementWords, token string) error {
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(deadline, http.MethodGet, baseURL+management.Paths.Status, nil)
		if err == nil {
			request.Header.Set("Authorization", "Bearer "+token)
			response, requestErr := client.Do(request)
			if requestErr == nil {
				var status map[string]any
				decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&status)
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK && decodeErr == nil {
					readiness, _ := status[management.JSON.DataPlaneReadiness].(map[string]any)
					state, _ := readiness[management.JSON.State].(string)
					reason, _ := readiness[management.JSON.Reason].(string)
					if state == management.Statuses.NotReady && strings.Contains(reason, runtimeInstance) {
						return nil
					}
				}
			}
		}
		select {
		case <-deadline.Done():
			return fmt.Errorf("Core did not observe graceful Runtime deregistration: %w", deadline.Err())
		case <-ticker.C:
		}
	}
}

func domainSettings(grants ...string) []byte {
	return domainSettingsWithFields(`[ {"name":"title","type":"text","required":true} ]`, grants...)
}

func domainSettingsWithCategory(grants ...string) []byte {
	return domainSettingsWithFields(`[ {"name":"title","type":"text","required":true}, {"name":"category","type":"text","required":true} ]`, grants...)
}

func domainSettingsWithFields(fields string, grants ...string) []byte {
	grantJSON := make([]string, 0, len(grants))
	for _, identity := range grants {
		grantJSON = append(grantJSON, fmt.Sprintf(`{"identity":%q,"entity":"Entry","operations":["create","batch"]}`, identity))
	}
	return []byte(fmt.Sprintf(`{"schemaVersion":"2","model":{"schemaVersion":"1","entities":[{"name":"Entry","fields":%s}]},"writeGrants":[%s],"gc":{"intervalSeconds":30,"batchSize":128,"scanLimit":4096,"referenceChecks":100000}}`, fields, strings.Join(grantJSON, ",")))
}

func domainSettingsWithUnknownGrant(identity string) []byte {
	return []byte(fmt.Sprintf(`{"schemaVersion":"2","model":{"schemaVersion":"1","entities":[{"name":"Entry","fields":[{"name":"title","type":"text","required":true}]}]},"writeGrants":[{"identity":%q,"entity":"MissingEntity","operations":["create"]}],"gc":{"intervalSeconds":30,"batchSize":128,"scanLimit":4096,"referenceChecks":100000}}`, identity))
}

func updateSettings(ctx context.Context, client *http.Client, management config.ManagementWords, baseURL, token string, revision int, idempotencyKey string, settings []byte) (string, error) {
	return updateSettingsForInstance(ctx, client, management, baseURL, token, domainInstance, revision, idempotencyKey, settings)
}

func updateSettingsForInstance(ctx context.Context, client *http.Client, management config.ManagementWords, baseURL, token, instanceID string, revision int, idempotencyKey string, settings []byte) (string, error) {
	path := management.Paths.Plugins + management.Paths.PluginIDSeparator + instanceID + management.Paths.PluginSettingsSuffix
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, baseURL+path, strings.NewReader(string(settings)))
	if err != nil {
		return "", err
	}
	request.Header.Set(management.Headers.ContentType, management.ContentTypes.JSON)
	request.Header.Set(management.Headers.IfMatch, fmt.Sprintf(`"%d"`, revision))
	request.Header.Set(management.Idempotency.Key, idempotencyKey)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return "", fmt.Errorf("Core settings update returned HTTP %d: %s", response.StatusCode, body)
	}
	var payload struct {
		ID string `json:"operationId"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil || payload.ID == "" {
		return "", fmt.Errorf("Core settings update returned an invalid operation: %w", err)
	}
	return payload.ID, nil
}

func waitForOperation(ctx context.Context, client *http.Client, management config.ManagementWords, baseURL, token, operationID string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+management.Paths.Operations+"/"+url.PathEscape(operationID), nil)
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		var operation struct {
			State string `json:"state"`
			Code  string `json:"errorCode"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&operation)
		_ = response.Body.Close()
		if decodeErr != nil || response.StatusCode != http.StatusOK {
			return fmt.Errorf("Core operation status request failed: HTTP %d decode=%v", response.StatusCode, decodeErr)
		}
		switch operation.State {
		case management.Statuses.Succeeded:
			return nil
		case management.Statuses.Failed, management.Statuses.Degraded:
			return fmt.Errorf("Core settings operation ended in %s (%s)", operation.State, operation.Code)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitForOperationFailure(ctx context.Context, client *http.Client, management config.ManagementWords, baseURL, token, operationID, expectedCode string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+management.Paths.Operations+"/"+url.PathEscape(operationID), nil)
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		var operation struct {
			State string `json:"state"`
			Code  string `json:"errorCode"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&operation)
		_ = response.Body.Close()
		if decodeErr != nil || response.StatusCode != http.StatusOK {
			return fmt.Errorf("Core operation status request failed: HTTP %d decode=%v", response.StatusCode, decodeErr)
		}
		if operation.State == management.Statuses.Failed {
			if operation.Code != expectedCode {
				return fmt.Errorf("Core candidate failed with %q, want %q", operation.Code, expectedCode)
			}
			return nil
		}
		if operation.State == management.Statuses.Succeeded || operation.State == management.Statuses.Degraded {
			return fmt.Errorf("Core accepted invalid Domain candidate: state=%s code=%s", operation.State, operation.Code)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func seedDomainConfiguration(ctx context.Context, databasePath, domainRoot string) error {
	contract, err := config.LoadSQLiteContract()
	if err != nil {
		return err
	}
	database, err := storage.OpenSQLite(ctx, databasePath, storage.SQLiteOptions{
		Driver: contract.Driver, ParentDirectoryMode: contract.ParentDirectoryMode, DatabaseFileMode: contract.DatabaseFileMode,
		MaxOpenConnections: contract.MaxOpenConnections, MaxIdleConnections: contract.MaxIdleConnections,
		SchemaVersion: contract.SchemaVersion, HasMigrationTableQuery: contract.HasMigrationTableQuery,
		MigrationVersionQuery: contract.MigrationVersionQuery, SchemaVersionError: contract.SchemaVersionError,
		Pragmas: contract.Pragmas,
	}, contract.Schema)
	if err != nil {
		return err
	}
	defer database.Close()
	manifest, err := os.ReadFile(filepath.Join(domainRoot, "contracts", "v2", "plugin.json"))
	if err != nil {
		return err
	}
	settings := domainSettings(writerURI)
	digest := sha256.Sum256(settings)
	if _, err := database.ExecContext(ctx, `INSERT INTO plugin_instances(id, manifest_json, state) VALUES (?, ?, ?)`, domainInstance, manifest, "configured"); err != nil {
		return err
	}
	_, err = database.ExecContext(ctx, `INSERT INTO plugin_config_generations(instance_id, generation, slot, raw_json, sha256, schema_version, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		domainInstance, 1, "active", settings, hex.EncodeToString(digest[:]), 2, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func seedRuntimeConfiguration(ctx context.Context, databasePath, runtimeRoot, readDigest, writeDigest, drainDigest string) error {
	contract, err := config.LoadSQLiteContract()
	if err != nil {
		return err
	}
	database, err := storage.OpenSQLite(ctx, databasePath, storage.SQLiteOptions{
		Driver: contract.Driver, ParentDirectoryMode: contract.ParentDirectoryMode, DatabaseFileMode: contract.DatabaseFileMode,
		MaxOpenConnections: contract.MaxOpenConnections, MaxIdleConnections: contract.MaxIdleConnections,
		SchemaVersion: contract.SchemaVersion, HasMigrationTableQuery: contract.HasMigrationTableQuery,
		MigrationVersionQuery: contract.MigrationVersionQuery, SchemaVersionError: contract.SchemaVersionError,
		Pragmas: contract.Pragmas,
	}, contract.Schema)
	if err != nil {
		return err
	}
	defer database.Close()
	manifest, err := os.ReadFile(filepath.Join(runtimeRoot, "contracts", "v2", "plugin.json"))
	if err != nil {
		return err
	}
	settings := runtimeSettingsDocument(readDigest, writeDigest, drainDigest)
	digest := sha256.Sum256(settings)
	if _, err := database.ExecContext(ctx, `INSERT INTO plugin_instances(id, manifest_json, state) VALUES (?, ?, ?)`, runtimeInstance, manifest, "configured"); err != nil {
		return err
	}
	_, err = database.ExecContext(ctx, `INSERT INTO plugin_config_generations(instance_id, generation, slot, raw_json, sha256, schema_version, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		runtimeInstance, 1, "active", settings, hex.EncodeToString(digest[:]), 2, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func runtimeSettingsDocument(readDigest, writeDigest, drainDigest string) []byte {
	return []byte(fmt.Sprintf(`{"schemaVersion":"2","modules":[{"moduleId":"domainReader","sha256":%q},{"moduleId":"domainWriter","sha256":%q},{"moduleId":"domainDrainWriter","sha256":%q}],"commands":[{"name":"readEntry","moduleId":"domainReader","export":"invoke","entities":["Entry"],"calls":[{"plugin":"domain","method":"domain.get"}]},{"name":"writeEntry","moduleId":"domainWriter","export":"invoke","entities":["Entry"],"calls":[{"plugin":"domain","method":"domain.transaction"}]},{"name":"writeEntryMethodDenied","moduleId":"domainWriter","export":"invoke","entities":["Entry"],"calls":[{"plugin":"domain","method":"domain.get"}]},{"name":"drainWrite","moduleId":"domainDrainWriter","export":"invoke","entities":["Entry"],"calls":[{"plugin":"domain","method":"domain.transaction"}]}]}`, readDigest, writeDigest, drainDigest))
}

func createRuntimeDomainLink(ctx context.Context, client *http.Client, management config.ManagementWords, baseURL, token string) error {
	body := []byte(fmt.Sprintf(`{"callerInstanceId":%q,"targetInstanceId":%q,"rules":[{"placementRule":"remote","carrier":"tcp","weight":100,"requiredContracts":[]}]}`, runtimeInstance, domainInstance))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+management.Paths.PluginLinks, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set(management.Headers.ContentType, management.ContentTypes.JSON)
	request.Header.Set(management.Idempotency.Key, fmt.Sprintf("runtime-domain-link-%d", time.Now().UnixNano()))
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		problem, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return fmt.Errorf("Core Runtime→Domain peer link creation returned HTTP %d: %s", response.StatusCode, problem)
	}
	return nil
}

func deleteRuntimeDomainLink(ctx context.Context, client *http.Client, management config.ManagementWords, baseURL, token string) error {
	path := management.Paths.PluginLinks + management.Paths.PluginIDSeparator + runtimeInstance + management.Paths.PluginIDSeparator + domainInstance
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, baseURL+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set(management.Headers.IfMatch, `"1"`)
	request.Header.Set(management.Idempotency.Key, "runtime-domain-link-delete")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		problem, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return fmt.Errorf("Core Runtime→Domain peer link deletion returned HTTP %d: %s", response.StatusCode, problem)
	}
	return nil
}

func waitForRuntimeDirectory(ctx context.Context, coreAddress, directory string, identity sdkmodels.PeerReplicaID) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		lastErr = pollRuntimeDirectory(ctx, coreAddress, directory, identity)
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Core did not publish eligible Domain replica before timeout: %w (last poll: %v)", ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func pollRuntimeDirectory(ctx context.Context, coreAddress, directory string, identity sdkmodels.PeerReplicaID) error {
	httpContract, err := sdkinfrastructure.LoadHTTPContract()
	if err != nil {
		return err
	}
	credentials, err := sdkinfrastructure.LoadCredentials(httpContract, sdkinfrastructure.CredentialsMaterial{
		CAFile:                filepath.Join(directory, "root.pem"),
		ServerCertificateFile: filepath.Join(directory, "runtime.pem"), ServerKeyFile: filepath.Join(directory, "runtime.key"),
		ClientCertificateFile: filepath.Join(directory, "runtime.pem"), ClientKeyFile: filepath.Join(directory, "runtime.key"),
	})
	if err != nil {
		return err
	}
	provider, err := sdkinfrastructure.NewStaticCredentialsProvider(credentials)
	if err != nil {
		return err
	}
	defer provider.Zeroize()
	revocation, err := sdkinfrastructure.NewRevocation(sdkinfrastructure.RevocationConfiguration{
		Authorities: credentials.TrustAuthorities(), Files: []string{filepath.Join(directory, "revoked.pem")},
	})
	if err != nil {
		return err
	}
	peer, err := sdkmodels.NewPeerIdentity("core-child", "")
	if err != nil {
		return err
	}
	transport, err := sdkinfrastructure.NewMutualTLSClient(httpContract, provider, sdkinfrastructure.MutualTLSClientConfig{
		Peer: peer, ServerName: "localhost", Revocation: revocation,
	})
	if err != nil {
		return err
	}
	defer transport.CloseIdleConnections()
	pollContract, err := sdkinfrastructure.LoadPeerDirectoryPollContract()
	if err != nil {
		return err
	}
	client, err := sdkinfrastructure.NewPeerDirectoryClient(pollContract, "https://"+coreAddress, transport, identity)
	if err != nil {
		return err
	}
	result, err := client.Poll(ctx, "", 0)
	if err != nil {
		return fmt.Errorf("Runtime-identity Core directory poll failed: %w", err)
	}
	for _, link := range result.Directory.Links {
		if link.TargetInstanceID == domainInstance && len(link.Replicas) == 1 && link.Replicas[0].Endpoint != "" && link.Replicas[0].Eligibility == sdkmodels.PeerEligibilityReady {
			return nil
		}
	}
	document, marshalErr := json.Marshal(result.Directory)
	if marshalErr != nil {
		return errors.New("Core peer directory omitted the eligible Domain replica for Runtime")
	}
	return fmt.Errorf("Core peer directory omitted the eligible Domain replica for Runtime: %s", document)
}

func domainReadModule() ([]byte, error) {
	payload := []byte(`{"plugin":"domain","method":"domain.get","payload":{"entity":"Entry","id":"018f47a0-7b1c-7abc-8def-0123456789ab"}}`)
	return domainHostCallModule(payload)
}

func domainWriteModule() ([]byte, error) {
	payload := []byte(`{"plugin":"domain","method":"domain.transaction","payload":{"operations":[{"kind":"create","entity":"Entry","object":{"title":"from-runtime"}}]}}`)
	return domainHostCallModule(payload)
}

func domainWriteThenLoopModule() ([]byte, error) {
	payload := []byte(`{"plugin":"domain","method":"domain.transaction","payload":{"operations":[{"kind":"create","entity":"Entry","object":{"title":"runtime-drain-committed","category":"article"}}]}}`)
	return domainHostCallThenLoopModule(payload, 5000000)
}

func domainHostCallModule(payload []byte) ([]byte, error) {
	return domainHostCallThenLoopModule(payload, 0)
}

func domainHostCallThenLoopModule(payload []byte, iterations uint32) ([]byte, error) {
	typeSection := []byte{2, 0x60, 1, 0x7f, 1, 0x7f, 0x60, 2, 0x7f, 0x7f, 1, 0x7e}
	importSection := []byte{1}
	importSection = appendWasmName(importSection, "liapoldus.plugin.v2")
	importSection = appendWasmName(importSection, "call")
	importSection = append(importSection, 0, 1)
	functionSection := []byte{2, 0, 1}
	memorySection := []byte{1, 0, 1}
	exportSection := []byte{3}
	exportSection = appendWasmName(exportSection, "memory")
	exportSection = append(exportSection, 2, 0)
	exportSection = appendWasmName(exportSection, "alloc")
	exportSection = append(exportSection, 0, 1)
	exportSection = appendWasmName(exportSection, "invoke")
	exportSection = append(exportSection, 0, 2)
	allocateBody := append([]byte{0, 0x41}, wasmSLEB(4096)...)
	allocateBody = append(allocateBody, 0x0b)
	invokeBody := []byte{0}
	if iterations > 0 {
		invokeBody = []byte{2, 1, 0x7f, 1, 0x7e}
	}
	invokeBody = append(invokeBody, 0x41, 0, 0x41)
	invokeBody = append(invokeBody, wasmSLEB(int64(len(payload)))...)
	invokeBody = append(invokeBody, 0x10, 0)
	if iterations > 0 {
		invokeBody = append(invokeBody, 0x21, 3, 0x41)
		invokeBody = append(invokeBody, wasmSLEB(int64(iterations))...)
		invokeBody = append(invokeBody, 0x21, 2, 0x03, 0x40, 0x20, 2, 0x41, 1, 0x6b, 0x22, 2, 0x0d, 0, 0x0b, 0x20, 3)
	}
	invokeBody = append(invokeBody, 0x0b)
	codeSection := []byte{2}
	codeSection = appendWasmVector(codeSection, allocateBody)
	codeSection = appendWasmVector(codeSection, invokeBody)
	dataSegment := []byte{1, 0, 0x41, 0, 0x0b}
	dataSegment = appendWasmVector(dataSegment, payload)
	module := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	module = appendWasmSection(module, 1, typeSection)
	module = appendWasmSection(module, 2, importSection)
	module = appendWasmSection(module, 3, functionSection)
	module = appendWasmSection(module, 5, memorySection)
	module = appendWasmSection(module, 7, exportSection)
	module = appendWasmSection(module, 10, codeSection)
	module = appendWasmSection(module, 11, dataSegment)
	return module, nil
}

func waitForDomainTitle(ctx context.Context, domainRoot, endpoint, expectedURI, callerURI, title, certificatePath, keyPath, rootPath string) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	payload, err := json.Marshal(struct {
		SQL    string   `json:"sql"`
		Params []string `json:"params"`
	}{SQL: "SELECT id FROM Entry WHERE title = ?", Params: []string{title}})
	if err != nil {
		return err
	}
	for {
		result, err := callDomain(ctx, domainRoot, endpoint, expectedURI, callerURI, certificatePath, keyPath, rootPath, "domain.query", payload)
		if err == nil && result.OK && len(result.Data.Rows) > 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for Domain object %q: %w (last response: %s)", title, ctx.Err(), marshalEnvelope(result))
		case <-ticker.C:
		}
	}
}

func appendWasmName(destination []byte, value string) []byte {
	destination = appendWasmU32(destination, uint32(len(value)))
	return append(destination, value...)
}

func appendWasmVector(destination, value []byte) []byte {
	destination = appendWasmU32(destination, uint32(len(value)))
	return append(destination, value...)
}

func appendWasmSection(module []byte, id byte, payload []byte) []byte {
	module = append(module, id)
	module = appendWasmU32(module, uint32(len(payload)))
	return append(module, payload...)
}

func appendWasmU32(destination []byte, value uint32) []byte {
	for value >= 0x80 {
		destination = append(destination, byte(value)|0x80)
		value >>= 7
	}
	return append(destination, byte(value))
}

func wasmSLEB(value int64) []byte {
	encoded := make([]byte, 0, 10)
	for {
		current := byte(value & 0x7f)
		value >>= 7
		more := !((value == 0 && current&0x40 == 0) || (value == -1 && current&0x40 != 0))
		if more {
			current |= 0x80
		}
		encoded = append(encoded, current)
		if !more {
			return encoded
		}
	}
}

func callDomain(ctx context.Context, domainRoot, endpoint, expectedURI, callerURI, certificatePath, keyPath, rootPath, method string, payload []byte) (envelope, error) {
	output, err := callPeer(ctx, domainRoot, endpoint, expectedURI, callerURI, certificatePath, keyPath, rootPath, method, payload)
	if err != nil {
		return envelope{}, err
	}
	var response envelope
	err = json.Unmarshal(output, &response)
	return response, err
}

func callRuntime(ctx context.Context, domainRoot, endpoint, expectedURI, callerURI, commandName string, entities []string, certificatePath, keyPath, rootPath string) (runtimeCallResult, error) {
	var result runtimeCallResult
	requestBody, err := json.Marshal(struct {
		Command  string          `json:"command"`
		Entities []string        `json:"entities"`
		Input    json.RawMessage `json:"input"`
	}{Command: commandName, Entities: entities, Input: json.RawMessage(`{}`)})
	if err != nil {
		return result, err
	}
	output, err := callPeer(ctx, domainRoot, endpoint, expectedURI, callerURI, certificatePath, keyPath, rootPath, "runtime.invoke", requestBody)
	if err != nil {
		return result, err
	}
	var response runtimeInvokeResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return result, err
	}
	result.Status, result.RawBody = response.Status, response.Body
	if err := json.Unmarshal([]byte(response.Body), &result.Body); err != nil {
		return result, err
	}
	return result, nil
}

func callPeer(ctx context.Context, domainRoot, endpoint, expectedURI, callerURI, certificatePath, keyPath, rootPath, method string, payload []byte) ([]byte, error) {
	if peerClientBinary == "" {
		return nil, errors.New("compiled peer client is unavailable")
	}
	command := exec.CommandContext(ctx, peerClientBinary, endpoint, expectedURI, callerURI, certificatePath, keyPath, rootPath, method)
	command.Env = os.Environ()
	command.Stdin = bytes.NewReader(payload)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("plugin peer client failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

// dropNextPeerCallResponse terminates TLS on both sides using the same
// authenticated identities, forwards generic pluginprotocol frames, and drops
// the first successful unary result after the upstream plugin has produced it.
// The Domain commit therefore occurs normally, while the caller observes a
// transport failure and must resolve the outcome through its writeId.
func dropNextPeerCallResponse(
	ctx context.Context,
	upstreamEndpoint, expectedDomainURI, expectedCallerURI string,
	serverCertificate, callerCertificate certificate,
	roots *x509.CertPool,
	committed func(context.Context) error,
) (string, <-chan error, error) {
	serverTLSCertificate, err := tls.X509KeyPair(serverCertificate.pem, serverCertificate.key)
	if err != nil {
		return "", nil, fmt.Errorf("load proxy server certificate: %w", err)
	}
	callerTLSCertificate, err := tls.X509KeyPair(callerCertificate.pem, callerCertificate.key)
	if err != nil {
		return "", nil, fmt.Errorf("load proxy caller certificate: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("listen for test proxy: %w", err)
	}
	endpoint := listener.Addr().String()
	finished := make(chan error, 1)
	acceptFinished := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
		case <-acceptFinished:
		}
	}()
	go func() {
		defer listener.Close()
		clientSocket, acceptErr := listener.Accept()
		close(acceptFinished)
		if acceptErr != nil {
			finished <- fmt.Errorf("accept test proxy client: %w", acceptErr)
			return
		}
		clientTLS := tls.Server(clientSocket, &tls.Config{
			Certificates: []tls.Certificate{serverTLSCertificate},
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    roots,
			MinVersion:   tls.VersionTLS13,
			VerifyConnection: func(state tls.ConnectionState) error {
				return verifyPeerURI(state, expectedCallerURI)
			},
		})
		if err := clientTLS.HandshakeContext(ctx); err != nil {
			_ = clientSocket.Close()
			finished <- fmt.Errorf("authenticate test proxy caller: %w", err)
			return
		}
		upstreamSocket, err := (&net.Dialer{}).DialContext(ctx, "tcp", upstreamEndpoint)
		if err != nil {
			_ = clientTLS.Close()
			finished <- fmt.Errorf("dial Domain upstream: %w", err)
			return
		}
		upstreamTLS := tls.Client(upstreamSocket, &tls.Config{
			Certificates: []tls.Certificate{callerTLSCertificate},
			RootCAs:      roots,
			ServerName:   "localhost",
			MinVersion:   tls.VersionTLS13,
			VerifyConnection: func(state tls.ConnectionState) error {
				return verifyPeerURI(state, expectedDomainURI)
			},
		})
		if err := upstreamTLS.HandshakeContext(ctx); err != nil {
			_ = clientTLS.Close()
			_ = upstreamSocket.Close()
			finished <- fmt.Errorf("authenticate test proxy upstream: %w", err)
			return
		}
		forwarded := make(chan error, 1)
		go func() {
			_, copyErr := io.Copy(upstreamTLS, clientTLS)
			forwarded <- copyErr
		}()
		go func() { _, _ = io.Copy(io.Discard, upstreamTLS) }()
		if committed == nil {
			_ = clientTLS.Close()
			_ = upstreamTLS.Close()
			finished <- errors.New("test proxy requires a commit probe")
			return
		}
		if err := committed(ctx); err != nil {
			_ = clientTLS.Close()
			_ = upstreamTLS.Close()
			finished <- fmt.Errorf("wait for committed Domain transaction: %w", err)
			return
		}
		_ = clientTLS.Close()
		_ = upstreamTLS.Close()
		<-forwarded
		finished <- nil
	}()
	return endpoint, finished, nil
}

func verifyPeerURI(state tls.ConnectionState, expected string) error {
	if len(state.PeerCertificates) == 0 {
		return errors.New("test proxy peer has no certificate")
	}
	for _, uri := range state.PeerCertificates[0].URIs {
		if uri.String() == expected {
			return nil
		}
	}
	return errors.New("test proxy authenticated an unexpected peer identity")
}

func readFixtureLog(directory, name string) string {
	path := filepath.Join(directory, name+".stderr.log")
	// The log path is constructed by this isolated test harness, never from a request.
	//nolint:gosec // Test-only diagnostic reads a file from its private temporary directory.
	contents, err := os.ReadFile(path)
	if err != nil {
		return "<unavailable>"
	}
	if len(contents) > 4096 {
		contents = contents[len(contents)-4096:]
	}
	return strings.TrimSpace(string(contents))
}

func waitForDomainGeneration(ctx context.Context, client *http.Client, endpoint, generation string, process *process) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err == nil {
			response, requestErr := client.Do(request)
			if requestErr == nil {
				var readiness sdkmodels.Readiness
				decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&readiness)
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK && decodeErr == nil && readiness.Ready && readiness.Generation == generation {
					return nil
				}
			}
		}
		select {
		case <-process.done:
			logContents, _ := os.ReadFile(process.logPath)
			return fmt.Errorf("Domain exited before applying generation %s: %w (stderr: %s)", generation, process.err, strings.TrimSpace(string(logContents)))
		case <-ctx.Done():
			return fmt.Errorf("Domain did not apply Core generation: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func assertReplicaGenerationRefused(ctx context.Context, client *http.Client, endpoint, generation, pendingGeneration string, process *process, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			return fmt.Errorf("plugin readiness request failed while retaining generation %s: %w", generation, err)
		}
		var readiness sdkmodels.Readiness
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&readiness)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusServiceUnavailable || decodeErr != nil || readiness.Ready ||
			readiness.Generation != generation || readiness.PendingGeneration != pendingGeneration {
			return fmt.Errorf("invalid candidate did not retain the fenced active generation: status=%d generation=%q pending=%q ready=%v decode=%v", response.StatusCode, readiness.Generation, readiness.PendingGeneration, readiness.Ready, decodeErr)
		}
		select {
		case <-process.done:
			return fmt.Errorf("plugin exited while retaining generation %s: %w", generation, process.err)
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitForHealth(ctx context.Context, client *http.Client, endpoint string, process *process) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/healthz", nil)
		if err == nil {
			response, requestErr := client.Do(request)
			if requestErr == nil {
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
		select {
		case <-process.done:
			return fmt.Errorf("Core exited before health: %w", process.err)
		case <-ctx.Done():
			return fmt.Errorf("Core did not become healthy: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func startProcess(directory, binary string, arguments []string, environment []string, logDirectory, name string) (*process, error) {
	stdout, err := os.OpenFile(filepath.Join(logDirectory, name+".stdout.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	stderr, err := os.OpenFile(filepath.Join(logDirectory, name+".stderr.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		_ = stdout.Close()
		return nil, err
	}
	command := exec.Command(binary, arguments...)
	command.Dir = directory
	command.Env = environment
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, err
	}
	if err := stdout.Close(); err != nil {
		_ = command.Process.Kill()
		return nil, err
	}
	if err := stderr.Close(); err != nil {
		_ = command.Process.Kill()
		return nil, err
	}
	started := &process{command: command, done: make(chan struct{}), logPath: filepath.Join(logDirectory, name+".stderr.log")}
	go func() {
		started.err = command.Wait()
		close(started.done)
	}()
	return started, nil
}

func stopProcess(started *process) {
	if started == nil || started.command == nil || started.command.Process == nil {
		return
	}
	_ = started.command.Process.Signal(syscall.SIGTERM)
	select {
	case <-started.done:
	case <-time.After(10 * time.Second):
		_ = started.command.Process.Kill()
		<-started.done
	}
}

func build(directory, binary, target string) error {
	command := exec.Command("go", "build", "-o", binary, target)
	command.Dir = directory
	command.Env = os.Environ()
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("build %s: %w: %s", target, err, output)
	}
	return nil
}

func issueRoot() ([]byte, *x509.Certificate, *ecdsa.PrivateKey, certificate, error) {
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, certificate{}, err
	}
	rootTemplate := &x509.Certificate{
		SerialNumber: randomSerial(), Subject: pkix.Name{CommonName: "core-domain-test-root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign, BasicConstraintsValid: true, SubjectKeyId: []byte{1, 2, 3, 4},
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		return nil, nil, nil, certificate{}, err
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return nil, nil, nil, certificate{}, err
	}
	coreCert, err := issueLeaf(root, rootKey, "core-child", nil, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth})
	if err != nil {
		return nil, nil, nil, certificate{}, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}), root, rootKey, coreCert, nil
}

func issueLeaf(root *x509.Certificate, signingKey *ecdsa.PrivateKey, commonName string, uris []string, usage []x509.ExtKeyUsage) (certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return certificate{}, err
	}
	template := &x509.Certificate{
		SerialNumber: randomSerial(), Subject: pkix.Name{CommonName: commonName},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: usage,
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	for _, value := range uris {
		reference, err := url.Parse(value)
		if err != nil {
			return certificate{}, err
		}
		template.URIs = append(template.URIs, reference)
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

func issueCRL(root *x509.Certificate, signingKey *ecdsa.PrivateKey) ([]byte, error) {
	now := time.Now().UTC()
	der, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number: big.NewInt(1), ThisUpdate: now.Add(-time.Minute), NextUpdate: now.Add(12 * time.Hour),
	}, root, signingKey)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: der}), nil
}

func httpClient(roots *x509.CertPool, clientCert certificate) (*http.Client, error) {
	configuration := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "localhost"}
	if len(clientCert.pem) > 0 {
		pair, err := tls.X509KeyPair(clientCert.pem, clientCert.key)
		if err != nil {
			return nil, err
		}
		configuration.Certificates = []tls.Certificate{pair}
	}
	// This fixture runs several separately race-instrumented processes in one
	// host. Keep every network operation bounded, while allowing race detector
	// overhead on the serialized Core management path.
	return &http.Client{Transport: &http.Transport{TLSClientConfig: configuration}, Timeout: 15 * time.Second}, nil
}

func freeAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	address := listener.Addr().String()
	return address, listener.Close()
}

func randomSerial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	value, err := rand.Int(rand.Reader, limit)
	if err != nil {
		panic(err)
	}
	return value
}

func marshalEnvelope(value envelope) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func repositoryRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
