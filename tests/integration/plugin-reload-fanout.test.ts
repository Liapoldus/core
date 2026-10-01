import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("SDK reload fanout", () => {
  it("continues roll-forward after one replica refuses and reports every outcome", async () => {
    const { stdout } = await execFileAsync("go", ["run", "./tests/fixtures/plugin-reload-fanout"], { cwd: coreRoot });
    expect(JSON.parse(stdout)).toEqual({
      error: true,
      calls: [1, 1, 1],
      replicas: [
        { id: "first", acknowledged: true, unreachable: false },
        { id: "second", acknowledged: false, unreachable: false },
        { id: "third", acknowledged: true, unreachable: false },
      ],
      nilFanoutUnavailable: true,
    });
  }, 30_000);
});
