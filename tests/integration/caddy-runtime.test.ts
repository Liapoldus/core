import { spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { once } from "node:events";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { freeAddress } from "../support/http.js";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

async function waitForHTTP(address: string, child: ChildProcess, stderr: () => string): Promise<void> {
  for (let attempt = 0; attempt < 300; attempt += 1) {
    if (child.exitCode !== null) throw new Error(`Caddy fixture exited: ${stderr()}`);
    try {
      const response = await fetch(`http://${address}/`);
      response.body?.cancel();
      return;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
  }
  throw new Error(`Caddy fixture did not serve HTTP: ${stderr()}`);
}

async function waitForOutput(fragment: string, child: ChildProcess, output: () => string): Promise<void> {
  for (let attempt = 0; attempt < 300; attempt += 1) {
    if (output().includes(fragment)) return;
    if (child.exitCode !== null) throw new Error(`Caddy fixture exited: ${output()}`);
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`Caddy fixture did not emit expected result: ${output()}`);
}

describe("embedded Caddy runtime", () => {
  it("serves a native Caddyfile from inside the Gateway process", async () => {
    const address = await freeAddress();
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-caddy-runtime-"));
    const configPath = join(directory, "runtime.Caddyfile");
    const fixture = await readFile(new URL("../fixtures/caddyfiles/runtime.Caddyfile", import.meta.url), "utf8");
    await writeFile(configPath, fixture.replaceAll("{{address}}", address), "utf8");

    const child = spawn("go", ["run", "./tests/fixtures/caddy-runtime", configPath], {
      cwd: coreRoot,
      stdio: ["ignore", "ignore", "pipe"],
    });
    let stderr = "";
    child.stderr?.on("data", (chunk: Buffer) => { stderr += chunk.toString(); });
    try {
      await waitForHTTP(address, child, () => stderr);
      const response = await fetch(`http://${address}/`);
      expect(response.status).toBe(200);
      expect(await response.text()).toBe("caddy-runtime-ok");
    } finally {
      child.kill("SIGTERM");
      if (child.exitCode === null) await once(child, "exit");
      await rm(directory, { recursive: true, force: true });
    }
  }, 90_000);

  it("replaces an adapted Caddyfile and keeps the active snapshot when a candidate is rejected", async () => {
    const address = await freeAddress();
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-caddy-reload-"));
    const initialPath = join(directory, "initial.Caddyfile");
    const replacementPath = join(directory, "replacement.Caddyfile");
    const invalidPath = join(directory, "invalid.Caddyfile");
    const initial = `http://${address} {\n  respond "initial"\n}\n`;
    const replacement = `http://${address} {\n  respond "replacement"\n}\n`;
    const invalid = `http://${address} {\n  nonexistent_liapoldus_directive\n}\n`;
    await writeFile(initialPath, initial, "utf8");
    await writeFile(replacementPath, replacement, "utf8");
    await writeFile(invalidPath, invalid, "utf8");

    const child = spawn("go", ["run", "./tests/fixtures/caddy-runtime-reload", initialPath], {
      cwd: coreRoot,
      stdio: ["pipe", "pipe", "pipe"],
    });
    let output = "";
    let stderr = "";
    child.stdout?.on("data", (chunk: Buffer) => { output += chunk.toString(); });
    child.stderr?.on("data", (chunk: Buffer) => { stderr += chunk.toString(); });

    try {
      await waitForHTTP(address, child, () => stderr);
      const initialResponse = await fetch(`http://${address}/`);
      expect(await initialResponse.text()).toBe("initial");

      child.stdin?.write(`${replacementPath}\n`);
      await waitForOutput("replaced", child, () => output);
      const replacementResponse = await fetch(`http://${address}/`);
      expect(await replacementResponse.text()).toBe("replacement");

      child.stdin?.write(`${invalidPath}\n`);
      await waitForOutput("rejected", child, () => output.slice(output.indexOf("replaced") + "replaced".length));
      const activeResponse = await fetch(`http://${address}/`);
      expect(await activeResponse.text()).toBe("replacement");
    } finally {
      child.kill("SIGTERM");
      if (child.exitCode === null) await once(child, "exit");
      await rm(directory, { recursive: true, force: true });
    }
  }, 90_000);
});
