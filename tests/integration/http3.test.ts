import { afterEach, describe, expect, it } from "vitest";
import { execFile } from "node:child_process";
import dgram from "node:dgram";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import tls from "node:tls";
import type { ChildProcess } from "node:child_process";
import { startGateway } from "../support/gateway.js";
import { freeAddress, writeGatewayConfig } from "../support/http.js";

const execFileAsync = promisify(execFile);
const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];

afterEach(async () => Promise.all(gateways.splice(0).map((gateway) => gateway.stop())));

function waitForTLS(address: string, process: ChildProcess): Promise<void> {
  return new Promise((resolve, reject) => {
    const [host, port] = address.split(":");
    const deadline = Date.now() + 5_000;
    let lastError = "";
    const connect = () => {
      const socket = tls.connect({ host, port: Number(port), rejectUnauthorized: false });
      socket.once("secureConnect", () => {
        socket.destroy();
        resolve();
      });
      socket.once("error", (error) => {
        socket.destroy();
        lastError = error.message;
        if (process.exitCode !== null) {
          reject(new Error(`Gateway exited before TLS became ready (${process.exitCode})`));
          return;
        }
        if (Date.now() >= deadline) reject(new Error(`TLS listener did not become ready: ${lastError}`));
        else setTimeout(connect, 25);
      });
    };
    connect();
  });
}

function expectUDPPortInUse(address: string): Promise<void> {
  const [host, port] = address.split(":");
  return new Promise((resolve, reject) => {
    const socket = dgram.createSocket("udp4");
    socket.once("error", (error: NodeJS.ErrnoException) => {
      if (error.code === "EADDRINUSE") resolve();
      else reject(error);
    });
    socket.bind(Number(port), host, () => {
      socket.close();
      reject(new Error("Gateway did not bind the UDP port for HTTP/3"));
    });
  });
}

describe("HTTP/3 listener", () => {
  it("binds TCP and UDP on the same address for an h3 TLS listener", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-http3-"));
    const cert = join(directory, "server.crt");
    const key = join(directory, "server.key");
    await execFileAsync("openssl", [
      "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
      "-subj", "/CN=localhost", "-keyout", key, "-out", cert,
    ]);
    const address = await freeAddress();
    const config = await writeGatewayConfig([
      "registry:",
      `  path: ${join(directory, "registry")}`,
      "tlsProfiles:",
      "  public:",
      "    certificates:",
      `      - { cert: ${JSON.stringify(cert)}, key: ${JSON.stringify(key)} }`,
      "    protocols: [http/1.1, h2, h3]",
      "listeners:",
      "  public:",
      "    type: http",
      `    address: ${address}`,
      "    tls: public",
      "    routes: []",
    ].join("\n"));
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);

    await waitForTLS(address, gateway.process);
    await expectUDPPortInUse(address);
  });
});
