import { execFile, spawn } from "node:child_process";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";

export interface GatewayResult {
  readonly exitCode: number;
  readonly stdout: string;
  readonly stderr: string;
}

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
const execFileAsync = promisify(execFile);
let binary: Promise<string> | undefined;

async function gatewayBinary(): Promise<string> {
  binary ??= (async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-gateway-bin-"));
    const path = join(directory, "gateway");
    await execFileAsync("go", ["build", "-o", path, "./cmd/gateway"], { cwd: coreRoot });
    return path;
  })();
  return binary;
}

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
