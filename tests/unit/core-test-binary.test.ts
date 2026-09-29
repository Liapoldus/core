import { existsSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { buildCoreTestBinary, cleanupCoreTestBinary } from "../support/core.js";

describe("Core test binary lifecycle", () => {
  it("removes its temporary executable and directory on cleanup", async () => {
    const executable = await buildCoreTestBinary();
    expect(existsSync(executable)).toBe(true);

    await cleanupCoreTestBinary();

    expect(existsSync(executable)).toBe(false);
  }, 60_000);
});
