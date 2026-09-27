import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { join } from "node:path";
import { readGoPackageSources } from "../support/presentation-source";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

describe("plugin-agnostic Gateway core", () => {
  it("contains no built-in plugin names or plugin-specific dispatch models", async () => {
    const files = [
      "assets/contracts/gateway.schema.json",
      "assets/contracts/errors.json",
      "contracts/v1/http-runtime.json",
      "contracts/v1/security-runtime.json",
      "internal/infrastructure/config/bootstrap.go",
      "internal/infrastructure/plugins/capability_dispatch.go",
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
});
