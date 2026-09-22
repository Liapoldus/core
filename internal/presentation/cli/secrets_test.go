package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSecretEnvironmentReference(t *testing.T) {
	t.Setenv("LIAPOLDUS_TEST_SECRET", "from-env")
	if got := resolveSecret("env:LIAPOLDUS_TEST_SECRET"); got != "from-env" {
		t.Fatalf("resolveSecret env = %q", got)
	}
}

func TestResolveSecretFileReference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(" from-file \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolveSecret("file:" + path); got != "from-file" {
		t.Fatalf("resolveSecret file = %q", got)
	}
}
