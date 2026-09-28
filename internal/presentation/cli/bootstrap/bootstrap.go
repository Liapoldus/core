package bootstrap

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/artifacts"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/internal/infrastructure/security"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/api"
	"github.com/Liapoldus/core/internal/presentation/cli/caddyruntime"
)

type RunOptions struct {
	Output          string
	Words           config.CLIWords
	RuntimeBindings caddyruntime.RuntimeBindings
	WriteFailure    func(output string, exitCode int, code, detail string)
}

type runContext struct {
	output       string
	words        config.CLIWords
	writeFailure func(output string, exitCode int, code, detail string)
}

type bootstrapStores struct {
	groupStore        *storage.SQLiteGroupStore
	auditStore        *storage.SQLiteAuditStore
	keyStore          *storage.SQLiteServiceKeyStore
	operationStore    *storage.SQLiteOperationStore
	pluginConfigStore *storage.SQLitePluginConfigurationStore
	adminWords        config.AdminMutationWords
	checkpointStore   *storage.SQLiteCaddyCheckpointStore
	checkpointFiles   *artifacts.CaddyCheckpointArtifacts
	releaseStore      *storage.SQLiteGroupReleaseStore
	releasePolicy     models.GroupReleasePolicy
	releaseArtifacts  artifacts.GroupReleaseArtifacts
}

type bootstrapInventory struct {
	management        config.ManagementWords
	contract          config.PluginInventoryContract
	records           []storage.PluginInstanceRecord
	cookiePolicyStore *storage.SQLitePluginCookiePolicyStore
	cookiePolicies    []models.PluginCookiePolicy
	view              []any
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
	return serveBootstrap(options, bootstrapConfig, input.RuntimeBindings)
}

