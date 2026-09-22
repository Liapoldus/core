import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { request as httpRequest } from "node:http";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { jsonOutput, runGateway, startGateway } from "../support/gateway.js";

type Running = { stop(): Promise<void>; process: import("node:child_process").ChildProcess };
const processes: Running[] = [];

afterEach(async () => {
  await Promise.all(processes.splice(0).map((gateway) => gateway.stop()));
});

async function freeAddress(): Promise<string> {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      if (typeof address === "string" || address === null) {
        reject(new Error("expected TCP address"));
        return;
      }
      server.close((error) => (error ? reject(error) : resolve(`127.0.0.1:${address.port}`)));
    });
  });
}

async function writeTree(): Promise<string> {
  const directory = await mkdtemp(join(tmpdir(), "liapoldus-routes-"));
  const files: Record<string, string> = {
    "exact/exact.txt": "exact",
    "pre/pre/leaf.txt": "prefix",
    "api/api/v1/res": "regex",
    "api/api/v9/res": "regex",
    "raw/raw/leaf.txt": "raw-string",
    "arr/arr-a/leaf.txt": "array",
  };
  for (const [relative, contents] of Object.entries(files)) {
    const target = join(directory, relative);
    await mkdir(join(target, ".."), { recursive: true });
    await writeFile(target, contents, "utf8");
  }
  return directory;
}

async function matcherConfig(): Promise<{ config: string; address: string; directory: string }> {
  const directory = await writeTree();
  const address = await freeAddress();
  const config = join(directory, "gateway.yaml");
  await writeFile(
    config,
    [
      "sites:",
      `  exact: { source: { type: directory, root: ${join(directory, "exact")} } }`,
      `  pre:   { source: { type: directory, root: ${join(directory, "pre")} } }`,
      `  api:   { source: { type: directory, root: ${join(directory, "api")} } }`,
      `  raw:   { source: { type: directory, root: ${join(directory, "raw")} } }`,
      `  arr:   { source: { type: directory, root: ${join(directory, "arr")} } }`,
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${address}`,
      "    routes:",
      "      - when:",
      "          path: { exact: /exact.txt }",
      "        then: { site: exact }",
      "      - when:",
      "          path: { prefix: /pre/ }",
      "        then: { site: pre }",
      "      - when:",
      "          path: { regex: '^/api/v[0-9]+/res$' }",
      "        then: { site: api }",
      "      - when:",
      "          path: /raw/",
      "        then: { site: raw }",
      "      - when:",
      "          path: [/arr-a/]",
      "        then: { site: arr }",
    ].join("\n") + "\n",
    "utf8",
  );
  return { config, address, directory };
}

async function waitReady(address: string): Promise<void> {
  for (let attempt = 0; attempt < 30; attempt += 1) {
    try {
      const response = await fetch(`http://${address}/missing.txt`);
      response.body?.cancel();
      return;
    } catch {
      // not up yet
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error(`gateway never became ready at ${address}`);
}

async function body(address: string, path: string): Promise<{ status: number; text: string }> {
  const response = await fetch(`http://${address}${path}`);
  return { status: response.status, text: await response.text() };
}

async function serveExit(config: string): Promise<number | null> {
  const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
  processes.push(gateway);
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      gateway.process.kill("SIGKILL");
      reject(new Error("serve did not exit within 5s"));
    }, 5000);
    gateway.process.once("close", (code, signal) => {
      clearTimeout(timer);
      resolve(signal === null ? code : null);
    });
  });
}

describe("path route matchers", () => {
  it("dispatches prefix, exact and regex routes to the matching site", async () => {
    const { config, address } = await matcherConfig();
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    processes.push(gateway);
    await waitReady(address);

    await expect(body(address, "/exact.txt")).resolves.toEqual({ status: 200, text: "exact" });
    await expect(body(address, "/pre/leaf.txt")).resolves.toEqual({ status: 200, text: "prefix" });
    await expect(body(address, "/api/v1/res")).resolves.toEqual({ status: 200, text: "regex" });
    await expect(body(address, "/api/v9/res")).resolves.toEqual({ status: 200, text: "regex" });
    await expect(body(address, "/raw/leaf.txt")).resolves.toEqual({ status: 200, text: "raw-string" });
    await expect(body(address, "/arr-a/leaf.txt")).resolves.toEqual({ status: 200, text: "array" });

    await expect(body(address, "/missing.txt")).resolves.toMatchObject({ status: 404 });
    await expect(body(address, "/pre-vote.txt")).resolves.toMatchObject({ status: 404 });
    await expect(body(address, "/exact.txt.bak")).resolves.toMatchObject({ status: 404 });
  });

  it("fails configuration compilation for an invalid regex matcher", async () => {
    const { directory } = await matcherConfig();
    const config = join(directory, "invalid-regex.yaml");
    await writeFile(
      config,
      [
        "sites:",
        `  docs: { source: { type: directory, root: ${join(directory, "exact")} } }`,
        "listeners:",
        "  web:",
        "    type: http",
        "    address: 127.0.0.1:0",
        "    routes:",
        "      - when:",
        "          path: { regex: '(' }",
        "        then: { site: docs }",
      ].join("\n") + "\n",
      "utf8",
    );

    const exit = await serveExit(config);
    expect(exit).toBe(3);

    const result = await runGateway(["--output", "json", "--config", config, "serve", "--no-management"]);
    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result)).toMatchObject({ problem: { code: "config_invalid" } });
  });
});

describe("HTTP route matcher fields", () => {
  it("requires host, method, headers, query and path to match together", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-route-fields-"));
    const address = await freeAddress();
    const config = join(directory, "gateway.yaml");
    await writeFile(
      config,
      [
        "listeners:",
        "  web:",
        "    type: http",
        `    address: ${address}`,
        "    routes:",
        "      - when:",
        "          host: api.example.test",
        "          method: POST",
        "          path: { exact: /secure }",
        "          headers: { x-client: { exact: trusted } }",
        "          query: { version: { exact: v1 } }",
        "        then: { deny: { status: 451 } }",
      ].join("\n") + "\n",
      "utf8",
    );
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    processes.push(gateway);
    await waitReady(address);

    const [, portValue] = address.split(":");
    const send = (path: string, method: string, headers: Record<string, string>) =>
      new Promise<{ status: number }>((resolve, reject) => {
        const request = httpRequest({
          host: "127.0.0.1",
          port: Number(portValue),
          path,
          method,
          headers,
        }, (response) => {
          response.resume();
          resolve({ status: response.statusCode ?? 0 });
        });
        request.once("error", reject);
        request.end();
      });
    const accepted = await send("/secure?version=v1", "POST", {
      host: "api.example.test",
      "x-client": "trusted",
    });
    expect(accepted.status).toBe(451);

    const mismatches = await Promise.all([
      send("/secure?version=v1", "POST", { host: "other.example.test", "x-client": "trusted" }),
      send("/secure?version=v1", "GET", { host: "api.example.test", "x-client": "trusted" }),
      send("/secure?version=v1", "POST", { host: "api.example.test", "x-client": "untrusted" }),
      send("/secure?version=v2", "POST", { host: "api.example.test", "x-client": "trusted" }),
    ]);
    expect(mismatches.map((response) => response.status)).toEqual([404, 404, 404, 404]);
  });
});
