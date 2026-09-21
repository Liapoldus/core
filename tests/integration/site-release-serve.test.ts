import { execFile } from "node:child_process";
import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { createServer, Socket } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it } from "vitest";
import { jsonOutput, runGateway, startGateway } from "../support/gateway.js";

const execute = promisify(execFile);
const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

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

async function releaseSource(root: string, name: string, body: string): Promise<string> {
  const directory = join(root, name);
  await mkdir(directory, { recursive: true });
  await writeFile(join(directory, "site.yaml"), "slug: blog\n");
  await writeFile(join(directory, "index.html"), body);
  return directory;
}

async function publish(root: string, sourceDirectory: string): Promise<void> {
  await execute("go", ["run", "./tests/fixtures/registry-probe", root, "blog", "publish", sourceDirectory], {
    cwd: coreRoot,
  });
}

async function releaseConfig(): Promise<{ config: string; address: string; registry: string }> {
  const registry = await mkdtemp(join(tmpdir(), "liapoldus-release-registry-"));
  const address = await freeAddress();
  const config = join(registry, "gateway.yaml");
  await writeFile(
    config,
    [
      "registry:",
      `  path: ${registry}`,
      "sites:",
      "  blog:",
      "    source:",
      "      type: release",
      "      slug: blog",
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${address}`,
      "    routes:",
      "      - when:",
      "          path: { prefix: / }",
      "        then: { site: blog }",
    ].join("\n") + "\n",
    "utf8",
  );
  return { config, address, registry };
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

async function rawRequest(address: string, target: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const [host, portText] = address.split(":");
    const socket = new Socket();
    const timer = setTimeout(() => {
      socket.destroy();
      reject(new Error("raw request timed out"));
    }, 3000);
    let chunked = "";
    socket.once("error", reject);
    socket.on("data", (received) => {
      chunked += received.toString("utf8");
    });
    socket.on("close", () => {
      clearTimeout(timer);
      resolve(chunked);
    });
    socket.connect(Number(portText), host, () => {
      socket.write(`GET ${target} HTTP/1.1\r\nHost: ${host}\r\nConnection: close\r\n\r\n`);
    });
  });
}

describe("release site serving", () => {
  it("serves the currently published release and follows re-publish to the active revision", async () => {
    const { config, address, registry } = await releaseConfig();
    const first = await releaseSource(registry, "source-one", "release one");
    await publish(registry, first);
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    processes.push(gateway);
    await waitReady(address);

    await expect(body(address, "/")).resolves.toEqual({ status: 200, text: "release one" });

    const second = await releaseSource(registry, "source-two", "release two");
    await publish(registry, second);

    await expect(body(address, "/")).resolves.toEqual({ status: 200, text: "release two" });
  });

  it("validates an unpublished release site but serves 404 until a revision exists", async () => {
    const { config, address } = await releaseConfig();

    const validated = await runGateway(["--output", "json", "config", "validate", config]);
    expect(validated.exitCode).toBe(0);
    expect(jsonOutput(validated)).toMatchObject({ ok: true, valid: true });

    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    processes.push(gateway);
    await waitReady(address);

    await expect(body(address, "/")).resolves.toMatchObject({ status: 404 });
  });

  it("returns 404 for release paths that traverse above the active revision root", async () => {
    const { config, address, registry } = await releaseConfig();
    const sourceDirectory = await releaseSource(registry, "source", "release root");
    await publish(registry, sourceDirectory);
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    processes.push(gateway);
    await waitReady(address);

    const escaped = "/releases/..%2f%2e%2e/%2e%2e/%2e%2e/%2e%2e/etc/hostname";
    const response = await rawRequest(address, escaped);
    expect(response.startsWith("HTTP/1.1 404")).toBe(true);
    expect(response).not.toContain("release root");
  });
});