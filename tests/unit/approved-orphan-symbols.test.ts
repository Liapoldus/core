import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const root = join(import.meta.dirname, "../..");

async function source(path: string): Promise<string> {
  return readFile(join(root, path), "utf8");
}

describe("approved orphan symbol cleanup", () => {
  it("removes only the five confirmed unreferenced symbols", async () => {
    const [api, client, cli, runtime] = await Promise.all([
      source("internal/presentation/api/adapter.go"),
      source("internal/infrastructure/plugins/protocol.go"),
      source("internal/presentation/cli/cli.go"),
      source("internal/infrastructure/plugins/runtime.go"),
    ]);

    expect(api).not.toMatch(/func redact\(/);
    expect(client).not.toMatch(/func \(c \*Client\) CallJSON\(/);
    expect(cli).not.toMatch(/func Execute\(/);
    expect(runtime).not.toMatch(/func \(r \*Runtime\) HTTPDispatchers\(/);
    expect(runtime).not.toMatch(/func \(r \*Runtime\) L4Dispatchers\(/);
  });
});
