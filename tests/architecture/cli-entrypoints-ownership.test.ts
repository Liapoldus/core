import { access, readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
describe("Core command ownership", () => {
  it("does not contain the retired embedded CLI package", async () => {
    await expect(access(join(root, "internal", "presentation", "cli"))).rejects.toBeDefined();
    const source = await readFile(join(root, "cmd", "core", "main.go"), "utf8");
    expect(source).not.toContain("cli.Execute");
    expect(source).toContain("runtime.Serve");
  });
});
