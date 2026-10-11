import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { fileURLToPath } from "node:url";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

describe("Management API response writer", () => {
  it("encodes JSON responses through one shared writer", async () => {
    const directory = join(coreRoot, "internal/presentation/api");
    const files = (await readdir(directory, { withFileTypes: true }))
      .filter((entry) => entry.isFile() && entry.name.endsWith(".go") && !entry.name.endsWith("_test.go"))
      .map((entry) => join(directory, entry.name));
    const source = (await Promise.all(files.map((file) => readFile(file, "utf8")))).join("\n");
    const encoderCalls = source.match(/json\.NewEncoder\(response\)\.Encode\(/g) ?? [];

    expect(encoderCalls).toHaveLength(1);
  });
});
