import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("group Management API reads", () => {
  it("lists and reads SQLite groups with revision pointers and safe not-found responses", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-group-management-"));
    try {
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/group-management-api", join(directory, "gateway.db")],
        { cwd: coreRoot },
      );

      expect(JSON.parse(result.stdout)).toEqual({
        listStatus: 200,
        listRequestID: true,
        groups: [
          { id: "application-a", kind: "application", active: true, currentRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", previousRevision: null, state: "ready" },
          { id: "system", kind: "system", active: true, currentRevision: null, previousRevision: null, state: "empty" },
        ],
        getStatus: 200,
        getRequestID: true,
        getGroup: { id: "application-a", kind: "application", active: true, currentRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", previousRevision: null, state: "ready" },
        missingStatus: 404,
        missingProblem: { code: "group_not_found", status: 404, requestId: true, noStoreDetail: true },
        unauthorizedStatus: 401,
        releaseListStatus: 200,
        releaseListRequestID: true,
        releases: [
          { id: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", groupId: "application-a", caddyfileDigest: "a1ec39a1a96fd53b4e5b58e0734941379bb4e7c19130f04d2c577262b6f8d95c", artifactDigest: null, actor: "", createdAt: expect.any(String) },
        ],
        releasePathsHidden: true,
        releaseDetailStatus: 200,
        releaseDetailSafe: true,
        publishStatus: expect.any(Number),
        publish: expect.any(Object),
        publishRetryStatus: expect.any(Number),
        publishRetry: expect.any(Object),
        operationAfterReopenStatus: expect.any(Number),
        operationAfterReopen: expect.any(Object),
        groupAfterReopenStatus: expect.any(Number),
        currentAfterReopen: expect.any(String),
        idempotencyConflictStatus: expect.any(Number),
        idempotencyConflictCode: expect.any(String),
        invalidCaddyfileStatus: expect.any(Number),
        invalidCaddyfileCode: expect.any(String),
        pointersAfterInvalid: expect.any(String),
        staleRevisionStatus: expect.any(Number),
        staleRevisionCode: expect.any(String),
        pointersAfterStale: expect.any(String),
        unsafeArtifactStatus: expect.any(Number),
        unsafeArtifactCode: expect.any(String),
        safeArtifactStatus: expect.any(Number),
        safeArtifactDetailStatus: expect.any(Number),
        safeArtifactDigest: expect.any(String),
        safeFrontendId: expect.any(String),
        safeFrontendDigest: expect.any(String),
        safeFrontendFiles: expect.any(Number),
        rollbackStatus: expect.any(Number),
        rollbackCode: expect.any(String),
        rollbackRetryStatus: expect.any(Number),
        rollbackRetryOperationId: expect.any(String),
        rollbackStaleStatus: expect.any(Number),
        rollbackStaleCode: expect.any(String),
        currentBeforeRollback: expect.any(String),
        previousBeforeRollback: expect.any(String),
        currentAfterRollback: expect.any(String),
        previousAfterRollback: expect.any(String),
      });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }, 15_000);

  it("accepts a valid multipart group release and returns its durable operation reference", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-group-publish-"));
    try {
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/group-management-api", join(directory, "gateway.db")],
        { cwd: coreRoot },
      );
      const report = JSON.parse(result.stdout) as {
        publishStatus: number;
        publish: { operationId?: string; state?: string; requestId?: string };
      };

      expect(report.publishStatus).toBe(202);
      expect(report.publish.operationId).toBeTruthy();
      expect(report.publish.state).toMatch(/^(pending|running)$/);
      expect(report.publish.requestId).toBeTruthy();
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });

  it("rejects stale revisions and invalid Caddyfiles without changing active pointers", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-group-publish-validation-"));
    try {
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/group-management-api", join(directory, "gateway.db")],
        { cwd: coreRoot },
      );
      const report = JSON.parse(result.stdout) as {
        invalidCaddyfileStatus: number;
        invalidCaddyfileCode: string;
        pointersAfterInvalid: string | null;
        staleRevisionStatus: number;
        staleRevisionCode: string;
        pointersAfterStale: string | null;
      };

      expect(report.invalidCaddyfileStatus).toBe(422);
      expect(report.invalidCaddyfileCode).toBe("caddy_adapt_failed");
      expect(report.pointersAfterInvalid).toBe("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa");
      expect(report.staleRevisionStatus).toBe(409);
      expect(report.staleRevisionCode).toBe("group_revision_conflict");
      expect(report.pointersAfterStale).toBe("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa");
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });

  it("deduplicates identical publish retries and rejects key reuse with different content", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-group-publish-idempotency-"));
    try {
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/group-management-api", join(directory, "gateway.db")],
        { cwd: coreRoot },
      );
      const report = JSON.parse(result.stdout) as {
        publish: { operationId?: string };
        publishRetryStatus: number;
        publishRetry: { operationId?: string };
        idempotencyConflictStatus: number;
        idempotencyConflictCode: string;
      };

      expect(report.publishRetryStatus).toBe(202);
      expect(report.publishRetry.operationId).toBe(report.publish.operationId);
      expect(report.idempotencyConflictStatus).toBe(409);
      expect(report.idempotencyConflictCode).toBe("idempotency_conflict");
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });

  it("persists the accepted release operation and revision metadata across SQLite reopen", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-group-publish-reopen-"));
    try {
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/group-management-api", join(directory, "gateway.db")],
        { cwd: coreRoot },
      );
      const report = JSON.parse(result.stdout) as {
        publishStatus: number;
        operationAfterReopenStatus: number;
        operationAfterReopen: { id?: string; kind?: string; state?: string };
        groupAfterReopenStatus: number;
        currentAfterReopen: string | null;
      };

      expect(report.publishStatus).toBe(202);
      expect(report.operationAfterReopenStatus).toBe(200);
      expect(report.operationAfterReopen.id).toBeTruthy();
      expect(report.operationAfterReopen.kind).toBe("group.publish");
      expect(report.operationAfterReopen.state).toMatch(/^(pending|running|succeeded|failed)$/);
      expect(report.groupAfterReopenStatus).toBe(200);
      expect(report.currentAfterReopen).not.toBeNull();
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
