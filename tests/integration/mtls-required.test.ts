import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import { request as httpsRequest } from "node:https";
import type { ChildProcess } from "node:child_process";
import { afterEach, describe, expect, it } from "vitest";
import { startGateway } from "../support/gateway.js";
import { freeAddress, writeGatewayConfig } from "../support/http.js";

const execFileAsync = promisify(execFile);
const managementToken = "mtls-management-test-token";
const managementEnvironment = { LIAPOLDUS_TEST_MTLS_MANAGEMENT_TOKEN: managementToken };
const vectors = JSON.parse(readFileSync(resolve(import.meta.dirname, "../../contracts/v1/golden-vectors.json"), "utf8")) as {
  vectors: Array<{
    id: string;
    input: { clientCertificate: null; mode: string };
    expected: { status: number; code: string };
  }>;
};
const vector = vectors.vectors.find(({ id }) => id === "mtls-required");
if (!vector) throw new Error("mtls-required vector is missing");

const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
const directories: string[] = [];

async function requestTLS(
  address: string,
  ca: string,
  client?: { certificate: string; key: string },
  path = "/",
  authorization?: string,
): Promise<{ status: number; body: string }> {
  const port = Number(address.slice(address.lastIndexOf(":") + 1));
  return new Promise((resolve, reject) => {
    const request = httpsRequest({
      hostname: "127.0.0.1",
      servername: "localhost",
      port,
      path,
      method: "GET",
      ca: readFileSync(ca),
      cert: client ? readFileSync(client.certificate) : undefined,
      key: client ? readFileSync(client.key) : undefined,
      headers: authorization ? { Authorization: authorization } : undefined,
    }, (response) => {
      const chunks: Buffer[] = [];
      response.on("data", (chunk: Buffer) => chunks.push(Buffer.from(chunk)));
      response.once("end", () => resolve({ status: response.statusCode ?? 0, body: Buffer.concat(chunks).toString("utf8") }));
    });
    request.once("error", reject);
    request.end();
  });
}

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    if (!gateway.process.killed) gateway.process.kill("SIGTERM");
    await gateway.stop();
  }
  for (const directory of directories.splice(0)) await rm(directory, { recursive: true, force: true });
});

describe("mTLS required golden vector", () => {
  it("returns the catalogued HTTP problem without a client certificate and accepts a valid one", async () => {
    expect(vector.input.clientCertificate).toBeNull();
    expect(vector.input.mode).toBe("require");

    const directory = await mkdtemp(join(tmpdir(), "liapoldus-mtls-required-"));
    directories.push(directory);
    const caCertificate = join(directory, "ca.crt");
    const caKey = join(directory, "ca.key");
    const serverCertificate = join(directory, "server.crt");
    const serverKey = join(directory, "server.key");
    const serverCSR = join(directory, "server.csr");
    const clientCertificate = join(directory, "client.crt");
    const clientKey = join(directory, "client.key");
    const clientCSR = join(directory, "client.csr");
    const untrustedCertificate = join(directory, "untrusted-client.crt");
    const untrustedKey = join(directory, "untrusted-client.key");
    const certificateBase = ["req", "-newkey", "rsa:2048", "-nodes", "-days", "1"];
    await execFileAsync("openssl", [
      "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
      "-subj", "/CN=Liapoldus test CA",
      "-addext", "basicConstraints=critical,CA:TRUE",
      "-addext", "keyUsage=critical,keyCertSign,cRLSign",
      "-keyout", caKey, "-out", caCertificate,
    ]);
    await execFileAsync("openssl", [
      ...certificateBase,
      "-subj", "/CN=localhost",
      "-addext", "subjectAltName=DNS:localhost",
      "-addext", "extendedKeyUsage=serverAuth",
      "-keyout", serverKey, "-out", serverCSR,
    ]);
    await execFileAsync("openssl", [
      "x509", "-req", "-days", "1", "-in", serverCSR,
      "-CA", caCertificate, "-CAkey", caKey, "-CAcreateserial", "-copy_extensions", "copy",
      "-out", serverCertificate,
    ]);
    await execFileAsync("openssl", [
      ...certificateBase,
      "-subj", "/CN=Liapoldus test client",
      "-addext", "extendedKeyUsage=clientAuth",
      "-keyout", clientKey, "-out", clientCSR,
    ]);
    await execFileAsync("openssl", [
      "x509", "-req", "-days", "1", "-in", clientCSR,
      "-CA", caCertificate, "-CAkey", caKey, "-CAcreateserial", "-copy_extensions", "copy",
      "-out", clientCertificate,
    ]);
    await execFileAsync("openssl", [
      "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
      "-subj", "/CN=Untrusted Liapoldus client",
      "-addext", "extendedKeyUsage=clientAuth",
      "-keyout", untrustedKey, "-out", untrustedCertificate,
    ]);

    const address = await freeAddress();
    const managementAddress = await freeAddress();
    const config = await writeGatewayConfig([
      "tlsProfiles:",
      "  public:",
      "    certificates:",
      `      - { cert: ${JSON.stringify(serverCertificate)}, key: ${JSON.stringify(serverKey)} }`,
      `    clientAuth: { mode: ${vector.input.mode}, ca: ${JSON.stringify(caCertificate)} }`,
      "    protocols: [http/1.1, h2]",
      "listeners:",
      "  public:",
      "    type: http",
      `    address: ${address}`,
      "    tls: public",
      "    routes: []",
      "management:",
      `  listener: { address: ${managementAddress}, tlsProfile: public }`,
      "  staticToken: env:LIAPOLDUS_TEST_MTLS_MANAGEMENT_TOKEN",
    ].join("\n"));
    const gateway = await startGateway(["--config", config, "serve"], managementEnvironment);
    gateways.push(gateway);

    let validCertificateResponse: { status: number; body: string } | undefined;
    let lastConnectionError: unknown;
    for (let attempt = 0; attempt < 100; attempt += 1) {
      try {
        validCertificateResponse = await requestTLS(address, caCertificate, { certificate: clientCertificate, key: clientKey });
        break;
      } catch (error) {
        lastConnectionError = error;
        await new Promise((resolve) => setTimeout(resolve, 100));
      }
    }
    expect(validCertificateResponse, String(lastConnectionError)).toMatchObject({ status: 404 });
    const authorizedManagementResponse = await requestTLS(
      managementAddress,
      caCertificate,
      { certificate: clientCertificate, key: clientKey },
      "/api/status",
      `Bearer ${managementToken}`,
    );
    expect(authorizedManagementResponse.status).toBe(200);

    const missingCertificateResponse = await requestTLS(address, caCertificate);
    expect(missingCertificateResponse.status).toBe(vector.expected.status);
    expect(JSON.parse(missingCertificateResponse.body)).toMatchObject({ code: vector.expected.code });
    const missingManagementCertificateResponse = await requestTLS(managementAddress, caCertificate, undefined, "/api/status");
    expect(missingManagementCertificateResponse.status).toBe(vector.expected.status);
    expect(JSON.parse(missingManagementCertificateResponse.body)).toMatchObject({ code: vector.expected.code });
    await expect(requestTLS(address, caCertificate, { certificate: untrustedCertificate, key: untrustedKey })).rejects.toBeDefined();
  }, 60_000);
});
