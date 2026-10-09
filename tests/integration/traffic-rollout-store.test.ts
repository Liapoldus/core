import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("SQLite traffic rollout store", () => {
	it("recovers immutable plan and exact cohorts after reopening and rejects a second open rollout", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-traffic-rollout-store-"));
		const database = join(directory, "core.db");
		try {
			const created = await execFileAsync("go", ["run", "./tests/fixtures/traffic-rollout-store", database, "create"], { cwd: coreRoot });
			const createdResult = JSON.parse(created.stdout) as { duplicateRejected: boolean };
			expect(createdResult.duplicateRejected).toBe(true);

			const reopened = await execFileAsync("go", ["run", "./tests/fixtures/traffic-rollout-store", database, "recover"], { cwd: coreRoot });
			expect(JSON.parse(reopened.stdout)).toMatchObject({
				ID: "rollout-1",
				InstanceID: "forms",
				Generation: 8,
				State: "running",
				ActiveStageIndex: 0,
				Revision: 1,
			Stages: [
					{ Index: 0, Stage: { id: "canary", candidateWeightPercent: 10 }, State: "active" },
					{ Index: 1, Stage: { id: "full", candidateWeightPercent: 100 }, State: "pending" },
				],
				Targets: [
					{ Cohort: "candidate", ReplicaID: "candidate-1", IncarnationID: "inc-candidate" },
					{ Cohort: "incumbent", ReplicaID: "stable-1", IncarnationID: "inc-stable" },
				],
			});
			expect(JSON.parse(reopened.stdout).PlanJSON).toBe('{ "releaseSha256" : "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "targets" : [{"replicaId":"candidate-1","incarnation":"inc-candidate"}], "stages" : [{"id":"canary","candidateWeightPercent":10,"minimumObservationSeconds":60,"requireManualApproval":true},{"id":"full","candidateWeightPercent":100,"minimumObservationSeconds":0,"requireManualApproval":true}] }');
		} finally {
			await rm(directory, { recursive: true, force: true });
		}
	}, 30000);

	it("requires the exact confirmed weight, observation window, and fresh revision before approval", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-traffic-rollout-approval-"));
		const database = join(directory, "core.db");
		try {
			const result = await execFileAsync("go", ["run", "./tests/fixtures/traffic-rollout-store", database, "lifecycle"], { cwd: coreRoot });
			expect(JSON.parse(result.stdout)).toEqual({
				wrongWeightRejected: true,
				prematureApprovalRejected: true,
				staleRevisionRejected: true,
				firstApprovalAdvanced: true,
				finalApprovalCompleted: true,
				activeStageIndex: 1,
				finalState: "completed",
				auditEvents: 4,
			});
		} finally {
			await rm(directory, { recursive: true, force: true });
		}
	}, 30000);
});
