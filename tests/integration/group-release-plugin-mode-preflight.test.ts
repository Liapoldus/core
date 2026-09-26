import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { createConnection } from "node:net";
import { join } from "node:path";
import { once } from "node:events";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { freeAddress } from "../support/http.js";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

async function waitForTCP(address: string, child: ChildProcess): Promise<void> {
  const [host, port] = address.split(":");
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (child.exitCode !== null) throw new Error(`Plugin launcher exited (${child.exitCode}).`);
    try {
      await new Promise<void>((resolve, reject) => {
        const connection = createConnection({ host, port: Number(port) });
        connection.once("connect", () => {
          connection.destroy();
          resolve();
        });
        connection.once("error", reject);
      });
      return;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  }
  throw new Error("Plugin listener did not become ready.");
}

async function stopChild(child: ChildProcess): Promise<void> {
  if (child.exitCode !== null || child.signalCode !== null) return;
  const exited = once(child, "exit");
  child.kill("SIGTERM");
  await exited;
}

describe("group release capability-mode preflight", () => {
  it("rejects undeclared invocation modes before reserving a release", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-group-plugin-mode-"));
    const pluginBinary = join(directory, "plugin-grpc");
    const launcherBinary = join(directory, "plugin-launcher");
    const pluginAddress = await freeAddress();
    const publicAddress = await freeAddress();
    const database = join(directory, "gateway.db");
    const artifacts = join(directory, "artifacts");
    let launcher: ChildProcess | undefined;

    try {
      await Promise.all([
        execFileAsync("go", ["build", "-o", pluginBinary, "./tests/fixtures/plugin-grpc"], { cwd: coreRoot }),
        execFileAsync("go", ["build", "-o", launcherBinary, "./tests/fixtures/plugin-process-launcher"], { cwd: coreRoot }),
      ]);
      launcher = spawn(launcherBinary, [pluginAddress, pluginBinary], {
        cwd: coreRoot,
        stdio: ["ignore", "ignore", "pipe"],
      });
      let launcherStderr = "";
      launcher.stderr?.on("data", (chunk: Buffer) => { launcherStderr += chunk.toString(); });
      await waitForTCP(pluginAddress, launcher);

      const result = await execFileAsync("go", [
        "run", "./tests/fixtures/group-release-plugin-mode-preflight",
        database, artifacts, publicAddress, pluginAddress,
      ], { cwd: coreRoot });
      const report = JSON.parse(result.stdout) as {
        matchingCallAccepted: boolean;
        matchingCallError: string;
        currentBeforeMismatch: string | null;
        currentAfterMismatch: string | null;
        operationsBeforeMismatch: number;
        operationsAfterMismatch: number;
        revisionsBeforeMismatch: number;
        revisionsAfterMismatch: number;
        mismatchRejectedSynchronously: boolean;
      };

      expect(launcherStderr).toBe("");
      expect(report.matchingCallError).toBe("");
      expect(report.matchingCallAccepted).toBe(true);
      expect(report.currentBeforeMismatch).not.toBeNull();
      expect(report.mismatchRejectedSynchronously, JSON.stringify(report)).toBe(true);
      expect(report.operationsAfterMismatch).toBe(report.operationsBeforeMismatch);
      expect(report.revisionsAfterMismatch).toBe(report.revisionsBeforeMismatch);
      expect(report.currentAfterMismatch).toBe(report.currentBeforeMismatch);
    } finally {
      if (launcher !== undefined) await stopChild(launcher);
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);
});
