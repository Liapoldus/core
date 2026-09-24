import { execFile } from "node:child_process";
import { readFile } from "node:fs/promises";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

describe("embedded Caddy build", () => {
  it("registers the contracted HTTP and Layer 4 modules in the Gateway binary", async () => {
    const contract = JSON.parse(
      await readFile(new URL("../../assets/contracts/caddy-build.json", import.meta.url), "utf8"),
    ) as { requiredModules: string[] };
    const result = await execFileAsync("go", ["run", "./tests/fixtures/caddy-module-probe"], {
      cwd: coreRoot,
      timeout: 120_000,
    });
    const registered = new Set(JSON.parse(result.stdout) as string[]);
    expect(contract.requiredModules.filter((module) => !registered.has(module))).toEqual([]);
  }, 150_000);
});
