import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { once } from "node:events";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { freeAddress } from "../support/http.js";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
const execFileAsync = promisify(execFile);

async function build(binaryName: string, packagePath: string, directory: string): Promise<string> {
  const output = join(directory, binaryName);
  await execFileAsync("go", ["build", "-o", output, packagePath], { cwd: coreRoot });
  return output;
}

async function stop(child: ChildProcess): Promise<void> {
  if (child.exitCode !== null || child.signalCode !== null) return;
  const exited = once(child, "exit");
  child.kill("SIGTERM");
  await exited;
}

async function startCaddy(binary: string, pluginAddress: string, mode: string, capability: string, address: string): Promise<{ child: ChildProcess; stderr: () => string; config: string }> {
  const config = join(tmpdir(), `liapoldus-stream-${process.pid}-${Date.now()}.Caddyfile`);
  await writeFile(config, `http://${address} {
  route {
    liapoldus_plugin fixture ${capability} ${mode}
  }
}
`, "utf8");
  const child = spawn(binary, [config, pluginAddress], { cwd: coreRoot, stdio: ["ignore", "ignore", "pipe"] });
  let output = "";
  child.stderr?.on("data", (chunk: Buffer) => { output += chunk.toString(); });
  return { child, stderr: () => output, config };
}

async function waitHTTP(address: string, child: ChildProcess, stderr: () => string): Promise<void> {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (child.exitCode !== null) throw new Error(`Caddy did not start: ${stderr()}`);
    try {
      await fetch(`http://${address}/ready`, { signal: AbortSignal.timeout(300) });
      return;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  }
  throw new Error(`Caddy listener did not become ready: ${stderr()}`);
}

describe("Caddy HTTP Stream plugin dispatch", () => {
  it("streams HTTP request chunks and response chunks without buffering the body", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-caddy-http-stream-"));
    const [caddyBinary, pluginBinary, address, pluginAddress] = await Promise.all([
      build("caddy-http-stream", "./tests/fixtures/caddy-http-stream", directory),
      build("grpc-plugin", "./tests/fixtures/http-stream-plugin", directory),
      freeAddress(),
      freeAddress(),
    ]);
    const plugin = spawn(pluginBinary, [], { cwd: coreRoot, stdio: ["ignore", "ignore", "pipe"], env: { ...process.env, LIAPOLDUS_PLUGIN_ENDPOINT: pluginAddress } });
    let pluginOutput = "";
    plugin.stderr?.on("data", (chunk: Buffer) => { pluginOutput += chunk.toString(); });
    const caddy = await startCaddy(caddyBinary, pluginAddress, "http-stream", "forms.http", address);
    try {
      await waitHTTP(address, caddy.child, caddy.stderr);
      const body = new ReadableStream<Uint8Array>({
        start(controller) {
          controller.enqueue(new TextEncoder().encode("first-"));
          controller.enqueue(new TextEncoder().encode("second"));
          controller.close();
        },
      });
      const response = await fetch(`http://${address}/upload`, { method: "POST", body, duplex: "half" } as RequestInit & { duplex: "half" });
      expect(response.status).toBe(200);
      expect(await response.text()).toBe("first-second");
    } catch (error) {
      throw new Error(`Caddy stderr: ${caddy.stderr()}\nPlugin stderr: ${pluginOutput}\n${String(error)}`);
    } finally {
      await stop(caddy.child);
      await stop(plugin);
      await rm(caddy.config, { force: true });
      await rm(directory, { recursive: true, force: true });
    }
  }, 180_000);

  it("lets the plugin accept a WebSocket and echoes framed text messages", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-caddy-websocket-stream-"));
    const [caddyBinary, pluginBinary, address, pluginAddress] = await Promise.all([
      build("caddy-websocket", "./tests/fixtures/caddy-http-stream", directory),
      build("grpc-plugin", "./tests/fixtures/http-stream-plugin", directory),
      freeAddress(),
      freeAddress(),
    ]);
    const plugin = spawn(pluginBinary, [], { cwd: coreRoot, stdio: ["ignore", "ignore", "pipe"], env: { ...process.env, LIAPOLDUS_PLUGIN_ENDPOINT: pluginAddress } });
    let pluginOutput = "";
    plugin.stderr?.on("data", (chunk: Buffer) => { pluginOutput += chunk.toString(); });
    const caddy = await startCaddy(caddyBinary, pluginAddress, "websocket", "forms.websocket", address);
    try {
      await waitHTTP(address, caddy.child, caddy.stderr);
      const socket = new WebSocket(`ws://${address}/socket`, ["liapoldus.test"]);
      await new Promise<void>((resolve, reject) => {
        socket.addEventListener("open", () => resolve(), { once: true });
        socket.addEventListener("error", () => reject(new Error("WebSocket handshake failed")), { once: true });
      });
      expect(socket.protocol).toBe("liapoldus.test");
      const received = new Promise<string>((resolve, reject) => {
        socket.addEventListener("message", (event) => resolve(String(event.data)), { once: true });
        socket.addEventListener("error", () => reject(new Error("WebSocket message failed")), { once: true });
      });
      socket.send("hello");
      expect(await received).toBe("hello");
      socket.close();
    } catch (error) {
      throw new Error(`Caddy stderr: ${caddy.stderr()}\nPlugin stderr: ${pluginOutput}\n${String(error)}`);
    } finally {
      await stop(caddy.child);
      await stop(plugin);
      await rm(caddy.config, { force: true });
      await rm(directory, { recursive: true, force: true });
    }
  }, 180_000);

  it("serializes plugin SSE events with event, id, and data fields", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-caddy-sse-stream-"));
    const [caddyBinary, pluginBinary, address, pluginAddress] = await Promise.all([
      build("caddy-sse", "./tests/fixtures/caddy-http-stream", directory),
      build("grpc-plugin", "./tests/fixtures/http-stream-plugin", directory),
      freeAddress(),
      freeAddress(),
    ]);
    const plugin = spawn(pluginBinary, [], { cwd: coreRoot, stdio: ["ignore", "ignore", "pipe"], env: { ...process.env, LIAPOLDUS_PLUGIN_ENDPOINT: pluginAddress } });
    let pluginOutput = "";
    plugin.stderr?.on("data", (chunk: Buffer) => { pluginOutput += chunk.toString(); });
    const caddy = await startCaddy(caddyBinary, pluginAddress, "sse", "forms.sse", address);
    try {
      await waitHTTP(address, caddy.child, caddy.stderr);
      const response = await fetch(`http://${address}/events`);
      expect(response.status).toBe(200);
      expect(response.headers.get("content-type")).toContain("text/event-stream");
      expect(await response.text()).toBe("event: ready\nid: one\ndata: fixture\n\n");
    } catch (error) {
      throw new Error(`Caddy stderr: ${caddy.stderr()}\nPlugin stderr: ${pluginOutput}\n${String(error)}`);
    } finally {
      await stop(caddy.child);
      await stop(plugin);
      await rm(caddy.config, { force: true });
      await rm(directory, { recursive: true, force: true });
    }
  }, 180_000);
});
