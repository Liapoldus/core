import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const root = resolve(import.meta.dirname, "../..");

describe("Core generic plugin artifact action forwarding", () => {
	it("authorizes, bounds, and streams an artifact action without interpreting its metadata", () => {
		const output = execFileSync("go", ["run", "./tests/fixtures/plugin-artifact-forwarding"], {
			cwd: root,
			encoding: "utf8",
			 timeout: 30_000,
		});
		expect(JSON.parse(output)).toEqual({
			acceptedStatus: 202,
			optionalIfMatchAccepted: true,
			optionalIfMatchForwarded: true,
			metadataPreserved: true,
			artifactByteCount: 2 * 1024 * 1024,
			invocationBound: true,
			missingIdempotencyRejected: true,
			invalidMultipartRejected: true,
			unauthorizedRejected: true,
			noFilenameForwarded: true,
		});
	}, 60_000);
});
