package cli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/artifacts"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/internal/infrastructure/security"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/api"
	bootstrapruntime "github.com/Liapoldus/core/internal/presentation/cli/bootstrap"
	"github.com/Liapoldus/core/internal/presentation/cli/caddyruntime"
)

func serve(options options, runtime RuntimeBindings) int {
	path, _, err := discoverConfig(options)
	if err != nil {
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.ConfigNotFound)
		return words.Exits.Arguments
	}
	bootstrap, err := config.LoadBootstrap(path)
	if err != nil {
		return configValidationFailure(options.output, err)
	}
	return serveBootstrap(options, bootstrap, runtime)
}

func access(options options) int {
	if len(options.command) != 2 || options.command[1] != words.Access.Bootstrap {
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.CommandExpected)
		return words.Exits.Arguments
	}
	path, _, err := discoverConfig(options)
	if err != nil {
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.ConfigNotFound)
		return words.Exits.Arguments
	}
	bootstrap, err := config.LoadBootstrap(path)
	if err != nil {
		return configValidationFailure(options.output, err)
	}
	database, err := bootstrapruntime.OpenDatabase(context.Background(), bootstrap.StatePath)
	if err != nil {
		writeFailure(options.output, words.Exits.Unavailable, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Unavailable
	}
	defer database.Close()
	keyStore, err := storage.NewSQLiteServiceKeyStore(database)
	if err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	secret := make([]byte, words.ServiceKey.KeyBytes)
	if _, err := rand.Read(secret); err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	clear(secret)
	verifier, err := security.HashServiceKey(token, words.ServiceKey.HashCost)
	if err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	identifier := make([]byte, words.ServiceKey.KeyBytes)
	if _, err := rand.Read(identifier); err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	keyID := hex.EncodeToString(identifier)
	clear(identifier)
	accessService := application.AccessService{Store: keyStore}
	if err := accessService.Bootstrap(context.Background(), keyID, verifier, words.ServiceKey.RolePlatformAdmin); err != nil {
		var exists models.ActiveServiceKeyExists
		if errors.As(err, &exists) {
			writeFailure(options.output, words.Exits.Conflict, words.Codes.AccessBootstrapConflict, words.Diagnostics.AccessBootstrapConflict)
			return words.Exits.Conflict
		}
		writeFailure(options.output, words.Exits.Unavailable, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Unavailable
	}
	if options.output == words.Outputs.JSON {
		writeSuccess(options.output, map[string]any{words.JSON.Token: token})
	} else {
		fmt.Println(token)
	}
	return words.Exits.OK
}

type bootstrapStores struct {
	groupStore       *storage.SQLiteGroupStore
	auditStore       *storage.SQLiteAuditStore
	keyStore         *storage.SQLiteServiceKeyStore
	operationStore   *storage.SQLiteOperationStore
	adminWords       config.AdminMutationWords
	checkpointStore  *storage.SQLiteCaddyCheckpointStore
	checkpointFiles  *artifacts.CaddyCheckpointArtifacts
	releaseStore     *storage.SQLiteGroupReleaseStore
	releasePolicy    models.GroupReleasePolicy
	releaseArtifacts artifacts.GroupReleaseArtifacts
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
	contract  config.PluginRuntimeContract
	instances map[string]models.PluginInstance
}

type managementInputs struct {
	auditWords       config.AuditWords
	errorCatalog     config.ErrorCatalog
	tlsConfiguration *tls.Config
}

func serveBootstrap(options options, bootstrap config.BootstrapConfig, runtimeBindings RuntimeBindings) int {
	sqliteContract, err := config.LoadSQLiteContract()
	if err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	database, err := bootstrapruntime.OpenDatabase(context.Background(), bootstrap.StatePath)
	if err != nil {
		writeFailure(options.output, words.Exits.Unavailable, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Unavailable
	}
	defer database.Close()
	stores, exitCode := loadBootstrapStores(options, bootstrap, database, sqliteContract)
	if exitCode != words.Exits.OK {
		return exitCode
	}
	inventory, exitCode := loadBootstrapInventory(options, database)
	if exitCode != words.Exits.OK {
		return exitCode
	}
	pluginLaunch, exitCode := prepareLocalPlugins(options, inventory.records)
	if exitCode != words.Exits.OK {
		return exitCode
	}
	pluginRuntime, err := plugins.StartRuntime(context.Background(), pluginLaunch.instances, nil)
	if err != nil {
		return failBootstrap(options, words.Exits.Unavailable, pluginLaunch.contract.Diagnostics.StartupFailed)
	}
	defer func() { _ = pluginRuntime.Stop(context.Background()) }()
	pluginBindings, exitCode := buildPluginDispatchBindings(options, pluginRuntime, inventory.cookiePolicies, inventory.management)
	if exitCode != words.Exits.OK {
		return exitCode
	}
	managementInputs, exitCode := loadManagementInputs(options, bootstrap)
	if exitCode != words.Exits.OK {
		return exitCode
	}
	readiness, reason, stopRuntime, caddyRuntime := systemDataPlane(stores.groupStore, bootstrap, inventory.management, runtimeBindings, pluginBindings)
	adminMutationService := newAdminMutationService(caddyRuntime, stores)
	if stopRuntime != nil {
		defer stopRuntime()
	}
	groupReleaseService := newGroupReleaseService(bootstrap, stores, caddyRuntime, adminMutationService)
	readiness, reason = activateCurrentGroupRelease(groupReleaseService, caddyRuntime, inventory.management, readiness, reason)
	management := newManagementServer(managementServerDependencies{
		bootstrap: bootstrap, runtimeBindings: runtimeBindings, stores: stores,
		inventory: inventory, managementInputs: managementInputs,
		groupReleaseService: groupReleaseService, adminMutationService: adminMutationService,
		caddyRuntime: caddyRuntime, readiness: readiness, reason: reason,
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := management.Listen(ctx, bootstrap.ManagementListen); err != nil && !errors.Is(err, net.ErrClosed) {
		writeFailure(options.output, words.Exits.Unavailable, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Unavailable
	}
	return words.Exits.OK
}

func failBootstrap(options options, exitCode int, detail string) int {
	writeFailure(options.output, exitCode, words.Codes.ConfigInvalid, detail)
	return exitCode
}

func loadBootstrapStores(options options, bootstrap config.BootstrapConfig, database *sql.DB, sqliteContract config.SQLiteContract) (bootstrapStores, int) {
	var stores bootstrapStores
	if err := os.MkdirAll(bootstrap.ArtifactsPath, os.FileMode(sqliteContract.ArtifactsDirectoryMode)); err != nil {
		return stores, failBootstrap(options, words.Exits.Unavailable, words.Diagnostics.ConfigInvalid)
	}
	var err error
	if stores.groupStore, err = storage.NewSQLiteGroupStore(database); err != nil {
		return stores, failBootstrap(options, words.Exits.Internal, words.Diagnostics.ConfigInvalid)
	}
	if stores.auditStore, err = storage.NewSQLiteAuditStore(database); err != nil {
		return stores, failBootstrap(options, words.Exits.Internal, words.Diagnostics.ConfigInvalid)
	}
	if stores.keyStore, err = storage.NewSQLiteServiceKeyStore(database); err != nil {
		return stores, failBootstrap(options, words.Exits.Internal, words.Diagnostics.ConfigInvalid)
	}
	if stores.operationStore, err = storage.NewSQLiteOperationStore(database); err != nil {
		return stores, failBootstrap(options, words.Exits.Internal, words.Diagnostics.ConfigInvalid)
	}
	if stores.adminWords, err = config.LoadAdminMutation(); err != nil {
		return stores, failBootstrap(options, words.Exits.Internal, words.Diagnostics.ConfigInvalid)
	}
	if stores.checkpointStore, err = storage.NewSQLiteCaddyCheckpointStore(database); err != nil {
		return stores, failBootstrap(options, words.Exits.Internal, words.Diagnostics.ConfigInvalid)
	}
	stores.checkpointFiles, err = artifacts.NewCaddyCheckpointArtifacts(bootstrap.ArtifactsPath, artifacts.CaddyCheckpointOptions{
		Directory: stores.adminWords.Paths.CheckpointDirectory, CheckpointSuffix: stores.adminWords.Paths.CheckpointSuffix,
		TemporarySuffix: stores.adminWords.Paths.TemporarySuffix, DirectoryMode: stores.adminWords.Modes.Directory,
		FileMode: stores.adminWords.Modes.File, InvalidConfiguration: stores.adminWords.Diagnostics.InvalidConfiguration,
	})
	if err != nil {
		return stores, failBootstrap(options, words.Exits.Internal, words.Diagnostics.ConfigInvalid)
	}
	if stores.releaseStore, err = storage.NewSQLiteGroupReleaseStore(database); err != nil {
		return stores, failBootstrap(options, words.Exits.Internal, words.Diagnostics.ConfigInvalid)
	}
	if stores.releasePolicy, err = config.LoadGroupRelease(); err != nil {
		return stores, failBootstrap(options, words.Exits.Internal, words.Diagnostics.ConfigInvalid)
	}
	if stores.releaseArtifacts, err = artifacts.NewGroupReleaseArtifacts(bootstrap.ArtifactsPath); err != nil {
		return stores, failBootstrap(options, words.Exits.Internal, words.Diagnostics.ConfigInvalid)
	}
	return stores, words.Exits.OK
}

func loadBootstrapInventory(options options, database *sql.DB) (bootstrapInventory, int) {
	var inventory bootstrapInventory
	var err error
	if inventory.management, err = config.LoadManagement(); err != nil {
		return inventory, failBootstrap(options, words.Exits.Internal, words.Diagnostics.ConfigInvalid)
	}
	if inventory.contract, err = config.LoadPluginInventoryContract(); err != nil {
		return inventory, failBootstrap(options, words.Exits.Internal, words.Diagnostics.ConfigInvalid)
	}
	inventory.records, err = storage.ListPluginInstances(context.Background(), database, storage.PluginInstanceQuery{
		SelectInstances: inventory.contract.SelectInstances,
		ValidModes:      inventory.contract.ValidModes,
		ValidStates:     inventory.contract.ValidStates,
		InvalidContract: inventory.contract.Diagnostics.InvalidContract,
		InvalidRecord:   inventory.contract.Diagnostics.InvalidRecord,
	})
	if err != nil {
		return inventory, failBootstrap(options, words.Exits.Unavailable, words.Diagnostics.ConfigInvalid)
	}
	if inventory.cookiePolicyStore, err = storage.NewSQLitePluginCookiePolicyStore(database); err != nil {
		return inventory, failBootstrap(options, words.Exits.Internal, words.Diagnostics.ConfigInvalid)
	}
	if inventory.cookiePolicies, err = inventory.cookiePolicyStore.List(context.Background()); err != nil {
		return inventory, failBootstrap(options, words.Exits.Unavailable, words.Diagnostics.ConfigInvalid)
	}
	if inventory.view, err = bootstrapruntime.PresentPluginInventory(inventory.records, inventory.contract); err != nil {
		return inventory, failBootstrap(options, words.Exits.Validation, words.Diagnostics.ConfigInvalid)
	}
	return inventory, words.Exits.OK
}

func prepareLocalPlugins(options options, records []storage.PluginInstanceRecord) (pluginLaunchSetup, int) {
	var setup pluginLaunchSetup
	var err error
	if setup.contract, err = config.LoadPluginRuntimeContract(); err != nil {
		return setup, failBootstrap(options, words.Exits.Internal, words.Diagnostics.ConfigInvalid)
	}
	launchSchema, err := config.LoadPluginLaunchSchema()
	if err != nil {
		return setup, failBootstrap(options, words.Exits.Internal, words.Diagnostics.ConfigInvalid)
	}
	fileReferencePrefix, err := config.LoadFileReferencePrefix()
	if err != nil {
		return setup, failBootstrap(options, words.Exits.Internal, words.Diagnostics.ConfigInvalid)
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
		return setup, failBootstrap(options, words.Exits.Validation, setup.contract.Diagnostics.InvalidLaunch)
	}
	return setup, words.Exits.OK
}

func buildPluginDispatchBindings(options options, runtime *plugins.Runtime, policies []models.PluginCookiePolicy, management config.ManagementWords) ([]PluginDispatchBinding, int) {
	bindings := make([]PluginDispatchBinding, 0, len(runtime.DispatchBindings()))
	for _, binding := range runtime.DispatchBindings() {
		bindings = append(bindings, PluginDispatchBinding{
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
			return nil, failBootstrap(options, words.Exits.Internal, words.Diagnostics.ConfigInvalid)
		}
		for index := range bindings {
			if bindings[index].Name == policy.InstanceID {
				bindings[index].CookiePolicies = append(bindings[index].CookiePolicies, encoded)
			}
		}
	}
	return bindings, words.Exits.OK
}

func loadManagementInputs(options options, bootstrap config.BootstrapConfig) (managementInputs, int) {
	var inputs managementInputs
	var err error
	if inputs.auditWords, err = config.LoadAudit(); err != nil {
		return inputs, failBootstrap(options, words.Exits.Internal, words.Diagnostics.ConfigInvalid)
	}
	if inputs.errorCatalog, err = config.LoadErrorCatalog(); err != nil {
		return inputs, failBootstrap(options, words.Exits.Internal, words.Diagnostics.ConfigInvalid)
	}
	if inputs.tlsConfiguration, err = bootstrapruntime.ManagementTLS(bootstrap, words.Diagnostics.ConfigInvalid); err != nil {
		return inputs, failBootstrap(options, words.Exits.Validation, words.Diagnostics.ConfigInvalid)
	}
	return inputs, words.Exits.OK
}

func newAdminMutationService(runtime CaddyRuntime, stores bootstrapStores) *application.AdminMutationService {
	adminClient, _ := runtime.(interfaces.CaddyAdminClient)
	return &application.AdminMutationService{
		Admin: adminClient, Checkpoints: stores.checkpointStore, Artifacts: stores.checkpointFiles,
		Policy: application.AdminMutationPolicy{
			MutationMethods:       stores.adminWords.Methods.Mutating,
			SuccessStatusMinimum:  stores.adminWords.Statuses.SuccessMinimum,
			SuccessStatusMaximum:  stores.adminWords.Statuses.SuccessMaximum,
			MaximumSnapshotBytes:  stores.adminWords.Limits.SnapshotBytes,
			OperationKind:         stores.adminWords.Operation.Kind,
			OperationRunning:      stores.adminWords.Operation.Running,
			OperationSucceeded:    stores.adminWords.Operation.Succeeded,
			OperationFailed:       stores.adminWords.Operation.Failed,
			AuditAction:           stores.adminWords.Audit.Action,
			AuditResource:         stores.adminWords.Audit.Resource,
			AuditStarted:          stores.adminWords.Audit.Started,
			AuditSucceeded:        stores.adminWords.Audit.Succeeded,
			AuditFailed:           stores.adminWords.Audit.Failed,
			InvalidConfiguration:  stores.adminWords.Diagnostics.InvalidConfiguration,
			SnapshotUnavailable:   stores.adminWords.Diagnostics.SnapshotUnavailable,
			CheckpointUnavailable: stores.adminWords.Diagnostics.CheckpointUnavailable,
			AdminUnavailable:      stores.adminWords.Diagnostics.AdminUnavailable,
		},
	}
}

func newGroupReleaseService(bootstrap config.BootstrapConfig, stores bootstrapStores, runtime CaddyRuntime, driftGuard *application.AdminMutationService) *application.GroupReleaseService {
	return &application.GroupReleaseService{
		Store: stores.groupStore, Releases: stores.releaseStore,
		ContentReader: artifacts.GroupRevisionReader{Root: bootstrap.ArtifactsPath},
		Artifacts:     stores.releaseArtifacts, Activator: runtime, DriftGuard: driftGuard, Policy: stores.releasePolicy,
	}
}

func activateCurrentGroupRelease(service *application.GroupReleaseService, runtime CaddyRuntime, management config.ManagementWords, readiness, reason string) (string, string) {
	if runtime == nil {
		return readiness, reason
	}
	if err := service.Recover(context.Background()); err != nil {
		return management.Statuses.NotReady, management.Statuses.RecoveryRequired
	}
	if reason == management.Statuses.SystemReleaseRequired {
		return readiness, reason
	}
	lazy, deferred := runtime.(interface{ Active() bool })
	if deferred && lazy.Active() {
		return readiness, reason
	}
	if err := service.ActivateCurrent(context.Background()); err != nil {
		return management.Statuses.NotReady, management.Statuses.RecoveryRequired
	}
	return readiness, reason
}

type managementServerDependencies struct {
	bootstrap            config.BootstrapConfig
	runtimeBindings      RuntimeBindings
	stores               bootstrapStores
	inventory            bootstrapInventory
	managementInputs     managementInputs
	groupReleaseService  *application.GroupReleaseService
	adminMutationService *application.AdminMutationService
	caddyRuntime         CaddyRuntime
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
		CookiePolicies: cookiePolicyManagementService(dependencies.bootstrap, inventory.cookiePolicyStore, dependencies.caddyRuntime, audit, management),
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

func dataPlaneReadiness(runtime CaddyRuntime, management config.ManagementWords, readiness, reason string, requestContext context.Context) (string, string) {
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

func cookiePolicyManagementService(bootstrap config.BootstrapConfig, store interfaces.PluginCookiePolicyStore, runtime CaddyRuntime, audit config.AuditWords, management config.ManagementWords) *application.PluginCookiePolicyService {
	if store == nil {
		return nil
	}
	var activator interfaces.PluginCookiePolicyActivator
	activator, _ = runtime.(interfaces.PluginCookiePolicyActivator)
	return &application.PluginCookiePolicyService{
		Store: store, Activator: activator,
		AuditAction:      audit.Audit.Actions.PluginCookiePolicyReplace,
		Resource:         audit.Audit.Resources.PluginCookiePolicies,
		Success:          audit.Audit.Results.Succeeded,
		Invalid:          management.Codes.InvalidCookiePolicy,
		RevisionConflict: management.Codes.CookiePolicyRevisionConflict,
	}
}

func systemDataPlane(store *storage.SQLiteGroupStore, bootstrap config.BootstrapConfig, managementWords config.ManagementWords, runtimeBindings RuntimeBindings, pluginBindings []PluginDispatchBinding) (string, string, func() error, CaddyRuntime) {
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
