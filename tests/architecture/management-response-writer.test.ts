import { describe, expect, it } from "vitest";
import { fileURLToPath } from "node:url";
import { readGoPackageSources } from "../support/presentation-source";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

describe("Management API response writer", () => {
  it("encodes JSON responses through one shared writer", async () => {
    const source = await readGoPackageSources(coreRoot, "internal/presentation/api");
    const encoderCalls = source.match(/json\.NewEncoder\(response\)\.Encode\(/g) ?? [];

    expect(encoderCalls).toHaveLength(1);
  });
});
