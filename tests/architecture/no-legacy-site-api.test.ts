import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

describe("legacy site API removal", () => {
  it("does not retain handlers or contract fields for the removed site registry", async () => {
    const [adapter, management, contracts, models] = await Promise.all([
      readFile(join(coreRoot, "internal/presentation/api/adapter.go"), "utf8"),
      readFile(join(coreRoot, "assets/contracts/management-fields.yaml"), "utf8"),
      readFile(join(coreRoot, "contracts/v1/management.openapi.yaml"), "utf8"),
      readFile(join(coreRoot, "internal/domain/models/release_revision_conflict.go"), "utf8").catch(() => ""),
    ]);

    expect(adapter).not.toMatch(/handleSite(?:Publish|Rollback)|PublishSite|RollbackSite|publishMu|idempotencyRecord/);
    expect(management).not.toMatch(/^\s+(?:siteSourceImmutable|releaseInvalid|publishInProgress|noPreviousRelease|releaseRevisionConflict|source):/m);
    expect(contracts).not.toMatch(/\/api\/sites|sitePublish|siteRollback/);
    expect(models).toBe("");
  });
});
