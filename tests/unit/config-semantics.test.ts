import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { createConfig, createConfigDir } from "../support/fixture.js";
import { jsonOutput, runGateway } from "../support/gateway.js";

const goldenVectors = JSON.parse(readFileSync(resolve(import.meta.dirname, "../../contracts/v1/golden-vectors.json"), "utf8")) as {
  vectors: Array<{ id: string; input: { site?: { locales?: string[]; defaultLocale?: string } }; expected: { code?: string } }>;
};

function vector(id: string) {
  const match = goldenVectors.vectors.find((candidate) => candidate.id === id);
  if (match === undefined) throw new Error(`missing gateway golden vector ${id}`);
  return match;
}

async function writeSiteRoot(directory: string, manifest: string): Promise<void> {
  await mkdir(directory, { recursive: true });
  await writeFile(join(directory, "site.yaml"), manifest, "utf8");
  await writeFile(join(directory, "index.html"), "<html></html>", "utf8");
}

describe("gateway config semantic validation", () => {
  it("rejects a remote management listener without a TLS profile", async () => {
    const config = await createConfig(
      "registry:\n  path: ./registry\n" +
        "management:\n  listener:\n    address: 0.0.0.0:9443\n",
    );

    const result = await runGateway(["--output", "json", "config", "validate", config]);

    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result)).toMatchObject({ problem: { code: "management_tls_required" } });
  });

  it("rejects a remote management listener whose TLS profile does not require a client certificate", async () => {
    const config = await createConfig(
      "registry:\n  path: ./registry\n" +
        "management:\n  listener:\n    address: 0.0.0.0:9443\n    tlsProfile: public\n" +
        "tlsProfiles:\n  public:\n" +
        "    certificates:\n      - cert: ./cert.pem\n        key: ./key.pem\n",
    );

    const result = await runGateway(["--output", "json", "config", "validate", config]);

    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result)).toMatchObject({ problem: { code: "management_mtls_required" } });
  });

  it("accepts a fully specified remote management listener", async () => {
    const config = await createConfig(
      "registry:\n  path: ./registry\n" +
        "management:\n  listener:\n    address: 0.0.0.0:9443\n    tlsProfile: admin\n" +
        "  serviceAccounts:\n" +
        "    - id: admin\n      role: platform-admin\n      keyHash: file:./secrets/accounts/admin.bcrypt\n" +
        "tlsProfiles:\n  admin:\n" +
        "    certificates:\n      - cert: ./cert.pem\n        key: ./key.pem\n" +
        "    clientAuth:\n      mode: require\n      ca: ./ca.pem\n",
    );

    const result = await runGateway(["--output", "json", "config", "validate", config]);

    expect(result.exitCode).toBe(0);
    expect(jsonOutput(result)).toMatchObject({ ok: true, valid: true });
  });

  it("rejects staticToken on a non-loopback management listener", async () => {
    const config = await createConfig(
      "registry:\n  path: ./registry\n" +
        "management:\n  listener:\n    address: 0.0.0.0:9443\n    tlsProfile: admin\n" +
        "  staticToken: file:./secrets/token\n" +
        "  serviceAccounts:\n" +
        "    - id: admin\n      role: platform-admin\n      keyHash: file:./secrets/accounts/admin.bcrypt\n" +
        "tlsProfiles:\n  admin:\n" +
        "    certificates:\n      - cert: ./cert.pem\n        key: ./key.pem\n" +
        "    clientAuth:\n      mode: require\n      ca: ./ca.pem\n",
    );

    const result = await runGateway(["--output", "json", "config", "validate", config]);

    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result)).toMatchObject({ problem: { code: "config_invalid" } });
  });

  it("rejects a defaultLocale that is not listed in site locales", async () => {
    const expected = vector("locale-invalid-default");
    const invalidSite = expected.input.site!;
    const root = await createConfigDir();
    await writeSiteRoot(root, ["slug: demo", "locales:", ...(invalidSite.locales ?? []).map((locale) => `  - ${locale}`), `defaultLocale: ${invalidSite.defaultLocale}`].join("\n"));
    const config = await createConfig(
      "registry:\n  path: ./registry\n" +
        "sites:\n  demo:\n    source:\n      type: directory\n      root: " +
        root +
        "\n",
    );

    const result = await runGateway(["--output", "json", "config", "validate", config]);

    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result)).toMatchObject({ problem: { code: expected.expected.code } });
  });

  it("accepts a defaultLocale listed in site locales", async () => {
    const root = await createConfigDir();
    await writeSiteRoot(root, "slug: demo\nlocales:\n  - en\n  - de\ndefaultLocale: de\n");
    const config = await createConfig(
      "registry:\n  path: ./registry\n" +
        "sites:\n  demo:\n    source:\n      type: directory\n      root: " +
        root +
        "\n",
    );

    const result = await runGateway(["--output", "json", "config", "validate", config]);

    expect(result.exitCode).toBe(0);
    expect(jsonOutput(result)).toMatchObject({ ok: true, valid: true });
  });
});
