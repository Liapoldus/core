import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { buildGatewayTestBinary } from "../support/gateway.js";

const execFileAsync = promisify(execFile);

describe("bootstrap path resolution", () => {
  it("resolves state and file references from gateway.yaml when CLI runs elsewhere", async () => {
    const root = await mkdtemp(join(tmpdir(), "liapoldus-bootstrap-relative-"));
    const configurationDirectory = join(root, "configuration");
    const workingDirectory = join(root, "operator-cwd");
    const certificateDirectory = join(configurationDirectory, "certificates");
    const database = join(configurationDirectory, "state", "gateway.db");
    const binary = await buildGatewayTestBinary();
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
      const config = join(configurationDirectory, "gateway.yaml");
      await writeFile(config, [
        "state:",
        "  path: state/gateway.db",
        "artifacts:",
        "  path: artifacts",
        "management:",
        "  listen: 127.0.0.1:0",
        "  tls:",
        "    certificate: file:certificates/management.crt",
        "    key: file:certificates/management.key",
        "caddy:",
        "  variant: embedded",
        "",
      ].join("\n"), "utf8");

      await execFileAsync(binary, ["--config", config, "access", "bootstrap"], { cwd: workingDirectory });
      const databaseBytes = await readFile(database);
      expect(databaseBytes.byteLength).toBeGreaterThan(0);
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  }, 120_000);
});
