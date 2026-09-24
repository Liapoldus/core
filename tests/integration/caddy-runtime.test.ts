import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { once } from "node:events";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { freeAddress } from "../support/http.js";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
const execFileAsync = promisify(execFile);

async function buildFixture(name: string): Promise<{ path: string; cleanup(): Promise<void> }> {
  const directory = await mkdtemp(join(tmpdir(), "liapoldus-caddy-fixture-"));
  const path = join(directory, name);
  try {
    await execFileAsync("go", ["build", "-o", path, `./tests/fixtures/${name}`], { cwd: coreRoot });
    return { path, cleanup: () => rm(directory, { recursive: true, force: true }) };
  } catch (error) {
    await rm(directory, { recursive: true, force: true });
    throw error;
  }
}

async function stopChild(child: ChildProcess): Promise<void> {
  if (child.exitCode !== null || child.signalCode !== null) return;
  const exited = once(child, "exit");
  child.kill("SIGTERM");
  await exited;
}

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

async function waitForOutput(fragment: string, child: ChildProcess, output: () => string, startAt = 0): Promise<void> {
  for (let attempt = 0; attempt < 300; attempt += 1) {
    if (output().slice(startAt).includes(fragment)) return;
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
    const fixtureBinary = await buildFixture("caddy-runtime");
    const fixture = await readFile(new URL("../fixtures/caddyfiles/runtime.Caddyfile", import.meta.url), "utf8");
    await writeFile(configPath, fixture.replaceAll("{{address}}", address), "utf8");

    const child = spawn(fixtureBinary.path, [configPath], {
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
      await stopChild(child);
      await fixtureBinary.cleanup();
      await rm(directory, { recursive: true, force: true });
    }
  }, 90_000);

  it("keeps the native Caddy Admin API disabled even when a Caddyfile requests a public bind", async () => {
    const address = await freeAddress();
    const adminAddress = await freeAddress();
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-caddy-admin-"));
    const configPath = join(directory, "runtime.Caddyfile");
    const fixtureBinary = await buildFixture("caddy-runtime");
    const fixture = `{
  admin 0.0.0.0:${adminAddress.split(":").at(-1)}
}

http://${address} {
  respond "runtime-ok"
}
`;
    await writeFile(configPath, fixture, "utf8");

    const child = spawn(fixtureBinary.path, [configPath], {
      cwd: coreRoot,
      stdio: ["ignore", "ignore", "pipe"],
    });
    let stderr = "";
    child.stderr?.on("data", (chunk: Buffer) => { stderr += chunk.toString(); });
    try {
      await waitForHTTP(address, child, () => stderr);
      let adminReachable = false;
      try {
        const response = await fetch(`http://${adminAddress}/config/`);
        adminReachable = true;
        response.body?.cancel();
      } catch {
        adminReachable = false;
      }
      expect(adminReachable).toBe(false);
    } finally {
      await stopChild(child);
      await fixtureBinary.cleanup();
      await rm(directory, { recursive: true, force: true });
    }
  }, 90_000);

  it("replaces an adapted Caddyfile and keeps the active snapshot when a candidate is rejected", async () => {
    const address = await freeAddress();
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-caddy-reload-"));
    const initialPath = join(directory, "initial.Caddyfile");
    const replacementPath = join(directory, "replacement.Caddyfile");
    const invalidPath = join(directory, "invalid.Caddyfile");
    const loadFailurePath = join(directory, "load-failure.Caddyfile");
    const certificatePath = join(directory, "invalid.crt");
    const keyPath = join(directory, "invalid.key");
    const fixtureBinary = await buildFixture("caddy-runtime-reload");
    const initial = `http://${address} {\n  respond "initial"\n}\n`;
    const replacement = `http://${address} {\n  respond "replacement"\n}\n`;
    const invalid = `http://${address} {\n  nonexistent_liapoldus_directive\n}\n`;
    const loadFailure = `https://localhost {\n  tls ${JSON.stringify(certificatePath)} ${JSON.stringify(keyPath)}\n  respond "unreachable"\n}\n`;
    await writeFile(initialPath, initial, "utf8");
    await writeFile(replacementPath, replacement, "utf8");
    await writeFile(invalidPath, invalid, "utf8");
    await writeFile(loadFailurePath, loadFailure, "utf8");
    await writeFile(certificatePath, "invalid certificate", "utf8");
    await writeFile(keyPath, "invalid private key", "utf8");

    const child = spawn(fixtureBinary.path, [initialPath], {
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

      const outputOffset = output.length;
      child.stdin?.write(`${loadFailurePath}\n`);
      await waitForOutput("rejected", child, () => output, outputOffset);
      const afterLoadFailure = await fetch(`http://${address}/`);
      expect(await afterLoadFailure.text()).toBe("replacement");
    } finally {
      await stopChild(child);
      await fixtureBinary.cleanup();
      await rm(directory, { recursive: true, force: true });
    }
  }, 90_000);
});
