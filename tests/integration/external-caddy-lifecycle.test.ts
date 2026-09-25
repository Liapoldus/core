import { execFile, spawn, type ChildProcess } from "node:child_process";
import { chmod, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { createInterface } from "node:readline";
import { tmpdir } from "node:os";
import { join, relative, resolve, sep } from "node:path";
import { request as httpRequest } from "node:http";
import { request as httpsRequest } from "node:https";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { buildGatewayTestBinary, startGateway } from "../support/gateway.js";
import { freeAddress } from "../support/http.js";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");
const externalBuildID = "external-caddy-fixture-build";

interface HTTPResult {
  readonly status: number;
  readonly body: string;
}

interface CaddyEvent {
  readonly name: string;
  readonly pid: number;
  readonly adminListen?: string;
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

function managementRequest(address: string, path: string, token: string): Promise<HTTPResult> {
  const port = Number(address.slice(address.lastIndexOf(":") + 1));
  return new Promise((resolveRequest, reject) => {
    const requestValue = httpsRequest({
      hostname: "127.0.0.1",
      port,
      path,
      method: "GET",
      rejectUnauthorized: false,
      headers: token === "" ? undefined : { Authorization: `Bearer ${token}` },
    }, (response) => {
      const chunks: Buffer[] = [];
      response.on("data", (chunk: Buffer) => chunks.push(Buffer.from(chunk)));
      response.once("end", () => resolveRequest({ status: response.statusCode ?? 0, body: Buffer.concat(chunks).toString("utf8") }));
    });
    requestValue.once("error", reject);
    requestValue.end();
  });
}

function unixAdminRequest(socketPath: string): Promise<HTTPResult> {
  return new Promise((resolveRequest, reject) => {
    const requestValue = httpRequest({
      socketPath,
      path: "/config/",
      method: "GET",
    }, (response) => {
      const chunks: Buffer[] = [];
      response.on("data", (chunk: Buffer) => chunks.push(Buffer.from(chunk)));
      response.once("end", () => resolveRequest({ status: response.statusCode ?? 0, body: Buffer.concat(chunks).toString("utf8") }));
    });
    requestValue.once("error", reject);
    requestValue.end();
  });
}

async function readEvents(path: string): Promise<CaddyEvent[]> {
  const contents = await readFile(path, "utf8").catch(() => "");
  return contents.split("\n").filter(Boolean).map((line) => JSON.parse(line) as CaddyEvent);
}

async function stopFixtureChildren(eventsPath: string): Promise<void> {
  const processIDs = [...new Set((await readEvents(eventsPath)).map((event) => event.pid))];
  for (const processID of processIDs) {
    try {
      process.kill(processID, "SIGTERM");
    } catch {
      // The Gateway supervisor may already have reaped this child.
    }
  }
  for (let attempt = 0; attempt < 30; attempt += 1) {
    const running = processIDs.filter((processID) => {
      try {
        process.kill(processID, 0);
        return true;
      } catch {
        return false;
      }
    });
    if (running.length === 0) return;
    await new Promise((resolveDelay) => setTimeout(resolveDelay, 100));
  }
  for (const processID of processIDs) {
    try {
      process.kill(processID, "SIGKILL");
    } catch {
      // Cleanup is best-effort after a bounded graceful stop.
    }
  }
}

function readJSONLines(child: ChildProcess): { next(): Promise<Record<string, unknown>>; write(value: unknown): void } {
  const lines = createInterface({ input: child.stdout! });
  const queue: string[] = [];
  const waiters: ((line: string) => void)[] = [];
  lines.on("line", (line) => {
    const waiter = waiters.shift();
    if (waiter) waiter(line);
    else queue.push(line);
  });
  return {
    next: async () => {
      const line = queue.shift() ?? await new Promise<string>((resolveLine, reject) => {
        const timer = setTimeout(() => reject(new Error("Timed out waiting for fixture host output.")), 10_000);
        waiters.push((value) => {
          clearTimeout(timer);
          resolveLine(value);
        });
      });
      return JSON.parse(line) as Record<string, unknown>;
    },
    write: (value) => child.stdin?.write(`${JSON.stringify(value)}\n`),
  };
}

async function waitFor(predicate: () => Promise<boolean>, description: string, timeoutMs = 30_000): Promise<void> {
  const attempts = Math.ceil(timeoutMs / 100);
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    try {
      if (await predicate()) return;
    } catch {
      // A listener becoming available is the condition being polled.
    }
    await new Promise((resolveDelay) => setTimeout(resolveDelay, 100));
  }
  throw new Error(`Timed out waiting for ${description}.`);
}

describe("supervised external Caddy Admin IPC", () => {
  it("runs the external boundary with private Admin IPC, acknowledged snapshots, and restart readiness", async () => {
    const directory = await mkdtemp(join(tmpdir(), "lc-"));
    const stateDirectory = join(directory, "state");
    const initialCaddyfile = join(directory, "initial.Caddyfile");
    const candidateCaddyfile = join(directory, "candidate.Caddyfile");
    const fixturePath = join(directory, "external-caddy-fixture");
    const externalBinary = join(directory, "liapoldus-caddy");
    const eventsPath = join(directory, "external-caddy-events.jsonl");
    const holdPath = join(directory, "hold-external-caddy-restart");
    const rejectPath = join(directory, "reject-external-caddy-load");
    const publicAddress = await freeAddress();
    let host: ChildProcess | undefined;
    try {
      await execFileAsync("go", ["build", "-o", fixturePath, "./tests/fixtures/external-caddy"], { cwd: coreRoot });
      await writeFile(externalBinary, [
        "#!/bin/sh",
        `export LIAPOLDUS_TEST_EXTERNAL_CADDY_EVENTS=${shellQuote(eventsPath)}`,
        `export LIAPOLDUS_TEST_EXTERNAL_CADDY_HOLD=${shellQuote(holdPath)}`,
        `export LIAPOLDUS_TEST_EXTERNAL_CADDY_REJECT=${shellQuote(rejectPath)}`,
        `exec ${shellQuote(fixturePath)} "$@"`,
        "",
      ].join("\n"), { mode: 0o700 });
      await chmod(externalBinary, 0o700);
      const initial = `http://${publicAddress} {\n  respond "external-active"\n}\n`;
      const candidate = `http://${publicAddress} {\n  respond "external-candidate"\n}\n`;
      await writeFile(initialCaddyfile, initial, "utf8");
      await writeFile(candidateCaddyfile, candidate, "utf8");

      host = spawn(fixturePath, ["component-host", externalBinary, stateDirectory, initialCaddyfile, externalBuildID], {
        cwd: coreRoot,
        stdio: ["pipe", "pipe", "pipe"],
      });
      let stderr = "";
      host.stderr?.on("data", (chunk: Buffer) => { stderr += chunk.toString(); });
      const protocol = readJSONLines(host);
      let started: Record<string, unknown>;
      try {
        started = await protocol.next();
      } catch (error) {
        throw new Error(`${String(error)} Child exit=${String(host.exitCode)} stderr=${stderr}`);
      }
      expect(started).toMatchObject({ event: "ready" });
      const socketPath = String(started.adminSocket);
      const relativeSocketPath = relative(resolve(stateDirectory), resolve(socketPath));
      expect(relativeSocketPath).not.toBe("");
      expect(relativeSocketPath).not.toBe("..");
      expect(relativeSocketPath.startsWith(`..${sep}`)).toBe(false);
      expect((await unixAdminRequest(socketPath)).status).toBe(200);
      expect(JSON.parse((await unixAdminRequest(socketPath)).body).fixtureGeneration).toBe(initial.trim());
      const publicAdmin = await fetch(`http://${publicAddress}/config/`).catch(() => undefined);
      expect(publicAdmin?.status ?? 0).not.toBe(200);
      const publicResponse = await fetch(`http://${publicAddress}/`);
      expect(publicResponse.status).toBe(200);
      expect(await publicResponse.text()).toBe("external-active");

      await writeFile(rejectPath, "reject", "utf8");
      protocol.write({ action: "activate", path: candidateCaddyfile });
      expect(await protocol.next()).toMatchObject({ action: "activate", activated: false });
      expect(JSON.parse((await unixAdminRequest(socketPath)).body).fixtureGeneration).toBe(initial.trim());
      protocol.write({ action: "ready" });
      expect(await protocol.next()).toMatchObject({ action: "ready", ready: true });

      protocol.write({ action: "activate", path: candidateCaddyfile });
      expect(await protocol.next()).toMatchObject({ action: "activate", activated: true });
      expect(JSON.parse((await unixAdminRequest(socketPath)).body).fixtureGeneration).toBe(candidate.trim());

      await writeFile(holdPath, "hold", "utf8");
      const eventsBeforeRestart = await readEvents(eventsPath);
      const currentPID = eventsBeforeRestart.findLast((event) => event.name === "ready")?.pid;
      expect(currentPID).toBeTypeOf("number");
      process.kill(currentPID!, "SIGKILL");
      await waitFor(async () => {
        const currentEvents = await readEvents(eventsPath);
        return currentEvents.filter((event) => event.name === "started").length >= 2
          && currentEvents.some((event) => event.name === "waiting");
      }, "supervisor restart of the external child");
      protocol.write({ action: "ready" });
      expect(await protocol.next()).toMatchObject({ action: "ready", ready: false });

      await rm(holdPath, { force: true });
      await waitFor(async () => (await readEvents(eventsPath)).filter((event) => event.name === "ready").length >= 2, "external child readiness after restart");
      protocol.write({ action: "ready" });
      expect(await protocol.next()).toMatchObject({ action: "ready", ready: true });
      protocol.write({ action: "stop" });
      expect(await protocol.next()).toMatchObject({ event: "stopped" });
      await new Promise<void>((resolveClose) => host!.once("close", () => resolveClose()));
      host = undefined;
      await stopFixtureChildren(eventsPath);
      expect(stderr).toBe("");
    } finally {
      await rm(holdPath, { force: true });
      if (host !== undefined) {
        host.kill("SIGTERM");
        await new Promise<void>((resolveClose) => host!.once("close", () => resolveClose()));
      }
      await stopFixtureChildren(eventsPath);
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);

  it("keeps Admin on a private Unix socket, restarts its child, and degrades data-plane readiness", async () => {
    const directory = await mkdtemp(join(tmpdir(), "lc-s-"));
    const stateDirectory = join(directory, "state");
    const database = join(stateDirectory, "gateway.db");
    const artifacts = join(stateDirectory, "artifacts");
    const config = join(directory, "gateway.yaml");
    const certificate = join(directory, "management.crt");
    const privateKey = join(directory, "management.key");
    const fixturePath = join(directory, "external-caddy-fixture");
    const externalBinary = join(directory, "liapoldus-caddy");
    const eventsPath = join(directory, "external-caddy-events.jsonl");
    const holdPath = join(directory, "hold-external-caddy-restart");
    const managementAddress = await freeAddress();
    const publicAddress = await freeAddress();
    let gateway: Awaited<ReturnType<typeof startGateway>> | undefined;

    try {
      await execFileAsync("openssl", [
        "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
        "-subj", "/CN=localhost",
        "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1",
        "-keyout", privateKey, "-out", certificate,
      ]);
      await execFileAsync("go", ["build", "-o", fixturePath, "./tests/fixtures/external-caddy"], { cwd: coreRoot });
      await writeFile(externalBinary, [
        "#!/bin/sh",
        `export LIAPOLDUS_TEST_EXTERNAL_CADDY_EVENTS=${shellQuote(eventsPath)}`,
        `export LIAPOLDUS_TEST_EXTERNAL_CADDY_HOLD=${shellQuote(holdPath)}`,
        `exec ${shellQuote(fixturePath)} "$@"`,
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
      await execFileAsync(fixturePath, ["seed", database, artifacts, publicAddress], { cwd: coreRoot });

      gateway = await startGateway(["--config", config, "serve"]);
      await waitFor(async () => (await managementRequest(managementAddress, "/healthz", "")).status === 200, "Management API");
      await waitFor(async () => (await readEvents(eventsPath)).some((event) => event.name === "ready"), "external Caddy Unix Admin socket", 8_000);

      const events = await readEvents(eventsPath);
      const firstReady = events.find((event) => event.name === "ready");
      expect(firstReady).toBeDefined();
      expect(firstReady?.adminListen).toMatch(/^unix\/\//);
      const socketPath = firstReady?.adminListen?.slice("unix//".length) ?? "";
      const relativeSocketPath = relative(resolve(stateDirectory), resolve(socketPath));
      expect(relativeSocketPath).not.toBe("");
      expect(relativeSocketPath).not.toBe("..");
      expect(relativeSocketPath.startsWith(`..${sep}`)).toBe(false);
      expect((await unixAdminRequest(socketPath)).status).toBe(200);

      const managementAdmin = await managementRequest(managementAddress, "/config/", token);
      expect(managementAdmin.status).toBe(404);
      const publicAdmin = await fetch(`http://${publicAddress}/config/`).catch(() => undefined);
      expect(publicAdmin?.status ?? 0).not.toBe(200);

      const activePublicResponse = await fetch(`http://${publicAddress}/`);
      expect(activePublicResponse.status).toBe(200);
      expect(await activePublicResponse.text()).toBe("external-active");
      const initialStatus = await managementRequest(managementAddress, "/api/status", token);
      expect(JSON.parse(initialStatus.body)).toMatchObject({
        dataPlaneReadiness: { state: "ready" },
      });

      await writeFile(holdPath, "hold", "utf8");
      process.kill(firstReady!.pid, "SIGKILL");
      await waitFor(async () => {
        const currentEvents = await readEvents(eventsPath);
        return currentEvents.filter((event) => event.name === "started").length >= 2
          && currentEvents.some((event) => event.name === "waiting");
      }, "supervisor restart of external Caddy");
      const degradedStatus = await managementRequest(managementAddress, "/api/status", token);
      expect(JSON.parse(degradedStatus.body)).toMatchObject({
        dataPlaneReadiness: { state: "not-ready", reason: "caddy-unavailable" },
      });

      await rm(holdPath, { force: true });
      await waitFor(async () => (await readEvents(eventsPath)).filter((event) => event.name === "ready").length >= 2, "restarted external Caddy Admin socket");
      await waitFor(async () => {
        const response = await managementRequest(managementAddress, "/api/status", token);
        return JSON.parse(response.body).dataPlaneReadiness?.state === "ready";
      }, "data-plane readiness restoration");
    } finally {
      await rm(holdPath, { force: true });
      if (gateway !== undefined) await gateway.stop();
      await stopFixtureChildren(eventsPath);
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);
});
