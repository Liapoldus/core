import { existsSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

describe("removed unreferenced legacy artifacts", () => {
  it("does not restore orphan domain types or filesystem-only adapters", () => {
    const removedPaths = [
      "internal/domain/models/action.go",
      "internal/domain/models/capability.go",
      "internal/domain/models/client_auth.go",
      "internal/domain/models/revision.go",
      "internal/domain/models/registry_lock_conflict.go",
      "internal/infrastructure/storage/management.go",
      "internal/infrastructure/storage/audit.go",
      "tests/fixtures/plugin-grant/main.go",
    ];

    for (const path of removedPaths) {
      expect(existsSync(join(coreRoot, path)), path).toBe(false);
    }
  });
});
