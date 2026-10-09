import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const root = join(import.meta.dirname, "../..");

describe("Core traffic-controller settings contract", () => {
	it("pins strict typed SQLite settings without a runtime config-file path", async () => {
		const [manifestBytes, schemaBytes, bootstrapBytes] = await Promise.all([
			readFile(join(root, "contracts/v2/manifest.json")),
			readFile(join(root, "contracts/v2/traffic-controller.schema.json")),
			readFile(join(root, "assets/contracts/core.schema.json"), "utf8"),
		]);
		const manifest = JSON.parse(manifestBytes.toString("utf8")) as { version: string; files: Record<string, string> };
		expect(manifest.version).toBe("liapoldus.core.v2");
		expect(manifest.files["traffic-controller.schema.json"]).toBe(
			`sha256:${createHash("sha256").update(schemaBytes).digest("hex")}`,
		);
		expect(schemaBytes.toString("utf8")).toContain("stored in SQLite");
		expect(schemaBytes.toString("utf8")).not.toContain("--traffic-controller-config");

		const schema = JSON.parse(schemaBytes.toString("utf8")) as {
			additionalProperties?: boolean;
			required?: string[];
			properties?: Record<string, unknown>;
		};
		expect(schema.additionalProperties).toBe(false);
		expect(schema.required).toEqual(["schemaVersion", "listen", "tls", "allowedIdentities"]);
		expect(Object.keys(schema.properties ?? {}).sort()).toEqual(["allowedIdentities", "listen", "schemaVersion", "tls"]);
		expect(JSON.parse(bootstrapBytes).properties).not.toHaveProperty("trafficController");
	});

	it("requires dedicated TLS trust and an exact non-empty identity allow-list", async () => {
		const schema = JSON.parse(await readFile(join(root, "contracts/v2/traffic-controller.schema.json"), "utf8")) as {
			$defs: Record<string, { required?: string[]; properties?: Record<string, unknown>; minItems?: number; additionalProperties?: boolean }>;
		};
		expect(schema.$defs.tls?.required).toEqual(["certificate", "key", "clientCA"]);
		expect(schema.$defs.allowedIdentity?.required).toEqual(["commonName"]);
		expect(schema.$defs.allowedIdentity?.additionalProperties).toBe(false);
		expect(schema.$defs.allowedIdentities?.minItems).toBe(1);
	});
});
