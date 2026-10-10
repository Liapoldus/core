import { spawn } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { request as httpsRequest } from "node:https";
import { describe, expect, it } from "vitest";
import { buildCoreTestBinary } from "../support/core.js";
import { freeAddress } from "../support/http.js";
import { initializeCore } from "../support/initialize.js";

describe("environment bootstrap", () => {
  it("uses the absolute SQLite path when commands run outside the project directory", async () => {
    const root = await mkdtemp(join(tmpdir(), "liapoldus-core-env-"));
    const configDirectory = join(root, "configuration");
    const workingDirectory = join(root, "operator-cwd");
    await Promise.all([mkdir(configDirectory), mkdir(workingDirectory)]);
    const binary = await buildCoreTestBinary();
    const address = await freeAddress();
    const controlAddress = await freeAddress();
    const initialized = await initializeCore(binary, configDirectory, address, controlAddress);
    let core: ReturnType<typeof spawn> | undefined;

    try {
      // Core has no init subcommand: the standalone CLI owns initialization.
      // The already initialized absolute state path is the only bootstrap input
      // consumed by this binary.
      expect((await readFile(initialized.database)).byteLength).toBeGreaterThan(0);
      core = spawn(binary, ["serve"], { cwd: workingDirectory, env: initialized.environment, stdio: "ignore" });

      let healthStatus = 0;
      for (let attempt = 0; attempt < 120; attempt += 1) {
        if (core.exitCode !== null) break;
        try {
          healthStatus = await new Promise<number>((resolve, reject) => {
            const port = Number(address.slice(address.lastIndexOf(":") + 1));
            const requestValue = httpsRequest({ hostname: "127.0.0.1", port, path: "/healthz", rejectUnauthorized: false }, (response) => {
              response.resume();
              response.once("end", () => resolve(response.statusCode ?? 0));
            });
            requestValue.once("error", reject);
            requestValue.end();
          });
          if (healthStatus === 200) break;
        } catch {
          await new Promise((resolve) => setTimeout(resolve, 50));
        }
      }
      expect(healthStatus).toBe(200);
      expect((await stat(configDirectory)).isDirectory()).toBe(true);
    } finally {
      if (core !== undefined && core.exitCode === null && core.signalCode === null) {
        core.kill("SIGTERM");
        await new Promise<void>((resolve) => core?.once("close", () => resolve()));
      }
      await rm(root, { recursive: true, force: true });
    }
  }, 120_000);
});
