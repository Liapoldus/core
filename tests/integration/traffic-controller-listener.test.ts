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
import { initializeCore } from "../support/initialize.js";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("private traffic-controller listener", () => {
	it("serves the dedicated mTLS-only intent API from core serve", async () => {
		const directory = await mkdtemp(join(tmpdir(), "liapoldus-traffic-controller-listener-"));
		const binary = await buildCoreTestBinary();
		const managementAddress = await freeAddress();
		const pluginControlAddress = await freeAddress();
		const controllerAddress = await freeAddress();
		const controllerCertificate = join(directory, "controller.crt");
		const controllerKey = join(directory, "controller.key");
		const controllerServerCertificate = join(directory, "controller-server.crt");
		const controllerServerKey = join(directory, "controller-server.key");
		const deniedCertificate = join(directory, "denied.crt");
		const deniedKey = join(directory, "denied.key");
		const controllerCA = join(directory, "controller-ca.crt");
		let database = "";
		let environment: NodeJS.ProcessEnv = {};
		let core: Awaited<ReturnType<typeof startCoreWithOutput>> | undefined;

		try {
			const initialized = await initializeCore(binary, directory, managementAddress, pluginControlAddress);
			database = initialized.database;
			environment = initialized.environment;
			await createCA(directory, "traffic-controller", controllerCA);
			await createCertificate(directory, "traffic-controller-server", controllerServerCertificate, controllerServerKey, "DNS:localhost,IP:127.0.0.1");
			await createSignedClient(directory, "controller", controllerCA, controllerCertificate, controllerKey,
				"URI:spiffe://liapoldus/controller/traffic");
			await createSignedClient(directory, "denied", controllerCA, deniedCertificate, deniedKey,
				"URI:spiffe://liapoldus/controller/unrelated");
			await execFileAsync("go", ["run", "./tests/fixtures/traffic-rollout-store", database, "create"], { cwd: coreRoot });
			core = await startCoreWithOutput(["serve"], environment);
			await waitForManagement(managementAddress, core.process);
			const current = await managementRequest(managementAddress, "/api/v2/settings", initialized.bootstrapToken);
			expect(current.status, current.body).toBe(200);
			const settings = JSON.parse(current.body).settings as Record<string, unknown>;
			settings.trafficController = { schemaVersion: 2, listen: controllerAddress, certificate: controllerServerCertificate, key: controllerServerKey, clientCA: controllerCA, allowedIdentities: [{ CommonName: "controller", UniformResourceIdentifier: "spiffe://liapoldus/controller/traffic" }] };
			const updated = await updateCoreSettings(managementAddress, initialized.bootstrapToken, settings);
			expect(updated.status, updated.body).toBe(200);
			await core.stop();
			core = await startCoreWithOutput(["serve"], environment);
			await waitForTLS(controllerAddress, core.process, {
				cert: controllerCertificate, key: controllerKey, ca: controllerCA,
				details: () => core?.stderr ?? "",
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
			expect(initialized.bootstrapToken.length).toBeGreaterThan(0);
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

async function waitForManagement(address: string, child: Awaited<ReturnType<typeof startCoreWithOutput>>["process"]): Promise<void> {
	for (let attempt = 0; attempt < 120; attempt += 1) {
		if (child.exitCode !== null) throw new Error(`Core exited before Management API became ready (${child.exitCode}).`);
		try {
			const response = await managementRequest(address, "/healthz");
			if (response.status === 200) return;
		} catch { await new Promise((resolve) => setTimeout(resolve, 50)); }
	}
	throw new Error("Core Management API did not become ready.");
}

function managementRequest(address: string, path: string, token?: string, settings?: unknown): Promise<{ status: number; body: string }> {
	const port = Number(address.slice(address.lastIndexOf(":") + 1));
	return new Promise((resolve, reject) => {
		const body = settings === undefined ? undefined : JSON.stringify(settings);
		const outgoing = httpsRequest({ hostname: "127.0.0.1", port, path,
			method: settings === undefined ? "GET" : "PUT", rejectUnauthorized: false,
			headers: { ...(token === undefined ? {} : { Authorization: `Bearer ${token}` }),
				...(body === undefined ? {} : { "Content-Type": "application/json", "If-Match": '"core-settings-1"' }) } }, (incoming) => {
			const chunks: Buffer[] = [];
			incoming.on("data", (chunk: Buffer) => chunks.push(Buffer.from(chunk)));
			incoming.once("end", () => resolve({ status: incoming.statusCode ?? 0, body: Buffer.concat(chunks).toString("utf8") }));
		});
		outgoing.once("error", reject);
		outgoing.end(body);
	});
}

async function updateCoreSettings(address: string, token: string, settings: unknown) {
	return managementRequest(address, "/api/v2/settings", token, settings);
}

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
