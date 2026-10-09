import { execFile } from "node:child_process";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { createGoWorkspace } from "../support/workspace.js";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("registered release cohort compatibility", () => {
	it("blocks configuration activation for mixed releases without mutual contract acceptance", async () => {
		const workspace = createGoWorkspace("core", "plugin-sdk");
		try {
			const { stdout } = await execFileAsync("go", ["run", "./tests/fixtures/registered-release-cohort"], {
				cwd: coreRoot,
				env: { ...process.env, GOWORK: workspace.path },
			});
			expect(JSON.parse(stdout)).toEqual({
				sameReleaseAllowed: true,
				mutuallyCompatibleReleasesAllowed: true,
				oneWayCompatibilityBlocked: true,
				missingCompatibilityEvidenceBlocked: true,
			});
		} finally {
			workspace.cleanup();
		}
	}, 45_000);
});
