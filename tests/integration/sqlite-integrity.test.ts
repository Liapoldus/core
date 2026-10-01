import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const root = resolve(import.meta.dirname, "../..");

describe("SQLite startup integrity gate", () => {
  it("refuses an orphaned foreign key before Core serves requests", () => {
    const output = execFileSync("go", ["run", "./tests/fixtures/sqlite-integrity"], {
      cwd: root,
      encoding: "utf8",
      timeout: 30_000,
      env: { ...process.env, GOWORK: "off", GOTOOLCHAIN: "go1.26.0" },
    });
    expect(JSON.parse(output)).toEqual({ orphanRejected: true, validReopen: true });
  }, 30_000);
});
