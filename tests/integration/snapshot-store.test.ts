import { execFile } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const execute = promisify(execFile);
const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

async function runProbe(source: string): Promise<Record<string, unknown>> {
  const directory = await mkdtemp(join(coreRoot, "tests", ".snapshot-probe-"));
  const program = join(directory, "main.go");
  try {
    await writeFile(program, source);
    const result = await execute("go", ["run", program], { cwd: coreRoot });
    return JSON.parse(result.stdout) as Record<string, unknown>;
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
}

describe("runtime snapshots", () => {
  it("keeps the active snapshot when preparation fails and exposes active revision metadata", async () => {
    const result = await runProbe(`
package main
import (
  "encoding/json"
  "fmt"
  "github.com/Liapoldus/core/internal/application"
  "github.com/Liapoldus/core/internal/domain/models"
  "github.com/Liapoldus/core/internal/infrastructure/storage"
)
func snapshot(revision, digest string) models.Snapshot {
  return models.Snapshot{Graph: models.CompiledGraph{Revision: models.Revision{Value: revision, Digest: digest}}}
}
func main() {
  store := storage.NewMemorySnapshotStore()
  service := application.RuntimeService{Store: store}
  first := service.Apply(snapshot("r1", "d1"))
  invalid := service.Apply(snapshot("", ""))
  active := store.Active()
  bytes, _ := json.Marshal(map[string]any{
    "first": first == nil,
    "invalid": invalid != nil,
    "revision": active.Graph.Revision.Value,
    "digest": active.Graph.Revision.Digest,
  })
  fmt.Print(string(bytes))
}`);

    expect(result).toEqual({ first: true, invalid: true, revision: "r1", digest: "d1" });
  });

  it("activates only prepared snapshots and drains the retired snapshot", async () => {
    const result = await runProbe(`
package main
import (
  "encoding/json"
  "fmt"
  "github.com/Liapoldus/core/internal/application"
  "github.com/Liapoldus/core/internal/domain/models"
  "github.com/Liapoldus/core/internal/infrastructure/storage"
)
func snapshot(revision string) models.Snapshot {
  return models.Snapshot{Graph: models.CompiledGraph{Revision: models.Revision{Value: revision, Digest: revision}}}
}
func main() {
  store := storage.NewMemorySnapshotStore()
  service := application.RuntimeService{Store: store}
  _ = service.Apply(snapshot("r1"))
  _ = service.Apply(snapshot("r2"))
  bytes, _ := json.Marshal(map[string]any{
    "active": store.Active().Graph.Revision.Value,
    "drained": store.DrainedRevision(),
  })
  fmt.Print(string(bytes))
}`);

    expect(result).toEqual({ active: "r2", drained: "r1" });
  });
});
