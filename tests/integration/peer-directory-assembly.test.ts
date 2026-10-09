import { execFile } from "node:child_process";
import { join } from "node:path";
import { promisify } from "node:util";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { parseJsonObject } from "../support/json.js";
import { createGoWorkspace } from "../support/workspace.js";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("Core peer directory assembly", () => {
	let workspace: { path: string; cleanup: () => void };

	beforeAll(() => {
		workspace = createGoWorkspace("core", "plugin-sdk");
	});

	afterAll(() => {
		workspace.cleanup();
	});

	it("assembles an SDK-valid peer directory from deny-by-default link policy", async () => {
		const { stdout } = await execFileAsync("go", ["run", "./tests/fixtures/peer-directory-assembly"], {
			cwd: coreRoot,
			env: { ...process.env, GOWORK: workspace.path },
		});
		const result = parseJsonObject(stdout);

		expect(result.parsedValid).toBe(true);
		expect(result.linkCount).toBe(true);
		expect(result.remoteLinkPresent).toBe(true);
		expect(result.samePlacementLinkPresent).toBe(true);
		expect(result.hiddenTargetDenied).toBe(true);
		expect(result.readyReplicaHasEndpoint).toBe(true);
		expect(result.notReadyReplicaHasNoEndpoint).toBe(true);
		expect(result.notReadyReplicaNotEligible).toBe(true);
		expect(result.weightPreserved).toBe(true);
		expect(result.requiredContractPreserved).toBe(true);
		expect(result.emptyPolicyDeniesAll).toBe(true);
		expect(result.generationStable).toBe(true);
		expect(result.generationIgnoresTime).toBe(true);
		expect(result.callerScoped).toBe(true);
		expect(result.overMaximumTTLRejected).toBe(true);
	}, 45_000);
});
