import { describe, expect, it } from "vitest";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { missingWorkspaceRepositories } from "../support/workspace.js";

const root = resolve(import.meta.dirname, "../..");
const plugins = resolve(root, "../plugins");

// forms-db owns its own surface, so this asserts a fact about that repository
// rather than a Core copy of it. The plugin-agnostic gate has no forms-db
// checkout; the integration workflow provides it.
const missingFormsDb = missingWorkspaceRepositories("plugins/forms-db");

describe("plugin admin surface boundary", () => {
  it("не содержит process supervision, удалённого из v1", () => {
    for (const removed of [
      "internal/infrastructure/plugins/supervisor.go",
      "internal/infrastructure/plugins/runtime.go",
    ]) {
      expect(existsSync(resolve(root, removed))).toBe(false);
    }
  });

  it("публикует типизированную admin-surface boundary", () => {
    const source = readFileSync(resolve(root, "internal/infrastructure/plugins/admin_surface.go"), "utf8");
    expect(source).toContain("type AdminSurface");
    expect(source).toContain("func ValidateAdminSurface");
    expect(source).toContain("func (s AdminSurface) Namespace");
  });

  it.skipIf(missingFormsDb.length > 0)("берёт forms-db surface из репозитория самого плагина", () => {
    const contract = resolve(plugins, "forms-db/contracts/v1/admin-surface.json");
    expect(existsSync(contract)).toBe(true);
    const parsed = JSON.parse(readFileSync(contract, "utf8"));
    expect(parsed.plugin).toBe("forms-db");
    expect(parsed.requiredCapabilities).toEqual(expect.arrayContaining(["forms.list", "forms.delete"]));
    expect(parsed.requiredCapabilities).not.toContain("admin.surface.get");
  });
});
