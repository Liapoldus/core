package bootstrap

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/internal/infrastructure/security"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/api"
)

type RunOptions struct {
	Output       string
	Words        config.CLIWords
	WriteFailure func(output string, exitCode int, code, detail string)
}

type runContext struct {
	output       string
	words        config.CLIWords
	writeFailure func(output string, exitCode int, code, detail string)
}

type bootstrapStores struct {
	auditStore        *storage.SQLiteAuditStore
	keyStore          *storage.SQLiteServiceKeyStore
	operationStore    *storage.SQLiteOperationStore
	pluginConfigStore *storage.SQLitePluginConfigurationStore
	pluginConfigWords config.PluginConfigurationWords
}

type bootstrapInventory struct {
	management config.ManagementWords
	contract   config.PluginInventoryContract
	records    []storage.PluginInstanceRecord
	view       []any
}

type managementInputs struct {
	auditWords       config.AuditWords
	errorCatalog     config.ErrorCatalog
	tlsConfiguration *tls.Config
}

func Serve(bootstrapConfig config.BootstrapConfig, input RunOptions) int {
	options := runContext{output: input.Output, words: input.Words, writeFailure: input.WriteFailure}
	return serveBootstrap(options, bootstrapConfig)
}

func serveBootstrap(options runContext, bootstrap config.BootstrapConfig) int {
	database, err := OpenDatabase(context.Background(), bootstrap.StatePath)
	if err != nil {
		options.writeFailure(options.output, options.words.Exits.Unavailable, options.words.Codes.ConfigInvalid, options.words.Diagnostics.ConfigInvalid)
		return options.words.Exits.Unavailable
	}
	defer database.Close()
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
	// v1 connects to the operator-declared, already-running plugin replicas
	// declared in core.yaml over the Plugin SDK REST API. The control plane is
	// always built from the bootstrap document: a Core that accepted core.yaml
	// and then silently dropped pluginControl would promote generations that no
	// replica can ever fetch, so an unusable control plane fails startup instead.
	controlPlane, err := buildPluginRESTControl(bootstrap)
	if err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	registry, err := newPluginRegistry(bootstrap)
	if err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	if err := registerDeclaredPlugins(context.Background(), database, registry); err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	replicaObservations := storage.NewPluginReplicaStore(database, options.words.Diagnostics.ConfigInvalid)
	applier := &plugins.SDKConfigurationApplier{
		Store:   stores.pluginConfigStore,
		Clients: controlPlane.ReloadClients,
		Observations: recordReplicaObservation{
			store:   replicaObservations,
			unavail: inventory.management.Codes.PluginUnavailable,
			refused: inventory.management.Codes.ActivationFailed,
		},
	}
	stopRESTControl, controlErr := startPluginRESTControl(controlPlane, stores.pluginConfigStore)
	if controlErr != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	defer stopRESTControl()
	management := newManagementServer(managementServerDependencies{
		stores: stores, inventory: inventory, managementInputs: managementInputs,
		registry: registry, replicas: replicaObservations,
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pluginConfigurationService := &application.PluginConfigurationService{
		Store: stores.pluginConfigStore, Applier: applier,
		Operations:     application.OperationService{Store: stores.operationStore},
		Unavailable:    inventory.management.Codes.ManagementUnavailable,
		OperationKind:  inventory.management.OperationKinds.PluginSettingsApply,
		PayloadVersion: stores.pluginConfigWords.SchemaVersion, MaximumPayloadBytes: stores.pluginConfigWords.MaximumPayloadBytes,
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
	if err := pluginConfigurationService.Recover(context.Background()); err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
	if err := management.Listen(ctx, bootstrap.ManagementListen, pluginConfigurationService); err != nil && !errors.Is(err, net.ErrClosed) {
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
		ValidModes:      inventory.contract.ValidModes,
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
	if inputs.tlsConfiguration, err = ManagementTLS(bootstrapConfig, options.words.Diagnostics.ConfigInvalid); err != nil {
		return inputs, failBootstrap(options, options.words.Exits.Validation, options.words.Diagnostics.ConfigInvalid)
	}
	return inputs, options.words.Exits.OK
}

type managementServerDependencies struct {
	stores           bootstrapStores
	inventory        bootstrapInventory
	managementInputs managementInputs
	registry         pluginRegistry
	replicas         *storage.PluginReplicaStore
}

func newManagementServer(dependencies managementServerDependencies) *api.Server {
	stores, inventory, inputs := dependencies.stores, dependencies.inventory, dependencies.managementInputs
	management := inventory.management
	audit := inputs.auditWords
	return &api.Server{
		Operations: application.OperationService{Store: stores.operationStore},
		Audit: &application.AuditService{
			Store: stores.auditStore, RetentionDays: audit.Audit.RetentionDays,
			MinimumLimit: management.Pagination.LimitMin, DefaultLimit: management.Pagination.LimitDefault,
			MaximumLimit: management.Pagination.LimitMax, InvalidLimit: audit.Audit.InvalidLimit,
		},
		AccessService: &application.AccessService{Store: stores.keyStore, Compare: security.CompareServiceKey},
		AuditWords:    audit, Management: management, Errors: inputs.errorCatalog,
		TLSConfig: inputs.tlsConfiguration,
		Plugins:   inventory.view, PluginIDField: inventory.contract.JSON.ID,
		DataPlaneReadiness: pluginReadiness(dependencies.registry, stores.pluginConfigStore, dependencies.replicas, management),
		DataPlaneDrift:     pluginDrift(dependencies.registry, stores.pluginConfigStore, dependencies.replicas),
	}
}
