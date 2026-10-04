import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const coreRoot = resolve(import.meta.dirname, "../..");

describe("generic Plugin SDK Admin Surface forwarding", () => {
	it("authenticates Management access, aggregates exact replica documents, and forwards action context once", () => {
		const output = execFileSync("go", ["run", "./tests/fixtures/plugin-admin-surface-forwarding"], {
			cwd: coreRoot,
			encoding: "utf8",
			timeout: 60_000,
		});

		expect(JSON.parse(output)).toEqual({
			unauthorizedSurfaceDenied: true,
			surfaceDocumentsAggregated: true,
			surfaceBytesAndDigestPreserved: true,
			replicaSurfaceMismatchUnavailable: true,
			unauthorizedActionDenied: true,
			actionContextBound: true,
			actionSelectedConvergedReplica: true,
			actionResponsePreserved: true,
			sameKeyAcceptedActionInvokedAgain: true,
			failedActionNotReplayed: true,
			actionAuditRedacted: true,
		});
	}, 90_000);
});
