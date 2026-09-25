import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

describe("Management API route contract", () => {
  it("keeps endpoint paths in contract assets, not Go adapters", async () => {
    const source = await readFile(join(root, "internal/presentation/api/adapter.go"), "utf8");
    const contract = await readFile(join(root, "assets/contracts/management-fields.yaml"), "utf8");
    const paths = [
      "/healthz",
      "/api/status",
      "/api/plugins",
      "/api/operations",
      "/api/audit",
      "/api/plugins/admin-surfaces",
      "/api/plugins/",
      "/api/groups",
      "/api/groups/",
      "/admin/pages/",
    ];
    const contractFields = [
      "healthz:", "status:", "plugins:", "operations:", "audit:",
      "adminSurfaces:", "adminPages:", "groups:", "groupByID:",
    ];

    for (const field of contractFields) expect(contract).toContain(field);
    for (const path of paths) {
      expect(source, path).not.toContain(`"${path}"`);
    }
  });
});
