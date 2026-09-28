import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const apiRoot = join(root, "internal", "presentation", "api");
const routerMethods = [
  "handle",
  "dispatchReadiness",
  "dispatchAccess",
  "dispatchPluginCollections",
  "dispatchAudit",
  "dispatchPluginActions",
  "dispatchOperations",
];
const managementHandlers = ["Healthz", "Readiness", "AuditList", "OperationGet"];
const authorizationMethods = [
  "authorizeManagementRequest",
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

  it("keeps management endpoint bodies in handlers/management.go", async () => {
    const management = await readFile(join(apiRoot, "handlers", "management.go"), "utf8").catch(() => "");
    const router = await readFile(join(apiRoot, "router.go"), "utf8").catch(() => "");

    for (const handler of managementHandlers) {
      expect(management).toMatch(new RegExp(`^func ${handler}\\(`, "m"));
      expect(router).toMatch(new RegExp(`handlers\\.${handler}\\(`));
    }
  });

  it("keeps authorization in router, request ID generation in codec, and removes adapter.go", async () => {
    const router = await readFile(join(apiRoot, "router.go"), "utf8").catch(() => "");
    const codec = await readFile(join(apiRoot, "codec.go"), "utf8").catch(() => "");
    const adapter = await readFile(join(apiRoot, "adapter.go"), "utf8").catch(() => "");

    for (const method of authorizationMethods) {
      expect(router).toMatch(new RegExp(`^func \\(server \\*Server\\) ${method}\\(`, "m"));
      expect(adapter).not.toMatch(new RegExp(`^func \\(server \\*Server\\) ${method}\\(`, "m"));
    }
    expect(codec).toMatch(/^func randomID\(/m);
    expect(adapter).not.toMatch(/^func randomID\(/m);
    expect(adapter).toBe("");
  });
});
