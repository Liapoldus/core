import { existsSync, readFileSync } from "node:fs";
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

  it("does not restore retired JSONL and telemetry configuration into the SQLite audit adapter", () => {
    const configurationAdapter = readFileSync(join(coreRoot, "internal/infrastructure/config/contracts.go"), "utf8");
    const auditAsset = readFileSync(join(coreRoot, "assets/contracts/audit-fields.yaml"), "utf8");
    for (const retiredShape of ["Metrics struct", "Logging struct", "Tracing struct", "Redaction []string", "Operations struct"]) {
      expect(configurationAdapter, retiredShape).not.toContain(retiredShape);
    }
    expect(auditAsset).not.toContain(".jsonl");
    expect(auditAsset).not.toContain("metrics:");
    expect(auditAsset).not.toContain("tracing:");
  });
});
