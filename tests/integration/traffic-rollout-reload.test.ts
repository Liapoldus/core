import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("traffic rollout candidate Reload barrier", () => {
	it("persists exact candidate ACKs and retries only unacknowledged candidate incarnations", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-traffic-rollout-reload-"));
		try {
			const result = await execFileAsync("go", ["run", "./tests/fixtures/traffic-rollout-reload", join(directory, "core.db")], { cwd: coreRoot });
			expect(JSON.parse(result.stdout)).toEqual({
				firstPending: true,
				firstCalls: ["candidate-a/inc-a", "candidate-b/inc-b"],
				secondCalls: ["candidate-b/inc-b"],
				candidateTargets: ["candidate-a/inc-a:true", "candidate-b/inc-b:true"],
				incumbentNeverCalled: true,
				configurationBarrierClosed: true,
				lostTargetFencesTrafficRollout: true,
				ordinarySettingsBlockedDuringTrafficRollout: true,
				configurationCohortHeldDuringRollout: true,
				configurationCohortHeldAfterTargetLost: true,
			});
		} finally {
			await rm(directory, { recursive: true, force: true });
		}
	}, 45_000);
});
