import { execFile } from "node:child_process";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("registered replica roll-forward reconciliation", () => {
	 it("retries only live replicas that have not acknowledged the exact active generation", async () => {
		const { stdout } = await execFileAsync("go", ["run", "./tests/fixtures/registered-replica-reconciliation"], {
			cwd: coreRoot,
		});
		const result = JSON.parse(stdout);

		expect(result.currentGenerationSkipped).toBe(true);
		expect(result.staleGenerationReloadedExactlyOnce).toBe(true);
		expect(result.failedReloadRetried).toBe(true);
		expect(result.expiredReplicaSkipped).toBe(true);
		expect(result.exactAcknowledgementsRecorded).toBe(true);
	}, 30_000);
});
