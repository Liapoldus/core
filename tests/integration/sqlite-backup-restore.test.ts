import { execFile } from "node:child_process";
import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
import { mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { buildCoreTestBinary } from "../support/core.js";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("Core SQLite backup and restore CLI", () => {
	 it("creates a private consistent backup and restores it after validating the source", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-sqlite-backup-"));
		const config = join(directory, "core.yaml");
		const database = join(directory, "state/core.db");
		const backup = join(directory, "backup/core.db");
		const binary = await buildCoreTestBinary();
		const lockBinary = join(directory, "sqlite-state-lock");
		await writeFile(config, bootstrapConfig(database), "utf8");
		try {
			await execFileAsync(binary, ["--config", config, "access", "bootstrap"], { cwd: coreRoot });
			const before = await serviceKeyCount(database);
			expect(before).toBeGreaterThan(0);

			await execFileAsync(binary, ["--config", config, "database", "backup", backup], { cwd: coreRoot });
			expect((await stat(backup)).mode & 0o077).toBe(0);
			await execFileAsync("go", ["build", "-o", lockBinary, "./tests/fixtures/sqlite-state-lock"], { cwd: coreRoot });
			const holder = spawn(lockBinary, [database], { stdio: ["ignore", "pipe", "ignore"] });
			const lines = createInterface({ input: holder.stdout });
			await new Promise<void>((resolve, reject) => {
				const timer = setTimeout(() => reject(new Error("state lock fixture did not start")), 10_000);
				lines.once("line", (line) => {
					clearTimeout(timer);
					line === "locked" ? resolve() : reject(new Error("state lock fixture failed"));
				});
				holder.once("error", reject);
			});
			try {
				const conflict = await execFileAsync(binary, ["--output", "json", "--config", config, "database", "restore", backup], { cwd: coreRoot }).catch((error: { stdout?: string }) => error);
				expect(JSON.parse(conflict.stdout ?? "{}")).toMatchObject({ ok: false, problem: { code: "database_busy" } });
			} finally {
				holder.kill("SIGTERM");
				await new Promise<void>((resolve) => holder.once("close", () => resolve()));
				lines.close();
			}

			await execFileAsync("go", ["run", "./tests/fixtures/sqlite-audit-trigger", database], { cwd: coreRoot });
			await execFileAsync(binary, ["--config", config, "database", "restore", backup], { cwd: coreRoot });
			expect(await readFile(database)).toEqual(await readFile(backup));
			expect(await serviceKeyCount(database)).toBe(before);

			const corruptBackup = join(directory, "corrupt.sqlite");
			await writeFile(corruptBackup, await readFile(backup));
			await writeFile(corruptBackup, Buffer.from("not a sqlite database"));
			await expect(execFileAsync(binary, ["--config", config, "database", "restore", corruptBackup], { cwd: coreRoot })).rejects.toBeDefined();
			expect(await serviceKeyCount(database)).toBe(before);
		} finally {
			await rm(directory, { recursive: true, force: true });
		}
	}, 120_000);
});

async function serviceKeyCount(database: string): Promise<number> {
	const result = await execFileAsync("go", ["run", "./tests/fixtures/sqlite-service-key-count", database], { cwd: coreRoot });
	return Number(JSON.parse(result.stdout).count);
}

function bootstrapConfig(database: string): string {
	return [
		"state:", `  path: ${database}`,
		"management:", "  listen: 127.0.0.1:19443", "  tls:",
		"    certificate: file:/tmp/management.crt", "    key: file:/tmp/management.key",
		"pluginControl:", "  listen: 127.0.0.1:19444", "  publicURL: https://127.0.0.1:19444", "  tls:",
		"    certificate: file:/tmp/plugin-control.crt", "    key: file:/tmp/plugin-control.key",
		"    replicaClientCA: file:/tmp/plugin-replica-ca.crt", "    replicaServerCA: file:/tmp/plugin-replica-server-ca.crt",
		"plugins:", "  - instanceId: catalog", "    replicas:", "      - replicaId: catalog-a",
		"        endpoint: https://127.0.0.1:19445", "        expectedPeerIdentity:", "          commonName: catalog-a", "",
	].join("\n");
}
