import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { createGoWorkspace } from "../support/workspace.js";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
const execFileAsync = promisify(execFile);

interface Observation {
  status: number;
  etag: string;
  code: string;
  revision: number;
  itemCount: number;
}

describe("Management plugin link policy API", () => {
  it("lists, creates, reads, replaces and deletes peer link policies with CAS and idempotency", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-links-api-"));
    const workspace = createGoWorkspace("core", "plugin-sdk");
    try {
      const { stdout } = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/plugin-links-api", join(directory, "core.db")],
        { cwd: coreRoot, env: { ...process.env, GOWORK: workspace.path, GOTOOLCHAIN: "go1.26.0" }, timeout: 120_000 },
      );
      const observed = JSON.parse(stdout) as Record<string, Observation>;
      expect(observed.listEmpty).toMatchObject({ status: 200, itemCount: 0 });
      expect(observed.createNoIdempotency).toMatchObject({ status: 400, code: "invalid_request" });
      expect(observed.createInvalidShape).toMatchObject({ status: 422, code: "plugin_link_invalid" });
      expect(observed.createOk).toMatchObject({ status: 201, etag: '"1"', revision: 1 });
      expect(observed.createReplay).toMatchObject({ status: 201, etag: '"1"', revision: 1 });
      expect(observed.createDuplicate).toMatchObject({ status: 409, code: "plugin_link_conflict" });
      expect(observed.listOne).toMatchObject({ status: 200, itemCount: 1 });
      expect(observed.getOk).toMatchObject({ status: 200, etag: '"1"', revision: 1 });
      expect(observed.getMissing).toMatchObject({ status: 404, code: "not_found" });
      expect(observed.replaceStale).toMatchObject({ status: 412, code: "plugin_revision_conflict" });
      expect(observed.replaceOk).toMatchObject({ status: 200, etag: '"2"', revision: 2 });
      expect(observed.replaceMissing).toMatchObject({ status: 404, code: "not_found" });
      expect(observed.deleteStale).toMatchObject({ status: 412, code: "plugin_revision_conflict" });
      expect(observed.deleteOk).toMatchObject({ status: 204 });
      expect(observed.deleteMissing).toMatchObject({ status: 404, code: "not_found" });
      expect(observed.listPaginationInvalid).toMatchObject({ status: 400, code: "invalid_pagination" });
    } finally {
      workspace.cleanup();
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);
});