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
          { id: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", groupId: "application-a", caddyfileDigest: "687a79b127387ef55ac664491ab82b5665df6e3d9dc62e2b6062485e2da1c9c4", artifactDigest: null, actor: "", createdAt: expect.any(String) },
        ],
        releasePathsHidden: true,
        releaseDetailStatus: 200,
        releaseDetailSafe: true,
        publishStatus: expect.any(Number),
        publish: expect.any(Object),
      });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });

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
});
