import { connect } from "node:net";
import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { readFile } from "node:fs/promises";
import { runGateway, startGateway } from "../support/gateway.js";
import { freeAddress, waitReady, writeGatewayConfig } from "../support/http.js";

const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    if (!gateway.process.killed) gateway.process.kill("SIGTERM");
    await gateway.stop();
  }
});

type HeaderLimitVector = {
  input: { headerBytes: number; limit: string };
  expected: { status: number; code: string };
};

async function headerLimitVector(): Promise<HeaderLimitVector> {
  const source = await readFile(new URL("../../contracts/v1/golden-vectors.json", import.meta.url), "utf8");
  const contract = JSON.parse(source) as { vectors: Array<{ id: string } & HeaderLimitVector> };
  const vector = contract.vectors.find(({ id }) => id === "header-limit");
  if (!vector) throw new Error("header-limit golden vector is missing");
  return vector;
}

function sendOversizedHeader(address: string, bytes: number): Promise<{ status: number; response: string }> {
  const [host, port] = address.split(":");
  const rawRequest = `GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\nX-Pad: ${"a".repeat(bytes)}\r\n\r\n`;
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    const socket = connect(Number(port), host);
    socket.once("error", reject);
    socket.on("data", (chunk: Buffer) => chunks.push(chunk));
    socket.once("end", () => {
      const response = Buffer.concat(chunks).toString("latin1");
      const status = Number(response.match(/^HTTP\/1\.1 (\d+)/)?.[1] ?? 0);
      resolve({ status, response });
    });
    socket.once("connect", () => socket.end(rawRequest));
  });
}

describe("HTTP header size limit", () => {
  it("enforces the configured headerBytes limit and returns its golden status", async () => {
    const vector = await headerLimitVector();
    const listener = await freeAddress();
    const config = await writeGatewayConfig([
      `upstreams:`, `  api:`, `    targets:`, `      - address: 127.0.0.1:1`,
      `listeners:`, `  web:`, `    type: http`, `    address: ${listener}`,
      `    limits: { headerBytes: ${vector.input.limit} }`,
      `    routes:`, `      - when: { path: { prefix: / } }`,
      `        then: { proxy: { upstream: api } }`,
    ].join("\n"));
    const validation = await runGateway(["--output", "json", "config", "validate", config]);
    expect(validation.exitCode, validation.stdout + validation.stderr).toBe(0);
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(listener);

    const response = await sendOversizedHeader(listener, vector.input.headerBytes);

    expect(response.status).toBe(vector.expected.status);
    const [, rawBody = ""] = response.response.split("\r\n\r\n", 2);
    expect(response.response.toLowerCase()).toContain("content-type: application/problem+json");
    expect(JSON.parse(rawBody)).toMatchObject({ code: vector.expected.code });
  });
});
