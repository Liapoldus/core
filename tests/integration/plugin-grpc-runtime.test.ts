import { execFile } from "node:child_process";
import net from "node:net";
import { mkdtemp, readFile } from "node:fs/promises";
import { readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { startGateway, startGatewayWithOutput } from "../support/gateway.js";
import { freeAddress, request, waitReady, writeGatewayConfig } from "../support/http.js";

const execFileAsync = promisify(execFile);
const root = fileURLToPath(new URL("../..", import.meta.url));
const vectors = JSON.parse(readFileSync(resolve(import.meta.dirname, "../../contracts/v1/golden-vectors.json"), "utf8")) as {
  vectors: Array<{
    id: string;
    input: { payloadHex: string };
    expected: { payloadEncoding: string };
  }>;
};
const tcpVector = vectors.vectors.find(({ id }) => id === "plugin-stream-tcp-bytes");
if (!tcpVector) throw new Error("plugin-stream-tcp-bytes vector is missing");
const tcpPluginVector = vectors.vectors.find(({ id }) => id === "tcp-plugin-protocol");
if (!tcpPluginVector) throw new Error("tcp-plugin-protocol vector is missing");
const startupVector = vectors.vectors.find(({ id }) => id === "plugin-startup-order");
if (!startupVector) throw new Error("plugin-startup-order vector is missing");
const gatewaySchema = JSON.parse(readFileSync(resolve(root, "assets/contracts/gateway.schema.json"), "utf8")) as {
  $defs: {
    plugin: {
      properties: {
        limits: { default: { startTimeout: string; timeout: string } };
      };
    };
  };
};
const memoryVectors = JSON.parse(readFileSync(resolve(import.meta.dirname, "../../contracts/v1/golden-vectors.json"), "utf8")) as {
  vectors: Array<{
    id: string;
    input: { rss: string; limit: string };
    expected: { processTerminated: boolean; status: number; code: string };
  }>;
};
const memoryVector = memoryVectors.vectors.find(({ id }) => id === "plugin-memory-limit");
if (!memoryVector) throw new Error("plugin-memory-limit vector is missing");
const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];

async function waitTCP(address: string): Promise<void> {
  const port = Number(address.split(":").at(-1));
  for (let attempt = 0; attempt < 100; attempt += 1) {
    const connected = await new Promise<boolean>((resolve) => {
      const socket = net.createConnection({ host: "127.0.0.1", port });
      socket.once("connect", () => { socket.destroy(); resolve(true); });
      socket.once("error", () => resolve(false));
    });
    if (connected) return;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error("Gateway TCP listener did not become ready");
}

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    if (gateway.process.exitCode === null) gateway.process.kill("SIGTERM");
    await gateway.stop();
  }
});

