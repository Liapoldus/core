import { describe, expect, it } from "vitest";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dirname, "../..");
const plugins = resolve(root, "../plugins");

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

  it("берёт forms-db surface из репозитория самого плагина", () => {
    const contract = resolve(plugins, "forms-db/contracts/v1/admin-surface.json");
    expect(existsSync(contract)).toBe(true);
    const parsed = JSON.parse(readFileSync(contract, "utf8"));
    expect(parsed.plugin).toBe("forms-db");
    expect(parsed.requiredCapabilities).toContain("admin.surface.get");
  });
});
