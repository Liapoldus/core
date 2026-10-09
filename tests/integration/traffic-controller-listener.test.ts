import { execFile } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { request as httpsRequest } from "node:https";
import { describe, expect, it } from "vitest";
import { buildCoreTestBinary, startCoreWithOutput } from "../support/core.js";
import { freeAddress } from "../support/http.js";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("private traffic-controller listener", () => {
	it("serves the dedicated mTLS-only intent API from core serve", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-traffic-controller-listener-"));
		const binary = await buildCoreTestBinary();
		const managementAddress = await freeAddress();
		const pluginControlAddress = await freeAddress();
		const pluginAddress = await freeAddress();
		const controllerAddress = await freeAddress();
		const managementCertificate = join(directory, "management.crt");
		const managementKey = join(directory, "management.key");
		const pluginCertificate = join(directory, "plugin-control.crt");
		const pluginKey = join(directory, "plugin-control.key");
		const pluginClientCA = join(directory, "plugin-client-ca.crt");
		const pluginServerCA = join(directory, "plugin-server-ca.crt");
		const controllerCertificate = join(directory, "controller.crt");
		const controllerKey = join(directory, "controller.key");
		const controllerServerCertificate = join(directory, "controller-server.crt");
		const controllerServerKey = join(directory, "controller-server.key");
		const deniedCertificate = join(directory, "denied.crt");
		const deniedKey = join(directory, "denied.key");
		const controllerCA = join(directory, "controller-ca.crt");
		const database = join(directory, "core.db");
		const bootstrap = join(directory, "core.yaml");
		const controllerConfig = join(directory, "traffic-controller.yaml");
		let core: Awaited<ReturnType<typeof startCoreWithOutput>> | undefined;

		try {
			await createCertificate(directory, "management", managementCertificate, managementKey, "DNS:localhost,IP:127.0.0.1");
			await createCertificate(directory, "plugin-control", pluginCertificate, pluginKey, "DNS:localhost,IP:127.0.0.1");
			await createCA(directory, "plugin-client", pluginClientCA);
			await createCA(directory, "plugin-server", pluginServerCA);
			await createCA(directory, "traffic-controller", controllerCA);
			await createCertificate(directory, "traffic-controller-server", controllerServerCertificate, controllerServerKey, "DNS:localhost,IP:127.0.0.1");
			await createSignedClient(directory, "controller", controllerCA, controllerCertificate, controllerKey,
				"URI:spiffe://liapoldus/controller/traffic");
			await createSignedClient(directory, "denied", controllerCA, deniedCertificate, deniedKey,
				"URI:spiffe://liapoldus/controller/unrelated");
			await writeFile(bootstrap, [
				"state:", `  path: ${database}`, "management:", `  listen: ${managementAddress}`,
				"  tls:", `    certificate: file:${managementCertificate}`, `    key: file:${managementKey}`,
				"pluginControl:", `  listen: ${pluginControlAddress}`, "  publicURL: https://core.internal:9444",
				"  tls:", `    certificate: file:${pluginCertificate}`, `    key: file:${pluginKey}`,
				`    replicaClientCA: file:${pluginClientCA}`, `    replicaServerCA: file:${pluginServerCA}`,
				"plugins:", "  - instanceId: fixture", "    replicas:", "      - replicaId: fixture-r1",
				`        endpoint: https://${pluginAddress}`, "        expectedPeerIdentity:", "          commonName: fixture-r1", "",
			].join("\n"), "utf8");
			await writeFile(controllerConfig, [
				"schemaVersion: 2", `listen: ${controllerAddress}`, "tls:",
				`  certificate: file:${controllerServerCertificate}`, `  key: file:${controllerServerKey}`,
				`  clientCA: file:${controllerCA}`, "allowedIdentities:",
				"  - commonName: controller", "    uniformResourceIdentifier: spiffe://liapoldus/controller/traffic", "",
			].join("\n"), "utf8");
			const initialized = await execFileAsync(binary, ["--config", bootstrap, "access", "bootstrap"]);
			await execFileAsync("go", ["run", "./tests/fixtures/traffic-rollout-store", database, "create"], { cwd: coreRoot });
			core = await startCoreWithOutput(["--config", bootstrap, "serve", "--traffic-controller-config", controllerConfig]);
			await waitForTLS(controllerAddress, core.process, {
				cert: controllerCertificate, key: controllerKey, ca: controllerCA,
				path: "/internal/v2/traffic-rollouts", details: () => core?.stderr ?? "",
			});
			const allowed = await request(controllerAddress, "/internal/v2/traffic-rollouts", {
				cert: controllerCertificate, key: controllerKey, ca: controllerCA,
			});
			const cached = await request(controllerAddress, "/internal/v2/traffic-rollouts", {
				cert: controllerCertificate, key: controllerKey, ca: controllerCA, ifNoneMatch: allowed.headers.etag,
			});
			const listed = JSON.parse(allowed.body) as { rollouts: Array<{ id: string; pluginId: string; activeGeneration: number; revision: number; stageId: string; state: string }> };
			const confirmationBody = JSON.stringify({ stageId: "canary", appliedCandidateWeightPercent: 10, controllerRevision: "traffic-rev-1" });
			const confirmation = await request(controllerAddress, "/internal/v2/traffic-rollouts/rollout-1/confirmation", {
				cert: controllerCertificate, key: controllerKey, ca: controllerCA, method: "PUT", body: confirmationBody,
				headers: { "Content-Type": "application/json", "If-Match": '"1"', "Idempotency-Key": "controller-confirm-1" },
			});
			const replay = await request(controllerAddress, "/internal/v2/traffic-rollouts/rollout-1/confirmation", {
				cert: controllerCertificate, key: controllerKey, ca: controllerCA, method: "PUT", body: confirmationBody,
				headers: { "Content-Type": "application/json", "If-Match": '"1"', "Idempotency-Key": "controller-confirm-1" },
			});
			const keyConflict = await request(controllerAddress, "/internal/v2/traffic-rollouts/rollout-1/confirmation", {
				cert: controllerCertificate, key: controllerKey, ca: controllerCA, method: "PUT",
				body: confirmationBody.replace("traffic-rev-1", "traffic-rev-2"),
				headers: { "Content-Type": "application/json", "If-Match": '"1"', "Idempotency-Key": "controller-confirm-1" },
			});
			const rejectedIdentity = await request(controllerAddress, "/internal/v2/traffic-rollouts", {
				cert: deniedCertificate, key: deniedKey, ca: controllerCA,
			});
			const denied = await request(controllerAddress, "/internal/v2/traffic-rollouts", { ca: controllerCA });
			expect(initialized.stdout.trim().length).toBeGreaterThan(0);
			expect(allowed.status).toBe(200);
			expect(allowed.headers.etag).toMatch(/^"[a-f0-9]{64}"$/);
			expect(listed.rollouts).toEqual([expect.objectContaining({
				id: "rollout-1", pluginId: "forms", activeGeneration: 8, revision: 1,
				stageId: "canary", state: "awaiting_controller",
			})]);
			expect(cached.status).toBe(304);
			expect(confirmation.status).toBe(200);
			expect(confirmation.body).toContain('"revision":2');
			expect(replay.body).toBe(confirmation.body);
			expect(keyConflict.status).toBe(409);
			expect(rejectedIdentity.status).toBe(403);
			expect(denied.status).toBe(0);
		} finally {
			if (core !== undefined) await core.stop();
			await rm(directory, { recursive: true, force: true });
		}
	}, 180_000);
});