describe("Gateway gRPC plugin process lifecycle", () => {
  it("uses startTimeout for handshake independently of the call timeout", async () => {
    expect(startupVector.expected).toMatchObject({ startTimeout: gatewaySchema.$defs.plugin.properties.limits.default.startTimeout });
    expect(gatewaySchema.$defs.plugin.properties.limits.default).toMatchObject({ startTimeout: "10s", timeout: "5s" });
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-grpc-plugin-start-timeout-"));
    const binary = join(directory, "forms-plugin");
    await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/plugin-grpc"], { cwd: root });
    const config = await writeGatewayConfig([
      "plugins:",
      "  forms:",
      `    binary: ${JSON.stringify(binary)}`,
      "    capabilities: [forms.submit]",
      "    settings: {}",
      "    limits: { startTimeout: 250ms, timeout: 15s }",
      "listeners: {}",
    ].join("\n"));
    const gateway = await startGatewayWithOutput(["--config", config, "serve", "--no-management"], {
      LIAPOLDUS_FIXTURE_MANIFEST_DELAY: "1s",
    });
    const startedAt = Date.now();
    const exitCode = await new Promise<number | null>((resolve) => {
      const timeout = setTimeout(() => resolve(null), 5_000);
      gateway.process.once("close", (code) => {
        clearTimeout(timeout);
        resolve(code);
      });
    });
    if (exitCode === null) await gateway.stop();
    const elapsed = Date.now() - startedAt;
    expect(exitCode).not.toBeNull();
    expect(exitCode).not.toBe(0);
    expect(elapsed).toBeGreaterThanOrEqual(150);
    expect(elapsed).toBeLessThan(5_000);
  }, 60_000);

  it("rejects startup when the plugin manifest omits a configured capability", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-grpc-plugin-capability-mismatch-"));
    const binary = join(directory, "forms-plugin");
    await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/plugin-grpc"], { cwd: root });
    const address = await freeAddress();
    const config = await writeGatewayConfig([
      "plugins:",
      "  forms:",
      `    binary: ${JSON.stringify(binary)}`,
      "    capabilities: [forms.submit]",
      "    env: [LIAPOLDUS_FIXTURE_MANIFEST_CAPABILITIES=forms.other]",
      "    settings: {}",
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${address}`,
      "    routes: []",
    ].join("\n"));
    const gateway = await startGatewayWithOutput(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);
    const exitCode = await new Promise<number | null>((resolve) => {
      const timeout = setTimeout(() => resolve(null), 3_000);
      gateway.process.once("close", (code) => {
        clearTimeout(timeout);
        resolve(code);
      });
    });
    if (exitCode === null) await gateway.stop();

    expect(exitCode).not.toBeNull();
    expect(exitCode).not.toBe(0);
    expect(gateway.stdout).not.toContain("forms.other");
    expect(gateway.stderr).not.toContain("forms.other");
  }, 60_000);

  it("starts a child plugin, handshakes, dispatches JSON Call, and strips secrets", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-grpc-plugin-"));
    const binary = join(directory, "forms-plugin");
    await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/plugin-grpc"], { cwd: root });
    const address = await freeAddress();
    const config = await writeGatewayConfig([
      "plugins:",
      "  forms:",
      `    binary: ${JSON.stringify(binary)}`,
      "    capabilities: [forms.submit]",
      "    settings: {}",
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${address}`,
      "    routes:",
      "      - when:",
      "          path: { exact: /submit }",
      "        then:",
      "          plugin: { instance: forms, capability: forms.submit }",
    ].join("\n"));
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);

    const response = await request(address, "/submit", {
      method: "POST",
      headers: { Authorization: "Bearer must-not-cross-boundary", Cookie: "sid=must-not-cross-boundary" },
      body: "submission",
    });

    expect(response.status).toBe(200);
    expect(JSON.parse(response.text)).toEqual({
      method: "POST",
      path: "/submit",
      body: "submission",
      authorizationPresent: false,
      cookiePresent: false,
    });
  }, 60_000);

  it.each([tcpVector, tcpPluginVector])("uses a typed gRPC Stream for TCP raw-byte vector $id", async (vector) => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-grpc-plugin-l4-"));
    const binary = join(directory, "forms-plugin");
    await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/plugin-grpc"], { cwd: root });
    const address = await freeAddress();
    const config = await writeGatewayConfig([
      "plugins:",
      "  forms:",
      `    binary: ${JSON.stringify(binary)}`,
      "    capabilities: [peer.session]",
      "    settings: {}",
      "listeners:",
      "  stream:",
      "    type: tcp",
      `    address: ${address}`,
      "    rules:",
      "      - then:",
      "          plugin: { instance: forms, capability: peer.session }",
    ].join("\n"));
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitTCP(address);

    const payload = Buffer.from(vector.input.payloadHex, "hex");
    const response = await new Promise<Buffer>((resolve, reject) => {
      const socket = net.createConnection({ host: "127.0.0.1", port: Number(address.split(":").at(-1)) });
      const chunks: Buffer[] = [];
      socket.once("error", reject);
      socket.on("data", (chunk: Buffer) => { chunks.push(Buffer.from(chunk)); });
      socket.once("end", () => resolve(Buffer.concat(chunks)));
      socket.once("connect", () => socket.end(payload));
    });

    expect(vector.expected.payloadEncoding).toBe("raw-bytes");
    expect(response).toEqual(Buffer.concat([Buffer.from("stream:"), payload]));
  }, 60_000);

  it("limits concurrent capability calls to the configured per-plugin bound", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-grpc-plugin-limit-"));
    const binary = join(directory, "forms-plugin");
    await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/plugin-grpc"], { cwd: root });
    const address = await freeAddress();
    const config = await writeGatewayConfig([
      "plugins:",
      "  forms:",
      `    binary: ${JSON.stringify(binary)}`,
      "    capabilities: [forms.concurrent]",
      "    limits: { calls: 1, timeout: 5s }",
      "    settings: {}",
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${address}`,
      "    routes:",
      "      - when: { path: { exact: /concurrent } }",
      "        then:",
      "          plugin: { instance: forms, capability: forms.concurrent }",
    ].join("\n"));
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);

    const responses = await Promise.all([
      request(address, "/concurrent"),
      request(address, "/concurrent"),
    ]);
    expect(responses.map((response) => JSON.parse(response.text))).toEqual([
      { maxConcurrent: 1 },
      { maxConcurrent: 1 },
    ]);
  }, 60_000);

  it("maps an expired plugin call deadline to the plugin_timeout problem", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-grpc-plugin-timeout-"));
    const binary = join(directory, "forms-plugin");
    await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/plugin-grpc"], { cwd: root });
    const address = await freeAddress();
    const config = await writeGatewayConfig([
      "plugins:",
      "  forms:",
      `    binary: ${JSON.stringify(binary)}`,
      "    capabilities: [forms.slow]",
      "    limits: { timeout: 2s }",
      "    settings: {}",
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${address}`,
      "    routes:",
      "      - when: { path: { exact: /slow } }",
      "        then:",
      "          plugin: { instance: forms, capability: forms.slow }",
    ].join("\n"));
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);

    const response = await request(address, "/slow");
    expect(response.status).toBe(504);
    expect(JSON.parse(response.text).code).toBe("plugin_timeout");
  }, 60_000);

  it("restarts a plugin process after an unexpected child exit", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-grpc-plugin-restart-"));
    const binary = join(directory, "forms-plugin");
    const marker = join(directory, "crashed-once");
    await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/plugin-grpc"], { cwd: root });
    const address = await freeAddress();
    const config = await writeGatewayConfig([
      "plugins:",
      "  forms:",
      `    binary: ${JSON.stringify(binary)}`,
      "    env:",
      `      - ${JSON.stringify(`LIAPOLDUS_FIXTURE_CRASH_MARKER=${marker}`)}`,
      "    capabilities: [forms.crash-once]",
      "    settings: {}",
      "    restart: { enabled: true, backoff: 100ms, maxBackoff: 500ms }",
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${address}`,
      "    routes:",
      "      - when: { path: { exact: /crash } }",
      "        then:",
      "          plugin: { instance: forms, capability: forms.crash-once }",
    ].join("\n"));
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);

    const crashed = await request(address, "/crash");
    expect(crashed.status).toBe(502);

    let recovered = false;
    for (let attempt = 0; attempt < 40; attempt += 1) {
      const response = await request(address, "/crash");
      if (response.status === 200) {
        recovered = true;
        break;
      }
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    expect(recovered).toBe(true);
  }, 60_000);

  it("restarts a plugin whose resident memory exceeds its configured RSS limit", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-grpc-plugin-memory-"));
    const binary = join(directory, "forms-plugin");
    const marker = join(directory, "starts");
    await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/plugin-grpc"], { cwd: root });
    const address = await freeAddress();
    const config = await writeGatewayConfig([
      "plugins:",
      "  forms:",
      `    binary: ${JSON.stringify(binary)}`,
      "    env:",
      `      - ${JSON.stringify(`LIAPOLDUS_FIXTURE_START_MARKER=${marker}`)}`,
      "    capabilities: [forms.memory]",
      `    limits: { memory: ${memoryVector.input.limit} }`,
      "    settings: {}",
      "    restart: { enabled: true, backoff: 100ms, maxBackoff: 500ms }",
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${address}`,
      "    routes:",
      "      - when: { path: { exact: /memory } }",
      "        then:",
      "          plugin: { instance: forms, capability: forms.memory }",
    ].join("\n"));
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);

    const response = await request(address, "/memory");
    expect(memoryVector.input).toEqual({ rss: "257MiB", limit: "256MiB" });
    expect(response.status).toBe(memoryVector.expected.status);
    expect(JSON.parse(response.text).code).toBe(memoryVector.expected.code);
    expect(memoryVector.expected.processTerminated).toBe(true);

    let startCount = 0;
    for (let attempt = 0; attempt < 100; attempt += 1) {
      try {
        startCount = (await readFile(marker, "utf8")).trim().split("\n").length;
      } catch {
        // The plugin process may not have written its first marker yet.
      }
      if (startCount >= 2) break;
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    expect(startCount).toBeGreaterThanOrEqual(2);
  }, 60_000);
});
