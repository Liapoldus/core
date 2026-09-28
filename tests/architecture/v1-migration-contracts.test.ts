import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = dirname(dirname(dirname(fileURLToPath(import.meta.url))));
const contract = (name: string) => join(root, "contracts", "v1", name);

describe("Gateway v1 management contracts", () => {
  it("defines only the minimal bootstrap surface", async () => {
    const schema = JSON.parse(await readFile(join(root, "assets/contracts/gateway.schema.json"), "utf8"));
    expect(schema.required).toEqual(["state", "artifacts", "execution", "management"]);
    expect(Object.keys(schema.properties).sort()).toEqual(["artifacts", "execution", "management", "pluginCatalog", "state"]);
    expect(schema.additionalProperties).toBe(false);
    expect(schema.$defs).not.toHaveProperty("site");
    expect(schema.$defs).not.toHaveProperty("listener");
  });

  it("does not expose product runtime administration through the Core API", async () => {
    const openapi = await readFile(contract("management.openapi.yaml"), "utf8");
    expect(openapi).not.toContain("/api/groups");
    expect(openapi).not.toContain("/api/tls");
    expect(openapi).not.toContain("/api/sites/{slug}/publish:");
  });

  it("keeps GrantBroker out of the traffic proxy path", async () => {
    const security = JSON.parse(await readFile(contract("security-runtime.json"), "utf8"));
    expect(security.plugins.dataPath).toContain("never proxies");
    expect(security.plugins.grantBroker).toContain("does not proxy");
    expect(security.plugins.managementApiInUserRequestPath).toBe(false);
  });

});
