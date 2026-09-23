import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { join } from "node:path";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

describe("plugin-agnostic Gateway core", () => {
  it("contains no built-in plugin names or plugin-specific dispatch models", async () => {
    const files = [
      "assets/contracts/gateway.schema.json",
      "assets/contracts/errors.json",
      "contracts/v1/plugin-contracts.json",
      "contracts/v1/http-runtime.json",
      "contracts/v1/security-runtime.json",
      "internal/domain/models/compiled_graph.go",
      "internal/domain/models/waf_action.go",
      "internal/infrastructure/config/compile.go",
      "internal/infrastructure/network/serve.go",
      "internal/infrastructure/network/waf_runtime.go",
      "internal/infrastructure/plugins/capability_dispatch.go",
      "internal/domain/models/tls_certificate.go",
      "internal/presentation/api/adapter.go",
      "assets/contracts/management-fields.yaml",
      "contracts/v1/management.openapi.yaml",
      "internal/presentation/cli/cli.go",
    ];
    const source = (await Promise.all(files.map((file) => readFile(join(coreRoot, file), "utf8")))).join("\n");
    expect(source).not.toMatch(/captcha|forms\.(submit|list|delete)|tls\.(issue|renew|revoke)|identity-subject|WAFChallenge|DispatchIdentity|IdentityRequest|IdentityAction/);
    expect(source).not.toMatch(/TLS issuer|TlsIssuer|Issuer:|\/api\/tls\/\{issuer\}/i);
  });
});
