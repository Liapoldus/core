import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("traffic rollout controller confirmation", () => {
	it("commits a confirmation and its idempotency receipt atomically", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-rollout-confirmation-"));
		try {
			const result = await execFileAsync("go", ["run", "./tests/fixtures/traffic-rollout-store", join(directory, "core.db"), "confirm-once"], { cwd: coreRoot });
			expect(JSON.parse(result.stdout)).toEqual({
				confirmed: true,
				exactReplayReturnsOriginalReceipt: true,
				reusedKeyWithDifferentRequestConflicts: true,
				stageChangedOnce: true,
				auditWrittenOnce: true,
			});
		} finally {
			await rm(directory, { recursive: true, force: true });
		}
	}, 30_000);
});
