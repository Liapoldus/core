import { afterEach, describe, expect, it } from "vitest";
import { execFile, spawn, type ChildProcess, type ChildProcessWithoutNullStreams } from "node:child_process";
import { mkdir, mkdtemp, readFile, readlink, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { readFileSync } from "node:fs";
import { createInterface } from "node:readline";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { startGateway } from "../support/gateway.js";
import { freeAddress, request, waitReady, writeGatewayConfig } from "../support/http.js";

const token = "registry-lock-test-token";
const environment = { LIAPOLDUS_TEST_MANAGEMENT_TOKEN: token };
const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
const directories: string[] = [];
const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
const execFileAsync = promisify(execFile);

const vectors = JSON.parse(readFileSync(resolve(import.meta.dirname, "../../contracts/v1/golden-vectors.json"), "utf8")) as {
  vectors: Array<{ id: string; expected: Record<string, unknown> }>;
};
function expectedVector(id: string): Record<string, unknown> {
  const vector = vectors.vectors.find((candidate) => candidate.id === id);
  if (!vector) throw new Error(`${id} golden vector is missing`);
  return vector.expected;
}

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    if (!gateway.process.killed) gateway.process.kill("SIGTERM");
    await gateway.stop();
  }
  for (const directory of directories.splice(0)) await rm(directory, { recursive: true, force: true });
});

async function startRegistryGateway(workspace: string): Promise<{
  registry: string;
  managementAddress: string;
  lockPath: string;
  source: string;
}> {
  const registry = join(workspace, "registry");
  const source = join(workspace, "source");
  await mkdir(source);
  await writeFile(join(source, "site.yaml"), "index: index.html\n", "utf8");
  await writeFile(join(source, "index.html"), "registry lock fixture\n", "utf8");

  const managementAddress = await freeAddress();
  const configPath = await writeGatewayConfig([
    "registry:", `  path: ${registry}`,
    "sites:", "  blog:", "    source: { type: release, slug: blog }",
    "listeners:", "  web:", "    type: http", `    address: ${await freeAddress()}`,
    "    routes:", "      - when: { path: { prefix: / } }", "        then: { site: blog }",
    "management:", `  listener: { address: ${managementAddress} }`,
    "  staticToken: env:LIAPOLDUS_TEST_MANAGEMENT_TOKEN",
  ].join("\n"));
  const gateway = await startGateway(["--config", configPath, "serve"], environment);
  gateways.push(gateway);
  await waitReady(managementAddress);

  return { registry, managementAddress, lockPath: join(registry, "sites", "blog", ".publish.lock"), source };
}

async function publish(managementAddress: string, source: string, idempotencyKey: string, expectedCurrentRevision: string | null = null) {
  return request(managementAddress, "/api/sites/blog/publish", {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: JSON.stringify({ source, idempotencyKey, expectedCurrentRevision }),
  });
}

async function holdFilesystemLock(binary: string, path: string): Promise<{ process: ChildProcessWithoutNullStreams; stop(): Promise<void> }> {
  const child = spawn(binary, [path], { cwd: coreRoot });
  const lines = createInterface({ input: child.stdout });
  await new Promise<void>((resolveReady, reject) => {
    const timeout = setTimeout(() => reject(new Error("lock fixture did not become ready")), 10000);
    lines.once("line", (line) => {
      clearTimeout(timeout);
      if (line !== "ready") reject(new Error("lock fixture returned an unexpected status"));
      else resolveReady();
    });
    child.once("error", reject);
    child.once("exit", (code) => reject(new Error(`lock fixture exited (${code})`)));
    child.stderr.on("data", (data) => reject(new Error(String(data))));
  });
  return {
    process: child,
    stop: () => new Promise<void>((resolveClose) => {
      child.once("close", () => resolveClose());
      child.kill("SIGTERM");
    }),
  };
}

