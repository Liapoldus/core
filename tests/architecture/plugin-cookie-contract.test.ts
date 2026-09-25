import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
const cookieActionContract = "https://raw.githubusercontent.com/Liapoldus/pluginprotocol/main/contracts/http/v1/response-action.schema.json#/$defs/cookieAction";

describe("plugin cookie contract ownership", () => {
  it("references the protocol response schema instead of redefining cookie strings", async () => {
    const contract = JSON.parse(await readFile(join(coreRoot, "contracts/v1/plugin-contracts.json"), "utf8"));
    expect(contract.httpCapabilityEnvelope.response.properties.cookies.items.$ref).toBe(cookieActionContract);
    expect(contract.wafDecision.oneOf[1].properties.response.properties.cookies.items.$ref).toBe(cookieActionContract);
    expect(JSON.stringify(contract)).not.toMatch(/"cookies"\s*:\s*\{[^}]*"type"\s*:\s*"string"/);
  });
});
