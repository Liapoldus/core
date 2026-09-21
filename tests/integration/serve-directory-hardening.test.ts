import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { createServer, Socket } from "node:net";
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

async function writeTree(): Promise<string> {
  const directory = await mkdtemp(join(tmpdir(), "liapoldus-directory-hardening-"));
  const files: Record<string, string> = {
    "index.html": "root index",
    "deep/index.html": "deep index",
    "sub/leaf.txt": "sub leaf",
  };
  for (const [relative, contents] of Object.entries(files)) {
    const target = join(directory, relative);
    await mkdir(join(target, ".."), { recursive: true });
    await writeFile(target, contents, "utf8");
  }
  return directory;
}

async function hardeningConfig(): Promise<{ config: string; address: string }> {
  const directory = await writeTree();
  const address = await freeAddress();
  const config = join(directory, "gateway.yaml");
  await writeFile(
    config,
    [
      "sites:",
      `  docs: { source: { type: directory, root: ${directory} } }`,
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${address}`,
      "    routes:",
      "      - when:",
      "          path: { prefix: / }",
      "        then: { site: docs }",
    ].join("\n") + "\n",
    "utf8",
  );
  return { config, address };
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
    let response = "";
    socket.once("error", reject);
    socket.on("data", (chunk) => {
      response += chunk.toString("utf8");
    });
    socket.on("close", () => {
      clearTimeout(timer);
      resolve(response);
    });
    socket.connect(Number(portText), host, () => {
      socket.write(`GET ${target} HTTP/1.1\r\nHost: ${host}\r\nConnection: close\r\n\r\n`);
    });
  });
}

describe("directory static site hardening", () => {
  it("serves the root index and directories that contain an index file", async () => {
    const { config, address } = await hardeningConfig();
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    processes.push(gateway);
    await waitReady(address);

    await expect(body(address, "/")).resolves.toEqual({ status: 200, text: "root index" });
    await expect(body(address, "/deep/")).resolves.toEqual({ status: 200, text: "deep index" });
  });

  it("returns 404 instead of a generated directory listing for a missing index file", async () => {
    const { config, address } = await hardeningConfig();
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    processes.push(gateway);
    await waitReady(address);

    await expect(body(address, "/sub/")).resolves.toMatchObject({ status: 404 });
    await expect(body(address, "/sub")).resolves.toMatchObject({ status: 404 });
  });

  it("returns 404 for a missing file", async () => {
    const { config, address } = await hardeningConfig();
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    processes.push(gateway);
    await waitReady(address);

    await expect(body(address, "/nope.txt")).resolves.toMatchObject({ status: 404 });
  });

  it("returns 404 for request paths that traverse above site.Root", async () => {
    const { config, address } = await hardeningConfig();
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    processes.push(gateway);
    await waitReady(address);

    // Sent raw so no client (WHATWG URL) normalises the encoded dot segments
    // away: the request must arrive at the gateway with "/.." still in its path.
    const escaped = "/deep/%2e%2e/%2e%2e/%2e%2e/%2e%2e/etc/hostname";
    const response = await rawRequest(address, escaped);
    expect(response.startsWith("HTTP/1.1 404")).toBe(true);
  });

  it("rejects an empty request-target without serving content", async () => {
    const { config, address } = await hardeningConfig();
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    processes.push(gateway);
    await waitReady(address);

    // net/http rejects a request line with an empty request-target ("GET  HTTP/1.1")
    // before the application handler runs, so the gateway answers 400. This locks
    // in that an empty target never reaches a directory listing.
    const response = await rawRequest(address, "");
    expect(response.startsWith("HTTP/1.1 400")).toBe(true);
  });
});