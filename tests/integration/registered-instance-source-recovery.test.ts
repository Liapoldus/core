import { execFile } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("registered plugin instance source recovery", () => {
	it("requires fresh registration after restart and rejects unregistered static identities", async () => {
		const directory = mkdtempSync(join(tmpdir(), "core-registered-source-"));
		try {
			const { stdout } = await execFileAsync("go", ["run", "./tests/fixtures/registered-source-recovery", join(directory, "core.sqlite")], {
				cwd: coreRoot,
				maxBuffer: 1024 * 1024,
			});
			expect(JSON.parse(stdout)).toEqual({
				registeredInstanceRequiresFreshLeaseAfterRestart: true,
				unregisteredStaticIdentityRejected: true,
			});
		} finally {
			rmSync(directory, { recursive: true, force: true });
		}
	}, 30_000);
});
