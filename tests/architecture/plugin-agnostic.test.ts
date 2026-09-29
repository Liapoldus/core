import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { join } from "node:path";
import { readGoPackageSources } from "../support/presentation-source";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

describe("plugin-agnostic Core core", () => {
  it("contains no built-in plugin names or plugin-specific dispatch models", async () => {
    const files = [
      "assets/contracts/core.schema.json",
      "assets/contracts/errors.json",
      "contracts/v1/security-runtime.json",
      "internal/infrastructure/config/bootstrap.go",
      "assets/contracts/management-fields.yaml",
      "contracts/v1/management.openapi.yaml",
    ];
    const [contractSources, apiSources, cliSources] = await Promise.all([
      Promise.all(files.map((file) => readFile(join(coreRoot, file), "utf8"))),
      readGoPackageSources(coreRoot, "internal/presentation/api"),
      readGoPackageSources(coreRoot, "internal/presentation/cli"),
    ]);
    const source = [...contractSources, apiSources, cliSources].join("\n");
    expect(source).not.toMatch(/captcha|forms\.(submit|list|delete)|tls\.(issue|renew|revoke)|identity-subject|WAFChallenge|DispatchIdentity|IdentityRequest|IdentityAction/);
    expect(source).not.toMatch(/WAFContext|WAFDecision|wafDecision|json:\"waf/);
    expect(source).not.toMatch(/TLS issuer|TlsIssuer|Issuer:|\/api\/tls\/\{issuer\}/i);
  });

  it("derives nothing from the peer protocol library or plugin executables", async () => {
    const [production, plugins, bootstrap, command, fixtures, module, moduleRoot] = await Promise.all([
      readGoPackageSources(coreRoot, "internal"),
      readGoPackageSources(coreRoot, "internal/infrastructure/plugins"),
      readGoPackageSources(coreRoot, "internal/presentation/cli"),
      readGoPackageSources(coreRoot, "cmd"),
      readGoPackageSources(coreRoot, "tests/fixtures"),
      readFile(join(coreRoot, "go.mod"), "utf8"),
      readFile(join(coreRoot, "contractassets.go"), "utf8"),
    ]);
    const source = [plugins, bootstrap, command, fixtures].join("\n");

    // The peer protocol boundary is a whole-tree guarantee, not a package-local
    // one: no layer of Core may reach for plugin-to-plugin transport.
    expect(production).not.toMatch(/github\.com\/Liapoldus\/pluginprotocol/);
    expect(moduleRoot).not.toMatch(/pluginprotocol/);
    expect(source).not.toMatch(/github\.com\/Liapoldus\/pluginprotocol/);
    expect(source).not.toMatch(/pluginv1|protojson/);
    expect(source).not.toMatch(/plugin_launch_settings|launchFields|configSecrets/);
    expect(source).not.toMatch(/exec\.Command(?:Context)?\(/);
    expect(source).not.toMatch(/(?:restartEnabled|restartInitialBackoff|restartMaximumBackoff|healthFailureThreshold|memoryLimitBytes)/);
    expect(module).not.toMatch(/pluginprotocol/);
  });
});
