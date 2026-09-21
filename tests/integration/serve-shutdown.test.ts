import { mkdtemp, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { startGateway } from "../support/gateway.js";

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

async function runningSite(): Promise<{ config: string; address: string }> {
  const directory = await mkdtemp(join(tmpdir(), "liapoldus-serve-shutdown-"));
  await writeFile(
    join(directory, "site.yaml"),
    "index: index.html\n",
    "utf8",
  );
  await writeFile(
    join(directory, "index.html"),
    "<!doctype html><title>shutdown</title><h1>shutdown fixture</h1>",
    "utf8",
  );
  await writeFile(
    join(directory, "large.bin"),
    Buffer.alloc(24 * 1024 * 1024, 0x61),
  );
  const address = await freeAddress();
  const config = join(directory, "gateway.yaml");
  await writeFile(
    config,
    [
      "sites:",
      "  docs:",
      "    source:",
      "      type: directory",
      `      root: ${directory}`,
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${address}`,
      "    routes:",
      "      - when:",
      "          path: { prefix: / }",
      "        then:",
      "          site: docs",
    ].join("\n") + "\n",
    "utf8",
  );
  return { config, address };
}

async function waitReady(address: string): Promise<void> {
  for (let attempt = 0; attempt < 30; attempt += 1) {
    try {
      const response = await fetch(`http://${address}/`);
      response.body?.cancel();
      if (response.status === 200) return;
    } catch {
      // not up yet
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error(`gateway never became ready at ${address}`);
}

function exitCode(gateway: Running): Promise<number | null> {
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("gateway did not exit within 5s")), 5000);
    gateway.process.once("close", (code, signal) => {
      clearTimeout(timeout);
      resolve(signal === null ? code : null);
    });
  });
}

describe("gateway serve graceful shutdown", () => {
  it("exits 0 on SIGTERM and drains an in-flight response", async () => {
    const { config, address } = await runningSite();
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    processes.push(gateway);
    await waitReady(address);

    const payload = 24 * 1024 * 1024;
    const response = await fetch(`http://${address}/large.bin`);
    const exiting = exitCode(gateway);
    gateway.process.kill("SIGTERM");

    expect(response.status).toBe(200);
    expect((await response.arrayBuffer()).byteLength).toBe(payload);
    expect(await exiting).toBe(0);

    await expect(fetch(`http://${address}/large.bin`)).rejects.toThrow();
  });

  it("exits 0 on SIGINT", async () => {
    const { config, address } = await runningSite();
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    processes.push(gateway);
    await waitReady(address);

    const exiting = exitCode(gateway);
    gateway.process.kill("SIGINT");
    expect(await exiting).toBe(0);
  });
});