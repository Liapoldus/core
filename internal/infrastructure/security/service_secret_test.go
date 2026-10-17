package security

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadStagedServiceSecretUsesOnlyOpaqueDirectRegularFiles(t *testing.T) {
	root := t.TempDir()
	reference := "ssr:v1:0123456789abcdef0123456789abcdef"
	secret := []byte("opaque-secret-bytes")
	if err := os.WriteFile(filepath.Join(root, reference), secret, 0o400); err != nil {
		t.Fatal(err)
	}
	got, err := ReadStagedServiceSecret(root, reference, int64(len(secret)))
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("read staged secret: %q %v", got, err)
	}
	clear(got)
	for _, invalid := range []string{"file:/private/key.pem", "../escape", "ssr:v1:short"} {
		if _, err := ReadStagedServiceSecret(root, invalid, 128); err == nil || strings.Contains(err.Error(), root) || strings.Contains(err.Error(), invalid) {
			t.Fatalf("unsafe reference %q was read or leaked: %v", invalid, err)
		}
	}
	if _, err := ReadStagedServiceSecret(root, reference, int64(len(secret)-1)); err == nil {
		t.Fatal("oversized staged secret was accepted")
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, secret, 0o400); err != nil {
		t.Fatal(err)
	}
	link := "ssr:v1:fedcba9876543210fedcba9876543210"
	if err := os.Symlink(outside, filepath.Join(root, link)); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadStagedServiceSecret(root, link, 128); err == nil {
		t.Fatal("staged secret symlink was followed")
	}
	rootLink := filepath.Join(t.TempDir(), "secret-root-link")
	if err := os.Symlink(root, rootLink); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadStagedServiceSecret(rootLink, reference, 128); err == nil {
		t.Fatal("secret root symlink was followed")
	}
}
