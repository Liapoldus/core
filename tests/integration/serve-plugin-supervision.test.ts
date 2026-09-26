import { execFile } from "node:child_process";
import { access, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { request as httpsRequest } from "node:https";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { buildGatewayTestBinary, startGateway } from "../support/gateway.js";
import { freeAddress } from "../support/http.js";

const execFileAsync = promisify(execFile);

async function buildFixture(name: string, output: string): Promise<void> {
  await execFileAsync("go", ["build", "-o", output, `./tests/fixtures/${name}`], {
    cwd: join(import.meta.dirname, "../.."),
  });
}

async function waitForManagement(address: string, gateway: Awaited<ReturnType<typeof startGateway>>["process"]): Promise<void> {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (gateway.exitCode !== null) throw new Error(`Gateway exited before readiness (${gateway.exitCode}).`);
    try {
      const ready = await new Promise<boolean>((resolve, reject) => {
        const request = httpsRequest({
          hostname: "127.0.0.1",
          port: Number(address.slice(address.lastIndexOf(":") + 1)),
          path: "/healthz",
          rejectUnauthorized: false,
        }, (response) => {
          response.resume();
          response.once("end", () => resolve(response.statusCode === 200));
        });
        request.once("error", reject);
        request.end();
      });
      if (ready) return;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  }
  throw new Error("Gateway Management API did not become ready.");
}

async function waitForPlugin(address: string): Promise<Response> {
  let lastStatus = 0;
  for (let attempt = 0; attempt < 100; attempt += 1) {
    try {
      const response = await fetch(`http://${address}/submit`);
      lastStatus = response.status;
      if (response.status === 200) return response;
      await response.body?.cancel();
    } catch {
      // The first call intentionally crashes the child, then the Supervisor must restart it.
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`Supervised plugin never recovered; last status was ${lastStatus}.`);
}

describe("serve local plugin supervision", () => {
  it("starts a SQLite-configured child, dispatches through Caddy, restarts it after exit, and stops it with Gateway", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-plugin-supervision-"));
    const gatewayBinary = await buildGatewayTestBinary();
    const pluginBinary = join(directory, "plugin-child");
    const managementAddress = await freeAddress();
    const publicAddress = await freeAddress();
    const certificate = join(directory, "management.crt");
    const privateKey = join(directory, "management.key");
    const database = join(directory, "gateway.db");
    const artifacts = join(directory, "artifacts");
    const config = join(directory, "gateway.yaml");
    const marker = `${pluginBinary}.starts`;
    const secretFile = join(directory, "plugin-secret.txt");
    let gateway: Awaited<ReturnType<typeof startGateway>> | undefined;

    try {
      await buildFixture("serve-plugin-child", pluginBinary);
      await writeFile(secretFile, "secret-dsn-for-fixture", { mode: 0o600 });
      await execFileAsync("openssl", [
        "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
        "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1",
        "-keyout", privateKey, "-out", certificate,
      ]);
      await writeFile(config, [
        "state:", `  path: ${database}`,
        "artifacts:", `  path: ${artifacts}`,
        "management:", `  listen: ${managementAddress}`,
        "  tls:", `    certificate: file:${certificate}`, `    key: file:${privateKey}`,
        "caddy:", "  variant: embedded", "",
      ].join("\n"), "utf8");
      const bootstrap = await execFileAsync(gatewayBinary, ["--config", config, "access", "bootstrap"]);
      expect(bootstrap.stdout.trim()).not.toHaveLength(0);
      await execFileAsync("go", ["run", "./tests/fixtures/serve-plugin-composition", database, artifacts, pluginBinary, secretFile], {
        cwd: join(import.meta.dirname, "../.."),
        env: { ...process.env, LIAPOLDUS_TEST_PUBLIC_ADDRESS: publicAddress },
      });

      gateway = await startGateway(["--config", config, "serve"], { LIAPOLDUS_TEST_SENTINEL: "must-not-reach-plugin" });
      await waitForManagement(managementAddress, gateway.process);
      const response = await waitForPlugin(publicAddress);
      expect(await response.text()).toBe("recovered");
      const starts = (await readFile(marker, "utf8")).trim().split("\n");
      expect(starts.length).toBeGreaterThanOrEqual(2);
      await expect(access(`${pluginBinary}.inherited-environment`)).rejects.toMatchObject({ code: "ENOENT" });

      await gateway.stop();
      gateway = undefined;
      const pids = starts.map((value) => Number(value)).filter(Number.isSafeInteger);
      expect(pids.length).toBeGreaterThanOrEqual(2);
      await expectNoProcess(pids[pids.length - 1]);
    } finally {
      if (gateway !== undefined) await gateway.stop();
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);
});

async function expectNoProcess(pid: number): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 100));
  try {
    process.kill(pid, 0);
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ESRCH") return;
    throw error;
  }
  throw new Error(`Plugin process ${pid} survived Gateway shutdown.`);
}
