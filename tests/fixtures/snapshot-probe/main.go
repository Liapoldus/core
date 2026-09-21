// Command snapshot-probe exercises the filesystem snapshot store from a
// specific root and prints a JSON result. It is driven by the TypeScript
// integration suites.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

func snapshot(revision, digest string) models.Snapshot {
	return models.Snapshot{Graph: models.CompiledGraph{Revision: models.Revision{Value: revision, Digest: digest}}}
}

func main() {
	layout, err := config.LoadSnapshotLayout()
	if err != nil {
		os.Exit(1)
	}
	root, action := os.Args[1], os.Args[2]
	store := storage.NewFilesystemSnapshotStore(root, layout)
	switch action {
	case "prepare":
		if _, err := store.Prepare(snapshot(os.Args[3], os.Args[4])); err != nil {
			os.Exit(1)
		}
		printOutput(map[string]any{"prepared": true})
	case "activate":
		previous, err := store.Activate(models.NewPreparedSnapshot(snapshot(os.Args[3], os.Args[4])))
		if err != nil {
			os.Exit(1)
		}
		printOutput(map[string]any{
			"activated":        true,
			"previousRevision": previous.Graph.Revision.Value,
			"previousDigest":   previous.Graph.Revision.Digest,
		})
	case "drain":
		if err := store.Drain(snapshot(os.Args[3], os.Args[4])); err != nil {
			os.Exit(1)
		}
		printOutput(map[string]any{"drained": true})
	case "active":
		active := store.Active()
		printOutput(map[string]any{"revision": active.Graph.Revision.Value, "digest": active.Graph.Revision.Digest})
	case "drained":
		printOutput(map[string]any{"revision": store.DrainedRevision()})
	case "failed-prepare":
		_, err := store.Prepare(snapshot("", ""))
		active := store.Active()
		printOutput(map[string]any{
			"failed":   err != nil,
			"revision": active.Graph.Revision.Value,
			"digest":   active.Graph.Revision.Digest,
		})
	}
}

func printOutput(output map[string]any) {
	bytes, _ := json.Marshal(output)
	fmt.Print(string(bytes))
}