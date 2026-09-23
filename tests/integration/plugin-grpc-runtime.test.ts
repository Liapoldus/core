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
import { startGateway } from "../support/gateway.js";
import { freeAddress, request, waitReady, writeGatewayConfig } from "../support/http.js";

const execFileAsync = promisify(execFile);
const root = fileURLToPath(new URL("../..", import.meta.url));
const vectors = JSON.parse(readFileSync(resolve(import.meta.dirname, "../../contracts/v1/golden-vectors.json"), "utf8")) as {
  vectors: Array<{
    id: string;
    input: { frame: string; payloadHex: string };
    expected: { payloadEncoding: string };
  }>;
};
const tcpVector = vectors.vectors.find(({ id }) => id === "plugin-stream-tcp-bytes");
if (!tcpVector) throw new Error("plugin-stream-tcp-bytes vector is missing");
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

  it("uses one typed gRPC Stream lifecycle for a configured TCP connection", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-grpc-plugin-l4-"));
    const binary = join(directory, "forms-plugin");
    await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/plugin-grpc"], { cwd: root });
    const address = await freeAddress();
    const config = await writeGatewayConfig([
      "plugins:",
      "  forms:",
      `    binary: ${JSON.stringify(binary)}`,
      "    capabilities: [tcp.echo]",
      "    settings: {}",
      "listeners:",
      "  stream:",
      "    type: tcp",
      `    address: ${address}`,
      "    rules:",
      "      - then:",
      "          plugin: { instance: forms, capability: tcp.echo }",
    ].join("\n"));
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitTCP(address);

    const payload = Buffer.from(tcpVector.input.payloadHex, "hex");
    const response = await new Promise<Buffer>((resolve, reject) => {
      const socket = net.createConnection({ host: "127.0.0.1", port: Number(address.split(":").at(-1)) });
      const chunks: Buffer[] = [];
      socket.once("error", reject);
      socket.on("data", (chunk: Buffer) => { chunks.push(Buffer.from(chunk)); });
      socket.once("end", () => resolve(Buffer.concat(chunks)));
      socket.once("connect", () => socket.end(payload));
    });

    expect(tcpVector.expected).toEqual({ payloadEncoding: "raw-bytes" });
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
      "    limits: { memory: 64MiB }",
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
    expect(response.status).toBe(503);
    expect(JSON.parse(response.text).code).toBe("resource_exhausted");

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
