import { execFile } from "node:child_process";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("plugin configuration snapshot", () => {
  it("serves exact active and previous bytes without reading the durable source on pull", async () => {
    const { stdout } = await execFileAsync("go", ["run", "./tests/fixtures/plugin-config-snapshot"], { cwd: coreRoot });
    expect(JSON.parse(stdout)).toEqual({
      initial: { status: 200, body: '{ "value": 1 }', digestValid: true },
      promoted: { status: 200, body: '{"value":2}', digestValid: true },
      previous: { status: 200, body: '{ "value": 1 }', digestValid: true },
      durableReadsDuringPull: 0,
    });
  }, 30_000);
});
