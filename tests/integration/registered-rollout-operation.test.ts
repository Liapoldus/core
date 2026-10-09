import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("registered replica configuration rollout operation", () => {
	 it("keeps partial registered rollouts running and completes after exact-generation recovery", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-registered-rollout-operation-"));
		try {
			const database = join(directory, "core.db");
			const { stdout: beginOutput } = await execFileAsync("go", ["run", "./tests/fixtures/registered-rollout-operation", database, "begin"], {
				cwd: coreRoot,
			});
			expect(JSON.parse(beginOutput)).toEqual({
				stateAfterPartialAck: "running",
				activeGeneration: 2,
			});

			const { stdout: recoverOutput } = await execFileAsync("go", ["run", "./tests/fixtures/registered-rollout-operation", database, "recover"], {
				cwd: coreRoot,
			});
			expect(JSON.parse(recoverOutput)).toEqual({
				stateBeforeRegistration: "running",
				didNotRetryBeforeRegistration: true,
				recoveredState: "succeeded",
				exactGenerationRetried: true,
			});
		} finally {
			await rm(directory, { recursive: true, force: true });
		}
	}, 30_000);
});
