import { execFile } from "node:child_process";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("Core outbound plugin TLS identity pinning", () => {
	it("accepts only the exact registered URI or common-name identity after chain verification", async () => {
		const { stdout } = await execFileAsync("go", ["run", "./tests/fixtures/plugin-peer-identity"], {
			cwd: coreRoot,
		});
		const result = JSON.parse(stdout);

		expect(result.exactURIAccepted).toBe(true);
		expect(result.wrongURIRejected).toBe(true);
		expect(result.missingURIRejected).toBe(true);
		expect(result.exactCommonNameAccepted).toBe(true);
		expect(result.revocationFailurePreserved).toBe(true);
	}, 30_000);
});
