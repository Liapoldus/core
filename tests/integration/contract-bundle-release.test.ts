import { createHash } from "node:crypto";
import { execFile } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");
const verifier = join(coreRoot, "scripts/verify-contract-bundle.mjs");

async function writeBundle(directory: string, files: Record<string, string>) {
  const digests = Object.fromEntries(
    Object.entries(files).map(([name, contents]) => [
      name,
      `sha256:${createHash("sha256").update(contents).digest("hex")}`,
    ]),
  );
  for (const [name, contents] of Object.entries(files)) {
    await writeFile(join(directory, name), contents, "utf8");
  }
  await writeFile(
    join(directory, "manifest.json"),
    JSON.stringify({ version: "liapoldus.gateway.v1", files: digests }),
    "utf8",
  );
}

describe("Gateway release contract bundle", () => {
  it("accepts a bundle whose payload and SHA-256 values match the manifest", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-contract-bundle-"));
    try {
      await writeBundle(directory, { "errors.json": "{}", "gateway.schema.json": "{}" });
      await expect(execFileAsync("node", [verifier, directory])).resolves.toBeDefined();
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });

  it("rejects payload files not declared in the manifest", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-contract-bundle-"));
    try {
      await writeBundle(directory, { "errors.json": "{}" });
      await writeFile(join(directory, "untracked.json"), "{}", "utf8");
      await expect(execFileAsync("node", [verifier, directory])).rejects.toMatchObject({
        stderr: expect.stringContaining("unmanifested contract payload"),
      });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
