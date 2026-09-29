import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("plugin configuration roll-forward", () => {
	it("promotes desired active before Reload and keeps it on a missing ACK", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-config-roll-forward-"));
		try {
			const result = await execFileAsync("go", ["run", "./tests/fixtures/plugin-config-roll-forward", join(directory, "core.db")], { cwd: coreRoot });
			expect(JSON.parse(result.stdout)).toEqual({
				reloadSawActive: 2,
				reloadSawPrevious: 1,
				activeAfterMissingAck: 2,
				previousAfterMissingAck: 1,
				stagingAfterMissingAck: false,
				instanceFenced: true,
			});
		} finally {
			await rm(directory, { recursive: true, force: true });
		}
	}, 30_000);
});
