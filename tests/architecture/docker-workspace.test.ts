import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

describe("Docker workspace dependency contract", () => {
  it("builds with the v1 pluginprotocol sibling module in context", async () => {
    const dockerfile = await readFile(join(root, "Dockerfile"), "utf8");
    const smoke = await readFile(join(root, "scripts", "docker-smoke.sh"), "utf8");
    const workflow = await readFile(join(root, ".github", "workflows", "verify.yml"), "utf8");

    expect(dockerfile).toContain("COPY pluginprotocol /workspace/pluginprotocol");
    expect(dockerfile).toContain("COPY core .");
    expect(smoke).toContain("pluginprotocol");
    expect(workflow).toContain("repository: Liapoldus/pluginprotocol");
    expect(workflow).toContain("path: pluginprotocol");
  });
});
