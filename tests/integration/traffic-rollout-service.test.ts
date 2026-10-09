import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("traffic rollout application service", () => {
	it("requires the exact ready candidate cohort and delegates one atomic promotion", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-traffic-rollout-service-"));
		try {
			const result = await execFileAsync("go", ["run", "./tests/fixtures/traffic-rollout-service", join(directory, "core.db")], { cwd: coreRoot });
			expect(JSON.parse(result.stdout)).toEqual({
				created: true,
				idempotentReplay: true,
				candidateExact: true,
				incumbentCaptured: true,
				invalidCandidateRejected: true,
				staleIncarnationRejected: true,
				pluginSchemaAccepted: true,
				pluginSchemaRejected: true,
			});
		} finally {
			await rm(directory, { recursive: true, force: true });
		}
	}, 30000);
});
