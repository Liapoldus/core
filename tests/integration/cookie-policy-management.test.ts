import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);

describe("Management API plugin cookie policy", () => {
  it("persists per-capability policy behind ETag CAS and exposes the committed revision", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-cookie-policy-api-"));
    try {
      const { stdout } = await execFileAsync("go", ["run", "./tests/fixtures/cookie-policy-api", join(directory, "gateway.db")], {
        cwd: join(import.meta.dirname, "../.."),
      });
      const result = JSON.parse(stdout) as {
        firstPutStatus: number;
        firstPutETag: string;
        stalePutStatus: number;
        getStatus: number;
        getETag: string;
        getRevision: number;
        allowedNames: string[];
        auditActions: string[];
        activationCount: number;
        staleDidNotActivate: boolean;
      };

      expect(result.firstPutStatus).toBe(200);
      expect(result.firstPutETag).toBe('"1"');
      expect(result.stalePutStatus).toBe(412);
      expect(result.getStatus).toBe(200);
      expect(result.getETag).toBe('"1"');
      expect(result.getRevision).toBe(1);
      expect(result.allowedNames).toEqual(["session"]);
      expect(result.auditActions).toEqual(["plugin.cookie_policy.replace"]);
      expect(result.activationCount).toBe(1);
      expect(result.staleDidNotActivate).toBe(true);
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);
});
