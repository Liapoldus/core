import { execFile } from "node:child_process";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("Core live plugin replica directory", () => {
	it("binds registration to the authenticated SAN, enforces lease/incarnation rules, and expires replicas", async () => {
		const { stdout } = await execFileAsync("go", ["run", "./tests/fixtures/plugin-replica-directory"], { cwd: coreRoot });
		const result = JSON.parse(stdout);

		expect(result.validRegistration).toBe(true);
		expect(result.activeDuplicateRenews).toBe(true);
		expect(result.certificateIdentityMismatchRejected).toBe(true);
		expect(result.changedImmutableMetadataRejected).toBe(true);
		expect(result.oldIncarnationRenewalRejected).toBe(true);
		expect(result.expiredSameIncarnationRegistrationRejected).toBe(true);
		expect(result.newIncarnationReplacesExpired).toBe(true);
		expect(result.expiredReplicaNotEligible).toBe(true);
		expect(result.authenticatedHTTPRegistration).toBe(true);
		expect(result.registrationWaitsForSnapshotActivation).toBe(true);
		expect(result.reloadHookRunsAfterRegistrationPublication).toBe(true);
		expect(result.reloadHookReceivesExactReplica).toBe(true);
		expect(result.identityMismatchHTTPRejected).toBe(true);
		expect(result.oversizedRegistrationRejected).toBe(true);
		expect(result.concurrentRegistrationSurvivesPersistenceFailure).toBe(true);
		expect(result.unregisteredIdentityUsesFallback).toBe(true);
		expect(result.registeredIdentityOverridesFallback).toBe(true);
		expect(result.expiredIdentityCannotDowngrade).toBe(true);
		expect(result.replacedIncarnationCannotDowngrade).toBe(true);
	}, 30_000);
});
