import { execFile } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { startGateway } from "../support/gateway.js";
import { freeAddress, request, waitReady } from "../support/http.js";

const execFileAsync = promisify(execFile);
const root = fileURLToPath(new URL("../..", import.meta.url));
const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
const directories: string[] = [];

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    if (gateway.process.exitCode === null) gateway.process.kill("SIGTERM");
    await gateway.stop();
  }
  for (const directory of directories.splice(0)) await rm(directory, { recursive: true, force: true });
});

describe("Gateway scoped plugin secret grants", () => {
  it("redeems a route-scoped secret through typed RPC without putting it in Call JSON", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-plugin-grant-"));
    directories.push(directory);
    const binary = join(directory, "grant-plugin");
    const address = await freeAddress();
    await writeFile(join(directory, "dns-token"), "fixture-secret-material", { mode: 0o600 });
    const config = join(directory, "gateway.yaml");
    await writeFile(config, [
      "secrets:",
      "  dnsToken: file:./dns-token",
      "plugins:",
      "  forms:",
      `    binary: ${JSON.stringify(binary)}`,
      "    settings: {}",
      "    capabilities: [forms.submit]",
      "    grants:",
      "      secrets:",
      "        - name: dnsToken",
      "          purpose: acme-dns01",
      "          domains: [example.com]",
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${address}`,
      "    routes:",
      "      - when:",
      "          path: { exact: /submit }",
      "        then:",
      "          plugin:",
      "            instance: forms",
      "            capability: forms.submit",
      "            context:",
      "              secrets: [dnsToken]",
    ].join("\n"), "utf8");
    await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/plugin-grant"], { cwd: root });
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);

    const response = await request(address, "/submit", { method: "POST", body: "hello" });

    expect(response.status).toBe(200);
    expect(JSON.parse(response.text)).toEqual({
      redeemed: true,
      wrongPurposeDenied: true,
      outOfScopeDenied: true,
      expiredHandleDenied: false,
      secretInCallJSON: false,
    });
    expect(response.text).not.toContain("fixture-secret-material");

    const repeated = await request(address, "/submit", { method: "POST", body: "hello again" });
    expect(JSON.parse(repeated.text)).toEqual({
      redeemed: false,
      wrongPurposeDenied: false,
      outOfScopeDenied: false,
      expiredHandleDenied: true,
      secretInCallJSON: false,
    });
    expect(repeated.text).not.toContain("fixture-secret-material");
  }, 60_000);
});
