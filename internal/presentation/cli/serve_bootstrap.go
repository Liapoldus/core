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
	"github.com/Liapoldus/core/internal/infrastructure/security"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/api"
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
	managementWords, err := config.LoadManagement()
	if err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
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
	readiness, reason, stopRuntime := systemDataPlane(groupStore, bootstrap, managementWords, runtimeBindings)
	if stopRuntime != nil {
		defer stopRuntime()
	}
	management := &api.Server{
		GroupService: application.GroupService{
			Store: groupStore, ContentReader: artifacts.GroupRevisionReader{Root: bootstrap.ArtifactsPath},
		},
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

func systemDataPlane(store *storage.SQLiteGroupStore, bootstrap config.BootstrapConfig, managementWords config.ManagementWords, runtimeBindings RuntimeBindings) (string, string, func() error) {
	sqliteContract, err := config.LoadSQLiteContract()
	if err != nil || sqliteContract.SystemGroupID == "" {
		return managementWords.Statuses.NotReady, managementWords.Statuses.RecoveryRequired, nil
	}
	pointers, err := store.GetPointers(context.Background(), sqliteContract.SystemGroupID)
	if err != nil {
		return managementWords.Statuses.NotReady, managementWords.Statuses.RecoveryRequired, nil
	}
	if pointers.CurrentRevisionID == nil {
		return managementWords.Statuses.NotReady, managementWords.Statuses.SystemReleaseRequired, nil
	}
	revision, err := store.GetRevision(context.Background(), sqliteContract.SystemGroupID, *pointers.CurrentRevisionID)
	if err != nil {
		return managementWords.Statuses.NotReady, managementWords.Statuses.RecoveryRequired, nil
	}
	path := revision.CaddyfilePath
	if !filepath.IsAbs(path) {
		path = filepath.Join(bootstrap.ArtifactsPath, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return managementWords.Statuses.NotReady, managementWords.Statuses.RecoveryRequired, nil
	}
	root, err := filepath.EvalSymlinks(bootstrap.ArtifactsPath)
	if err != nil || !withinDirectory(root, resolved) {
		return managementWords.Statuses.NotReady, managementWords.Statuses.RecoveryRequired, nil
	}
	caddyfile, err := os.ReadFile(resolved)
	if err != nil {
		return managementWords.Statuses.NotReady, managementWords.Statuses.RecoveryRequired, nil
	}
	digest := sha256.Sum256(caddyfile)
	if hex.EncodeToString(digest[:]) != revision.CaddyfileDigest {
		return managementWords.Statuses.NotReady, managementWords.Statuses.RecoveryRequired, nil
	}
	if bootstrap.CaddyVariant != bootstrap.CaddyEmbeddedVariant || runtimeBindings.StartCaddyfile == nil {
		return managementWords.Statuses.NotReady, managementWords.Statuses.CaddyUnavailable, nil
	}
	stop, err := runtimeBindings.StartCaddyfile(caddyfile)
	if err != nil {
		return managementWords.Statuses.NotReady, managementWords.Statuses.CaddyUnavailable, nil
	}
	return managementWords.Statuses.Ready, "", stop
}

func withinDirectory(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
