import { access, readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { join } from "node:path";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

describe("Core executable surface", () => {
  it("contains only the API server and no embedded Core CLI", async () => {
    const source = await readFile(join(coreRoot, "cmd/core/main.go"), "utf8");
    await expect(access(join(coreRoot, "cmd/core-migrate"))).rejects.toBeDefined();
    expect(source).toContain("internal/runtime");
    expect(source).not.toContain("internal/presentation/cli");
    expect(source).not.toMatch(/Execute\(|flag\.|database\s+(backup|restore)/);
  });
});
