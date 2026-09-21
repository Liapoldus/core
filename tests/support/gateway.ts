import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";

export interface GatewayResult {
  readonly exitCode: number;
  readonly stdout: string;
  readonly stderr: string;
}

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

export async function runGateway(
  args: readonly string[],
  environment: NodeJS.ProcessEnv = {},
): Promise<GatewayResult> {
  return new Promise((resolve, reject) => {
    const child = spawn("go", ["run", "./cmd/gateway", ...args], {
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
