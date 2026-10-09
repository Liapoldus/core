import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { join } from "node:path";
import { readGoPackageSources } from "../support/presentation-source";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

describe("target Core CLI surface", () => {
	it("contains only the server, access bootstrap and SQLite maintenance commands", async () => {
    const [contracts, source] = await Promise.all([
      readFile(join(coreRoot, "assets/contracts/cli-fields.yaml"), "utf8"),
      readGoPackageSources(coreRoot, "internal/presentation/cli"),
    ]);

    const commands = contracts.match(/^commands:\n((?:  [^\n]+\n)+)/m)?.[1] ?? "";
		expect(commands).toMatch(/serve: serve/);
		expect(commands).toMatch(/access: access/);
		expect(commands).toMatch(/database: database/);
    expect(commands).not.toMatch(/(?:config|accounts|site):/);
    expect(contracts).not.toMatch(/^subcommands:/m);
    expect(contracts).not.toMatch(/^site:/m);
    expect(contracts).not.toMatch(/--config|LIAPOLDUS_CORE_CONFIG|core\.yaml/);
    expect(source).not.toMatch(/func (config|accounts|site)\(/);
  });
});
