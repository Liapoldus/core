import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

export async function createConfig(contents: string): Promise<string> {
  const directory = await mkdtemp(join(tmpdir(), "liapoldus-gateway-test-"));
  const path = join(directory, "gateway.yaml");
  await writeFile(path, contents, "utf8");
  return path;
}
