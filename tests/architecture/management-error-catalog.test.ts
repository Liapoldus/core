import { readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { loadCanonicalDocsFile } from "../support/canonical-contracts";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

describe("management error catalog", () => {
  it("uses canonical management errors and contains no retired transport or registry codes", async () => {
    const management = readFileSync(join(coreRoot, "assets/contracts/management-fields.yaml"), "utf8");
    const errors = JSON.parse(readFileSync(join(coreRoot, "assets/contracts/errors.json"), "utf8")) as {
      errors: Array<{ code: string }>;
    };
    const docsErrorsBuffer = await loadCanonicalDocsFile("public/spec/errors.json");
    const docsErrors = JSON.parse(docsErrorsBuffer.toString("utf8")) as { errors: Array<{ code: string }> };
    const codes = errors.errors.map(({ code }) => code);

    expect(management).not.toMatch(/^  routeNotFound:/m);
    expect(management).not.toMatch(/^  registryUnavailable:/m);
    expect(management).not.toMatch(/^  mtlsRequired:/m);
    expect(management).not.toContain("management_mtls_required");
    expect(management).toContain("management_unavailable");
    expect(codes).toContain("management_unavailable");
    expect(codes).not.toContain("route_not_found");
    expect(codes).not.toContain("registry_unavailable");
    expect(codes).not.toContain("management_mtls_required");
    const canonicalCodes = new Set(docsErrors.errors.map(({ code }) => code));
    expect(codes.every((code) => canonicalCodes.has(code))).toBe(true);
  }, 20000);
});
