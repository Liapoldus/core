import { execFile } from "node:child_process";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("registered replica Reload resolution", () => {
	it("fans out to the current lease snapshot, excludes expired replicas, and fails closed on a bad endpoint", async () => {
		const { stdout } = await execFileAsync("go", ["run", "./tests/fixtures/registered-replica-reload"], {
			cwd: coreRoot,
		});
		const result = JSON.parse(stdout);

		expect(result.found).toBe(true);
		expect(result.replicaIDs).toEqual(["replica-a", "replica-b"]);
		expect(result.called).toEqual(["replica-a", "replica-b"]);
		expect(result.released).toEqual(2);
		expect(result.emptyDirectoryUsesStatic).toBe(false);
		expect(result.expiredInstanceFenced).toBe(true);
		expect(result.invalidMemberRejectedWholeSnapshot).toBe(true);
		expect(result.closedPartialClients).toBe(1);
	}, 30_000);
});
