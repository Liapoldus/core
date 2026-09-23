import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

describe("Management API v1", () => {
  it("exposes the documented control-plane resources", () => {
    const source = readFileSync(resolve(process.cwd(), "../internal/presentation/api/adapter.go"), "utf8");
    for (const route of ["/healthz", "/api/status", "/api/config", "/api/config/validate", "/api/operations/", "/api/audit"]) {
      expect(source).toContain(route);
    }
  });

  it("documents secret-safe and request-correlated responses", () => {
    const source = readFileSync(resolve(process.cwd(), "../internal/presentation/api/adapter.go"), "utf8");
    const contract = readFileSync(resolve(process.cwd(), "../assets/contracts/management-fields.yaml"), "utf8");
    expect(contract).toContain("requestId: X-Request-ID");
    expect(source).toContain("application/problem+json");
    expect(source).toContain("***");
  });

  it("exposes guarded configuration lifecycle endpoints", () => {
    const source = readFileSync(resolve(process.cwd(), "../internal/presentation/api/adapter.go"), "utf8");
    expect(source).toContain("/api/config/reload");
    expect(source).toContain("If-Match");
    expect(source).toContain("conflict");
  });
});
