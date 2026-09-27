import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const apiRoot = join(root, "internal", "presentation", "api");
const groupHandlers = [
  "GroupList",
  "GroupCreate",
  "GroupGet",
  "GroupReleases",
  "GroupRelease",
  "GroupPublish",
  "GroupRollback",
];

describe("API group handler ownership", () => {
  it("places group request handlers and multipart helpers in api/handlers", async () => {
    const source = await readFile(join(apiRoot, "handlers", "groups.go"), "utf8").catch(() => "");
    const deps = await readFile(join(apiRoot, "handlers", "deps.go"), "utf8").catch(() => "");

    for (const handler of groupHandlers) {
      expect(source).toMatch(new RegExp(`^func ${handler}\\(`, "m"));
    }
    expect(source).toMatch(/^type groupReleaseMetadata struct \{/m);
    expect(source).toMatch(/^func readGroupReleaseMultipart\(/m);
    expect(source).toMatch(/^func groupResponse\(/m);
    expect(source).toMatch(/^func ascii\(/m);
    expect(deps).toMatch(/^type GroupDependencies struct \{/m);
  });

  it("routes group requests through the handlers package", async () => {
    const source = await readFile(join(apiRoot, "router.go"), "utf8");

    for (const handler of groupHandlers) {
      expect(source).toMatch(new RegExp(`handlers\\.${handler}\\(`));
    }
  });

  it("removes group request handling and group codec declarations from API root", async () => {
    const adapter = await readFile(join(apiRoot, "adapter.go"), "utf8").catch(() => "");
    const codec = await readFile(join(apiRoot, "codec.go"), "utf8");

    for (const method of [
      "handleGroupList",
      "handleGroupCreate",
      "handleGroupGet",
      "handleGroupReleases",
      "handleGroupRelease",
      "handleGroupPublish",
      "readGroupPublishRequest",
      "acceptGroupPublish",
      "writeGroupOperationAccepted",
      "handleGroupRollback",
      "readGroupRollbackRequest",
      "acceptGroupRollback",
    ]) {
      expect(adapter).not.toMatch(new RegExp(`^func \\(server \\*Server\\) ${method}\\(`, "m"));
    }
    expect(codec).not.toMatch(/^type groupReleaseMetadata struct \{/m);
    expect(codec).not.toMatch(/^func readGroupReleaseMultipart\(/m);
    expect(codec).not.toMatch(/^func \(server \*Server\) groupResponse\(/m);
    expect(codec).not.toMatch(/^func ascii\(/m);
  });
});