func serveBootstrap(options runContext, bootstrap config.BootstrapConfig, runtimeBindings caddyruntime.RuntimeBindings) int {
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
	pluginRuntime, err := plugins.StartRuntime(context.Background(), pluginLaunch.instances, nil, pluginLaunch.runtimeSettings)
	if err != nil {
		return failBootstrap(options, options.words.Exits.Unavailable, pluginLaunch.contract.Diagnostics.StartupFailed)
	}
	defer func() { _ = pluginRuntime.Stop(context.Background()) }()
	pluginBindings, exitCode := buildPluginDispatchBindings(options, pluginRuntime, inventory.cookiePolicies, inventory.management)
	if exitCode != options.words.Exits.OK {
		return exitCode
	}
	managementInputs, exitCode := loadManagementInputs(options, bootstrap)
	if exitCode != options.words.Exits.OK {
		return exitCode
	}
	readiness, reason, stopRuntime, caddyRuntime := systemDataPlane(stores.groupStore, bootstrap, inventory.management, runtimeBindings, pluginBindings)
	adminMutationService := NewAdminMutationService(caddyRuntime, stores.checkpointStore, stores.checkpointFiles, stores.adminWords)
	if stopRuntime != nil {
		defer stopRuntime()
	}
	groupReleaseService := NewGroupReleaseService(bootstrap.ArtifactsPath, stores.groupStore, stores.releaseStore, stores.releaseArtifacts, stores.releasePolicy, caddyRuntime, adminMutationService)
	readiness, reason = ActivateCurrentGroupRelease(groupReleaseService, caddyRuntime, inventory.management, readiness, reason)
	management := newManagementServer(managementServerDependencies{
		bootstrap: bootstrap, runtimeBindings: runtimeBindings, stores: stores,
		inventory: inventory, managementInputs: managementInputs,
		groupReleaseService: groupReleaseService, adminMutationService: adminMutationService,
		caddyRuntime: caddyRuntime, readiness: readiness, reason: reason,
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
	if stores.groupStore, err = storage.NewSQLiteGroupStore(database); err != nil {
		return stores, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
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
	if stores.adminWords, err = config.LoadAdminMutation(); err != nil {
		return stores, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	if stores.checkpointStore, err = storage.NewSQLiteCaddyCheckpointStore(database); err != nil {
		return stores, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	stores.checkpointFiles, err = artifacts.NewCaddyCheckpointArtifacts(bootstrap.ArtifactsPath, artifacts.CaddyCheckpointOptions{
		Directory: stores.adminWords.Paths.CheckpointDirectory, CheckpointSuffix: stores.adminWords.Paths.CheckpointSuffix,
		TemporarySuffix: stores.adminWords.Paths.TemporarySuffix, DirectoryMode: stores.adminWords.Modes.Directory,
		FileMode: stores.adminWords.Modes.File, InvalidConfiguration: stores.adminWords.Diagnostics.InvalidConfiguration,
	})
	if err != nil {
		return stores, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	if stores.releaseStore, err = storage.NewSQLiteGroupReleaseStore(database); err != nil {
		return stores, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	if stores.releasePolicy, err = config.LoadGroupRelease(); err != nil {
		return stores, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	if stores.releaseArtifacts, err = artifacts.NewGroupReleaseArtifacts(bootstrap.ArtifactsPath); err != nil {
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
	if inventory.cookiePolicyStore, err = storage.NewSQLitePluginCookiePolicyStore(database); err != nil {
		return inventory, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
	}
	if inventory.cookiePolicies, err = inventory.cookiePolicyStore.List(context.Background()); err != nil {
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

func buildPluginDispatchBindings(options runContext, runtime *plugins.Runtime, policies []models.PluginCookiePolicy, management config.ManagementWords) ([]caddyruntime.PluginDispatchBinding, int) {
	bindings := make([]caddyruntime.PluginDispatchBinding, 0, len(runtime.DispatchBindings()))
	for _, binding := range runtime.DispatchBindings() {
		bindings = append(bindings, caddyruntime.PluginDispatchBinding{
			Name: binding.Name, Endpoint: binding.Endpoint,
			Timeout: binding.Timeout, StartTimeout: binding.StartTimeout,
			MaxConcurrentCalls: binding.MaxConcurrentCalls,
		})
	}
	for _, policy := range policies {
		encoded, err := json.Marshal(plugins.CookiePolicy{
			Version: management.CookiePolicy.Version, InstanceID: policy.InstanceID,
			Capability: policy.Capability, AllowedNames: policy.AllowedNames,
		})
		if err != nil {
			return nil, failBootstrap(options, options.words.Exits.Internal, options.words.Diagnostics.ConfigInvalid)
		}
		for index := range bindings {
			if bindings[index].Name == policy.InstanceID {
				bindings[index].CookiePolicies = append(bindings[index].CookiePolicies, encoded)
			}
		}
	}
	return bindings, options.words.Exits.OK
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
	bootstrap            config.BootstrapConfig
	runtimeBindings      caddyruntime.RuntimeBindings
	stores               bootstrapStores
	inventory            bootstrapInventory
	managementInputs     managementInputs
	groupReleaseService  *application.GroupReleaseService
	adminMutationService *application.AdminMutationService
	caddyRuntime         caddyruntime.CaddyRuntime
	readiness            string
	reason               string
}

func newManagementServer(dependencies managementServerDependencies) *api.Server {
	stores, inventory, inputs := dependencies.stores, dependencies.inventory, dependencies.managementInputs
	management := inventory.management
	audit := inputs.auditWords
	return &api.Server{
		GroupService: application.GroupService{
			Store: stores.groupStore, ContentReader: artifacts.GroupRevisionReader{Root: dependencies.bootstrap.ArtifactsPath},
		},
		Operations:     application.OperationService{Store: stores.operationStore},
		GroupReleases:  dependencies.groupReleaseService,
		AdminMutations: dependencies.adminMutationService,
		AdminWords:     stores.adminWords,
		CookiePolicies: CookiePolicyManagementService(inventory.cookiePolicyStore, dependencies.caddyRuntime, audit, management),
		Audit: &application.AuditService{
			Store: stores.auditStore, RetentionDays: audit.Audit.RetentionDays,
			MinimumLimit: management.Pagination.LimitMin, DefaultLimit: management.Pagination.LimitDefault,
			MaximumLimit: management.Pagination.LimitMax, InvalidLimit: audit.Audit.InvalidLimit,
		},
		AccessService: &application.AccessService{Store: stores.keyStore, Compare: security.CompareServiceKey},
		AuditWords:    audit, Management: management, Errors: inputs.errorCatalog,
		TLSConfig: inputs.tlsConfiguration, CaddyVariant: dependencies.bootstrap.CaddyVariant,
		CaddyBuildID: dependencies.runtimeBindings.CaddyBuildID, CaddyModules: dependencies.runtimeBindings.CaddyModules,
		Plugins: inventory.view, PluginIDField: inventory.contract.JSON.ID,
		DataPlaneState: dependencies.readiness, DataPlaneReason: dependencies.reason,
		DataPlaneReadiness: func(requestContext context.Context) (string, string) {
			return dataPlaneReadiness(dependencies.caddyRuntime, management, dependencies.readiness, dependencies.reason, requestContext)
		},
	}
}

func dataPlaneReadiness(runtime caddyruntime.CaddyRuntime, management config.ManagementWords, readiness, reason string, requestContext context.Context) (string, string) {
	lazy, deferred := runtime.(interface{ Active() bool })
	if deferred && !lazy.Active() {
		if readiness == management.Statuses.Ready {
			return management.Statuses.NotReady, management.Statuses.CaddyUnavailable
		}
		return readiness, reason
	}
	if readiness != management.Statuses.Ready && !deferred {
		return readiness, reason
	}
	probe, ok := runtime.(interface{ Ready(context.Context) error })
	if !ok || probe.Ready(requestContext) == nil {
		if deferred {
			return management.Statuses.Ready, ""
		}
		return readiness, reason
	}
	return management.Statuses.NotReady, management.Statuses.CaddyUnavailable
}

func systemDataPlane(store *storage.SQLiteGroupStore, bootstrap config.BootstrapConfig, managementWords config.ManagementWords, runtimeBindings caddyruntime.RuntimeBindings, pluginBindings []caddyruntime.PluginDispatchBinding) (string, string, func() error, caddyruntime.CaddyRuntime) {
	sqliteContract, err := config.LoadSQLiteContract()
	if err != nil || sqliteContract.SystemGroupID == "" {
		return managementWords.Statuses.NotReady, managementWords.Statuses.RecoveryRequired, nil, nil
	}
	pointers, err := store.GetPointers(context.Background(), sqliteContract.SystemGroupID)
	if err != nil {
		return managementWords.Statuses.NotReady, managementWords.Statuses.RecoveryRequired, nil, nil
	}
	if pointers.CurrentRevisionID == nil {
		activator := caddyruntime.NewSystemCaddyActivator(bootstrap, runtimeBindings, pluginBindings, managementWords.Statuses.CaddyUnavailable, managementWords.CookiePolicy.Version)
		return managementWords.Statuses.NotReady, managementWords.Statuses.SystemReleaseRequired, activator.Stop, activator
	}
	revision, err := store.GetRevision(context.Background(), sqliteContract.SystemGroupID, *pointers.CurrentRevisionID)
	if err != nil {
		return managementWords.Statuses.NotReady, managementWords.Statuses.RecoveryRequired, nil, nil
	}
	path := revision.CaddyfilePath
	if !filepath.IsAbs(path) {
		path = filepath.Join(bootstrap.ArtifactsPath, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return managementWords.Statuses.NotReady, managementWords.Statuses.RecoveryRequired, nil, nil
	}
	root, err := filepath.EvalSymlinks(bootstrap.ArtifactsPath)
	if err != nil || !withinDirectory(root, resolved) {
		return managementWords.Statuses.NotReady, managementWords.Statuses.RecoveryRequired, nil, nil
	}
	caddyfile, err := os.ReadFile(resolved)
	if err != nil {
		return managementWords.Statuses.NotReady, managementWords.Statuses.RecoveryRequired, nil, nil
	}
	digest := sha256.Sum256(caddyfile)
	if hex.EncodeToString(digest[:]) != revision.CaddyfileDigest {
		return managementWords.Statuses.NotReady, managementWords.Statuses.RecoveryRequired, nil, nil
	}
	if bootstrap.CaddyVariant != bootstrap.CaddyEmbeddedVariant && bootstrap.CaddyVariant != bootstrap.CaddyExternalVariant {
		return managementWords.Statuses.NotReady, managementWords.Statuses.CaddyUnavailable, nil, nil
	}
	activator := caddyruntime.NewSystemCaddyActivator(bootstrap, runtimeBindings, pluginBindings, managementWords.Statuses.CaddyUnavailable, managementWords.CookiePolicy.Version)
	return managementWords.Statuses.Ready, "", activator.Stop, activator
}

func withinDirectory(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
