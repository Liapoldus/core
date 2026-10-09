import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const root = join(import.meta.dirname, "../..");

describe("Core v2 traffic rollout contract", () => {
	it("pins the v2 contract bytes in its manifest", async () => {
		const [manifestBytes, contractBytes] = await Promise.all([
			readFile(join(root, "contracts/v2/manifest.json")),
			readFile(join(root, "contracts/v2/traffic-rollout.openapi.yaml")),
		]);
		const manifest = JSON.parse(manifestBytes.toString("utf8")) as { version: string; files: Record<string, string> };
		expect(manifest.version).toBe("liapoldus.core.v2");
		expect(manifest.files["traffic-rollout.openapi.yaml"]).toBe(`sha256:${createHash("sha256").update(contractBytes).digest("hex")}`);
	});

	it("separates platform-admin rollout authoring from the mTLS controller surface", async () => {
		const openapi = await readFile(join(root, "contracts/v2/traffic-rollout.openapi.yaml"), "utf8");
		expect(openapi).toContain("/api/plugins/{pluginId}/rollouts:");
		expect(openapi).toContain("/internal/v2/traffic-rollouts:");
		expect(openapi).toContain("/internal/v2/traffic-rollouts/{rolloutId}/confirmation:");
		expect(openapi).toContain("/api/plugins/{pluginId}/rollouts/{rolloutId}/stages/{stageId}/approve:");
		expect(openapi).toContain("type: mutualTLS");
		expect(openapi).toContain("PlatformAdminBearer");
		expect(openapi).toContain("TrafficControllerMTLS");
		expect(openapi).toContain("replicaId");
		expect(openapi).toContain("incarnation");
		expect(openapi).toContain("candidateWeightPercent");
		expect(openapi).toContain("controllerRevision");
		expect(openapi).toContain("requireManualApproval");
	});

	it("keeps the immediate settings endpoint distinct from staged traffic rollout", async () => {
		const fields = await readFile(join(root, "assets/contracts/management-fields.yaml"), "utf8");
		const openapi = await readFile(join(root, "contracts/v2/traffic-rollout.openapi.yaml"), "utf8");
		expect(fields).toContain("pluginSettingsSuffix: /settings");
		expect(openapi).toContain("/api/plugins/{pluginId}/rollouts:");
		expect(openapi).toContain("configuration:");
		expect(openapi).toContain("format: binary");
	});
});
