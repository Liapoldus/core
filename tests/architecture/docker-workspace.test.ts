import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

describe("Docker workspace dependency contract", () => {
  it("builds with only the Plugin SDK sibling module in context", async () => {
    const dockerfile = await readFile(join(root, "Dockerfile"), "utf8");
    const smoke = await readFile(join(root, "scripts", "docker-smoke.sh"), "utf8");
    const workflow = await readFile(join(root, ".github", "workflows", "verify.yml"), "utf8");

    expect(dockerfile).toContain("COPY plugin-sdk /workspace/plugin-sdk");
    expect(dockerfile).toContain("COPY core /workspace/core");
    expect(dockerfile).toContain("go work init ./core ./plugin-sdk");
    expect(dockerfile).toContain("go work edit -go=1.26.0 go.work");
    expect(smoke).toContain("plugin-sdk");
    expect(workflow).toContain("repository: Liapoldus/plugin-sdk");
    expect(workflow).toContain("path: plugin-sdk");
    expect(workflow).toContain("go work init ./core ./plugin-sdk");
    expect(workflow).toContain("go work edit -go=1.26.0 go.work");
  });

  it("keeps the removed pluginprotocol module out of every build context", async () => {
    const dockerfile = await readFile(join(root, "Dockerfile"), "utf8");
    const smoke = await readFile(join(root, "scripts", "docker-smoke.sh"), "utf8");
    const workflow = await readFile(join(root, ".github", "workflows", "verify.yml"), "utf8");

    expect(dockerfile).not.toContain("pluginprotocol");
    expect(smoke).not.toContain("pluginprotocol");
    expect(workflow).not.toContain("pluginprotocol");
  });
});
