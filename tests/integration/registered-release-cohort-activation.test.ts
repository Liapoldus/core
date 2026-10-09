import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { createGoWorkspace } from "../support/workspace.js";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("registered release cohort configuration activation", () => {
	it("preserves both published generations and clears staging when the release gate rejects", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-release-cohort-activation-"));
		const workspace = createGoWorkspace("core", "plugin-sdk");
		try {
			const { stdout } = await execFileAsync(
				"go",
				["run", "./tests/fixtures/release-cohort-activation", join(directory, "core.db")],
				{ cwd: coreRoot, env: { ...process.env, GOWORK: workspace.path } },
			);
			expect(JSON.parse(stdout)).toEqual({
				operationState: "failed",
				operationErrorCode: "plugin_revision_conflict",
				activeGeneration: 2,
				activeRaw: '{ "release" : "stable" }\n',
				previousGeneration: 1,
				previousRaw: '{\n  "release" : "older"\n}\n',
				stagingPresent: false,
				pluginApplyCalls: 0,
			});
		} finally {
			workspace.cleanup();
			await rm(directory, { recursive: true, force: true });
		}
	}, 45_000);
});
