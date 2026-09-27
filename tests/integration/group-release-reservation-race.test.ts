import { execFile, spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { createInterface } from "node:readline";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const coreRoot = join(import.meta.dirname, "../..");
const managementToken = "fixture-management-token";
const execFileAsync = promisify(execFile);

interface FixtureEvent {
  readonly kind: string;
  readonly address?: string;
  readonly activationCount?: number;
  readonly validationCount?: number;
}

interface PublishResponse {
  readonly status: number;
  readonly body: Record<string, unknown>;
}

class EventQueue {
  private readonly events: FixtureEvent[] = [];
  private readonly waiters: Array<{ kind: string; resolve: (event: FixtureEvent) => void }> = [];

  push(event: FixtureEvent): void {
    const waiterIndex = this.waiters.findIndex((waiter) => waiter.kind === event.kind);
    if (waiterIndex >= 0) {
      const [waiter] = this.waiters.splice(waiterIndex, 1);
      waiter?.resolve(event);
      return;
    }
    this.events.push(event);
  }

  wait(kind: string): Promise<FixtureEvent> {
    const eventIndex = this.events.findIndex((event) => event.kind === kind);
    if (eventIndex >= 0) {
      const [event] = this.events.splice(eventIndex, 1);
      return Promise.resolve(event as FixtureEvent);
    }
    return new Promise((resolve) => this.waiters.push({ kind, resolve }));
  }
}

async function withDeadline<T>(promise: Promise<T>, timeoutMs: number, message: string): Promise<T> {
  let timer: NodeJS.Timeout | undefined;
  return Promise.race([
    promise,
    new Promise<never>((_, reject) => { timer = setTimeout(() => reject(new Error(message)), timeoutMs); }),
  ]).finally(() => {
    if (timer !== undefined) clearTimeout(timer);
  });
}

interface RunningFixture {
  readonly address: string;
  readonly process: ChildProcessWithoutNullStreams;
  waitForActivation(): Promise<FixtureEvent>;
  waitForValidation(): Promise<FixtureEvent>;
  releaseActivation(): void;
  asyncState(): Promise<FixtureEvent>;
  stop(): Promise<void>;
}

async function startFixture(directory: string, mode: "duplicate" | "stale"): Promise<RunningFixture> {
  const binary = join(directory, "group-release-reservation-race");
  await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/group-release-reservation-race"], { cwd: coreRoot });
  const child = spawn(binary, [join(directory, "gateway.db"), mode], { cwd: coreRoot, stdio: ["pipe", "pipe", "pipe"] });
  const events = new EventQueue();
  const lines = createInterface({ input: child.stdout });
  let stderr = "";
  child.stderr.on("data", (chunk: Buffer) => { stderr += chunk.toString("utf8"); });
  lines.on("line", (line) => {
    try {
      events.push(JSON.parse(line) as FixtureEvent);
    } catch {
      child.kill("SIGTERM");
    }
  });
  const closed = new Promise<void>((resolve) => child.once("close", () => resolve()));
  const ready = await withDeadline(events.wait("ready"), 60_000, `fixture did not start: ${stderr}`);
  if (ready.address === undefined) throw new Error("fixture did not provide its HTTP address");

  return {
    address: ready.address,
    process: child,
    waitForActivation: () => events.wait("activation_started"),
    waitForValidation: () => events.wait("validation_entered"),
    releaseActivation: () => { child.stdin.write("release\n"); },
    asyncState: async () => {
      child.stdin.write("state\n");
      return events.wait("state");
    },
    stop: async () => {
      if (child.exitCode === null && child.signalCode === null) {
        child.stdin.write("release\n");
        child.kill("SIGTERM");
      }
      await closed;
      lines.close();
    },
  };
}

function multipartBody(idempotencyKey: string, responseText: string): string {
  const boundary = "liapoldus-release-race-boundary";
  const metadata = JSON.stringify({ idempotencyKey, expectedCurrentRevision: null });
  const releaseCaddyfile = `race.example.test {\n  respond "${responseText}"\n}\n`;
  return [
    `--${boundary}\r\nContent-Disposition: form-data; name="metadata"\r\nContent-Type: application/json\r\n\r\n${metadata}\r\n`,
    `--${boundary}\r\nContent-Disposition: form-data; name="caddyfile"; filename="Caddyfile"\r\nContent-Type: text/plain\r\n\r\n${releaseCaddyfile}\r\n`,
    `--${boundary}--\r\n`,
  ].join("");
}

async function publish(address: string, key: string, responseText: string): Promise<PublishResponse> {
  const boundary = "liapoldus-release-race-boundary";
  const response = await fetch(`${address}/api/groups/release-race/releases`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${managementToken}`,
      "Content-Type": `multipart/form-data; boundary=${boundary}`,
    },
    body: multipartBody(key, responseText),
    signal: AbortSignal.timeout(15_000),
  });
  return {
    status: response.status,
    body: await response.json() as Record<string, unknown>,
  };
}

async function waitForOperation(address: string, operationID: string): Promise<Record<string, unknown>> {
  const deadline = Date.now() + 15_000;
  let operation: Record<string, unknown> = {};
  while (Date.now() < deadline) {
    const response = await fetch(`${address}/api/operations/${operationID}`, {
      headers: { Authorization: `Bearer ${managementToken}` },
    });
    operation = await response.json() as Record<string, unknown>;
    if (operation.state === "succeeded" || operation.state === "failed") return operation;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error(`operation ${operationID} did not finish: ${JSON.stringify(operation)}`);
}

async function releaseCount(address: string): Promise<number> {
  const response = await fetch(`${address}/api/groups/release-race/releases`, {
    headers: { Authorization: `Bearer ${managementToken}` },
  });
  const body = await response.json() as { items?: unknown[] };
  return body.items?.length ?? -1;
}

describe("concurrent group release reservation", () => {
  it("deduplicates simultaneous identical multipart requests to one durable operation and revision", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-release-duplicate-race-"));
    const fixture = await startFixture(directory, "duplicate");
    try {
      const responsesPromise = Promise.all([
        publish(fixture.address, "same-race-key-00001", "same-release"),
        publish(fixture.address, "same-race-key-00001", "same-release"),
      ]);
      await withDeadline(
        Promise.all([fixture.waitForValidation(), fixture.waitForValidation()]),
        15_000,
        "both concurrent requests did not reach release validation",
      );
      const responses = await responsesPromise;
      fixture.releaseActivation();
      const operationIDs = responses.map((response) => response.body.operationId).filter((id): id is string => typeof id === "string");
      await Promise.all([...new Set(operationIDs)].map((id) => waitForOperation(fixture.address, id)));
      const state = await fixture.asyncState();

      expect(responses.map((response) => response.status)).toEqual([202, 202]);
      expect(operationIDs).toHaveLength(2);
      expect(operationIDs[0]).toBe(operationIDs[1]);
      expect(state.activationCount).toBe(1);
      expect(await releaseCount(fixture.address)).toBe(1);
    } finally {
      await fixture.stop();
      await rm(directory, { recursive: true, force: true });
    }
  }, 90_000);

  it("rejects a distinct stale-CAS release while the first release is pending, before a second activation", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-release-stale-race-"));
    const fixture = await startFixture(directory, "stale");
    try {
      const first = await publish(fixture.address, "first-race-key-0001", "first-release");
      expect(first.status).toBe(202);
      const firstOperationID = String(first.body.operationId);
      await withDeadline(fixture.waitForActivation(), 15_000, "first release never entered activation");
      const second = await publish(fixture.address, "second-race-key-001", "second-release");
      fixture.releaseActivation();
      await waitForOperation(fixture.address, firstOperationID);
      if (second.status === 202 && typeof second.body.operationId === "string") {
        await waitForOperation(fixture.address, second.body.operationId);
      }
      const state = await fixture.asyncState();

      expect(second.status).toBe(409);
      expect(second.body.code).toBe("group_revision_conflict");
      expect(state.activationCount).toBe(1);
      expect(await releaseCount(fixture.address)).toBe(1);
    } finally {
      await fixture.stop();
      await rm(directory, { recursive: true, force: true });
    }
  }, 90_000);
});
