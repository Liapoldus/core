import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("registered rollout target loss", () => {
	it("fails the durable rollout after the frozen target lease expires without changing active config", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-rollout-target-lost-"));
		try {
			const { stdout } = await execFileAsync("go", ["run", "./tests/fixtures/registered-rollout-target-lost", join(directory, "core.db")], {
				cwd: coreRoot,
			});
			expect(JSON.parse(stdout)).toEqual({
				state: "failed",
				errorCode: "target_lost",
				activeGeneration: 2,
				rolloutOpen: false,
				reloadCount: 1,
				reloadAfterLeaseExpiry: false,
			});
		} finally {
			await rm(directory, { recursive: true, force: true });
		}
	}, 30_000);
});
