import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("durable Management API operations", () => {
  it("survives SQLite close and reopen and returns 404 for an unknown operation", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-operation-store-"));
    try {
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/operation-persistence", join(directory, "core.db")],
        { cwd: coreRoot },
      );
      const report = JSON.parse(result.stdout) as {
        createStatus: number;
        operationId: string;
        readStatus: number;
        operation: { id?: string; kind?: string; state?: string; createdAt?: string; requestId?: string };
        unknownStatus: number;
        unknownCode: string;
        resolveStatus: number;
        resolvedOperations: Array<{ id: string; state: string; resourceId: string }>;
        resolvedKeyAbsent: boolean;
        resultSecretAbsent: boolean;
        storedPayloadsNull: boolean;
        appliedRevision: string;
        appliedRaw: string;
      };

      expect(report.createStatus).toBe(202);
      expect(report.operationId).toBeTruthy();
      expect(report.readStatus).toBe(200);
      expect(report.operation).toMatchObject({
        id: report.operationId,
        kind: "service-settings-rollback",
        state: "succeeded",
        createdAt: expect.stringMatching(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}/),
        requestId: expect.any(String),
      });
      expect(report.appliedRevision).toBe("1");
      expect(report.appliedRaw).toBe('{"generation":1}');
      expect(report.operation).not.toHaveProperty("actor");
      expect(report.operation).not.toHaveProperty("resource");
      expect(report.operation).not.toHaveProperty("result");
      expect(report.operation).not.toHaveProperty("problem");
      expect(report.unknownStatus).toBe(404);
      expect(report.unknownCode).toBeTruthy();
      expect(report.resolveStatus).toBe(200);
      expect(report.resolvedOperations).toEqual([
        { id: report.operationId, state: "succeeded", resourceId: "forms" },
      ]);
      expect(report.resolvedKeyAbsent).toBe(true);
      expect(report.resultSecretAbsent).toBe(true);
      expect(report.storedPayloadsNull).toBe(true);
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
