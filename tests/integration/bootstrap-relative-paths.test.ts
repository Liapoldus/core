import { execFile, spawn } from "node:child_process";
import { mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { buildCoreTestBinary } from "../support/core.js";
import { freeAddress } from "../support/http.js";
import { request as httpsRequest } from "node:https";

const execFileAsync = promisify(execFile);

describe("bootstrap path resolution", () => {
  it("resolves state and file references from core.yaml when CLI runs elsewhere", async () => {
    const root = await mkdtemp(join(tmpdir(), "liapoldus-bootstrap-relative-"));
    const configurationDirectory = join(root, "configuration");
    const workingDirectory = join(root, "operator-cwd");
    const certificateDirectory = join(configurationDirectory, "certificates");
    const database = join(configurationDirectory, "state", "core.db");
    const binary = await buildCoreTestBinary();
    const address = await freeAddress();
    const pluginControlAddress = await freeAddress();
    let core: { process: ReturnType<typeof spawn>; stop(): Promise<void> } | undefined;
    await Promise.all([
      import("node:fs/promises").then(({ mkdir }) => mkdir(certificateDirectory, { recursive: true })),
      import("node:fs/promises").then(({ mkdir }) => mkdir(workingDirectory, { recursive: true })),
    ]);

    try {
      await execFileAsync("openssl", [
        "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
        "-subj", "/CN=localhost",
        "-keyout", join(certificateDirectory, "management.key"),
        "-out", join(certificateDirectory, "management.crt"),
      ]);
      await execFileAsync("openssl", [
        "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
        "-subj", "/CN=plugin-control",
        "-keyout", join(certificateDirectory, "plugin-control.key"),
        "-out", join(certificateDirectory, "plugin-control.crt"),
      ]);
      await execFileAsync("openssl", [
        "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
        "-subj", "/CN=plugin-replica-ca",
        "-keyout", join(certificateDirectory, "plugin-replica-ca.key"),
        "-out", join(certificateDirectory, "plugin-replica-ca.crt"),
      ]);
      const config = join(configurationDirectory, "core.yaml");
      await writeFile(config, [
        "state:",
        "  path: state/core.db",
        "management:",
        `  listen: ${address}`,
        "  tls:",
        "    certificate: file:certificates/management.crt",
        "    key: file:certificates/management.key",
        "pluginControl:",
        `  listen: ${pluginControlAddress}`,
        `  publicURL: https://${pluginControlAddress}`,
        "  tls:",
        "    certificate: file:certificates/plugin-control.crt",
        "    key: file:certificates/plugin-control.key",
        "    replicaClientCA: file:certificates/plugin-replica-ca.crt",
        "",
      ].join("\n"), "utf8");

      await execFileAsync(binary, ["--config", config, "access", "bootstrap"], { cwd: workingDirectory });
      const databaseBytes = await readFile(database);
      expect(databaseBytes.byteLength).toBeGreaterThan(0);
      const child = spawn(binary, ["--config", config, "serve"], { cwd: workingDirectory, stdio: "ignore" });
      core = {
        process: child,
        stop: () => new Promise((resolve) => {
          if (child.exitCode !== null || child.signalCode !== null) {
            resolve();
            return;
          }
          child.once("close", () => resolve());
          child.kill("SIGTERM");
        }),
      };
      let healthStatus = 0;
      for (let attempt = 0; attempt < 120; attempt += 1) {
        if (core.process.exitCode !== null) break;
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
      expect((await stat(join(configurationDirectory, "state"))).isDirectory()).toBe(true);
    } finally {
      if (core !== undefined) await core.stop();
      await rm(root, { recursive: true, force: true });
    }
  }, 120_000);
});
