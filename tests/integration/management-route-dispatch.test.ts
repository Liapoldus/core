import { execFile } from "node:child_process";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

interface Observation {
  readonly status: number;
  readonly body: { readonly code?: string } | null;
}

describe("Management API route/method dispatch characterization", () => {
  it("preserves current path precedence and method outcomes", async () => {
    const { stdout } = await execFileAsync(
      "go",
      ["run", "./tests/fixtures/management-api-characterization", "route-matrix"],
      { cwd: coreRoot },
    );
    const actual = JSON.parse(stdout) as Record<string, Observation>;
    const dispatch = Object.fromEntries(
      Object.entries(actual).map(([name, response]) => [name, {
        status: response.status,
        code: response.body?.code ?? null,
      }]),
    );

    expect(dispatch).toEqual({
      serviceListPost: { status: 404, code: "not_found" },
      serviceDetailPut: { status: 404, code: "not_found" },
      serviceAdminPatch: { status: 404, code: "not_found" },
      serviceRollbackGet: { status: 404, code: "not_found" },
      adminSurfacesPost: { status: 404, code: "not_found" },
      serviceKeyDelete: { status: 404, code: "not_found" },
      auditPost: { status: 404, code: "not_found" },
      operationPost: { status: 404, code: "not_found" },
      statusHead: { status: 404, code: null },
    });
  }, 30_000);
});
