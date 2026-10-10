import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

describe("Core composition root", () => {
  it("only composes the generic control-plane entry point", async () => {
    const source = await readFile(join(root, "cmd", "core", "main.go"), "utf8");

    expect(source).toContain("runtime.Serve");
    expect(source).toContain("runtime.StatePath");
    expect(source).not.toContain("cli.Execute");
    expect(source).not.toContain("RuntimeBindings");
  });
});
