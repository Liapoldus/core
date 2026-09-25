package cli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
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
	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/protobuf/encoding/protojson"
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
	database, err := openBootstrapDatabase(context.Background(), bootstrap.StatePath)
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

func serveBootstrap(options options, bootstrap config.BootstrapConfig, runtimeBindings RuntimeBindings) int {
	sqliteContract, err := config.LoadSQLiteContract()
	if err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	database, err := openBootstrapDatabase(context.Background(), bootstrap.StatePath)
	if err != nil {
		writeFailure(options.output, words.Exits.Unavailable, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Unavailable
	}
	defer database.Close()
	if err := os.MkdirAll(bootstrap.ArtifactsPath, os.FileMode(sqliteContract.ArtifactsDirectoryMode)); err != nil {
		writeFailure(options.output, words.Exits.Unavailable, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Unavailable
	}
	groupStore, err := storage.NewSQLiteGroupStore(database)
	if err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	auditStore, err := storage.NewSQLiteAuditStore(database)
	if err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	keyStore, err := storage.NewSQLiteServiceKeyStore(database)
	if err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	operationStore, err := storage.NewSQLiteOperationStore(database)
	if err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	releaseStore, err := storage.NewSQLiteGroupReleaseStore(database)
	if err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	releasePolicy, err := config.LoadGroupRelease()
	if err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	releaseArtifacts, err := artifacts.NewGroupReleaseArtifacts(bootstrap.ArtifactsPath)
	if err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	managementWords, err := config.LoadManagement()
	if err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	pluginInventoryContract, err := config.LoadPluginInventoryContract()
	if err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	pluginRecords, err := storage.ListPluginInstances(context.Background(), database, pluginInventoryContract)
	if err != nil {
		writeFailure(options.output, words.Exits.Unavailable, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Unavailable
	}
	pluginInventory, err := presentPluginInventory(pluginRecords, pluginInventoryContract)
	if err != nil {
		writeFailure(options.output, words.Exits.Validation, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Validation
	}
	auditWords, err := config.LoadAudit()
	if err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	errorCatalog, err := config.LoadErrorCatalog()
	if err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	tlsConfiguration, err := managementTLS(bootstrap)
	if err != nil {
		writeFailure(options.output, words.Exits.Validation, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Validation
	}
	readiness, reason, stopRuntime, caddyRuntime := systemDataPlane(groupStore, bootstrap, managementWords, runtimeBindings)
	if stopRuntime != nil {
		defer stopRuntime()
	}
	groupReleaseService := &application.GroupReleaseService{
		Store: groupStore, Releases: releaseStore,
		ContentReader: artifacts.GroupRevisionReader{Root: bootstrap.ArtifactsPath},
		Artifacts:     releaseArtifacts, Activator: caddyRuntime, Policy: releasePolicy,
	}
	if caddyRuntime != nil {
		if err := groupReleaseService.ActivateCurrent(context.Background()); err != nil {
			readiness, reason = managementWords.Statuses.NotReady, managementWords.Statuses.RecoveryRequired
		} else if err := groupReleaseService.Recover(context.Background()); err != nil {
			readiness, reason = managementWords.Statuses.NotReady, managementWords.Statuses.RecoveryRequired
		}
	}
	management := &api.Server{
		GroupService: application.GroupService{
			Store: groupStore, ContentReader: artifacts.GroupRevisionReader{Root: bootstrap.ArtifactsPath},
		},
		Operations:    application.OperationService{Store: operationStore},
		GroupReleases: groupReleaseService,
		Audit: &application.AuditService{
			Store: auditStore, RetentionDays: auditWords.Audit.RetentionDays,
			MinimumLimit: managementWords.Pagination.LimitMin, DefaultLimit: managementWords.Pagination.LimitDefault,
			MaximumLimit: managementWords.Pagination.LimitMax, InvalidLimit: auditWords.Audit.InvalidLimit,
		},
		AccessService:   &application.AccessService{Store: keyStore, Compare: security.CompareServiceKey},
		AuditWords:      auditWords,
		Management:      managementWords,
		Errors:          errorCatalog,
		TLSConfig:       tlsConfiguration,
		CaddyVariant:    bootstrap.CaddyVariant,
		CaddyBuildID:    runtimeBindings.CaddyBuildID,
		CaddyModules:    runtimeBindings.CaddyModules,
		Plugins:         pluginInventory,
		DataPlaneState:  readiness,
		DataPlaneReason: reason,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := management.Listen(ctx, bootstrap.ManagementListen); err != nil && !errors.Is(err, net.ErrClosed) {
		writeFailure(options.output, words.Exits.Unavailable, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Unavailable
	}
	return words.Exits.OK
}

func presentPluginInventory(records []storage.PluginInstanceRecord, contract config.PluginInventoryContract) ([]any, error) {
	items := make([]any, 0, len(records))
	for _, record := range records {
		manifest := new(pluginv1.Manifest)
		if err := protojson.Unmarshal(record.ManifestJSON, manifest); err != nil || plugins.ValidateManifest(manifest, nil) != nil || manifest.GetName() != record.ID {
			return nil, errors.New(contract.Diagnostics.InvalidManifest)
		}
		descriptors := make([]map[string]any, 0, len(manifest.GetCapabilityDescriptors()))
		for _, descriptor := range manifest.GetCapabilityDescriptors() {
			modes := make([]string, 0, len(descriptor.GetModes()))
			for _, mode := range descriptor.GetModes() {
				modes = append(modes, mode.String())
			}
			descriptors = append(descriptors, map[string]any{
				contract.JSON.DescriptorCapability: descriptor.GetCapability(),
				contract.JSON.DescriptorModes:      modes,
			})
		}
		items = append(items, map[string]any{
			contract.JSON.ID:                    record.ID,
			contract.JSON.Mode:                  record.Mode,
			contract.JSON.State:                 record.State,
			contract.JSON.Revision:              record.Revision,
			contract.JSON.Capabilities:          manifest.GetCapabilities(),
			contract.JSON.CapabilityDescriptors: descriptors,
		})
	}
	return items, nil
}

func openBootstrapDatabase(ctx context.Context, path string) (*sql.DB, error) {
	contract, err := config.LoadSQLiteContract()
	if err != nil {
		return nil, err
	}
	return storage.OpenSQLite(ctx, path, storage.SQLiteOptions{
		Driver:                 contract.Driver,
		ParentDirectoryMode:    contract.ParentDirectoryMode,
		DatabaseFileMode:       contract.DatabaseFileMode,
		MaxOpenConnections:     contract.MaxOpenConnections,
		MaxIdleConnections:     contract.MaxIdleConnections,
		SchemaVersion:          contract.SchemaVersion,
		HasMigrationTableQuery: contract.HasMigrationTableQuery,
		MigrationVersionQuery:  contract.MigrationVersionQuery,
		SchemaVersionError:     contract.SchemaVersionError,
		Pragmas:                contract.Pragmas,
	}, contract.Schema)
}

func managementTLS(bootstrap config.BootstrapConfig) (*tls.Config, error) {
	certificatePEM, err := os.ReadFile(bootstrap.ManagementCertificate)
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(bootstrap.ManagementKey)
	if err != nil {
		return nil, err
	}
	certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil {
		return nil, err
	}
	result := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	if bootstrap.ManagementClientCA != "" {
		caPEM, err := os.ReadFile(bootstrap.ManagementClientCA)
		if err != nil {
			return nil, err
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(caPEM) {
			return nil, errors.New(words.Diagnostics.ConfigInvalid)
		}
		result.ClientCAs = roots
		result.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return result, nil
}

func systemDataPlane(store *storage.SQLiteGroupStore, bootstrap config.BootstrapConfig, managementWords config.ManagementWords, runtimeBindings RuntimeBindings) (string, string, func() error, CaddyRuntime) {
	sqliteContract, err := config.LoadSQLiteContract()
	if err != nil || sqliteContract.SystemGroupID == "" {
		return managementWords.Statuses.NotReady, managementWords.Statuses.RecoveryRequired, nil, nil
	}
	pointers, err := store.GetPointers(context.Background(), sqliteContract.SystemGroupID)
	if err != nil {
		return managementWords.Statuses.NotReady, managementWords.Statuses.RecoveryRequired, nil, nil
	}
	if pointers.CurrentRevisionID == nil {
		return managementWords.Statuses.NotReady, managementWords.Statuses.SystemReleaseRequired, nil, nil
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
	if bootstrap.CaddyVariant != bootstrap.CaddyEmbeddedVariant || runtimeBindings.StartCaddyfile == nil {
		return managementWords.Statuses.NotReady, managementWords.Statuses.CaddyUnavailable, nil, nil
	}
	caddyRuntime, err := runtimeBindings.StartCaddyfile(caddyfile)
	if err != nil {
		return managementWords.Statuses.NotReady, managementWords.Statuses.CaddyUnavailable, nil, nil
	}
	return managementWords.Statuses.Ready, "", caddyRuntime.Stop, caddyRuntime
}

func withinDirectory(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
