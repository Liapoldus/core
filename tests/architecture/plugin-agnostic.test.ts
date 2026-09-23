import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { join } from "node:path";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

describe("plugin-agnostic Gateway core", () => {
  it("contains no built-in plugin names or plugin-specific dispatch models", async () => {
    const files = [
      "assets/contracts/gateway.schema.json",
      "internal/domain/models/compiled_graph.go",
      "internal/domain/models/waf_action.go",
      "internal/infrastructure/config/compile.go",
      "internal/infrastructure/network/serve.go",
      "internal/infrastructure/network/waf_runtime.go",
      "internal/infrastructure/plugins/capability_dispatch.go",
      "internal/presentation/cli/cli.go",
    ];
    const source = (await Promise.all(files.map((file) => readFile(join(coreRoot, file), "utf8")))).join("\n");
    expect(source).not.toMatch(/captchaProviders|CaptchaProvider|captcha\.verify|WAFChallenge|DispatchIdentity|IdentityRequest|IdentityAction/);
  });
});
