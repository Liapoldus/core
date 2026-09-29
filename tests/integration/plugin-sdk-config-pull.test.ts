import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("Plugin SDK exact-generation configuration pull", () => {
	 it("serves only the authenticated instance's retained generations byte-for-byte over mTLS", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-config-pull-"));
		try {
			const result = await execFileAsync("go", ["run", "./tests/fixtures/plugin-sdk-config-pull", join(directory, "core.db")], {
				cwd: coreRoot,
				timeout: 30_000,
			});
			expect(JSON.parse(result.stdout)).toEqual({
				activeRaw: "{ \"origin\" : \"active\" }\n",
				previousRaw: "{\"origin\":\"previous\"}",
				pendingGenerationRejected: true,
				exactDigest: true,
				unknownGenerationRejected: true,
				otherInstanceRejected: true,
				unauthenticatedRejected: true,
				reloadAcknowledged: true,
				reloadGeneration: "3",
				reloadPulledRaw: "{\"origin\":\"staging\"}",
				activatedGeneration: 3,
				activeAfterReload: 3,
				previousAfterReload: 1,
				stagingCleared: true,
				mismatchedBytesRejected: true,
				unknownPluginRejected: true,
			});
		} finally {
			await rm(directory, { recursive: true, force: true });
		}
	}, 60_000);
});
