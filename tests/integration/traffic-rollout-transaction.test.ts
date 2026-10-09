import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("atomic traffic rollout submission", () => {
	it("promotes the exact JSON generation and stores operation, cohorts, and audit atomically", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-traffic-rollout-tx-"));
		const database = join(directory, "core.db");
		try {
			const result = await execFileAsync("go", ["run", "./tests/fixtures/traffic-rollout-transaction", database], { cwd: coreRoot });
			expect(JSON.parse(result.stdout)).toEqual({
				created: true,
				idempotentReplay: true,
				differentRequestRejected: true,
				activeGeneration: 1,
				activeRawJSON: '{ "settings" : [1, 2] }',
				trafficRollouts: 1,
				pluginRollouts: 1,
				pluginTargets: 1,
				operations: 1,
				auditEvents: 1,
				pluginTargetIDs: ["candidate-1"],
			});
		} finally {
			await rm(directory, { recursive: true, force: true });
		}
	}, 30000);
});
