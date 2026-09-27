import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { readGoPackageSources } from "../support/presentation-source";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

describe("Management API contract value ownership", () => {
  it("reads existing JSON keys, query names, and response media types from contract assets", async () => {
    const [source, fields] = await Promise.all([
      readGoPackageSources(coreRoot, "internal/presentation/api"),
      readFile(join(coreRoot, "assets/contracts/management-fields.yaml"), "utf8"),
    ]);

    for (const field of [
      "requestId: requestId",
      "status: status",
      "state: state",
      "items: items",
      "nextCursor: nextCursor",
      "limit: limit",
      "cursor: cursor",
      "json: application/json",
      "problem: application/problem+json",
    ]) {
      expect(fields).toContain(field);
    }

    for (const value of [
      "requestId",
      "status",
      "state",
      "items",
      "nextCursor",
      "limit",
      "cursor",
      "application/json",
      "application/problem+json",
    ]) {
      expect(source).not.toContain(`"${value}"`);
    }
  });
});
