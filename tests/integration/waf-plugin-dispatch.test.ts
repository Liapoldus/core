import { execFile } from "node:child_process";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { runGateway, startGateway } from "../support/gateway.js";
import { request, waitReady, writeGatewayConfig, freeAddress } from "../support/http.js";
import { startUpstream } from "../support/upstream.js";

const execFileAsync = promisify(execFile);
const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
const upstreams: Array<{ stop(): Promise<void>; hits(): { requests: number } }> = [];

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    if (gateway.process.exitCode === null) gateway.process.kill("SIGTERM");
    await gateway.stop();
  }
  for (const upstream of upstreams.splice(0)) await upstream.stop();
});

describe("generic WAF capability dispatch", () => {
  it("delegates the decision to the configured capability without forwarding credentials", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-waf-plugin-"));
    const binary = join(directory, "policy-fixture");
    await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/plugin-grpc"], { cwd: coreRoot });
    const upstream = await startUpstream();
    upstreams.push(upstream);
    const address = await freeAddress();
    const config = await writeGatewayConfig([
      "plugins:",
      "  policy:",
      `    binary: ${JSON.stringify(binary)}`,
      "    capabilities: [edge.policy]",
      "    env: [LIAPOLDUS_FIXTURE_MANIFEST_CAPABILITIES=edge.policy, LIAPOLDUS_FIXTURE_MANIFEST_NAME=policy]",
      "    settings: {}",
      "authPolicies:",
      "  protected:",
      "    plugin: { instance: policy, capability: edge.policy }",
      "wafPolicies:",
      "  protect:",
      "    rules:",
      "      - when: { path: { prefix: /private } }",
      "        then: { plugin: { instance: policy, capability: edge.policy } }",
      "upstreams:",
      "  api:",
      `    targets: [{ address: ${upstream.address} }]`,
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${address}`,
      "    routes:",
      "      - when: { path: { prefix: /login } }",
      "        then: { auth: protected, proxy: { upstream: api } }",
      "      - when: { path: { prefix: / } }",
      "        then: { proxy: { upstream: api }, waf: protect }",
    ].join("\n"));
    const validation = await runGateway(["--output", "json", "config", "validate", config]);
    expect(validation.exitCode, validation.stdout + validation.stderr).toBe(0);
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);
    const upstreamRequestsBefore = upstream.hits().requests;

    const response = await request(address, "/private/resource", {
      headers: { Authorization: "Bearer do-not-forward", Cookie: "session=do-not-forward" },
    });

    expect(response.status).toBe(403);
    expect(response.headers.get("x-policy-decision")).toBe("plugin");
    expect(response.headers.get("x-credentials-forwarded")).toBe("false");
    expect(response.text).toBe("request denied by configured capability");
    expect(upstream.hits().requests).toBe(upstreamRequestsBefore);

    const authResponse = await request(address, "/login/resource", {
      headers: { Authorization: "Bearer do-not-forward", Cookie: "session=do-not-forward" },
    });
    expect(authResponse.status).toBe(200);
    expect(JSON.parse(authResponse.text)).toMatchObject({
      method: "GET",
      path: "/login/resource",
      authorizationPresent: false,
      cookiePresent: false,
    });
    expect(authResponse.text).not.toContain("do-not-forward");
  }, 60_000);
});
