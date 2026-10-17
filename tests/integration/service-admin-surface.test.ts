import { describe, expect, it } from "vitest";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { missingWorkspaceRepositories } from "../support/workspace.js";

const root = resolve(import.meta.dirname, "../..");
const services = resolve(root, "../services");

// forms-db owns its own surface, so this asserts a fact about that repository
// rather than a Core copy of it. The service-agnostic gate has no forms-db
// checkout; the integration workflow provides it.
const missingFormsDb = missingWorkspaceRepositories("services/forms-db");

describe("service admin surface boundary", () => {
  it("не содержит process supervision, удалённого из v1", () => {
    for (const removed of [
      "internal/infrastructure/services/supervisor.go",
      "internal/infrastructure/services/runtime.go",
    ]) {
      expect(existsSync(resolve(root, removed))).toBe(false);
    }
  });

  it("публикует типизированную admin-surface boundary", () => {
    const source = readFileSync(resolve(root, "internal/infrastructure/services/admin_surface.go"), "utf8");
    expect(source).toContain("type AdminSurface");
    expect(source).toContain("func ValidateAdminSurface");
    expect(source).toContain("func (s AdminSurface) Namespace");
  });

  it.skipIf(missingFormsDb.length > 0)("берёт forms-db surface из репозитория самого сервиса", () => {
    const contract = resolve(services, "forms-db/contracts/v1/admin-surface.json");
    expect(existsSync(contract)).toBe(true);
    const parsed = JSON.parse(readFileSync(contract, "utf8"));
    expect(parsed.service).toBe("forms-db");
    expect(parsed.requiredCapabilities).toEqual(expect.arrayContaining(["forms.list", "forms.delete"]));
    expect(parsed.requiredCapabilities).not.toContain("admin.surface.get");
  });
});
