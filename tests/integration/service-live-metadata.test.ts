import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { createServer, request as httpsRequest, type Server } from "node:https";
import type { PeerCertificate, TLSSocket } from "node:tls";
import { describe, expect, it } from "vitest";
import { buildCoreTestBinary, startCoreWithOutput } from "../support/core.js";
import { freeAddress } from "../support/http.js";
import { initializeCore } from "../support/initialize.js";
import { missingWorkspaceRepositories } from "../support/workspace.js";

const execFileAsync = promisify(execFile);
const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
const workspaceRoot = dirname(coreRoot);
const missing = missingWorkspaceRepositories("service-sdk");

interface Endpoint {
  readonly method: string;
  readonly path: string;
}

interface LifecycleContract {
  readonly contractVersion: string;
  readonly identity: { readonly uriTemplate: string };
  readonly endpoints: Record<string, Endpoint>;
  readonly responses: Record<string, { readonly status: number }>;
}

interface HTTPContract {
  readonly contractVersion: string;
  readonly identity: { readonly registration: { readonly mediaType: string } };
  readonly service: {
    readonly endpoints: Record<string, Endpoint>;
    readonly responses: { readonly contentTypes: { readonly json: string } };
  };
}

interface ResponseValue {
  readonly status: number;
  readonly body: string;
}

function request(address: string, options: {
  readonly path: string;
  readonly method?: string;
  readonly token?: string;
  readonly ca: Buffer;
  readonly cert?: Buffer;
  readonly key?: Buffer;
  readonly body?: string;
  readonly expectedServerName?: string;
  readonly contentType?: string;
}): Promise<ResponseValue> {
  const port = Number(address.slice(address.lastIndexOf(":") + 1));
  return new Promise((resolve, reject) => {
    const requestValue = httpsRequest({
      hostname: "127.0.0.1",
      port,
      path: options.path,
      method: options.method ?? "GET",
      ca: options.ca,
      cert: options.cert,
      key: options.key,
      ...(options.expectedServerName === undefined ? {} : {
        servername: options.expectedServerName,
        checkServerIdentity: (_hostname: string, certificate: PeerCertificate) => {
          return certificate.subject.CN === options.expectedServerName ? undefined : new Error("unexpected Core TLS identity");
        },
      }),
      headers: {
        ...(options.token === undefined ? {} : { Authorization: `Bearer ${options.token}` }),
        ...(options.contentType === undefined ? {} : { "Content-Type": options.contentType }),
        ...(options.body === undefined ? {} : { "Content-Length": Buffer.byteLength(options.body) }),
      },
      timeout: 5000,
    }, (response) => {
      const chunks: Buffer[] = [];
      response.on("data", (chunk: Buffer) => chunks.push(Buffer.from(chunk)));
      response.once("end", () => resolve({ status: response.statusCode ?? 0, body: Buffer.concat(chunks).toString("utf8") }));
    });
    requestValue.once("error", reject);
    requestValue.once("timeout", () => requestValue.destroy(new Error("request timed out")));
    requestValue.end(options.body);
  });
}

