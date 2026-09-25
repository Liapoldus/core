import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

describe("Management API v1", () => {
  it("exposes the documented control-plane resources", () => {
    const contract = readFileSync(resolve(process.cwd(), "../assets/contracts/management-fields.yaml"), "utf8");
    for (const route of ["/healthz", "/api/status", "/api/groups", "/api/operations", "/api/audit"]) {
      expect(contract).toContain(route);
    }
    for (const retired of ["/api/config", "/api/sites", "/api/listeners", "/api/upstreams", "/metrics"]) {
      expect(contract).not.toContain(retired);
    }
  });

  it("documents secret-safe and request-correlated responses", () => {
    const source = readFileSync(resolve(process.cwd(), "../internal/presentation/api/adapter.go"), "utf8");
    const contract = readFileSync(resolve(process.cwd(), "../assets/contracts/management-fields.yaml"), "utf8");
    expect(contract).toContain("requestId: X-Request-ID");
    expect(source).toContain("application/problem+json");
    expect(source).toContain("***");
  });

  it("does not expose the retired Gateway config-DSL API", () => {
    const source = readFileSync(resolve(process.cwd(), "../internal/presentation/api/adapter.go"), "utf8");
    expect(source).not.toContain("case path == server.Management.Paths.Config");
    expect(source).not.toContain("Paths.ConfigValidate &&");
    expect(source).not.toContain("Paths.Reload ||");
    expect(source).not.toContain("case strings.HasPrefix(path, server.Management.Paths.Sites");
  });
});
