import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("plugin settings mutation API", () => {
  it("preserves direct raw JSON bytes and enforces CAS, duplicate-key, size and audit contracts", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-plugin-settings-mutation-"));
    try {
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/plugin-settings-mutation", join(directory, "core.db")],
        { cwd: coreRoot },
      );
      const report = JSON.parse(result.stdout) as {
        acceptedStatus: number;
        accepted: { operationId?: string; state?: string; requestId?: string };
        operation: { id?: string; kind?: string; state?: string; resourceId?: string; errorCode?: string };
        applyCalls: Array<{ revision: string; config: string }>;
        settings: { revision?: string; config?: Record<string, unknown> };
        replayStatus: number;
        replayOperationId: string;
        conflictingReplayStatus: number;
        conflictingReplayCode: string;
        staleStatus: number;
        staleCode: string;
        invalidStatus: number;
        invalidUTF8Status: number;
        tooLargeStatus: number;
        activeRaw: string;
        activeDigest: string;
        slots: string[];
        rejectedOperation: { state?: string; errorCode?: string };
        settingsAfterReject: { revision?: string; config?: Record<string, unknown> };
        auditedActor: string;
        auditActions: string[];
        firstConfig: { acceptedStatus: number; state: string; revision: string; raw: string };
      };

      expect(report.acceptedStatus).toBe(202);
      expect(report.accepted).toMatchObject({
        operationId: expect.any(String),
        state: expect.stringMatching(/^(pending|running)$/),
        requestId: expect.any(String),
      });
      expect(report.operation).toMatchObject({
        id: report.accepted.operationId,
        kind: expect.any(String),
        state: "succeeded",
        resourceId: "fixture",
      });
      expect(report.applyCalls).toEqual([
        { revision: "2", config: "{ \"origin\" : \"new\" }\n" },
        { revision: "3", config: '{"reject":true}' },
      ]);
      expect(report.settings).toMatchObject({ revision: "2", config: { origin: "new" } });
      expect(report.replayStatus).toBe(202);
      expect(report.replayOperationId).toBe(report.accepted.operationId);
      expect(report.conflictingReplayStatus).toBe(409);
      expect(report.conflictingReplayCode).toBe("idempotency_conflict");
      expect(report.staleStatus).toBe(412);
      expect(report.staleCode).toBe("plugin_revision_conflict");
      expect(report.invalidStatus).toBe(400);
      expect(report.invalidUTF8Status).toBe(400);
      expect(report.tooLargeStatus).toBe(422);
      expect(report.activeRaw).toBe('{"reject":true}');
      expect(report.activeDigest).toBe(createHash("sha256").update(report.activeRaw).digest("hex"));
      expect(report.slots).toEqual(["active", "previous"]);
      expect(report.rejectedOperation).toMatchObject({ state: "failed", errorCode: "activation_failed" });
      expect(report.settingsAfterReject).toMatchObject({ revision: "3", config: { reject: true } });
      expect(report.auditedActor).toBe("static-token");
      expect(report.auditActions).toContain("plugin_settings.apply");
      expect(report.auditActions.filter((action) => action === "plugin_settings.apply")).toHaveLength(2);
      expect(report.firstConfig).toEqual({
        acceptedStatus: 202,
        state: "succeeded",
        revision: "1",
        raw: '{"initial":true}',
      });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }, 30_000);
});
