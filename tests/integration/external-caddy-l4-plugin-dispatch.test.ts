import { execFile, spawn, type ChildProcess } from "node:child_process";
import { chmod, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { createSocket, type Socket as DatagramSocket } from "node:dgram";
import { createConnection, type Socket } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { once } from "node:events";
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

async function connectEventually(address: string, child: ChildProcess): Promise<Socket> {
  const [host, portText] = address.split(":");
  const port = Number(portText);
  for (let attempt = 0; attempt < 200; attempt += 1) {
    if (child.exitCode !== null) throw new Error("External Caddy host exited before the TCP listener became ready.");
    try {
      const socket = createConnection({ host, port });
      await once(socket, "connect");
      return socket;
    } catch {
      await new Promise((resolveRetry) => setTimeout(resolveRetry, 50));
    }
  }
  throw new Error("External Caddy TCP listener did not become ready.");
}

async function udpExchange(socket: DatagramSocket, address: string, payload: Buffer, timeoutMs: number): Promise<Buffer> {
  const separator = address.lastIndexOf(":");
  const host = address.slice(0, separator);
  const port = Number(address.slice(separator + 1));
  return new Promise((resolve, reject) => {
    const cleanup = () => {
      clearTimeout(timer);
      socket.off("message", receive);
    };
    const receive = (message: Buffer) => {
      cleanup();
      resolve(Buffer.from(message));
    };
    const timer = setTimeout(() => {
      cleanup();
      reject(new Error("External Caddy UDP plugin relay timed out."));
    }, timeoutMs);
    socket.once("message", receive);
    socket.send(payload, port, host, (error) => {
      if (error) {
        cleanup();
        reject(error);
      }
    });
  });
}

describe("supervised external Caddy-L4 plugin dispatch", () => {
  it("dispatches TCP connection bytes and distinct UDP datagrams directly from Caddy to the plugin", async () => {
    const directory = await mkdtemp(join(tmpdir(), "lc-"));
    const tcpAddress = await freeAddress();
    const udpAddress = await freeAddress();
    const pluginAddress = await freeAddress();
    const stateDirectory = join(directory, "state");
    const pluginBinary = join(directory, "l4-plugin");
    const launcherBinary = join(directory, "plugin-launcher");
    const caddyBinary = join(directory, "liapoldus-caddy");
    const externalLog = join(directory, "external-caddy.log");
    const hostBinary = join(directory, "external-caddy-host");
    const initialCaddyfile = join(directory, "initial.Caddyfile");
    const caddyfile = `{
  layer4 {
    ${tcpAddress} {
      route {
        liapoldus_plugin fixture peer.session tcp
      }
    }
    udp/${udpAddress} {
      route {
        liapoldus_plugin fixture peer.session udp
      }
    }
  }
}
`;
    let plugin: ChildProcess | undefined;
    let host: ChildProcess | undefined;
    let pluginStderr = "";
    let hostStderr = "";

    try {
      await Promise.all([
        buildFixture("caddy-l4-plugin", pluginBinary),
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
      const protocol = fixtureProtocol(host);
      expect(await protocol.next()).toMatchObject({ event: "ready", ready: true });

      const tcp = await connectEventually(tcpAddress, host);
      const tcpPayload = Buffer.from([0x00, 0x01, 0x7f, 0x80, 0xfe, 0xff]);
      const tcpResponse = new Promise<Buffer>((resolve, reject) => {
        const chunks: Buffer[] = [];
        tcp.setTimeout(5_000, () => reject(new Error("External Caddy TCP plugin relay timed out.")));
        tcp.on("data", (chunk) => {
          chunks.push(chunk);
          const received = Buffer.concat(chunks);
          if (received.length >= tcpPayload.length) resolve(received.subarray(0, tcpPayload.length));
        });
        tcp.once("error", reject);
      });
      tcp.write(tcpPayload);
      expect(await tcpResponse).toEqual(tcpPayload);
      tcp.destroy();

      const udp = createSocket("udp4");
      try {
        await new Promise<void>((resolve, reject) => {
          udp.once("error", reject);
          udp.bind(0, "127.0.0.1", resolve);
        });
        let ready: Buffer | undefined;
        for (let attempt = 0; attempt < 40 && !ready; attempt += 1) {
          if (host.exitCode !== null) throw new Error("External Caddy host exited before the UDP listener became ready.");
          try {
            ready = await udpExchange(udp, udpAddress, Buffer.from("ready"), 200);
          } catch {
            // Initial datagrams can race Caddy-L4 binding the UDP listener.
          }
        }
        if (!ready || ready.length < 1) throw new Error("External Caddy UDP listener did not become ready.");

        const first = Buffer.from([0x00, 0x01, 0x7f, 0xff]);
        const second = Buffer.from([0x80, 0x00, 0xfe, 0x04, 0x05]);
        const firstResponse = await udpExchange(udp, udpAddress, first, 2_000);
        const secondResponse = await udpExchange(udp, udpAddress, second, 2_000);
        expect(firstResponse).toEqual(Buffer.concat([Buffer.from([ready[0] + 1]), first]));
        expect(secondResponse).toEqual(Buffer.concat([Buffer.from([ready[0] + 2]), second]));
      } finally {
        udp.close();
      }

      // The host process only supervises control/configuration; it must not proxy user payloads.
      expect(hostStderr).not.toContain(tcpPayload.toString("hex"));
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
  }, 240_000);
});
