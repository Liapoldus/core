import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { readGoPackageSources } from "../support/presentation-source";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

describe("Management API route contract", () => {
  it("keeps endpoint paths in contract assets, not Go adapters", async () => {
    const source = await readGoPackageSources(root, "internal/presentation/api");
    const contract = await readFile(join(root, "assets/contracts/management-fields.yaml"), "utf8");
    const paths = [
      "/healthz",
      "/api/status",
      "/api/plugins",
      "/api/operations",
      "/api/audit",
      "/api/plugins/admin-surfaces",
      "/api/plugins/",
      "/admin/pages/",
    ];
    const contractFields = [
      "healthz:", "status:", "plugins:", "operations:", "audit:",
      "adminSurfaces:", "adminPages:",
    ];

    for (const field of contractFields) expect(contract).toContain(field);
    for (const path of paths) {
      expect(source, path).not.toContain(`"${path}"`);
    }
  });
});