type TLSOptions = { cert?: string; key?: string; ca: string; ifNoneMatch?: string | string[]; method?: string; body?: string; headers?: Record<string, string> };

function request(address: string, path: string, options: TLSOptions): Promise<{ status: number; body: string; headers: Record<string, string | string[] | undefined> }> {
	const port = Number(address.slice(address.lastIndexOf(":") + 1));
	return new Promise((resolve, reject) => {
		const headers = { ...(options.headers ?? {}), ...(options.ifNoneMatch === undefined ? {} : { "If-None-Match": options.ifNoneMatch }) };
		const outgoing = httpsRequest({ hostname: "127.0.0.1", port, path, method: options.method ?? "GET", ca: readFileSync(options.ca), headers,
			cert: options.cert === undefined ? undefined : readFileSync(options.cert),
			key: options.key === undefined ? undefined : readFileSync(options.key), rejectUnauthorized: false }, (incoming) => {
			const chunks: Buffer[] = [];
			incoming.on("data", (chunk: Buffer) => chunks.push(Buffer.from(chunk)));
			incoming.once("end", () => resolve({ status: incoming.statusCode ?? 0,
				body: Buffer.concat(chunks).toString("utf8"), headers: incoming.headers }));
		});
		outgoing.once("error", (error) => {
			if (options.cert === undefined) resolve({ status: 0, body: "", headers: {} });
			else reject(error);
		});
		outgoing.end(options.body);
	});
}

async function waitForTLS(address: string, child: Awaited<ReturnType<typeof startCoreWithOutput>>["process"], options: TLSOptions & { details(): string }): Promise<void> {
	let lastFailure = "no response";
	for (let attempt = 0; attempt < 160; attempt += 1) {
	if (child.exitCode !== null) throw new Error(`Core exited before controller listener was ready (${child.exitCode}). ${options.details()}`);
		try {
			const response = await request(address, "/internal/v2/traffic-rollouts", options);
			if (response.status === 200) return;
			lastFailure = `status=${response.status} body=${response.body}`;
		} catch (error) { lastFailure = String(error); }
		await new Promise((resolve) => setTimeout(resolve, 50));
	}
	throw new Error(`Traffic-controller listener did not become ready (${lastFailure}). ${options.details()}`);
}

async function createCA(directory: string, name: string, certificate: string): Promise<void> {
	await execFileAsync("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", `/CN=${name}-ca`,
		"-keyout", join(directory, `${name}-ca.key`), "-out", certificate]);
}

async function createCertificate(directory: string, name: string, certificate: string, key: string, san: string): Promise<void> {
	await execFileAsync("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", `/CN=${name}`,
		"-addext", `subjectAltName=${san}`, "-keyout", key, "-out", certificate]);
}

async function createSignedClient(directory: string, name: string, ca: string, certificate: string, key: string, san: string): Promise<void> {
	const request = join(directory, `${name}.csr`);
	const extensions = join(directory, `${name}.ext`);
	await writeFile(extensions, `subjectAltName=${san}\nextendedKeyUsage=clientAuth\n`, "utf8");
	await execFileAsync("openssl", ["req", "-new", "-newkey", "rsa:2048", "-nodes", "-subj", `/CN=${name}`,
		"-keyout", key, "-out", request]);
	await execFileAsync("openssl", ["x509", "-req", "-in", request, "-CA", ca,
		"-CAkey", join(directory, "traffic-controller-ca.key"), "-CAcreateserial", "-days", "1",
		"-extfile", extensions, "-out", certificate]);
}
