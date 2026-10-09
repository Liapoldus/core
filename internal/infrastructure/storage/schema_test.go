package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestSchemaPreservesMigrationHistory(t *testing.T) {
	sum := sha256.Sum256([]byte(SchemaSQL))
	if hex.EncodeToString(sum[:]) != "8dd019876826ae1b74e25ab7c10b128c8475f62215ee109cedbe3b92c6e356fd" {
		t.Fatal("schema differs from pre-migration history")
	}
}
