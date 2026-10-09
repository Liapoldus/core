import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("platform-admin traffic rollout endpoint", () => {
	it("accepts exact raw configuration plus stage metadata and returns a durable operation", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-traffic-rollout-api-"));
		try {
			const environment = process.env.LIAPOLDUS_GOWORK ? { ...process.env, GOWORK: process.env.LIAPOLDUS_GOWORK } : process.env;
			const result = await execFileAsync("go", ["run", "./tests/fixtures/traffic-rollout-api", join(directory, "core.db")], { cwd: coreRoot, env: environment });
			expect(JSON.parse(result.stdout)).toEqual({
				accepted: true,
				operationCreated: true,
				idempotentReplay: true,
				rawConfigurationPreserved: true,
				wrongMethodRejected: true,
				oversizedMetadataRejected: true,
				manualApprovalAccepted: true,
				manualApprovalIdempotent: true,
				manualApprovalAdvanced: true,
				approvalOperationCompleted: true,
				adminCanReadRevision: true,
			});
		} finally {
			await rm(directory, { recursive: true, force: true });
		}
	}, 30000);
});
