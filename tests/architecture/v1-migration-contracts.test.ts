import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = dirname(dirname(dirname(fileURLToPath(import.meta.url))));
const contract = (name: string) => join(root, "contracts", "v1", name);

type SchemaNode = Record<string, unknown>;

const descriptiveKeys = new Set(["description", "x-liapoldus-invariants"]);

/**
 * Collects every machine-readable object key and string literal of a JSON Schema
 * node. Human-readable documentation fields are excluded on purpose: prose is
 * allowed to mention deferred v2 work, but the bootstrap surface itself must not
 * declare, enumerate or reference it.
 */
function structuralTokens(node: unknown, tokens: Set<string> = new Set(), key?: string): Set<string> {
  if (typeof node === "string") {
    if (key !== undefined && !descriptiveKeys.has(key)) {
      tokens.add(node);
    }
    return tokens;
  }
  if (Array.isArray(node)) {
    for (const item of node) {
      structuralTokens(item, tokens, key);
    }
    return tokens;
  }
  if (node !== null && typeof node === "object") {
    for (const [childKey, childValue] of Object.entries(node)) {
      tokens.add(childKey);
      structuralTokens(childValue, tokens, childKey);
    }
  }
  return tokens;
}

describe("Core v1 management contracts", () => {
  it("defines only the minimal bootstrap surface", async () => {
    const schema = JSON.parse(
      await readFile(join(root, "assets/contracts/core.schema.json"), "utf8"),
    ) as SchemaNode;
    const required = (schema.required ?? []) as string[];
    const properties = Object.keys((schema.properties ?? {}) as SchemaNode);
    const definitions = Object.keys((schema.$defs ?? {}) as SchemaNode);

    // The declared plugin registry is part of the minimal v1 bootstrap surface:
    // it is the only source of replica endpoints and expected identities, so a
    // Core that started without it could never deliver a generation.
    expect(required).toEqual(["state", "management", "pluginControl", "plugins"]);
    expect([...properties].sort()).toEqual(["management", "pluginControl", "plugins", "state"]);
    expect(schema.additionalProperties).toBe(false);
    expect(new Set(required), "every declared bootstrap block must be required").toEqual(
      new Set(properties),
    );
    for (const removed of ["execution", "artifacts", "pluginCatalog", "site", "listener"]) {
      expect(properties, removed).not.toContain(removed);
      expect(definitions, removed).not.toContain(removed);
    }
  });

  it("exposes no deployment-mode, provider or TUF surface in the bootstrap schema", async () => {
    const text = await readFile(join(root, "assets/contracts/core.schema.json"), "utf8");
    const schema = JSON.parse(text) as SchemaNode;
    const definitions = (schema.$defs ?? {}) as SchemaNode;
    const references = [...text.matchAll(/"#\/\$defs\/([^"]+)"/g)].map((match) => match[1]!);

    expect(schema.allOf, "conditional profile branching must be gone").toBeUndefined();
    for (const reference of references) {
      expect(Object.keys(definitions), reference).toContain(reference);
    }
    const forbidden = [
      "execution",
      "artifacts",
      "pluginCatalog",
      "supervised",
      "external",
      "profile",
      "tuf",
      "catalog",
      "install",
      "docker",
      "compose",
      "swarm",
      "kubernetes",
      "container",
      "image",
    ];
    const tokens = structuralTokens(schema);
    for (const token of forbidden) {
      expect([...tokens].filter((value) => value.toLowerCase().includes(token)), token).toEqual([]);
    }
  });

  it("exposes no Management API plugin registration or desired-state replacement", async () => {
    const openapi = await readFile(contract("management.openapi.yaml"), "utf8");

    // Declared topology is operator-owned bootstrap configuration, so the
    // Management API must neither create an instance nor rewrite its replicas.
    for (const removedShape of [
      "    PluginCreate:",
      "    PluginDesiredState:",
      "    PluginReplicaInput:",
      "      operationId: createPlugin",
      "      operationId: updatePlugin",
      "      operationId: deletePlugin",
      "    post:\n      operationId: createPlugin",
    ]) {
      expect(openapi, removedShape).not.toContain(removedShape);
    }
    expect(openapi).toContain("    post:\n      operationId: createServiceKey");
    // The read paths that survive must still be instance-scoped and redacted.
    expect(openapi).toContain("      operationId: getPlugin\n");
    expect(openapi).toContain("      operationId: listPlugins");
    expect(openapi).toContain("        id: { type: string }\n        state: { type: string }");
  });

  it("keeps the published bootstrap vector aligned with the bootstrap surface", async () => {
    const schema = JSON.parse(
      await readFile(join(root, "assets/contracts/core.schema.json"), "utf8"),
    ) as SchemaNode;
    const properties = new Set(Object.keys((schema.properties ?? {}) as SchemaNode));
    const vectors = (JSON.parse(
      await readFile(contract("golden-vectors.json"), "utf8"),
    ) as { vectors: Array<{ id: string; input: { keys?: string[] }; expected: { valid?: boolean } }> })
      .vectors.filter((vector) => vector.input.keys !== undefined && vector.expected.valid === false);

    expect(vectors, "bootstrap rejection vector").toHaveLength(1);
    const unknown = (vectors[0]!.input.keys ?? []).filter((key) => !properties.has(key));
    expect(unknown, "only a product-runtime key may be unknown to the v1 bootstrap schema").toEqual([
      "site",
    ]);
  });

  it("exposes no cookie policy or interaction policy surface in v1", async () => {
    const openapi = await readFile(contract("management.openapi.yaml"), "utf8");

    // These surfaces are not dispatched by the v1 router, so the contract must
    // not advertise them and must not keep their now-unreferenced envelopes.
    for (const notInV1 of [
      "  /api/plugins/{pluginId}/cookie-policies/{capability}:",
      "  /api/plugins/{pluginId}/interactions:",
      "    IfMatchOptional:",
      "    CookiePreconditionRequired:",
      "    InvalidCookiePolicy:",
      "    CookiePolicy:",
      "    CookiePolicyInput:",
      "    InteractionPolicy:",
      "    InteractionPolicyInput:",
      "    InteractionEdge:",
      "cookie_policy_revision_conflict",
      "interaction_generation_conflict",
      "interaction_policy_invalid",
    ]) {
      expect(openapi, notInV1).not.toContain(notInV1);
    }

    // Optional `If-Match` has exactly one meaning left: none, since every
    // mutating v1 operation requires a strong ETag.
    expect(openapi).toContain("    IfMatch:");
    expect(openapi).toContain("      required: true");
  });

  it("does not expose product runtime administration through the Core API", async () => {
    const openapi = await readFile(contract("management.openapi.yaml"), "utf8");
    expect(openapi).not.toContain("/api/groups");
    expect(openapi).not.toContain("/api/tls");
    expect(openapi).not.toContain("/api/sites/{slug}/publish:");
  });

  it("exposes no plugin process lifecycle or release installation in the Core contract", async () => {
    const openapi = await readFile(contract("management.openapi.yaml"), "utf8");
    for (const v2Surface of [
      "/api/plugins/{pluginId}/install:",
      "/api/plugins/{pluginId}/restart:",
      "installPluginRelease",
      "restartPlugin",
      "PluginReleaseSelection",
    ]) {
      expect(openapi).not.toContain(v2Surface);
    }
    for (const v2Code of [
      "plugin_catalog_untrusted",
      "plugin_release_incompatible",
      "plugin_profile_operation_forbidden",
    ]) {
      expect(openapi).not.toContain(v2Code);
    }
  });

  it("publishes the rollback route the router actually implements", async () => {
    const openapi = await readFile(contract("management.openapi.yaml"), "utf8");
    expect(openapi).toContain("/api/plugins/{pluginId}/rollback:");
    expect(openapi).toContain("rollbackPluginSettings");
  });

  it("keeps GrantBroker out of the traffic proxy path", async () => {
    const security = JSON.parse(await readFile(contract("security-runtime.json"), "utf8"));
    expect(security.plugins.dataPath).toContain("never proxies");
    expect(security.plugins.grantBroker).toContain("does not proxy");
    expect(security.plugins.managementApiInUserRequestPath).toBe(false);
  });
});
