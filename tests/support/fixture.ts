import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

export async function createConfig(contents: string): Promise<string> {
  const directory = await createConfigDir();
  const path = join(directory, "gateway.yaml");
  await writeFile(path, contents, "utf8");
  return path;
}

export async function createConfigDir(): Promise<string> {
  return mkdtemp(join(tmpdir(), "liapoldus-gateway-test-"));
}
