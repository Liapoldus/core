import { describe, expect, it } from "vitest";
import { createConfig } from "../support/fixture.js";
import { jsonOutput, runGateway } from "../support/gateway.js";

function bootstrap(listen: string, options: { tls?: boolean; clientCA?: boolean } = {}): string {
  const tls = options.tls === false
    ? ""
    : `  tls:\n    certificate: file:/run/secrets/management.crt\n    key: file:/run/secrets/management.key\n${options.clientCA ? "    clientCA: file:/run/secrets/management-ca.crt\n" : ""}`;
  return `state:\n  path: ./data/gateway.db\nartifacts:\n  path: ./data/artifacts\nmanagement:\n  listen: ${JSON.stringify(listen)}\n${tls}  bearerVerifier: file:/run/secrets/service-key-verifiers.json\ncaddy:\n  variant: embedded\n`;
}

async function validate(document: string): Promise<boolean> {
  const path = await createConfig(document);
  const result = await runGateway(["--output", "json", "--config", path, "config", "validate"]);
  if (result.exitCode === 0) return true;
  expect(jsonOutput(result)).toHaveProperty("problem.code");
  return false;
}

describe("Management API bootstrap transport security", () => {
  it("requires a TLS server identity even on a loopback bind", async () => {
    await expect(validate(bootstrap("127.0.0.1:9443", { tls: false }))).resolves.toBe(false);
  });

  it("allows loopback TLS without client certificates for the SSH bridge", async () => {
    await expect(validate(bootstrap("127.0.0.1:9443"))).resolves.toBe(true);
    await expect(validate(bootstrap("[::1]:9443"))).resolves.toBe(true);
  });

  it.each(["10.20.30.40:9443", "gateway.internal:9443", "0.0.0.0:9443", "[::]:9443"])(
    "requires client CA verification for non-loopback bind %s",
    async (listen) => {
      await expect(validate(bootstrap(listen))).resolves.toBe(false);
      await expect(validate(bootstrap(listen, { clientCA: true }))).resolves.toBe(true);
    },
  );
});
