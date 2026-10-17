import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { readGoPackageSources } from "../support/presentation-source";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

/**
 * The published OpenAPI document is the normative description of the v1
 * Management API. It must declare exactly the surface the router actually
 * dispatches: an advertised-but-absent operation is a false promise to
 * integrators, and an implemented-but-unadvertised operation is undocumented
 * surface. This gate exists because the two drifted apart unnoticed.
 */
const implementedOperations = [
  "GET /healthz",
  "GET /api/status",
  "GET /api/v2/settings",
  "PUT /api/v2/settings",
  "GET /api/services",
  "GET /api/services/{serviceId}",
  "GET /api/services/{serviceId}/secrets",
  "POST /api/services/{serviceId}/secrets",
  "DELETE /api/services/{serviceId}/secrets/{reference}",
  "GET /api/services/{serviceId}/manifest",
  "GET /api/services/{serviceId}/schema",
  "GET /api/services/{serviceId}/settings",
  "GET /api/services/{serviceId}/status",
  "PUT /api/services/{serviceId}/settings",
  "POST /api/services/{serviceId}/rollback",
  "GET /api/service-links",
  "POST /api/service-links",
  "GET /api/service-links/{callerInstanceId}/{targetInstanceId}",
  "PUT /api/service-links/{callerInstanceId}/{targetInstanceId}",
  "DELETE /api/service-links/{callerInstanceId}/{targetInstanceId}",
  "GET /api/services/admin-surfaces",
  "POST /api/services/{serviceId}/admin/pages/{page}/query",
  "POST /api/services/{serviceId}/admin/pages/{page}/actions/{action}",
  "POST /api/services/{serviceId}/replicas/register",
  "GET /api/operations/{operationId}",
  "POST /api/operations/resolve",
  "GET /api/audit",
  "GET /api/access/service-keys",
  "POST /api/access/service-keys",
  "POST /api/config-bundles/apply",
  "POST /api/config-bundles/plan",
];

/** Extracts `METHOD /path` pairs from the two-space-indented OpenAPI path map. */
function declaredOperations(yaml: string): string[] {
  const operations: string[] = [];
  let inPaths = false;
  let currentPath: string | null = null;
  for (const line of yaml.split("\n")) {
    if (/^paths:\s*$/.test(line)) {
      inPaths = true;
      continue;
    }
    if (inPaths && /^\S/.test(line)) break;
    if (!inPaths) continue;
    const pathMatch = /^ {2}(\/[^:]*):\s*$/.exec(line);
    if (pathMatch) {
      currentPath = pathMatch[1];
      continue;
    }
    const methodMatch = /^ {4}(get|put|post|delete|patch):(\s|$)/.exec(line);
    if (methodMatch && currentPath) {
      operations.push(`${methodMatch[1].toUpperCase()} ${currentPath}`);
    }
  }
  return operations.sort();
}

describe("published Management OpenAPI surface", () => {
  it("declares exactly the operations the router dispatches", async () => {
    const yaml = await readFile(join(root, "contracts/v1/management.openapi.yaml"), "utf8");
    expect(declaredOperations(yaml)).toEqual([...implementedOperations].sort());
  });

  it("advertises no v1-forbidden registration or lifecycle operation", async () => {
    const yaml = await readFile(join(root, "contracts/v1/management.openapi.yaml"), "utf8");
    const forbidden = [
      "POST /api/services",
      "PUT /api/services/{serviceId}",
      "DELETE /api/services/{serviceId}",
    ];
    for (const operation of forbidden) {
      expect(declaredOperations(yaml), operation).not.toContain(operation);
    }
    for (const operation of declaredOperations(yaml)) {
      expect(operation).not.toMatch(/\/(?:restart|install|uninstall|supervise|scale)(?:\/|$)/);
    }
    expect(yaml).not.toContain("release reference");
  });

  it("keeps every component $ref resolvable and every component used", async () => {
    const yaml = await readFile(join(root, "contracts/v1/management.openapi.yaml"), "utf8");
    const referenced = new Set<string>();
    for (const [, kind, name] of yaml.matchAll(/\$ref:\s*'#\/components\/(\w+)\/(\w+)'/g)) {
      referenced.add(`${kind}/${name}`);
    }
    expect(referenced.size).toBeGreaterThan(0);

    const components = /^components:\n([\s\S]+)$/m.exec(yaml);
    expect(components, "components block").not.toBeNull();
    const defined = new Set<string>();
    let kind: string | null = null;
    for (const line of components![1].split("\n")) {
      const section = /^ {2}(\w+):\s*$/.exec(line);
      if (section) {
        kind = section[1];
        continue;
      }
      const member = /^ {4}(\w+):\s*$/.exec(line);
      if (member && kind) defined.add(`${kind}/${member[1]}`);
    }
    expect(defined.size).toBeGreaterThan(0);

    for (const ref of referenced) {
      expect(defined, `dangling $ref ${ref}`).toContain(ref);
    }
    // `securitySchemes` is applied by the document-level `security` list rather
    // than by an explicit $ref, so only data components must be referenced.
    for (const component of defined) {
      if (component.startsWith("securitySchemes/")) continue;
      if (component === "responses/InternalError" || component === "responses/ServiceCallFailed") continue;
      expect(referenced, `unused component ${component}`).toContain(component);
    }
  });

  it("routes service Admin Surface listing at the dispatched path", async () => {
    const [yaml, fields, source] = await Promise.all([
      readFile(join(root, "contracts/v1/management.openapi.yaml"), "utf8"),
      readFile(join(root, "assets/contracts/management-fields.yaml"), "utf8"),
      readGoPackageSources(root, "internal/presentation/api"),
    ]);
    expect(fields).toContain("adminSurfaces: /api/services/admin-surfaces");
    expect(yaml).toContain("  /api/services/admin-surfaces:");
    expect(yaml).not.toContain("/admin/surface");
    expect(source).toContain("AdminSurfaceList");
  });

  it("publishes the Core-owned settings CAS boundary", async () => {
    const yaml = await readFile(join(root, "contracts/v1/management.openapi.yaml"), "utf8");
    const path = yaml.split("  /api/v2/settings:")[1]?.split("\n  /")[0] ?? "";

    expect(path).toContain("operationId: getCoreSettings");
    expect(path).toContain("operationId: updateCoreSettings");
    expect(path).toContain("IfMatch");
    expect(path).toContain("CoreSettingsSnapshot");
    expect(path).toContain("CoreSettingsInput");
    expect(path).toContain("PreconditionFailed");
  });
});
