package runtime

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/infrastructure/security"
	"github.com/Liapoldus/core/v3/internal/infrastructure/services"
	"github.com/Liapoldus/core/v3/internal/infrastructure/storage"
	settingsstore "github.com/Liapoldus/core/v3/internal/infrastructure/storage/settings"
	"github.com/Liapoldus/core/v3/internal/presentation/api"
	sdkmodels "github.com/Liapoldus/service-sdk/v2/domain/models"
)

type RunOptions struct {
	Output            string
	Words             config.RuntimeWords
	WriteFailure      func(output string, exitCode int, code, detail string)
	TrafficController *config.TrafficControllerConfig
}

type runContext struct {
	output            string
	words             config.RuntimeWords
	writeFailure      func(output string, exitCode int, code, detail string)
	trafficController *config.TrafficControllerConfig
}

type bootstrapStores struct {
	auditStore          *storage.SQLiteAuditStore
	keyStore            *storage.SQLiteServiceKeyStore
	operationStore      *storage.SQLiteOperationStore
	serviceConfigStore  *storage.SQLiteServiceConfigurationStore
	serviceSecretStore  *storage.SQLiteServiceSecretReferenceStore
	serviceLinkStore    *storage.SQLiteServiceLinkPolicyStore
	trafficRolloutStore *storage.SQLiteTrafficRolloutStore
	serviceConfigWords  config.ServiceConfigurationWords
}

type bootstrapInventory struct {
	management config.ManagementWords
	contract   config.ServiceInventoryContract
	records    []storage.ServiceInstanceRecord
	view       []any
}

type managementInputs struct {
	auditWords        config.AuditWords
	errorCatalog      config.ErrorCatalog
	trafficRolloutAPI config.TrafficRolloutAPIContract
	tlsConfiguration  *tls.Config
}

// Host is the managed lifetime of one Core runtime. It is intentionally
// transport-agnostic at the public boundary: the composition root selects the
// bootstrap settings and the host owns cancellation, readiness and shutdown.
type Host struct {
	cancel context.CancelFunc
	ready  <-chan struct{}
	ended  <-chan struct{}
	exitMu sync.RWMutex
	exit   int
}

func Serve(bootstrapConfig config.BootstrapConfig, input RunOptions) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	host, err := Start(ctx, bootstrapConfig, input)
	if err != nil {
		input.WriteFailure(input.Output, input.Words.Exits.Unavailable, input.Words.Codes.ConfigInvalid, input.Words.Diagnostics.ConfigInvalid)
		return input.Words.Exits.Unavailable
	}
	return host.Wait()
}

// Start launches one Core runtime under the caller's context. It does not
// return ready until WaitReady observes the bound management listener; callers
// must still invoke Close or cancel the context when the host is no longer
// needed. Core remains singleton relative to its SQLite state lock.
func Start(ctx context.Context, bootstrapConfig config.BootstrapConfig, input RunOptions) (*Host, error) {
	if ctx == nil {
		return nil, errors.New("core host context is required")
	}
	derived, cancel := context.WithCancel(ctx)
	ready := make(chan struct{})
	ended := make(chan struct{})
	host := &Host{cancel: cancel, ready: ready, ended: ended}
	options := runContext{output: input.Output, words: input.Words, writeFailure: input.WriteFailure, trafficController: input.TrafficController}
	go func() {
		exitCode := serveBootstrap(derived, options, bootstrapConfig, ready)
		host.exitMu.Lock()
		host.exit = exitCode
		host.exitMu.Unlock()
		close(ended)
	}()
	return host, nil
}

// WaitReady blocks until the management listener is bound or startup exits.
func (host *Host) WaitReady(ctx context.Context) error {
	if host == nil || ctx == nil {
		return errors.New("core host is unavailable")
	}
	select {
	case <-host.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-host.ended:
		return errors.New("core host failed before readiness")
	}
}

