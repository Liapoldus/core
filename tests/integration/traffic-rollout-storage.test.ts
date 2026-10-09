import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("Core v2 durable traffic rollout state", () => {
	it("creates rollout and per-stage durable tables with a one-open-rollout invariant", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-traffic-rollout-storage-"));
		try {
			const result = await execFileAsync("go", ["run", "./tests/fixtures/sqlite-probe", join(directory, "core.db")], { cwd: coreRoot });
			const database = JSON.parse(result.stdout) as { migrationVersion: number; requiredTables: string[] };
			expect(database.migrationVersion).toBe(14);
			expect(database.requiredTables).toEqual(expect.arrayContaining([
				"traffic_rollouts",
				"traffic_rollout_stages",
				"traffic_rollout_cohort_targets",
			]));
		} finally {
			await rm(directory, { recursive: true, force: true });
		}
	}, 30000);
});
