import { describe, expect, it } from "vitest";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dirname, "../..");
const protocol = resolve(root, "../pluginprotocol");

describe("plugin supervisor и admin surface boundary", () => {
  it("имеет реалный supervisor с жизненным циклом процесса", () => {
    const source = readFileSync(resolve(root, "internal/infrastructure/plugins/supervisor.go"), "utf8");
    expect(source).toContain("func NewSupervisor");
    expect(source).toContain("func (s *Supervisor) Start");
    expect(source).toContain("func (s *Supervisor) Stop");
    expect(source).toContain("func (s *Supervisor) Restart");
  });

  it("публикует типизированную admin-surface boundary", () => {
    const source = readFileSync(resolve(root, "internal/infrastructure/plugins/admin_surface.go"), "utf8");
    expect(source).toContain("type AdminSurface");
    expect(source).toContain("func ValidateAdminSurface");
    expect(source).toContain("func (s AdminSurface) Namespace");
  });

  it("берёт forms-db surface из канонического protocol repository", () => {
    const contract = resolve(protocol, "contracts/forms-db/v1/admin-surface.json");
    expect(existsSync(contract)).toBe(true);
    const parsed = JSON.parse(readFileSync(contract, "utf8"));
    expect(parsed.capability).toBe("admin.surface");
    expect(parsed.namespace).toBe("forms-db");
  });
});
