import { execFile } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("traffic-controller v2 config loader", () => {
	it("loads a strict standalone config and resolves file references relative to it", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-traffic-controller-"));
		const configPath = join(directory, "traffic-controller.yaml");
		const binary = join(directory, "config-loader");
		try {
			await writeFile(configPath, validConfig(), "utf8");
			await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/traffic-controller-config"], { cwd: coreRoot });
			const loaded = await execFileAsync(binary, [configPath], { cwd: coreRoot });
			expect(JSON.parse(loaded.stdout)).toMatchObject({
				SchemaVersion: 2,
				Listen: "127.0.0.1:9445",
				Certificate: join(directory, "controller.crt"),
				Key: join(directory, "controller.key"),
				ClientCA: join(directory, "controller-ca.crt"),
				ClientCRLs: [join(directory, "controller.crl")],
				AllowedIdentities: [{ CommonName: "controller", UniformResourceIdentifier: "spiffe://liapoldus/controller/traffic" }],
			});

			for (const [name, invalid] of [
				["unknown field", validConfig().replace("schemaVersion: 2", "schemaVersion: 2\nextra: true")],
				["unsupported version", validConfig().replace("schemaVersion: 2", "schemaVersion: 1")],
				["duplicate key", validConfig().replace("listen: 127.0.0.1:9445", "listen: 127.0.0.1:9445\nlisten: 127.0.0.1:9446")],
				["empty identities", validConfig().replace("  - commonName: controller\n    uniformResourceIdentifier: spiffe://liapoldus/controller/traffic\n", "")],
				["duplicate identity", validConfig().replace("  - commonName: controller\n    uniformResourceIdentifier: spiffe://liapoldus/controller/traffic\n", "  - commonName: controller\n    uniformResourceIdentifier: spiffe://liapoldus/controller/traffic\n  - commonName: controller\n")],
				["inline key", validConfig().replace("key: file:controller.key", "key: plaintext-private-key")],
			]) {
				await writeFile(configPath, invalid, "utf8");
				let rejected = false;
				try {
					await execFileAsync(binary, [configPath], { cwd: coreRoot });
				} catch {
					rejected = true;
				}
				expect(rejected, name).toBe(true);
			}
		} finally {
			await rm(directory, { recursive: true, force: true });
		}
	});
});

function validConfig(): string {
	return [
		"schemaVersion: 2",
		"listen: 127.0.0.1:9445",
		"tls:",
		"  certificate: file:controller.crt",
		"  key: file:controller.key",
		"  clientCA: file:controller-ca.crt",
		"  clientCRLs:",
		"    - file:controller.crl",
		"allowedIdentities:",
		"  - commonName: controller",
		"    uniformResourceIdentifier: spiffe://liapoldus/controller/traffic",
		"",
	].join("\n");
}
