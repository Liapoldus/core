import { execFile, spawn, type ChildProcess } from "node:child_process";
import { chmod, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { freeAddress } from "../support/http.js";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");
const externalBuildID = "liapoldus-external-caddy-fixture";

interface FixtureMessage {
  readonly event?: string;
  readonly ready?: boolean;
}

function fixtureProtocol(child: ChildProcess): { next(): Promise<FixtureMessage> } {
  const lines = createInterface({ input: child.stdout! });
  const buffered: string[] = [];
  const waiters: Array<(line: string) => void> = [];
  lines.on("line", (line) => {
    const waiter = waiters.shift();
    if (waiter) waiter(line);
    else buffered.push(line);
  });
  return {
    next: async () => {
      const line = buffered.shift() ?? await new Promise<string>((resolveLine, reject) => {
        const timer = setTimeout(() => reject(new Error("Timed out waiting for external Caddy fixture.")), 30_000);
        waiters.push((value) => {
          clearTimeout(timer);
          resolveLine(value);
        });
      });
      return JSON.parse(line) as FixtureMessage;
    },
  };
}

async function buildFixture(name: string, output: string): Promise<void> {
  await execFileAsync("go", ["build", "-o", output, `./tests/fixtures/${name}`], { cwd: coreRoot });
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

async function stopChild(child: ChildProcess | undefined): Promise<void> {
  if (child === undefined || child.exitCode !== null || child.signalCode !== null) return;
  await new Promise<void>((resolveClose) => {
    child.once("close", () => resolveClose());
    child.kill("SIGTERM");
  });
}

async function waitForHTTP(address: string, child: ChildProcess): Promise<void> {
  for (let attempt = 0; attempt < 300; attempt += 1) {
    if (child.exitCode !== null) throw new Error("External Caddy host exited before its HTTP listener became ready.");
    try {
      const response = await fetch(`http://${address}/ready`, { signal: AbortSignal.timeout(300) });
      response.body?.cancel();
      return;
    } catch {
      await new Promise((resolveDelay) => setTimeout(resolveDelay, 100));
    }
  }
  throw new Error("External Caddy HTTP listener did not become ready.");
}

describe("supervised external Caddy HTTP Stream modes", () => {
  it("dispatches HTTP chunks, WebSocket messages, and SSE events directly to the plugin", async () => {
    const directory = await mkdtemp(join(tmpdir(), "lc-"));
    const publicAddress = await freeAddress();
    const pluginAddress = await freeAddress();
    const stateDirectory = join(directory, "state");
    const pluginBinary = join(directory, "http-stream-plugin");
    const launcherBinary = join(directory, "plugin-launcher");
    const caddyBinary = join(directory, "liapoldus-caddy");
    const hostBinary = join(directory, "external-caddy-host");
    const externalLog = join(directory, "external-caddy.log");
    const initialCaddyfile = join(directory, "initial.Caddyfile");
    const caddyfile = `http://${publicAddress} {
  route /upload* {
    liapoldus_plugin fixture forms.http http-stream
  }
  route /socket* {
    liapoldus_plugin fixture forms.websocket websocket
  }
  route /events* {
    liapoldus_plugin fixture forms.sse sse
  }
}
`;
    let plugin: ChildProcess | undefined;
    let host: ChildProcess | undefined;
    let pluginStderr = "";
    let hostStderr = "";

    try {
      await Promise.all([
        buildFixture("http-stream-plugin", pluginBinary),
        buildFixture("plugin-process-launcher", launcherBinary),
        buildFixture("external-caddy-custom", caddyBinary),
        buildFixture("external-caddy-plugin-host", hostBinary),
      ]);
      await writeFile(initialCaddyfile, caddyfile, "utf8");
      const externalWrapper = join(directory, "liapoldus-caddy-wrapper");
      await writeFile(externalWrapper, [
        "#!/bin/sh",
        `exec ${shellQuote(caddyBinary)} "$@" 2>>${shellQuote(externalLog)}`,
        "",
      ].join("\n"), { mode: 0o700 });
      await chmod(externalWrapper, 0o700);

      plugin = spawn(launcherBinary, [pluginAddress, pluginBinary], {
        cwd: coreRoot,
        stdio: ["ignore", "ignore", "pipe"],
      });
      plugin.stderr?.on("data", (chunk: Buffer) => { pluginStderr += chunk.toString(); });
      host = spawn(hostBinary, [externalWrapper, stateDirectory, initialCaddyfile, externalBuildID, pluginAddress], {
        cwd: coreRoot,
        stdio: ["pipe", "pipe", "pipe"],
      });
      host.stderr?.on("data", (chunk: Buffer) => { hostStderr += chunk.toString(); });
      expect(await fixtureProtocol(host).next()).toMatchObject({ event: "ready", ready: true });
      await waitForHTTP(publicAddress, host);

      const requestBody = new ReadableStream<Uint8Array>({
        start(controller) {
          controller.enqueue(new TextEncoder().encode("first-"));
          controller.enqueue(new TextEncoder().encode("second"));
          controller.close();
        },
      });
      const httpResponse = await fetch(`http://${publicAddress}/upload`, {
        method: "POST",
        body: requestBody,
        duplex: "half",
      } as RequestInit & { duplex: "half" });
      expect(httpResponse.status).toBe(200);
      expect(await httpResponse.text()).toBe("first-second");

      const socket = new WebSocket(`ws://${publicAddress}/socket`, ["liapoldus.test"]);
      await new Promise<void>((resolveOpen, reject) => {
        socket.addEventListener("open", () => resolveOpen(), { once: true });
        socket.addEventListener("error", () => reject(new Error("External Caddy WebSocket handshake failed.")), { once: true });
      });
      expect(socket.protocol).toBe("liapoldus.test");
      const message = new Promise<string>((resolveMessage, reject) => {
        socket.addEventListener("message", (event) => resolveMessage(String(event.data)), { once: true });
        socket.addEventListener("error", () => reject(new Error("External Caddy WebSocket message failed.")), { once: true });
      });
      socket.send("stream-message");
      expect(await message).toBe("stream-message");
      socket.close();

      const sseResponse = await fetch(`http://${publicAddress}/events`);
      expect(sseResponse.status).toBe(200);
      expect(sseResponse.headers.get("content-type")).toContain("text/event-stream");
      expect(await sseResponse.text()).toBe("event: ready\nid: one\ndata: fixture\n\n");
      expect(hostStderr).not.toContain(pluginAddress);
      expect(pluginStderr).not.toContain("secret");
    } catch (error) {
      const externalDiagnostics = await readFile(externalLog, "utf8").catch(() => "");
      throw new Error(`External Caddy stderr: ${hostStderr}\nExternal diagnostics: ${externalDiagnostics}\nPlugin stderr: ${pluginStderr}\n${String(error)}`);
    } finally {
      await stopChild(host);
      await stopChild(plugin);
      await rm(directory, { recursive: true, force: true });
    }
  }, 180_000);
});