describe("filesystem registry publish locks", () => {
  it("rejects a live lock without changing current or previous releases", async () => {
    const workspace = await mkdtemp(join(tmpdir(), "liapoldus-registry-lock-active-"));
    directories.push(workspace);
    const fixture = await startRegistryGateway(workspace);
    const initial = await publish(fixture.managementAddress, fixture.source, "active-baseline-key");
    expect(initial.status).toBe(201);
    const revision = (JSON.parse(initial.text) as { result: { revision: string } }).result.revision;
    const currentPointer = await readlink(join(fixture.registry, "sites", "blog", "current"));

    const ownerPid = gateways.at(-1)?.process.pid;
    expect(ownerPid).toBeDefined();
    const activeLock = JSON.stringify({ pid: ownerPid, startedAt: new Date().toISOString(), nonce: "active-lock" });
    await writeFile(fixture.lockPath, activeLock, { mode: 0o600 });
    const response = await publish(fixture.managementAddress, fixture.source, "active-conflict-key", revision);

    expect(response.status).toBe(expectedVector("publish-lock-active").status);
    expect(response.text).toContain(expectedVector("publish-lock-active").code as string);
    expect(await readFile(fixture.lockPath, "utf8")).toBe(activeLock);
    expect(await readlink(join(fixture.registry, "sites", "blog", "current"))).toBe(currentPointer);
    await expect(readlink(join(fixture.registry, "sites", "blog", "previous"))).rejects.toMatchObject({ code: "ENOENT" });
  });

  it("recovers a lock owned by a nonexistent process and records the recovery", async () => {
    const workspace = await mkdtemp(join(tmpdir(), "liapoldus-registry-lock-stale-"));
    directories.push(workspace);
    const fixture = await startRegistryGateway(workspace);
    await mkdir(join(fixture.registry, "sites", "blog"), { recursive: true });
    await writeFile(fixture.lockPath, JSON.stringify({ pid: 999999, startedAt: "2026-01-01T00:00:00Z", nonce: "n" }), { mode: 0o600 });

    const response = await publish(fixture.managementAddress, fixture.source, "stale-lock-recovery-key");
    const auditResponse = await request(fixture.managementAddress, "/api/audit", {
      headers: { Authorization: `Bearer ${token}` },
    });
    const audit = JSON.parse(auditResponse.text) as { items: Array<{ action: string }> };

    expect(response.status).toBe(201);
    expect(await readlink(join(fixture.registry, "sites", "blog", "current"))).toContain(JSON.parse(response.text).result.revision as string);
    expect(audit.items.some((item) => item.action === expectedVector("publish-lock-recovery").auditAction)).toBe(true);
  });

  it("rejects a lock held by another process and publishes once it is released", async () => {
    const workspace = await mkdtemp(join(tmpdir(), "liapoldus-registry-lock-process-"));
    directories.push(workspace);
    const fixture = await startRegistryGateway(workspace);
    await mkdir(dirname(fixture.lockPath), { recursive: true });
    const lockHolder = join(workspace, "registry-lock-holder");
    await execFileAsync("go", ["build", "-o", lockHolder, "./tests/fixtures/registry-lock-holder"], { cwd: coreRoot });
    const holder = await holdFilesystemLock(lockHolder, fixture.lockPath);

    const rejected = await publish(fixture.managementAddress, fixture.source, "process-lock-key");
    expect(rejected.status).toBe(expectedVector("publish-lock-active").status);
    expect(rejected.text).toContain(expectedVector("publish-lock-active").code as string);
    await expect(readlink(join(fixture.registry, "sites", "blog", "current"))).rejects.toMatchObject({ code: "ENOENT" });

    await holder.stop();
    const published = await publish(fixture.managementAddress, fixture.source, "process-lock-key");
    expect(published.status).toBe(201);
    expect(await readlink(join(fixture.registry, "sites", "blog", "current"))).toContain(
      (JSON.parse(published.text) as { result: { revision: string } }).result.revision,
    );
  }, 30000);
});
