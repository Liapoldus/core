import { execFile } from "node:child_process";
import { mkdir, mkdtemp, readlink, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const execute = promisify(execFile);
const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

async function source(root: string, name: string, body: string): Promise<string> {
  const directory = join(root, name);
  await mkdir(directory, { recursive: true });
  await writeFile(join(directory, "site.yaml"), "slug: blog\n");
  await writeFile(join(directory, "index.html"), body);
  return directory;
}

async function probe(root: string, action: string, sourcePath?: string): Promise<Record<string, unknown>> {
  const args = ["run", "./tests/fixtures/registry-probe", root, "blog", action];
  if (sourcePath) args.push(sourcePath);
  const result = await execute("go", args, { cwd: coreRoot });
  return JSON.parse(result.stdout) as Record<string, unknown>;
}

describe("filesystem release registry", () => {
  it("publishes immutable releases, retains current and previous, and rolls them back", async () => {
    const root = await mkdtemp(join(tmpdir(), "liapoldus-registry-"));
    const first = await source(root, "source-one", "one");
    const second = await source(root, "source-two", "two");
    const releaseOne = await probe(root, "publish", first);
    const releaseTwo = await probe(root, "publish", second);
    expect(releaseOne.id).not.toBe(releaseTwo.id);
    expect(await readlink(join(root, "sites", "blog", "current"))).toContain(String(releaseTwo.id));
    expect(await readlink(join(root, "sites", "blog", "previous"))).toContain(String(releaseOne.id));
    const rolledBack = await probe(root, "rollback");
    expect(rolledBack.id).toBe(releaseOne.id);
    expect(await readlink(join(root, "sites", "blog", "current"))).toContain(String(releaseOne.id));
    expect(await readlink(join(root, "sites", "blog", "previous"))).toContain(String(releaseTwo.id));
  });

  it("rejects an invalid source without moving pointers or copying symlink escapes", async () => {
    const root = await mkdtemp(join(tmpdir(), "liapoldus-registry-"));
    const valid = await source(root, "valid", "safe");
    await probe(root, "publish", valid);
    const before = await readlink(join(root, "sites", "blog", "current"));
    const invalid = join(root, "invalid");
    await mkdir(invalid);
    await writeFile(join(invalid, "site.yaml"), "slug: blog\n");
    await symlink(join(root, "outside"), join(invalid, "escape"));
    await expect(probe(root, "publish", invalid)).rejects.toMatchObject({ code: 1 });
    expect(await readlink(join(root, "sites", "blog", "current"))).toBe(before);
  });
});
