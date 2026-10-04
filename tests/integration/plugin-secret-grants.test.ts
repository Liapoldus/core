import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("Core Plugin SDK scoped secret grants", () => {
	it("binds grants to the active generation and exact mTLS replica, redeems once, and never emits the value outside redemption", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-secret-grants-"));
		try {
			const result = await execFileAsync("go", ["run", "./tests/fixtures/plugin-secret-grants", directory], {
				cwd: coreRoot,
				timeout: 30_000,
			});
			expect(JSON.parse(result.stdout)).toEqual({
				grantLimitEnforced: true,
				issuedForActiveGeneration: true,
				previousGenerationDenied: true,
				unreferencedSecretDenied: true,
				wrongReplicaDenied: true,
				activeGenerationChangeDenied: true,
				redemptionReturnsExactSecret: true,
				secondRedemptionRejected: true,
				secretAbsentFromLogsAndErrors: true,
				handleAbsentFromLogsAndErrors: true,
			});
		} finally {
			await rm(directory, { recursive: true, force: true });
		}
	}, 60_000);
});
