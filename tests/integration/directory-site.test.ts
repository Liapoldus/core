import { cp, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it } from "vitest";
import { startGateway } from "../support/gateway.js";

const fixture = fileURLToPath(new URL("../fixtures/e2e/directory-site", import.meta.url));
const processes: Array<{ stop(): Promise<void> }> = [];

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
      server.close((error) => error ? reject(error) : resolve(`127.0.0.1:${address.port}`));
    });
  });
}

describe("directory static site", () => {
  it("serves a configured directory source through an HTTP route", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-directory-site-"));
    await cp(fixture, directory, { recursive: true });
    const address = await freeAddress();
    const configPath = join(directory, "gateway.yaml");
    const config = await readFile(configPath, "utf8");
    await writeFile(configPath, config.replace("__ADDRESS__", address));

    const gateway = await startGateway(["--config", configPath, "serve", "--no-management"]);
    processes.push(gateway);

    let response: Response | undefined;
    for (let attempt = 0; attempt < 30; attempt += 1) {
      try {
        response = await fetch(`http://${address}/`);
        break;
      } catch {
        await new Promise((resolve) => setTimeout(resolve, 50));
      }
    }

    expect(response?.status).toBe(200);
    expect(await response?.text()).toContain("Gateway online");
  });
});