async function issueServiceCertificate(
  directory: string,
  name: string,
  caCertificate: string,
  caKey: string,
  identityURI: string,
  usage: "serverAuth" | "clientAuth",
): Promise<{ certificate: Buffer; key: Buffer }> {
  const keyPath = join(directory, `${name}.key`);
  const requestPath = join(directory, `${name}.csr`);
  const certificatePath = join(directory, `${name}.crt`);
  const extensionsPath = join(directory, `${name}.ext`);
  await execFileAsync("openssl", ["req", "-new", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:prime256v1", "-nodes", "-keyout", keyPath, "-out", requestPath, "-subj", `/CN=${name}`]);
  await writeFile(extensionsPath, `[service]\nbasicConstraints=CA:FALSE\nkeyUsage=digitalSignature\nextendedKeyUsage=${usage}\nsubjectAltName=DNS:localhost,IP:127.0.0.1,URI:${identityURI}\n`);
  await execFileAsync("openssl", [
    "x509", "-req", "-in", requestPath, "-CA", caCertificate, "-CAkey", caKey,
    "-CAcreateserial", "-days", "1", "-extfile", extensionsPath, "-extensions", "service", "-out", certificatePath,
  ]);
  return { certificate: await readFile(certificatePath), key: await readFile(keyPath) };
}

async function listen(server: Server): Promise<string> {
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  if (address === null || typeof address === "string") throw new Error("metadata service has no TCP address");
  return `127.0.0.1:${String(address.port)}`;
}

async function stop(server: Server): Promise<void> {
  if (!server.listening) return;
  await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
}

async function waitForCore(address: string, ca: Buffer, child: Awaited<ReturnType<typeof startCoreWithOutput>>["process"]): Promise<void> {
  let lastError: unknown;
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (child.exitCode !== null) throw new Error(`Core exited before Management API became ready (${child.exitCode}).`);
    try {
      const response = await request(address, { path: "/healthz", ca });
      if (response.status === 200) return;
    } catch (error) {
      lastError = error;
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  }
  throw new Error(`Core Management API did not become ready: ${String(lastError)}.`);
}

describe.skipIf(missing.length > 0)("Core-backed live Service SDK metadata", () => {
  it("registers a live mTLS replica and returns its manifest/schema/status through Management API", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-core-live-service-metadata-"));
    const binary = await buildCoreTestBinary();
    const [managementAddress, controlAddress] = await Promise.all([freeAddress(), freeAddress()]);
    let core: Awaited<ReturnType<typeof startCoreWithOutput>> | undefined;
    let service: Server | undefined;

    try {
      const initialized = await initializeCore(binary, directory, managementAddress, controlAddress);
      const lifecycle = JSON.parse(await readFile(join(workspaceRoot, "service-sdk/infrastructure/assets/service-sdk/v2/replica-lifecycle.json"), "utf8")) as LifecycleContract;
      const httpContract = JSON.parse(await readFile(join(workspaceRoot, "service-sdk/infrastructure/assets/service-sdk/v2/http-contract.json"), "utf8")) as HTTPContract;
      const serviceIdentity = { instanceId: "server", replicaId: "replica-1", incarnationId: "incarnation-1", placementId: "local" };
      const serviceURI = lifecycle.identity.uriTemplate
        .replace("{instanceId}", serviceIdentity.instanceId)
        .replace("{replicaId}", serviceIdentity.replicaId)
        .replace("{incarnationId}", serviceIdentity.incarnationId);

      const serviceServerTLS = await issueServiceCertificate(
        directory, "server-rest", join(directory, "replica-server-ca.crt"), join(directory, "replica-server-ca.key"), serviceURI, "serverAuth",
      );
      const serviceClientTLS = await issueServiceCertificate(
        directory, "server-lifecycle", join(directory, "replica-client-ca.crt"), join(directory, "replica-client-ca.key"), serviceURI, "clientAuth",
      );
      const manifest = {
        id: "server",
        version: "1.2.3",
        sdkVersion: "service-sdk/v2",
        manifestVersion: "service-runtime/v1",
        configurationUI: {
          version: "service-configuration-ui/v1",
          groups: [{
            id: "listeners",
            title: { ru: "Слушатели", en: "Listeners" },
            fields: [{ path: "/port", label: { ru: "Порт", en: "Port" }, widget: "slider" }],
          }],
        },
      };
      const schema = { type: "object", properties: { port: { type: "integer", minimum: 1, maximum: 65535 } } };
      const identity = { contractVersion: httpContract.contractVersion, instanceId: "server", replicaId: "replica-1", appliedGeneration: "1", ready: true };
      const readiness = { ready: true, generation: "1", sha256: "a".repeat(64), schemaVersion: "1", pendingGeneration: "", instanceId: "server", replicaId: "replica-1" };
      const documents: Record<string, unknown> = { manifest, configSchema: schema, identity, ready: readiness };
      const received: string[] = [];
      const controlCertificate = await readFile(join(directory, "control.crt"));

      service = createServer({
        cert: serviceServerTLS.certificate, key: serviceServerTLS.key, ca: controlCertificate,
        requestCert: true, rejectUnauthorized: true, minVersion: "TLSv1.3",
      }, (requestValue, response) => {
        if (!(requestValue.socket as TLSSocket).authorized) {
          response.writeHead(401).end();
          return;
        }
        const route = Object.entries(httpContract.service.endpoints).find(([, endpoint]) => endpoint.method === requestValue.method && endpoint.path === requestValue.url);
        if (route === undefined || !(route[0] in documents)) {
          response.writeHead(404).end();
          return;
        }
        received.push(route[0]);
        response.writeHead(200, { "Content-Type": httpContract.service.responses.contentTypes.json });
        response.end(JSON.stringify(documents[route[0]]));
      });
      const serviceAddress = await listen(service);
      const serviceEndpoint = `https://${serviceAddress}`;
      const coreClientCA = controlCertificate;
      const managementCA = await readFile(join(directory, "management.crt"));

      core = await startCoreWithOutput(["serve"], initialized.environment);
      try {
        await waitForCore(managementAddress, managementCA, core.process);
      } catch (error) {
        throw new Error(`${String(error)}\nCore stdout: ${core.stdout}\nCore stderr: ${core.stderr}`);
      }

      const registration = {
        contractVersion: lifecycle.contractVersion,
        identity: serviceIdentity,
        restEndpoint: serviceEndpoint,
        peerEndpoints: [],
        release: { version: "1.2.3", sha256: "a".repeat(64) },
        advertisedContracts: [],
        acceptedContracts: [],
        appliedGeneration: "1",
        ready: true,
      };
      const register = await request(controlAddress, {
        path: lifecycle.endpoints.register.path,
        method: lifecycle.endpoints.register.method,
        ca: coreClientCA,
        cert: serviceClientTLS.certificate,
        key: serviceClientTLS.key,
        expectedServerName: "service-control",
        contentType: httpContract.identity.registration.mediaType,
        body: JSON.stringify(registration),
      });
      expect(register.status).toBe(lifecycle.responses.register.status);

      const manifestResponse = await request(managementAddress, { path: "/api/services/server/manifest", token: initialized.bootstrapToken, ca: managementCA });
      const schemaResponse = await request(managementAddress, { path: "/api/services/server/schema", token: initialized.bootstrapToken, ca: managementCA });
      const statusResponse = await request(managementAddress, { path: "/api/services/server/status", token: initialized.bootstrapToken, ca: managementCA });
      expect(manifestResponse.status).toBe(200);
      expect(JSON.parse(manifestResponse.body)).toEqual(manifest);
      expect(JSON.parse(manifestResponse.body).configurationUI).toEqual(manifest.configurationUI);
      expect(schemaResponse.status).toBe(200);
      expect(JSON.parse(schemaResponse.body)).toEqual(schema);
      expect(statusResponse.status).toBe(200);
      expect(JSON.parse(statusResponse.body)).toEqual({
        instanceId: "server", replicaId: "replica-1", artifactId: "server", artifactVersion: "1.2.3",
        artifactDigest: `sha256:${"a".repeat(64)}`, identity, readiness,
        replicas: [{
          replicaId: "replica-1", artifactVersion: "1.2.3", artifactDigest: `sha256:${"a".repeat(64)}`,
          identity, readiness, ready: true,
          configuration: { desiredGeneration: null, appliedGeneration: null, state: "unconfigured" },
        }],
      });
      expect(received.sort()).toEqual(["configSchema", "identity", "manifest", "ready"].flatMap((route) => [route, route, route]).sort());
    } finally {
      if (core !== undefined) await core.stop();
      if (service !== undefined) await stop(service);
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);
});