// Ready reports whether the management listener has been bound.
func (host *Host) Ready() bool {
	if host == nil {
		return false
	}
	select {
	case <-host.ready:
		return true
	default:
		return false
	}
}

// Wait waits for the runtime and returns its process-compatible exit code.
func (host *Host) Wait() int {
	if host == nil {
		return 1
	}
	<-host.ended
	host.exitMu.RLock()
	defer host.exitMu.RUnlock()
	return host.exit
}

// Close requests graceful shutdown and waits for all Core listeners and
// background workers to finish.
func (host *Host) Close() int {
	if host == nil {
		return 1
	}
	host.cancel()
	return host.Wait()
}

func serveBootstrap(ctx context.Context, options runContext, bootstrap config.BootstrapConfig, ready chan<- struct{}) int {
	database, unlockState, err := OpenServingDatabase(context.Background(), bootstrap.StatePath)
	if err != nil {
		options.writeFailure(options.output, options.words.Exits.Unavailable, options.words.Codes.ConfigInvalid, options.words.Diagnostics.ConfigInvalid)
		return options.words.Exits.Unavailable
	}
	defer unlockState()
	defer database.Close()
	coreSettings := settingsstore.New(database)
	settingsSnapshot, err := coreSettings.Read(context.Background())
	if err != nil {
		return failBootstrap(options, options.words.Exits.Validation, options.words.Diagnostics.ConfigInvalid)
	}
	if _, err = coreSettings.Activate(context.Background(), settingsSnapshot.DesiredRevision, func(raw []byte) error {
		settings, err := config.DecodeSettings(raw)
		if err != nil {
			return err
		}
		candidate := settings.Bootstrap(bootstrap.StatePath)
		if _, err = ManagementTLS(candidate, options.words.Diagnostics.ConfigInvalid); err != nil {
			return err
		}
		_, err = serviceControlTLS(candidate)
		return err
	}); err != nil {
		return failBootstrap(options, options.words.Exits.Validation, options.words.Diagnostics.ConfigInvalid)
	}
	stores, exitCode := loadBootstrapStores(options, bootstrap, database)
	if exitCode != options.words.Exits.OK {
		return exitCode
	}
	if err := bootstrapManagementKey(context.Background(), stores.keyStore, options.words); err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	inventory, exitCode := loadBootstrapInventory(options, database)
	if exitCode != options.words.Exits.OK {
		return exitCode
	}
	managementInputs, exitCode := loadManagementInputs(options, bootstrap)
	if exitCode != options.words.Exits.OK {
		return exitCode
	}
	// The control plane is built from the effective settings stored in SQLite.
	// An invalid control listener or trust configuration fails startup before
	// Core advertises readiness.
	controlPlane, err := buildServiceRESTControl(bootstrap)
	if err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	if len(inventory.contract.ValidStates) == 0 {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	registeredInstances, err := storage.ListRegisteredServiceInstances(context.Background(), database)
	if err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	controlPlane.ReplicaDirectory.MarkRegisteredInstances(registeredInstances)
	controlPlane.RegisterReplica = func(ctx context.Context, registration services.SDKReplicaRegistrationRequest) error {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		return storage.RegisterServiceInstanceReplica(ctx, database,
			registration.Identity.InstanceID, registration.Identity.ReplicaID, []byte("{}"),
			inventory.contract.ValidStates[0], storage.ReplicaObservedPending, now)
	}
	instanceIDs := make([]string, 0, len(inventory.records))
	for _, record := range inventory.records {
		instanceIDs = append(instanceIDs, record.ID)
	}
	configurationSnapshot, err := storage.NewServiceConfigurationSnapshot(context.Background(), stores.serviceConfigStore, instanceIDs)
	if err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	replicaObservations := storage.NewServiceReplicaStore(database, options.words.Diagnostics.ConfigInvalid)
	convergenceSnapshot, err := storage.NewServiceConvergenceSnapshot(context.Background(), stores.serviceConfigStore, replicaObservations, instanceIDs)
	if err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	linkPolicySnapshot, err := storage.NewServiceLinkPolicySnapshot(context.Background(), stores.serviceLinkStore)
	if err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	peerDirectoryChanges := services.NewPeerDirectoryBroadcaster()
	peerDirectoryPoll, pollErr := services.LoadSDKPeerDirectoryPollContract()
	if pollErr != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	controlPlane.PeerDirectoryPoll = peerDirectoryPoll
	controlPlane.PeerDirectoryTTL = time.Duration(controlPlane.ReplicaLifecycle.Lease.TTLSeconds) * time.Second
	controlPlane.PeerDirectoryRules = func(callerInstanceID string) []models.PeerLinkRule {
		return linkPolicySnapshot.View().Rules(callerInstanceID)
	}
	controlPlane.PeerDirectoryChanges = peerDirectoryChanges.Subscribe
	controlPlane.ReplicaDirectory.SetChangeNotifier(peerDirectoryChanges.Notify)
	observations := recordReplicaObservation{
		store:       replicaObservations,
		convergence: convergenceSnapshot,
		unavail:     inventory.management.Codes.ServiceUnavailable,
		refused:     inventory.management.Codes.ActivationFailed,
	}
	applier := &services.SDKConfigurationApplier{
		Store:        stores.serviceConfigStore,
		Registered:   controlPlane.RegisteredReloads,
		Snapshot:     configurationSnapshot,
		Convergence:  convergenceSnapshot,
		Observations: observations,
	}
	serviceConfigurationService := &application.ServiceConfigurationService{
		Store: stores.serviceConfigStore, Applier: applier,
		Operations:            application.OperationService{Store: stores.operationStore},
		Unavailable:           inventory.management.Codes.ManagementUnavailable,
		OperationKind:         inventory.management.OperationKinds.ServiceSettingsApply,
		RollbackOperationKind: inventory.management.OperationKinds.ServiceSettingsRollback,
		PayloadVersion:        stores.serviceConfigWords.SchemaVersion, MaximumPayloadBytes: stores.serviceConfigWords.MaximumPayloadBytes,
		PayloadFailureCode: inventory.management.Codes.ActivationFailed,
		OperationStates: application.ServiceConfigurationOperationStates{
			Pending: inventory.management.Statuses.Pending, Running: inventory.management.Statuses.Running,
			Succeeded: inventory.management.Statuses.Succeeded, Failed: inventory.management.Statuses.Failed,
		},
		OperationFailureCodes: application.ServiceConfigurationFailureCodes{
			Rejected:    inventory.management.Codes.ServiceConfigInvalid,
			Conflict:    inventory.management.Codes.ServiceRevisionConflict,
			Unavailable: inventory.management.Codes.ServiceUnavailable,
			ApplyFailed: inventory.management.Codes.ActivationFailed,
			TargetLost:  inventory.management.Codes.TargetLost,
		},
		RevisionStates: application.ServiceConfigurationRevisionStates{
			Active: stores.serviceConfigWords.Slots.Active, Candidate: stores.serviceConfigWords.Slots.Staging,
			Failed: "",
		},
		RecoveryAudit: application.ServiceConfigurationRecoveryAudit{
			CandidateAction: managementInputs.auditWords.Audit.Actions.ServiceSettingsCandidate,
			AppliedAction:   managementInputs.auditWords.Audit.Actions.ServiceSettingsApply,
			FailedAction:    managementInputs.auditWords.Audit.Actions.ServiceSettingsApplyFailed,
			Succeeded:       managementInputs.auditWords.Audit.Results.Succeeded,
			Failed:          managementInputs.auditWords.Audit.Results.Failed,
		},
	}
	trafficRollouts := &application.TrafficRolloutService{
		Store: stores.trafficRolloutStore, ConfigurationStore: stores.serviceConfigStore,
		ConfigurationRollouts: stores.serviceConfigStore, Applier: applier,
		Operations: application.OperationService{Store: stores.operationStore},
		Replicas:   applier, Validator: applier,
		States: application.TrafficRolloutStates{
			Running: inventory.management.Statuses.Running, Active: stores.serviceConfigWords.Slots.Active,
			Pending: inventory.management.Statuses.Pending, Completed: inventory.management.Statuses.Completed,
		},
		OperationStates: application.ServiceConfigurationOperationStates{
			Pending: inventory.management.Statuses.Pending, Running: inventory.management.Statuses.Running,
			Failed: inventory.management.Statuses.Failed,
		},
		TargetLostCode: inventory.management.Codes.TargetLost,
		SchemaVersion:  stores.serviceConfigWords.SchemaVersion, MaximumConfigurationBytes: stores.serviceConfigWords.MaximumPayloadBytes,
		WorkerContext: ctx, ScheduleWorker: func(worker func()) { go worker() },
	}
	applier.SkipRegisteredInstance = trafficRollouts.ConfigurationCohortHeld
	// Do not synchronously apply the currently active generation inside the
	// Service SDK registration request. Registration must succeed even when an
	// existing generation is rejected: Core still needs the live replica to
	// expose its SDK metadata so the operator can repair the desired settings.
	// The registered-replica reconciler below retries the exact active generation
	// independently, while a new Studio runtime start submits its Project bundle
	// through the Management API after Core-backed metadata inspection.
	management := newManagementServer(managementServerDependencies{
		stores: stores, inventory: inventory, managementInputs: managementInputs,
		convergence: convergenceSnapshot, linkPolicy: linkPolicySnapshot, serviceControl: controlPlane,
		trafficRollouts: trafficRollouts, linkPolicyChanged: peerDirectoryChanges.Notify,
	})
	management.CoreSettings = coreSettings
	management.ValidateSettings = func(raw []byte) error { _, err := config.DecodeSettings(raw); return err }
	management.ConfigBundles = &application.ConfigBundleService{
		Configurations:  serviceConfigurationService,
		Operations:      application.OperationService{Store: stores.operationStore},
		OperationKind:   inventory.management.OperationKinds.ServiceSettingsApply,
		Pending:         inventory.management.Statuses.Pending,
		CandidateAction: managementInputs.auditWords.Audit.Actions.ServiceSettingsCandidate,
		ApplyAction:     managementInputs.auditWords.Audit.Actions.ServiceSettingsApply,
		FailedAction:    managementInputs.auditWords.Audit.Actions.ServiceSettingsApplyFailed,
		SucceededResult: managementInputs.auditWords.Audit.Results.Succeeded,
		FailedResult:    managementInputs.auditWords.Audit.Results.Failed,
	}
	if err := serviceConfigurationService.Recover(context.Background()); err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	if err := configurationSnapshot.RefreshAll(context.Background()); err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	if err := convergenceSnapshot.Refresh(context.Background()); err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	stopRESTControl, controlErr := startServiceRESTControl(controlPlane, configurationSnapshot, stores.serviceConfigStore, stores.serviceSecretStore, bootstrap.SecretRoot)
	if controlErr != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	defer stopRESTControl()
	// Reconciliation at startup restores exact generations for live leases.
	_ = applier.ReconcileRegisteredReplicas(context.Background())
	reconciliationPolicy, policyErr := config.LoadServiceReconciliationPolicy()
	if policyErr != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	stopRegisteredReplicaReconciler := startRegisteredReplicaReconciler(ctx, reconciliationPolicy.ReadinessPollInterval(), applier, func(recoveryContext context.Context) error {
		if err := serviceConfigurationService.RecoverRegistered(recoveryContext, controlPlane.ReplicaDirectory.HasRegisteredInstance); err != nil {
			return err
		}
		for _, instanceID := range controlPlane.ReplicaDirectory.RegisteredInstanceIDs() {
			_ = trafficRollouts.ReconcileInstance(recoveryContext, instanceID)
		}
		return nil
	})
	defer stopRegisteredReplicaReconciler()
	stopTrafficController, trafficControllerDone, controllerErr := startTrafficController(options.trafficController, trafficRollouts, managementInputs.trafficRolloutAPI)
	if controllerErr != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	defer stopTrafficController()
	if err := serveManagementAndTrafficController(ctx, management, bootstrap.ManagementListen, serviceConfigurationService, ready,
		stopTrafficController, trafficControllerDone); err != nil && !errors.Is(err, net.ErrClosed) {
		options.writeFailure(options.output, options.words.Exits.Unavailable, options.words.Codes.ConfigInvalid, options.words.Diagnostics.ConfigInvalid)
		return options.words.Exits.Unavailable
	}
	return options.words.Exits.OK
}

// bootstrapManagementKey establishes the first local Management API
// credential without putting its value in SQLite, the target profile, or the
// project. The CLI supplies it through a one-time file for containerized Core
// or through CORE_INIT_MANAGEMENT_TOKEN for a host process. Existing service
// keys always win, so restarting Core never rotates operator credentials.
func bootstrapManagementKey(ctx context.Context, store *storage.SQLiteServiceKeyStore, words config.RuntimeWords) error {
	if store == nil {
		return errors.New("service key store is required")
	}
	verifiers, err := store.ActiveVerifiers(ctx)
	if err != nil {
		return err
	}
	if len(verifiers) > 0 {
		discardBootstrapManagementToken()
		return nil
	}
	token, err := takeBootstrapManagementToken()
	if err != nil {
		return err
	}
	if token == "" {
		return nil
	}
	if words.ServiceKey.HashCost < 1 || words.ServiceKey.RolePlatformAdmin == "" {
		return errors.New("service key bootstrap contract is invalid")
	}
	verifier, err := security.HashServiceKey(token, words.ServiceKey.HashCost)
	if err != nil {
		return err
	}
	defer clear(verifier)
	return store.Bootstrap(ctx, "local-bootstrap", verifier, words.ServiceKey.RolePlatformAdmin)
}

// discardBootstrapManagementToken removes one-time bootstrap material when
// an initialized Core already has an active management verifier. Container
// restarts retain the original environment, but the one-time file itself has
// intentionally already been unlinked.
func discardBootstrapManagementToken() {
	path := os.Getenv("CORE_INIT_MANAGEMENT_TOKEN_FILE")
	_ = os.Unsetenv("CORE_INIT_MANAGEMENT_TOKEN_FILE")
	_ = os.Unsetenv("CORE_INIT_MANAGEMENT_TOKEN")
	if path != "" && filepath.IsAbs(path) {
		_ = os.Remove(path)
	}
}

// takeBootstrapManagementToken removes the bootstrap value from the process
// environment before it is used. The Core process never retains the token as
// an environment variable after initial state creation.
func takeBootstrapManagementToken() (string, error) {
	file := os.Getenv("CORE_INIT_MANAGEMENT_TOKEN_FILE")
	_ = os.Unsetenv("CORE_INIT_MANAGEMENT_TOKEN_FILE")
	if file != "" {
		return consumeBootstrapTokenFile(file)
	}
	token := os.Getenv("CORE_INIT_MANAGEMENT_TOKEN")
	_ = os.Unsetenv("CORE_INIT_MANAGEMENT_TOKEN")
	return token, nil
}

// consumeBootstrapTokenFile accepts only a regular file and unlinks it before
// returning the token to the caller. The containing directory is a private,
// short-lived mount created by the CLI; the file path itself is never logged.
func consumeBootstrapTokenFile(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("bootstrap token file path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("bootstrap token file is unavailable")
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("bootstrap token file is unavailable")
	}
	if err := os.Remove(path); err != nil {
		clear(value)
		return "", errors.New("bootstrap token file could not be consumed")
	}
	token := strings.TrimSpace(string(value))
	clear(value)
	if token == "" {
		return "", errors.New("bootstrap token file is empty")
	}
	return token, nil
}

func failBootstrap(options runContext, exitCode int, detail string) int {
	options.writeFailure(options.output, exitCode, options.words.Codes.ConfigInvalid, detail)
	return exitCode
}

func loadBootstrapStores(options runContext, bootstrap config.BootstrapConfig, database *sql.DB) (bootstrapStores, int) {
	var stores bootstrapStores
	var err error
	if stores.auditStore, err = storage.NewSQLiteAuditStore(database); err != nil {
		return stores, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	if stores.keyStore, err = storage.NewSQLiteServiceKeyStore(database); err != nil {
		return stores, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	if stores.operationStore, err = storage.NewSQLiteOperationStore(database); err != nil {
		return stores, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	if stores.serviceConfigStore, err = storage.NewSQLiteServiceConfigurationStore(database); err != nil {
		return stores, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	if stores.serviceSecretStore, err = storage.NewSQLiteServiceSecretReferenceStore(database); err != nil {
		return stores, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	if stores.serviceConfigWords, err = config.LoadServiceConfiguration(); err != nil {
		return stores, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	if stores.serviceLinkStore, err = storage.NewSQLiteServiceLinkPolicyStore(database); err != nil {
		return stores, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	if stores.trafficRolloutStore, err = storage.NewSQLiteTrafficRolloutStore(database); err != nil {
		return stores, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	return stores, options.words.Exits.OK
}

func loadBootstrapInventory(options runContext, database *sql.DB) (bootstrapInventory, int) {
	var inventory bootstrapInventory
	var err error
	if inventory.management, err = config.LoadManagement(); err != nil {
		return inventory, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	if inventory.contract, err = config.LoadServiceInventoryContract(); err != nil {
		return inventory, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	inventory.records, err = storage.ListServiceInstances(context.Background(), database, storage.ServiceInstanceQuery{
		SelectInstances: inventory.contract.SelectInstances,
		ValidStates:     inventory.contract.ValidStates,
		InvalidContract: inventory.contract.Diagnostics.InvalidContract,
		InvalidRecord:   inventory.contract.Diagnostics.InvalidRecord,
	})
	if err != nil {
		return inventory, failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	if inventory.view, err = PresentServiceInventory(inventory.records, inventory.contract); err != nil {
		return inventory, failBootstrap(options, options.words.Exits.Validation, options.words.Diagnostics.ConfigInvalid)
	}
	return inventory, options.words.Exits.OK
}

func loadManagementInputs(options runContext, bootstrapConfig config.BootstrapConfig) (managementInputs, int) {
	var inputs managementInputs
	var err error
	if inputs.auditWords, err = config.LoadAudit(); err != nil {
		return inputs, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	if inputs.errorCatalog, err = config.LoadErrorCatalog(); err != nil {
		return inputs, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	if inputs.trafficRolloutAPI, err = config.LoadTrafficRolloutAPIContract(); err != nil {
		return inputs, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	if inputs.tlsConfiguration, err = ManagementTLS(bootstrapConfig, options.words.Diagnostics.ConfigInvalid); err != nil {
		return inputs, failBootstrap(options, options.words.Exits.Validation, options.words.Diagnostics.ConfigInvalid)
	}
	return inputs, options.words.Exits.OK
}

type managementServerDependencies struct {
	stores            bootstrapStores
	inventory         bootstrapInventory
	managementInputs  managementInputs
	convergence       *storage.ServiceConvergenceSnapshot
	linkPolicy        *storage.ServiceLinkPolicySnapshot
	linkPolicyChanged func()
	serviceControl    *ServiceRESTControl
	trafficRollouts   *application.TrafficRolloutService
}

func newManagementServer(dependencies managementServerDependencies) *api.Server {
	stores, inventory, inputs := dependencies.stores, dependencies.inventory, dependencies.managementInputs
	management := inventory.management
	audit := inputs.auditWords
	return &api.Server{
		Operations:        application.OperationService{Store: stores.operationStore},
		TrafficRolloutAPI: inputs.trafficRolloutAPI,
		TrafficRollouts:   dependencies.trafficRollouts,
		ServiceAdminControl: &services.SDKAdminControl{
			Instances: func(ctx context.Context) ([]string, error) {
				seen := make(map[string]struct{})
				view, err := dependencies.convergence.Current()
				if err != nil {
					return nil, err
				}
				for instanceID := range view.Desired {
					seen[instanceID] = struct{}{}
				}
				for _, live := range dependencies.serviceControl.RegisteredReloads.Source.Snapshot() {
					seen[live.Registration.Identity.InstanceID] = struct{}{}
				}
				instances := make([]string, 0, len(seen))
				for instanceID := range seen {
					instances = append(instances, instanceID)
				}
				return instances, ctx.Err()
			},
			ResolveFanout: func(ctx context.Context, instanceID string) (*services.SDKReloadFanout, func(), bool, error) {
				client, release, found, err := dependencies.serviceControl.RegisteredReloads.Resolve(ctx, instanceID)
				if err != nil || !found {
					return nil, release, found, err
				}
				fanout, ok := client.(*services.SDKReloadFanout)
				if !ok {
					if release != nil {
						release()
					}
					return nil, nil, true, services.ErrServiceUnavailable
				}
				return fanout, release, true, nil
			},
			HTTPContract: dependencies.serviceControl.HTTPContract,
			EligibleReplicaIDs: func(ctx context.Context, instanceID string) ([]string, error) {
				view, err := dependencies.convergence.Current()
				if err != nil {
					return nil, err
				}
				generation, active := view.Desired[instanceID]
				if !active || generation < 1 {
					return nil, nil
				}
				incarnations := make(map[string]string)
				for _, live := range dependencies.serviceControl.RegisteredReloads.Source.Snapshot() {
					identity := live.Registration.Identity
					if identity.InstanceID == instanceID {
						incarnations[identity.ReplicaID] = identity.IncarnationID
					}
				}
				eligible := make([]string, 0)
				for _, observation := range view.Records {
					if observation.InstanceID == instanceID && observation.ObservedState == storage.ReplicaObservedAcknowledged &&
						observation.ObservedGeneration.Valid && observation.ObservedGeneration.Int64 == generation &&
						incarnations[observation.ReplicaID] != "" && observation.IncarnationID == incarnations[observation.ReplicaID] {
						eligible = append(eligible, observation.ReplicaID)
					}
				}
				return eligible, nil
			},
		},
		Audit: &application.AuditService{
			Store: stores.auditStore, RetentionDays: audit.Audit.RetentionDays,
			MinimumLimit: management.Pagination.LimitMin, DefaultLimit: management.Pagination.LimitDefault,
			MaximumLimit: management.Pagination.LimitMax, InvalidLimit: audit.Audit.InvalidLimit,
		},
		AccessService: &application.AccessService{Store: stores.keyStore, Compare: security.CompareServiceKey},
		AuditWords:    audit, Management: management, Errors: inputs.errorCatalog,
		ServiceLinks: &application.ServiceLinkPolicyService{
			Store: stores.serviceLinkStore, Reader: stores.serviceLinkStore, Operations: stores.operationStore,
			Kinds: application.ServiceLinkPolicyOperationKinds{
				Create:  management.OperationKinds.ServiceLinkCreate,
				Replace: management.OperationKinds.ServiceLinkReplace,
				Delete:  management.OperationKinds.ServiceLinkDelete,
			},
			States: application.ServiceLinkPolicyOperationStates{
				Pending: management.Statuses.Pending, Succeeded: management.Statuses.Succeeded, Failed: management.Statuses.Failed,
			},
			Now: time.Now,
			Refresh: func() {
				if dependencies.linkPolicy != nil {
					_ = dependencies.linkPolicy.Refresh(context.Background())
				}
				if dependencies.linkPolicyChanged != nil {
					dependencies.linkPolicyChanged()
				}
			},
		},
		ServiceSecretReferences: &application.ServiceSecretReferenceService{Store: stores.serviceSecretStore},
		ServiceMetadata: func(ctx context.Context, instanceID string) (models.ServiceMetadata, error) {
			metadata, err := dependencies.serviceControl.RegisteredReloads.Metadata(ctx, instanceID)
			if err != nil {
				return models.ServiceMetadata{}, err
			}
			convergence, err := dependencies.convergence.Current()
			if err != nil {
				return models.ServiceMetadata{}, err
			}
			var manifestIdentity struct {
				ID      string `json:"id"`
				Version string `json:"version"`
			}
			if json.Unmarshal(metadata.Manifest, &manifestIdentity) != nil ||
				manifestIdentity.ID != instanceID || manifestIdentity.Version != metadata.Release.Version {
				return models.ServiceMetadata{}, models.ErrServiceRegistrationMismatch
			}
			replicas := make([]models.ServiceReplicaRelease, 0, len(metadata.Replicas))
			for _, replica := range metadata.Replicas {
				if replica.IncarnationID == "" {
					return models.ServiceMetadata{}, services.ErrServiceUnavailable
				}
				replicas = append(replicas, models.ServiceReplicaRelease{
					ReplicaID: replica.ReplicaID, ArtifactVersion: replica.Release.Version,
					ArtifactDigest: "sha256:" + replica.Release.SHA256,
					Identity:       replica.Identity, Readiness: replica.Readiness,
					Configuration: serviceReplicaConfigurationStatus(convergence, instanceID, replica.ReplicaID, replica.IncarnationID),
				})
			}
			return models.ServiceMetadata{
				Manifest: metadata.Manifest, ConfigurationSchema: metadata.ConfigurationSchema,
				Identity: metadata.Identity, Readiness: metadata.Readiness,
				ReplicaID: metadata.ReplicaID, ArtifactVersion: metadata.Release.Version,
				ArtifactDigest: "sha256:" + metadata.Release.SHA256, Replicas: replicas,
			}, nil
		},
		RegisterService: func(ctx context.Context, registration models.ServiceRegistration) (models.ServiceRegistrationResult, error) {
			if err := dependencies.serviceControl.RegisteredReloads.ValidateRuntimeRegistration(ctx, registration); err != nil {
				return models.ServiceRegistrationResult{}, err
			}
			metadata, err := dependencies.serviceControl.RegisteredReloads.Metadata(ctx, registration.InstanceID)
			if err != nil {
				return models.ServiceRegistrationResult{}, err
			}
			var manifest sdkmodels.ManifestDescriptor
			if json.Unmarshal(metadata.Manifest, &manifest) != nil || manifest.Validate() != nil ||
				manifest.ID != registration.ArtifactID || manifest.Version != registration.ArtifactVersion ||
				metadata.Identity.InstanceID != registration.InstanceID || metadata.Identity.ReplicaID != registration.ReplicaID ||
				metadata.Readiness.InstanceID != registration.InstanceID || metadata.Readiness.ReplicaID != registration.ReplicaID {
				return models.ServiceRegistrationResult{}, models.ErrServiceRegistrationMismatch
			}
			return models.ServiceRegistrationResult{Accepted: true, InstanceID: registration.InstanceID, ReplicaID: registration.ReplicaID, Status: "registered"}, nil
		},
		TLSConfig: inputs.tlsConfiguration,
		Services:  inventory.view, ServiceIDField: inventory.contract.JSON.ID,
		DataPlaneReadiness: serviceReadiness(dependencies.serviceControl.ReplicaDirectory, dependencies.convergence, management),
		DataPlaneDrift:     serviceDrift(dependencies.serviceControl.ReplicaDirectory, dependencies.convergence),
	}
}
