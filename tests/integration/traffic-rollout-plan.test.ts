import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("Core v2 traffic rollout plan", () => {
	it("accepts exact incarnation targets and monotonically increasing stages ending at 100%", async () => {
		const result = await validate({
			releaseSha256: "a".repeat(64),
			targets: [{ replicaId: "candidate-1", incarnation: "incarnation-1" }],
			stages: [
				{ id: "canary", candidateWeightPercent: 10, minimumObservationSeconds: 60 },
				{ id: "full", candidateWeightPercent: 100, minimumObservationSeconds: 0, requireManualApproval: true },
			],
		});

		expect(result).toMatchObject({ valid: true, requireManualApproval: true });
	});

	it("rejects replacement/duplicate targets and non-monotonic or incomplete traffic stages", async () => {
		const base = {
			releaseSha256: "b".repeat(64),
			targets: [{ replicaId: "candidate-1", incarnation: "incarnation-1" }],
			stages: [
				{ id: "canary", candidateWeightPercent: 10, minimumObservationSeconds: 60 },
				{ id: "full", candidateWeightPercent: 100, minimumObservationSeconds: 0 },
			],
		};
		const invalid = [
			{ ...base, targets: [...base.targets, base.targets[0]] },
			{ ...base, targets: [{ replicaId: "candidate-1", incarnation: "" }] },
			{ ...base, stages: [{ ...base.stages[0], candidateWeightPercent: 50 }, { ...base.stages[1], candidateWeightPercent: 40 }] },
			{ ...base, stages: [base.stages[0]] },
			{ ...base, stages: [{ ...base.stages[0], id: "same" }, { ...base.stages[1], id: "same" }] },
		];

		for (const plan of invalid) {
			expect((await validate(plan)).valid).toBe(false);
		}
	});

	it("rejects ambiguous duplicate JSON keys and unknown plan fields", async () => {
		const valid = JSON.stringify({
			releaseSha256: "c".repeat(64),
			targets: [{ replicaId: "candidate-1", incarnation: "incarnation-1" }],
			stages: [{ id: "full", candidateWeightPercent: 100, minimumObservationSeconds: 0 }],
		});
		const duplicate = valid.replace('"releaseSha256":', '"releaseSha256":"' + "d".repeat(64) + '","releaseSha256":');
		expect((await validateRaw(duplicate)).valid).toBe(false);
		expect((await validateRaw(valid.replace('"targets":', '"unexpected":true,"targets":'))).valid).toBe(false);
		expect((await validateRaw(valid.replace('"releaseSha256":', '"ReleaseSha256":'))).valid).toBe(false);
	});

	it("requires manual approval for every stage and defaults it on when omitted", async () => {
		const base = {
			releaseSha256: "a".repeat(64),
			targets: [{ replicaId: "candidate-1", incarnation: "incarnation-1" }],
			stages: [{ id: "full", candidateWeightPercent: 100, minimumObservationSeconds: 0 }],
		};
		expect(await validate(base)).toMatchObject({ valid: true, requireManualApproval: true });
		expect(await validate({ ...base, stages: [{ ...base.stages[0], requireManualApproval: false }] })).toMatchObject({ valid: false });
	});

	it("selects only the exact ready incarnation and keeps the incumbent cohort explicit", async () => {
		const result = await validateRaw(JSON.stringify({
			releaseSha256: "a".repeat(64),
			targets: [{ replicaId: "candidate", incarnation: "inc-1" }],
			stages: [
				{ id: "canary", candidateWeightPercent: 10, minimumObservationSeconds: 60 },
				{ id: "full", candidateWeightPercent: 100, minimumObservationSeconds: 0 },
			],
		}), [
			replica("candidate", "inc-1", "a", true, 60),
			replica("incumbent", "inc-7", "b", true, 60),
		]);

		expect(result).toMatchObject({
			valid: true,
			candidateTargets: [{ replicaId: "candidate", incarnation: "inc-1" }],
			incumbentTargets: [{ replicaId: "incumbent", incarnation: "inc-7" }],
		});
	});

	it("rejects a lost/replaced, unready, expired, or wrong-release candidate and a missing incumbent", async () => {
		const plan = {
			releaseSha256: "a".repeat(64),
			targets: [{ replicaId: "candidate", incarnation: "inc-1" }],
			stages: [
				{ id: "canary", candidateWeightPercent: 10, minimumObservationSeconds: 60 },
				{ id: "full", candidateWeightPercent: 100, minimumObservationSeconds: 0 },
			],
		};
		const invalidCohorts = [
			[replica("candidate", "inc-2", "a", true, 60), replica("incumbent", "inc-7", "b", true, 60)],
			[replica("candidate", "inc-1", "a", false, 60), replica("incumbent", "inc-7", "b", true, 60)],
			[replica("candidate", "inc-1", "a", true, -60), replica("incumbent", "inc-7", "b", true, 60)],
			[replica("candidate", "inc-1", "c", true, 60), replica("incumbent", "inc-7", "b", true, 60)],
			[replica("candidate", "inc-1", "a", true, 60)],
		];
		for (const cohort of invalidCohorts) {
			expect((await validateRaw(JSON.stringify(plan), cohort)).valid).toBe(false);
		}
	});

	it("allows an immediate 100% stage without an incumbent and excludes unavailable incumbents", async () => {
		const plan = {
			releaseSha256: "a".repeat(64),
			targets: [{ replicaId: "candidate", incarnation: "inc-1" }],
			stages: [{ id: "full", candidateWeightPercent: 100, minimumObservationSeconds: 0 }],
		};
		const full = await validateRaw(JSON.stringify(plan), [replica("candidate", "inc-1", "a", true, 60)]);
		expect(full).toMatchObject({ valid: true, candidateTargets: [{ replicaId: "candidate", incarnation: "inc-1" }], incumbentTargets: [] });

		const partial = await validateRaw(JSON.stringify({ ...plan, stages: [{ ...plan.stages[0], candidateWeightPercent: 10 }, { ...plan.stages[0], id: "full" }] }), [
			replica("candidate", "inc-1", "a", true, 60),
			replica("expired", "inc-2", "b", true, -60),
			replica("unready", "inc-3", "b", false, 60),
		]);
		expect(partial.valid).toBe(false);
	});
});

async function validate(plan: unknown): Promise<{ valid: boolean }> {
	return validateRaw(JSON.stringify(plan));
}

async function validateRaw(raw: string, replicas?: unknown[]): Promise<Record<string, unknown> & { valid: boolean }> {
	const directory = await mkdtemp(join(tmpdir(), "liapoldus-traffic-rollout-plan-"));
	try {
		const binary = join(directory, "traffic-rollout-plan");
		await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/traffic-rollout-plan"], { cwd: coreRoot });
		const argumentsList = [raw];
		if (replicas) argumentsList.push(JSON.stringify(replicas));
		const result = await execFileAsync(binary, argumentsList, { cwd: coreRoot });
		return JSON.parse(result.stdout) as Record<string, unknown> & { valid: boolean };
	} finally {
		await rm(directory, { recursive: true, force: true });
	}
}

function replica(replicaId: string, incarnation: string, release: string, ready: boolean, leaseSeconds: number): unknown {
	return {
		replicaId,
		incarnation,
		releaseSha256: release.repeat(64),
		ready,
		leaseExpiresInSeconds: leaseSeconds,
	};
}
