package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Liapoldus/core/internal/runtime"
	_ "modernc.org/sqlite"
)

func main() {
	root, err := os.MkdirTemp("", "core-sqlite-integrity-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(root)
	path := filepath.Join(root, "core.sqlite")
	ctx := context.Background()
	db, err := runtime.OpenDatabase(ctx, path)
	if err != nil {
		panic(err)
	}
	if err := db.Close(); err != nil {
		panic(err)
	}
	db, err = runtime.OpenDatabase(ctx, path)
	validReopen := err == nil
	if err == nil {
		_ = db.Close()
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		panic(err)
	}
	if _, err := raw.ExecContext(ctx, `PRAGMA foreign_keys = OFF; INSERT INTO operation_payloads(operation_id, version, resource, expected_revision, schema_version, digest) VALUES ('orphan', 1, 'fixture', 1, 1, 'digest');`); err != nil {
		panic(err)
	}
	if err := raw.Close(); err != nil {
		panic(err)
	}
	db, err = runtime.OpenDatabase(ctx, path)
	if err == nil {
		_ = db.Close()
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]bool{"validReopen": validReopen, "orphanRejected": err != nil}); err != nil {
		panic(err)
	}
}
