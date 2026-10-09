package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	settingsstore "github.com/Liapoldus/core/internal/infrastructure/storage/settings"
	bootstrapruntime "github.com/Liapoldus/core/internal/presentation/cli/bootstrap"
)

func statePath() (string, error) {
	path := os.Getenv("CORE_SQLITE_PATH")
	if path == "" || !filepath.IsAbs(path) {
		return "", config.ErrInvalidDocument
	}
	return filepath.Clean(path), nil
}

func initCore(options options) int {
	if len(options.command) != 1 {
		return configValidationFailure(options.output, config.ErrInvalidDocument)
	}
	path, err := statePath()
	if err != nil {
		return configValidationFailure(options.output, err)
	}
	settings, err := initialSettings()
	if err != nil {
		return configValidationFailure(options.output, err)
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return configValidationFailure(options.output, err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return configValidationFailure(options.output, err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			writeFailure(options.output, words.Exits.Conflict, "core_already_initialized", "Core initialization requires a new database.")
			return words.Exits.Conflict
		}
		return configValidationFailure(options.output, err)
	}
	if err = file.Close(); err != nil {
		return configValidationFailure(options.output, err)
	}
	database, unlock, err := bootstrapruntime.OpenExclusiveDatabase(context.Background(), path)
	if err != nil {
		return configValidationFailure(options.output, err)
	}
	defer unlock()
	defer database.Close()
	if err = settingsstore.EnsureSchema(database); err != nil {
		return configValidationFailure(options.output, err)
	}
	if _, err = settingsstore.New(database).Init(context.Background(), raw, "operator-init"); err != nil {
		return configValidationFailure(options.output, err)
	}
	writeSuccess(options.output, map[string]any{"ok": true, "desiredRevision": 1, "effectiveRevision": 0, "pending": true})
	return words.Exits.OK
}

// Initial values are read once. serve never consults CORE_INIT_* or overlays
// persisted settings with ENV values.
func initialSettings() (config.Settings, error) {
	value := func(name, fallback string) string {
		if v, ok := os.LookupEnv("CORE_INIT_" + name); ok {
			return v
		}
		return fallback
	}
	s := config.Settings{
		Management: config.ManagementSettings{Listen: value("MANAGEMENT_LISTEN", "127.0.0.1:8080"),
			TLS:          config.TLSReferences{Certificate: value("MANAGEMENT_CERTIFICATE", ""), Key: value("MANAGEMENT_KEY", ""), ClientCA: value("MANAGEMENT_CLIENT_CA", "")},
			MaxBodyBytes: 1048576, HeaderTimeout: value("HEADER_TIMEOUT", "5s"), RequestTimeout: value("REQUEST_TIMEOUT", "30s")},
		PluginControl: config.ControlSettings{Listen: value("CONTROL_LISTEN", "127.0.0.1:8081"), PublicURL: value("CONTROL_PUBLIC_URL", "https://localhost:8081"),
			TLS:             config.TLSReferences{Certificate: value("CONTROL_CERTIFICATE", ""), Key: value("CONTROL_KEY", "")},
			ReplicaClientCA: value("REPLICA_CLIENT_CA", ""), ReplicaServerCA: value("REPLICA_SERVER_CA", ""),
			ReplicaClientCRLs: filepath.SplitList(value("REPLICA_CLIENT_CRLS", "")),
			ReplicaServerCRLs: filepath.SplitList(value("REPLICA_SERVER_CRLS", ""))},
		SecretRoot: value("SECRET_ROOT", ""),
	}
	if max, ok := os.LookupEnv("CORE_INIT_MAX_BODY_BYTES"); ok {
		parsed, err := strconv.ParseInt(max, 10, 64)
		if err != nil {
			return s, config.ErrInvalidDocument
		}
		s.Management.MaxBodyBytes = parsed
	}
	return s, s.Validate()
}

func storedSettings(path string) (config.Settings, error) {
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return config.Settings{}, config.ErrInvalidDocument
	}
	database, err := bootstrapruntime.OpenDatabase(context.Background(), path)
	if err != nil {
		return config.Settings{}, config.ErrInvalidDocument
	}
	defer database.Close()
	snapshot, err := settingsstore.New(database).Read(context.Background())
	if err != nil {
		return config.Settings{}, err
	}
	return config.DecodeSettings(snapshot.DesiredDocument)
}

func recoverSettings(options options) int {
	if len(options.command) != 2 {
		return configValidationFailure(options.output, config.ErrInvalidDocument)
	}
	revision, err := strconv.ParseInt(options.command[1], 10, 64)
	if err != nil || revision < 1 {
		return configValidationFailure(options.output, config.ErrInvalidDocument)
	}
	path, err := statePath()
	if err != nil {
		return configValidationFailure(options.output, err)
	}
	if _, err = os.Stat(path); err != nil {
		return configValidationFailure(options.output, err)
	}
	database, unlock, err := bootstrapruntime.OpenExclusiveDatabase(context.Background(), path)
	if err != nil {
		return configValidationFailure(options.output, err)
	}
	defer unlock()
	defer database.Close()
	snapshot, err := settingsstore.New(database).Recover(context.Background(), revision, "operator-recovery")
	if err != nil {
		return configValidationFailure(options.output, err)
	}
	writeSuccess(options.output, map[string]any{"ok": true, "desiredRevision": snapshot.DesiredRevision, "effectiveRevision": snapshot.EffectiveRevision, "pending": snapshot.Pending})
	return words.Exits.OK
}
