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
	"github.com/Liapoldus/core/internal/domain/models"
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
}

type bootstrapInventory struct {
	management config.ManagementWords
	contract   config.PluginInventoryContract
	records    []storage.PluginInstanceRecord
	view       []any
}

type pluginLaunchSetup struct {
	contract        config.PluginRuntimeContract
	instances       map[string]models.PluginInstance
	runtimeSettings plugins.RuntimeSettings
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
	sqliteContract, err := config.LoadSQLiteContract()
	if err != nil {
		options.writeFailure(options.output, options.words.Exits.Internal, options.words.Codes.ConfigInvalid, options.words.Diagnostics.ConfigInvalid)
		return options.words.Exits.Internal
	}
	database, err := OpenDatabase(context.Background(), bootstrap.StatePath)
	if err != nil {
		options.writeFailure(options.output, options.words.Exits.Unavailable, options.words.Codes.ConfigInvalid, options.words.Diagnostics.ConfigInvalid)
		return options.words.Exits.Unavailable
	}
	defer database.Close()
	stores, exitCode := loadBootstrapStores(options, bootstrap, database, sqliteContract)
	if exitCode != options.words.Exits.OK {
		return exitCode
	}
	inventory, exitCode := loadBootstrapInventory(options, database)
	if exitCode != options.words.Exits.OK {
		return exitCode
	}
	pluginLaunch, exitCode := prepareLocalPlugins(options, inventory.records)
	if exitCode != options.words.Exits.OK {
		return exitCode
	}
	pluginRuntime, err := plugins.StartRuntime(context.Background(), pluginLaunch.instances, pluginLaunch.runtimeSettings)
	if err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, pluginLaunch.contract.Diagnostics.StartupFailed)
	}
	defer func() { _ = pluginRuntime.Stop(context.Background()) }()
	managementInputs, exitCode := loadManagementInputs(options, bootstrap)
	if exitCode != options.words.Exits.OK {
		return exitCode
	}
	management := newManagementServer(managementServerDependencies{
		stores: stores, inventory: inventory, managementInputs: managementInputs,
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pluginConfigurationService := &application.PluginConfigurationService{
		Store: stores.pluginConfigStore, Applier: pluginRuntime,
		Unavailable: inventory.management.Codes.ManagementUnavailable,
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

func loadBootstrapStores(options runContext, bootstrap config.BootstrapConfig, database *sql.DB, sqliteContract config.SQLiteContract) (bootstrapStores, int) {
	var stores bootstrapStores
	if err := os.MkdirAll(bootstrap.ArtifactsPath, os.FileMode(sqliteContract.ArtifactsDirectoryMode)); err != nil {
		return stores, failBootstrap(options, options.words.Exits.Unavailable, options.words.Diagnostics.ConfigInvalid)
	}
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

func prepareLocalPlugins(options runContext, records []storage.PluginInstanceRecord) (pluginLaunchSetup, int) {
	var setup pluginLaunchSetup
	var err error
	if setup.contract, err = config.LoadPluginRuntimeContract(); err != nil {
		return setup, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	launchSchema, err := config.LoadPluginLaunchSchema()
	if err != nil {
		return setup, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	fileReferencePrefix, err := config.LoadFileReferencePrefix()
	if err != nil {
		return setup, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	setup.runtimeSettings = plugins.RuntimeSettings{
		FileReferencePrefix: fileReferencePrefix,
		MaximumSecretBytes:  setup.contract.ConfigSecrets.MaximumBytes,
	}
	localRecords := make([]plugins.LocalInstanceRecord, 0, len(records))
	for _, record := range records {
		localRecords = append(localRecords, plugins.LocalInstanceRecord{
			ID: record.ID, Mode: record.Mode, Revision: record.Revision, LaunchJSON: record.LaunchJSON,
			SettingsJSON: record.SettingsJSON, ManifestJSON: record.ManifestJSON,
		})
	}
	setup.instances, err = plugins.BuildLocalInstances(localRecords, plugins.LocalRuntimeContract{
		LocalMode: setup.contract.Modes.Local, BinaryField: setup.contract.LaunchFields.Binary,
		MaximumSecretBytes: setup.contract.ConfigSecrets.MaximumBytes, SecretGrantPurpose: setup.contract.ConfigSecrets.GrantPurpose,
		FileReferencePrefix: fileReferencePrefix, LaunchSchema: launchSchema,
		CallTimeout: setup.contract.Defaults.CallTimeout, StartTimeout: setup.contract.Defaults.StartTimeout,
		MaxConcurrentCalls: setup.contract.Defaults.MaxConcurrentCalls, RestartEnabled: setup.contract.Defaults.RestartEnabled,
		RestartInitialBackoff:  setup.contract.Defaults.RestartInitialBackoff,
		RestartMaximumBackoff:  setup.contract.Defaults.RestartMaximumBackoff,
		HealthProbeInterval:    setup.contract.Defaults.HealthProbeInterval,
		HealthFailureThreshold: setup.contract.Defaults.HealthFailureThreshold,
		MemoryProbeInterval:    setup.contract.Defaults.MemoryProbeInterval,
		MemoryLimitBytes:       setup.contract.Defaults.MemoryLimitBytes,
		InvalidContract:        setup.contract.Diagnostics.InvalidContract,
		InvalidLaunch:          setup.contract.Diagnostics.InvalidLaunch,
	})
	if err != nil {
		return setup, failBootstrap(options, options.words.Exits.Validation, setup.contract.Diagnostics.InvalidLaunch)
	}
	return setup, options.words.Exits.OK
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
		DataPlaneState: management.Statuses.Ready,
	}
}
