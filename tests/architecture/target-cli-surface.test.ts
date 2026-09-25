import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { join } from "node:path";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

describe("target Gateway CLI surface", () => {
  it("contains only the server and access bootstrap commands", async () => {
    const [contracts, source] = await Promise.all([
      readFile(join(coreRoot, "assets/contracts/cli-fields.yaml"), "utf8"),
      readFile(join(coreRoot, "internal/presentation/cli/cli.go"), "utf8"),
    ]);

    expect(contracts).not.toMatch(/^  (config|accounts|site):/m);
    expect(contracts).not.toMatch(/^subcommands:/m);
    expect(contracts).not.toMatch(/^site:/m);
    expect(source).not.toMatch(/func (config|accounts|site)\(/);
  });
});
