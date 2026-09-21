import { execFile } from "node:child_process";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const execute = promisify(execFile);
const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

async function probe(root: string, action: string, revision = "", digest = ""): Promise<Record<string, unknown>> {
  const args = ["run", "./tests/fixtures/snapshot-probe", root, action];
  if (revision) args.push(revision, digest);
  const result = await execute("go", args, { cwd: coreRoot });
  return JSON.parse(result.stdout) as Record<string, unknown>;
}

describe("filesystem snapshot store", () => {
  it("persists prepared, activated and drained snapshots across probe processes", async () => {
    const root = await mkdtemp(join(tmpdir(), "liapoldus-snapshot-store-"));
    await probe(root, "prepare", "r1", "d1");
    await probe(root, "activate", "r1", "d1");
    await probe(root, "prepare", "r2", "d2");
    const activated = await probe(root, "activate", "r2", "d2");
    expect(activated).toEqual({ activated: true, previousRevision: "r1", previousDigest: "d1" });
    expect(await probe(root, "active")).toEqual({ revision: "r2", digest: "d2" });
    await probe(root, "drain", "r1", "d1");
    expect(await probe(root, "drained")).toEqual({ revision: "r1" });
  });

  it("keeps the active snapshot unchanged when preparation fails", async () => {
    const root = await mkdtemp(join(tmpdir(), "liapoldus-snapshot-store-"));
    await probe(root, "prepare", "r1", "d1");
    await probe(root, "activate", "r1", "d1");
    const failed = await probe(root, "failed-prepare");
    expect(failed).toEqual({ failed: true, revision: "r1", digest: "d1" });
    expect(await probe(root, "active")).toEqual({ revision: "r1", digest: "d1" });
  });
});