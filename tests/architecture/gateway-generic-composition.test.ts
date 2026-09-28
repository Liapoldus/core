import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

describe("Gateway composition root", () => {
  it("only composes the generic control-plane entry point", async () => {
    const source = await readFile(join(root, "cmd", "gateway", "main.go"), "utf8");

    expect(source).toContain("cli.Execute(os.Args[1:])");
    expect(source).not.toContain("RuntimeBindings");
  });
});
