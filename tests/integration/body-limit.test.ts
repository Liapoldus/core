import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { readFile } from "node:fs/promises";
import { runGateway, startGateway } from "../support/gateway.js";
import { freeAddress, request, waitReady, writeGatewayConfig } from "../support/http.js";

const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    if (!gateway.process.killed) gateway.process.kill("SIGTERM");
    await gateway.stop();
  }
});

type BodyLimitVector = {
  input: { bodyBytes: number; limit: string };
  expected: { status: number; code: string };
};

async function bodyLimitVector(): Promise<BodyLimitVector> {
  const source = await readFile(new URL("../../contracts/v1/golden-vectors.json", import.meta.url), "utf8");
  const contract = JSON.parse(source) as { vectors: Array<{ id: string } & BodyLimitVector> };
  const vector = contract.vectors.find(({ id }) => id === "body-limit");
  if (!vector) throw new Error("body-limit golden vector is missing");
  return vector;
}

describe("HTTP body size limit", () => {
  it("rejects the first byte beyond the configured limit with the golden Problem code", async () => {
    const vector = await bodyLimitVector();
    const listener = await freeAddress();
    const config = await writeGatewayConfig([
      `upstreams:`, `  api:`, `    targets:`, `      - address: 127.0.0.1:1`,
      `listeners:`, `  web:`, `    type: http`, `    address: ${listener}`,
      `    limits: { bodyBytes: ${vector.input.limit} }`,
      `    routes:`, `      - when: { path: { prefix: / } }`,
      `        then: { proxy: { upstream: api } }`,
    ].join("\n"));
    const validation = await runGateway(["--output", "json", "config", "validate", config]);
    expect(validation.exitCode, validation.stdout + validation.stderr).toBe(0);
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(listener);

    const response = await request(listener, "/over-limit", {
      method: "POST",
      body: "x".repeat(vector.input.bodyBytes),
    });

    expect(response.status).toBe(vector.expected.status);
    expect(response.headers.get("content-type")).toContain("application/problem+json");
    expect(JSON.parse(response.text)).toMatchObject({ code: vector.expected.code });
  });
});
