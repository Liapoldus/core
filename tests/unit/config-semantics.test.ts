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
  it("rejects a WAF challenge whose provider is not declared", async () => {
    const config = await createConfig(
      "listeners: {}\n" +
        "wafPolicies:\n" +
        "  protect:\n" +
        "    rules:\n" +
        "      - when: { path: { prefix: /private } }\n" +
        "        then: { challenge: { provider: missing } }\n",
    );

    const result = await runGateway(["--output", "json", "config", "validate", config]);

    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result)).toMatchObject({ problem: { code: "config_invalid" } });
  });

  it("rejects a captcha provider bound to a capability the plugin did not declare", async () => {
    const config = await createConfig(
      "listeners: {}\n" +
        "secrets:\n  recaptchaSecret: env:LIAPOLDUS_CAPTCHA_SECRET\n" +
        "plugins:\n" +
        "  captcha:\n" +
        "    binary: ./captcha\n" +
        "    capabilities: [captcha.other]\n" +
        "    settings: {}\n" +
        "    grants:\n" +
        "      secrets:\n" +
        "        - name: recaptchaSecret\n" +
        "          purpose: captcha.verify\n" +
        "          domains: [www.google.com]\n" +
        "captchaProviders:\n" +
        "  public:\n" +
        "    plugin: { instance: captcha, capability: captcha.verify }\n" +
        "    verifyUrl: https://www.google.com/recaptcha/api/siteverify\n" +
        "    secret: recaptchaSecret\n" +
        "    allowedHosts: [www.google.com]\n" +
        "wafPolicies:\n" +
        "  protect:\n" +
        "    rules:\n" +
        "      - when: { path: { prefix: /private } }\n" +
        "        then: { challenge: { provider: public } }\n",
    );

    const result = await runGateway(["--output", "json", "config", "validate", config], {
      LIAPOLDUS_CAPTCHA_SECRET: "test-only-captcha-secret",
    });

    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result)).toMatchObject({ problem: { code: "config_invalid" } });
    expect(result.stdout).not.toContain("test-only-captcha-secret");
    expect(result.stderr).not.toContain("test-only-captcha-secret");
  });

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
