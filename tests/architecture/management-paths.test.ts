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
      "adminSurfaces:", "adminPages:", "adminActions:", "adminQueryAction:",
    ];

    for (const field of contractFields) expect(contract).toContain(field);
    for (const path of paths) {
      expect(source, path).not.toContain(`"${path}"`);
    }
  });

  it("keeps plugin rollback on a contract path and omits every v1-forbidden lifecycle verb", async () => {
    const source = await readGoPackageSources(root, "internal/presentation/api");
    const contract = await readFile(join(root, "assets/contracts/management-fields.yaml"), "utf8");

    expect(contract).toContain("pluginRollbackSuffix: rollback");
    expect(contract).toContain("pluginSettingsRollback: plugin-settings-rollback");
    expect(source, "rollback path must come from the contract asset").not.toContain(`"/rollback"`);

    for (const verb of ["/restart", "/install", "/uninstall", "/supervise", "/scale"]) {
      expect(contract, verb).not.toContain(verb);
      expect(source, verb).not.toContain(verb);
    }
  });
});
