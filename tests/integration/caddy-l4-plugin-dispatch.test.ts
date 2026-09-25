import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { once } from "node:events";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { createConnection, type Socket } from "node:net";
import { describe, expect, it } from "vitest";
import { freeAddress } from "../support/http.js";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
const execFileAsync = promisify(execFile);

async function build(binaryName: string, moduleRoot: string, packagePath: string, directory: string): Promise<string> {
  const output = join(directory, binaryName);
  await execFileAsync("go", ["build", "-o", output, packagePath], { cwd: moduleRoot });
  return output;
}

async function connectEventually(address: string, child: ChildProcess, stderr: () => string): Promise<Socket> {
  const [host, portText] = address.split(":");
  const port = Number(portText);
  for (let attempt = 0; attempt < 200; attempt += 1) {
    if (child.exitCode !== null) throw new Error(`Caddy fixture exited before listening: ${stderr()}`);
    try {
      const socket = createConnection({ host, port });
      await once(socket, "connect");
      return socket;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  }
  throw new Error(`Caddy L4 listener did not become ready: ${stderr()}`);
}

async function stop(child: ChildProcess): Promise<void> {
  if (child.exitCode !== null || child.signalCode !== null) return;
  const exited = once(child, "exit");
  child.kill("SIGTERM");
  await exited;
}

describe("Caddy-L4 plugin dispatch", () => {
  it("relays TCP connection bytes through the declared plugin Stream capability", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-caddy-l4-plugin-"));
    const [caddyBinary, pluginBinary, address] = await Promise.all([
      build("caddy-l4", coreRoot, "./tests/fixtures/caddy-plugin", directory),
      build("grpc-plugin", coreRoot, "./tests/fixtures/caddy-l4-plugin", directory),
      freeAddress(),
    ]);
    const configPath = join(directory, "l4.Caddyfile");
    const caddyfile = `{
  layer4 {
    ${address} {
      route {
        liapoldus_plugin fixture peer.session tcp
      }
    }
  }
}
`;
    await writeFile(configPath, caddyfile, "utf8");

    const pluginAddress = await freeAddress();
    const plugin = spawn(pluginBinary, [], {
      cwd: coreRoot,
      stdio: ["ignore", "ignore", "pipe"],
      env: { ...process.env, LIAPOLDUS_PLUGIN_ENDPOINT: pluginAddress },
    });
    let pluginStderr = "";
    plugin.stderr?.on("data", (chunk: Buffer) => { pluginStderr += chunk.toString(); });
    const caddy = spawn(caddyBinary, [configPath, pluginAddress], { cwd: coreRoot, stdio: ["ignore", "ignore", "pipe"] });
    let caddyStderr = "";
    caddy.stderr?.on("data", (chunk: Buffer) => { caddyStderr += chunk.toString(); });

    try {
      const socket = await connectEventually(address, caddy, () => caddyStderr);
      const payload = Buffer.from([0x00, 0x01, 0x7f, 0x80, 0xfe, 0xff]);
      const received = new Promise<Buffer>((resolve, reject) => {
        const chunks: Buffer[] = [];
        socket.setTimeout(5_000, () => reject(new Error("TCP plugin relay timed out")));
        socket.on("data", (chunk) => {
          chunks.push(chunk);
          const length = Buffer.concat(chunks).length;
          if (length >= payload.length) resolve(Buffer.concat(chunks).subarray(0, payload.length));
        });
        socket.once("error", reject);
      });
      socket.write(payload);
      expect(await received).toEqual(payload);
      socket.destroy();
    } catch (error) {
      throw new Error(`Caddy stderr: ${caddyStderr}\nPlugin stderr: ${pluginStderr}\n${String(error)}`);
    } finally {
      await stop(caddy);
      await stop(plugin);
      await rm(directory, { recursive: true, force: true });
    }
  }, 180_000);
});
