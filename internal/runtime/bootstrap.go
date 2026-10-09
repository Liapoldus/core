package runtime

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/internal/infrastructure/security"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	settingsstore "github.com/Liapoldus/core/internal/infrastructure/storage/settings"
	"github.com/Liapoldus/core/internal/presentation/api"
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
	pluginConfigStore   *storage.SQLitePluginConfigurationStore
	pluginLinkStore     *storage.SQLitePluginLinkPolicyStore
	trafficRolloutStore *storage.SQLiteTrafficRolloutStore
	pluginConfigWords   config.PluginConfigurationWords
}

type bootstrapInventory struct {
	management config.ManagementWords
	contract   config.PluginInventoryContract
	records    []storage.PluginInstanceRecord
	view       []any
}

type managementInputs struct {
	auditWords        config.AuditWords
	errorCatalog      config.ErrorCatalog
	trafficRolloutAPI config.TrafficRolloutAPIContract
	tlsConfiguration  *tls.Config
}

func Serve(bootstrapConfig config.BootstrapConfig, input RunOptions) int {
	options := runContext{output: input.Output, words: input.Words, writeFailure: input.WriteFailure, trafficController: input.TrafficController}
	return serveBootstrap(options, bootstrapConfig)
}

func serveBootstrap(options runContext, bootstrap config.BootstrapConfig) int {
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
		_, err = pluginControlTLS(candidate)
		return err
	}); err != nil {
		return failBootstrap(options, options.words.Exits.Validation, options.words.Diagnostics.ConfigInvalid)
	}
	stores, exitCode := loadBootstrapStores(options, bootstrap, database)
	if exitCode != options.words.Exits.OK {
		return exitCode
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
	controlPlane, err := buildPluginRESTControl(bootstrap)
	if err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	if len(inventory.contract.ValidStates) == 0 {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	registeredInstances, err := storage.ListRegisteredPluginInstances(context.Background(), database)
	if err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	controlPlane.ReplicaDirectory.MarkRegisteredInstances(registeredInstances)
	controlPlane.RegisterReplica = func(ctx context.Context, registration plugins.SDKReplicaRegistrationRequest) error {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		return storage.RegisterPluginInstanceReplica(ctx, database,
			registration.Identity.InstanceID, registration.Identity.ReplicaID, []byte("{}"),
			inventory.contract.ValidStates[0], storage.ReplicaObservedPending, now)
	}
	instanceIDs := make([]string, 0, len(inventory.records))
	for _, record := range inventory.records {
		instanceIDs = append(instanceIDs, record.ID)
	}
	configurationSnapshot, err := storage.NewPluginConfigurationSnapshot(context.Background(), stores.pluginConfigStore, instanceIDs)
	if err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	replicaObservations := storage.NewPluginReplicaStore(database, options.words.Diagnostics.ConfigInvalid)
	convergenceSnapshot, err := storage.NewPluginConvergenceSnapshot(context.Background(), stores.pluginConfigStore, replicaObservations, instanceIDs)
	if err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	linkPolicySnapshot, err := storage.NewPluginLinkPolicySnapshot(context.Background(), stores.pluginLinkStore)
	if err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	peerDirectoryChanges := plugins.NewPeerDirectoryBroadcaster()
	peerDirectoryPoll, pollErr := plugins.LoadSDKPeerDirectoryPollContract()
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
		unavail:     inventory.management.Codes.PluginUnavailable,
		refused:     inventory.management.Codes.ActivationFailed,
	}
	applier := &plugins.SDKConfigurationApplier{
		Store:        stores.pluginConfigStore,
		Registered:   controlPlane.RegisteredReloads,
		Snapshot:     configurationSnapshot,
		Convergence:  convergenceSnapshot,
		Observations: observations,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pluginConfigurationService := &application.PluginConfigurationService{
		Store: stores.pluginConfigStore, Applier: applier,
		Operations:            application.OperationService{Store: stores.operationStore},
		Unavailable:           inventory.management.Codes.ManagementUnavailable,
		OperationKind:         inventory.management.OperationKinds.PluginSettingsApply,
		RollbackOperationKind: inventory.management.OperationKinds.PluginSettingsRollback,
		PayloadVersion:        stores.pluginConfigWords.SchemaVersion, MaximumPayloadBytes: stores.pluginConfigWords.MaximumPayloadBytes,
		PayloadFailureCode: inventory.management.Codes.ActivationFailed,
		OperationStates: application.PluginConfigurationOperationStates{
			Pending: inventory.management.Statuses.Pending, Running: inventory.management.Statuses.Running,
			Succeeded: inventory.management.Statuses.Succeeded, Failed: inventory.management.Statuses.Failed,
		},
		OperationFailureCodes: application.PluginConfigurationFailureCodes{
			Rejected:    inventory.management.Codes.PluginConfigInvalid,
			Conflict:    inventory.management.Codes.PluginRevisionConflict,
			Unavailable: inventory.management.Codes.PluginUnavailable,
			ApplyFailed: inventory.management.Codes.ActivationFailed,
			TargetLost:  inventory.management.Codes.TargetLost,
		},
		RevisionStates: application.PluginConfigurationRevisionStates{
			Active: stores.pluginConfigWords.Slots.Active, Candidate: stores.pluginConfigWords.Slots.Staging,
			Failed: "",
		},
		RecoveryAudit: application.PluginConfigurationRecoveryAudit{
			CandidateAction: managementInputs.auditWords.Audit.Actions.PluginSettingsCandidate,
			AppliedAction:   managementInputs.auditWords.Audit.Actions.PluginSettingsApply,
			FailedAction:    managementInputs.auditWords.Audit.Actions.PluginSettingsApplyFailed,
			Succeeded:       managementInputs.auditWords.Audit.Results.Succeeded,
			Failed:          managementInputs.auditWords.Audit.Results.Failed,
		},
	}
	trafficRollouts := &application.TrafficRolloutService{
		Store: stores.trafficRolloutStore, ConfigurationStore: stores.pluginConfigStore,
		ConfigurationRollouts: stores.pluginConfigStore, Applier: applier,
		Operations: application.OperationService{Store: stores.operationStore},
		Replicas:   controlPlane.RegisteredReloads, Validator: controlPlane.RegisteredReloads,
		States: application.TrafficRolloutStates{
			Running: inventory.management.Statuses.Running, Active: stores.pluginConfigWords.Slots.Active,
			Pending: inventory.management.Statuses.Pending, Completed: inventory.management.Statuses.Completed,
		},
		OperationStates: application.PluginConfigurationOperationStates{
			Pending: inventory.management.Statuses.Pending, Running: inventory.management.Statuses.Running,
			Failed: inventory.management.Statuses.Failed,
		},
		TargetLostCode: inventory.management.Codes.TargetLost,
		SchemaVersion:  stores.pluginConfigWords.SchemaVersion, MaximumConfigurationBytes: stores.pluginConfigWords.MaximumPayloadBytes,
		WorkerContext: ctx, ScheduleWorker: func(worker func()) { go worker() },
	}
	applier.SkipRegisteredInstance = trafficRollouts.ConfigurationCohortHeld
	controlPlane.OnReplicaRegistered = func(registrationContext context.Context, instanceID, replicaID string) error {
		if err := applier.ReloadActiveReplica(registrationContext, instanceID, replicaID); err != nil {
			return err
		}
		_ = trafficRollouts.ReconcileInstance(registrationContext, instanceID)
		return nil
	}
	management := newManagementServer(managementServerDependencies{
		stores: stores, inventory: inventory, managementInputs: managementInputs,
		convergence: convergenceSnapshot, linkPolicy: linkPolicySnapshot, pluginControl: controlPlane,
		trafficRollouts: trafficRollouts, linkPolicyChanged: peerDirectoryChanges.Notify,
	})
	management.CoreSettings = coreSettings
	management.ValidateSettings = func(raw []byte) error { _, err := config.DecodeSettings(raw); return err }
	if err := pluginConfigurationService.Recover(context.Background()); err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	if err := configurationSnapshot.RefreshAll(context.Background()); err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	if err := convergenceSnapshot.Refresh(context.Background()); err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	stopRESTControl, controlErr := startPluginRESTControl(controlPlane, configurationSnapshot, bootstrap.SourcePath)
	if controlErr != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	defer stopRESTControl()
	// Reconciliation at startup restores exact generations for live leases.
	_ = applier.ReconcileRegisteredReplicas(context.Background())
	reconciliationPolicy, policyErr := config.LoadPluginReconciliationPolicy()
	if policyErr != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	stopRegisteredReplicaReconciler := startRegisteredReplicaReconciler(ctx, reconciliationPolicy.ReadinessPollInterval(), applier, func(recoveryContext context.Context) error {
		if err := pluginConfigurationService.RecoverRegistered(recoveryContext, controlPlane.ReplicaDirectory.HasRegisteredInstance); err != nil {
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
	if err := serveManagementAndTrafficController(ctx, management, bootstrap.ManagementListen, pluginConfigurationService,
		stopTrafficController, trafficControllerDone); err != nil && !errors.Is(err, net.ErrClosed) {
		options.writeFailure(options.output, options.words.Exits.Unavailable, options.words.Codes.ConfigInvalid, options.words.Diagnostics.ConfigInvalid)
		return options.words.Exits.Unavailable
	}
	return options.words.Exits.OK
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
	if stores.pluginConfigStore, err = storage.NewSQLitePluginConfigurationStore(database); err != nil {
		return stores, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	if stores.pluginConfigWords, err = config.LoadPluginConfiguration(); err != nil {
		return stores, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	if stores.pluginLinkStore, err = storage.NewSQLitePluginLinkPolicyStore(database); err != nil {
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
	if inventory.contract, err = config.LoadPluginInventoryContract(); err != nil {
		return inventory, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	inventory.records, err = storage.ListPluginInstances(context.Background(), database, storage.PluginInstanceQuery{
		SelectInstances: inventory.contract.SelectInstances,
		ValidStates:     inventory.contract.ValidStates,
		InvalidContract: inventory.contract.Diagnostics.InvalidContract,
		InvalidRecord:   inventory.contract.Diagnostics.InvalidRecord,
	})
	if err != nil {
		return inventory, failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	if inventory.view, err = PresentPluginInventory(inventory.records, inventory.contract); err != nil {
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
	convergence       *storage.PluginConvergenceSnapshot
	linkPolicy        *storage.PluginLinkPolicySnapshot
	linkPolicyChanged func()
	pluginControl     *PluginRESTControl
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
		PluginAdminControl: &plugins.SDKAdminControl{
			Instances: func(ctx context.Context) ([]string, error) {
				seen := make(map[string]struct{})
				view, err := dependencies.convergence.Current()
				if err != nil {
					return nil, err
				}
				for instanceID := range view.Desired {
					seen[instanceID] = struct{}{}
				}
				for _, live := range dependencies.pluginControl.RegisteredReloads.Source.Snapshot() {
					seen[live.Registration.Identity.InstanceID] = struct{}{}
				}
				instances := make([]string, 0, len(seen))
				for instanceID := range seen {
					instances = append(instances, instanceID)
				}
				return instances, ctx.Err()
			},
			ResolveFanout: func(ctx context.Context, instanceID string) (*plugins.SDKReloadFanout, func(), bool, error) {
				client, release, found, err := dependencies.pluginControl.RegisteredReloads.Resolve(ctx, instanceID)
				if err != nil || !found {
					return nil, release, found, err
				}
				fanout, ok := client.(*plugins.SDKReloadFanout)
				if !ok {
					if release != nil {
						release()
					}
					return nil, nil, true, plugins.ErrPluginUnavailable
				}
				return fanout, release, true, nil
			},
			HTTPContract: dependencies.pluginControl.HTTPContract,
			EligibleReplicaIDs: func(ctx context.Context, instanceID string) ([]string, error) {
				view, err := dependencies.convergence.Current()
				if err != nil {
					return nil, err
				}
				generation, active := view.Desired[instanceID]
				if !active || generation < 1 {
					return nil, nil
				}
				eligible := make([]string, 0)
				for _, observation := range view.Records {
					if observation.InstanceID == instanceID && observation.ObservedState == storage.ReplicaObservedAcknowledged &&
						observation.ObservedGeneration.Valid && observation.ObservedGeneration.Int64 == generation {
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
		PluginLinks: &application.PluginLinkPolicyService{
			Store: stores.pluginLinkStore, Reader: stores.pluginLinkStore, Operations: stores.operationStore,
			Kinds: application.PluginLinkPolicyOperationKinds{
				Create:  management.OperationKinds.PluginLinkCreate,
				Replace: management.OperationKinds.PluginLinkReplace,
				Delete:  management.OperationKinds.PluginLinkDelete,
			},
			States: application.PluginLinkPolicyOperationStates{
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
		TLSConfig: inputs.tlsConfiguration,
		Plugins:   inventory.view, PluginIDField: inventory.contract.JSON.ID,
		DataPlaneReadiness: pluginReadiness(dependencies.pluginControl.ReplicaDirectory, dependencies.convergence, management),
		DataPlaneDrift:     pluginDrift(dependencies.pluginControl.ReplicaDirectory, dependencies.convergence),
	}
}
