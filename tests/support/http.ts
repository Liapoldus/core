import { createServer } from "node:net";
import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

export async function freeAddress(): Promise<string> {
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

export function portOf(address: string): string {
  const separator = address.lastIndexOf(":");
  return separator >= 0 ? address.slice(separator + 1) : "";
}

export async function writeGatewayConfig(contents: string): Promise<string> {
  const directory = await mkdtemp(join(tmpdir(), "liapoldus-gateway-config-"));
  const path = join(directory, "gateway.yaml");
  await writeFile(path, contents, "utf8");
  return path;
}

export interface GatewayHTTPResponse {
  status: number;
  text: string;
  headers: Headers;
}

export async function waitReady(address: string): Promise<void> {
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

export async function request(address: string, path: string, init?: RequestInit): Promise<GatewayHTTPResponse> {
  const response = await fetch(`http://${address}${path}`, init);
  return { status: response.status, text: await response.text(), headers: response.headers };
}