import { execFile, spawn, type ChildProcess } from "node:child_process";
import { afterAll } from "vitest";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { env } from "node:process";

export interface GatewayResult {
  readonly exitCode: number;
  readonly stdout: string;
  readonly stderr: string;
}

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
const execFileAsync = promisify(execFile);
let binary: Promise<string> | undefined;
let binaryDirectory: string | undefined;

async function gatewayBinary(): Promise<string> {
  binary ??= (async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-gateway-bin-"));
    binaryDirectory = directory;
    const path = join(directory, "gateway");
    await execFileAsync("go", ["build", "-o", path, "./cmd/gateway"], { cwd: coreRoot });
    return path;
  })();
  return binary;
}

export function buildGatewayTestBinary(): Promise<string> {
  return gatewayBinary();
}

export async function cleanupGatewayTestBinary(): Promise<void> {
  const directory = binaryDirectory;
  if (directory === undefined) return;
  binaryDirectory = undefined;
  binary = undefined;
  await rm(directory, { recursive: true, force: true });
}

afterAll(cleanupGatewayTestBinary);

export async function runGateway(
  args: readonly string[],
  environment: NodeJS.ProcessEnv = {},
): Promise<GatewayResult> {
	const executable = await gatewayBinary();
  return new Promise((resolve, reject) => {
    const child = spawn(executable, args, {
      cwd: coreRoot,
      env: { ...process.env, ...environment },
      stdio: ["ignore", "pipe", "pipe"],
    });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk: Buffer) => { stdout += chunk; });
    child.stderr.on("data", (chunk: Buffer) => { stderr += chunk; });
    child.once("error", reject);
    child.once("close", (code) => {
      resolve({ exitCode: code ?? 1, stdout, stderr });
    });
  });
}

export function jsonOutput(result: GatewayResult): Record<string, unknown> {
  return JSON.parse(result.stdout) as Record<string, unknown>;
}

export async function startGateway(
  args: readonly string[],
  environment: NodeJS.ProcessEnv = {},
): Promise<{ process: ChildProcess; stop(): Promise<void> }> {
  const executable = await gatewayBinary();
  const process = spawn(executable, args, { cwd: coreRoot, env: { ...env, ...environment }, stdio: "ignore" });
  return {
    process,
    stop: () => {
      if (process.exitCode !== null) {
        return Promise.resolve();
      }
      return new Promise((resolve) => {
        process.once("close", () => resolve());
        process.kill("SIGTERM");
      });
    },
  };
}
