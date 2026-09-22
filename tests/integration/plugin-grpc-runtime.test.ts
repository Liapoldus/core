import { execFile } from "node:child_process";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { startGateway } from "../support/gateway.js";
import { freeAddress, request, waitReady, writeGatewayConfig } from "../support/http.js";

const execFileAsync = promisify(execFile);
const root = fileURLToPath(new URL("../..", import.meta.url));
const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];

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
});
