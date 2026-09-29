import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { readGoPackageSources } from "../support/presentation-source";

const root = dirname(dirname(dirname(fileURLToPath(import.meta.url))));
const contractPath = (name: string) => join(root, "assets", "contracts", name);

describe("Management API v1", () => {
  it("exposes the documented control-plane resources", () => {
    const contract = readFileSync(contractPath("management-fields.yaml"), "utf8");
    for (const route of ["/healthz", "/api/status", "/api/plugins", "/api/operations", "/api/audit"]) {
      expect(contract).toContain(route);
    }
    for (const retired of ["/api/config", "/api/sites", "/api/listeners", "/api/upstreams", "/metrics"]) {
      expect(contract).not.toContain(retired);
    }
  });

  it("documents secret-safe and request-correlated responses", () => {
    const contract = readFileSync(contractPath("management-fields.yaml"), "utf8");
    const errors = readFileSync(contractPath("errors.json"), "utf8");
    expect(contract).toContain("requestId: X-Request-ID");
    expect(errors).toContain("application/problem+json");
    expect(errors).toContain("details contain no secret values");
  });

  it("does not expose the retired Core config-DSL API", async () => {
    const source = await readGoPackageSources(resolve(root), "internal/presentation/api");
    expect(source).not.toContain("case path == server.Management.Paths.Config");
    expect(source).not.toContain("Paths.ConfigValidate &&");
    expect(source).not.toContain("Paths.Reload ||");
    expect(source).not.toContain("case strings.HasPrefix(path, server.Management.Paths.Sites");
  });
});
