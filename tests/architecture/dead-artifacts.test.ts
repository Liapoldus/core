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
      "internal/domain/models/actor.go",
      "internal/domain/interfaces/authorizer.go",
      "internal/application/management_service.go",
      "contracts/v1/plugin-contracts.json",
      "internal/infrastructure/storage/management.go",
      "internal/infrastructure/storage/audit.go",
      "tests/fixtures/plugin-grant/main.go",
      "internal/infrastructure/plugins/protocol.go",
      "internal/infrastructure/plugins/runtime.go",
      "internal/infrastructure/plugins/supervisor.go",
      "internal/infrastructure/plugins/local_launch.go",
      "internal/infrastructure/plugins/manifest.go",
      "internal/infrastructure/plugins/manifest_modes.go",
      "internal/infrastructure/plugins/inventory_manifest.go",
      "internal/infrastructure/plugins/grant_broker.go",
      "assets/contracts/plugin-runtime.json",
      "assets/contracts/local-launch.schema.json",
    ];

    for (const path of removedPaths) {
      expect(existsSync(join(coreRoot, path)), path).toBe(false);
    }
    expect(existsSync(join(coreRoot, "tests/fixtures/plugin-grant")), "empty plugin-grant fixture directory").toBe(false);
    for (const fixture of [
      "http-stream-plugin",
      "invalid-manifest-plugin",
      "plugin-grpc",
      "plugin-manifest-check",
      "plugin-manifest-mode-gate",
      "plugin-process-launcher",
      "plugin-runtime-config-apply",
      "serve-plugin-child",
    ]) {
      expect(existsSync(join(coreRoot, "tests/fixtures", fixture)), fixture).toBe(false);
    }
  });

  it("does not restore v2 Caddy runtime, local launch or binary release fixtures", () => {
    const v2Fixtures = [
      "caddy-plugin",
      "caddy-runtime",
      "caddy-runtime-reload",
      "caddy-http-stream",
      "caddy-l4-plugin",
      "caddy-cookie-policy",
      "caddy-cookie-policy-stream",
      "caddy-module-probe",
      "external-caddy",
      "external-caddy-admin-proxy",
      "external-caddy-custom",
      "external-caddy-plugin-host",
      "serve-local-plugin-products",
      "serve-plugin-composition",
      "group-release-store",
      "group-release-recovery",
      "group-release-reservation-race",
      "group-release-process-state",
      "group-release-plugin-mode-preflight",
    ];

    for (const fixture of v2Fixtures) {
      expect(existsSync(join(coreRoot, "tests/fixtures", fixture)), fixture).toBe(false);
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
