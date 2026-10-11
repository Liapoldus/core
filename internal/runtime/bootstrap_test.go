package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/infrastructure/storage"
)

func TestBootstrapTokenIsConsumedFromProcessEnvironment(t *testing.T) {
	const token = "synthetic-bootstrap-token-for-test"
	t.Setenv("CORE_INIT_MANAGEMENT_TOKEN", token)
	got, err := takeBootstrapManagementToken()
	if err != nil || got != token {
		t.Fatalf("consumed token=%q", got)
	}
	if got := os.Getenv("CORE_INIT_MANAGEMENT_TOKEN"); got != "" {
		t.Fatal("bootstrap token remains in the Core process environment")
	}
}

func TestBootstrapTokenFileIsConsumedAndRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap-token")
	if err := os.WriteFile(path, []byte("synthetic-file-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CORE_INIT_MANAGEMENT_TOKEN_FILE", path)
	t.Setenv("CORE_INIT_MANAGEMENT_TOKEN", "must-not-win")
	got, err := takeBootstrapManagementToken()
	if err != nil || got != "synthetic-file-token" {
		t.Fatalf("consumed token=%q, err=%v", got, err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("one-time token file still exists: %v", err)
	}
	if got := os.Getenv("CORE_INIT_MANAGEMENT_TOKEN_FILE"); got != "" {
		t.Fatal("bootstrap token file path remains in the Core process environment")
	}
}

func TestBootstrapTokenFileRejectsInsecurePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap-token")
	if err := os.WriteFile(path, []byte("synthetic-file-token"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := consumeBootstrapTokenFile(path); err == nil {
		t.Fatal("token file with group/world permissions was accepted")
	}
}

func TestBootstrapTokenFileCreatesOnlyFirstManagementVerifier(t *testing.T) {
	root := t.TempDir()
	database, err := OpenDatabase(context.Background(), filepath.Join(root, "core.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	store, err := storage.NewSQLiteServiceKeyStore(database)
	if err != nil {
		t.Fatal(err)
	}
	words, err := config.LoadRuntime()
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(root, "management-token")
	if err := os.WriteFile(tokenPath, []byte("fresh-test-management-token-at-least-32-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CORE_INIT_MANAGEMENT_TOKEN_FILE", tokenPath)
	if err := bootstrapManagementKey(context.Background(), store, words); err != nil {
		t.Fatalf("bootstrap first key: %v", err)
	}
	verifiers, err := store.ActiveVerifiers(context.Background())
	if err != nil || len(verifiers) != 1 {
		t.Fatalf("active verifiers=%d err=%v", len(verifiers), err)
	}
	// A Docker restart reuses the container environment, but the consumed
	// one-time mount is gone. Existing state must continue using its verifier.
	t.Setenv("CORE_INIT_MANAGEMENT_TOKEN_FILE", filepath.Join(root, "already-consumed"))
	if err := bootstrapManagementKey(context.Background(), store, words); err != nil {
		t.Fatalf("restart with initialized state: %v", err)
	}
}

func TestHostWaitReadyDoesNotConsumeExitResult(t *testing.T) {
	host, err := Start(context.Background(), config.BootstrapConfig{
		StatePath: filepath.Join(t.TempDir(), "missing", "core.sqlite"),
	}, RunOptions{WriteFailure: func(string, int, string, string) {}})
	if err != nil {
		t.Fatalf("start host: %v", err)
	}

	readyContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := host.WaitReady(readyContext); err == nil {
		t.Fatal("WaitReady succeeded after startup failure")
	}
	first := host.Wait()
	second := host.Wait()
	if first != second {
		t.Fatalf("Wait results = %d and %d", first, second)
	}
}
