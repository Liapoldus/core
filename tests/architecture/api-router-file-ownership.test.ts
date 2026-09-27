import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const apiRoot = join(root, "internal", "presentation", "api");
const routerMethods = [
  "handle",
  "dispatchReadinessAndAdmin",
  "dispatchAccessAndGroups",
  "dispatchGroupReleases",
  "dispatchPluginCollections",
  "dispatchAudit",
  "dispatchPluginActions",
  "dispatchOperations",
];
const requestBoundaryMethods = [
  "handleHealthz",
  "authorizeManagementRequest",
  "handleReadiness",
  "handleAuditList",
  "handleOperationGet",
  "authenticate",
];

describe("API router ownership", () => {
  it("keeps request routing and path/method dispatch in api/router.go", async () => {
    const source = await readFile(join(apiRoot, "router.go"), "utf8").catch(() => "");

    for (const method of routerMethods) {
      expect(source).toMatch(new RegExp(`^func \\(server \\*Server\\) ${method}\\(`, "m"));
    }
  });

  it("removes router methods from the legacy adapter file", async () => {
    const source = await readFile(join(apiRoot, "adapter.go"), "utf8").catch(() => "");

    for (const method of routerMethods) {
      expect(source).not.toMatch(new RegExp(`^func \\(server \\*Server\\) ${method}\\(`, "m"));
    }
  });

  it("owns remaining request-boundary handlers in router.go and retires adapter.go", async () => {
    const router = await readFile(join(apiRoot, "router.go"), "utf8").catch(() => "");
    const adapter = await readFile(join(apiRoot, "adapter.go"), "utf8").catch(() => "");

    for (const method of requestBoundaryMethods) {
      expect(router).toMatch(new RegExp(`^func (?:\\(server \\*Server\\) )?${method}\\(`, "m"));
      expect(adapter).not.toMatch(new RegExp(`^func (?:\\(server \\*Server\\) )?${method}\\(`, "m"));
    }
    expect(router).toMatch(/^func randomID\(/m);
    expect(adapter).not.toMatch(/^func randomID\(/m);
    expect(adapter).toBe("");
  });
});
