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
const upstreams: Array<{ stop(): Promise<void> }> = [];

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    if (gateway.process.exitCode === null) gateway.process.kill("SIGTERM");
    await gateway.stop();
  }
  for (const upstream of upstreams.splice(0)) await upstream.stop();
});

describe("Gateway WAF captcha challenge", () => {
  it("challenges before proxying and returns an opaque, provider-bound token", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-captcha-challenge-"));
    const binary = join(directory, "captcha-fixture");
    await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/plugin-grpc"], { cwd: coreRoot });
    const upstream = await startUpstream();
    upstreams.push(upstream);
    const address = await freeAddress();
    const config = await writeGatewayConfig([
      "secrets:",
      "  recaptchaSecret: env:LIAPOLDUS_CAPTCHA_SECRET",
      "plugins:",
      "  captcha:",
      `    binary: ${JSON.stringify(binary)}`,
      "    capabilities: [captcha.verify]",
      "    env: [LIAPOLDUS_FIXTURE_MANIFEST_CAPABILITIES=captcha.verify, LIAPOLDUS_FIXTURE_MANIFEST_NAME=captcha]",
      "    settings: {}",
      "    grants:",
      "      secrets:",
      "        - name: recaptchaSecret",
      "          purpose: captcha.verify",
      "          domains: [www.google.com]",
      "captchaProviders:",
      "  public:",
      "    plugin: { instance: captcha, capability: captcha.verify }",
      "    verifyUrl: https://www.google.com/recaptcha/api/siteverify",
      "    secret: recaptchaSecret",
      "    allowedHosts: [www.google.com]",
      "wafPolicies:",
      "  protect:",
      "    rules:",
      "      - when: { path: { prefix: /private } }",
      "        then: { challenge: { provider: public } }",
      "upstreams:",
      "  api:",
      `    targets: [{ address: ${upstream.address} }]`,
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${address}`,
      "    routes:",
      "      - when: { path: { prefix: / } }",
      "        then: { proxy: { upstream: api }, waf: protect }",
    ].join("\n"));
    const validation = await runGateway(["--output", "json", "config", "validate", config], {
      LIAPOLDUS_CAPTCHA_SECRET: "test-only-captcha-secret",
    });
    expect(validation.exitCode).toBe(0);
    const gateway = await startGateway(["--config", config, "serve", "--no-management"], {
      LIAPOLDUS_CAPTCHA_SECRET: "test-only-captcha-secret",
    });
    gateways.push(gateway);
    await waitReady(address);
    const upstreamRequestsBeforeChallenge = upstream.hits().requests;

    const response = await request(address, "/private/resource");

    expect(response.status).toBe(403);
    const problem = JSON.parse(response.text) as Record<string, unknown>;
    expect(problem).toMatchObject({ code: "challenge_required", provider: "public" });
    expect(typeof problem.challengeToken).toBe("string");
    expect((problem.challengeToken as string).length).toBeGreaterThan(16);
    expect(response.text).not.toContain("test-only-captcha-secret");
    expect(upstream.hits().requests).toBe(upstreamRequestsBeforeChallenge);

    const verification = await request(address, "/.well-known/liapoldus/challenge/verify", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ challengeToken: problem.challengeToken, responseToken: "fixture-response" }),
    });
    expect(verification.status).toBe(204);
    const setCookie = verification.headers.get("set-cookie");
    expect(setCookie).toContain("_lpgw_challenge=");
    expect(setCookie).toContain("HttpOnly");
    expect(setCookie).toContain("SameSite=Lax");
    const cookie = setCookie?.split(";", 1)[0];
    const cleared = await request(address, "/private/resource", { headers: { Cookie: cookie ?? "" } });
    expect(cleared.status).toBe(200);
    expect(upstream.hits().requests).toBe(upstreamRequestsBeforeChallenge + 1);
  }, 60_000);
});
