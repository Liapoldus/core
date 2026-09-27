import { execFile, type ChildProcess } from "node:child_process";
import { createServer } from "node:net";
import { gzipSync } from "node:zlib";
import { chmod, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { request as httpsRequest } from "node:https";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { buildGatewayTestBinary, observeChildClose, startGatewayWithOutput } from "../support/gateway.js";
import { freeAddress } from "../support/http.js";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");
const externalBuildID = "external-caddy-fixture-build";
const activationToken = "crash-candidate-release";

interface HTTPResult {
  readonly status: number;
  readonly body: string;
}

interface FixtureEvent {
  readonly name: string;
  readonly pid: number;
  readonly detail?: string;
}

interface ProcessState {
  readonly currentRevision: string | null;
  readonly previousRevision: string | null;
  readonly operationState: string;
  readonly journalState: string;
  readonly pendingCount: number;
  readonly pendingOperation: string;
  readonly caddyfilePath: string;
  readonly artifactPath: string;
  readonly caddyfileExists: boolean;
  readonly artifactExists: boolean;
}

function managementRequest(
  address: string,
  path: string,
  token: string,
  method = "GET",
  body?: Buffer,
  contentType?: string,
): Promise<HTTPResult> {
  const port = Number(address.slice(address.lastIndexOf(":") + 1));
  return new Promise((resolve, reject) => {
    const request = httpsRequest({
      hostname: "127.0.0.1",
      port,
      path,
      method,
      rejectUnauthorized: false,
      headers: {
        Authorization: `Bearer ${token}`,
        ...(body === undefined ? {} : { "Content-Length": body.byteLength }),
        ...(contentType === undefined ? {} : { "Content-Type": contentType }),
      },
    }, (response) => {
      const chunks: Buffer[] = [];
      response.on("data", (chunk: Buffer) => chunks.push(Buffer.from(chunk)));
      response.once("end", () => resolve({ status: response.statusCode ?? 0, body: Buffer.concat(chunks).toString("utf8") }));
    });
    request.once("error", reject);
    if (body !== undefined) request.write(body);
    request.end();
  });
}

async function waitFor<T>(check: () => Promise<T | undefined>, description: string, timeoutMs = 30_000): Promise<T> {
  const deadline = Date.now() + timeoutMs;
  let lastError: unknown;
  while (Date.now() < deadline) {
    try {
      const value = await check();
      if (value !== undefined) return value;
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  throw new Error(`Timed out waiting for ${description}${lastError === undefined ? "" : `: ${String(lastError)}`}`);
}

async function readEvents(path: string): Promise<FixtureEvent[]> {
  const contents = await readFile(path, "utf8").catch(() => "");
  return contents.split("\n").filter(Boolean).map((line) => JSON.parse(line) as FixtureEvent);
}

async function processState(fixture: string, database: string, artifacts: string, operationID: string, staged?: ProcessState): Promise<ProcessState> {
  const args = [fixture, "inspect", database, artifacts, operationID];
  if (staged !== undefined) args.push(staged.caddyfilePath, staged.artifactPath);
  const result = await execFileAsync(fixture, args.slice(1), { cwd: coreRoot });
  return JSON.parse(result.stdout) as ProcessState;
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

function tarGzip(path: string, value: string): Buffer {
  const content = Buffer.from(value, "utf8");
  const header = Buffer.alloc(512);
  header.write(path, 0, 100, "utf8");
  header.write("0000644\0", 100, 8, "ascii");
  header.write("0000000\0", 108, 8, "ascii");
  header.write("0000000\0", 116, 8, "ascii");
  header.write(`${content.byteLength.toString(8).padStart(11, "0")}\0`, 124, 12, "ascii");
  header.write("00000000000\0", 136, 12, "ascii");
  header.fill(0x20, 148, 156);
  header.write("0", 156, 1, "ascii");
  header.write("ustar\0", 257, 6, "ascii");
  header.write("00", 263, 2, "ascii");
  const checksum = header.reduce((sum, byte) => sum + byte, 0);
  header.write(`${checksum.toString(8).padStart(6, "0")}\0 `, 148, 8, "ascii");
  const padding = Buffer.alloc((512 - (content.byteLength % 512)) % 512);
  return gzipSync(Buffer.concat([header, content, padding, Buffer.alloc(1024)]));
}

function releaseBody(caddyfile: string, currentRevision: string): { body: Buffer; contentType: string } {
  const boundary = "liapoldus-process-crash-boundary";
  const archive = tarGzip("frontends/ui/index.html", "candidate frontend");
  const chunks = [
    Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="metadata"\r\nContent-Type: application/json\r\n\r\n${JSON.stringify({ idempotencyKey: "process-crash-candidate-0001", expectedCurrentRevision: currentRevision })}\r\n`, "utf8"),
    Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="caddyfile"; filename="Caddyfile"\r\nContent-Type: text/plain\r\n\r\n${caddyfile}\r\n`, "utf8"),
    Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="artifact"; filename="frontend.tar.gz"\r\nContent-Type: application/gzip\r\n\r\n`, "utf8"),
    archive,
    Buffer.from(`\r\n--${boundary}--\r\n`, "utf8"),
  ];
  return { body: Buffer.concat(chunks), contentType: `multipart/form-data; boundary=${boundary}` };
}

async function waitPortReleased(address: string): Promise<void> {
  const port = Number(address.slice(address.lastIndexOf(":") + 1));
  await waitFor(() => new Promise<boolean | undefined>((resolve) => {
    const server = createServer();
    server.once("error", () => resolve(undefined));
    server.listen(port, "127.0.0.1", () => server.close((error) => resolve(error ? undefined : true)));
  }), `external Caddy to release port ${port}`);
}

async function stopFixtureChildren(pids: Set<number>): Promise<void> {
  for (const pid of pids) {
    try {
      process.kill(pid, "SIGTERM");
    } catch {
      continue;
    }
  }
}

describe("Gateway process group-release crash recovery", () => {
  it("recovers the old current/previous and served snapshot after Caddy accepted a candidate but before SQLite commit", async () => {
    const directory = await mkdtemp(join(tmpdir(), "lc-"));
    const stateDirectory = join(directory, "state");
    const database = join(stateDirectory, "gateway.db");
    const artifacts = join(stateDirectory, "artifacts");
    const config = join(directory, "gateway.yaml");
    const certificate = join(directory, "management.crt");
    const privateKey = join(directory, "management.key");
    const fixture = join(directory, "external-caddy-fixture");
    const stateFixture = join(directory, "group-release-process-state");
    const externalBinary = join(directory, "liapoldus-caddy");
    const eventsPath = join(directory, "external-caddy-events.jsonl");
    const externalLog = join(directory, "external-caddy.log");
    const startupHoldPath = join(directory, "hold-startup");
    const activationBarrierPath = join(directory, "hold-caddy-activation");
    const managementAddress = await freeAddress();
    const publicAddress = await freeAddress();
    const orphanedCaddyPids = new Set<number>();
    let gateway: Awaited<ReturnType<typeof startGatewayWithOutput>> | undefined;

    try {
      await execFileAsync("openssl", [
        "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
        "-subj", "/CN=localhost",
        "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1",
        "-keyout", privateKey, "-out", certificate,
      ]);
      await writeFile(activationBarrierPath, "hold", "utf8");
      await Promise.all([
        execFileAsync("go", ["build", "-o", fixture, "./tests/fixtures/external-caddy"], { cwd: coreRoot }),
        execFileAsync("go", ["build", "-o", stateFixture, "./tests/fixtures/group-release-process-state"], { cwd: coreRoot }),
      ]);
      await writeFile(externalBinary, [
        "#!/bin/sh",
        `export LIAPOLDUS_TEST_EXTERNAL_CADDY_EVENTS=${shellQuote(eventsPath)}`,
        `export LIAPOLDUS_TEST_EXTERNAL_CADDY_HOLD=${shellQuote(startupHoldPath)}`,
        `export LIAPOLDUS_TEST_EXTERNAL_CADDY_ACTIVATION_BARRIER=${shellQuote(activationBarrierPath)}`,
        `export LIAPOLDUS_TEST_EXTERNAL_CADDY_ACTIVATION_TOKEN=${shellQuote(activationToken)}`,
        `exec ${shellQuote(fixture)} "$@" 2>>${shellQuote(externalLog)}`,
        "",
      ].join("\n"), { mode: 0o700 });
      await chmod(externalBinary, 0o700);
      await writeFile(config, [
        "state:",
        `  path: ${database}`,
        "artifacts:",
        `  path: ${artifacts}`,
        "management:",
        `  listen: ${managementAddress}`,
        "  tls:",
        `    certificate: file:${certificate}`,
        `    key: file:${privateKey}`,
        "caddy:",
        "  variant: external",
        `  binary: ${externalBinary}`,
        `  expectedBuildID: ${externalBuildID}`,
        "",
      ].join("\n"), "utf8");

      const gatewayBinary = await buildGatewayTestBinary();
      const bootstrap = await execFileAsync(gatewayBinary, ["--config", config, "access", "bootstrap"]);
      const token = bootstrap.stdout.trim();
      expect(token).not.toBe("");
      const seed = await execFileAsync(stateFixture, ["seed", database, artifacts, publicAddress], { cwd: coreRoot });
      const pointers = JSON.parse(seed.stdout) as { currentRevision: string; previousRevision: string };

      gateway = await startGatewayWithOutput(["--config", config, "serve"]);
      await waitFor(async () => (await managementRequest(managementAddress, "/healthz", token)).status === 200 || undefined, "Gateway Management API");
      try {
        await waitFor(async () => {
        const events = await readEvents(eventsPath);
        for (const event of events) if (event.name === "started") orphanedCaddyPids.add(event.pid);
        return events.find((event) => event.name === "ready");
        }, "initial external Caddy readiness", 10_000);
      } catch (error) {
        throw new Error(`${String(error)}\nGateway exit: ${gateway.process.exitCode}\nGateway stderr: ${gateway.stderr}\nExternal log: ${await readFile(externalLog, "utf8").catch(() => "<missing>")}\nExternal events: ${await readFile(eventsPath, "utf8").catch(() => "<missing>")}`);
      }
      const initialResponse = await fetch(`http://${publicAddress}/`);
      expect(initialResponse.status).toBe(200);
      expect(await initialResponse.text()).toBe("current-release");

      const candidateCaddyfile = `http://${publicAddress} {\n  respond "${activationToken}"\n}\n`;
      const multipart = releaseBody(candidateCaddyfile, pointers.currentRevision);
      const accepted = await managementRequest(
        managementAddress, "/api/groups/system/releases", token, "POST", multipart.body, multipart.contentType,
      );
      expect(accepted.status).toBe(202);
      const operationID = (JSON.parse(accepted.body) as { operationId: string }).operationId;

      const blockedActivation = await waitFor(async () => {
        const events = await readEvents(eventsPath);
        for (const event of events) if (event.name === "started") orphanedCaddyPids.add(event.pid);
        return events.find((event) => event.name === "activation-blocked" && event.detail === activationToken);
      }, "fixture-confirmed post-load/pre-response barrier", 15_000);
      orphanedCaddyPids.add(blockedActivation.pid);

      const candidateResponse = await fetch(`http://${publicAddress}/`);
      expect(candidateResponse.status).toBe(200);
      expect(await candidateResponse.text()).toBe(activationToken);
      const beforeCrash = await processState(stateFixture, database, artifacts, operationID);
      expect(beforeCrash).toMatchObject({
        currentRevision: pointers.currentRevision,
        previousRevision: pointers.previousRevision,
        operationState: "pending",
        journalState: "pending",
        pendingCount: 1,
        pendingOperation: operationID,
        caddyfileExists: true,
        artifactExists: true,
      });

      const gatewayClosed = observeChildClose(gateway.process);
      gateway.process.kill("SIGKILL");
      await gatewayClosed;
      gateway = undefined;
      await stopFixtureChildren(orphanedCaddyPids);
      await waitPortReleased(publicAddress);
      await rm(activationBarrierPath, { force: true });

      gateway = await startGatewayWithOutput(["--config", config, "serve"]);
      await waitFor(async () => (await managementRequest(managementAddress, "/healthz", token)).status === 200 || undefined, "restarted Gateway Management API");
      await waitFor(async () => {
        const events = await readEvents(eventsPath);
        for (const event of events) if (event.name === "started") orphanedCaddyPids.add(event.pid);
        return events.filter((event) => event.name === "ready").length >= 2 ? true : undefined;
      }, "recovered external Caddy readiness");

      const recoveredResponse = await fetch(`http://${publicAddress}/`);
      expect(recoveredResponse.status).toBe(200);
      expect(await recoveredResponse.text()).toBe("current-release");
      const afterRecovery = await processState(
        stateFixture, database, artifacts, operationID, beforeCrash,
      );
      expect(afterRecovery).toMatchObject({
        currentRevision: pointers.currentRevision,
        previousRevision: pointers.previousRevision,
        operationState: "failed",
        journalState: "failed",
        pendingCount: 0,
        pendingOperation: "",
        caddyfileExists: false,
        artifactExists: false,
      });
    } finally {
      await rm(activationBarrierPath, { force: true });
      if (gateway !== undefined) await gateway.stop();
      const events = await readEvents(eventsPath);
      for (const event of events) if (event.name === "started") orphanedCaddyPids.add(event.pid);
      await stopFixtureChildren(orphanedCaddyPids);
      await waitPortReleased(publicAddress);
      await rm(directory, { recursive: true, force: true });
    }
  }, 180_000);
});
