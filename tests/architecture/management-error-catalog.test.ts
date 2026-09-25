import { readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

describe("management error catalog", () => {
  it("uses canonical management errors and contains no retired registry/route codes", async () => {
    const management = readFileSync(join(coreRoot, "assets/contracts/management-fields.yaml"), "utf8");
    const errors = JSON.parse(readFileSync(join(coreRoot, "assets/contracts/errors.json"), "utf8")) as {
      errors: Array<{ code: string }>;
    };
    const response = await fetch("https://api.github.com/repos/Liapoldus/liapoldus.github.io/contents/public/spec/errors.json?ref=main", {
      headers: { Accept: "application/vnd.github.raw+json" },
      signal: AbortSignal.timeout(10000),
    });
    expect(response.ok, "canonical Gateway error catalog must be reachable").toBe(true);
    const docsErrors = await response.json() as { errors: Array<{ code: string }> };
    const codes = errors.errors.map(({ code }) => code);

    expect(management).not.toMatch(/^  routeNotFound:/m);
    expect(management).not.toMatch(/^  registryUnavailable:/m);
    expect(management).not.toMatch(/^  mtlsRequired:/m);
    expect(management).toContain("management_mtls_required");
    expect(management).toContain("management_unavailable");
    expect(codes).toContain("management_mtls_required");
    expect(codes).toContain("management_unavailable");
    expect(codes).not.toContain("route_not_found");
    expect(codes).not.toContain("registry_unavailable");
    expect(docsErrors.errors).toEqual(errors.errors);
  }, 20000);
});
