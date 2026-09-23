import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = dirname(dirname(dirname(fileURLToPath(import.meta.url))));
const contract = (name: string) => join(root, "contracts", "v1", name);

describe("Gateway v1 migration contracts", () => {
  it("declares both local and fixed remote plugin endpoints with mandatory mTLS", async () => {
    const schema = JSON.parse(await readFile(contract("gateway.schema.json"), "utf8"));
    const plugin = schema.$defs.plugin;
    expect(plugin.properties.mode.enum).toEqual(["local", "remote"]);
    expect(plugin.properties.endpoint.properties.tls.required).toEqual(
      expect.arrayContaining(["serverName", "ca", "clientCertificate", "clientKey"]),
    );
    const remoteCondition = plugin.allOf.find((condition: { if?: { required?: string[]; properties?: { mode?: { const?: string } } }; then?: { required?: string[] } }) =>
      condition.if?.properties?.mode?.const === "remote",
    );
    expect(remoteCondition?.if?.required).toContain("mode");
    expect(remoteCondition?.then?.required).toContain("endpoint");
  });

  it("allows incoming cookies only as an explicit capability context allow-list", async () => {
    const schema = JSON.parse(await readFile(contract("gateway.schema.json"), "utf8"));
    const cookies = schema.$defs.pluginContext.properties.cookies;
    expect(cookies.type).toBe("array");
    expect(cookies.default).toEqual([]);
    expect(cookies.uniqueItems).toBe(true);
  });

  it("uses domain-addressed Caddy-managed TLS operations rather than issuer routes", async () => {
    const openapi = await readFile(contract("management.openapi.yaml"), "utf8");
    expect(openapi).toContain("/api/tls/{domain}/renew:");
    expect(openapi).toContain("/api/tls/{domain}/revoke:");
    expect(openapi).not.toContain("/api/tls/{issuer}/");
    expect(openapi).toContain("required: [idempotencyKey, serial, reason]");
  });

  it("publishes one bounded tar.gz as multipart with revision metadata", async () => {
    const openapi = await readFile(contract("management.openapi.yaml"), "utf8");
    expect(openapi).toContain("multipart/form-data:");
    expect(openapi).toContain("PublishMultipartRequest");
    expect(openapi).toContain("expectedCurrentRevision");
    expect(openapi).toContain("Exactly one gzip-compressed tar archive.");
  });
});
