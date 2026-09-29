import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("plugin settings startup recovery", () => {
	 it("durably stages raw settings before 202 and recovers from the staging generation", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-plugin-settings-reservation-"));
    const databasePath = join(directory, "core.db");
    try {
      const reserved = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/plugin-settings-recovery", "reserve-only", databasePath],
        { cwd: coreRoot },
      );
      const reservation = JSON.parse(reserved.stdout) as {
        acceptedStatus: number;
        operationId: string;
        payload: { version: number; resource: string; expectedRevision: number; schemaVersion: number; digest: string };
        payloadFound: boolean;
        candidateRevision: number;
      };
      expect(reservation).toMatchObject({
        acceptedStatus: 202,
        operationId: expect.any(String),
        candidateRevision: 2,
        payloadFound: true,
        payload: {
          version: 1,
          resource: "fixture",
          expectedRevision: 1,
          schemaVersion: 1,
          digest: expect.stringMatching(/^[a-f0-9]{64}$/),
        },
      });

      const recovered = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/plugin-settings-recovery", "recover", databasePath],
        { cwd: coreRoot },
      );
      expect(JSON.parse(recovered.stdout)).toMatchObject({
        managementReady: true,
        applyCalls: [{ revision: "2", config: '{ "origin" : "recovered" }' }],
        operation: { id: reservation.operationId, state: "succeeded" },
        settings: { revision: 2, config: { origin: "recovered" } },
        replayOperationId: reservation.operationId,
      });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }, 60_000);

  it("fails a staged operation with corrupted metadata without applying or switching settings", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-plugin-settings-corrupt-payload-"));
    const databasePath = join(directory, "core.db");
    try {
      const reserved = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/plugin-settings-recovery", "reserve-only", databasePath],
        { cwd: coreRoot },
      );
      const operationId = (JSON.parse(reserved.stdout) as { operationId: string }).operationId;
      await execFileAsync("go", ["run", "./tests/fixtures/plugin-settings-recovery", "corrupt-payload", databasePath], { cwd: coreRoot });
      const recovered = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/plugin-settings-recovery", "recover", databasePath],
        { cwd: coreRoot },
      );
      expect(JSON.parse(recovered.stdout)).toMatchObject({
        managementReady: true,
        applyCalls: [],
        operation: { id: operationId, state: "failed", errorCode: "activation_failed" },
        settings: { revision: 1, config: { origin: "old" } },
        pendingRevision: 0,
      });
      expect(recovered.stdout).not.toContain("recovered");
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }, 60_000);

  it("reconciles a reserved operation and durable candidate before management readiness", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-plugin-settings-recovery-"));
    const databasePath = join(directory, "core.db");
    try {
      const interrupted = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/plugin-settings-recovery", "interrupt", databasePath],
        { cwd: coreRoot },
      );
      const interruptedReport = JSON.parse(interrupted.stdout) as {
        acceptedStatus: number;
        operationId: string;
        candidateRevision: number;
        activeRevision: number;
        operationState: string;
        pendingRevision: number;
      };
      expect(interruptedReport).toMatchObject({
        acceptedStatus: 202,
        operationId: expect.any(String),
        candidateRevision: 2,
        activeRevision: 2,
        operationState: "running",
        pendingRevision: 0,
      });

      const unavailableRestart = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/plugin-settings-recovery", "recover", databasePath, "unavailable"],
        { cwd: coreRoot },
      );
      const unavailableReport = JSON.parse(unavailableRestart.stdout) as {
        managementReady: boolean;
        applyCalls: Array<{ revision: string; config: string }>;
        operation: { id?: string; state?: string };
        settings: { revision?: number };
        pendingRevision: number;
        instanceFenced: boolean;
      };
      expect(unavailableReport.managementReady).toBe(true);
      expect(unavailableReport.applyCalls).toEqual([{ revision: "2", config: '{ "origin" : "recovered" }' }]);
      expect(unavailableReport.operation).toMatchObject({ id: interruptedReport.operationId, state: "running" });
      expect(unavailableReport.settings.revision).toBe(2);
      expect(unavailableReport.pendingRevision).toBe(0);
      expect(unavailableReport.instanceFenced).toBe(true);

      const restarted = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/plugin-settings-recovery", "recover", databasePath],
        { cwd: coreRoot },
      );
      const report = JSON.parse(restarted.stdout) as {
        recoverySupported: boolean;
        managementReady: boolean;
        applyCalls: Array<{ revision: string; config: string }>;
        operation: { id?: string; state?: string; errorCode?: string };
        settings: { revision?: number; config?: Record<string, unknown> };
        replayStatus: number;
        replayOperationId: string;
        auditActor: string;
        auditActions: string[];
      };
      expect(report.recoverySupported).toBe(true);
      expect(report.managementReady).toBe(true);
      expect(report.applyCalls).toEqual([{ revision: "2", config: '{ "origin" : "recovered" }' }]);
      expect(report.operation).toMatchObject({ id: interruptedReport.operationId, state: "succeeded" });
      expect(report.settings).toMatchObject({ revision: 2, config: { origin: "recovered" } });
      expect(report.replayStatus).toBe(202);
      expect(report.replayOperationId).toBe(interruptedReport.operationId);
      expect(report.auditActor).toBe("static-token");
      expect(report.auditActions).toContain("plugin_settings.apply");
      expect(report.auditActions.filter((action) => action === "plugin_settings.candidate")).toHaveLength(1);

      const restartedAgain = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/plugin-settings-recovery", "recover", databasePath],
        { cwd: coreRoot },
      );
      const repeated = JSON.parse(restartedAgain.stdout) as {
        managementReady: boolean;
        applyCalls: Array<unknown>;
        operation: { id?: string; state?: string };
        settings: { revision?: number };
        auditActions: string[];
      };
      expect(repeated.managementReady).toBe(true);
      expect(repeated.applyCalls).toEqual([]);
      expect(repeated.operation).toMatchObject({ id: interruptedReport.operationId, state: "succeeded" });
      expect(repeated.settings.revision).toBe(2);
      expect(repeated.auditActions.filter((action) => action === "plugin_settings.candidate")).toHaveLength(1);
      expect(repeated.auditActions.filter((action) => action === "plugin_settings.apply")).toHaveLength(1);
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }, 60_000);
});
