import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const root = join(process.cwd(), "..");

describe("Management API route contract", () => {
  it("keeps endpoint paths in contract assets, not Go adapters", async () => {
    const source = await readFile(join(root, "internal/presentation/api/adapter.go"), "utf8");
    const paths = [
      "/healthz",
      "/api/status",
      "/api/listeners",
      "/api/upstreams",
      "/api/sites",
      "/api/plugins",
      "/api/operations",
      "/api/config",
      "/api/config/validate",
      "/api/config/reload",
      "/api/reload",
      "/api/tls/",
      "/api/audit",
      "/metrics",
      "/api/plugins/admin-surfaces",
      "/api/plugins/",
      "/admin/pages/",
    ];

    for (const path of paths) {
      expect(source, path).not.toContain(`"${path}"`);
    }
  });
});
