import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { createGoWorkspace } from "../support/workspace.js";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("registered replica rollout cohort", () => {
	it("rolls back through a durable exact-incarnation cohort and recovers partial ACK", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-rollout-rollback-"));
		const workspace = createGoWorkspace("core", "plugin-sdk");
		const database = join(directory, "core.db");
		try {
			const { stdout: begin } = await execFileAsync("go", ["run", "./tests/fixtures/registered-rollout-cohort", database, "rollback-begin"], {
				cwd: coreRoot,
				env: { ...process.env, GOWORK: workspace.path },
			});
			expect(JSON.parse(begin)).toEqual({
				state: "running",
				activeGeneration: 1,
				previousGeneration: 2,
				targets: ["replica-a/incarnation-a", "replica-b/incarnation-b"],
				acknowledged: ["replica-b/incarnation-b"],
			});

			const { stdout: recovered } = await execFileAsync("go", ["run", "./tests/fixtures/registered-rollout-cohort", database, "rollback-recover"], {
				cwd: coreRoot,
				env: { ...process.env, GOWORK: workspace.path },
			});
			expect(JSON.parse(recovered)).toEqual({
				state: "succeeded",
				activeGeneration: 1,
				targets: ["replica-a/incarnation-a", "replica-b/incarnation-b"],
				called: ["replica-a/incarnation-a"],
				replacementNeverCalled: true,
			});
		} finally {
			workspace.cleanup();
			await rm(directory, { recursive: true, force: true });
		}
	}, 45_000);

	it("recovers against the frozen replica incarnation set without admitting a replacement", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-rollout-cohort-"));
		const workspace = createGoWorkspace("core", "plugin-sdk");
		const database = join(directory, "core.db");
		try {
			const { stdout: begin } = await execFileAsync("go", ["run", "./tests/fixtures/registered-rollout-cohort", database, "begin"], {
				cwd: coreRoot,
				env: { ...process.env, GOWORK: workspace.path },
			});
			expect(JSON.parse(begin)).toEqual({
				state: "running",
				cohort: ["replica-a/incarnation-a", "replica-b/incarnation-b"],
				acknowledged: ["replica-b"],
				prematureCloseBlocked: true,
			});

			const { stdout: replacement } = await execFileAsync("go", ["run", "./tests/fixtures/registered-rollout-cohort", database, "replacement"], {
				cwd: coreRoot,
				env: { ...process.env, GOWORK: workspace.path },
			});
			expect(JSON.parse(replacement)).toEqual({
				state: "running",
				targets: ["replica-a/incarnation-a", "replica-b/incarnation-b"],
				called: [],
				replacementDidNotAcknowledge: true,
				rollbackBlocked: true,
				secondRolloutBlocked: true,
				activeGeneration: 2,
			});

			const { stdout: recovered } = await execFileAsync("go", ["run", "./tests/fixtures/registered-rollout-cohort", database, "recovered"], {
				cwd: coreRoot,
				env: { ...process.env, GOWORK: workspace.path },
			});
			expect(JSON.parse(recovered)).toEqual({
				state: "succeeded",
				targets: ["replica-a/incarnation-a", "replica-b/incarnation-b"],
				called: ["replica-a/incarnation-a"],
			});
		} finally {
			workspace.cleanup();
			await rm(directory, { recursive: true, force: true });
		}
	}, 45_000);

	it("reloads only exact frozen incarnations and does not replay acknowledged replicas", async () => {
		const workspace = createGoWorkspace("core", "plugin-sdk");
		try {
		const { stdout } = await execFileAsync("go", ["run", "./tests/fixtures/registered-rollout-exact-dispatch"], {
			cwd: coreRoot,
			env: { ...process.env, GOWORK: workspace.path },
		});
		expect(JSON.parse(stdout)).toEqual({
			firstPending: true,
			firstAck: ["replica-b/incarnation-b"],
			firstCalls: ["replica-b/incarnation-b"],
			replacementNeverCalled: true,
			secondAck: ["replica-a/incarnation-a"],
			secondCalls: ["replica-a/incarnation-a"],
		});
		} finally {
			workspace.cleanup();
		}
	}, 45_000);
});
