package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	settingsstore "github.com/Liapoldus/core/internal/infrastructure/storage/settings"
)

// EnsureInitialized creates the first settings revision for a new local state.
// This is server bootstrap, not a command surface: the Core binary accepts no
// subcommands and never performs migration or backup work.
func EnsureInitialized(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	settings, err := initialSettings()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	database, unlock, err := OpenExclusiveDatabase(context.Background(), path)
	if err != nil {
		return err
	}
	defer unlock()
	defer database.Close()
	if err := settingsstore.EnsureSchema(database); err != nil {
		return err
	}
	_, err = settingsstore.New(database).Init(context.Background(), raw, "runtime-bootstrap")
	return err
}

func initialSettings() (config.Settings, error) {
	value := func(name, fallback string) string {
		if value, ok := os.LookupEnv("CORE_INIT_" + name); ok {
			return value
		}
		return fallback
	}
	settings := config.Settings{
		Management: config.ManagementSettings{Listen: value("MANAGEMENT_LISTEN", "127.0.0.1:8080"),
			TLS:          config.TLSReferences{Certificate: value("MANAGEMENT_CERTIFICATE", ""), Key: value("MANAGEMENT_KEY", ""), ClientCA: value("MANAGEMENT_CLIENT_CA", "")},
			MaxBodyBytes: 1048576, HeaderTimeout: value("HEADER_TIMEOUT", "5s"), RequestTimeout: value("REQUEST_TIMEOUT", "30s")},
		PluginControl: config.ControlSettings{Listen: value("CONTROL_LISTEN", "127.0.0.1:8081"), PublicURL: value("CONTROL_PUBLIC_URL", "https://localhost:8081"),
			TLS:             config.TLSReferences{Certificate: value("CONTROL_CERTIFICATE", ""), Key: value("CONTROL_KEY", "")},
			ReplicaClientCA: value("REPLICA_CLIENT_CA", ""), ReplicaServerCA: value("REPLICA_SERVER_CA", ""),
			ReplicaClientCRLs: filepath.SplitList(value("REPLICA_CLIENT_CRLS", "")), ReplicaServerCRLs: filepath.SplitList(value("REPLICA_SERVER_CRLS", ""))},
		SecretRoot: value("SECRET_ROOT", ""),
	}
	if max, ok := os.LookupEnv("CORE_INIT_MAX_BODY_BYTES"); ok {
		parsed, err := strconv.ParseInt(max, 10, 64)
		if err != nil {
			return config.Settings{}, config.ErrInvalidDocument
		}
		settings.Management.MaxBodyBytes = parsed
	}
	return settings, settings.Validate()
}

// StatePath returns the one process bootstrap input accepted by the Core
// server. Initialization and updates are owned by the standalone liapoldus
// CLI; Core only reads the persisted settings document.
func StatePath() (string, error) {
	path := os.Getenv("CORE_SQLITE_PATH")
	if path == "" || !filepath.IsAbs(path) {
		return "", config.ErrInvalidDocument
	}
	return filepath.Clean(path), nil
}

// StoredSettings reads the desired settings revision without exposing the
// SQLite store to the server entrypoint.
func StoredSettings(path string) (config.Settings, error) {
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return config.Settings{}, config.ErrInvalidDocument
	}
	database, err := OpenDatabase(context.Background(), path)
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
