import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

type Observation = {
  status: number;
  contentType: string;
  location: string;
  body: Record<string, unknown>;
};

type ApplyCall = { revision: string; config: string };

type RollbackReport = {
  accepted: Observation;
  replay: Observation;
  conflicting: Observation;
  missingIfMatch: Observation;
  missingKey: Observation;
  unknown: Observation;
  withoutPrevious: Observation;
  rejected: Observation;
  rejectedState: string;
  rejectedErrorCode: string;
  unauthorized: Observation;
  applyCalls: ApplyCall[] | null;
  activeRaw: string;
  activeDigest: string;
  auditedActor: string;
  auditActions: string[];
};

const previousExactBytes = '{\n  "origin" : "first"\n}\n';

describe("plugin settings rollback API", () => {
  it("reverts to the previous generation through an idempotent, bodyless operation", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-plugin-settings-rollback-"));
    try {
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/plugin-settings-rollback", join(directory, "core.db")],
        { cwd: coreRoot },
      );
      const report = JSON.parse(result.stdout) as RollbackReport;

      expect(report.accepted.status).toBe(202);
      expect(report.accepted.contentType).toBe("application/json");
      expect(report.accepted.body).toEqual({
        operationId: expect.any(String),
        requestId: expect.any(String),
        state: "running",
      });
      expect(report.accepted.body).not.toHaveProperty("configuration");
      expect(report.accepted.location).toBe(`/api/operations/${report.accepted.body.operationId as string}`);
      expect(report.applyCalls?.[0]).toEqual({ revision: "1", config: previousExactBytes });
      expect(report.activeRaw).toBe(previousExactBytes);
      expect(report.auditActions).toEqual(["plugin_settings.rollback", "plugin_settings.rollback"]);

      expect(report.replay.status).toBe(202);
      expect(report.replay.body.operationId).toBe(report.accepted.body.operationId);
      expect(report.replay.body.state).toBe("running");
      expect(report.applyCalls).toHaveLength(2);

      expect(report.conflicting.status).toBe(412);
      expect(report.conflicting.contentType).toBe("application/problem+json");
      expect(report.conflicting.body.code).toBe("plugin_revision_conflict");
      expect(report.applyCalls).toHaveLength(2);

      expect(report.missingIfMatch.status).toBe(400);
      expect(report.missingIfMatch.body.code).toBe("invalid_request");
      expect(report.missingKey.status).toBe(400);
      expect(report.missingKey.body.code).toBe("invalid_request");

      expect(report.unknown.status).toBe(404);
      expect(report.unknown.body.code).toBe("plugin_not_found");

      expect(report.withoutPrevious.status).toBe(412);
      expect(report.withoutPrevious.body.code).toBe("plugin_revision_conflict");

      expect(report.rejected.status).toBe(503);
      expect(report.rejected.body.code).toBe("plugin_unavailable");
      expect(report.rejectedState).toBe("failed");
      expect(report.rejectedErrorCode).toBe("activation_failed");

      expect(report.unauthorized.status).toBe(401);
      expect(report.unauthorized.body.code).toBe("management_bearer_required");
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
