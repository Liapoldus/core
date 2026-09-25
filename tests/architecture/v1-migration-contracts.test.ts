import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = dirname(dirname(dirname(fileURLToPath(import.meta.url))));
const contract = (name: string) => join(root, "contracts", "v1", name);

describe("Gateway v1 Caddy control-plane contracts", () => {
  it("defines only the minimal bootstrap surface", async () => {
    const schema = JSON.parse(await readFile(contract("gateway.schema.json"), "utf8"));
    expect(schema.required).toEqual(["state", "artifacts", "management", "caddy"]);
    expect(Object.keys(schema.properties).sort()).toEqual(["artifacts", "caddy", "management", "state"]);
    expect(schema.additionalProperties).toBe(false);
    expect(schema.$defs.caddy.properties.variant.enum).toEqual(["embedded", "external"]);
    expect(schema.$defs).not.toHaveProperty("site");
    expect(schema.$defs).not.toHaveProperty("listener");
  });

  it("exposes group revisions and complete Admin API pass-through without a site publish API", async () => {
    const openapi = await readFile(contract("management.openapi.yaml"), "utf8");
    expect(openapi).toContain("/api/groups/{groupId}/releases:");
    expect(openapi).toContain("expectedCurrentRevision");
    expect(openapi).toContain("Caddy Admin");
    expect(openapi).not.toContain("/api/sites/{slug}/publish:");
  });

  it("defines direct Caddy-to-plugin dispatch and every v1 Stream mode", async () => {
    const runtime = JSON.parse(await readFile(contract("http-runtime.json"), "utf8"));
    const plugins = JSON.parse(await readFile(contract("plugin-contracts.json"), "utf8"));
    expect(runtime.pluginDataProtocol.call).toContain("directly");
    expect(plugins.capabilityInvocationModes.gatewayValidation).toContain("before activation");
    expect(plugins.streamContract.modes).toEqual(["http-stream", "websocket", "sse", "tcp", "udp"]);
    expect(plugins.streamContract.websocket).toContain("subprotocol");
  });

  it("keeps external Caddy snapshots private and GrantBroker out of the traffic proxy path", async () => {
    const security = JSON.parse(await readFile(contract("security-runtime.json"), "utf8"));
    expect(security.caddyAdmin.externalProcess).toContain("supervises");
    expect(security.plugins.dataPath).toContain("never proxies");
    expect(security.plugins.grantBroker).toContain("does not proxy");
  });

});
